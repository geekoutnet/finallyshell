package app

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
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
	Firewall           string
}

type caddyInstallFacts struct {
	Docker, Systemd, OpenRC bool
	Firewall                string
	Versions                []string
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
	nginxToCaddy([]monitor.NginxSite, string) ([]caddyNginxSite, error)
	nginxCutover(context.Context, monitor.CaddyRunner, monitor.CaddyInstall, monitor.CaddyChange, func(string)) (string, error)
}

type caddyMonitorBackend struct{ releaseClient *http.Client }

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
	if install.Method == monitor.CaddyDocker && caddyContainerName.MatchString(install.Container) && caddyValidPath(install.ContainerConfig) {
		args = []string{"docker", "exec", install.Container, "caddy", "adapt", "--config", install.ContainerConfig, "--adapter", "caddyfile", "--pretty"}
	} else if !caddyValidPath(install.Binary) || install.Method == monitor.CaddyDocker {
		return "", errCaddyPanelTarget
	}
	out, _, err := run(ctx, monitor.SystemCommand(args...), nil, nil)
	return out, err
}

func (caddyMonitorBackend) service(ctx context.Context, run monitor.CaddyRunner, install monitor.CaddyInstall, action string, progress func(string)) (string, error) {
	result, err := monitor.CaddyService(ctx, run, install, action, progress)
	if progress != nil && err == nil {
		return "", nil
	}
	return result.Output, err
}

func (caddyMonitorBackend) serviceInfo(ctx context.Context, run monitor.CaddyRunner, install monitor.CaddyInstall) (caddyServiceInfo, error) {
	if install.ServiceManager != "systemd" && install.ServiceManager != "openrc" && install.Method != monitor.CaddyDocker {
		return caddyServiceInfo{}, nil
	}
	info, err := monitor.CaddyService(ctx, run, install, monitor.CaddyStatus)
	return caddyServiceInfo{PID: int(info.PID), Memory: info.Memory, Since: info.Since}, err
}

func (caddyMonitorBackend) admin(ctx context.Context, run monitor.CaddyRunner, install monitor.CaddyInstall, method, target, body string) (string, error) {
	return monitor.CaddyAdmin(ctx, run, install, method, target, body)
}

func (caddyMonitorBackend) certs(ctx context.Context, run monitor.CaddyRunner, install monitor.CaddyInstall) (caddyCertificateInfo, error) {
	var info caddyCertificateInfo
	certs, err := monitor.CaddyCertificates(ctx, run, install)
	for _, cert := range certs {
		entry := caddyCertificate{Domains: strings.Join(cert.SANs, ", "), Issuer: cert.Issuer, Key: cert.KeyType, Path: cert.Path, Expires: cert.NotAfter, Subject: cert.Subject, PEM: cert.PEM}
		if entry.Domains == "" {
			entry.Domains = entry.Subject
		}
		if cert.IsCA && cert.Path == path.Join(install.DataDir, "pki/authorities/local/root.crt") {
			info.Root = entry
		} else {
			info.Rows = append(info.Rows, entry)
		}
	}
	if info.Root.PEM == "" && caddyAdminReason(install) == "" {
		if chain, caErr := monitor.CaddyLocalCA(ctx, run, install); caErr == nil {
			info.Root = caddyRootFromChain(chain)
		}
	}
	if caddyIsCaddyfile(install) {
		if files, readErr := monitor.ReadCaddyFiles(ctx, run, install); readErr == nil {
			info.OnDemand = monitor.CaddyOnDemandTLS(files)
		}
	}
	return info, err
}

