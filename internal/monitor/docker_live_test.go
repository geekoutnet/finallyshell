package monitor

import (
	"context"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

type dockerLiveBox struct {
	ctx    context.Context
	client *ssh.Client
	cmd    DockerCmd
	name   string
	dir    string
}

func openDockerLiveBox(t *testing.T) dockerLiveBox {
	t.Helper()
	spec := os.Getenv("FINALLYSHELL_TEST_SSH")
	if os.Getenv("FINALLYSHELL_TEST_MUTATE") != "1" || spec[strings.LastIndexByte(spec, '@')+1:] != "127.0.0.1:2222" {
		t.Skip("requires mutation opt-in and the local testbox")
	}
	client := dialTestServer(t)
	if client.User() != "root" {
		t.Skip("requires root on the testbox")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Minute)
	t.Cleanup(cancel)
	box := dockerLiveBox{ctx: ctx, client: client, cmd: DockerCmd{Tool: "docker"}, name: fmt.Sprintf("fslive%x", time.Now().UnixNano())}
	if box.raw(t, "hostname") != "fs-testbox" {
		t.Fatal("not the local testbox")
	}
	box.dir = "/tmp/" + box.name
	box.raw(t, "mkdir -m 700 -- "+dockerQuote(box.dir))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		script := `set -e
name=` + dockerQuote(box.name) + `
for id in $(docker ps -aq --filter "label=fslive=$name"); do docker rm -fv "$id"; done
for ref in $(docker image ls --format '{{.Repository}}:{{.Tag}}'); do
  case "$ref" in "$name"*|127.0.0.1:*/"$name"*) docker image rm "$ref" ;; esac
done
docker image prune -af --filter "label=fslive=$name"
docker network prune -f --filter "label=fslive=$name"
docker volume prune -f --filter "label=fslive=$name"
rm -r -- ` + dockerQuote(box.dir)
		if out, err := RunDocker(ctx, client, scriptHead+script); err != nil {
			t.Errorf("cleanup: %v: %s", err, out)
		}
	})
	return box
}

func dockerLiveScript(script string, err error) string {
	if err != nil {
		panic(err)
	}
	return script
}

func (box dockerLiveBox) raw(t *testing.T, script string) string {
	t.Helper()
	out, err := RunDocker(box.ctx, box.client, script)
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	return strings.TrimSpace(out)
}

func (box dockerLiveBox) container(t *testing.T, name string) Container {
	t.Helper()
	data, err := ReadDocker(box.ctx, box.client)
	if err != nil || data.State != DockerOK {
		t.Fatalf("Docker read: %v, %v", data.State, err)
	}
	index := slices.IndexFunc(data.Containers, func(ct Container) bool { return ct.Name == name })
	if index < 0 {
		t.Fatalf("container %s not found", name)
	}
	return data.Containers[index]
}

func (box dockerLiveBox) contains(t *testing.T, script, want string) {
	t.Helper()
	if out := box.raw(t, script); !strings.Contains(out, want) {
		t.Fatalf("expected %q in %q", want, out)
	}
}

