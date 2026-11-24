package monitor

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

type DockerEngineVersion struct {
	Version, APIVersion, OS, Arch string
}

type DockerEngineInfo struct {
	Client, Server                                                         DockerEngineVersion
	OS, Kernel, Architecture, Driver, RootDir, CgroupDriver, CgroupVersion string
	DefaultRuntime, LoggingDriver, HTTPProxy, HTTPSProxy                   string
	Runtimes, Mirrors, Insecure, Warnings                                  []string
	LiveRestore, Rootless                                                  bool
	CPUs, Containers, Running, Paused, Stopped, Images                     int
	Memory                                                                 uint64
}

func (cmd DockerCmd) EngineInfo() string {
	return resourceReadHead + cmd.Line("info", "--format", "{{json .}}") + "\n"
}
func (cmd DockerCmd) EngineVersion() string {
	return resourceReadHead + cmd.Line("version", "--format", "{{json .}}") + "\n"
}

func ParseDockerEngineInfo(info, version string) (DockerEngineInfo, error) {
	var result DockerEngineInfo
	var raw struct {
		OperatingSystem, KernelVersion, Architecture, Driver, DockerRootDir               string
		CgroupDriver, CgroupVersion, DefaultRuntime, LoggingDriver, HTTPProxy, HTTPSProxy string
		NCPU, Containers, ContainersRunning, ContainersPaused, ContainersStopped, Images  int
		MemTotal                                                                          uint64
		LiveRestoreEnabled                                                                bool
		Warnings, SecurityOptions                                                         []string
		Runtimes                                                                          map[string]json.RawMessage
		RegistryConfig                                                                    struct {
			Mirrors, InsecureRegistryCIDRs []string
			IndexConfigs                   map[string]struct{ Secure bool }
		}
	}
	if err := json.Unmarshal([]byte(info), &raw); err != nil {
		return result, err
	}
	var versions struct{ Client, Server DockerEngineVersion }
	if err := json.Unmarshal([]byte(version), &versions); err != nil {
		return result, err
	}
	result = DockerEngineInfo{
		Client: versions.Client, Server: versions.Server, OS: raw.OperatingSystem, Kernel: raw.KernelVersion,
		Architecture: raw.Architecture, Driver: raw.Driver, RootDir: raw.DockerRootDir, CgroupDriver: raw.CgroupDriver,
		CgroupVersion: raw.CgroupVersion, DefaultRuntime: raw.DefaultRuntime, LoggingDriver: raw.LoggingDriver,
		HTTPProxy: resourcePublicURL(raw.HTTPProxy), HTTPSProxy: resourcePublicURL(raw.HTTPSProxy), Warnings: raw.Warnings,
		LiveRestore: raw.LiveRestoreEnabled, CPUs: raw.NCPU, Memory: raw.MemTotal, Containers: raw.Containers,
		Running: raw.ContainersRunning, Paused: raw.ContainersPaused, Stopped: raw.ContainersStopped, Images: raw.Images,
	}
	for _, value := range raw.SecurityOptions {
		result.Rootless = result.Rootless || strings.HasPrefix(value, "name=rootless")
	}
	for name := range raw.Runtimes {
		result.Runtimes = append(result.Runtimes, name)
	}
	slices.Sort(result.Runtimes)
	for _, mirror := range raw.RegistryConfig.Mirrors {
		if value := resourcePublicURL(mirror); value != "" {
			result.Mirrors = append(result.Mirrors, value)
		}
	}
	result.Insecure = append(result.Insecure, raw.RegistryConfig.InsecureRegistryCIDRs...)
	for name, index := range raw.RegistryConfig.IndexConfigs {
		if !index.Secure {
			result.Insecure = append(result.Insecure, name)
		}
	}
	slices.Sort(result.Insecure)
	return result, nil
}

func resourcePublicURL(text string) string {
	address, err := url.Parse(text)
	if err != nil || address.Host == "" || (address.Scheme != "http" && address.Scheme != "https" && address.Scheme != "socks5" && address.Scheme != "socks5h") {
		return ""
	}
	return address.Scheme + "://" + address.Host
}

type DockerResourceDisk struct {
	Type, Total, Active, Size, Reclaimable string
}

