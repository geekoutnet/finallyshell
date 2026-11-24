package monitor

import (
	"errors"
	"strings"
	"testing"
)

func TestDockerImageCommands(t *testing.T) {
	cmd := DockerCmd{Tool: "docker", Sudo: true}
	tests := []struct {
		name string
		make func() (string, error)
		line string
	}{
		{"pull", func() (string, error) {
			return cmd.ImagePull("registry.example:5000/team/demo:v2", "linux/arm64/v8", false)
		}, `sudo -n docker 'image' 'pull' '--platform' 'linux/arm64/v8' '--' 'registry.example:5000/team/demo:v2'`},
		{"all tags", func() (string, error) { return cmd.ImagePull("demo", "", true) }, `sudo -n docker 'image' 'pull' '--all-tags' '--' 'demo'`},
		{"tag", func() (string, error) { return cmd.ImageTag("abcdef123456", "team/demo:test") }, `sudo -n docker 'image' 'tag' '--' 'abcdef123456' 'team/demo:test'`},
		{"push", func() (string, error) { return cmd.ImagePush("team/demo:test") }, `sudo -n docker 'image' 'push' '--' 'team/demo:test'`},
		{"remove", func() (string, error) { return cmd.ImageRemove(true, "team/demo:test", "abcdef123456") }, `sudo -n docker 'image' 'rm' '--no-prune' '--force' '--' 'team/demo:test' 'abcdef123456'`},
		{"load", func() (string, error) { return cmd.ImageLoad("/tmp/demo's.tar.gz") }, `sudo -n docker 'image' 'load' '-i' '/tmp/demo'\''s.tar.gz'`},
		{"save", func() (string, error) { return cmd.ImageSave("/tmp/demo's.tar", false, "team/demo:test") }, `ln -T -- "$temp/archive.tar" '/tmp/demo'\''s.tar'`},
		{"gzip", func() (string, error) { return cmd.ImageSave("/tmp/demo's.tar.gz", true, "team/demo:test") }, `gzip -c "$temp/archive.tar" > "$temp/archive.tar.gz"`},
		{"build", func() (string, error) {
			return cmd.ImageBuild(ImageBuild{Context: "/srv/my app", Tags: []string{"demo:test"}, Args: []string{"TITLE=it's $5"}})
		}, `sudo -n docker 'build' '--progress=plain' '-f' '/srv/my app/Dockerfile' '-t' 'demo:test' '--build-arg' 'TITLE=it'\''s $5' '--' '/srv/my app'`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			script, err := test.make()
			if err != nil || !strings.Contains(script, test.line+"\n") {
				t.Fatalf("got %q, %v; want line %q", script, err, test.line)
			}
		})
	}
	if got := cmd.ImagePrune(true); got != scriptHead+"sudo -n docker 'image' 'prune' '--force' '--all'\n" {
		t.Fatal(got)
	}
	for _, bad := range []string{"--help", "a;id", "https://registry.example/a:b", "Repo:tag", "repo:bad tag", "foo//bar:x", "registry:65536/app:tag", "foo:tag@anything", "repo\n", "repo:", "repo@sha256:abcd"} {
		if ValidImageReference(bad) {
			t.Errorf("accepted %q", bad)
		}
		if _, err := cmd.ImageTag("abcdef123456", bad); err == nil {
			t.Errorf("accepted target %q", bad)
		}
	}
	if _, err := cmd.ImagePull("demo:tag", "", true); err == nil {
		t.Fatal("accepted image tag with --all-tags")
	}
	if _, err := cmd.ImageBuild(ImageBuild{Context: "/srv/app", Args: []string{"--flag"}}); err == nil {
		t.Fatal("accepted a build argument without a value")
	}
	if _, err := cmd.ImageSave("--output", false, "demo:test"); err == nil {
		t.Fatal("accepted non-absolute destination")
	}
	if got := cmd.ImageList(); got != scriptHead+"sudo -n docker 'image' 'ls' '-a' '--no-trunc' '--format' '{{json .}}'\n" {
		t.Fatal(got)
	}
	if got := cmd.ImageDangling(); !strings.Contains(got, "'--filter' 'dangling=true'\n") {
		t.Fatal(got)
	}
	podman := DockerCmd{Tool: "podman"}
	if got := podman.ImageList(); !strings.Contains(got, "podman 'image' 'ls' '-a' '--no-trunc' '--format' 'json'\n") {
		t.Fatal(got)
	}
	if _, err := podman.ImageRemove(true, "demo:test"); !errors.Is(err, ErrImageForce) {
		t.Fatalf("podman force: %v", err)
	}
	guard, err := cmd.ImageGuard([]ImageRow{{ID: "abcdef123456", Ref: "demo:test"}})
	if err != nil || !strings.Contains(guard, `case "${actual#sha256:}" in 'abcdef123456'*`) || !strings.Contains(guard, "'--' 'demo:test') || exit $?\n") {
		t.Fatalf("guard: %q, %v", guard, err)
	}
}

