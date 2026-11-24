package monitor

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

type ctrFields map[string]json.RawMessage

func (fields ctrFields) put(key string, value any) { fields[key], _ = json.Marshal(value) }

func ctrGet[Value any](fields ctrFields, key string) (value Value) {
	_ = json.Unmarshal(fields[key], &value)
	return value
}

type ctrInspect struct {
	ID              string `json:"Id"`
	Image           string
	Config          ctrFields
	HostConfig      ctrFields
	State           json.RawMessage
	Mounts          []ctrFields
	NetworkSettings struct{ Networks map[string]ctrFields }
}

func ctrInspectOf(ct *Container) (ctrInspect, error) {
	var raw ctrInspect
	if err := json.Unmarshal(ct.Inspect, &raw); err != nil || raw.Config == nil || raw.HostConfig == nil {
		return raw, errors.New("container configuration is unavailable")
	}
	return raw, nil
}

func CtrConfigOf(ct *Container) (CtrConfig, error) {
	raw, err := ctrInspectOf(ct)
	if err != nil {
		return CtrConfig{}, err
	}
	return ctrConfigOf(ct, raw), nil
}

func ctrPairsOf(fields map[string]string) []string {
	var pairs []string
	for _, key := range slices.Sorted(maps.Keys(fields)) {
		pairs = append(pairs, key+"="+fields[key])
	}
	return pairs
}

func ctrPositive(value int64) string {
	if value == 0 {
		return ""
	}
	return strconv.FormatInt(value, 10)
}

func ctrConfigOf(ct *Container, raw ctrInspect) CtrConfig {
	config, host := raw.Config, raw.HostConfig
	form := CtrConfig{
		Name: ct.Name, Image: ctrGet[string](config, "Image"), WorkDir: ctrGet[string](config, "WorkingDir"),
		User: ctrGet[string](config, "User"), Hostname: ctrGet[string](config, "Hostname"),
		Command: ctrGet[[]string](config, "Cmd"), Entrypoint: ctrGet[[]string](config, "Entrypoint"),
		Env: ctrGet[[]string](config, "Env"), Labels: ctrPairsOf(ctrGet[map[string]string](config, "Labels")),
		Network: ctrGet[string](host, "NetworkMode"), Privileged: ctrGet[bool](host, "Privileged"),
		PublishAll: ctrGet[bool](host, "PublishAllPorts"), AutoRemove: ctrGet[bool](host, "AutoRemove"),
		Init: ctrGet[bool](host, "Init"), TTY: ctrGet[bool](config, "Tty"), Stdin: ctrGet[bool](config, "OpenStdin"),
		CapAdd: ctrGet[[]string](host, "CapAdd"), CapDrop: ctrGet[[]string](host, "CapDrop"),
		Memory: ctrPositive(ctrGet[int64](host, "Memory")), MemorySwap: ctrPositive(ctrGet[int64](host, "MemorySwap")),
		Shares: ctrPositive(ctrGet[int64](host, "CpuShares")),
	}
	if form.Image == "" {
		form.Image = ct.Image
	}
	if form.Hostname != "" && strings.HasPrefix(raw.ID, form.Hostname) {
		form.Hostname = ""
	}
	if cpu := ctrGet[int64](host, "NanoCpus"); cpu > 0 {
		form.CPUs = strconv.FormatFloat(float64(cpu)/1e9, 'f', -1, 64)
	}
	restart := ctrGet[struct {
		Name              string
		MaximumRetryCount int
	}](host, "RestartPolicy")
	form.Restart = restart.Name
	if form.Restart == "" {
		form.Restart = "no"
	}
	if restart.Name == "on-failure" && restart.MaximumRetryCount > 0 {
		form.Restart += ":" + strconv.Itoa(restart.MaximumRetryCount)
	}
	logging := ctrGet[struct {
		Type   string
		Config map[string]string
	}](host, "LogConfig")
	form.LogDriver, form.LogOptions = logging.Type, ctrPairsOf(logging.Config)
	networks := raw.NetworkSettings.Networks
	if primary := networks[ctrPrimary(form.Network, networks)]; primary != nil {
		form.IPv4 = ctrGet[string](ctrGet[ctrFields](primary, "IPAMConfig"), "IPv4Address")
		form.Aliases = slices.DeleteFunc(ctrGet[[]string](primary, "Aliases"), func(alias string) bool {
			return alias == ct.Name || len(alias) >= 12 && strings.HasPrefix(raw.ID, alias)
		})
	}
	bindings := ctrGet[map[string][]struct{ HostIP, HostPort string }](host, "PortBindings")
	for _, key := range slices.Sorted(maps.Keys(bindings)) {
		port, protocol, _ := strings.Cut(key, "/")
		for _, binding := range bindings[key] {
			form.Ports = append(form.Ports, CtrPort{binding.HostIP, binding.HostPort, port, protocol})
		}
	}
	for _, mount := range raw.Mounts {
		var source string
		switch ctrGet[string](mount, "Type") {
		case "bind":
			source = ctrGet[string](mount, "Source")
		case "volume":
			source = ctrGet[string](mount, "Name")
		}
		if source != "" {
			form.Mounts = append(form.Mounts, CtrMount{source, ctrGet[string](mount, "Destination"), !ctrGet[bool](mount, "RW")})
		}
	}
	for _, device := range ctrGet[[]struct{ PathOnHost, PathInContainer, CgroupPermissions string }](host, "Devices") {
		form.Devices = append(form.Devices, CtrDevice{device.PathOnHost, device.PathInContainer, device.CgroupPermissions})
	}
	return form
}

