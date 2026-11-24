package monitor

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// Docker is what the Overview tab shows of a server's containers: the
// engine, every container with its inspect data and live usage, and the
// images, volumes, networks and disk use. It is read once, by
// ReadDocker; Podman, where there is no docker command, is read the same
// way.
type Docker struct {
	Tool  string // "docker" or "podman"; "" when neither is installed
	Sudo  bool   // read with sudo -n, as the user could not reach the socket
	Root  bool
	State DockerState // whether the engine answered; the rest is empty unless DockerOK
	// Error is what the engine said when it did not answer.
	Error string

	Version       string // the engine's
	APIVersion    string
	OS            string // as the engine sees it: CentOS Linux 7 (Core)
	StorageDriver string
	CgroupDriver  string
	CgroupVersion string
	RootDir       string
	LoggingDriver string
	CPUs          int
	MemTotal      uint64
	Warnings      []string
	// The engine's counts, which are of every container and image even
	// when the lists below are cut short.
	NumContainers, Running, Paused, Stopped, NumImages int

	Containers []Container
	Images     []DockerImage
	Volumes    []DockerVolume
	Networks   []DockerNetwork
	Disk       []DockerDiskUsage
}

// DockerState is whether the engine answered.
type DockerState int

const (
	DockerOK     DockerState = iota
	DockerDenied             // permission denied even after escalation
	DockerDown               // the daemon does not run
	DockerFailed             // another error; Docker.Error says
	DockerNeedsRoot
)

// Container is a container as docker inspect and docker stats tell of
// it.
type Container struct {
	ID      string
	Name    string // without docker's leading /
	Image   string // as it was asked for: nginx:latest
	ImageID string // sha256:…

	Created, StartedAt, FinishedAt time.Time // zero when not known, or never

	Status     string // running, exited, paused, restarting, created, removing, dead
	Running    bool
	Paused     bool
	Restarting bool
	OOMKilled  bool
	ExitCode   int
	Error      string
	PID        int
	// Health is healthy, unhealthy or starting; "" for a container
	// without a health check.
	Health        string
	FailingStreak int
	HealthOutput  string // what the last health check printed

	RestartCount  int
	RestartPolicy string // no, always, unless-stopped, on-failure
	MaxRetries    int    // of on-failure
	AutoRemove    bool

	Command    []string // Cmd
	Entrypoint []string
	Env        []string // NAME=value
	WorkingDir string
	User       string
	Hostname   string
	Labels     map[string]string
	Privileged bool
	ReadOnly   bool // the root file system
	CapAdd     []string

	NetworkMode string
	Networks    []ContainerNetwork // by name
	Ports       []PortBinding      // published, by container port
	Exposed     []string           // exposed but not published: 80/tcp
	Mounts      []Mount

	NanoCPUs  int64 // CPU limit, in billionths of a CPU; 0 for none
	CPUShares int64
	CPUQuota  int64 // µs per CPUPeriod
	CPUPeriod int64
	CPUSet    string
	Memory    int64 // limit in bytes, 0 for none
	MemSwap   int64
	MemResv   int64
	PidsLimit int64

	LogDriver  string
	LogOptions map[string]string

	// Compose's labels, when the container is a service's.
	Project, Service, ComposeDir, ComposeFiles string

	Stats *ContainerStats // nil when docker stats did not list it (not running)

	// Inspect is the container's docker inspect output, as the engine
	// wrote it.
	Inspect json.RawMessage
}

// ContainerNetwork is a network a container is on, with its addresses.
type ContainerNetwork struct {
	Name          string
	IP            string
	PrefixLen     int
	Gateway       string
	IPv6          string
	IPv6PrefixLen int
	IPv6Gateway   string
	MAC           string
	Aliases       []string
}

