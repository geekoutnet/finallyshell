package monitor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// The Overview tab manages containers with the commands below: each is a
// POSIX sh script built here, every argument quoted, which RunDocker
// runs. They use the tool the read found (Docker.Tool), with sudo -n when
// the read needed it, so an action reaches the engine the list came from.

// DockerCmd is how to run the engine's command on a server: its tool,
// docker or podman, and whether through sudo -n.
type DockerCmd struct {
	Tool string
	Sudo bool
}

// CmdOf is the command that reads d.
func CmdOf(d *Docker) DockerCmd { return DockerCmd{Tool: d.Tool, Sudo: d.Sudo} }

// prefix is the command's words before its arguments: docker, or
// sudo -n docker.
func (c DockerCmd) prefix() string {
	t := c.Tool
	if t != "podman" {
		t = "docker"
	}
	if c.Sudo {
		return "sudo -n " + t
	}
	return t
}

// Line is the command line running the tool with args, each quoted.
func (c DockerCmd) Line(args ...string) string {
	parts := []string{c.prefix()}
	for _, arg := range args {
		parts = append(parts, dockerQuote(arg))
	}
	return strings.Join(parts, " ")
}

func dockerQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// ctrRef is what a container ID or name may be.
var ctrRef = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)

// ValidName tells whether s may name a container: what docker run
// --name takes.
func ValidName(s string) bool { return ctrRef.MatchString(s) }

// ErrBadContainer is a container ID or name that is not one.
var ErrBadContainer = errors.New("not a container ID or name")

func checkRefs(refs []string) error {
	if len(refs) == 0 {
		return ErrBadContainer
	}
	for _, r := range refs {
		if !ValidName(r) {
			return fmt.Errorf("%w: %q", ErrBadContainer, r)
		}
	}
	return nil
}

// scriptHead starts every action's script: its messages in English, and
// what goes to stderr to stdout, in order with the rest.
const scriptHead = "export LC_ALL=C\nexec 2>&1\n"

// CtrOp is an action on containers that the engine's command of the
// same name does to each: start, stop, restart, kill, pause, unpause.
type CtrOp string

const (
	OpStart   CtrOp = "start"
	OpStop    CtrOp = "stop"
	OpRestart CtrOp = "restart"
	OpKill    CtrOp = "kill"
	OpPause   CtrOp = "pause"
	OpUnpause CtrOp = "unpause"
)

var ctrOps = []CtrOp{OpStart, OpStop, OpRestart, OpKill, OpPause, OpUnpause}

// OpScript does op to the containers ids.
func (c DockerCmd) OpScript(op CtrOp, ids ...string) (string, error) {
	if !slices.Contains(ctrOps, op) {
		return "", fmt.Errorf("unknown action %q", op)
	}
	if err := checkRefs(ids); err != nil {
		return "", err
	}
	return scriptHead + c.Line(append([]string{string(op)}, ids...)...) + "\n", nil
}

// RemoveScript removes the containers ids; force removes those that run
// too, volumes their anonymous volumes.
func (c DockerCmd) RemoveScript(force, volumes bool, ids ...string) (string, error) {
	if err := checkRefs(ids); err != nil {
		return "", err
	}
	args := []string{"rm"}
	if force {
		args = append(args, "-f")
	}
	if volumes {
		args = append(args, "-v")
	}
	return scriptHead + c.Line(append(args, ids...)...) + "\n", nil
}

// RenameScript gives the container id the name name.
func (c DockerCmd) RenameScript(id, name string) (string, error) {
	if err := checkRefs([]string{id, name}); err != nil {
		return "", err
	}
	return scriptHead + c.Line("rename", id, name) + "\n", nil
}

// RestartPolicies are the restart policies a container may have.
var RestartPolicies = []string{"no", "on-failure", "always", "unless-stopped"}

// RestartPolicyScript sets the container id's restart policy; max is
// how many times on-failure tries, 0 for no end.
func (c DockerCmd) RestartPolicyScript(id, policy string, max int) (string, error) {
	if err := checkRefs([]string{id}); err != nil {
		return "", err
	}
	if !slices.Contains(RestartPolicies, policy) || max < 0 {
		return "", fmt.Errorf("bad restart policy %q", policy)
	}
	if policy == "on-failure" && max > 0 {
		policy += ":" + strconv.Itoa(max)
	}
	return scriptHead + c.Line("update", "--restart", policy, id) + "\n", nil
}

