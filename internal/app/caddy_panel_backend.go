package app

import (
	"context"
	"errors"
	"io"
	"path"
	"strconv"
	"strings"
	"time"

	"finallyshell/internal/monitor"
)

type caddyFileInfo struct {
	monitor.CaddyFile
	Modified time.Time
}

type caddyServiceInfo struct {
	PID    int
	Memory int64
	Since  string
}

type caddyCertificate struct {
	Domains, Issuer, Key, Path, Subject, PEM string
	Expires                                  time.Time
}

type caddyCertificateInfo struct {
	Rows     []caddyCertificate
	Root     caddyCertificate
	OnDemand bool
}

type caddyBackup struct {
	Path string
}

type caddyBackupPreview struct {
	Files  []monitor.CaddyFile
	Change monitor.CaddyChange
}

type caddyInstallOptions struct {
	Method, Version    string
	Start, Enable      bool
	OpenPorts          bool
	ReplaceConflicting bool
}

type caddyInstallFacts struct {
	Docker   bool
	Firewall string
	Versions []string
}

type caddyNginxSite struct {
	Name, Content string
	Notes         []string
}

type caddyBackend interface {
	detect(context.Context, monitor.CaddyRunner) (monitor.CaddyInstall, error)
	files(context.Context, monitor.CaddyRunner, monitor.CaddyInstall) ([]caddyFileInfo, error)
	apply(context.Context, monitor.CaddyRunner, monitor.CaddyInstall, monitor.CaddyChange) (monitor.CaddyApplyResult, error)
	validate(context.Context, monitor.CaddyRunner, monitor.CaddyInstall, []monitor.CaddyFile) (string, error)
	format(context.Context, monitor.CaddyRunner, monitor.CaddyInstall, string) (string, error)
	adapt(context.Context, monitor.CaddyRunner, monitor.CaddyInstall) (string, error)
	service(context.Context, monitor.CaddyRunner, monitor.CaddyInstall, string, func(string)) (string, error)
	serviceInfo(context.Context, monitor.CaddyRunner, monitor.CaddyInstall) (caddyServiceInfo, error)
	admin(context.Context, monitor.CaddyRunner, monitor.CaddyInstall, string, string, string) (string, error)
	certs(context.Context, monitor.CaddyRunner, monitor.CaddyInstall) (caddyCertificateInfo, error)
	logFiles([]monitor.CaddyFile) []string
	backups(context.Context, monitor.CaddyRunner, monitor.CaddyInstall) ([]caddyBackup, error)
	backupPreview(context.Context, monitor.CaddyRunner, monitor.CaddyInstall, string) (caddyBackupPreview, error)
	deleteBackup(context.Context, monitor.CaddyRunner, monitor.CaddyInstall, string) (string, error)
	latest(context.Context) (string, error)
	installFacts(context.Context, monitor.CaddyRunner, monitor.CaddyInstall) (caddyInstallFacts, error)
	install(context.Context, monitor.CaddyRunner, monitor.CaddyInstall, caddyInstallOptions, func(string)) (string, error)
	update(context.Context, monitor.CaddyRunner, monitor.CaddyInstall, string, bool, func(string)) (string, error)
	uninstall(context.Context, monitor.CaddyRunner, monitor.CaddyInstall, bool, func(string)) (string, error)
	managedPreview(context.Context, monitor.CaddyRunner, monitor.CaddyInstall) (caddyBackupPreview, error)
	migrateBinary(context.Context, monitor.CaddyRunner, monitor.CaddyInstall, func(string)) (string, error)
	nginxToCaddy([]monitor.NginxSite) ([]caddyNginxSite, error)
	nginxCutover(context.Context, monitor.CaddyRunner, monitor.CaddyInstall, monitor.CaddyChange, func(string)) (string, error)
}

type caddyMonitorBackend struct{}

func (caddyMonitorBackend) detect(ctx context.Context, run monitor.CaddyRunner) (monitor.CaddyInstall, error) {
	return monitor.DetectCaddy(ctx, run)
}