// PortBinding is a container's port published on the host. A port
// published on both 0.0.0.0 and ::, as docker does by default, is one
// PortBinding with HostIP 0.0.0.0 and AlsoIPv6 set.
type PortBinding struct {
	Port     int    // the container's
	Proto    string // tcp, udp, sctp
	HostIP   string // "" for every address
	HostPort int
	AlsoIPv6 bool
}

// Mount is a volume or folder mounted in a container.
type Mount struct {
	Type        string // bind, volume, tmpfs
	Name        string // a volume's
	Source      string
	Destination string
	Mode        string
	RW          bool
}

// ContainerStats is a running container's usage, from docker stats.
type ContainerStats struct {
	CPU        float64 // percent of one CPU
	MemUsed    uint64
	MemLimit   uint64
	MemPercent float64
	NetRx      uint64
	NetTx      uint64
	BlockRead  uint64
	BlockWrite uint64
	PIDs       int
}

// DockerImage is a line of docker images.
type DockerImage struct {
	ID         string
	Repository string
	Tag        string
	Size       uint64
	Created    string // as docker says it: 5 hours ago
}

// DockerVolume is a line of docker volume ls.
type DockerVolume struct {
	Name       string
	Driver     string
	Mountpoint string
}

// DockerNetwork is a line of docker network ls.
type DockerNetwork struct {
	ID       string
	Name     string
	Driver   string
	Scope    string
	Internal bool
	IPv6     bool
}

// DockerDiskUsage is a line of docker system df: the space of images,
// containers, local volumes or the build cache, as docker writes it.
type DockerDiskUsage struct {
	Type        string // Images, Containers, Local Volumes, Build Cache
	Total       string
	Active      string
	Size        string
	Reclaimable string // 10.58GB (30%)
}

// dockerMark starts each section of dockerScript's output; procSections
// splits it.
const dockerMark = procMark

// dockerScript reads everything in one round trip. It is POSIX sh. Every
// part may fail and leave its section empty. When the user may not use
// the socket, the engine is asked again with sudo -n, which never asks
// for a password. timeout, where there is one, keeps a stuck engine from
// hanging docker stats or system df past the app's own timeout, which
// would lose all the rest.
const dockerScript = `d=
if command -v docker >/dev/null 2>&1; then d=docker
elif command -v podman >/dev/null 2>&1; then d=podman; fi
` + dockerReadHead + `
if [ -n "$d" ]; then
  info=$($t "$d" 'info' '--format' '{{json .}}' 2>&1); st=$?
  if [ $st -ne 0 ] && $t sudo '-n' "$d" 'info' '--format' '{{json .}}' >/dev/null 2>&1; then
    m sudo; echo yes
    engine() { $t sudo '-n' "$d" "$@"; }
    info=$(engine 'info' '--format' '{{json .}}' 2>&1); st=$?
  fi
` + dockerReadBody

const dockerReadHead = `export LC_ALL=C
m() { echo "` + dockerMark + `$1---"; }
engine() { $t "$d" "$@"; }
m tool; echo "$d"
t=
if [ -n "$d" ]; then timeout 5 true >/dev/null 2>&1 && t="timeout 20"; fi
`

const dockerReadBody = `
  m info; printf '%s\n' "$info"
  m status; echo $st
  if [ $st -eq 0 ]; then
    m version; engine 'version' '--format' '{{json .}}' 2>/dev/null
    ids=$(engine 'ps' '-aq' '--no-trunc' 2>/dev/null)
    set -f
    set --
    for id in $ids; do
      case "$id" in *[!0-9a-f]*) continue ;; esac
      set -- "$@" "$id"
    done
    m inspect; [ "$#" -eq 0 ] || engine 'inspect' '--format' '{{json .}}' "$@" 2>/dev/null
    m images; engine 'images' '--format' '{{json .}}' 2>/dev/null
    m volumes; engine 'volume' 'ls' '--format' '{{json .}}' 2>/dev/null
    m networks; engine 'network' 'ls' '--format' '{{json .}}' 2>/dev/null
    m stats; [ "$#" -eq 0 ] || engine 'stats' '--no-stream' '--no-trunc' '--format' '{{json .}}' 2>/dev/null
    m df; engine 'system' 'df' '--format' '{{json .}}' 2>/dev/null
  fi
fi
m end
`