// LogsScript reads the last tail lines of the container id's output,
// stdout and stderr both, each line after its time.
func (c DockerCmd) LogsScript(id string, tail int) (string, error) {
	if err := checkRefs([]string{id}); err != nil {
		return "", err
	}
	return scriptHead + c.Line("logs", "--tail", strconv.Itoa(max(tail, 1)), "--timestamps", id) + "\n", nil
}

// ExecShell is the command line that opens a shell in the container id,
// bash where it has one, for a terminal to run.
func (c DockerCmd) ExecShell(id string) (string, error) {
	if err := checkRefs([]string{id}); err != nil {
		return "", err
	}
	return c.Line("exec", "-it", id, "sh", "-c", "command -v bash >/dev/null && exec bash || exec sh"), nil
}

// Compose is a Compose project as its containers' labels tell: its name,
// the folder it was started in and its files.
type Compose struct {
	Project string
	Dir     string
	Files   []string
}

// ComposeOf is the project of ct, by its labels; Project "" when it has
// none.
func ComposeOf(ct *Container) Compose {
	p := Compose{Project: ct.Project, Dir: ct.ComposeDir}
	for f := range strings.SplitSeq(ct.ComposeFiles, ",") {
		if f = strings.TrimSpace(f); f != "" {
			p.Files = append(p.Files, f)
		}
	}
	return p
}

// composeHead finds compose: docker compose, or docker-compose; podman
// compose, or podman-compose. It says so and ends the script when there
// is neither. It goes to the project's folder first, where relative
// paths in its files are relative to.
func (c DockerCmd) composeHead(p Compose) string {
	var b strings.Builder
	b.WriteString(scriptHead)
	if p.Dir != "" {
		b.WriteString("cd " + dockerQuote(p.Dir) + " || exit 1\n")
	}
	v1 := "docker-compose"
	if c.Tool == "podman" {
		v1 = "podman-compose"
	}
	sudo := ""
	if c.Sudo {
		sudo = "sudo -n "
	}
	fmt.Fprintf(&b, `if %s 'compose' 'version' >/dev/null 2>&1; then c() { %s 'compose' "$@"; }
elif %s%s 'version' >/dev/null 2>&1; then c() { %s%s "$@"; }
else echo "Neither '%s compose' nor %s is installed."; exit 127; fi
`, c.prefix(), c.prefix(), sudo, v1, sudo, v1, strings.TrimPrefix(c.prefix(), "sudo -n "), v1)
	return b.String()
}

// composeLine is compose running args in the project p: c -p project -f
// file... args.
func composeLine(p Compose, args ...string) string {
	parts := []string{"c", "'-p'", dockerQuote(p.Project)}
	for _, f := range p.Files {
		parts = append(parts, "'-f'", dockerQuote(f))
	}
	for _, a := range args {
		parts = append(parts, dockerQuote(a))
	}
	return strings.Join(parts, " ")
}

func checkCompose(p Compose, services []string) error {
	if p.Project == "" || !ValidName(p.Project) {
		return fmt.Errorf("not a Compose project: %q", p.Project)
	}
	if !strings.HasPrefix(p.Dir, "/") || len(p.Files) == 0 {
		return fmt.Errorf("the project %s has no working directory or configuration files known", p.Project)
	}
	for _, file := range p.Files {
		if file == "" || file == "-" || strings.ContainsRune(file, 0) {
			return fmt.Errorf("not a Compose configuration file: %q", file)
		}
	}
	for _, s := range services {
		if !ValidName(s) {
			return fmt.Errorf("not a Compose service: %q", s)
		}
	}
	return nil
}

// ComposeUpdateScript pulls the images of the project p, or of its
// service when not "", and recreates its containers from them: a service
// is recreated alone; otherwise all the project's services are recreated.
func (c DockerCmd) ComposeUpdateScript(p Compose, service string) (string, error) {
	var svc []string
	if service != "" {
		svc = []string{service}
	}
	if err := checkCompose(p, svc); err != nil {
		return "", err
	}
	up := []string{"up", "-d", "--force-recreate"}
	if service != "" {
		up = append(up, "--no-deps", service)
	}
	return c.composeHead(p) + composeLine(p, append([]string{"pull"}, svc...)...) + " && " + composeLine(p, up...) + "\n", nil
}

