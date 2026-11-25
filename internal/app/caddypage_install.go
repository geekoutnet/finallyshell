package app

import (
	"context"
	"errors"
	"net/url"
	"slices"
	"strings"

	"finallyshell/internal/monitor"
	"finallyshell/locales"
	"github.com/egoist/mygo/ui"
)

type caddyInstallPage struct {
	requested, adopted bool
	factsReady         bool
	choice, version    int
	flags              caddyInstallOptions
	facts              caddyInstallFacts
	failure            string
}

func (a *App) refreshCaddyInstall(s *Session) { a.caddyRefresh(s) }

func caddyNativeMethod(os string) string {
	switch strings.ToLower(os) {
	case "debian", "ubuntu", "raspbian", "linuxmint":
		return monitor.CaddyAPT
	case "fedora", "rhel", "centos", "rocky", "almalinux":
		return monitor.CaddyDNF
	case "arch", "manjaro", "endeavouros":
		return monitor.CaddyPacman
	case "alpine":
		return monitor.CaddyAPK
	}
	return ""
}

func caddyInstallMethods(os string, docker bool) []string {
	methods := []string{}
	if recommended := caddyNativeMethod(os); recommended != "" {
		methods = append(methods, recommended)
	}
	methods = append(methods, monitor.CaddyBinary)
	if docker {
		methods = append(methods, monitor.CaddyDocker)
	}
	return methods
}

func (a *App) caddyInstallSection(c *ui.Context, s *Session) {
	if !a.caddyDetected(c, s, true) {
		return
	}
	page, install := &s.caddy.install, s.caddy.state.install
	if !page.requested {
		page.requested = true
		page.flags.Start = len(install.Conflicts) == 0
		page.flags.Enable = page.flags.Start
		caddyRead(a, s, caddyBackend.installFacts, func(facts caddyInstallFacts, err error) {
			page.facts, page.factsReady = facts, true
			if !errors.Is(err, monitor.ErrCaddyNotImplemented) {
				page.failure = caddyError(err)
			}
		})
		if !s.caddy.overview.requested && install.Method != "" && install.PackageLatest == "" && (install.Method == monitor.CaddyBinary || install.Method == monitor.CaddyDocker) {
			caddyRead(a, s, func(backend caddyBackend, ctx context.Context, _ monitor.CaddyRunner, _ monitor.CaddyInstall) (string, error) {
				return backend.latest(ctx)
			}, func(latest string, _ error) { s.caddy.overview.data.latest = latest })
		}
	}
	ui.Scroll(c).Grow(1).MinHeight(0).FillWidth().Children(func() {
		ui.Column(c).Padding(8, 12).Gap(8).FillWidth().Children(func() {
			caddyNotice(c, page.failure)
			if install.Method == monitor.CaddyNotInstalled {
				a.caddyInstallForm(c, s, page, install)
				return
			}
			a.caddyInstalledActions(c, s, install)
			a.caddyMigrateActions(c, s, install)
		})
	})
}

