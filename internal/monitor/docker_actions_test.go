package monitor

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestDockerRunCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if output, err := RunDocker(ctx, nil, ""); output != "" || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled run: %q, %v", output, err)
	}
}

func TestDockerCommandLines(t *testing.T) {
	docker, sudo, podman := DockerCmd{Tool: "docker"}, DockerCmd{Tool: "docker", Sudo: true}, DockerCmd{Tool: "podman"}
	id := "4f2a9c0d1e3b4f2a9c0d1e3b4f2a9c0d1e3b4f2a9c0d1e3b4f2a9c0d1e3b4f2a"
	must := func(s string, err error) string {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimPrefix(s, scriptHead)
	}
	for _, tc := range []struct{ got, want string }{
		{must(docker.OpScript(OpRestart, id)), "docker 'restart' '" + id + "'\n"},
		{must(sudo.OpScript(OpStop, "web", "db")), "sudo -n docker 'stop' 'web' 'db'\n"},
		{must(podman.OpScript(OpPause, "web")), "podman 'pause' 'web'\n"},
		{must(docker.OpScript(OpKill, "web")), "docker 'kill' 'web'\n"},
		{must(docker.RemoveScript(false, false, "web")), "docker 'rm' 'web'\n"},
		{must(sudo.RemoveScript(true, true, "web")), "sudo -n docker 'rm' '-f' '-v' 'web'\n"},
		{must(docker.RenameScript("web", "web.2")), "docker 'rename' 'web' 'web.2'\n"},
		{must(docker.RestartPolicyScript("web", "on-failure", 5)), "docker 'update' '--restart' 'on-failure:5' 'web'\n"},
		{must(docker.RestartPolicyScript("web", "on-failure", 0)), "docker 'update' '--restart' 'on-failure' 'web'\n"},
		{must(docker.RestartPolicyScript("web", "unless-stopped", 3)), "docker 'update' '--restart' 'unless-stopped' 'web'\n"},
		{must(sudo.LogsScript("web", 500)), "sudo -n docker 'logs' '--tail' '500' '--timestamps' 'web'\n"},
		{must(docker.ExecShell("web")), "docker 'exec' '-it' 'web' 'sh' '-c' 'command -v bash >/dev/null && exec bash || exec sh'"},
		{docker.Line("run", "it's", "a b", "$HOME", ""), `docker 'run' 'it'\''s' 'a b' '$HOME' ''`},
	} {
		if tc.got != tc.want {
			t.Errorf("got %q, want %q", tc.got, tc.want)
		}
	}
	if s, _ := docker.OpScript(OpStart, "web"); !strings.HasPrefix(s, "export LC_ALL=C\nexec 2>&1\n") {
		t.Errorf("no head: %q", s)
	}
	// Nothing that is not a container's name gets to the command.
	for _, bad := range []string{"", "-f", "web; rm -rf /", "a b", "$(id)", "web\n", "../x", "_x"} {
		if _, err := docker.OpScript(OpStop, bad); err == nil {
			t.Errorf("OpScript took %q", bad)
		}
		if _, err := docker.RenameScript("web", bad); err == nil {
			t.Errorf("RenameScript took %q", bad)
		}
	}
	if _, err := docker.OpScript("rm", "web"); err == nil {
		t.Error("OpScript took rm")
	}
	if _, err := docker.RestartPolicyScript("web", "sometimes", 0); err == nil {
		t.Error("a restart policy that is none")
	}
}

