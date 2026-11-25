package app

import (
	"context"
	"slices"
	"strings"
	"unicode/utf8"

	"finallyshell/internal/monitor"
	"finallyshell/locales"
	"github.com/egoist/mygo/ui"
)

func (a *App) caddyMigrateActions(c *ui.Context, s *Session, install monitor.CaddyInstall) {
	ui.Text(c, locales.CpanelMigration()).FontSize(16).Bold()
	ui.Text(c, locales.CpanelAdoptNote()).TextColor(theme.TextMuted)
	page := &s.caddy.install
	if page.adopted {
		ui.Text(c, locales.CpanelAdopted()).TextColor(theme.Accent)
	}
	managedReason, packageReason, nginxReason := "", "", ""
	switch {
	case !caddyIsCaddyfile(install):
		managedReason = locales.CpanelCaddyfileOnly()
	case install.Managed:
		managedReason = locales.CpanelAlreadyManaged()
	case install.Method == monitor.CaddyDocker:
		managedReason = locales.CpanelDockerMigration()
	}
	if install.Method != monitor.CaddyBinary || !slices.Contains([]string{monitor.CaddyAPT, monitor.CaddyDNF}, caddyNativeMethod(install.OS)) || len(install.Modules) != 0 {
		packageReason = locales.CpanelNoPackageMigration()
	}
	if !caddyIsCaddyfile(install) {
		nginxReason = locales.CpanelCaddyfileOnly()
	}
	caddyActions(c,
		caddyAction{locales.CpanelAdopt(), "", func() {
			page.adopted = true
			a.openCaddyPage(s, caddySecOverview)
		}},
		caddyAction{locales.CpanelConvertManaged(), managedReason, func() {
			caddyReadDialog(a, s, locales.CpanelConvertManaged(), caddyBackend.managedPreview, func(dialog *systemDialog, preview caddyBackupPreview) {
				preview.Change.Reload = true
				a.caddyReviewChange(s, dialog, preview)
			})
		}},
		caddyAction{locales.CpanelBinaryToPackage(), packageReason, func() {
			a.caddyConfirm(s, locales.CpanelBinaryToPackage(), locales.CpanelPackageSteps(caddyInstallName(caddyNativeMethod(install.OS)), install.Config, install.DataDir), locales.CpanelPackageNote(),
				func(ctx context.Context, backend caddyBackend, run monitor.CaddyRunner, install monitor.CaddyInstall, progress func(string)) (string, error) {
					return backend.migrateBinary(ctx, run, install, progress)
				})
		}},
		caddyAction{locales.CpanelImportNginx(), nginxReason, func() { a.caddyImportNginx(s) }},
	)
	ui.Text(c, locales.CpanelDockerMigration()).TextColor(theme.TextMuted)
}

type caddyNginxPreview struct {
	main  monitor.CaddyFile
	sites []caddyNginxSite
}

func (a *App) caddyImportNginx(s *Session) {
	caddyReadDialog(a, s, locales.CpanelImportNginx(), func(backend caddyBackend, ctx context.Context, run monitor.CaddyRunner, install monitor.CaddyInstall) (caddyNginxPreview, error) {
		var preview caddyNginxPreview
		files, err := backend.files(ctx, run, install)
		if err != nil {
			return preview, err
		}
		for _, file := range files {
			if file.Path == install.Config && caddyValidPath(file.Path) {
				preview.main = file.CaddyFile
				break
			}
		}
		if preview.main.Path == "" {
			return preview, errCaddyPanelTarget
		}
		inventory, err := monitor.ReadNginxInventory(ctx, func(ctx context.Context, script string) (string, string, error) {
			return run(ctx, monitor.NginxToolPath+script, nil, nil)
		})
		if err != nil {
			return preview, err
		}
		preview.sites, err = backend.nginxToCaddy(inventory.Sites)
		return preview, err
	}, func(dialog *systemDialog, preview caddyNginxPreview) { a.caddyNginxReview(s, dialog, preview) })
}

