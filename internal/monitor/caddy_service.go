package monitor

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

const (
	CaddyStart     = "start"
	CaddyStop      = "stop"
	CaddyRestart   = "restart"
	CaddyReload    = "reload"
	CaddyEnable    = "enable"
	CaddyDisable   = "disable"
	CaddyEnableNow = "enable --now"
	CaddyStatus    = "status"
	CaddyValidate  = "validate"
)

// CaddyServiceResult includes the command output and status properties, when
// requested. Unknown memory is -1.
type CaddyServiceResult struct {
	Output, Active, Since string
	PID, Memory           int64
}

func caddyServiceScript(inst CaddyInstall, action string) (string, error) {
	if inst.Method == CaddyDocker && !caddyIdentifier.MatchString(inst.Container) {
		return "", ErrCaddyUnsafe
	}
	config := inst.Config
	if inst.Method == CaddyDocker && (action == CaddyValidate || action == CaddyReload) {
		var ok bool
		config, ok = caddyContainerPath(inst, config)
		if !ok {
			return "", ErrCaddyUnsafe
		}
	}
	switch action {
	case CaddyValidate:
		if inst.Resume {
			return "", ErrCaddyUnsafe
		}
		return caddyValidateCommand(inst, config), nil
	case CaddyReload:
		return caddyReloadCommand(inst, config)
	case CaddyStart, CaddyStop, CaddyRestart, CaddyEnable, CaddyDisable, CaddyEnableNow, CaddyStatus:
	default:
		return "", ErrCaddyUnsafe
	}
	switch {
	case inst.Method == CaddyDocker:
		switch action {
		case CaddyStatus:
			return SystemCommand("docker", "inspect", "--format", "ActiveState={{.State.Status}}\nMainPID={{.State.Pid}}\nActiveEnterTimestamp={{.State.StartedAt}}", inst.Container) + " || exit '1'\n" +
				SystemCommand("docker", "stats", "--no-stream", "--format", "MemoryUsage={{.MemUsage}}", inst.Container) + " 2>/dev/null || :", nil
		case CaddyEnable, CaddyEnableNow:
			command := SystemCommand("docker", "update", "--restart", "unless-stopped", inst.Container)
			if action == CaddyEnableNow {
				command += " && " + SystemCommand("docker", "start", inst.Container)
			}
			return command, nil
		case CaddyDisable:
			return SystemCommand("docker", "update", "--restart", "no", inst.Container), nil
		default:
			return SystemCommand("docker", action, inst.Container), nil
		}
	case inst.Unit != "" && inst.ServiceManager != "openrc":
		if action == CaddyStatus {
			return SystemCommand("systemctl", "show", "-p", "MainPID,MemoryCurrent,ActiveEnterTimestamp,ActiveState", inst.Unit) + " || exit '1'\n" +
				SystemCommand("systemctl", "status", "--no-pager", "--full", "--lines=0", inst.Unit) + " || { status=$?; [ \"$status\" = '3' ]; }", nil
		}
		if action == CaddyEnableNow {
			return SystemCommand("systemctl", "enable", "--now", inst.Unit), nil
		}
		return SystemCommand("systemctl", action, inst.Unit), nil
	case inst.ServiceManager == "openrc":
		switch action {
		case CaddyEnable, CaddyEnableNow:
			command := SystemCommand("rc-update", "add", "caddy", "default")
			if action == CaddyEnableNow {
				command += " && " + SystemCommand("rc-service", "caddy", "start")
			}
			return command, nil
		case CaddyDisable:
			return SystemCommand("rc-update", "del", "caddy", "default"), nil
		default:
			return SystemCommand("rc-service", "caddy", action), nil
		}
	default:
		return "", ErrCaddyUnsafe
	}
}

// CaddyService runs a named service action with systemctl, OpenRC or Docker.
// Validation and native reload without a unit use the Caddy CLI. Unsupported
// actions or service layouts return ErrCaddyUnsafe without running a command.
// Optional progress receives streamed output for actions other than status.
func CaddyService(ctx context.Context, run CaddyRunner, inst CaddyInstall, action string, progress ...func(string)) (CaddyServiceResult, error) {
	result := CaddyServiceResult{Memory: -1}
	inst, err := caddyNormalize(inst)
	if err != nil {
		return result, err
	}
	if action == "validate-only" {
		action = CaddyValidate
	}
	if action == "enable-now" {
		action = CaddyEnableNow
	}
	command, err := caddyServiceScript(inst, action)
	if err != nil {
		return result, err
	}
	status, diagnostic, err := run(ctx, command, nil, caddyProgress(progress))
	result.Output = caddyOutput(status, diagnostic)
	if action == CaddyStatus {
		for line := range strings.SplitSeq(status, "\n") {
			key, value, _ := strings.Cut(strings.TrimSuffix(line, "\r"), "=")
			switch key {
			case "MainPID":
				result.PID, _ = strconv.ParseInt(value, 10, 64)
			case "MemoryCurrent":
				if number, parseErr := strconv.ParseInt(value, 10, 64); parseErr == nil && number >= 0 {
					result.Memory = number
				}
			case "MemoryUsage":
				used, _, _ := strings.Cut(value, " /")
				for suffix, scale := range map[string]float64{"TiB": 1 << 40, "GiB": 1 << 30, "MiB": 1 << 20, "KiB": 1 << 10, "B": 1} {
					if amount, ok := strings.CutSuffix(strings.TrimSpace(used), suffix); ok {
						if number, parseErr := strconv.ParseFloat(amount, 64); parseErr == nil && number >= 0 && number*scale < 1<<63 {
							result.Memory = int64(number * scale)
							break
						}
					}
				}
			case "ActiveState":
				result.Active = value
			case "ActiveEnterTimestamp":
				result.Since = value
			}
		}
	}
	if err != nil && action == CaddyValidate && ctx.Err() == nil {
		return result, fmt.Errorf("%w: %w: %s", ErrCaddyInvalid, err, result.Output)
	}
	if err != nil {
		return result, caddyCommandError(err, result.Output)
	}
	return result, nil
}

func caddyProgress(progress []func(string)) func(string) {
	if len(progress) != 0 {
		return progress[0]
	}
	return nil
}