func DockerReclaimed(output string) (uint64, bool) {
	var total uint64
	found := false
	for line := range strings.SplitSeq(output, "\n") {
		if size, ok := strings.CutPrefix(strings.TrimSpace(line), "Total reclaimed space:"); ok {
			total += ParseSize(size)
			found = true
		}
	}
	return total, found
}

type DockerVolumeUsage struct {
	Size  string
	Links int
}

func (cmd DockerCmd) ResourceDisk(verbose bool) string {
	args := []string{"system", "df"}
	if verbose {
		args = append(args, "-v")
	}
	if cmd.Tool == "podman" {
		args = append(args, "--format", "json")
	} else {
		args = append(args, "--format", "{{json .}}")
	}
	return resourceReadHead + cmd.Line(args...) + "\n"
}

func ParseDockerResourceDisk(output string) ([]DockerResourceDisk, error) {
	var rows []DockerResourceDisk
	if strings.HasPrefix(strings.TrimSpace(output), "[") {
		var podman []struct {
			Type              string
			Total, Active     int
			Size, Reclaimable json.RawMessage
		}
		if err := json.Unmarshal([]byte(output), &podman); err != nil {
			return nil, err
		}
		for _, row := range podman {
			rows = append(rows, DockerResourceDisk{row.Type, strconv.Itoa(row.Total), strconv.Itoa(row.Active), resourceSize(row.Size), resourceSize(row.Reclaimable)})
		}
		return rows, nil
	}
	for line := range strings.SplitSeq(output, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var row struct {
			DockerResourceDisk
			TotalCount string
		}
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			return nil, err
		}
		if row.Type == "" {
			return nil, ErrScriptOutput
		}
		if row.TotalCount != "" {
			row.Total = row.TotalCount
		}
		rows = append(rows, row.DockerResourceDisk)
	}
	return rows, nil
}

func resourceSize(raw json.RawMessage) string {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	if bytes.Equal(raw, []byte("null")) || len(raw) == 0 {
		return ""
	}
	return string(raw) + " B"
}

func ParseDockerVolumeUsage(output string) (map[string]DockerVolumeUsage, error) {
	result := make(map[string]DockerVolumeUsage)
	type volumeRow struct {
		Name, VolumeName string
		Size, Links      json.RawMessage
	}
	add := func(row volumeRow) error {
		name := row.Name
		if name == "" {
			name = row.VolumeName
		}
		if !ValidName(name) || len(row.Links) == 0 || len(row.Size) == 0 {
			return nil
		}
		text := strings.Trim(string(row.Links), `"`)
		links := -1
		if text != "N/A" && text != "null" {
			var err error
			links, err = strconv.Atoi(text)
			if err != nil {
				return err
			}
		}
		result[name] = DockerVolumeUsage{resourceSize(row.Size), links}
		return nil
	}
	var verbose struct{ Volumes []volumeRow }
	if json.Unmarshal([]byte(output), &verbose) == nil && verbose.Volumes != nil {
		for _, row := range verbose.Volumes {
			if err := add(row); err != nil {
				return nil, err
			}
		}
		return result, nil
	}
	active := false
	for line := range strings.SplitSeq(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "Local Volumes space usage:") {
			active = true
			continue
		}
		if strings.HasPrefix(line, "Build cache usage:") {
			break
		}
		if strings.HasPrefix(line, "{") {
			var row volumeRow
			if err := json.Unmarshal([]byte(line), &row); err != nil {
				return nil, err
			}
			if err := add(row); err != nil {
				return nil, err
			}
		} else if active {
			fields := strings.Fields(line)
			if len(fields) < 3 || !ValidName(fields[0]) {
				continue
			}
			links, err := strconv.Atoi(fields[1])
			if err == nil {
				result[fields[0]] = DockerVolumeUsage{strings.Join(fields[2:], " "), links}
			}
		}
	}
	if !active && len(result) == 0 {
		return nil, ErrScriptOutput
	}
	return result, nil
}

type DockerEvent struct {
	Time                   time.Time
	Type, Action, Name, ID string
	Attributes             map[string]string
}