// ReadDocker runs the script once over a new exec channel of client, and
// stops it when ctx ends. On an error, what the output had until then
// is returned with it: at least whether Docker is installed.
func ReadDocker(ctx context.Context, client *ssh.Client) (Docker, error) {
	return ReadDockerWith(ctx, func(ctx context.Context, script string) (string, string, error) {
		return RunScript(ctx, client, script)
	})
}

func ReadDockerWith(ctx context.Context, run func(context.Context, string) (string, string, error)) (Docker, error) {
	d, err := readDockerScript(ctx, dockerScript, run)
	if d.State == DockerDenied {
		d.State = DockerNeedsRoot
	}
	return d, err
}

func ReadDockerRoot(ctx context.Context, tool string, run func(context.Context, string) (string, string, error)) (Docker, error) {
	if tool != "docker" && tool != "podman" {
		return Docker{}, ErrDockerResourceUnsupported
	}
	script := "d=" + dockerQuote(tool) + "\n" + dockerReadHead + `
if [ -n "$d" ]; then
  info=$(engine 'info' '--format' '{{json .}}' 2>&1); st=$?
` + dockerReadBody
	d, err := readDockerScript(ctx, script, run)
	if err == nil {
		if d.Tool != tool {
			return d, ErrScriptOutput
		}
		d.Root, d.Sudo = d.State != DockerDenied, false
	}
	return d, err
}

func readDockerScript(ctx context.Context, script string, run func(context.Context, string) (string, string, error)) (Docker, error) {
	out, _, err := run(ctx, script)
	out = strings.ReplaceAll(out, "\r\n", "\n")
	d := ParseDocker(out)
	if ctx.Err() != nil {
		return d, ctx.Err()
	}
	if !strings.Contains(out, dockerMark+"end---") {
		if err != nil {
			return d, err
		}
		return d, ErrScriptOutput
	}
	return d, nil
}

// ParseDocker reads the output of the script.
func ParseDocker(out string) Docker {
	sec := procSections(out)
	d := Docker{Tool: strings.TrimSpace(sec["tool"]), Sudo: strings.TrimSpace(sec["sudo"]) == "yes"}
	if d.Tool == "" {
		return d
	}
	info, msg := parseDockerInfo(sec["info"])
	status := strings.TrimSpace(sec["status"])
	if status != "0" || len(info.ServerErrors) > 0 {
		if len(info.ServerErrors) > 0 {
			msg = append(info.ServerErrors, msg...)
		}
		d.State, d.Error = dockerError(strings.Join(msg, "\n"))
		if d.Tool == "podman" && strings.Contains(strings.ToLower(d.Error), "cannot clone: operation not permitted") {
			d.State = DockerDenied
		}
		return d
	}
	info.apply(&d)
	parseDockerVersion(sec["version"], &d)
	d.Containers = parseInspect(sec["inspect"])
	addStats(d.Containers, sec["stats"])
	d.Images = parseImages(sec["images"])
	jsonLines(sec["volumes"], func(v *DockerVolume) bool { return v.Name != "" }, &d.Volumes)
	d.Networks = parseNetworks(sec["networks"])
	d.Disk = parseDiskUsage(sec["df"])
	return d
}

// dockerError tells the engine's error by what it says.
func dockerError(msg string) (DockerState, string) {
	msg = strings.TrimSpace(msg)
	low := strings.ToLower(msg)
	switch {
	case strings.Contains(low, "permission denied"):
		return DockerDenied, msg
	case strings.Contains(low, "cannot connect to the docker daemon"),
		strings.Contains(low, "is the docker daemon running"),
		strings.Contains(low, "connection refused"),
		strings.Contains(low, "cannot connect to podman"),
		strings.Contains(low, "no such file or directory") && strings.Contains(low, ".sock"):
		return DockerDown, msg
	}
	if msg == "" {
		msg = "docker info failed"
	}
	return DockerFailed, msg
}