func TestDockerComposeScripts(t *testing.T) {
	p := ComposeOf(&Container{Project: "shop", ComposeDir: "/srv/my shop", ComposeFiles: "/srv/my shop/compose.yaml,/srv/my shop/compose.prod.yaml"})
	if want := (Compose{Project: "shop", Dir: "/srv/my shop", Files: []string{"/srv/my shop/compose.yaml", "/srv/my shop/compose.prod.yaml"}}); !reflect.DeepEqual(p, want) {
		t.Fatalf("ComposeOf = %+v", p)
	}
	head := scriptHead + `cd '/srv/my shop' || exit 1
if docker 'compose' 'version' >/dev/null 2>&1; then c() { docker 'compose' "$@"; }
elif docker-compose 'version' >/dev/null 2>&1; then c() { docker-compose "$@"; }
else echo "Neither 'docker compose' nor docker-compose is installed."; exit 127; fi
`
	files := `c '-p' 'shop' '-f' '/srv/my shop/compose.yaml' '-f' '/srv/my shop/compose.prod.yaml'`
	got, err := DockerCmd{Tool: "docker"}.ComposeUpdateScript(p, "web")
	if want := head + files + " 'pull' 'web' && " + files + " 'up' '-d' '--force-recreate' '--no-deps' 'web'\n"; err != nil || got != want {
		t.Errorf("update a service:\n%s\nwant\n%s (%v)", got, want, err)
	}
	got, _ = DockerCmd{Tool: "docker"}.ComposeUpdateScript(p, "")
	if want := head + files + " 'pull' && " + files + " 'up' '-d' '--force-recreate'\n"; got != want {
		t.Errorf("update the project:\n%s\nwant\n%s", got, want)
	}
	got, _ = DockerCmd{Tool: "docker"}.ComposeDownScript(p)
	if want := head + files + " 'down'\n"; got != want {
		t.Errorf("down:\n%s", got)
	}
	got, _ = DockerCmd{Tool: "docker"}.ComposeLogsScript(p, 200)
	if want := head + files + " 'logs' '--no-color' '--timestamps' '--tail' '200'\n"; got != want {
		t.Errorf("logs:\n%s", got)
	}
	// With sudo, and Podman's.
	got, _ = DockerCmd{Tool: "docker", Sudo: true}.ComposeDownScript(p)
	if !strings.Contains(got, `elif sudo -n docker-compose 'version' >/dev/null 2>&1; then c() { sudo -n docker-compose "$@"; }`) {
		t.Errorf("sudo:\n%s", got)
	}
	got, _ = DockerCmd{Tool: "podman"}.ComposeDownScript(p)
	if !strings.Contains(got, "if podman 'compose' 'version'") || !strings.Contains(got, `c() { podman-compose "$@"; }`) {
		t.Errorf("podman:\n%s", got)
	}
	// A project without files is not pulled; names that are not are
	// refused.
	if _, err := (DockerCmd{Tool: "docker"}).ComposeUpdateScript(Compose{Project: "shop"}, ""); err == nil {
		t.Error("updated a project without files")
	}
	if _, err := (DockerCmd{Tool: "docker"}).ComposeUpdateScript(p, "web;id"); err == nil {
		t.Error("took a bad service")
	}
	if _, err := (DockerCmd{Tool: "docker"}).ComposeDownScript(Compose{Project: "$(id)"}); err == nil {
		t.Error("took a bad project")
	}
	if _, err := (DockerCmd{Tool: "docker"}).ComposeDownScript(Compose{Project: "shop", Files: []string{"compose.yaml"}}); err == nil {
		t.Error("down without a working directory")
	}
	checkSyntax(t, head+files+" 'pull' 'web' && "+files+" 'up' '-d'\n")
}

// recreateSample is a container with an anonymous volume, a named one
// and a bind, on two networks, as docker inspect writes it.
const recreateSample = `{"Id":"8a1f3c5e7b9d8a1f3c5e7b9d8a1f3c5e7b9d8a1f3c5e7b9d8a1f3c5e7b9d0001","Name":"/gitea",
"State":{"Status":"running","Running":true},
"Config":{"Hostname":"8a1f3c5e7b9d","Image":"gitea/gitea:1.22","Env":["USER_UID=1000","Q=it's \"$x\""],"Labels":{"a":"b"}},
"HostConfig":{"Binds":["gitea-data:/data:rw","/etc/timezone:/etc/timezone:ro"],"NetworkMode":"gitea_net","RestartPolicy":{"Name":"always","MaximumRetryCount":0},"PortBindings":{"3000/tcp":[{"HostIp":"","HostPort":"3000"}]}},
"Mounts":[{"Type":"volume","Name":"gitea-data","Destination":"/data"},{"Type":"bind","Source":"/etc/timezone","Destination":"/etc/timezone"},{"Type":"volume","Name":"0d2c4e6f","Destination":"/var/cache"}],
"NetworkSettings":{"Networks":{
 "gitea_net":{"IPAMConfig":{"IPv4Address":"172.30.0.10"},"Aliases":["gitea","8a1f3c5e7b9d"],"IPAddress":"172.30.0.10","MacAddress":"02:42:ac:1e:00:0a","EndpointID":"e1"},
 "monitoring":{"IPAMConfig":null,"Aliases":null,"IPAddress":"172.31.0.4","EndpointID":"e2"}}}}`

