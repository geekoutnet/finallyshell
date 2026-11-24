package monitor

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

type DockerSubnet struct {
	Subnet, Gateway, IPRange string
}

type DockerNetworkSpec struct {
	Name, Driver, Options, Labels    string
	IPv4, IPv6                       DockerSubnet
	Internal, Attachable, EnableIPv6 bool
}

type DockerNetworkEndpoint struct {
	ID, Name, IPv4, IPv6, MAC string
}

type DockerNetworkDetails struct {
	ID, Name, Driver, Scope             string
	Created                             time.Time
	Internal, Attachable, Ingress, IPv6 bool
	IPAMDriver                          string
	IPAMOptions                         map[string]string
	Subnets                             []DockerSubnet
	Endpoints                           []DockerNetworkEndpoint
	Labels, Options                     map[string]string
}

func (network DockerNetworkDetails) Protected() bool {
	return network.Ingress || slices.Contains([]string{"bridge", "host", "none", "podman"}, network.Name)
}

type DockerVolumeDetails struct {
	Name, Driver, Scope, Mountpoint string
	Created                         time.Time
	Anonymous                       bool
	Labels, Options                 map[string]string
	CreatedRaw                      string
}

var dockerGeneratedVolumeName = regexp.MustCompile(`^[a-f0-9]{64}$`)
var dockerVolumeAPIPattern = regexp.MustCompile(`^([0-9]+)\.([0-9]+)$`)

type dockerResourceListRow struct {
	ID, Name string
}