// ComposeDownScript stops and removes the project p's containers and
// networks; its volumes stay.
func (c DockerCmd) ComposeDownScript(p Compose) (string, error) {
	if err := checkCompose(p, nil); err != nil {
		return "", err
	}
	return c.composeHead(p) + composeLine(p, "down") + "\n", nil
}

// ComposeLogsScript reads the last tail lines of each container of the
// project p, each line after its container's name and its time.
func (c DockerCmd) ComposeLogsScript(p Compose, tail int) (string, error) {
	if err := checkCompose(p, nil); err != nil {
		return "", err
	}
	return c.composeHead(p) + composeLine(p, "logs", "--no-color", "--timestamps", "--tail", strconv.Itoa(max(tail, 1))) + "\n", nil
}

// RecreateScript pulls the image of the container ct, which no Compose
// project manages, and replaces the container with a new one from it, as
// Portainer's Recreate does: the old one stops and steps aside, renamed
// to name-old-<at>; the new one is created through the Engine API (curl
// on the engine's socket) with the old one's settings (its docker
// inspect's Config, HostConfig and networks), its anonymous volumes
// mounted again so that their data stays, and joins the old one's other
// networks. It must start successfully before the old one is
// removed. When a step fails, the new container is removed and the old
// one takes its name back and starts again.
func (c DockerCmd) RecreateScript(ct *Container, at time.Time) (string, error) {
	if c.Tool == "podman" {
		return "", errors.New("recreating a container is for Docker only")
	}
	if err := checkRefs([]string{ct.ID, ct.Name}); err != nil {
		return "", err
	}
	if ct.Project != "" {
		return "", errors.New("recreate a Compose container through its project")
	}
	if ct.AutoRemove {
		return "", errors.New("the container is removed as it stops (--rm)")
	}
	if ct.Paused || ct.Status == "paused" {
		return "", errors.New("unpause the container before recreating it")
	}
	r, err := recreateRequest(ct.Inspect)
	if err != nil {
		return "", err
	}
	sudo := ""
	if c.Sudo {
		sudo = "sudo -n "
	}
	old := ct.Name + "-old-" + at.Format("20060102-150405")
	start := "false"
	if r.running {
		start = "true"
	}
	curl := sudo + `curl -q -sS --globoff --noproxy '*' --unix-socket "$sock"`
	var b strings.Builder
	b.WriteString(scriptHead)
	fmt.Fprintf(&b, `id=%s name=%s old=%s running=%s new=
step() { echo "==> $*"; }
fail() { echo "$*"; exit 1; }
command -v curl >/dev/null 2>&1 || fail "curl is not installed on the server: it is needed to create the container through the Docker socket. Nothing was changed."
endpoint=$DOCKER_HOST
if [ -z "$endpoint" ] || [ -n "$DOCKER_CONTEXT" ]; then
  endpoint=$(%s) || fail "Could not determine the Docker socket. Nothing was changed."
fi
case "$endpoint" in unix:///*) sock=${endpoint#unix://} ;; *) fail "Recreate needs a Unix Docker socket. Nothing was changed." ;; esac
[ -S "$sock" ] || fail "No Docker socket at $sock. Nothing was changed."
before=$(%s -f "http://localhost/containers/$id/json" </dev/null) || fail "Could not inspect the container through the Docker socket. Nothing was changed."
actual=$(printf '%%s\n' "$before" | sed -n 's/^[{[:space:]]*"Id"[[:space:]]*:[[:space:]]*"\([0-9a-f]*\)".*/\1/p')
[ "$actual" = "$id" ] || fail "The Docker socket does not identify the selected container. Nothing was changed."
# post path body: POSTs body to the Engine API; $code is the HTTP status,
# $resp the answer.
post() {
  out=$(printf '%%s' "$2" | %s -H 'Content-Type: application/json' -X POST --data-binary @- -w '\n%%{http_code}' "http://localhost$1") || { echo "$out"; return 1; }
  code=$(printf '%%s\n' "$out" | tail -n 1)
  resp=$(printf '%%s\n' "$out" | sed '$d')
}
rollback() {
  step "Rolling back"
  if [ -n "$new" ]; then %s || fail "$1 Rollback could not remove the replacement; the original is named $old."; fi
  %s || fail "$1 Rollback could not restore the original name; the original is named $old."
  if [ "$running" = true ]; then %s || fail "$1 Rollback restored the original name but could not start it."; fi
  fail "$1 The container was put back as it was."
}
step %s
%s || fail "Could not pull the image. Nothing was changed."
`, dockerQuote(ct.ID), dockerQuote(ct.Name), dockerQuote(old), start,
		c.Line("context", "inspect", "--format", "{{.Endpoints.docker.Host}}"), curl, curl,
		c.Line("rm", "-f")+` "$new" >/dev/null`,
		c.Line("rename", ct.ID, ct.Name), c.Line("start", ct.ID)+" >/dev/null",
		dockerQuote("Pulling "+r.image), c.Line("pull", "--", r.image))
	fmt.Fprintf(&b, `if [ "$running" = true ]; then
  step "Stopping $name"
  %s >/dev/null || fail "Could not stop the container. Nothing was changed."
fi
%s || {
  if [ "$running" = true ]; then %s || fail "Could not rename or restart the original container."; fi
  fail "Could not rename the container."
}
step "Creating $name"
post %s %s || rollback "Could not reach the Docker socket."
new=$(printf '%%s' "$resp" | sed -n 's/.*"Id": *"\([0-9a-f]*\)".*/\1/p')
[ "$code" = 201 ] && [ -n "$new" ] || { echo "$resp"; new=; rollback "Could not create the new container."; }
`, c.Line("stop", ct.ID), c.Line("rename", ct.ID, old), c.Line("start", ct.ID)+" >/dev/null",
		dockerQuote("/containers/create?name="+ct.Name), dockerQuote(string(r.body)))
	for _, n := range r.networks {
		fmt.Fprintf(&b, `step "Connecting to %s"
post %s "%s$new%s" || rollback "Could not reach the Docker socket."
[ "$code" = 200 ] || { echo "$resp"; rollback "Could not connect the new container to %s."; }
`, shDouble(n.name), dockerQuote("/networks/"+url.PathEscape(n.name)+"/connect"), shDouble(n.before), shDouble(n.after), shDouble(n.name))
	}
	fmt.Fprintf(&b, `step "Starting $name"
%s "$new" >/dev/null || rollback "The new container did not start."
step "Removing the old container"
%s >/dev/null || fail "The replacement started, but the old container $old could not be removed."
step "Done: $name is recreated from %s."
`, c.Line("start"), c.Line("rm", ct.ID), shDouble(r.image))
	return b.String(), nil
}