func ParseDockerEvent(line string) (DockerEvent, bool) {
	var row struct {
		Type, Action, Status, Name, ID string
		Time                           json.RawMessage
		TimeNano                       int64
		Actor                          struct {
			ID         string
			Attributes map[string]string
		}
		Attributes map[string]string
	}
	if json.Unmarshal([]byte(line), &row) != nil || row.Type == "" {
		return DockerEvent{}, false
	}
	event := DockerEvent{Type: row.Type, Action: row.Action, Name: row.Name, ID: row.Actor.ID, Attributes: row.Actor.Attributes}
	if event.Action == "" {
		event.Action = row.Status
	}
	if event.Attributes == nil {
		event.Attributes = row.Attributes
	}
	if event.Name == "" {
		event.Name = event.Attributes["name"]
	}
	if event.ID == "" {
		event.ID = row.ID
	}
	if row.TimeNano != 0 {
		event.Time = time.Unix(0, row.TimeNano)
	} else {
		var seconds int64
		if json.Unmarshal(row.Time, &seconds) == nil && seconds != 0 {
			event.Time = time.Unix(seconds, 0)
		} else {
			var date string
			if json.Unmarshal(row.Time, &date) == nil {
				event.Time = parseTime(date)
			}
		}
	}
	return event, event.Action != ""
}

func (cmd DockerCmd) Events(since string) string {
	format := "{{json .}}"
	if cmd.Tool == "podman" {
		format = "json"
	}
	return resourceReadHead + "exec " + cmd.Line("events", "--since", since, "--format", format) + "\n"
}

func StreamDockerEvents(ctx context.Context, client *ssh.Client, script string, report func(string)) (string, error) {
	return runResourceExec(ctx, client, script, "", report, true)
}

type DockerRegistries struct {
	Registries []string
	Store      string
	Helpers    map[string]string
}

func ParseDockerRegistries(output string) (DockerRegistries, error) {
	var result DockerRegistries
	err := json.Unmarshal([]byte(output), &result)
	for index, registry := range result.Registries {
		if registry == "https://index.docker.io/v1/" {
			result.Registries[index] = "docker.io"
		}
	}
	if helper, ok := result.Helpers["https://index.docker.io/v1/"]; ok {
		result.Helpers["docker.io"] = helper
		delete(result.Helpers, "https://index.docker.io/v1/")
	}
	slices.Sort(result.Registries)
	result.Registries = slices.Compact(result.Registries)
	return result, err
}

func (cmd DockerCmd) RegistryList() string {
	return resourceReadHead + `file="$HOME/.docker/config.json"
if [ ! -e "$file" ] && [ -x "$HOME" ] && { [ ! -e "$HOME/.docker" ] || [ -x "$HOME/.docker" ]; }; then printf '%s\n' '{}'; exit 0; fi
if command -v python3 >/dev/null 2>&1; then
  exec python3 -c 'import json,re,subprocess,sys
with open(sys.argv[1], encoding="utf-8") as f: c = json.load(f)
registries = set((c.get("auths") or {}).keys())
store = c.get("credsStore") or ""
helpers = c.get("credHelpers") or {}
for helper in set(helpers.values()) | {store}:
    if not isinstance(helper,str) or not re.fullmatch(r"[a-zA-Z0-9_.-]+",helper): continue
    try:
        result = subprocess.run(["docker-credential-"+helper,"list"],stdin=subprocess.DEVNULL,stdout=subprocess.PIPE,stderr=subprocess.DEVNULL,check=True,timeout=3)
        listed = json.loads(result.stdout)
        if isinstance(listed,dict): registries.update(key for key in listed if helpers.get(key,store) == helper)
    except (OSError,ValueError,subprocess.SubprocessError): pass
print(json.dumps(dict(registries=sorted(registries),store=store,helpers=helpers)))' "$file" 2>/dev/null && exit 0
  printf '%s\n' 'Unable to read registry metadata.' >&2
  exit 1
fi
if command -v jq >/dev/null 2>&1; then
  metadata=$(jq -ec '{registries: ((.auths // {}) | keys), store: (.credsStore // ""), helpers: (.credHelpers // {})}' "$file" 2>/dev/null) || exit 1
  candidates=$(printf '%s\n' "$metadata" | jq -r '[.store, .helpers[]] | unique[] | strings | select(test("^[a-zA-Z0-9_.-]+$"))')
  registries=$(
    for helper in $candidates; do
      timeout 3 "docker-credential-$helper" list </dev/null 2>/dev/null |
        jq -c --argjson metadata "$metadata" --arg helper "$helper" 'keys[] as $key | select((if ($metadata.helpers | has($key)) then $metadata.helpers[$key] else $metadata.store end) == $helper) | $key' 2>/dev/null
    done
  )
  printf '%s\n' "$registries" | jq -cs --argjson metadata "$metadata" '. as $listed | $metadata | .registries |= (. + $listed | unique)'
  exit $?
fi
printf '%s\n' 'Registry metadata requires jq or Python 3.' >&2
exit 127
`
}