// dockerInfo is docker info's JSON, and podman info's, whose parts are
// under host, store and version. encoding/json matches the names without
// case, so Host is host.
type dockerInfo struct {
	ServerVersion     string
	OperatingSystem   string
	Driver            string
	CgroupDriver      string
	CgroupVersion     string
	DockerRootDir     string
	LoggingDriver     string
	NCPU              int
	MemTotal          uint64
	Containers        int
	ContainersRunning int
	ContainersPaused  int
	ContainersStopped int
	Images            int
	Warnings          []string
	ServerErrors      []string

	Host *struct {
		CPUs          int
		MemTotal      uint64
		CgroupManager string
		CgroupVersion string
		Distribution  struct{ Distribution, Version string }
	}
	Store *struct {
		GraphDriverName string
		GraphRoot       string
		ContainerStore  struct{ Number, Paused, Running, Stopped int }
		ImageStore      struct{ Number int }
	}
	Version *struct{ Version, APIVersion string }
}

// parseDockerInfo reads the info section: the JSON, and the other lines,
// which are errors.
func parseDockerInfo(s string) (dockerInfo, []string) {
	var info dockerInfo
	var msg []string
	for _, l := range lines(s) {
		if strings.HasPrefix(l, "{") && json.Unmarshal([]byte(l), &info) == nil {
			continue
		}
		msg = append(msg, l)
	}
	return info, msg
}

func (info *dockerInfo) apply(d *Docker) {
	d.Version, d.OS = info.ServerVersion, info.OperatingSystem
	d.StorageDriver, d.CgroupDriver, d.CgroupVersion = info.Driver, info.CgroupDriver, info.CgroupVersion
	d.RootDir, d.LoggingDriver = info.DockerRootDir, info.LoggingDriver
	d.CPUs, d.MemTotal, d.Warnings = info.NCPU, info.MemTotal, info.Warnings
	d.NumContainers, d.Running, d.Paused, d.Stopped = info.Containers, info.ContainersRunning, info.ContainersPaused, info.ContainersStopped
	d.NumImages = info.Images
	if h := info.Host; h != nil {
		d.CPUs, d.MemTotal = h.CPUs, h.MemTotal
		d.CgroupDriver, d.CgroupVersion = h.CgroupManager, h.CgroupVersion
		d.OS = strings.TrimSpace(h.Distribution.Distribution + " " + h.Distribution.Version)
	}
	if st := info.Store; st != nil {
		d.StorageDriver, d.RootDir = st.GraphDriverName, st.GraphRoot
		cs := st.ContainerStore
		d.NumContainers, d.Running, d.Paused, d.Stopped = cs.Number, cs.Running, cs.Paused, cs.Stopped
		d.NumImages = st.ImageStore.Number
	}
	if v := info.Version; v != nil {
		d.Version, d.APIVersion = v.Version, v.APIVersion
	}
}

// parseDockerVersion reads docker version's JSON for the API version, and
// the engine's version when info had none.
func parseDockerVersion(s string, d *Docker) {
	var v struct {
		Client, Server *struct{ Version, APIVersion string }
	}
	if json.Unmarshal([]byte(firstLine(s)), &v) != nil {
		return
	}
	for _, p := range []*struct{ Version, APIVersion string }{v.Server, v.Client} {
		if p == nil {
			continue
		}
		if d.Version == "" {
			d.Version = p.Version
		}
		if d.APIVersion == "" {
			d.APIVersion = p.APIVersion
		}
	}
}

// strList is a list of strings that may come as one string, as an older
// Podman writes Entrypoint.
type strList []string

