package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"net/url"
	"os/exec"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"finallyshell/internal/caddyfile"
	"finallyshell/internal/monitor"
	"finallyshell/locales"
	"github.com/egoist/mygo/ui"
)

func (a *App) caddySiteActions(s *Session, row *caddySiteRow) []caddySiteAction {
	page := &s.caddy.sites
	base := page.blocked()
	edit, enabled, address, code := base, base, base, ""
	name := ""
	if row == nil {
		base, edit, enabled, address, code = locales.CsiteSelect(), locales.CsiteSelect(), locales.CsiteSelect(), locales.CsiteSelect(), locales.CsiteSelect()
	} else {
		name = row.id
		if edit == "" {
			switch {
			case !row.active():
				edit = locales.CsiteEnableFirst()
			case row.raw:
				edit = locales.CsiteRawSite()
			}
		}
		if address == "" && !row.active() {
			address = locales.CsiteEnableFirst()
		} else if address == "" && len(caddySiteURLs(row.node.Args)) == 0 {
			address = locales.CsiteNoURL()
		}
	}
	lookup := func(run func(*caddySiteRow)) func() {
		return func() {
			if current := page.find(name); current != nil {
				run(current)
			}
		}
	}
	toggle := locales.CsiteDisable()
	if row != nil && !row.active() {
		toggle = locales.CsiteEnable()
	}
	return []caddySiteAction{
		{title: locales.Edit(), reason: edit, run: lookup(func(row *caddySiteRow) { a.caddySiteOpenForm(s, row, false, false) })},
		{title: locales.CsiteDuplicate(), reason: edit, run: lookup(func(row *caddySiteRow) { a.caddySiteOpenForm(s, row, true, false) })},
		{title: toggle, reason: enabled, run: lookup(func(row *caddySiteRow) { a.caddySiteConfirm(s, row, toggle, false) })},
		{title: locales.Delete(), reason: base, danger: true, run: lookup(func(row *caddySiteRow) { a.caddySiteConfirm(s, row, locales.Delete(), true) })},
		{title: locales.CsiteCode(), reason: code, run: lookup(func(row *caddySiteRow) {
			a.caddySectionEditor(s, &page.caddySectionFiles, row.file, func() { a.refreshCaddySites(s) })
		})},
		{title: locales.CsiteOpenBrowser(), reason: address, run: lookup(func(row *caddySiteRow) {
			a.caddySiteURLDialog(s, row, true)
		})},
		{title: locales.CsiteProbe(), reason: address, run: lookup(func(row *caddySiteRow) {
			a.caddySiteURLDialog(s, row, false)
		})},
	}
}

func caddySiteChange(main, file monitor.CaddyFile, text string) monitor.CaddyChange {
	expect := map[string]string{file.Path: file.Content}
	if main.Path != "" {
		expect[main.Path] = main.Content
	}
	return monitor.CaddyChange{Writes: []monitor.CaddyFile{{Path: file.Path, Content: text}}, Expect: expect, Reload: true}
}

func (a *App) caddySiteConfirm(s *Session, row *caddySiteRow, action string, remove bool) {
	page := &s.caddy.sites
	if page.blocked() != "" {
		return
	}
	dialog := &systemDialog{client: s.client, title: action, target: row.name() + "\n" + row.file.Path}
	page.dialog = dialog
	var text string
	var err error
	if remove {
		text, err = row.doc.Replace(row.node, "")
		dialog.consequence = locales.CsiteDeleteWarning()
	} else if row.active() {
		text, err = row.doc.DisableSite(row.node)
		dialog.consequence = locales.CsiteDisableWarning()
	} else {
		text, err = row.doc.EnableSite(row.node)
		dialog.consequence = locales.CsiteEnableWarning()
	}
	if err != nil {
		dialog.failure = locales.CsiteRawConfig()
		return
	}
	main, _ := page.main()
	change := caddySiteChange(main, row.file, text)
	file := row.file
	dialog.confirm = func() {
		if page.blocked() == "" {
			a.caddySiteWork(s, dialog, nil, true, func(context.Context) (monitor.CaddyChange, error) { return change, nil })
		}
	}
	if !caddySectionPath(file.Path) {
		dialog.confirm = nil
		dialog.failure = locales.CsiteRawConfig()
	}
}