const registryLoginGuard = `file="$HOME/.docker/config.json"
registry=`

const registryHelperLookup = `
key=$registry
[ "$key" != docker.io ] || key='https://index.docker.io/v1/'
if command -v jq >/dev/null 2>&1; then
  helper=$(jq -ers --arg key "$key" 'select(length == 1) | .[0] | select(type == "object") | select(all(keys[]; . == "credsStore" or . == "credHelpers" or (ascii_downcase != "credsstore" and ascii_downcase != "credhelpers"))) | (if ((.credHelpers // {}) | has($key)) then .credHelpers[$key] else .credsStore end) | select(type == "string" and test("^[a-zA-Z0-9_.-]+$"))' "$file" 2>/dev/null) || exit 1
elif command -v python3 >/dev/null 2>&1; then
  helper=$(python3 -c 'import json,re,sys
with open(sys.argv[1], encoding="utf-8") as f: c = json.load(f)
if not isinstance(c, dict) or any(k.lower() in ("credsstore", "credhelpers") and k not in ("credsStore", "credHelpers") for k in c): sys.exit(1)
helper = (c.get("credHelpers") or {}).get(sys.argv[2], c.get("credsStore", ""))
if isinstance(helper, str) and re.fullmatch(r"[a-zA-Z0-9_.-]+", helper): print(helper)
else: sys.exit(1)' "$file" "$key" 2>/dev/null) || exit 1
else exit 1; fi
case "$helper" in ''|*[!a-zA-Z0-9_.-]*) exit 1 ;; esac
command -v "docker-credential-$helper" >/dev/null 2>&1 || exit 1
umask 077
config=$(mktemp -d) || exit 1
trap 'rm -f -- "$config/config.json"; rmdir -- "$config"' EXIT
printf '{"credsStore":"%s"}\n' "$helper" > "$config/config.json" || exit 1
unset DOCKER_AUTH_CONFIG DOCKER_CONTEXT DOCKER_HOST DOCKER_TLS DOCKER_TLS_VERIFY DOCKER_CERT_PATH
export DOCKER_CONFIG="$config"
`

func ValidDockerRegistry(registry string) bool {
	if registry == "" || strings.ContainsAny(registry, "/?#@% \\\r\n\t\x00") {
		return false
	}
	host := registry
	if strings.Contains(registry, ":") {
		if address, err := netip.ParseAddr(strings.Trim(registry, "[]")); err == nil && address.Is6() {
			return strings.HasPrefix(registry, "[") && strings.HasSuffix(registry, "]")
		}
		var port string
		var err error
		host, port, err = net.SplitHostPort(registry)
		if number, parseErr := strconv.ParseUint(port, 10, 16); err != nil || parseErr != nil || number == 0 {
			return false
		}
	}
	if _, err := netip.ParseAddr(host); err == nil {
		return true
	}
	return len(host) <= 253 && slices.IndexFunc(strings.Split(host, "."), func(part string) bool {
		return len(part) > 63 || !ValidName(part) || strings.Contains(part, "_") || strings.HasSuffix(part, "-")
	}) == -1
}

type DockerInput struct {
	script, input string
	private       bool
}

func (input DockerInput) Script() string { return input.script }

func (cmd DockerCmd) RegistryLogin(registry, username, password string) (DockerInput, error) {
	if cmd.Tool != "docker" {
		return DockerInput{}, ErrDockerResourceUnsupported
	}
	if !ValidDockerRegistry(registry) || username == "" || password == "" || strings.ContainsAny(username+password, "\x00\r\n") || strings.HasPrefix(username, "-") {
		return DockerInput{}, ErrDockerResourceInput
	}
	return DockerInput{script: "exec 2>&1\n" + registryLoginGuard + dockerQuote(registry) + registryHelperLookup +
		DockerCmd{Tool: "docker"}.Line("login", "--username", username, "--password-stdin", registry) + "\n", input: password + "\n", private: true}, nil
}