func ctrPrimary(mode string, networks map[string]ctrFields) string {
	if _, ok := networks[mode]; ok {
		return mode
	}
	if mode == "default" && networks["bridge"] != nil {
		return "bridge"
	}
	for _, key := range slices.Sorted(maps.Keys(networks)) {
		return key
	}
	return ""
}

type ctrEdit struct {
	name       string
	inspect    json.RawMessage
	pull       bool
	oldNetwork []netJoin
	verify     string
}

func (cmd DockerCmd) CtrEditScript(ct *Container, form CtrConfig, at time.Time) (string, error) {
	if ct.Project != "" || ct.AutoRemove || ct.Paused || ct.Status == "paused" || cmd.Tool == "podman" {
		return "", errors.New("cannot replace this container safely")
	}
	if form.AutoRemove {
		return "", CtrConfigError("autoremove")
	}
	raw, err := ctrInspectOf(ct)
	if err != nil {
		return "", err
	}
	original := ctrConfigOf(ct, raw)
	if err := form.validate(); err != nil {
		return "", err
	}
	if ctrGet[bool](raw.HostConfig, "AutoRemove") || ctrGet[map[string]string](raw.Config, "Labels")["com.docker.compose.project"] != "" {
		return "", errors.New("cannot replace this container safely")
	}
	if original.Network != form.Network && len(raw.NetworkSettings.Networks) > 1 &&
		(form.Network == "host" || form.Network == "none" || strings.HasPrefix(form.Network, "container:")) {
		return "", CtrConfigError("network")
	}
	edit := ctrEdit{name: form.Name, pull: form.Pull, verify: ctrEditVerify(cmd, ct)}
	edit.oldNetwork = ctrFixedNetworks(raw)
	ctrEditConfig(&raw, original, form)
	ctrEditHost(&raw, original, form)
	ctrEditNetwork(&raw, original, form)
	edit.inspect, err = json.Marshal(raw)
	if err != nil {
		return "", err
	}
	return cmd.recreateScript(ct, at, &edit)
}