func TestDockerImageParsers(t *testing.T) {
	id := strings.Repeat("a", 64)
	list := `{"Repository":"library/demo","Tag":"bookworm","ID":"sha256:` + id + `","CreatedAt":"2026-01-04 13:42:00 +0000 UTC","Size":"51.3MB"}
{"Repository":"<none>","Tag":"<none>","ID":"bbbbbbbbbbbb","CreatedAt":"2026-01-01 08:00:00 +0800 CST","Size":"4.4MB"}
{"Repository":"registry.example/demo","Tag":"<none>","ID":"sha256:` + id + `","Size":"51.3MB"}
{"Repository":"library/demo","Tag":"stable","ID":"sha256:` + id + `","Size":"51.3MB"}`
	rows, err := ParseImageList(list)
	if err != nil || len(rows) != 3 || rows[0].Ref != "library/demo:bookworm" || rows[0].Size != 51300000 || rows[0].Created.Year() != 2026 || !rows[1].Untagged || rows[1].Ref != "bbbbbbbbbbbb" {
		t.Fatalf("list: %+v, %v", rows, err)
	}
	rows, err = ParseImageList(`[{"Id":"` + id + `","Names":["localhost/demo:one","localhost/demo:two"],"Size":12345,"Created":1767488400}]`)
	if err != nil || len(rows) != 2 || rows[1].Ref != "localhost/demo:two" || rows[0].Size != 12345 {
		t.Fatalf("podman list: %+v, %v", rows, err)
	}
	if _, err := ParseImageList(list + "\n{\"ID\":"); err == nil {
		t.Fatal("accepted truncated list")
	}
	inspect, err := ParseImageInspect(`[{
		"Id":"sha256:` + id + `","RepoTags":["demo:latest"],"RepoDigests":["demo@sha256:` + id + `"],
		"Created":"2026-01-01T00:00:00.123Z","Architecture":"amd64","Os":"linux","Size":123,
		"RootFS":{"Type":"layers","Layers":["sha256:` + id + `"]},
		"Config":{"Env":["TZ=UTC"],"Cmd":["demo"],"ExposedPorts":{"80/tcp":{}},"Labels":{"example":"test"}}
	}]`)
	if err != nil || len(inspect) != 1 || inspect[0].Platform() != "linux/amd64" || len(inspect[0].RootFS.Layers) != 1 || inspect[0].Config.Env[0] != "TZ=UTC" || len(inspect[0].Raw) == 0 {
		t.Fatalf("inspect: %+v, %v", inspect, err)
	}
	history, err := ParseImageHistory(`{"ID":"<missing>","CreatedBy":"/bin/sh -c #(nop)  CMD [\"demo\"]","CreatedAt":"2026-01-04 13:42:00 +0000 UTC","Size":"7B","Comment":"buildkit.dockerfile.v0"}`)
	if err != nil || len(history) != 1 || history[0].Size != 7 || history[0].Comment != "buildkit.dockerfile.v0" {
		t.Fatalf("history: %+v, %v", history, err)
	}
}

func TestDockerImageProgressTail(t *testing.T) {
	var seen int
	stream := &imageStream{progress: func(chunk string) { seen += len(chunk) }}
	const limit = 64 << 10
	first := strings.Repeat("a", limit+230)
	second := strings.Repeat("b", 51) + "\nTotal reclaimed space: 4MB\n"
	for _, chunk := range []string{first, second} {
		if count, err := stream.Write([]byte(chunk)); err != nil || count != len(chunk) {
			t.Fatalf("stream wrote %d, %v", count, err)
		}
	}
	if seen != len(first)+len(second) || len(stream.tail) != limit || !strings.HasSuffix(string(stream.tail), second) {
		t.Fatal("progress lost bytes or the engine summary")
	}
}