func (a *App) caddySiteOpenForm(s *Session, row *caddySiteRow, duplicate, templates bool) {
	page := &s.caddy.sites
	if page.blocked() != "" || page.mainDoc == nil {
		return
	}
	if page.form != nil {
		page.form.clearPasswords()
	}
	main, _ := page.main()
	file, doc := main, page.mainDoc
	var node *caddyfile.Node
	spec := caddyfile.SiteSpec{Type: caddyfile.Static}
	title, original := locales.CsiteNew(), ""
	if row != nil {
		if row.raw || !row.active() {
			return
		}
		spec, original = row.spec, row.name()
		if duplicate {
			spec.Addresses = nil
			title = locales.CsiteDuplicate()
		} else {
			file, doc, node, title = row.file, row.doc, row.node, locales.Edit()
		}
	}
	form := newCaddySiteForm(spec)
	if templates {
		form = newCaddySiteTemplates()
		title = locales.CsiteTemplates()
	}
	page.form = form
	dialog := &systemDialog{client: s.client, title: title}
	page.dialog = dialog
	var body func(*ui.Context, float32)
	body = func(c *ui.Context, height float32) {
		options := caddySiteFormOptions{PHPSockets: page.phpSockets, DockerPorts: caddySiteDockerPorts(s, page.inst), ReloadPHPSockets: func() { a.caddySitePHPSockets(s, dialog) }}
		if dialog.output != "" || dialog.failure != "" {
			height -= min(220, height/4) + 22
		}
		value := form.view(c, height, page.inst, options)
		if form.templates {
			return
		}
		blocked := page.blocked()
		if blocked == "" && page.addressConflict(node, value.Addresses) {
			blocked = locales.CsiteConflict()
		}
		if blocked != "" {
			ui.Text(c, blocked).TextColor(theme.Warning).FontSize(12).FillWidth()
		}
		ui.Row(c).FillWidth().Wrap().Gap(8).Justify(ui.End).Children(func() {
			if ui.Button(c, locales.CsiteValidate()).Disabled(value.Err != nil || blocked != "").Clicked() {
				a.caddySiteFormWork(s, dialog, body, form, main, file, doc, node, false)
			}
			if ui.PrimaryButton(c, locales.Save()).Disabled(value.Err != nil || blocked != "").Clicked() {
				target := strings.Join(value.Addresses, ", ")
				if original != "" && target != original {
					target = original + " → " + target
				}
				dialog.target = target + "\n" + file.Path
				dialog.body, dialog.failure, dialog.output = nil, "", ""
				dialog.consequence = locales.CsiteSaveWarning()
				dialog.back = func() { dialog.body, dialog.target, dialog.consequence, dialog.confirm = body, "", "", nil }
				dialog.confirm = func() {
					a.caddySiteFormWork(s, dialog, body, form, main, file, doc, node, true)
				}
			}
		})
	}
	dialog.body = body
}

func (page *caddySitesPage) addressConflict(original *caddyfile.Node, addresses []string) bool {
	for _, row := range page.rows {
		if row.node == original || !row.active() {
			continue
		}
		for _, address := range addresses {
			if slices.ContainsFunc(row.node.Args, func(existing string) bool { return strings.EqualFold(address, existing) }) {
				return true
			}
		}
	}
	return false
}

func (a *App) caddySiteFormWork(s *Session, dialog *systemDialog, body func(*ui.Context, float32), form *caddySiteForm, main, file monitor.CaddyFile, doc *caddyfile.Document, node *caddyfile.Node, apply bool) {
	page := &s.caddy.sites
	value := form.snapshot(page.inst)
	if page.blocked() != "" || value.Err != nil || page.addressConflict(node, value.Addresses) {
		dialog.body, dialog.target, dialog.consequence = body, "", ""
		return
	}
	backend, run := a.caddySectionOps(s, &page.caddySectionFiles)
	inst := page.inst
	invalid := errors.New(locales.CsiteRawConfig())
	if apply {
		form.clearPasswords()
	}
	a.caddySiteWork(s, dialog, body, apply, func(ctx context.Context) (monitor.CaddyChange, error) {
		text, err := value.render(ctx, func(ctx context.Context, password string) (string, error) {
			return backend.hash(ctx, run, inst, password)
		})
		if err != nil {
			return monitor.CaddyChange{}, err
		}
		if node != nil {
			text, err = doc.ReplaceSite(node, text)
		} else {
			text, err = doc.Append(text)
		}
		if err != nil {
			return monitor.CaddyChange{}, invalid
		}
		return caddySiteChange(main, file, text), nil
	})
}