func (a *App) caddyInstallForm(c *ui.Context, s *Session, page *caddyInstallPage, install monitor.CaddyInstall) {
	ui.Text(c, locales.CpanelInstall()).FontSize(18).Bold()
	methods := caddyInstallMethods(install.OS, page.facts.Docker)
	labels := make([]string, len(methods))
	for index, method := range methods {
		labels[index] = caddyInstallName(method)
	}
	ui.Row(c).Gap(8).Wrap().AlignItems(ui.Center).Children(func() {
		if install.OS != "" {
			ui.Text(c, locales.CpanelOS(install.OS)).TextColor(theme.TextMuted)
		}
		systemSelect(c, &page.choice, locales.CpanelMethod(), labels)
	})
	options := &page.flags
	options.Method = methods[page.choice]
	if options.Method == caddyNativeMethod(install.OS) {
		ui.Text(c, locales.CpanelRecommended(labels[page.choice])).TextColor(theme.TextMuted).FillWidth()
	}
	options.Version = ""
	if options.Method == monitor.CaddyBinary || options.Method == monitor.CaddyDocker {
		versions := []string{locales.CpanelLatest()}
		for _, version := range page.facts.Versions {
			if caddyValidRelease(version) && !slices.Contains(versions, version) {
				versions = append(versions, version)
			}
		}
		systemSelect(c, &page.version, locales.CpanelVersion(), versions)
		if page.version > 0 {
			options.Version = versions[page.version]
		}
	}
	if len(install.Conflicts) != 0 {
		ui.Text(c, locales.CpanelConflicts(strings.Join(install.Conflicts, ", "))).TextColor(theme.Warning)
		if caddyConflictServices(install.Conflicts) {
			reason := ""
			if !page.facts.Systemd {
				reason = locales.CwireNeedsSystemd()
				options.ReplaceConflicting = false
			}
			ui.Checkbox(c, &options.ReplaceConflicting, locales.CpanelReplaceConflicts()).Disabled(reason != "").Tooltip(reason)
			if options.ReplaceConflicting {
				options.Start = true
			}
		}
	}
	blocked := len(install.Conflicts) != 0 && !options.ReplaceConflicting
	if blocked {
		options.Start, options.Enable = false, false
	}
	if options.Method == monitor.CaddyDocker && !options.Start {
		options.Enable = false
	}
	startReason, enableReason := "", ""
	if blocked {
		startReason = locales.CpanelConflicts(strings.Join(install.Conflicts, ", "))
		enableReason = startReason
	} else if options.ReplaceConflicting {
		startReason = locales.CwireReplacementNeedsStart()
	}
	if enableReason == "" && options.Method == monitor.CaddyDocker && !options.Start {
		enableReason = locales.CwireDockerNeedsStart()
	}
	ui.Row(c).Gap(10).Wrap().Children(func() {
		ui.Checkbox(c, &options.Start, locales.CpanelStartNow()).Disabled(startReason != "").Tooltip(startReason)
		ui.Checkbox(c, &options.Enable, locales.CpanelAutostart()).Disabled(enableReason != "").Tooltip(enableReason)
		ui.Checkbox(c, &options.OpenPorts, locales.CpanelOpenPorts()).Disabled(page.facts.Firewall == "").Tooltip(locales.CpanelFirewallHint())
	})
	reason := caddyInstallMethodReason(options.Method, page.facts)
	if !page.factsReady {
		reason = locales.CpanelReading()
	} else if reason == "" && options.Method == monitor.CaddyBinary && len(page.facts.Versions) == 0 {
		reason = locales.CwireReleaseUnavailable()
	}
	if ui.PrimaryButton(c, locales.CpanelReviewInstall()).Disabled(reason != "").Tooltip(reason).AlignSelf(ui.Start).Clicked() {
		a.caddyConfirmInstall(s, install, *options, page.facts)
	}
}

func caddyInstallMethodReason(method string, facts caddyInstallFacts) string {
	if method == monitor.CaddyAPK {
		if !facts.OpenRC {
			return locales.CwireNeedsOpenRC()
		}
	} else if method != monitor.CaddyDocker && !facts.Systemd {
		return locales.CwireNeedsSystemd()
	}
	return ""
}

func caddyConflictServices(conflicts []string) bool {
	if len(conflicts) == 0 {
		return false
	}
	for _, conflict := range conflicts {
		switch conflict {
		case "nginx", "apache", "apache2", "httpd":
		default:
			return false
		}
	}
	return true
}