func (l *strList) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		return nil
	}
	var s string
	if json.Unmarshal(b, &s) == nil {
		*l = strList(strings.Fields(s))
		return nil
	}
	var list []string
	err := json.Unmarshal(b, &list)
	*l = list
	return err
}

type portJSON struct {
	HostIP   string `json:"HostIp"`
	HostPort string
}

type healthJSON struct {
	Status        string
	FailingStreak int
	Log           []struct{ Output string }
}

// inspectJSON is the part of docker inspect's JSON the tab shows.
type inspectJSON struct {
	ID           string `json:"Id"`
	Name         string
	Created      string
	Image        string
	RestartCount int
	State        struct {
		Status                                 string
		Running, Paused, Restarting, OOMKilled bool
		Pid, ExitCode                          int
		Error                                  string
		StartedAt, FinishedAt                  string
		Health                                 *healthJSON
		Healthcheck                            *healthJSON // Podman before 4
	}
	HostConfig struct {
		NetworkMode    string
		Privileged     bool
		ReadonlyRootfs bool
		AutoRemove     bool
		CapAdd         strList
		RestartPolicy  struct {
			Name              string
			MaximumRetryCount int
		}
		PortBindings                             map[string][]portJSON
		NanoCpus, CpuShares, CpuQuota, CpuPeriod int64
		CpusetCpus                               string
		Memory, MemorySwap, MemoryReservation    int64
		PidsLimit                                *int64
		LogConfig                                struct {
			Type   string
			Config map[string]string
		}
	}
	Config struct {
		Hostname, User, WorkingDir, Image string
		Cmd, Entrypoint                   strList
		Env                               []string
		Labels                            map[string]string
		ExposedPorts                      map[string]struct{}
	}
	NetworkSettings struct {
		Ports    map[string][]portJSON
		Networks map[string]struct {
			IPAddress           string
			IPPrefixLen         int
			Gateway             string
			GlobalIPv6Address   string
			GlobalIPv6PrefixLen int
			IPv6Gateway         string
			MacAddress          string
			Aliases             []string
		}
	}
	Mounts []struct {
		Type, Name, Source, Destination, Mode string
		RW                                    bool
	}
}

// parseInspect reads docker inspect's output: one JSON object a line, as
// --format '{{json .}}' writes them, or one array of them. A container
// that does not parse is left out.
func parseInspect(s string) []Container {
	var raws []json.RawMessage
	if t := strings.TrimSpace(s); strings.HasPrefix(t, "[") {
		json.Unmarshal([]byte(t), &raws)
	} else {
		forLines(s, func(l []byte) {
			raws = append(raws, json.RawMessage(l))
		})
	}
	var out []Container
	for _, raw := range raws {
		var j inspectJSON
		if json.Unmarshal(raw, &j) != nil || j.ID == "" {
			continue
		}
		out = append(out, j.container(raw))
	}
	return out
}

// forLines calls fn with each line of s that holds a JSON object.
func forLines(s string, fn func([]byte)) {
	sc := bufio.NewScanner(strings.NewReader(s))
	sc.Buffer(make([]byte, 64<<10), procOutputLimit)
	for sc.Scan() {
		if l := bytes.TrimSpace(sc.Bytes()); len(l) > 0 && l[0] == '{' {
			fn(bytes.Clone(l))
		}
	}
}

// jsonLines decodes each line of s holding a JSON object into a T, and
// appends those keep accepts to out.
func jsonLines[T any](s string, keep func(*T) bool, out *[]T) {
	forLines(s, func(l []byte) {
		var v T
		if json.Unmarshal(l, &v) == nil && keep(&v) {
			*out = append(*out, v)
		}
	})
}

func parseTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil || t.Year() <= 1 {
		return time.Time{}
	}
	return t
}