func (cmd DockerCmd) RegistryLogout(registry string) (string, error) {
	if cmd.Tool != "docker" {
		return "", ErrDockerResourceUnsupported
	}
	if !ValidDockerRegistry(registry) {
		return "", ErrDockerResourceInput
	}
	args := []string{"logout"}
	if registry != "docker.io" {
		args = append(args, registry)
	}
	return scriptHead + "unset DOCKER_AUTH_CONFIG\n" + `DOCKER_CONFIG="$HOME/.docker" ` + DockerCmd{Tool: "docker"}.Line(args...) + "\n", nil
}

func (input DockerInput) Run(ctx context.Context, client *ssh.Client, report func(string)) (string, error) {
	if input.private {
		_, err := runResourceExec(ctx, client, input.script, input.input, func(string) {}, false)
		return "", err
	}
	return runResourceExec(ctx, client, input.script, input.input, report, false)
}

func (input DockerInput) RunRoot(ctx context.Context, run DockerRootRunner, report func(string)) (string, error) {
	if input.private {
		_, _, err := run(ctx, input.script, PrivilegeOptions{Stdin: strings.NewReader(input.input)})
		if err == nil || errors.Is(err, ErrPrivilegeNotCached) || errors.Is(err, ErrPrivilegeDenied) ||
			errors.Is(err, ErrPrivilegeCancelled) || errors.Is(err, ErrPrivilegeWrongPassword) ||
			errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return "", err
		}
		return "", ErrDockerResourceInput
	}
	return runDockerRoot(ctx, run, input.script, PrivilegeOptions{Stdin: strings.NewReader(input.input), Progress: report, Interrupt: report != nil})
}

func runResourceExec(ctx context.Context, client *ssh.Client, script, input string, report func(string), stopRemote bool) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if client == nil {
		return "", errors.New("not connected")
	}
	session, err := client.NewSession()
	if err != nil {
		return "", err
	}
	defer session.Close()
	stop := context.AfterFunc(ctx, func() {
		if stopRemote {
			_ = session.Signal(ssh.SIGTERM)
		}
		_ = session.Close()
	})
	defer stop()
	session.Stdin = strings.NewReader(input)
	var output, stderr bytes.Buffer
	session.Stderr = &limitWriter{w: &stderr, n: 64 << 10}
	session.Stdout = &limitWriter{w: &output, n: procOutputLimit}
	if report != nil {
		session.Stdout = io.MultiWriter(session.Stdout, scriptProgress(report))
	}
	err = session.Run("sh -c " + dockerQuote(script))
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	var exit *ssh.ExitError
	if errors.As(err, &exit) {
		err = &DockerExit{Status: exit.ExitStatus()}
	}
	return output.String() + stderr.String(), err
}

func DockerEventLines(report func(DockerEvent)) (func(string), func()) {
	var pending strings.Builder
	tooLong := false
	consume := func() {
		if !tooLong {
			if event, ok := ParseDockerEvent(pending.String()); ok {
				report(event)
			}
		}
		pending.Reset()
		tooLong = false
	}
	return func(chunk string) {
		for part := range strings.SplitAfterSeq(chunk, "\n") {
			if pending.Len()+len(part) > bufio.MaxScanTokenSize {
				tooLong = true
			}
			if !tooLong {
				pending.WriteString(part)
			}
			if strings.HasSuffix(part, "\n") {
				consume()
			}
		}
	}, consume
}

const daemonPath = "/etc/docker/daemon.json"

const daemonGuard = `set -eu
export LC_ALL=C
exec 2>&1
asroot() { "$@"; }
fail() { printf '%s\n' "$*" >&2; exit 1; }
`