func TestDockerLiveContainers(t *testing.T) {
	box := openDockerLiveBox(t)
	cmd, name := box.cmd, box.name
	net := name + "net"
	box.raw(t, dockerLiveScript(cmd.NetworkCreate(DockerNetworkSpec{Name: net, Driver: "bridge", Labels: "fslive=" + name,
		IPv4: DockerSubnet{Subnet: "10.243.219.0/24", Gateway: "10.243.219.1"}})))
	box.raw(t, "printf live-file > "+box.dir+"/source")
	form := CtrConfig{
		Name: name, Image: "alpine:3.20", Network: net, IPv4: "10.243.219.12", Restart: "unless-stopped", Aliases: []string{"fslive-alias"},
		Ports: []CtrPort{{IP: "127.0.0.1", Host: "", Container: "8080", Protocol: "tcp"}},
		Env:   []string{"FSLIVE_VALUE=a b"}, Labels: []string{"fslive=" + name},
		Mounts:  []CtrMount{{Source: box.dir, Target: "/fixture", ReadOnly: true}},
		Command: []string{"sh", "-c", "echo fslive-log; trap 'exit 0' TERM; while :; do sleep 1; done"},
	}
	box.raw(t, dockerLiveScript(cmd.CtrRunScript(form)))
	ct := box.container(t, name)
	assertSettings := func(ct Container, env string) {
		t.Helper()
		if !ct.Running || ct.Labels["fslive"] != name || !slices.Contains(ct.Env, env) || len(ct.Ports) != 1 || ct.Ports[0].HostIP != "127.0.0.1" ||
			ct.Ports[0].Port != 8080 || ct.Ports[0].HostPort == 0 || len(ct.Mounts) != 1 || ct.Mounts[0].Source != box.dir || ct.Mounts[0].RW ||
			len(ct.Networks) != 1 || ct.Networks[0].Name != net || ct.Networks[0].IP != form.IPv4 ||
			!slices.Contains(ct.Networks[0].Aliases, "fslive-alias") || ct.RestartPolicy != "unless-stopped" {
			t.Fatalf("container settings not retained: %+v", ct)
		}
	}
	assertSettings(ct, "FSLIVE_VALUE=a b")
	t.Run("logs-shell-stats", func(t *testing.T) {
		box.contains(t, dockerLiveScript(cmd.LogsScript(ct.ID, 20)), "fslive-log")
		shell := dockerLiveScript(cmd.ExecShell(ct.ID))
		session, err := box.client.NewSession()
		if err != nil {
			t.Fatal(err)
		}
		defer session.Close()
		ctx, cancel := context.WithTimeout(box.ctx, 10*time.Second)
		defer cancel()
		stop := context.AfterFunc(ctx, func() { session.Close() })
		defer stop()
		if err := session.RequestPty("dumb", 24, 80, ssh.TerminalModes{ssh.ECHO: 0}); err != nil {
			t.Fatal(err)
		}
		stdin, err := session.StdinPipe()
		if err != nil {
			t.Fatal(err)
		}
		sent := make(chan error, 1)
		go func() {
			_, err := io.WriteString(stdin, "printf '%s%s\\n' fslive- shell\nexit\n")
			sent <- err
		}()
		if out, err := session.CombinedOutput(shell); err != nil || !strings.Contains(string(out), "fslive-shell") {
			t.Fatalf("interactive exec: %v: %s", err, out)
		}
		if err := <-sent; err != nil {
			t.Fatal(err)
		}
		stats := ParseCtrStats([]Container{ct}, box.raw(t, dockerLiveScript(cmd.CtrStatsScript(ct.ID))))
		if stats[ct.ID].PIDs == 0 || stats[ct.ID].MemLimit == 0 {
			t.Fatalf("stats: %+v", stats)
		}
	})
	t.Run("files", func(t *testing.T) {
		const dir = "/tmp/fslive"
		file := func(op CtrFileOp, first, second string) {
			t.Helper()
			box.raw(t, dockerLiveScript(cmd.CtrFileScript(op, ct.ID, first, second)))
		}
		file(CtrFileMkdir, dir, "")
		file(CtrFileFromServer, dir+"/a b", box.dir+"/source")
		rows, err := ParseCtrFiles(box.raw(t, dockerLiveScript(cmd.CtrListScript(ct.ID, dir))))
		if err != nil || len(rows) != 1 || rows[0].Name != "a b" || rows[0].Size != 9 {
			t.Fatalf("file list: %+v, %v", rows, err)
		}
		file(CtrFileFromServer, dir+"/occupied", box.dir+"/source")
		for _, script := range []string{
			dockerLiveScript(cmd.CtrFileScript(CtrFileRename, ct.ID, dir+"/a b", dir+"/occupied")),
			dockerLiveScript(cmd.CtrFileScript(CtrFileFromServer, ct.ID, dir+"/occupied", box.dir+"/source")),
			dockerLiveScript(cmd.CtrFileScript(CtrFileToServer, ct.ID, dir+"/occupied", box.dir+"/source")),
		} {
			if _, err := RunDocker(box.ctx, box.client, script); err == nil {
				t.Error("file operation reported success with an occupied destination")
			}
		}
		file(CtrFileDelete, dir+"/occupied", "")
		file(CtrFileRename, dir+"/a b", dir+"/renamed")
		file(CtrFileToServer, dir+"/renamed", box.dir+"/copied")
		box.contains(t, "cat "+box.dir+"/copied", "live-file")
		file(CtrFileDelete, dir+"/renamed", "")
		rows, err = ParseCtrFiles(box.raw(t, dockerLiveScript(cmd.CtrListScript(ct.ID, dir))))
		if err != nil || len(rows) != 0 {
			t.Fatalf("deleted file still listed: %+v, %v", rows, err)
		}
		box.raw(t, cmd.ctrExec(ct.ID, "ln", "-s", "target with spaces", dir+"/fslive-link"))
		rows, err = ParseCtrFiles(box.raw(t, dockerLiveScript(cmd.CtrListScript(ct.ID, dir))))
		if err != nil || len(rows) != 1 || rows[0].Name != "fslive-link" {
			t.Fatalf("symlink file list: %+v, %v", rows, err)
		}
		box.raw(t, cmd.ctrExec(ct.ID, "touch", dir+"/unsafe\nname"))
		if _, err := RunDocker(box.ctx, box.client, dockerLiveScript(cmd.CtrListScript(ct.ID, dir))); err == nil {
			t.Fatal("lossy BusyBox listing accepted a newline in a filename")
		}
	})
	t.Run("lifecycle-batch", func(t *testing.T) {
		second := form
		second.Name, second.Ports, second.IPv4 = name+"batch", nil, ""
		box.raw(t, dockerLiveScript(cmd.CtrRunScript(second)))
		other := box.container(t, second.Name)
		for _, step := range []struct {
			op     CtrOp
			status string
		}{{OpPause, "paused"}, {OpUnpause, "running"}, {OpStop, "exited"}, {OpStart, "running"}, {OpRestart, "running"}, {OpStop, "exited"}} {
			box.raw(t, dockerLiveScript(cmd.OpScript(step.op, ct.ID, other.ID)))
			if box.container(t, name).Status != step.status || box.container(t, second.Name).Status != step.status {
				t.Fatalf("%s did not reach %s", step.op, step.status)
			}
		}
		box.raw(t, dockerLiveScript(cmd.OpScript(OpStart, ct.ID)))
		box.raw(t, dockerLiveScript(cmd.RestartPolicyScript(ct.ID, "no", 0)))
		box.raw(t, dockerLiveScript(cmd.OpScript(OpKill, ct.ID)))
		if stopped := box.container(t, name); stopped.ExitCode != 137 || stopped.RestartPolicy != "no" {
			t.Fatalf("kill/restart policy: %d / %s", stopped.ExitCode, stopped.RestartPolicy)
		}
		box.raw(t, dockerLiveScript(cmd.RestartPolicyScript(ct.ID, form.Restart, 0)))
		box.raw(t, dockerLiveScript(cmd.OpScript(OpStart, ct.ID)))
		box.raw(t, dockerLiveScript(cmd.RenameScript(other.ID, name+"renamed")))
		if box.container(t, name+"renamed").ID != other.ID {
			t.Fatal("renamed container has a different ID")
		}
		box.raw(t, dockerLiveScript(cmd.RemoveScript(false, true, other.ID)))
		anonymous := box.raw(t, cmd.Line("create", "--name", name+"anonymous", "--label", "fslive="+name, "--volume", "/data", "alpine:3.20"))
		mounted := box.container(t, name+"anonymous")
		if len(mounted.Mounts) != 1 || mounted.Mounts[0].Name == "" {
			t.Fatal("anonymous volume fixture unavailable")
		}
		box.raw(t, dockerLiveScript(cmd.RemoveScript(false, true, anonymous)))
		box.raw(t, "! docker volume inspect "+dockerQuote(mounted.Mounts[0].Name)+" >/dev/null 2>&1")
	})
	t.Run("commit-export-edit-recreate", func(t *testing.T) {
		box.raw(t, dockerLiveScript(cmd.CtrCommitScript(ct.ID, name+":commit", "live", "tester", true)))
		box.raw(t, dockerLiveScript(cmd.CtrExportScript(ct.ID, box.dir+"/rootfs.tar")))
		box.raw(t, "tar -tf "+box.dir+"/rootfs.tar | grep -qx 'bin/sh'")
		box.raw(t, dockerLiveScript(cmd.ImageImport(box.dir+"/rootfs.tar", name+":imported")))
		ct = box.container(t, name)
		box.raw(t, dockerLiveScript(cmd.RecreateScript(&ct, time.Now())))
		recreated := box.container(t, name)
		assertSettings(recreated, "FSLIVE_VALUE=a b")
		if recreated.ID == ct.ID {
			t.Fatal("recreate did not replace the container")
		}
		ct = recreated
		edited, err := CtrConfigOf(&ct)
		if err != nil {
			t.Fatal(err)
		}
		edited.Env = []string{"FSLIVE_VALUE=edited"}
		box.raw(t, dockerLiveScript(cmd.CtrEditScript(&ct, edited, time.Now())))
		replacement := box.container(t, name)
		assertSettings(replacement, "FSLIVE_VALUE=edited")
		if replacement.ID == ct.ID {
			t.Fatal("edit did not replace the container")
		}
	})
}