func caddyRootFromChain(chain string) caddyCertificate {
	for block, rest := pem.Decode([]byte(chain)); block != nil; block, rest = pem.Decode(rest) {
		if block.Type != "CERTIFICATE" {
			continue
		}
		if cert, err := x509.ParseCertificate(block.Bytes); err == nil && cert.IsCA && cert.Subject.String() == cert.Issuer.String() && cert.CheckSignatureFrom(cert) == nil {
			return caddyCertificate{Subject: cert.Subject.String(), Issuer: cert.Issuer.String(), Key: cert.PublicKeyAlgorithm.String(), Expires: cert.NotAfter, PEM: string(pem.EncodeToMemory(block))}
		}
	}
	return caddyCertificate{}
}

func (caddyMonitorBackend) logFiles(files []monitor.CaddyFile) []string {
	return monitor.CaddyLogFiles(files)
}

func (caddyMonitorBackend) backups(ctx context.Context, run monitor.CaddyRunner, install monitor.CaddyInstall) ([]caddyBackup, error) {
	paths, err := monitor.CaddyBackups(ctx, run, install)
	backups := make([]caddyBackup, 0, len(paths))
	for _, name := range paths {
		backups = append(backups, caddyBackup{Path: name})
	}
	return backups, err
}

func (caddyMonitorBackend) backupPreview(ctx context.Context, run monitor.CaddyRunner, install monitor.CaddyInstall, backup string) (caddyBackupPreview, error) {
	change, err := monitor.PreviewCaddyBackup(ctx, run, install, backup)
	return caddyChangePreview(change), err
}

func (caddyMonitorBackend) deleteBackup(ctx context.Context, run monitor.CaddyRunner, install monitor.CaddyInstall, backup string) (string, error) {
	return "", monitor.DeleteCaddyBackup(ctx, run, install, backup)
}

func (backend caddyMonitorBackend) latest(ctx context.Context) (string, error) {
	client := backend.releaseClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return monitor.LatestCaddyRelease(ctx, client)
}

func (backend caddyMonitorBackend) installFacts(ctx context.Context, run monitor.CaddyRunner, install monitor.CaddyInstall) (caddyInstallFacts, error) {
	text, _, err := run(ctx, `if 'docker' 'info' '--format' '{{.ServerVersion}}' >/dev/null 2>&1; then 'echo' 'docker'; fi
if [ -d '/run/systemd/system' ] && command -v 'systemctl' >/dev/null 2>&1; then 'echo' 'systemd'; fi
if command -v 'rc-service' >/dev/null 2>&1 && command -v 'rc-update' >/dev/null 2>&1; then 'echo' 'openrc'; fi
if 'env' 'LC_ALL=C' 'ufw' 'status' 2>/dev/null | 'grep' '-q' '^Status: active'; then 'echo' 'ufw'
elif 'firewall-cmd' '--state' 2>/dev/null | 'grep' '-qx' 'running'; then 'echo' 'firewalld'; fi`, nil, nil)
	facts := caddyInstallFacts{}
	for _, line := range strings.Fields(text) {
		switch line {
		case "docker":
			facts.Docker = true
		case "systemd":
			facts.Systemd = true
		case "openrc":
			facts.OpenRC = true
		case "ufw", "firewalld":
			facts.Firewall = line
		}
	}
	if install.Method == monitor.CaddyNotInstalled && facts.Systemd {
		if version, latestErr := backend.latest(ctx); latestErr == nil && version != "" {
			facts.Versions = []string{version}
		}
	}
	return facts, err
}

func (backend caddyMonitorBackend) install(ctx context.Context, run monitor.CaddyRunner, install monitor.CaddyInstall, options caddyInstallOptions, progress func(string)) (string, error) {
	selected := monitor.CaddyInstallOptions{WithoutStart: !options.Start, WithoutEnable: !options.Enable}
	switch options.Method {
	case monitor.CaddyBinary:
		selected.Version = options.Version
		if selected.Version == "" {
			var err error
			selected.Version, err = backend.latest(ctx)
			if err != nil {
				return "", err
			}
		}
	case monitor.CaddyDocker:
		selected.DockerTag = strings.TrimPrefix(options.Version, "v")
	}
	if options.OpenPorts {
		if options.Firewall == "" {
			return "", errCaddyPanelTarget
		}
		selected.Firewall = options.Firewall
	}
	if options.ReplaceConflicting && len(install.Conflicts) == 0 {
		return "", errCaddyPanelTarget
	}
	if options.ReplaceConflicting {
		selected.ReplaceConflicting = install.Conflicts
	}
	result, err := monitor.InstallCaddy(ctx, run, install, options.Method, progress, selected)
	return result.Version, err
}

