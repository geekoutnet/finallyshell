package monitor

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

func (box dockerLiveBox) imageRows(t *testing.T) []ImageRow {
	t.Helper()
	rows, err := ParseImageList(box.raw(t, box.cmd.ImageList()))
	if err != nil || len(rows) == 0 {
		t.Fatalf("image list: %d, %v", len(rows), err)
	}
	return rows
}

func (box dockerLiveBox) imageInspect(t *testing.T, ref string) ImageInspect {
	t.Helper()
	data, err := ParseImageInspect(box.raw(t, dockerLiveScript(box.cmd.ImageInspect(ref))))
	if err != nil || len(data) != 1 || data[0].ID == "" {
		t.Fatalf("inspect %s: %+v, %v", ref, data, err)
	}
	return data[0]
}

func (box dockerLiveBox) imageProgress(t *testing.T, script string) string {
	t.Helper()
	var streamed strings.Builder
	out, err := RunImageProgress(box.ctx, box.client, script, func(chunk string) { streamed.WriteString(chunk) })
	if err != nil || streamed.Len() == 0 {
		t.Fatalf("image progress: %v: %s", err, out)
	}
	return streamed.String()
}

func TestDockerLiveImages(t *testing.T) {
	box := openDockerLiveBox(t)
	cmd, name := box.cmd, box.name
	const base = "alpine:3.20"
	baseImage := box.imageInspect(t, base)
	if baseImage.Platform() == "" || !slices.ContainsFunc(box.imageRows(t), func(row ImageRow) bool { return row.Ref == base && row.ID == baseImage.ID }) {
		t.Fatal("baseline image did not round-trip through the list and inspect")
	}
	t.Run("search-history-pull", func(t *testing.T) {
		history, err := ParseImageHistory(box.raw(t, dockerLiveScript(cmd.ImageHistory(base))))
		if err != nil || len(history) == 0 {
			t.Errorf("history: %+v, %v", history, err)
		}
		search := ParseImageSearch(box.raw(t, dockerLiveScript(cmd.ImageSearch("alpine"))))
		if !slices.Contains(search, "alpine") {
			t.Errorf("search: %+v", search)
		}
		box.imageProgress(t, dockerLiveScript(cmd.ImagePull(base, "", false)))
	})
	t.Run("tag-save-load", func(t *testing.T) {
		ref := name + ":saved"
		for _, gzip := range []bool{false, true} {
			box.raw(t, dockerLiveScript(cmd.ImageTag(base, ref)))
			file := box.dir + "/image.tar"
			if gzip {
				file += ".gz"
			}
			box.raw(t, dockerLiveScript(cmd.ImageSave(file, gzip, ref)))
			if gzip {
				box.raw(t, "gzip -t -- "+file)
			} else {
				box.raw(t, "tar -tf "+file+" | grep -qx manifest.json")
			}
			box.raw(t, dockerLiveScript(cmd.ImageRemove(false, ref)))
			box.raw(t, "! docker image inspect "+ref+" >/dev/null 2>&1")
			box.imageProgress(t, dockerLiveScript(cmd.ImageLoad(file)))
			got := box.imageInspect(t, ref)
			if got.ID != baseImage.ID {
				t.Fatalf("loaded %s, want %s", got.ID, baseImage.ID)
			}
			box.raw(t, dockerLiveScript(cmd.ImageGuard([]ImageRow{{Ref: ref, ID: got.ID}})))
			wrong := "sha256:" + strings.Repeat("f", 64)
			if _, err := RunDocker(box.ctx, box.client, dockerLiveScript(cmd.ImageGuard([]ImageRow{{Ref: ref, ID: wrong}}))); err == nil {
				t.Fatal("image reference identity guard accepted the wrong image")
			}
			box.raw(t, dockerLiveScript(cmd.ImageRemove(false, ref)))
		}
	})
	t.Run("build-prune", func(t *testing.T) {
		ref := name + ":build"
		var old string
		for _, revision := range []string{"first", "second"} {
			file := "FROM scratch\nLABEL fslive=" + name + " revision=" + revision + "\n"
			box.raw(t, "printf %s "+dockerQuote(file)+" > "+box.dir+"/Dockerfile")
			box.imageProgress(t, dockerLiveScript(cmd.ImageBuild(ImageBuild{Context: box.dir, Tags: []string{ref}, NoCache: true})))
			if revision == "first" {
				old = box.imageInspect(t, ref).ID
			}
		}
		rows, err := ParseImageList(box.raw(t, cmd.ImageDangling()))
		if err != nil || !slices.ContainsFunc(rows, func(row ImageRow) bool { return row.ID == old }) {
			t.Fatalf("old image not dangling: %d, %v", len(rows), err)
		}
		box.prune(t, cmd.ImagePrune(false))
		box.raw(t, "! docker image inspect "+old+" >/dev/null 2>&1")
		box.imageInspect(t, ref)
		box.prune(t, cmd.ImagePrune(true))
		box.raw(t, "! docker image inspect "+ref+" >/dev/null 2>&1")
		box.imageInspect(t, base)
	})
}