func TestDockerRecreateRequest(t *testing.T) {
	r, err := recreateRequest(json.RawMessage(recreateSample))
	if err != nil {
		t.Fatal(err)
	}
	if r.image != "gitea/gitea:1.22" || !r.running {
		t.Errorf("image %q, running %v", r.image, r.running)
	}
	var body struct {
		Hostname         *string
		Image            string
		Env              []string
		HostConfig       map[string]any
		NetworkingConfig struct {
			EndpointsConfig map[string]map[string]any
		}
	}
	if err := json.Unmarshal(r.body, &body); err != nil {
		t.Fatal(err)
	}
	if body.Hostname != nil || body.Image != "gitea/gitea:1.22" || len(body.Env) != 2 || body.HostConfig["NetworkMode"] != "gitea_net" {
		t.Errorf("body %s", r.body)
	}
	// The anonymous volume is mounted again by name; the named one is a
	// bind already.
	mounts, _ := json.Marshal(body.HostConfig["Mounts"])
	if string(mounts) != `[{"Source":"0d2c4e6f","Target":"/var/cache","Type":"volume"}]` {
		t.Errorf("mounts %s", mounts)
	}
	// Created on its network mode's network, with its fixed address and
	// its alias, not the old ID's; the other network joined after.
	eps := body.NetworkingConfig.EndpointsConfig
	ep, _ := json.Marshal(eps["gitea_net"])
	if len(eps) != 1 || string(ep) != `{"Aliases":["gitea"],"IPAMConfig":{"IPv4Address":"172.30.0.10"}}` {
		t.Errorf("endpoints %v", eps)
	}
	if len(r.networks) != 1 || r.networks[0].name != "monitoring" || r.networks[0].before != `{"EndpointConfig":{},"Container":"` {
		t.Errorf("networks %+v", r.networks)
	}
	for _, bad := range []string{`{}`, `not json`, `{"Config":{"Image":"sha256:abc"},"HostConfig":{}}`} {
		if _, err := recreateRequest(json.RawMessage(bad)); err == nil {
			t.Errorf("took %s", bad)
		}
	}
}

func recreateCtr(t *testing.T) *Container {
	t.Helper()
	cs := parseInspect(strings.ReplaceAll(recreateSample, "\n", ""))
	if len(cs) != 1 {
		t.Fatal("the sample does not parse")
	}
	return &cs[0]
}

var recreateAt = time.Date(2026, 11, 24, 15, 30, 0, 0, time.UTC)

func TestDockerRecreateScript(t *testing.T) {
	ct := recreateCtr(t)
	s, err := DockerCmd{Tool: "docker", Sudo: true}.RecreateScript(ct, recreateAt)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"id='" + ct.ID + "' name='gitea' old='gitea-old-20261124-153000' running=true new=",
		"sudo -n docker 'pull' '--' 'gitea/gitea:1.22' || fail",
		"sudo -n docker 'stop' '" + ct.ID + "'",
		"sudo -n docker 'rename' '" + ct.ID + "' 'gitea-old-20261124-153000' ||",
		`sudo -n curl -q -sS --globoff --noproxy '*' --unix-socket "$sock"`,
		"post '/containers/create?name=gitea' '{",
		`"Q=it'\''s \"$x\""`, // the environment, quoted
		`post '/networks/monitoring/connect' "{\"EndpointConfig\":{},\"Container\":\"$new\"}"`,
		`sudo -n docker 'start' "$new" >/dev/null || rollback`,
		`sudo -n docker 'rm' '-f' "$new"`,
		"sudo -n docker 'rename' '" + ct.ID + "' 'gitea' ||",
		"sudo -n docker 'rm' '" + ct.ID + "' >/dev/null ||",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("no %q in\n%s", want, s)
		}
	}
	checkSyntax(t, s)
	if _, err := (DockerCmd{Tool: "podman"}).RecreateScript(ct, recreateAt); err == nil {
		t.Error("Podman's")
	}
	ct.AutoRemove = true
	if _, err := (DockerCmd{Tool: "docker"}).RecreateScript(ct, recreateAt); err == nil {
		t.Error("a --rm container")
	}
	ct.AutoRemove, ct.Project = false, "shop"
	if _, err := (DockerCmd{Tool: "docker"}).RecreateScript(ct, recreateAt); err == nil {
		t.Error("a Compose container")
	}
}