func (j *inspectJSON) container(raw json.RawMessage) Container {
	st, hc, cfg := &j.State, &j.HostConfig, &j.Config
	c := Container{
		ID: j.ID, Name: strings.TrimPrefix(j.Name, "/"), Image: cfg.Image, ImageID: j.Image,
		Created: parseTime(j.Created), StartedAt: parseTime(st.StartedAt), FinishedAt: parseTime(st.FinishedAt),
		Status: st.Status, Running: st.Running, Paused: st.Paused, Restarting: st.Restarting, OOMKilled: st.OOMKilled,
		ExitCode: st.ExitCode, Error: st.Error, PID: st.Pid,
		RestartCount: j.RestartCount, RestartPolicy: hc.RestartPolicy.Name, MaxRetries: hc.RestartPolicy.MaximumRetryCount,
		AutoRemove: hc.AutoRemove,
		Command:    cfg.Cmd, Entrypoint: cfg.Entrypoint, Env: cfg.Env, WorkingDir: cfg.WorkingDir, User: cfg.User,
		Hostname: cfg.Hostname, Labels: cfg.Labels, Privileged: hc.Privileged, ReadOnly: hc.ReadonlyRootfs, CapAdd: hc.CapAdd,
		NetworkMode: hc.NetworkMode,
		NanoCPUs:    hc.NanoCpus, CPUShares: hc.CpuShares, CPUQuota: hc.CpuQuota, CPUPeriod: hc.CpuPeriod, CPUSet: hc.CpusetCpus,
		Memory: hc.Memory, MemSwap: hc.MemorySwap, MemResv: hc.MemoryReservation,
		LogDriver: hc.LogConfig.Type, LogOptions: hc.LogConfig.Config,
		Inspect: raw,
	}
	if c.Status == "" { // an engine that only has the flags
		switch {
		case st.Paused:
			c.Status = "paused"
		case st.Restarting:
			c.Status = "restarting"
		case st.Running:
			c.Status = "running"
		default:
			c.Status = "exited"
		}
	}
	if hc.PidsLimit != nil && *hc.PidsLimit > 0 {
		c.PidsLimit = *hc.PidsLimit
	}
	h := st.Health
	if h == nil {
		h = st.Healthcheck
	}
	if h != nil && h.Status != "" && h.Status != "none" {
		c.Health, c.FailingStreak = h.Status, h.FailingStreak
		if n := len(h.Log); n > 0 {
			c.HealthOutput = strings.TrimSpace(h.Log[n-1].Output)
		}
	}
	if l := c.Labels; l != nil {
		c.Project = l["com.docker.compose.project"]
		if c.Project == "" {
			c.Project = l["io.podman.compose.project"]
		}
		c.Service = l["com.docker.compose.service"]
		c.ComposeDir = l["com.docker.compose.project.working_dir"]
		c.ComposeFiles = l["com.docker.compose.project.config_files"]
	}
	for name, n := range j.NetworkSettings.Networks {
		c.Networks = append(c.Networks, ContainerNetwork{
			Name: name, IP: n.IPAddress, PrefixLen: n.IPPrefixLen, Gateway: n.Gateway,
			IPv6: n.GlobalIPv6Address, IPv6PrefixLen: n.GlobalIPv6PrefixLen, IPv6Gateway: n.IPv6Gateway,
			MAC: n.MacAddress, Aliases: n.Aliases,
		})
	}
	slices.SortFunc(c.Networks, func(a, b ContainerNetwork) int { return strings.Compare(a.Name, b.Name) })
	// The ports as they are now; a container that does not run has none,
	// and shows those it is set to publish.
	ports := j.NetworkSettings.Ports
	if !hasBindings(ports) {
		for k, v := range hc.PortBindings {
			if ports == nil {
				ports = map[string][]portJSON{}
			}
			ports[k] = v
		}
	}
	c.Ports = mergePorts(ports)
	published := map[string]bool{}
	for k, v := range ports {
		if len(v) > 0 {
			published[k] = true
		}
	}
	for k := range cfg.ExposedPorts {
		if !published[k] {
			c.Exposed = append(c.Exposed, k)
		}
	}
	for k, v := range ports {
		if _, ok := cfg.ExposedPorts[k]; !ok && len(v) == 0 {
			c.Exposed = append(c.Exposed, k)
		}
	}
	slices.SortFunc(c.Exposed, comparePortKeys)
	for _, m := range j.Mounts {
		c.Mounts = append(c.Mounts, Mount{Type: m.Type, Name: m.Name, Source: m.Source, Destination: m.Destination, Mode: m.Mode, RW: m.RW})
	}
	return c
}