func ctrEditVerify(cmd DockerCmd, ct *Container) string {
	var data ctrFields
	if json.Unmarshal(ct.Inspect, &data) != nil {
		return ""
	}
	var expected []string
	for _, field := range []string{"Config", "HostConfig"} {
		var compact bytes.Buffer
		if json.Compact(&compact, data[field]) != nil {
			return ""
		}
		expected = append(expected, compact.String())
	}
	state := ctrGet[ctrFields](data, "State")
	status := ctrGet[string](state, "Status")
	if status == "" {
		status = ct.Status
	}
	return fmt.Sprintf(`current=$(%s) || fail "Could not verify the original configuration. Nothing was changed."
[ "$current" = %s ] || fail "The original container changed. Refresh and reopen the editor. Nothing was changed."
`, cmd.Line("inspect", "--format", "{{.Name}} {{.State.Status}} {{json .Config}} {{json .HostConfig}}", ct.ID),
		dockerQuote("/"+ct.Name+" "+status+" "+strings.Join(expected, " ")))
}

func ctrChanged[Value comparable](fields ctrFields, key string, old, next Value) {
	if old != next {
		fields.put(key, next)
	}
}

func ctrChangedList(fields ctrFields, key string, old, next []string) {
	if !slices.Equal(old, next) {
		if next == nil {
			next = []string{}
		}
		fields.put(key, next)
	}
}

func ctrEditConfig(raw *ctrInspect, old, next CtrConfig) {
	config := raw.Config
	image := next.Image
	if old.Image == next.Image && validCtrImage(raw.Image) {
		image = raw.Image
	}
	config.put("Image", image)
	ctrChanged(config, "WorkingDir", old.WorkDir, next.WorkDir)
	ctrChanged(config, "User", old.User, next.User)
	ctrChanged(config, "Hostname", old.Hostname, next.Hostname)
	ctrChanged(config, "Tty", old.TTY, next.TTY)
	ctrChanged(config, "OpenStdin", old.Stdin, next.Stdin)
	ctrChanged(config, "AttachStdin", old.Stdin, next.Stdin)
	ctrChangedList(config, "Cmd", old.Command, next.Command)
	ctrChangedList(config, "Entrypoint", old.Entrypoint, next.Entrypoint)
	ctrChangedList(config, "Env", old.Env, next.Env)
	if !slices.Equal(old.Labels, next.Labels) {
		pairs, _ := ctrPairs(next.Labels, validCtrLabel)
		config.put("Labels", pairs)
	}
}

func ctrEditHost(raw *ctrInspect, old, next CtrConfig) {
	host := raw.HostConfig
	ctrChanged(host, "Privileged", old.Privileged, next.Privileged)
	ctrChanged(host, "Init", old.Init, next.Init)
	ctrChanged(host, "PublishAllPorts", old.PublishAll, next.PublishAll)
	ctrChangedList(host, "CapAdd", old.CapAdd, next.CapAdd)
	ctrChangedList(host, "CapDrop", old.CapDrop, next.CapDrop)
	if old.Restart != next.Restart {
		policy, retries, _ := ctrRestart(next.Restart)
		host.put("RestartPolicy", map[string]any{"Name": policy, "MaximumRetryCount": retries})
	}
	if old.CPUs != next.CPUs {
		value, _ := ctrCPUValue(next.CPUs)
		host.put("NanoCpus", value)
	}
	for _, item := range []struct{ name, old, next string }{{"Memory", old.Memory, next.Memory}, {"MemorySwap", old.MemorySwap, next.MemorySwap}} {
		if item.old != item.next {
			value, _ := ctrByteValue(item.next, item.name == "MemorySwap")
			host.put(item.name, value)
		}
	}
	if old.Shares != next.Shares {
		value, _ := strconv.ParseInt(next.Shares, 10, 64)
		host.put("CpuShares", value)
	}
	if old.LogDriver != next.LogDriver || !slices.Equal(old.LogOptions, next.LogOptions) {
		pairs, _ := ctrPairs(next.LogOptions, ValidName)
		host.put("LogConfig", map[string]any{"Type": next.LogDriver, "Config": pairs})
	}
	if !slices.Equal(old.Devices, next.Devices) {
		devices := make([]map[string]string, 0, len(next.Devices))
		for _, device := range next.Devices {
			permissions := device.Permissions
			if permissions == "" {
				permissions = "rwm"
			}
			devices = append(devices, map[string]string{"PathOnHost": device.Source, "PathInContainer": device.Target, "CgroupPermissions": permissions})
		}
		host.put("Devices", devices)
	}
	if !slices.Equal(old.Ports, next.Ports) {
		ports := make(map[string][]map[string]string)
		exposed := ctrGet[ctrFields](raw.Config, "ExposedPorts")
		if exposed == nil {
			exposed = ctrFields{}
		}
		for _, port := range next.Ports {
			ports[port.key()] = append(ports[port.key()], map[string]string{"HostIp": port.IP, "HostPort": port.Host})
			exposed.put(port.key(), map[string]any{})
		}
		host.put("PortBindings", ports)
		raw.Config.put("ExposedPorts", exposed)
	}
	if !slices.Equal(old.Mounts, next.Mounts) {
		ctrEditMounts(raw, old.Mounts, next.Mounts)
	}
}