func (cmd DockerCmd) daemonHead() string {
	cmd.Sudo = false
	return daemonGuard + `
[ "$(uname -s)" = Linux ] || fail 'The system daemon configuration requires Linux.'
endpoint=$(` + cmd.Line("context", "inspect", "--format", "{{.Endpoints.docker.Host}}") + `) || fail 'Could not inspect the Docker context.'
case "$endpoint" in unix:///var/run/docker.sock|unix:///run/docker.sock) ;; *) fail 'The system daemon configuration requires the default local Docker socket.' ;; esac
[ -S "${endpoint#unix://}" ] || fail 'The default Docker socket was not found on the SSH host.'
pid= manager=
if command -v systemctl >/dev/null 2>&1 && [ -d /run/systemd/system ]; then
  pid=$(systemctl 'show' '--value' '--property=MainPID' 'docker.service' 2>/dev/null) || fail 'Could not identify docker.service.'
  manager=systemd
elif command -v rc-service >/dev/null 2>&1; then
  rc-service 'docker' 'status' >/dev/null 2>&1 || fail 'The Docker OpenRC service is not running.'
  pid=$(cat '/run/docker.pid' 2>/dev/null || cat '/var/run/docker.pid' 2>/dev/null) || fail 'Could not identify the Docker process.'
  manager=openrc
elif command -v service >/dev/null 2>&1; then
  service 'docker' 'status' >/dev/null 2>&1 || fail 'The Docker system service is not running.'
  pid=$(cat '/run/docker.pid' 2>/dev/null || cat '/var/run/docker.pid' 2>/dev/null) || fail 'Could not identify the Docker process.'
  manager=service
fi
case "$pid" in ''|*[!0-9]*) fail 'Could not identify the running Docker system service.' ;; esac
[ "$pid" -gt 1 ] || fail 'The Docker system service is not running.'
[ "$(cat "/proc/$pid/comm" 2>/dev/null)" = dockerd ] || fail 'The system service does not own a dockerd process.'
args=$(tr '\000' '\n' < "/proc/$pid/cmdline") || fail 'Could not inspect the Docker daemon arguments.'
printf '%s\n' "$args" | awk '
  pending { if ($0 != "/etc/docker/daemon.json") bad=1; pending=0; next }
  $0 == "--config-file" { pending=1; next }
  /^--config-file=/ && $0 != "--config-file=/etc/docker/daemon.json" { bad=1 }
  END { exit(bad || pending) }
' || fail 'The system Docker daemon uses a different configuration file.'
socket=$(readlink -f "${endpoint#unix://}") || fail 'Could not resolve the Docker socket.'
inode=$(awk -v socket="$socket" '$4 == "00010000" && ($8 == socket || $8 == "/run/docker.sock" || $8 == "/var/run/docker.sock") {print $7; exit}' /proc/net/unix)
case "$inode" in ''|*[!0-9]*) fail 'Could not identify the Docker socket.' ;; esac
if ! ls -l "/proc/$pid/fd" 2>/dev/null | grep -F "socket:[$inode]" >/dev/null &&
   ! asroot 'ls' '-l' "/proc/$pid/fd" 2>/dev/null | grep -F "socket:[$inode]" >/dev/null; then
  fail 'The system Docker service does not own the selected socket.'
fi
`
}

func (cmd DockerCmd) DaemonRead() string {
	return cmd.daemonHead() + `file='/etc/docker/daemon.json'
[ ! -L /etc/docker ] || fail 'Refusing to read a symbolic link.'
if [ -f "$file" ] && [ ! -L "$file" ] && cat "$file" 2>/dev/null; then exit 0; fi
if [ ! -e "$file" ] && [ ! -L "$file" ] && [ -x /etc ] && { [ ! -e /etc/docker ] || [ -x /etc/docker ]; }; then printf '\n'; exit 0; fi
asroot sh -c 'if [ -f "$1" ] && [ ! -L "$1" ]; then cat "$1"; elif [ ! -e "$1" ] && [ ! -L "$1" ]; then printf "\n"; else exit 1; fi' sh "$file"
`
}

func DaemonMirrors(raw string) ([]string, error) {
	if len(raw) > 256<<10 {
		return nil, ErrDockerResourceInput
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &doc); err != nil || doc == nil {
		return nil, ErrDockerResourceInput
	}
	var mirrors []string
	if entry, exists := doc["registry-mirrors"]; exists {
		if err := json.Unmarshal(entry, &mirrors); err != nil || mirrors == nil {
			return nil, ErrDockerResourceInput
		}
	}
	for _, mirror := range mirrors {
		parsed, err := url.Parse(mirror)
		if err != nil || parsed.Scheme != "https" || !ValidDockerRegistry(parsed.Host) || parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
			return nil, ErrDockerResourceInput
		}
	}
	return mirrors, nil
}