func (caddyMonitorBackend) files(ctx context.Context, run monitor.CaddyRunner, install monitor.CaddyInstall) ([]caddyFileInfo, error) {
	files, err := monitor.ReadCaddyFiles(ctx, run, install)
	if err != nil {
		return nil, err
	}
	result := make([]caddyFileInfo, len(files))
	var commands []string
	for index, file := range files {
		result[index].CaddyFile = file
		if caddyValidPath(file.Path) && install.Method != monitor.CaddyDocker {
			prefix := monitor.SystemCommand("printf", "%s ", strconv.Itoa(index))
			commands = append(commands, prefix+"; "+monitor.SystemCommand("stat", "-c", "%Y", "--", file.Path)+" 2>/dev/null || "+monitor.SystemCommand("printf", "%s\\n", "-"))
		}
	}
	if len(commands) != 0 {
		text, _, _ := run(ctx, strings.Join(commands, "\n"), nil, nil)
		for line := range strings.SplitSeq(text, "\n") {
			fields := strings.Fields(line)
			if len(fields) != 2 {
				continue
			}
			index, indexErr := strconv.Atoi(fields[0])
			modified, dateErr := strconv.ParseInt(fields[1], 10, 64)
			if indexErr == nil && dateErr == nil && index >= 0 && index < len(result) {
				result[index].Modified = time.Unix(modified, 0)
			}
		}
	}
	return result, nil
}

func (caddyMonitorBackend) apply(ctx context.Context, run monitor.CaddyRunner, install monitor.CaddyInstall, change monitor.CaddyChange) (monitor.CaddyApplyResult, error) {
	return monitor.ApplyCaddyChange(ctx, run, install, change)
}

func (caddyMonitorBackend) validate(ctx context.Context, run monitor.CaddyRunner, install monitor.CaddyInstall, files []monitor.CaddyFile) (string, error) {
	return monitor.ValidateCaddyConfig(ctx, run, install, files)
}

func (caddyMonitorBackend) format(ctx context.Context, run monitor.CaddyRunner, install monitor.CaddyInstall, content string) (string, error) {
	return monitor.FormatCaddyfile(ctx, run, install, content)
}

func (caddyMonitorBackend) adapt(ctx context.Context, run monitor.CaddyRunner, install monitor.CaddyInstall) (string, error) {
	if !caddyValidPath(install.Config) || !caddyIsCaddyfile(install) {
		return "", errCaddyPanelTarget
	}
	args := []string{install.Binary, "adapt", "--config", install.Config, "--adapter", "caddyfile", "--pretty"}
	if install.Method == monitor.CaddyDocker && caddyContainerName.MatchString(install.Container) {
		args = []string{"docker", "exec", install.Container, "caddy", "adapt", "--config", "/etc/caddy/Caddyfile", "--adapter", "caddyfile", "--pretty"}
	} else if !caddyValidPath(install.Binary) || install.Method == monitor.CaddyDocker {
		return "", errCaddyPanelTarget
	}
	out, _, err := run(ctx, monitor.SystemCommand(args...), nil, nil)
	return out, err
}

func (caddyMonitorBackend) service(context.Context, monitor.CaddyRunner, monitor.CaddyInstall, string, func(string)) (string, error) {
	return "", monitor.ErrCaddyNotImplemented
}

func (caddyMonitorBackend) serviceInfo(ctx context.Context, run monitor.CaddyRunner, install monitor.CaddyInstall) (caddyServiceInfo, error) {
	if install.ServiceManager != "systemd" || !monitor.ValidService(install.Unit) {
		return caddyServiceInfo{}, nil
	}
	text, _, err := run(ctx, monitor.SystemCommand("systemctl", "show", "--no-pager", "--property=Id,MainPID,MemoryCurrent,ActiveEnterTimestamp", install.Unit), nil, nil)
	info := monitor.ParseServiceProperties(text)[install.Unit]
	return caddyServiceInfo{PID: int(info.PID), Memory: info.Memory, Since: info.Entered}, err
}

func (caddyMonitorBackend) admin(context.Context, monitor.CaddyRunner, monitor.CaddyInstall, string, string, string) (string, error) {
	return "", monitor.ErrCaddyNotImplemented
}