func TestDockerLiveRegistry(t *testing.T) {
	box := openDockerLiveBox(t)
	cmd, name := box.cmd, box.name
	const registryImage = "registry:2"
	if _, err := RunDocker(box.ctx, box.client, dockerLiveScript(cmd.ImageInspect(registryImage))); err != nil {
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			rm, _ := cmd.RemoveScript(true, true, name)
			_, _ = RunDocker(ctx, box.client, rm)
			remove, _ := cmd.ImageRemove(false, registryImage)
			_, _ = RunDocker(ctx, box.client, remove)
		})
		box.imageProgress(t, dockerLiveScript(cmd.ImagePull(registryImage, "", false)))
	}
	var seed [24]byte
	rand.Read(seed[:])
	secret := hex.EncodeToString(seed[:])
	box.raw(t, "mkdir -m 700 -- "+box.dir+"/auth "+box.dir+"/bin "+box.dir+"/.docker")
	makeHash := `umask 077
python3 -c 'import crypt,sys; print("fslive:" + crypt.crypt(sys.stdin.readline().strip(), crypt.mksalt(crypt.METHOD_BLOWFISH)))' > ` + box.dir + "/auth/htpasswd"
	if _, err := (DockerInput{input: secret + "\n", script: makeHash, private: true}).Run(box.ctx, box.client, nil); err != nil {
		t.Fatalf("generate htpasswd fixture: %v", err)
	}
	box.raw(t, dockerLiveScript(cmd.CtrRunScript(CtrConfig{
		Name: name, Image: registryImage, Labels: []string{"fslive=" + name},
		Env:    []string{"REGISTRY_AUTH=htpasswd", "REGISTRY_AUTH_HTPASSWD_REALM=FSLive", "REGISTRY_AUTH_HTPASSWD_PATH=/auth/htpasswd"},
		Mounts: []CtrMount{{Source: box.dir + "/auth", Target: "/auth", ReadOnly: true}},
		Ports:  []CtrPort{{IP: "127.0.0.1", Container: "5000", Protocol: "tcp"}},
	})))
	ct := box.container(t, name)
	if len(ct.Ports) != 1 || ct.Ports[0].HostPort == 0 {
		t.Fatal("registry did not publish a port")
	}
	registry := "127.0.0.1:" + strconv.Itoa(ct.Ports[0].HostPort)
	box.raw(t, "for attempt in 1 2 3 4 5; do [ \"$(curl -s -o /dev/null -w '%{http_code}' http://"+registry+"/v2/)\" = 401 ] && exit 0; sleep 1; done; exit 1")
	helper := `#!/bin/sh
set -eu
file="$HOME/.docker/fslive-auth"
umask 077
case "$1" in
  store) cat > "$file" ;;
  get) cat "$file" ;;
  erase) rm -f -- "$file" ;;
  list) if [ -f "$file" ]; then jq '{(.ServerURL): .Username}' "$file"; else echo '{}'; fi ;;
  *) exit 1 ;;
esac
`
	box.raw(t, "printf %s "+dockerQuote(helper)+" > "+box.dir+"/bin/docker-credential-fslive; chmod 700 "+box.dir+"/bin/docker-credential-fslive; printf '%s\\n' '{\"credsStore\":\"fslive\"}' > "+box.dir+"/.docker/config.json")
	env := "export HOME=" + box.dir + " PATH=" + box.dir + "/bin:$PATH\n"
	input, err := cmd.RegistryLogin(registry, "fslive", secret)
	if err != nil {
		t.Fatal(err)
	}
	input.script = env + input.Script()
	if out, err := input.Run(box.ctx, box.client, nil); err != nil || out != "" {
		t.Fatalf("registry login failed: %v", err)
	}
	listed := func() DockerRegistries {
		t.Helper()
		data, err := ParseDockerRegistries(box.raw(t, env+cmd.RegistryList()))
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	if data := listed(); data.Store != "fslive" || !slices.Contains(data.Registries, registry) {
		t.Fatal("registry login not visible through credential helper")
	}
	ref := registry + "/" + name + ":pushed"
	box.raw(t, dockerLiveScript(cmd.ImageTag("alpine:3.20", ref)))
	box.imageProgress(t, env+dockerLiveScript(cmd.ImagePush(ref)))
	box.raw(t, dockerLiveScript(cmd.ImageRemove(false, ref)))
	box.imageProgress(t, env+dockerLiveScript(cmd.ImagePull(ref, "", false)))
	box.imageInspect(t, ref)
	box.raw(t, env+dockerLiveScript(cmd.RegistryLogout(registry)))
	if slices.Contains(listed().Registries, registry) {
		t.Fatal("registry still listed after logout")
	}
	if _, err := RunDocker(box.ctx, box.client, env+dockerLiveScript(cmd.ImagePush(ref))); err == nil {
		t.Fatal("push accepted after logout")
	}
}