// checkSyntax has sh read script without running it.
func checkSyntax(t *testing.T, script string) {
	t.Helper()
	sh, err := exec.LookPath("sh")
	if err != nil {
		return
	}
	cmd := exec.Command(sh, "-n")
	cmd.Stdin = strings.NewReader(script)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Errorf("sh -n: %v: %s\n%s", err, out, script)
	}
}

// fakeEngine is a folder with docker and curl commands that log their
// calls and answer as the test says, and a socket for the script's
// check: RecreateScript runs on it as on a server, without an engine.
type fakeEngine struct {
	dir, log string
	env      []string
}

func newFakeEngine(t *testing.T, fail, createCode string) *fakeEngine {
	if runtime.GOOS == "windows" {
		t.Skip("the fake engine needs a Unix shell and socket")
	}
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	dir := t.TempDir()
	f := &fakeEngine{dir: dir, log: filepath.Join(dir, "log")}
	sock := filepath.Join(dir, "docker.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Skip(err)
	}
	t.Cleanup(func() { l.Close() })
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!"+sh+"\n"+body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// docker fails the call whose words start with $FAIL.
	write("docker", `echo "docker $*" >> "$LOG"
case "$*" in "$FAIL"*) echo "Error: $1 failed" >&2; exit 1 ;; esac
`)
	write("sudo", `shift; exec "$@"`)
	write("curl", `body=$(cat); for a; do u=$a; done
echo "curl $u $body" >> "$LOG"
case "$u" in
*/containers/*/json) printf '{"Id":"%s"}' "$ID" ;;
*/containers/create*) printf '{"Id":"feedbeef","Warnings":[]}\n%s' "$CREATE" ;;
*) printf '\n200' ;;
esac
`)
	f.env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"), "LOG="+f.log, "FAIL="+fail, "CREATE="+createCode, "DOCKER_HOST=unix://"+sock, "DOCKER_CONTEXT=", "ID="+recreateCtr(t).ID)
	return f
}

// run runs script on the fake engine: its output, whether it failed,
// and the calls.
func (f *fakeEngine) run(t *testing.T, script string) (string, bool, []string) {
	t.Helper()
	cmd := exec.Command("sh", "-s")
	cmd.Stdin, cmd.Env = strings.NewReader(script), f.env
	out, err := cmd.CombinedOutput()
	data, _ := os.ReadFile(f.log)
	var calls []string
	for _, l := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		// A request's URL tells it; the create's body has the image.
		if url, body, ok := strings.Cut(l, " {"); ok && strings.HasPrefix(l, "curl ") {
			if strings.Contains(url, "/containers/create") && !strings.Contains(body, `"Image":"gitea/gitea:1.22"`) {
				t.Errorf("create body {%s", body)
			}
			l = url
		}
		calls = append(calls, strings.TrimRight(l, " "))
	}
	return string(out), err != nil, calls
}