func ctrEditMounts(raw *ctrInspect, old, next []CtrMount) {
	changed := func(target string) bool {
		return slices.ContainsFunc(old, func(mount CtrMount) bool { return mount.Target == target && !slices.Contains(next, mount) }) ||
			slices.ContainsFunc(next, func(mount CtrMount) bool { return mount.Target == target && !slices.Contains(old, mount) })
	}
	host := raw.HostConfig
	binds := slices.DeleteFunc(ctrGet[[]string](host, "Binds"), func(bind string) bool {
		parts := strings.Split(bind, ":")
		return len(parts) > 1 && changed(parts[1])
	})
	mounts := slices.DeleteFunc(ctrGet[[]ctrFields](host, "Mounts"), func(mount ctrFields) bool { return changed(ctrGet[string](mount, "Target")) })
	raw.Mounts = slices.DeleteFunc(raw.Mounts, func(mount ctrFields) bool { return changed(ctrGet[string](mount, "Destination")) })
	for _, mount := range next {
		if !changed(mount.Target) {
			continue
		}
		kind := "volume"
		if ctrPath(mount.Source) {
			kind = "bind"
		}
		fields := ctrFields{}
		fields.put("Type", kind)
		fields.put("Source", mount.Source)
		fields.put("Target", mount.Target)
		fields.put("ReadOnly", mount.ReadOnly)
		mounts = append(mounts, fields)
	}
	host.put("Binds", binds)
	host.put("Mounts", mounts)
}

func ctrEditNetwork(raw *ctrInspect, old, next CtrConfig) {
	if old.Network == next.Network && old.IPv4 == next.IPv4 && slices.Equal(old.Aliases, next.Aliases) {
		return
	}
	networks := raw.NetworkSettings.Networks
	if networks == nil {
		networks = map[string]ctrFields{}
		raw.NetworkSettings.Networks = networks
	}
	oldPrimary := ctrPrimary(old.Network, networks)
	mode := next.Network
	if mode == "" {
		mode = "default"
	}
	if mode == "host" || mode == "none" || strings.HasPrefix(mode, "container:") {
		if len(networks) <= 1 {
			clear(networks)
		}
		raw.HostConfig.put("NetworkMode", mode)
		return
	}
	name := mode
	if name == "default" {
		name = "bridge"
	}
	if old.Network != next.Network {
		if name != oldPrimary {
			delete(networks, oldPrimary)
		}
		raw.HostConfig.put("NetworkMode", mode)
	} else if oldPrimary != "" {
		name = oldPrimary
	}
	settings := networks[name]
	if settings == nil {
		settings = ctrFields{}
		networks[name] = settings
	}
	if old.IPv4 != next.IPv4 || old.Network != next.Network {
		addresses := ctrGet[ctrFields](settings, "IPAMConfig")
		if addresses == nil {
			addresses = ctrFields{}
		}
		if next.IPv4 == "" {
			delete(addresses, "IPv4Address")
		} else {
			addresses.put("IPv4Address", next.IPv4)
		}
		if len(addresses) == 0 {
			delete(settings, "IPAMConfig")
		} else {
			settings.put("IPAMConfig", addresses)
		}
	}
	if old.Network != next.Network || !slices.Equal(old.Aliases, next.Aliases) {
		settings.put("Aliases", next.Aliases)
	}
}