func caddyInstallSteps(install monitor.CaddyInstall, options caddyInstallOptions, facts caddyInstallFacts) string {
	var method string
	switch options.Method {
	case monitor.CaddyAPT:
		method = locales.CpanelStepsApt()
	case monitor.CaddyDNF:
		plugin := "dnf-plugins-core"
		if install.OS == "fedora" {
			plugin = "dnf5-plugins"
		}
		method = locales.CpanelStepsDnf(plugin)
	case monitor.CaddyPacman:
		method = locales.CpanelStepsPacman()
	case monitor.CaddyAPK:
		method = locales.CpanelStepsApk()
	case monitor.CaddyBinary:
		method = locales.CpanelStepsBinary()
	case monitor.CaddyDocker:
		method = locales.CpanelStepsDocker()
	}
	steps := []string{method}
	if options.Method == monitor.CaddyBinary || options.Method == monitor.CaddyDocker {
		version := options.Version
		if version == "" {
			version = locales.CpanelLatest()
		}
		steps = append(steps, locales.CpanelVersionTarget(version))
	}
	if options.ReplaceConflicting {
		steps = append([]string{locales.CpanelReplaceWarning(strings.Join(install.Conflicts, ", "))}, steps...)
	}
	steps = append(steps, locales.CpanelStartOption(caddyBool(options.Start)), locales.CpanelBootOption(caddyBool(options.Enable)))
	if options.OpenPorts {
		firewall := facts.Firewall
		if firewall == "firewalld" {
			firewall = locales.CwireFirewalldDefault()
		}
		steps = append([]string{locales.CpanelFirewallSteps(firewall)}, steps...)
	}
	return strings.Join(steps, "\n\n")
}

func (a *App) caddyConfirmInstall(s *Session, install monitor.CaddyInstall, options caddyInstallOptions, facts caddyInstallFacts) {
	if install.Method != "" || !slices.Contains(caddyInstallMethods(install.OS, facts.Docker), options.Method) || caddyInstallMethodReason(options.Method, facts) != "" {
		return
	}
	if options.Version != "" && (!caddyValidRelease(options.Version) || !slices.Contains(facts.Versions, options.Version) || options.Method != monitor.CaddyBinary && options.Method != monitor.CaddyDocker) {
		return
	}
	options.ReplaceConflicting = options.ReplaceConflicting && facts.Systemd && caddyConflictServices(install.Conflicts)
	options.OpenPorts = options.OpenPorts && (facts.Firewall == "ufw" || facts.Firewall == "firewalld")
	if options.OpenPorts {
		options.Firewall = facts.Firewall
	}
	if options.ReplaceConflicting {
		options.Start = true
	}
	if len(install.Conflicts) != 0 && !options.ReplaceConflicting {
		options.Start, options.Enable = false, false
	}
	if options.Method == monitor.CaddyDocker && !options.Start {
		options.Enable = false
	}
	if options.Method == monitor.CaddyBinary && options.Version == "" {
		if len(facts.Versions) == 0 {
			return
		}
		options.Version = facts.Versions[0]
	}
	dialog := a.caddyConfirm(s, locales.CpanelReviewInstall(), caddyInstallSteps(install, options, facts), locales.CpanelInstallNote(), func(ctx context.Context, backend caddyBackend, run monitor.CaddyRunner, install monitor.CaddyInstall, progress func(string)) (string, error) {
		return backend.install(ctx, run, install, options, progress)
	})
	confirm := dialog.confirm
	dialog.confirm = func() {
		a.openCaddyPage(s, caddySecOverview)
		confirm()
	}
	if options.ReplaceConflicting {
		a.caddyRequireWord(dialog, locales.CpanelTypeCutover(), "caddy")
	}
}

func caddyValidRelease(version string) bool {
	number := strings.TrimPrefix(version, "v")
	return number != "" && caddyVersionNumber.FindString(number) == number
}