func (caddyMonitorBackend) certs(context.Context, monitor.CaddyRunner, monitor.CaddyInstall) (caddyCertificateInfo, error) {
	return caddyCertificateInfo{}, monitor.ErrCaddyNotImplemented
}

func (caddyMonitorBackend) logFiles([]monitor.CaddyFile) []string { return nil }

func (caddyMonitorBackend) backups(context.Context, monitor.CaddyRunner, monitor.CaddyInstall) ([]caddyBackup, error) {
	return nil, monitor.ErrCaddyNotImplemented
}

func (caddyMonitorBackend) backupPreview(context.Context, monitor.CaddyRunner, monitor.CaddyInstall, string) (caddyBackupPreview, error) {
	return caddyBackupPreview{}, monitor.ErrCaddyNotImplemented
}

func (caddyMonitorBackend) deleteBackup(context.Context, monitor.CaddyRunner, monitor.CaddyInstall, string) (string, error) {
	return "", monitor.ErrCaddyNotImplemented
}

func (caddyMonitorBackend) latest(context.Context) (string, error) {
	return "", monitor.ErrCaddyNotImplemented
}

func (backend caddyMonitorBackend) installFacts(ctx context.Context, run monitor.CaddyRunner, _ monitor.CaddyInstall) (caddyInstallFacts, error) {
	text, _, err := run(ctx, `if command -v 'docker' >/dev/null 2>&1; then 'echo' 'docker'; fi
if 'env' 'LC_ALL=C' 'ufw' 'status' 2>/dev/null | 'grep' '-q' '^Status: active'; then 'echo' 'ufw'
elif 'firewall-cmd' '--state' 2>/dev/null | 'grep' '-qx' 'running'; then 'echo' 'firewalld'; fi`, nil, nil)
	facts := caddyInstallFacts{}
	for _, line := range strings.Fields(text) {
		switch line {
		case "docker":
			facts.Docker = true
		case "ufw", "firewalld":
			facts.Firewall = line
		}
	}
	if version, latestErr := backend.latest(ctx); latestErr == nil && version != "" {
		facts.Versions = []string{version}
	}
	return facts, err
}

func (caddyMonitorBackend) install(context.Context, monitor.CaddyRunner, monitor.CaddyInstall, caddyInstallOptions, func(string)) (string, error) {
	return "", monitor.ErrCaddyNotImplemented
}

func (caddyMonitorBackend) update(context.Context, monitor.CaddyRunner, monitor.CaddyInstall, string, bool, func(string)) (string, error) {
	return "", monitor.ErrCaddyNotImplemented
}

func (caddyMonitorBackend) uninstall(context.Context, monitor.CaddyRunner, monitor.CaddyInstall, bool, func(string)) (string, error) {
	return "", monitor.ErrCaddyNotImplemented
}

func (caddyMonitorBackend) managedPreview(context.Context, monitor.CaddyRunner, monitor.CaddyInstall) (caddyBackupPreview, error) {
	return caddyBackupPreview{}, monitor.ErrCaddyNotImplemented
}

func (caddyMonitorBackend) migrateBinary(context.Context, monitor.CaddyRunner, monitor.CaddyInstall, func(string)) (string, error) {
	return "", monitor.ErrCaddyNotImplemented
}

func (caddyMonitorBackend) nginxToCaddy([]monitor.NginxSite) ([]caddyNginxSite, error) {
	return nil, monitor.ErrCaddyNotImplemented
}

func (caddyMonitorBackend) nginxCutover(context.Context, monitor.CaddyRunner, monitor.CaddyInstall, monitor.CaddyChange, func(string)) (string, error) {
	return "", monitor.ErrCaddyNotImplemented
}

var errCaddyPanelTarget = errors.New("invalid Caddy target")

func caddyValidPath(file string) bool {
	return path.IsAbs(file) && file != "/" && path.Clean(file) == file && len(file) <= 4096 && !strings.ContainsAny(file, "\x00\r\n")
}

func caddyProgressRunner(run monitor.CaddyRunner, progress func(string)) monitor.CaddyRunner {
	return func(ctx context.Context, script string, input io.Reader, callback func(string)) (string, string, error) {
		if callback == nil {
			callback = progress
		}
		return run(ctx, script, input, callback)
	}
}
