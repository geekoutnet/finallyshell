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
	choice, version    int
	flags              caddyInstallOptions
	facts              caddyInstallFacts
	failure            string
}

func (a *App) refreshCaddyInstall(s *Session) { a.caddyRefresh(s) }

func caddyNativeMethod(os string) string {
	switch strings.ToLower(os) {
	case "debian", "ubuntu":
		return monitor.CaddyAPT
	case "fedora", "rhel", "centos", "rocky", "almalinux", "ol":
		return monitor.CaddyDNF
	case "arch", "manjaro":
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
			page.facts = facts
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
		if method == caddyNativeMethod(install.OS) {
			labels[index] = locales.CpanelRecommended(labels[index])
		}
	}
	ui.Row(c).Gap(8).Wrap().AlignItems(ui.Center).Children(func() {
		if install.OS != "" {
			ui.Text(c, locales.CpanelOS(install.OS)).TextColor(theme.TextMuted)
		}
		systemSelect(c, &page.choice, locales.CpanelMethod(), labels)
	})
	options := &page.flags
	options.Method = methods[page.choice]
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
			ui.Checkbox(c, &options.ReplaceConflicting, locales.CpanelReplaceConflicts())
		}
	}
	blocked := len(install.Conflicts) != 0 && !options.ReplaceConflicting
	if blocked {
		options.Start, options.Enable = false, false
	}
	ui.Row(c).Gap(10).Wrap().Children(func() {
		ui.Checkbox(c, &options.Start, locales.CpanelStartNow()).Disabled(blocked)
		ui.Checkbox(c, &options.Enable, locales.CpanelAutostart()).Disabled(blocked)
		ui.Checkbox(c, &options.OpenPorts, locales.CpanelOpenPorts()).Disabled(page.facts.Firewall == "").Tooltip(locales.CpanelFirewallHint())
	})
	if ui.PrimaryButton(c, locales.CpanelReviewInstall()).AlignSelf(ui.Start).Clicked() {
		a.caddyConfirmInstall(s, install, *options, page.facts)
	}
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
		steps = append(steps, locales.CpanelReplaceWarning(strings.Join(install.Conflicts, ", ")))
	}
	steps = append(steps, locales.CpanelStartOption(caddyBool(options.Start)), locales.CpanelBootOption(caddyBool(options.Enable)))
	if options.OpenPorts {
		steps = append(steps, locales.CpanelFirewallSteps(facts.Firewall))
	}
	return strings.Join(steps, "\n\n")
}

func (a *App) caddyConfirmInstall(s *Session, install monitor.CaddyInstall, options caddyInstallOptions, facts caddyInstallFacts) {
	if install.Method != "" || !slices.Contains(caddyInstallMethods(install.OS, facts.Docker), options.Method) {
		return
	}
	if options.Version != "" && (!caddyValidRelease(options.Version) || !slices.Contains(facts.Versions, options.Version) || options.Method != monitor.CaddyBinary && options.Method != monitor.CaddyDocker) {
		return
	}
	options.ReplaceConflicting = options.ReplaceConflicting && caddyConflictServices(install.Conflicts)
	options.OpenPorts = options.OpenPorts && (facts.Firewall == "ufw" || facts.Firewall == "firewalld")
	if len(install.Conflicts) != 0 && !options.ReplaceConflicting {
		options.Start, options.Enable = false, false
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
	reason := ""
	if latest == "" {
		reason = locales.CpanelNoUpdate()
	} else {
		ui.Text(c, locales.CpanelUpdateAvailable(latest)).TextColor(theme.Accent)
		ui.Link(c, locales.CpanelReleaseNotes(), "https://github.com/caddyserver/caddy/releases/latest")
	}
	caddyActions(c,
		caddyAction{locales.CpanelUpdate(), reason, func() { a.caddyConfirmUpdate(s, latest, false) }},
		caddyAction{locales.CpanelRepair(), "", func() { a.caddyConfirmUpdate(s, install.Version, true) }},
		caddyAction{locales.CpanelUninstall(), "", func() { a.caddyConfirmUninstall(s, false) }},
		caddyAction{locales.CpanelPurge(), "", func() { a.caddyConfirmUninstall(s, true) }},
	)
}

func (a *App) caddyConfirmUpdate(s *Session, version string, repair bool) {
	install := s.caddy.state.install
	title := locales.CpanelUpdate()
	if repair {
		title = locales.CpanelRepair()
	} else if version == "" {
		return
	}
	target := locales.CpanelUpdateTarget(install.Version, version, caddyInstallName(install.Method))
	dialog := a.caddyConfirm(s, title, target, locales.CpanelUpdateNote(), func(ctx context.Context, backend caddyBackend, run monitor.CaddyRunner, install monitor.CaddyInstall, progress func(string)) (string, error) {
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