func parseResourceList(output string) ([]dockerResourceListRow, error) {
	var rows []dockerResourceListRow
	if strings.HasPrefix(strings.TrimSpace(output), "[") {
		err := json.Unmarshal([]byte(output), &rows)
		return rows, err
	}
	for line := range strings.SplitSeq(output, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var row dockerResourceListRow
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			return nil, err
		}
		if !ValidName(row.Name) {
			return nil, ErrBadContainer
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func (cmd DockerCmd) NetworkList() string {
	if cmd.Tool == "podman" {
		return resourceReadHead + cmd.Line("network", "ls", "--format", "json") + "\n"
	}
	return resourceReadHead + cmd.Line("network", "ls", "--no-trunc", "--format", "{{json .}}") + "\n"
}

func (cmd DockerCmd) VolumeList() string {
	if cmd.Tool == "podman" {
		return resourceReadHead + cmd.Line("volume", "ls", "--format", "json") + "\n"
	}
	return resourceReadHead + cmd.Line("volume", "ls", "--format", "{{json .}}") + "\n"
}

func (cmd DockerCmd) UnusedVolumes() string {
	format := "{{json .}}"
	if cmd.Tool == "podman" {
		format = "json"
	}
	return resourceReadHead + cmd.Line("volume", "ls", "--filter", "dangling=true", "--format", format) + "\n"
}

func (cmd DockerCmd) LocalEngine() string {
	if cmd.Tool == "podman" {
		return resourceReadHead + `if [ -n "${CONTAINER_HOST:-}${CONTAINER_CONNECTION:-}" ]; then printf 'remote\n'; else printf 'local\n'; fi` + "\n"
	}
	return resourceReadHead + `endpoint=$(` + cmd.Line("context", "inspect", "--format", "{{.Endpoints.docker.Host}}") + `) || exit 1
case "$endpoint" in unix:///*) if test -S "${endpoint#unix://}"; then printf 'local\n'; else printf 'remote\n'; fi ;; *) printf 'remote\n' ;; esac
`
}

func DockerResourceRefs(output string, network bool) ([]string, error) {
	rows, err := parseResourceList(output)
	if err != nil {
		return nil, err
	}
	refs := make([]string, 0, len(rows))
	for _, row := range rows {
		ref := row.Name
		if network && row.ID != "" {
			ref = row.ID
		}
		if !ValidName(ref) {
			return nil, ErrBadContainer
		}
		refs = append(refs, ref)
	}
	return refs, nil
}

func (cmd DockerCmd) ResourceInspect(kind string, refs []string) (string, error) {
	if kind != "network" && kind != "volume" {
		return "", ErrDockerResourceInput
	}
	if err := checkRefs(refs); err != nil {
		return "", err
	}
	return resourceReadHead + cmd.Line(append([]string{kind, "inspect"}, refs...)...) + "\n", nil
}

func ParseDockerNetworkDetails(output string) ([]DockerNetworkDetails, error) {
	var rows []struct {
		ID, Name, Driver, Scope                   string
		Created                                   string
		Internal, Attachable, Ingress, EnableIPv6 bool
		PodmanIPv6Enabled                         bool `json:"ipv6_enabled"`
		Labels, Options                           map[string]string
		IPAM                                      struct {
			Driver  string
			Options map[string]string
			Config  []DockerSubnet
		}
		Subnets []struct {
			Subnet, Gateway string
			LeaseRange      struct {
				StartIP string `json:"start_ip"`
				EndIP   string `json:"end_ip"`
			} `json:"lease_range"`
		}
		PodmanIPAM map[string]string `json:"ipam_options"`
		Containers map[string]struct {
			Name, IPv4Address, IPv6Address, MacAddress string
			Interfaces                                 map[string]struct {
				MAC     string `json:"mac_address"`
				Subnets []struct {
					IP string `json:"ipnet"`
				}
			}
		}
	}
	if err := json.Unmarshal([]byte(output), &rows); err != nil {
		return nil, err
	}
	networks := make([]DockerNetworkDetails, 0, len(rows))
	for _, row := range rows {
		network := DockerNetworkDetails{ID: row.ID, Name: row.Name, Driver: row.Driver, Scope: row.Scope, Created: parseTime(row.Created),
			Internal: row.Internal, Attachable: row.Attachable, Ingress: row.Ingress, IPv6: row.EnableIPv6 || row.PodmanIPv6Enabled,
			IPAMDriver: row.IPAM.Driver, IPAMOptions: row.IPAM.Options, Subnets: row.IPAM.Config, Labels: row.Labels, Options: row.Options}
		if network.IPAMDriver == "" {
			network.IPAMDriver = row.PodmanIPAM["driver"]
		}
		for _, subnet := range row.Subnets {
			var ipRange string
			if subnet.LeaseRange.StartIP != "" {
				ipRange = subnet.LeaseRange.StartIP + " – " + subnet.LeaseRange.EndIP
			}
			network.Subnets = append(network.Subnets, DockerSubnet{subnet.Subnet, subnet.Gateway, ipRange})
		}
		for id, endpoint := range row.Containers {
			item := DockerNetworkEndpoint{id, endpoint.Name, endpoint.IPv4Address, endpoint.IPv6Address, endpoint.MacAddress}
			for _, iface := range endpoint.Interfaces {
				item.MAC = iface.MAC
				for _, subnet := range iface.Subnets {
					if strings.Contains(subnet.IP, ":") {
						item.IPv6 = subnet.IP
					} else {
						item.IPv4 = subnet.IP
					}
				}
			}
			network.Endpoints = append(network.Endpoints, item)
		}
		slices.SortFunc(network.Endpoints, func(left, right DockerNetworkEndpoint) int { return strings.Compare(left.Name, right.Name) })
		networks = append(networks, network)
	}
	return networks, nil
}

func ParseDockerVolumeDetails(output string) ([]DockerVolumeDetails, error) {
	var rows []struct {
		Name, Driver, Scope, Mountpoint, CreatedAt string
		Anonymous                                  bool
		Labels, Options                            map[string]string
	}
	if err := json.Unmarshal([]byte(output), &rows); err != nil {
		return nil, err
	}
	volumes := make([]DockerVolumeDetails, 0, len(rows))
	for _, row := range rows {
		_, anonymous := row.Labels["com.docker.volume.anonymous"]
		volumes = append(volumes, DockerVolumeDetails{Name: row.Name, Driver: row.Driver, Scope: row.Scope, Mountpoint: row.Mountpoint,
			Created: parseTime(row.CreatedAt), Anonymous: anonymous || row.Anonymous, Labels: row.Labels, Options: row.Options, CreatedRaw: row.CreatedAt})
	}
	return volumes, nil
}

func (volume DockerVolumeDetails) LooksAnonymous() bool {
	return volume.Anonymous || dockerGeneratedVolumeName.MatchString(volume.Name)
}

var (
	ErrDockerResourceInput       = errors.New("invalid Docker resource input")
	ErrDockerResourceUnsupported = errors.New("operation not supported by this engine")
)

const resourceReadHead = "export LC_ALL=C\n"

func resourceAction(cmd DockerCmd, args ...string) (string, error) {
	return scriptHead + cmd.Line(args...) + "\n", nil
}

func resourcePairs(args []string, flag, text string) ([]string, error) {
	keys := make(map[string]bool)
	for line := range strings.SplitSeq(text, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		key, _, found := strings.Cut(line, "=")
		if !found || !ValidName(key) || keys[key] || strings.ContainsAny(line, "\x00\r") {
			return nil, ErrDockerResourceInput
		}
		keys[key] = true
		args = append(args, flag, line)
	}
	return args, nil
}

func resourceSubnet(args []string, config DockerSubnet, ipv4 bool) ([]string, error) {
	if config == (DockerSubnet{}) {
		return args, nil
	}
	prefix, err := netip.ParsePrefix(config.Subnet)
	if err != nil || prefix.Addr().Is4() != ipv4 || prefix.Masked() != prefix || prefix.Addr().Is4In6() {
		return nil, ErrDockerResourceInput
	}
	args = append(args, "--subnet", config.Subnet)
	if config.Gateway != "" {
		addr, err := netip.ParseAddr(config.Gateway)
		if err != nil || addr.Zone() != "" || !prefix.Contains(addr) {
			return nil, ErrDockerResourceInput
		}
		args = append(args, "--gateway", config.Gateway)
	}
	if config.IPRange != "" {
		ipRange, err := netip.ParsePrefix(config.IPRange)
		if err != nil || ipRange.Masked() != ipRange || ipRange.Bits() < prefix.Bits() || !prefix.Contains(ipRange.Addr()) {
			return nil, ErrDockerResourceInput
		}
		args = append(args, "--ip-range", config.IPRange)
	}
	return args, nil
}

func (cmd DockerCmd) NetworkCreate(spec DockerNetworkSpec) (string, error) {
	if !ValidName(spec.Name) || !ValidName(spec.Driver) || (DockerNetworkDetails{Name: spec.Name}).Protected() || (spec.IPv6 != (DockerSubnet{}) && !spec.EnableIPv6) {
		return "", ErrDockerResourceInput
	}
	if cmd.Tool == "podman" && (spec.Attachable || spec.Driver == "overlay") {
		return "", ErrDockerResourceUnsupported
	}
	args := []string{"network", "create", "--driver", spec.Driver}
	for _, subnet := range []struct {
		config DockerSubnet
		ipv4   bool
	}{{spec.IPv4, true}, {spec.IPv6, false}} {
		var err error
		if args, err = resourceSubnet(args, subnet.config, subnet.ipv4); err != nil {
			return "", err
		}
	}
	if spec.Internal {
		args = append(args, "--internal")
	}
	if spec.Attachable {
		args = append(args, "--attachable")
	}
	if spec.EnableIPv6 {
		args = append(args, "--ipv6")
	}
	return resourceCreate(cmd, args, spec.Name, spec.Options, spec.Labels)
}

func resourceCreate(cmd DockerCmd, args []string, name, options, labels string) (string, error) {
	args, err := resourcePairs(args, "--opt", options)
	if err != nil {
		return "", err
	}
	args, err = resourcePairs(args, "--label", labels)
	if err != nil {
		return "", err
	}
	return resourceAction(cmd, append(args, name)...)
}

func (cmd DockerCmd) NetworkConnect(network, container, ipv4, aliases string) (string, error) {
	if err := checkRefs([]string{network, container}); err != nil {
		return "", err
	}
	args := []string{"network", "connect"}
	if ipv4 != "" {
		addr, err := netip.ParseAddr(ipv4)
		if err != nil || !addr.Is4() {
			return "", ErrDockerResourceInput
		}
		args = append(args, "--ip", ipv4)
	}
	for alias := range strings.FieldsFuncSeq(aliases, func(char rune) bool { return char == ',' || char == ' ' }) {
		if !ValidName(alias) {
			return "", ErrDockerResourceInput
		}
		args = append(args, "--alias", alias)
	}
	return resourceAction(cmd, append(args, network, container)...)
}

func (cmd DockerCmd) NetworkDisconnect(network, container string, force bool) (string, error) {
	if err := checkRefs([]string{network, container}); err != nil {
		return "", err
	}
	if force && cmd.Tool == "podman" {
		return "", ErrDockerResourceUnsupported
	}
	args := []string{"network", "disconnect"}
	if force {
		args = append(args, "--force")
	}
	return resourceAction(cmd, append(args, network, container)...)
}

func (cmd DockerCmd) NetworkRemove(networks ...DockerNetworkDetails) (string, error) {
	var refs []string
	for _, network := range networks {
		if network.Protected() || len(network.Endpoints) != 0 {
			return "", ErrDockerResourceInput
		}
		refs = append(refs, network.ID)
	}
	if err := checkRefs(refs); err != nil {
		return "", err
	}
	return resourceAction(cmd, append([]string{"network", "rm"}, refs...)...)
}

func (cmd DockerCmd) VolumeCreate(name, driver, options, labels string) (string, error) {
	if !ValidName(name) || !ValidName(driver) {
		return "", ErrDockerResourceInput
	}
	return resourceCreate(cmd, []string{"volume", "create", "--driver", driver}, name, options, labels)
}

func (cmd DockerCmd) VolumeRemove(force bool, volumes ...DockerVolumeDetails) (string, error) {
	if force && cmd.Tool == "podman" {
		return "", ErrDockerResourceUnsupported
	}
	if len(volumes) == 0 {
		return "", ErrDockerResourceInput
	}
	args := []string{"volume", "rm"}
	if force {
		args = append(args, "--force")
	}
	var script strings.Builder
	script.WriteString(scriptHead)
	script.WriteString("set -e\n")
	for _, volume := range volumes {
		if !ValidName(volume.Name) || !ValidName(volume.Driver) || volume.Created.IsZero() || volume.CreatedRaw == "" || strings.ContainsAny(volume.Mountpoint+volume.CreatedRaw, "\x00\r\n") {
			return "", ErrDockerResourceInput
		}
		created := "{{.CreatedAt}}"
		if cmd.Tool == "podman" {
			created = `{{.CreatedAt.Format "2006-01-02T15:04:05.999999999Z07:00"}}`
		}
		verify := cmd.Line("volume", "inspect", "--format", created+"\n{{.Driver}}\n{{.Mountpoint}}", volume.Name)
		expected := dockerQuote(volume.CreatedRaw + "\n" + volume.Driver + "\n" + volume.Mountpoint)
		script.WriteString("actual=$(" + verify + ")\n")
		script.WriteString("[ \"$actual\" = " + expected + " ] || { printf '%s\\n' " + dockerQuote("Volume changed since confirmation: "+volume.Name) + "; exit 1; }\n")
		script.WriteString(cmd.Line(append(args, volume.Name)...) + "\n")
	}
	return script.String(), nil
}

func (cmd DockerCmd) VolumePruneSupported(version string, apis ...string) bool {
	major, _, _ := strings.Cut(strings.TrimPrefix(version, "v"), ".")
	number, err := strconv.Atoi(major)
	if cmd.Tool != "docker" || err != nil || number < 23 {
		return false
	}
	for _, api := range apis {
		parts := dockerVolumeAPIPattern.FindStringSubmatch(api)
		if parts == nil {
			return false
		}
		apiMajor, _ := strconv.Atoi(parts[1])
		apiMinor, _ := strconv.Atoi(parts[2])
		if apiMajor < 1 || apiMajor == 1 && apiMinor < 42 {
			return false
		}
	}
	return true
}

func (cmd DockerCmd) ResourcePrune(kind string, all bool, version string) (string, error) {
	switch kind {
	case "container", "image", "network", "volume", "builder":
	default:
		return "", ErrDockerResourceInput
	}
	if (kind == "volume" && !cmd.VolumePruneSupported(version)) || (kind == "builder" && cmd.Tool == "podman") {
		return "", ErrDockerResourceUnsupported
	}
	if all && kind != "volume" && kind != "image" {
		return "", fmt.Errorf("%w: --all", ErrDockerResourceInput)
	}
	args := []string{kind, "prune", "--force"}
	if all {
		args = append(args, "--all")
	}
	if kind != "volume" {
		return resourceAction(cmd, args...)
	}
	if !all {
		args = append(args, "--filter", "all=false")
	}
	return scriptHead + cmd.volumePruneGuard(all) + cmd.Line(args...) + "\n", nil
}

func (cmd DockerCmd) volumePruneGuard(all bool) string {
	guard := `fail() { printf '%s\n' 'Volume prune requires Docker API 1.42+ and no configured all-volume prune filter.' >&2; exit 1; }
api=$(` + cmd.Line("version", "--format", "{{.Client.APIVersion}} {{.Server.APIVersion}}") + `) || fail
set -- $api
[ "$#" = 2 ] || fail
for version in "$1" "$2"; do
  major=${version%%.*} minor=${version#*.}
  case "$major:$minor" in *[!0-9:]*|:*|*:) fail ;; esac
  [ "$major" -gt 1 ] || { [ "$major" -eq 1 ] && [ "$minor" -ge 42 ]; } || fail
done
`
	if all {
		return guard
	}
	configUser := "sh -c "
	if cmd.Sudo {
		configUser = "sudo -n sh -c "
	}
	return guard + configUser + dockerQuote(`file="${DOCKER_CONFIG:-$HOME/.docker}/config.json"
if [ ! -e "$file" ]; then exit 0; fi
if command -v jq >/dev/null 2>&1; then
  exec jq -e 'all((.pruneFilters // [])[]; (type == "string" and (startswith("all=") | not)))' "$file" >/dev/null 2>&1
fi
if command -v python3 >/dev/null 2>&1; then
  exec python3 -c 'import json,sys
with open(sys.argv[1], encoding="utf-8") as f: c = json.load(f)
sys.exit(not all(isinstance(item, str) and not item.startswith("all=") for item in c.get("pruneFilters") or []))' "$file" 2>/dev/null
fi
exit 1`) + " || fail\n"
}