func (a *App) caddySiteWork(s *Session, dialog *systemDialog, body func(*ui.Context, float32), apply bool, build func(context.Context) (monitor.CaddyChange, error)) {
	page := &s.caddy.sites
	backend, run := a.caddySectionOps(s, &page.caddySectionFiles)
	inst, banner := page.inst, locales.SvcDone()
	ctx, cancel := context.WithTimeout(systemContext(s), 2*time.Minute)
	dialog.cancel, dialog.busy, dialog.done = cancel, true, false
	dialog.body, dialog.consequence, dialog.back = nil, "", nil
	dialog.output, dialog.failure, dialog.rootDenied = "", "", false
	page.work = true
	progress, flush := a.caddySectionProgress(dialog, func() bool { return page.dialog == dialog && dialog.busy })
	go func() {
		defer cancel()
		run := caddySectionProgressRunner(run, progress)
		change, err := build(ctx)
		var result monitor.CaddyApplyResult
		if err == nil && ctx.Err() != nil {
			err = ctx.Err()
		}
		if err == nil {
			if apply {
				result, err = backend.apply(ctx, run, inst, change)
			} else {
				result.Validate, err = backend.validate(ctx, run, inst, change.Writes)
			}
		}
		flush()
		a.post(func() {
			if !systemReady(s, dialog.client) {
				return
			}
			page.work = false
			dialog.busy = false
			dialog.cancel = nil
			dialog.rootDenied = siteRootDenied(err)
			dialog.failure = systemError("", err)
			if errors.Is(err, monitor.ErrCaddyChanged) {
				dialog.failure = locales.CsiteChanged()
			}
			if result.Validate != "" {
				dialog.output = systemTail(strings.TrimSpace(dialog.output+"\n"+systemRedact(result.Validate)), 65536)
			}
			if result.Backup != "" {
				backup := locales.SiteBackup(result.Backup)
				if !strings.Contains(dialog.output, backup) {
					dialog.output = strings.TrimSpace(dialog.output + "\n" + backup)
				}
			}
			if result.RolledBack {
				dialog.output = strings.TrimSpace(dialog.output + "\n" + locales.CsiteRollback())
			}
			if err == nil && dialog.output == "" {
				dialog.output = banner
			}
			if !apply || err != nil {
				dialog.body, dialog.target = body, ""
			} else {
				dialog.done = true
				a.refreshCaddySites(s)
			}
		})
	}()
}

func caddySiteURLs(addrs []string) []string {
	var result []string
	for _, address := range addrs {
		if strings.ContainsAny(address, "*{}\\%#?\x00\r\n") || strings.HasPrefix(address, ":") {
			continue
		}
		if !strings.Contains(address, "://") {
			address = "https://" + address
		}
		parsed, err := url.Parse(address)
		if err != nil || parsed.Hostname() == "" || parsed.User != nil || parsed.Scheme != "http" && parsed.Scheme != "https" {
			continue
		}
		if !slices.Contains(result, parsed.String()) {
			result = append(result, parsed.String())
		}
	}
	return result
}

func (a *App) caddySiteURLDialog(s *Session, row *caddySiteRow, openBrowser bool) {
	page := &s.caddy.sites
	choices := caddySiteURLs(row.node.Args)
	if !row.active() || len(choices) == 0 {
		return
	}
	address := choices[0]
	title := locales.CsiteProbe()
	if openBrowser {
		title = locales.CsiteOpenBrowser()
	}
	dialog := &systemDialog{client: s.client, title: title, target: row.name()}
	page.dialog = dialog
	dialog.body = func(c *ui.Context, _ float32) {
		ui.Select(c, &address, choices).FillWidth().Label(locales.CsiteAddresses())
		if openBrowser {
			if ui.PrimaryButton(c, title).AlignSelf(ui.End).Clicked() {
				c.OpenURL(address)
				page.dialog = nil
			}
			return
		}
		ui.Row(c).Gap(8).Wrap().Children(func() {
			if ui.Button(c, locales.CsiteProbeLocal()).Clicked() {
				a.caddySiteProbe(s, dialog, address, false)
			}
			if ui.Button(c, locales.CsiteProbeRemote()).Clicked() {
				a.caddySiteProbe(s, dialog, address, true)
			}
		})
	}
}