// shDouble is s for the inside of a shell's double quotes.
func shDouble(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "$", `\$`, "`", "\\`")
	return r.Replace(s)
}

// recreate is what RecreateScript needs of a docker inspect: the body
// creating the new container, its image, whether the old one ran, and
// the networks the new one joins after it is created.
type recreate struct {
	body     []byte
	image    string
	running  bool
	networks []netJoin
}

// netJoin is a network to connect the new container to: the body of the
// request is before, the container's ID, then after.
type netJoin struct {
	name, before, after string
}

// recreateRequest reads a container's docker inspect for RecreateScript.
func recreateRequest(inspect json.RawMessage) (recreate, error) {
	var j struct {
		ID         string `json:"Id"`
		Config     map[string]json.RawMessage
		HostConfig map[string]json.RawMessage
		State      struct{ Running, Restarting bool }
		Mounts     []struct{ Type, Name, Destination string }

		NetworkSettings struct {
			Networks map[string]map[string]json.RawMessage
		}
	}
	if err := json.Unmarshal(inspect, &j); err != nil || j.Config == nil || j.HostConfig == nil {
		return recreate{}, errors.New("the container's docker inspect is not known")
	}
	r := recreate{running: j.State.Running || j.State.Restarting}
	json.Unmarshal(j.Config["Image"], &r.image)
	if r.image == "" || strings.HasPrefix(r.image, "sha256:") {
		return recreate{}, errors.New("the container's image has no name to pull")
	}
	// A host name docker gave it, its ID's start, is the new one's ID's.
	var host string
	if json.Unmarshal(j.Config["Hostname"], &host) == nil && host != "" && strings.HasPrefix(j.ID, host) {
		delete(j.Config, "Hostname")
	}
	// The anonymous volumes, which no bind or mount names: mounted by
	// name, they keep their data.
	covered := map[string]bool{}
	var binds []string
	json.Unmarshal(j.HostConfig["Binds"], &binds)
	for _, b := range binds {
		if parts := strings.Split(b, ":"); len(parts) >= 2 {
			covered[parts[1]] = true
		}
	}
	var mounts []map[string]any
	json.Unmarshal(j.HostConfig["Mounts"], &mounts)
	for _, m := range mounts {
		if t, ok := m["Target"].(string); ok {
			covered[t] = true
		}
	}
	var tmpfs map[string]string
	json.Unmarshal(j.HostConfig["Tmpfs"], &tmpfs)
	for t := range tmpfs {
		covered[t] = true
	}
	added := false
	for _, m := range j.Mounts {
		if m.Type == "volume" && m.Name != "" && !covered[m.Destination] {
			mounts = append(mounts, map[string]any{"Type": "volume", "Source": m.Name, "Target": m.Destination})
			covered[m.Destination] = true
			added = true
		}
	}
	if added {
		b, _ := json.Marshal(mounts)
		j.HostConfig["Mounts"] = b
	}
	// The networks: the one of its network mode with the create, the
	// others after, as an engine before API 1.44 takes one network only.
	var mode string
	json.Unmarshal(j.HostConfig["NetworkMode"], &mode)
	body := map[string]any{}
	for k, v := range j.Config {
		body[k] = v
	}
	body["HostConfig"] = j.HostConfig
	shared := mode == "host" || mode == "none" || strings.HasPrefix(mode, "container:")
	if nets := j.NetworkSettings.Networks; len(nets) > 0 && !shared {
		names := make([]string, 0, len(nets))
		for n := range nets {
			names = append(names, n)
		}
		slices.Sort(names)
		first := names[0]
		if _, ok := nets[mode]; ok {
			first = mode
		} else if mode == "default" {
			if _, ok := nets["bridge"]; ok {
				first = "bridge"
			}
		}
		body["NetworkingConfig"] = map[string]any{"EndpointsConfig": map[string]any{first: endpoint(nets[first], j.ID)}}
		for _, n := range names {
			if n == first {
				continue
			}
			e, _ := json.Marshal(endpoint(nets[n], j.ID))
			r.networks = append(r.networks, netJoin{name: n, before: `{"EndpointConfig":` + string(e) + `,"Container":"`, after: `"}`})
		}
	}
	b, err := json.Marshal(body)
	if err != nil {
		return recreate{}, err
	}
	r.body = b
	return r, nil
}