func (a *App) caddyNginxReview(s *Session, dialog *systemDialog, preview caddyNginxPreview) {
	if len(preview.sites) == 0 {
		dialog.output = locales.CpanelNoNginxSites()
		return
	}
	sites := slices.Clone(preview.sites)
	names := make([]string, len(sites))
	selected := make([]bool, len(sites))
	for index, site := range sites {
		names[index] = locales.CpanelNginxSiteNumber(index+1, site.Name)
	}
	index := 0
	cutover := false
	install := s.caddy.state.install
	canCutover := install.ServiceManager == "systemd" && install.Method != monitor.CaddyDocker &&
		monitor.ValidService(install.Unit) && len(install.Conflicts) == 1 && install.Conflicts[0] == "nginx"
	dialog.body = func(c *ui.Context, height float32) {
		systemSelect(c, &index, locales.CaddySites(), names)
		ui.Checkbox(c, &selected[index], locales.CpanelIncludeSite())
		if len(sites[index].Notes) != 0 {
			ui.Scroll(c).MaxHeight(64).FillWidth().Children(func() {
				ui.Text(c, strings.Join(sites[index].Notes, "\n")).TextColor(theme.Warning).FillWidth()
			})
		}
		ui.TextArea(c.Key(index), &sites[index].Content).Font("monospace").Height(min(245, height/3)).FillWidth().Label(locales.CpanelGeneratedSite())
		if canCutover {
			ui.Checkbox(c, &cutover, locales.CpanelCutoverNginx())
		}
		count := caddyNginxSelection(sites, selected, nil)
		if ui.Button(c, locales.CpanelReviewImport(count)).Disabled(count == 0).Clicked() {
			change, _ := caddyNginxChange(preview.main, sites, selected, caddyServiceRunning(install))
			next := &systemDialog{client: s.client, title: locales.CpanelImportNginx(), target: preview.main.Path}
			s.caddy.state.dialog = next
			a.caddyReviewChange(s, next, caddyBackupPreview{Files: []monitor.CaddyFile{preview.main}, Change: change})
			if cutover && canCutover {
				next.consequence = locales.CpanelNginxCutoverNote()
				next.confirm = func() {
					confirmation := a.caddyConfirm(s, locales.CpanelCutoverNginx(), preview.main.Path, locales.CpanelNginxCutoverNote(),
						func(ctx context.Context, backend caddyBackend, run monitor.CaddyRunner, install monitor.CaddyInstall, progress func(string)) (string, error) {
							return backend.nginxCutover(ctx, run, install, change, progress)
						})
					a.caddyRequireWord(confirmation, locales.CpanelTypeCutover(), "caddy")
				}
			} else if !change.Reload {
				next.consequence = locales.CpanelImportWithoutStart()
			}
		}
	}
}

func caddyServiceRunning(install monitor.CaddyInstall) bool {
	return install.Active == "active" || install.Active == "running"
}

func caddyNginxChange(main monitor.CaddyFile, sites []caddyNginxSite, chosen []bool, reload bool) (monitor.CaddyChange, int) {
	var snippets []string
	caddyNginxSelection(sites, chosen, func(text string) { snippets = append(snippets, text) })
	if !caddyValidPath(main.Path) || len(snippets) == 0 {
		return monitor.CaddyChange{}, 0
	}
	file := monitor.CaddyFile{Path: main.Path, Content: strings.TrimRight(main.Content, "\r\n") + "\n\n" + strings.Join(snippets, "\n\n") + "\n"}
	return monitor.CaddyChange{Writes: []monitor.CaddyFile{file}, Expect: map[string]string{main.Path: main.Content}, Reload: reload}, len(snippets)
}

func caddyNginxSelection(sites []caddyNginxSite, chosen []bool, include func(string)) int {
	count := 0
	for index, site := range sites {
		text := strings.TrimSpace(site.Content)
		if index < len(chosen) && chosen[index] && text != "" && !strings.ContainsRune(text, 0) && utf8.ValidString(text) {
			count++
			if include != nil {
				include(text)
			}
		}
	}
	return count
}