func caddySiteCurlArgs(address string) []string {
	return []string{"curl", "-q", "-sSI", "--connect-timeout", "5", "--max-time", "12", "--proto", "=http,https", "--", address}
}

func caddySiteLocalCurl(ctx context.Context, args []string) (string, string, error) {
	command := exec.CommandContext(ctx, args[0], args[1:]...)
	var stderr strings.Builder
	command.Stderr = &stderr
	out, err := command.Output()
	return string(out), stderr.String(), err
}

func (a *App) caddySiteProbe(s *Session, dialog *systemDialog, address string, remote bool) {
	args := caddySiteCurlArgs(address)
	var run systemRunner
	if remote {
		if probe := s.caddy.sites.backend.runner; probe != nil {
			run = systemRunner(probe)
		} else {
			run = systemReader(s)
		}
	} else {
		run = func(ctx context.Context, _ string, _ io.Reader, _ func(string)) (string, string, error) {
			return caddySiteLocalCurl(ctx, args)
		}
	}
	systemScript := monitor.SystemCommand(args...)
	a.systemWork(s, dialog, run, systemScript, nil, nil)
}

func caddySiteDockerPorts(s *Session, inst monitor.CaddyInstall) []caddySiteChoice {
	if inst.Method == monitor.CaddyDocker || s.overview.docker.err != nil || s.overview.docker.d.State != monitor.DockerOK {
		return nil
	}
	var choices []caddySiteChoice
	for _, container := range s.overview.docker.d.Containers {
		if !container.Running || container.Paused || container.Restarting {
			continue
		}
		for _, bind := range container.Ports {
			if bind.Proto != "tcp" || bind.HostPort < 1 || bind.HostPort > 65535 {
				continue
			}
			host := bind.HostIP
			switch host {
			case "", "0.0.0.0":
				host = "127.0.0.1"
			case "::":
				host = "::1"
			}
			if _, err := netip.ParseAddr(host); err != nil {
				continue
			}
			upstream := net.JoinHostPort(host, strconv.Itoa(bind.HostPort))
			choices = append(choices, caddySiteChoice{label: fmt.Sprintf("%s · %s", container.Name, upstream), value: upstream})
		}
	}
	slices.SortFunc(choices, func(left, right caddySiteChoice) int { return strings.Compare(left.label, right.label) })
	return slices.CompactFunc(choices, func(left, right caddySiteChoice) bool { return left.label == right.label })
}

func caddySitePHPCommand() string {
	return `for socket in /run/php/*fpm*.sock /run/php-fpm/*.sock /run/php-fpm.sock /run/phpfpm/*.sock /var/run/php/*.sock /var/run/php-fpm/*.sock; do
  [ -S "$socket" ] || continue
  ` + monitor.SystemCommand("printf", "%s\n") + ` "$socket"
done
`
}

func caddySiteParsePHPSockets(out string) []string {
	var sockets []string
	for line := range strings.SplitSeq(out, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if caddySectionPath(line) && (strings.HasPrefix(line, "/run/") || strings.HasPrefix(line, "/var/run/")) && path.Ext(line) == ".sock" {
			sockets = append(sockets, "unix/"+line)
		}
	}
	slices.Sort(sockets)
	return slices.Compact(sockets)
}

func (a *App) caddySitePHPSockets(s *Session, dialog *systemDialog) {
	page := &s.caddy.sites
	if page.phpReading || page.inst.Method == monitor.CaddyDocker {
		return
	}
	page.phpReading = true
	_, run := a.caddySectionOps(s, &page.caddySectionFiles)
	ctx, cancel := context.WithTimeout(systemContext(s), 15*time.Second)
	go func() {
		defer cancel()
		out, stderr, err := run(ctx, caddySitePHPCommand(), nil, nil)
		found := caddySiteParsePHPSockets(out)
		a.post(func() {
			if !systemReady(s, dialog.client) {
				return
			}
			page.phpReading = false
			if page.dialog != dialog {
				return
			}
			if err != nil {
				dialog.failure = systemError(stderr, err)
			} else {
				page.phpSockets = found
				if len(found) == 0 {
					dialog.output = locales.CsiteNoSockets()
				}
			}
		})
	}()
}
