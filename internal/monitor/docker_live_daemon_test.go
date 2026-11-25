package monitor

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestDockerLiveDaemon(t *testing.T) {
	if os.Getenv("FINALLYSHELL_TEST_DOCKER_RESTART") != "1" {
		t.Skip("requires explicit opt-in to restart the testbox Docker daemon")
	}
	box := openDockerLiveBox(t)
	cmd := box.cmd
	if names := box.raw(t, "docker ps -a --format '{{.Names}}' | grep '^fslive' || true"); names != "" {
		t.Fatalf("another Docker test is active: %s", names)
	}
	read := func() string {
		t.Helper()
		data, err := RunDocker(box.ctx, box.client, cmd.DaemonRead())
		if err != nil {
			t.Fatalf("read daemon config: %v: %s", err, data)
		}
		return data
	}
	original := read()
	running := strings.Fields(box.raw(t, "docker ps -q --no-trunc"))
	if len(running) == 0 {
		t.Fatal("expected pre-existing running containers")
	}
	backup := box.dir + "/daemon.before"
	existed := box.raw(t, "if [ -f "+daemonPath+" ]; then cp -p -- "+daemonPath+" "+backup+" && echo present; fi") == "present"
	if existed {
		sha := fmt.Sprintf("%x", sha256.Sum256([]byte(original)))
		box.raw(t, "test \"$(sha256sum < "+backup+" | cut -d ' ' -f1)\" = "+sha)
	}
	const mirror = "https://fslive.invalid"
	data := original
	if strings.TrimSpace(data) == "" {
		data = "{}\n"
	}
	mirrors, err := DaemonMirrors(data)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := WithDaemonMirrors(data, append(mirrors, mirror))
	if err != nil {
		t.Fatal(err)
	}
	when := time.Now().UTC()
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(updated)))
	startRunning := "for id in " + strings.Join(running, " ") + `; do
  [ "$(docker inspect --format '{{.State.Running}}' "$id")" = true ] || docker start "$id" || exit 1
done`
	changed := false
	t.Cleanup(func() {
		if !changed {
			return
		}
		restore := "rm -- " + daemonPath
		if existed {
			restore = "cp -p -- " + backup + " " + daemonPath
		}
		script := "set -e\n[ \"$(sha256sum < " + daemonPath + " | cut -d ' ' -f1)\" = " + digest + " ]\n" + restore + "\nsystemctl restart docker.service\n" + startRunning + "\nsystemctl is-active docker.service\n"
		if existed {
			script += "cmp -- " + daemonPath + " " + backup + "\nrm -f -- " + daemonPath + ".bak-" + when.Format("20060102150405") + "\n"
		} else {
			script += "test ! -e " + daemonPath + "\n"
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if out, err := RunDocker(ctx, box.client, script); err != nil {
			t.Errorf("restore daemon and baseline containers: %v: %s", err, out)
		}
	})
	input, err := cmd.DaemonSave(original, updated, when)
	if err != nil {
		t.Fatal(err)
	}
	if out, err := input.Run(box.ctx, box.client, nil); err != nil {
		engine := box.raw(t, cmd.EngineVersion())
		if !strings.Contains(engine, "20.10.") || !strings.Contains(out, "Could not validate the configuration") || read() != original || existed {
			t.Fatalf("daemon save: %v: %s", err, out)
		}
		t.Log("20.10 rejects save safely: dockerd does not support --validate; staging a synthetic config for restart only")
		box.raw(t, "(umask 077; set -C; printf %s "+dockerQuote(updated)+" > "+daemonPath+")")
	}
	changed = true
	if saved, err := DaemonMirrors(read()); err != nil || !slices.Contains(saved, mirror) {
		t.Fatalf("read back mirrors: %v", err)
	}
	box.raw(t, dockerLiveScript(cmd.DaemonRestart(updated)))
	box.raw(t, startRunning)
	engine, err := ParseDockerEngineInfo(box.raw(t, cmd.EngineInfo()), box.raw(t, cmd.EngineVersion()))
	if err != nil || !slices.Contains(engine.Mirrors, mirror) {
		t.Fatalf("running engine did not load mirror: %v, %v", engine.Mirrors, err)
	}
}