func TestDockerRecreateRuns(t *testing.T) {
	ct := recreateCtr(t)
	id, old := ct.ID, "gitea-old-20261124-153000"
	script, err := DockerCmd{Tool: "docker", Sudo: true}.RecreateScript(ct, recreateAt)
	if err != nil {
		t.Fatal(err)
	}
	create := "curl http://localhost/containers/create?name=gitea"
	inspect := "curl http://localhost/containers/" + id + "/json"
	join := "curl http://localhost/networks/monitoring/connect"
	for _, tc := range []struct {
		name, fail, code string
		failed           bool
		calls            []string
		out              string
	}{
		{"works", "-", "201", false, []string{
			inspect, "docker pull -- gitea/gitea:1.22", "docker stop " + id, "docker rename " + id + " " + old, create, join,
			"docker start feedbeef", "docker rm " + id,
		}, "==> Done: gitea is recreated from gitea/gitea:1.22."},
		{"pull fails", "pull", "201", true, []string{inspect, "docker pull -- gitea/gitea:1.22"}, "Could not pull the image. Nothing was changed."},
		{"create fails", "-", "409", true, []string{
			inspect, "docker pull -- gitea/gitea:1.22", "docker stop " + id, "docker rename " + id + " " + old, create,
			"docker rename " + id + " gitea", "docker start " + id,
		}, "Could not create the new container. The container was put back as it was."},
		{"start fails", "start feedbeef", "201", true, []string{
			inspect, "docker pull -- gitea/gitea:1.22", "docker stop " + id, "docker rename " + id + " " + old, create, join,
			"docker start feedbeef", "docker rm -f feedbeef", "docker rename " + id + " gitea", "docker start " + id,
		}, "The new container did not start. The container was put back as it was."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeEngine(t, tc.fail, tc.code)
			out, failed, calls := f.run(t, script)
			if failed != tc.failed || !strings.Contains(out, tc.out) || !reflect.DeepEqual(calls, tc.calls) {
				t.Errorf("failed %v, output\n%s\ncalls\n%s", failed, out, strings.Join(calls, "\n"))
			}
		})
	}
}

func TestDockerRecreateStoppedAndWrongSocket(t *testing.T) {
	ct := recreateCtr(t)
	ct.Inspect = json.RawMessage(strings.ReplaceAll(string(ct.Inspect), `"Running":true`, `"Running":false`))
	script, err := (DockerCmd{Tool: "docker"}).RecreateScript(ct, recreateAt)
	if err != nil {
		t.Fatal(err)
	}
	fake := newFakeEngine(t, "-", "201")
	if out, failed, calls := fake.run(t, script); failed || slices.Contains(calls, "docker stop "+ct.ID) || !slices.Contains(calls, "docker start feedbeef") {
		t.Fatalf("stopped replacement: %v, %q, %v", failed, out, calls)
	}
	fake = newFakeEngine(t, "-", "201")
	fake.env = append(fake.env, "ID=deadbeef")
	if out, failed, calls := fake.run(t, script); !failed || len(calls) != 1 || !strings.Contains(out, "does not identify the selected container") {
		t.Fatalf("wrong socket: %v, %q, %v", failed, out, calls)
	}
}

// Without docker compose, docker-compose (v1) runs the project's
// commands; with neither, the script says so.
func TestDockerComposeFallbackRuns(t *testing.T) {
	f := newFakeEngine(t, "compose", "201")
	p := Compose{Project: "shop", Dir: f.dir, Files: []string{"/srv/shop/compose.yaml"}}
	script, err := DockerCmd{Tool: "docker"}.ComposeUpdateScript(p, "web")
	if err != nil {
		t.Fatal(err)
	}
	// Only the fakes: compose's script needs nothing else, and no
	// docker-compose of the system's answers.
	for i, e := range f.env {
		if strings.HasPrefix(e, "PATH=") {
			f.env[i] = "PATH=" + f.dir
		}
	}
	if out, failed, _ := f.run(t, script); !failed || !strings.Contains(out, "Neither 'docker compose' nor docker-compose is installed.") {
		t.Errorf("neither: failed %v, %s", failed, out)
	}
	if err := os.WriteFile(f.log, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.dir, "docker-compose"), []byte("#!/bin/sh\necho \"docker-compose $*\" >> \"$LOG\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, failed, calls := f.run(t, script)
	want := []string{
		"docker compose version",
		"docker-compose version",
		"docker-compose -p shop -f /srv/shop/compose.yaml pull web",
		"docker-compose -p shop -f /srv/shop/compose.yaml up -d --force-recreate --no-deps web",
	}
	if failed || !reflect.DeepEqual(calls, want) {
		t.Errorf("v1: failed %v, calls %q", failed, calls)
	}
}