func ctrFixedNetworks(raw ctrInspect) []netJoin {
	var joins []netJoin
	networks := raw.NetworkSettings.Networks
	for _, name := range slices.Sorted(maps.Keys(networks)) {
		settings := endpoint(networks[name], raw.ID)
		config := ctrGet[ctrFields](settings, "IPAMConfig")
		if ctrGet[string](config, "IPv4Address") == "" && ctrGet[string](config, "IPv6Address") == "" {
			continue
		}
		body, _ := json.Marshal(settings)
		joins = append(joins, netJoin{name: name, before: `{"EndpointConfig":` + string(body) + `,"Container":"`, after: `"}`})
	}
	return joins
}

func (edit ctrEdit) preflight(cmd DockerCmd, image string) string {
	var script strings.Builder
	if edit.pull && !strings.HasPrefix(image, "sha256:") {
		fmt.Fprintf(&script, "if ! %s >/dev/null; then\n  step %s\n  %s || fail \"Could not pull the image. Nothing was changed.\"\nfi\n",
			cmd.Line("image", "inspect", "--", image), dockerQuote("Pulling "+image), cmd.Line("pull", "--", image))
	} else {
		fmt.Fprintf(&script, "%s >/dev/null || fail \"The image is not available locally. Nothing was changed.\"\n", cmd.Line("image", "inspect", "--", image))
	}
	script.WriteString(edit.verify)
	script.WriteString("phase=0\ntrap 'if [ \"$phase\" = 1 ]; then rollback \"The operation was interrupted.\"; fi' HUP INT TERM\n")
	return script.String()
}

func ctrReleaseNetworks(cmd DockerCmd, id string, edit *ctrEdit) string {
	if edit == nil {
		return ""
	}
	var script strings.Builder
	script.WriteString("phase=1\n")
	for index, network := range edit.oldNetwork {
		fmt.Fprintf(&script, "%s >/dev/null || rollback %s\ndetached%d=1\n", cmd.Line("network", "disconnect", network.name, id),
			dockerQuote("Could not release the original endpoint on "+network.name+"."), index)
	}
	return script.String()
}

func ctrRestoreNetworks(edit *ctrEdit) string {
	if edit == nil {
		return ""
	}
	var script strings.Builder
	script.WriteString("  phase=0\n")
	for index, network := range edit.oldNetwork {
		fmt.Fprintf(&script, "  if [ \"$detached%d\" = 1 ]; then\n    post %s \"%s$id%s\" && [ \"$code\" = 200 ] || fail %s\n  fi\n", index,
			dockerQuote("/networks/"+url.PathEscape(network.name)+"/connect"), shDouble(network.before), shDouble(network.after),
			dockerQuote("Rollback could not restore the original endpoint on "+network.name+"."))
	}
	return script.String()
}

func ctrCheckReplacement(cmd DockerCmd, edit *ctrEdit) string {
	if edit == nil {
		return ""
	}
	return fmt.Sprintf(`sleep 1
[ "$(%s "$new")" = running ] || rollback "The new container did not stay running."
phase=0
`, cmd.Line("inspect", "--format", "{{.State.Status}}"))
}