func (a *App) caddyInstalledActions(c *ui.Context, s *Session, install monitor.CaddyInstall) {
	ui.Text(c, locales.CpanelInstalledVia(caddyInstallName(install.Method))).Bold().FontSize(18)
	ui.Text(c, strings.TrimSpace(install.Version)).SingleLine().Tooltip(install.Version).TextColor(theme.TextMuted)
	latest := caddyPanelLatest(s)
	reason := caddyUpdateReason(install, false)
	if latest == "" && reason == "" {
		reason = locales.CpanelNoUpdate()
	} else if latest != "" && reason == "" {
		ui.Text(c, locales.CpanelUpdateAvailable(latest)).TextColor(theme.Accent)
		ui.Link(c, locales.CpanelReleaseNotes(), "https://github.com/caddyserver/caddy/releases/latest")
	}
	caddyActions(c,
		caddyAction{locales.CpanelUpdate(), reason, func() { a.caddyConfirmUpdate(s, latest, false) }},
		caddyAction{locales.CpanelRepair(), caddyUpdateReason(install, true), func() { a.caddyConfirmUpdate(s, install.Version, true) }},
		caddyAction{locales.CpanelUninstall(), caddyUninstallReason(install, false), func() { a.caddyConfirmUninstall(s, false) }},
		caddyAction{locales.CpanelPurge(), caddyUninstallReason(install, true), func() { a.caddyConfirmUninstall(s, true) }},
	)
}

func (a *App) caddyConfirmUpdate(s *Session, version string, repair bool) {
	install := s.caddy.state.install
	if caddyUpdateReason(install, repair) != "" {
		return
	}
	title := locales.CpanelUpdate()
	if repair {
		title = locales.CpanelRepair()
	} else if version == "" {
		return
	}
	targetVersion, note := version, locales.CpanelUpdateNote()
	switch {
	case install.Method == monitor.CaddyDocker && repair:
		targetVersion, note = install.ContainerImage, locales.CwireDockerRepair()
	case install.Method == monitor.CaddyBinary && len(install.Modules) > 0:
		targetVersion, note = locales.CpanelLatest(), locales.CwireCustomUpdate()
	case install.Method != monitor.CaddyBinary && install.Method != monitor.CaddyDocker:
		note = locales.CwirePackageUpdate()
	}
	target := locales.CpanelUpdateTarget(install.Version, targetVersion, caddyInstallName(install.Method))
	dialog := a.caddyConfirm(s, title, target, note, func(ctx context.Context, backend caddyBackend, run monitor.CaddyRunner, install monitor.CaddyInstall, progress func(string)) (string, error) {
		return backend.update(ctx, run, install, version, repair, progress)
	})
	if version := caddyVersionNumber.FindString(version); version != "" {
		dialog.body = func(c *ui.Context, _ float32) {
			ui.Link(c, locales.CpanelReleaseNotes(), "https://github.com/caddyserver/caddy/releases/tag/"+url.PathEscape("v"+version))
		}
	}
}

func (a *App) caddyConfirmUninstall(s *Session, purge bool) {
	install := s.caddy.state.install
	if caddyUninstallReason(install, purge) != "" {
		return
	}
	title, note := locales.CpanelUninstall(), locales.CpanelUninstallNote()
	service := install.Unit
	if install.Method == monitor.CaddyDocker {
		service = install.Container
	}
	target := strings.Join([]string{caddyInstallName(install.Method), service, install.Binary}, "\n")
	if purge {
		title, note = locales.CpanelPurge(), locales.CpanelPurgeNote()
		target += "\n" + install.Config + "\n" + install.DataDir
	}
	dialog := a.caddyConfirm(s, title, strings.TrimSpace(target), note, func(ctx context.Context, backend caddyBackend, run monitor.CaddyRunner, install monitor.CaddyInstall, progress func(string)) (string, error) {
		return backend.uninstall(ctx, run, install, purge, progress)
	})
	if purge {
		a.caddyRequireWord(dialog, locales.CpanelTypePurge(), "caddy")
	}
}

func (a *App) caddyRequireWord(dialog *systemDialog, label, word string) {
	value := ""
	confirm := dialog.confirm
	dialog.confirm = nil
	dialog.body = func(c *ui.Context, _ float32) {
		ui.Text(c, label).TextColor(theme.Danger)
		ui.TextInput(c, &value).FillWidth().Label(label)
		dialog.confirm = nil
		if value == word {
			dialog.confirm = confirm
		}
	}
}
