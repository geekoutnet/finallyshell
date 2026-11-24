package monitor

import (
	"fmt"
	"math"
	"net"
	"net/netip"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

type CtrPort struct{ IP, Host, Container, Protocol string }

type CtrMount struct {
	Source, Target string
	ReadOnly       bool
}

type CtrDevice struct{ Source, Target, Permissions string }

type CtrConfig struct {
	Image, Name, WorkDir, User, Hostname                       string
	Network, IPv4, Restart, CPUs, Memory, MemorySwap, Shares   string
	LogDriver                                                  string
	Command, Entrypoint, Env, Labels, Aliases, CapAdd, CapDrop []string
	LogOptions                                                 []string
	Ports                                                      []CtrPort
	Mounts                                                     []CtrMount
	Devices                                                    []CtrDevice
	Pull, PublishAll, Privileged, AutoRemove, Init, TTY, Stdin bool
}

type CtrConfigError string

func (e CtrConfigError) Error() string { return "invalid container setting: " + string(e) }

var (
	ctrImageRef = regexp.MustCompile(`^(?:[A-Za-z0-9][A-Za-z0-9.-]*(?::[0-9]+)?/)?(?:[a-z0-9]+(?:[._-]+[a-z0-9]+)*/)*[a-z0-9]+(?:[._-]+[a-z0-9]+)*(?::[A-Za-z0-9_][A-Za-z0-9_.-]{0,127})?(?:@sha256:[a-f0-9]{64})?$`)
	ctrEnvKey   = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	ctrBytes    = regexp.MustCompile(`^([0-9]+)([kmgtp]?)(?:b)?$`)
	ctrCPU      = regexp.MustCompile(`^[0-9]+(?:\.[0-9]{1,9})?$`)
)

func validCtrImage(image string) bool {
	return ctrImageRef.MatchString(image) || strings.HasPrefix(image, "sha256:") && len(image) == 71 && isCtrHex(image[7:])
}

func isCtrHex(text string) bool {
	return strings.Trim(text, "0123456789abcdef") == ""
}

func ctrPath(value string) bool {
	return strings.HasPrefix(value, "/") && !strings.ContainsRune(value, 0)
}

func (port CtrPort) key() string { return port.Container + "/" + port.Protocol }

func (port CtrPort) publish() string {
	if port.IP != "" {
		return net.JoinHostPort(port.IP, port.Host) + ":" + port.key()
	}
	if port.Host != "" {
		return port.Host + ":" + port.key()
	}
	return port.key()
}

func ctrPortNumber(value string) bool {
	number, err := strconv.ParseUint(value, 10, 16)
	return err == nil && number > 0 && strconv.FormatUint(number, 10) == value
}

func ctrPairs(values []string, validKey func(string) bool) (map[string]string, bool) {
	out := make(map[string]string, len(values))
	for _, value := range values {
		key, val, found := strings.Cut(value, "=")
		if _, duplicate := out[key]; duplicate || !found || !validKey(key) || strings.ContainsRune(value, 0) {
			return nil, false
		}
		out[key] = val
	}
	return out, true
}

func ctrRestart(value string) (string, int, bool) {
	if value == "" {
		return "no", 0, true
	}
	policy, retries, hasRetries := strings.Cut(value, ":")
	if !slices.Contains(RestartPolicies, policy) {
		return "", 0, false
	}
	if !hasRetries {
		return policy, 0, true
	}
	count, err := strconv.ParseUint(retries, 10, 31)
	return policy, int(count), policy == "on-failure" && err == nil
}

func ctrCPUValue(value string) (int64, bool) {
	if value == "" {
		return 0, true
	}
	count, err := strconv.ParseFloat(value, 64)
	return int64(math.Round(count * 1e9)), err == nil && ctrCPU.MatchString(value) && count < float64(math.MaxInt64)/1e9
}

func ctrByteValue(value string, swap bool) (int64, bool) {
	if value == "" {
		return 0, true
	}
	if swap && value == "-1" {
		return -1, true
	}
	parts := ctrBytes.FindStringSubmatch(strings.ToLower(value))
	if parts == nil {
		return 0, false
	}
	count, err := strconv.ParseUint(parts[1], 10, 63)
	shift := 0
	if parts[2] != "" {
		shift = 10 * (strings.Index("kmgtp", parts[2]) + 1)
	}
	return int64(count << shift), err == nil && count <= math.MaxInt64>>shift
}

func (config CtrConfig) validate() error {
	switch {
	case !validCtrImage(config.Image):
		return CtrConfigError("image")
	case !ValidName(config.Name):
		return CtrConfigError("name")
	case config.WorkDir != "" && !ctrPath(config.WorkDir):
		return CtrConfigError("workdir")
	case strings.ContainsRune(config.User, 0) || config.Hostname != "" && !ValidName(config.Hostname):
		return CtrConfigError("identity")
	}
	for _, args := range [][]string{config.Command, config.Entrypoint} {
		if slices.ContainsFunc(args, func(arg string) bool { return strings.ContainsRune(arg, 0) }) {
			return CtrConfigError("command")
		}
	}
	for _, port := range config.Ports {
		if !ctrPortNumber(port.Container) || port.Host != "" && !ctrPortNumber(port.Host) ||
			port.Protocol != "tcp" && port.Protocol != "udp" && port.Protocol != "sctp" {
			return CtrConfigError("ports")
		}
		if ip, err := netip.ParseAddr(port.IP); port.IP != "" && (err != nil || ip.Zone() != "") {
			return CtrConfigError("ports")
		}
	}
	mountTargets := map[string]bool{}
	for _, mount := range config.Mounts {
		if (!ctrPath(mount.Source) && !ValidName(mount.Source)) || !ctrPath(mount.Target) || path.Clean(mount.Target) == "/" ||
			strings.Contains(mount.Source+mount.Target, ":") || mountTargets[path.Clean(mount.Target)] {
			return CtrConfigError("mounts")
		}
		mountTargets[path.Clean(mount.Target)] = true
	}
	for _, input := range []struct {
		name string
		list []string
		key  func(string) bool
	}{{"env", config.Env, ctrEnvKey.MatchString}, {"labels", config.Labels, validCtrLabel}, {"logs", config.LogOptions, ValidName}} {
		if _, ok := ctrPairs(input.list, input.key); !ok {
			return CtrConfigError(input.name)
		}
	}
	shared := strings.HasPrefix(config.Network, "container:") && ValidName(strings.TrimPrefix(config.Network, "container:"))
	if config.Network != "" && !ValidName(config.Network) && !shared {
		return CtrConfigError("network")
	}
	if config.IPv4 != "" || len(config.Aliases) > 0 {
		if config.Network == "" || config.Network == "default" || config.Network == "bridge" || config.Network == "host" || config.Network == "none" || shared ||
			slices.ContainsFunc(config.Aliases, func(value string) bool { return !ValidName(value) }) {
			return CtrConfigError("network")
		}
		if ip, err := netip.ParseAddr(config.IPv4); config.IPv4 != "" && (err != nil || !ip.Is4()) {
			return CtrConfigError("network")
		}
	}
	if policy, _, ok := ctrRestart(config.Restart); !ok || config.AutoRemove && policy != "no" {
		return CtrConfigError("restart")
	}
	_, cpuOK := ctrCPUValue(config.CPUs)
	memory, memoryOK := ctrByteValue(config.Memory, false)
	swap, swapOK := ctrByteValue(config.MemorySwap, true)
	_, sharesErr := strconv.ParseUint(config.Shares, 10, 63)
	if !cpuOK || !memoryOK || !swapOK || swap > 0 && (memory == 0 || swap < memory) || config.Shares != "" && sharesErr != nil {
		return CtrConfigError("resources")
	}
	for _, list := range [][]string{config.CapAdd, config.CapDrop} {
		if slices.ContainsFunc(list, func(value string) bool { return !ValidName(value) }) {
			return CtrConfigError("capabilities")
		}
	}
	for _, device := range config.Devices {
		if !ctrPath(device.Source) || !ctrPath(device.Target) || strings.Contains(device.Source+device.Target, ":") ||
			strings.Trim(device.Permissions, "rwm") != "" || len(device.Permissions) > 3 {
			return CtrConfigError("devices")
		}
	}
	if config.LogDriver != "" && !ValidName(config.LogDriver) {
		return CtrConfigError("logs")
	}
	return nil
}

func validCtrLabel(key string) bool {
	return ValidName(key) && !strings.HasPrefix(key, "com.docker.compose.") && !strings.HasPrefix(key, "io.podman.compose.")
}

func (config CtrConfig) runArgs(mask bool) ([]string, error) {
	if err := config.validate(); err != nil {
		return nil, err
	}
	args := []string{"run", "-d", "--name", config.Name}
	put := func(flag, value string) {
		if value != "" {
			args = append(args, flag, value)
		}
	}
	pull := "never"
	if config.Pull {
		pull = "missing"
	}
	put("--pull", pull)
	put("--workdir", config.WorkDir)
	put("--user", config.User)
	put("--hostname", config.Hostname)
	for _, port := range config.Ports {
		put("--publish", port.publish())
	}
	for _, mount := range config.Mounts {
		mode := ""
		if mount.ReadOnly {
			mode = ":ro"
		}
		put("--volume", mount.Source+":"+mount.Target+mode)
	}
	for _, value := range config.Env {
		if mask {
			key, _, _ := strings.Cut(value, "=")
			value = key + "=••••"
		}
		put("--env", value)
	}
	for _, value := range config.Labels {
		put("--label", value)
	}
	put("--network", config.Network)
	put("--ip", config.IPv4)
	for _, value := range config.Aliases {
		put("--network-alias", value)
	}
	put("--restart", config.Restart)
	put("--cpus", config.CPUs)
	put("--memory", config.Memory)
	put("--memory-swap", config.MemorySwap)
	put("--cpu-shares", config.Shares)
	put("--log-driver", config.LogDriver)
	for _, group := range []struct {
		flag   string
		values []string
	}{{"--cap-add", config.CapAdd}, {"--cap-drop", config.CapDrop}, {"--log-opt", config.LogOptions}} {
		for _, value := range group.values {
			if mask && group.flag == "--log-opt" {
				key, _, _ := strings.Cut(value, "=")
				value = key + "=••••"
			}
			put(group.flag, value)
		}
	}
	for _, device := range config.Devices {
		value := device.Source + ":" + device.Target
		if device.Permissions != "" {
			value += ":" + device.Permissions
		}
		put("--device", value)
	}
	for _, option := range []struct {
		flag    string
		enabled bool
	}{
		{"--publish-all", config.PublishAll}, {"--privileged", config.Privileged}, {"--rm", config.AutoRemove},
		{"--init", config.Init}, {"--tty", config.TTY}, {"--interactive", config.Stdin},
	} {
		if option.enabled {
			args = append(args, option.flag)
		}
	}
	if len(config.Entrypoint) > 0 {
		if config.Entrypoint[0] == "" && len(config.Entrypoint) > 1 {
			return nil, CtrConfigError("command")
		}
		args = append(args, "--entrypoint", config.Entrypoint[0])
	}
	args = append(args, "--", config.Image)
	if len(config.Entrypoint) > 1 {
		args = append(args, config.Entrypoint[1:]...)
	}
	return append(args, config.Command...), nil
}

func (cmd DockerCmd) CtrRunScript(config CtrConfig) (string, error) {
	args, err := config.runArgs(false)
	if err != nil {
		return "", err
	}
	return scriptHead + cmd.Line(args...) + "\n", nil
}

func (cmd DockerCmd) CtrRunPreview(config CtrConfig) (string, error) {
	args, err := config.runArgs(true)
	if err != nil {
		return "", err
	}
	return cmd.Line(args...), nil
}

func (cmd DockerCmd) CtrCommitScript(id, image, message, author string, pause bool) (string, error) {
	if err := checkRefs([]string{id}); err != nil {
		return "", err
	}
	if !ctrImageRef.MatchString(image) || strings.Contains(image, "@") || strings.ContainsRune(message+author, 0) {
		return "", CtrConfigError("image")
	}
	return scriptHead + cmd.Line("commit", "--pause="+strconv.FormatBool(pause), "--message", message, "--author", author, "--", id, image) + "\n", nil
}

func (cmd DockerCmd) CtrExportScript(id, destination string) (string, error) {
	if err := checkRefs([]string{id}); err != nil {
		return "", err
	}
	if !ctrPath(destination) || path.Clean(destination) != destination || destination == "/" {
		return "", CtrConfigError("path")
	}
	write := cmd.Line("export", "-o") + ` "$tmp" '--' ` + dockerQuote(id)
	if cmd.Sudo {
		write = cmd.Line("export", "--", id) + ` > "$tmp"`
	}
	return scriptHead + fmt.Sprintf(`dest=%s
if [ -e "$dest" ] || [ -L "$dest" ]; then echo 'The export destination already exists.'; exit 1; fi
umask 077
tmp=$(mktemp %s) || exit 1
trap 'rm -f -- "$tmp"' EXIT
trap 'exit 1' HUP INT TERM
%s || exit 1
ln -T -- "$tmp" "$dest"
`, dockerQuote(destination), dockerQuote(path.Join(path.Dir(destination), ".container-export.XXXXXXXX")), write), nil
}