func (caddyMonitorBackend) update(ctx context.Context, run monitor.CaddyRunner, install monitor.CaddyInstall, version string, repair bool, progress func(string)) (string, error) {
	var selected monitor.CaddyUpdateOptions
	switch install.Method {
	case monitor.CaddyBinary:
		if len(install.Modules) == 0 {
			if number := caddyVersionNumber.FindString(version); number != "" {
				selected.Version = "v" + number
			} else {
				return "", errCaddyPanelTarget
			}
		} else if repair {
			return "", errCaddyPanelTarget
		}
	case monitor.CaddyDocker:
		if !repair {
			selected.DockerTag = caddyVersionNumber.FindString(version)
			if selected.DockerTag == "" {
				return "", errCaddyPanelTarget
			}
		} else if install.ContainerImage == "" || strings.Contains(install.ContainerImage, "@") {
			return "", errCaddyPanelTarget
		}
	default:
		if repair {
			return "", errCaddyPanelTarget
		}
	}
	result, err := monitor.UpdateCaddy(ctx, run, install, progress, selected)
	return result.Version, err
}

func (caddyMonitorBackend) uninstall(ctx context.Context, run monitor.CaddyRunner, install monitor.CaddyInstall, purge bool, progress func(string)) (string, error) {
	return "", monitor.UninstallCaddy(ctx, run, install, progress, monitor.CaddyUninstallOptions{RemoveData: purge})
}

func (caddyMonitorBackend) managedPreview(ctx context.Context, run monitor.CaddyRunner, install monitor.CaddyInstall) (caddyBackupPreview, error) {
	change, err := monitor.PreviewCaddyManaged(ctx, run, install)
	return caddyChangePreview(change), err
}

func caddyChangePreview(change monitor.CaddyChange) caddyBackupPreview {
	preview := caddyBackupPreview{Change: change}
	before := func(name string) {
		if content, ok := change.Expect[name]; ok {
			preview.Files = append(preview.Files, monitor.CaddyFile{Path: name, Content: content, Size: int64(len(content))})
		}
	}
	for _, file := range change.Writes {
		before(file.Path)
	}
	for _, name := range change.Deletes {
		before(name)
	}
	return preview
}

func (caddyMonitorBackend) migrateBinary(ctx context.Context, run monitor.CaddyRunner, install monitor.CaddyInstall, progress func(string)) (string, error) {
	result, err := monitor.MigrateBinaryToPackage(ctx, run, install, progress)
	return result.Version, err
}

func (caddyMonitorBackend) nginxToCaddy(sites []monitor.NginxSite, dump string) ([]caddyNginxSite, error) {
	var drafts []monitor.CaddyNginxDraft
	if dump != "" {
		var err error
		drafts, err = monitor.NginxDumpToCaddySites(dump)
		if err != nil {
			return nil, err
		}
	} else {
		drafts = monitor.NginxToCaddySites(sites)
	}
	var rows []caddyNginxSite
	for _, site := range drafts {
		rows = append(rows, caddyNginxSite{Name: site.Name, Content: site.Content, Notes: site.Notes})
	}
	return rows, nil
}

func (caddyMonitorBackend) nginxCutover(ctx context.Context, run monitor.CaddyRunner, install monitor.CaddyInstall, change monitor.CaddyChange, progress func(string)) (string, error) {
	result, err := monitor.CaddyNginxCutover(ctx, run, install, change, progress)
	return caddyApplyOutput(result), err
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