func hasBindings(ports map[string][]portJSON) bool {
	for _, v := range ports {
		if len(v) > 0 {
			return true
		}
	}
	return false
}

// splitPortKey splits 80/tcp into 80 and tcp; a key without a protocol is
// tcp.
func splitPortKey(k string) (int, string) {
	p, proto, ok := strings.Cut(k, "/")
	if !ok {
		proto = "tcp"
	}
	n, _ := strconv.Atoi(p)
	return n, proto
}

func comparePortKeys(a, b string) int {
	pa, qa := splitPortKey(a)
	pb, qb := splitPortKey(b)
	if pa != pb {
		return pa - pb
	}
	return strings.Compare(qa, qb)
}

// mergePorts lists the bindings by container port, then host port, a
// port on both 0.0.0.0 and :: once.
func mergePorts(ports map[string][]portJSON) []PortBinding {
	var out []PortBinding
	for k, binds := range ports {
		port, proto := splitPortKey(k)
		for _, b := range binds {
			hp, _ := strconv.Atoi(b.HostPort)
			out = append(out, PortBinding{Port: port, Proto: proto, HostIP: b.HostIP, HostPort: hp})
		}
	}
	slices.SortFunc(out, func(a, b PortBinding) int {
		if a.Port != b.Port {
			return a.Port - b.Port
		}
		if a.Proto != b.Proto {
			return strings.Compare(a.Proto, b.Proto)
		}
		if a.HostPort != b.HostPort {
			return a.HostPort - b.HostPort
		}
		return strings.Compare(a.HostIP, b.HostIP)
	})
	// 0.0.0.0 sorts before ::, so the IPv4 binding is kept first.
	kept := out[:0]
	for _, p := range out {
		if p.HostIP == "::" {
			i := slices.IndexFunc(kept, func(q PortBinding) bool {
				return q.Port == p.Port && q.Proto == p.Proto && q.HostPort == p.HostPort && q.HostIP == "0.0.0.0"
			})
			if i >= 0 {
				kept[i].AlsoIPv6 = true
				continue
			}
		}
		kept = append(kept, p)
	}
	return kept
}

// statsJSON is a line of docker stats; Podman's PIDS is PIDs.
type statsJSON struct {
	ID, Container, Name                        string
	CPUPerc, MemUsage, MemPerc, NetIO, BlockIO string
	PIDs                                       json.RawMessage
}

// addStats gives each container its line of docker stats, found by its
// ID, which may be cut short, or its name.
func addStats(cs []Container, s string) {
	forLines(s, func(l []byte) {
		var j statsJSON
		if json.Unmarshal(l, &j) != nil {
			return
		}
		id := j.ID
		if id == "" {
			id = j.Container
		}
		i := slices.IndexFunc(cs, func(c Container) bool {
			return id != "" && strings.HasPrefix(c.ID, id) || j.Name != "" && c.Name == j.Name
		})
		if i < 0 {
			return
		}
		st := &ContainerStats{CPU: parsePercent(j.CPUPerc), MemPercent: parsePercent(j.MemPerc)}
		st.MemUsed, st.MemLimit = parseSizePair(j.MemUsage)
		st.NetRx, st.NetTx = parseSizePair(j.NetIO)
		st.BlockRead, st.BlockWrite = parseSizePair(j.BlockIO)
		var pids string
		if json.Unmarshal(j.PIDs, &pids) != nil {
			pids = string(j.PIDs)
		}
		st.PIDs, _ = strconv.Atoi(strings.TrimSpace(pids))
		cs[i].Stats = st
	})
}