func WithDaemonMirrors(raw string, mirrors []string) (string, error) {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &doc); err != nil || doc == nil {
		return "", ErrDockerResourceInput
	}
	if len(mirrors) == 0 {
		delete(doc, "registry-mirrors")
	} else {
		doc["registry-mirrors"], _ = json.Marshal(mirrors)
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return "", err
	}
	_, err = DaemonMirrors(string(data))
	return string(data) + "\n", err
}

func (cmd DockerCmd) DaemonSave(original, raw string, now time.Time) (DockerInput, error) {
	if cmd.Tool != "docker" {
		return DockerInput{}, ErrDockerResourceUnsupported
	}
	if _, err := DaemonMirrors(raw); err != nil {
		return DockerInput{}, err
	}
	backup := daemonPath + ".bak-" + now.UTC().Format("20060102150405")
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(original)))
	return DockerInput{script: cmd.daemonHead() + "file=" + dockerQuote(daemonPath) + " backup=" + dockerQuote(backup) + " expected=" + dockerQuote(hash) + "\n" + `
asroot 'test' '!' '-L' '/etc/docker' || fail 'Refusing to write through a symbolic link.'
asroot 'test' '-d' '/etc/docker' || asroot 'install' '-d' '-m' '0755' '/etc/docker'
verify_config() {
  asroot 'test' '!' '-L' "$file" || fail 'Refusing to replace a symbolic link.'
  if asroot 'test' '-e' "$file"; then
    asroot 'test' '-f' "$file" || fail 'The daemon configuration is not a regular file.'
    current=$(asroot 'sha256sum' "$file")
  else
    current=$(printf '\n' | sha256sum)
  fi
  [ "${current%% *}" = "$expected" ] || fail 'The configuration changed on the host. Reload before saving.'
}
verify_config
tmp=$(asroot 'mktemp' '/etc/docker/.daemon.json.XXXXXXXX')
trap 'asroot rm -f -- "$tmp"' EXIT
asroot 'tee' "$tmp" >/dev/null
if ! asroot 'python3' '-c' 'import pathlib, subprocess, sys
proc = pathlib.Path("/proc") / sys.argv[1]
flags = (proc / "cmdline").read_bytes().split(b"\0")[1:]
args = []
skip = False
for arg in flags:
    if skip: skip = False; continue
    if arg == b"--config-file": skip = True; continue
    if not arg or arg.startswith(b"--config-file="): continue
    args.append(arg)
subprocess.run([str(proc / "exe"), *args, "--validate", "--config-file", sys.argv[2]], check=True, stdin=subprocess.DEVNULL, timeout=30)' "$pid" "$tmp" >/dev/null 2>&1; then
  fail 'Could not validate the configuration with the running Docker service flags. Python 3 and dockerd validation are required. Nothing was installed.'
fi
verify_config
if asroot 'test' '-f' "$file"; then
  asroot 'test' '!' '-e' "$backup" && asroot 'test' '!' '-L' "$backup" || fail 'The backup already exists.'
  asroot 'cp' '-p' '--' "$file" "$backup"
  printf '%s\n' "$backup"
fi
verify_config
asroot 'mv' '-f' '--' "$tmp" "$file"
`, input: raw}, nil
}

func (cmd DockerCmd) DaemonRestart(expectedConfig string) (string, error) {
	if cmd.Tool != "docker" {
		return "", ErrDockerResourceUnsupported
	}
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(expectedConfig)))
	return cmd.daemonHead() + "expected=" + dockerQuote(hash) + "\n" + `current=$(asroot 'sha256sum' '/etc/docker/daemon.json') || fail 'Could not read the saved configuration.'
[ "${current%% *}" = "$expected" ] || fail 'The configuration changed after saving. Restart was not attempted.'
case "$manager" in
  systemd) asroot 'systemctl' 'restart' 'docker.service' ;;
  openrc) asroot 'rc-service' 'docker' 'restart' ;;
  service) asroot 'service' 'docker' 'restart' ;;
  *) fail 'No supported Docker service manager was found.' ;;
esac
`, nil
}