// endpoint is the settings of a network of the old container the new
// one takes: its fixed addresses, aliases (not the old one's ID), links
// and driver options. The addresses the network gave it, its MAC and
// its endpoint's ID are the new one's own.
func endpoint(e map[string]json.RawMessage, id string) map[string]json.RawMessage {
	out := map[string]json.RawMessage{}
	for _, k := range []string{"IPAMConfig", "Links", "DriverOpts"} {
		if v, ok := e[k]; ok && string(v) != "null" {
			out[k] = v
		}
	}
	var aliases []string
	json.Unmarshal(e["Aliases"], &aliases)
	aliases = slices.DeleteFunc(aliases, func(a string) bool { return len(a) >= 12 && strings.HasPrefix(id, a) })
	if len(aliases) > 0 {
		out["Aliases"], _ = json.Marshal(aliases)
	}
	return out
}

// DockerExit is an action's script that ended with an error: its exit
// status. Its output says why.
type DockerExit struct{ Status int }

func (e *DockerExit) Error() string { return fmt.Sprintf("exit status %d", e.Status) }

// RunDocker runs an action's script on a new exec channel of client and
// returns what it wrote, stderr in order with stdout, stopping it when
// ctx ends. A script that fails is a *DockerExit.
func RunDocker(ctx context.Context, client *ssh.Client, script string) (string, error) {
	return RunDockerProgress(ctx, client, script, nil)
}

// RunDockerProgress also reports successive stdout chunks from the action.
func RunDockerProgress(ctx context.Context, client *ssh.Client, script string, progress func(string)) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	out, stderr, err := runScript(ctx, client, script, progress)
	out += stderr // before exec 2>&1, or from sh itself
	if ctx.Err() != nil {
		return out, ctx.Err()
	}
	var exit *ssh.ExitError
	if errors.As(err, &exit) {
		return out, &DockerExit{Status: exit.ExitStatus()}
	}
	return out, err
}