// parsePercent reads 12.34%; "--" and the like are 0.
func parsePercent(s string) float64 {
	f, _ := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "%")), 64)
	return f
}

// parseSizePair reads "93.18MiB / 53.9GiB".
func parseSizePair(s string) (uint64, uint64) {
	a, b, _ := strings.Cut(s, "/")
	return ParseSize(a), ParseSize(b)
}

// ParseSize reads a size as docker and Podman write it: 0B, 1.04MB,
// 93.18MiB, 3.4 kB. The decimal units are of 1000, the binary ones of
// 1024; 0 when it is not a size.
func ParseSize(s string) uint64 {
	s = strings.TrimSpace(s)
	i := 0
	for i < len(s) && (s[i] >= '0' && s[i] <= '9' || s[i] == '.') {
		i++
	}
	f, err := strconv.ParseFloat(s[:i], 64)
	if err != nil {
		return 0
	}
	unit := strings.ToLower(strings.TrimSpace(s[i:]))
	mult := 1.0
	switch strings.TrimSuffix(unit, "b") {
	case "":
	case "k":
		mult = 1e3
	case "m":
		mult = 1e6
	case "g":
		mult = 1e9
	case "t":
		mult = 1e12
	case "p":
		mult = 1e15
	case "ki":
		mult = 1 << 10
	case "mi":
		mult = 1 << 20
	case "gi":
		mult = 1 << 30
	case "ti":
		mult = 1 << 40
	case "pi":
		mult = 1 << 50
	default:
		return 0
	}
	return uint64(f*mult + 0.5)
}

func parseImages(s string) []DockerImage {
	var out []DockerImage
	forLines(s, func(l []byte) {
		var j struct {
			ID, Repository, Tag, Size, CreatedSince string
		}
		if json.Unmarshal(l, &j) != nil || j.ID == "" {
			return
		}
		out = append(out, DockerImage{ID: j.ID, Repository: j.Repository, Tag: j.Tag, Size: ParseSize(j.Size), Created: j.CreatedSince})
	})
	return out
}

// flexBool is a bool docker writes as "true".
type flexBool bool

func (b *flexBool) UnmarshalJSON(data []byte) error {
	s := strings.Trim(string(data), `"`)
	*b = flexBool(s == "true")
	return nil
}

func parseNetworks(s string) []DockerNetwork {
	var out []DockerNetwork
	forLines(s, func(l []byte) {
		var j struct {
			ID, Name, Driver, Scope string
			Internal, IPv6          flexBool
			IPv6Enabled             flexBool `json:"ipv6_enabled"`
		}
		if json.Unmarshal(l, &j) != nil || j.Name == "" {
			return
		}
		out = append(out, DockerNetwork{ID: j.ID, Name: j.Name, Driver: j.Driver, Scope: j.Scope, Internal: bool(j.Internal), IPv6: bool(j.IPv6 || j.IPv6Enabled)})
	})
	return out
}

func parseDiskUsage(s string) []DockerDiskUsage {
	var out []DockerDiskUsage
	forLines(s, func(l []byte) {
		var j struct {
			Type, TotalCount, Total, Active, Size, Reclaimable json.RawMessage
		}
		if json.Unmarshal(l, &j) != nil {
			return
		}
		str := func(r json.RawMessage) string {
			var s string
			if json.Unmarshal(r, &s) == nil {
				return s
			}
			return strings.TrimSpace(string(r))
		}
		u := DockerDiskUsage{Type: str(j.Type), Total: str(j.TotalCount), Active: str(j.Active), Size: str(j.Size), Reclaimable: str(j.Reclaimable)}
		if u.Total == "" {
			u.Total = str(j.Total)
		}
		if u.Type != "" {
			out = append(out, u)
		}
	})
	return out
}
