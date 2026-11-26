package app

import (
	"cmp"
	"context"
	"slices"
	"strconv"
	"strings"
	"time"

	"finallyshell/internal/monitor"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

// owServicesPage is the Services section: the init scripts and procd.
type owServicesPage struct {
	poll     sessionPoll
	rows     []monitor.OpenWrtService
	loaded   bool
	err      string
	filter   string
	table    ui.ListState
	row      int
	selected ui.Selection[string]
	sort     ui.SortOrder
}

func (a *App) owServicesSection(c *ui.Context, s *Session) {
	page := &s.server.openwrt.services
	a.owPoll(&page.poll, s, 15*time.Second, func() string { return monitor.OpenWrtServicesScript }, func(out string, err error) {
		page.loaded, page.err = true, ""
		if err != nil {
			page.err = err.Error()
			return
		}
		page.rows = monitor.ParseOpenWrtServices(out)
	})
	query := strings.ToLower(strings.TrimSpace(page.filter))
	var rows []monitor.OpenWrtService
	for _, r := range page.rows {
		if strings.Contains(r.Name, query) {
			rows = append(rows, r)
		}
	}
	slices.SortStableFunc(rows, func(x, y monitor.OpenWrtService) int {
		order := 0
		switch page.sort.Column {
		case "state":
			order = cmp.Compare(owServiceState(x), owServiceState(y))
		case "boot":
			order = cmp.Compare(owServiceBoot(x), owServiceBoot(y))
		}
		if order == 0 {
			order = cmp.Compare(x.Name, y.Name)
		}
		if page.sort.Descending {
			return -order
		}
		return order
	})
	picked := func() []monitor.OpenWrtService {
		var out []monitor.OpenWrtService
		for _, r := range rows {
			if page.selected.Has(r.Name) {
				out = append(out, r)
			}
		}
		return out
	}
	menu := func(m *ui.Menu) {
		sel := picked()
		if len(sel) != 1 {
			m.Item(locales.SvcSelectOne()).Disabled(true)
			return
		}
		r := sel[0]
		labels := map[string]func() string{
			"start": locales.SvcStart, "stop": locales.SvcStop, "restart": locales.SvcRestart,
			"reload": locales.SvcReload, "enable": locales.SvcEnable, "disable": locales.SvcDisable,
		}
		for _, action := range monitor.OpenWrtServiceActions {
			if action == "enable" {
				m.Separator()
			}
			off := action == "enable" && r.Enabled || action == "disable" && !r.Enabled ||
				r.Managed && (action == "start" && r.Running || action == "stop" && !r.Running)
			item := m.Item(labels[action]()).Disabled(off)
			if action == "stop" || action == "disable" {
				item.Danger()
			}
			if item.Chosen() && !off {
				a.owServiceAction(s, r.Name, action, labels[action]())
			}
		}
	}
	owToolbar(c, func() {
		ui.SearchField(c, &page.filter).Placeholder(locales.SvcFilter()).Label(locales.SvcFilter()).Width(200).MinWidth(90).Shrink(1)
		ui.MenuButton(c, locales.SvcActions(), menu)
	})
	owState(c, page.loaded, page.err, len(rows) == 0)
	page.table.Key = func(i int) any { return rows[i].Name }
	page.table.Label = func(i int) string { return rows[i].Name }
	page.table.Sort, page.table.Selection, page.table.Selected, page.table.KeepOffset = &page.sort, &page.selected, &page.row, true
	page.table.CellMenu = func(m *ui.Menu, i, _ int) {
		page.selected.Clear()
		page.selected.Add(rows[i].Name)
		menu(m)
	}
	columns := []ui.TableColumn{
		{ID: "name", Title: locales.OwName(), MinWidth: 160, Sortable: true},
		{ID: "state", Title: locales.SvcState(), Width: 120, Sortable: true},
		{ID: "boot", Title: locales.SvcStartup(), Width: 120, Sortable: true},
		{ID: "pid", Title: locales.ColPID(), Width: 120, Align: ui.End},
	}
	ui.Table(c, &page.table, columns, len(rows), func(i, col int) {
		r := rows[i]
		pids := make([]string, len(r.PIDs))
		for j, p := range r.PIDs {
			pids[j] = strconv.Itoa(p)
		}
		text := []string{r.Name, owServiceState(r), owServiceBoot(r), orDash(strings.Join(pids, " "))}[col]
		cell := ui.Text(c, text).SingleLine().Tooltip(text).MinWidth(0).Shrink(1)
		if col == 1 {
			switch {
			case r.Running:
				cell.TextColor(theme.Success)
			case !r.Managed:
				cell.TextColor(theme.TextMuted)
			}
		}
	}).Grow(1).MinHeight(0).Label(locales.ServerServices())
}

func owServiceState(r monitor.OpenWrtService) string {
	switch {
	case r.Running:
		return locales.SvcRunning()
	case r.Managed:
		return locales.OwStopped()
	}
	return locales.OwOneShot()
}

func owServiceBoot(r monitor.OpenWrtService) string {
	if r.Enabled {
		return locales.OwEnabled()
	}
	return locales.OwDisabled()
}

func (a *App) owServiceAction(s *Session, name, action, label string) {
	script, err := monitor.OpenWrtServiceCommand(name, action)
	if err != nil {
		return
	}
	note := locales.OwServiceNote()
	if action != "enable" && action != "disable" && action != "start" && monitor.OpenWrtServiceCutsSSH(name) {
		note += "\n" + locales.OwCutsConnection()
	}
	a.owRun(s, label, name, note, script, func(s *Session) { s.server.openwrt.services.poll.pollNow() })
}

// owPackagesPage is the Packages section: opkg's or apk's packages.
type owPackagesPage struct {
	poll        sessionPoll
	pm          string
	rows        []monitor.OpenWrtPackage
	lists       bool
	loaded      bool
	err         string
	filter      string
	onlyUpgrade bool
	table       ui.ListState
	row         int
	selected    ui.Selection[string]
	// The install dialog's search.
	name, term string
	found      []monitor.OpenWrtPackage
	searching  bool
	searchErr  string
}

func (a *App) owPackagesSection(c *ui.Context, s *Session) {
	page := &s.server.openwrt.packages
	a.owPoll(&page.poll, s, 10*time.Minute, func() string { return monitor.OpenWrtPackagesScript }, func(out string, err error) {
		page.loaded, page.err = true, ""
		page.pm, page.rows, page.lists = monitor.ParseOpenWrtPackages(out)
		if page.pm == "" {
			page.err = locales.OwNoPackageManager()
			if err != nil {
				page.err = err.Error()
			}
		}
	})
	query := strings.ToLower(strings.TrimSpace(page.filter))
	upgrades := 0
	var rows []monitor.OpenWrtPackage
	for _, r := range page.rows {
		if r.Upgrade != "" {
			upgrades++
		}
		if strings.Contains(r.Name, query) && (!page.onlyUpgrade || r.Upgrade != "") {
			rows = append(rows, r)
		}
	}
	var picked []string
	pickedUpgrade := true
	for _, r := range rows {
		if page.selected.Has(r.Name) {
			picked = append(picked, r.Name)
			pickedUpgrade = pickedUpgrade && r.Upgrade != ""
		}
	}
	after := func(s *Session) { s.server.openwrt.packages.poll.pollNow() }
	owToolbar(c, func() {
		ui.SearchField(c, &page.filter).Placeholder(locales.OwFilterPackages()).Label(locales.OwFilterPackages()).Width(200).MinWidth(90).Shrink(1)
		ui.Checkbox(c, &page.onlyUpgrade, locales.OwOnlyUpgrades(upgrades))
		ui.Spacer(c)
		ready := page.pm != ""
		if ui.Button(c, locales.OwUpdateLists()).Disabled(!ready).Clicked() {
			if script, err := monitor.OpenWrtPackageCommand(page.pm, "update"); err == nil {
				a.owRun(s, locales.OwUpdateLists(), page.pm, locales.OwUpdateListsNote(), script, after)
			}
		}
		if ui.Button(c, locales.OwInstall()).Disabled(!ready).Clicked() {
			a.owInstallPackage(s)
		}
		if ui.Button(c, locales.OwUpgrade()).Disabled(!ready || len(picked) == 0 || !pickedUpgrade).Clicked() {
			if script, err := monitor.OpenWrtPackageCommand(page.pm, "upgrade", picked...); err == nil {
				a.owRun(s, locales.OwUpgrade(), strings.Join(picked, " "), locales.OwUpgradeNote(), script, after)
			}
		}
		if ui.Button(c, locales.OwRemove()).Disabled(!ready || len(picked) == 0).Clicked() {
			if script, err := monitor.OpenWrtPackageCommand(page.pm, "remove", picked...); err == nil {
				a.owRun(s, locales.OwRemove(), strings.Join(picked, " "), locales.OwRemoveNote(), script, after)
			}
		}
	})
	if page.loaded && page.pm != "" && !page.lists {
		ui.Text(c, locales.OwNoLists()).Padding(0, 8, 4, 8).TextColor(theme.Warning)
	}
	owState(c, page.loaded, page.err, len(rows) == 0)
	page.table.Key = func(i int) any { return rows[i].Name }
	page.table.Label = func(i int) string { return rows[i].Name }
	page.table.Selection, page.table.Selected, page.table.KeepOffset = &page.selected, &page.row, true
	columns := []ui.TableColumn{
		{ID: "name", Title: locales.OwName(), MinWidth: 200},
		{ID: "version", Title: locales.OwVersion(), Width: 220},
		{ID: "upgrade", Title: locales.OwUpgradeTo(), Width: 220},
	}
	ui.Table(c, &page.table, columns, len(rows), func(i, col int) {
		r := rows[i]
		text := []string{r.Name, r.Version, r.Upgrade}[col]
		cell := ui.Text(c, text).SingleLine().Tooltip(text).MinWidth(0).Shrink(1)
		if col == 2 {
			cell.TextColor(theme.Accent)
		}
	}).Grow(1).MinHeight(0).Label(locales.OwPackagesCount(len(page.rows)))
}

// owInstallPackage asks for a package to install, with a search of the
// downloaded lists.
func (a *App) owInstallPackage(s *Session) {
	page := &s.server.openwrt.packages
	page.name, page.term, page.found, page.searchErr = "", "", nil, ""
	pm := page.pm
	a.owForm(s, locales.OwInstall(), func(c *ui.Context) {
		ui.Row(c).FillWidth().Gap(6).AlignItems(ui.End).Children(func() {
			ui.Column(c).Grow(1).MinWidth(0).Children(func() {
				owField(c, locales.OwSearchPackages(), &page.term, "sftp")
			})
			if ui.Button(c, locales.OwSearch()).Disabled(page.searching).Clicked() {
				a.owSearchPackages(s)
			}
		})
		switch {
		case page.searching:
			ui.Spinner(c)
		case page.searchErr != "":
			ui.Text(c, page.searchErr).TextColor(theme.Warning)
		}
		if len(page.found) > 0 {
			ui.Scroll(c).FillWidth().MaxHeight(180).Children(func() {
				ui.Column(c).FillWidth().Children(func() {
					for _, p := range page.found {
						row := ui.Button(c, p.Name+"  "+p.Version).FillWidth()
						if row.Clicked() {
							page.name = p.Name
						}
					}
				})
			})
		}
		owField(c, locales.OwPackageName(), &page.name, monitor.OpenWrtSFTPPackage)
	}, func() (string, error) {
		return monitor.OpenWrtPackageCommand(pm, "install", strings.Fields(page.name)...)
	}, func(s *Session) { s.server.openwrt.packages.poll.pollNow() })
}

func (a *App) owSearchPackages(s *Session) {
	page := &s.server.openwrt.packages
	script, err := monitor.OpenWrtSearchCommand(page.pm, strings.TrimSpace(page.term))
	if err != nil {
		page.searchErr = locales.OwInvalid()
		return
	}
	page.searching, page.searchErr = true, ""
	read, pm := systemReader(s), page.pm
	go func() {
		ctx, cancel := context.WithTimeout(systemContext(s), 60*time.Second)
		defer cancel()
		out, stderr, err := read(ctx, script, nil, nil)
		found := monitor.ParseOpenWrtSearch(pm, out)
		a.post(func() {
			page.searching, page.found = false, found
			switch {
			case err != nil:
				page.searchErr = systemError(stderr, err)
			case len(found) == 0:
				page.searchErr = locales.OwNotFound()
			}
		})
	}()
}

// owLogsPage is the Logs section: logread's log, or the kernel's.
type owLogsPage struct {
	poll   sessionPoll
	source int
	lines  int // index of owLogLines
	text   string
	loaded bool
	err    string
	filter string
	shown  string
}

var owLogLines = []int{200, 500, 2000}

func (a *App) owLogsSection(c *ui.Context, s *Session) {
	page := &s.server.openwrt.logs
	a.owPoll(&page.poll, s, 10*time.Second, func() string {
		return monitor.OpenWrtLogCommand(page.source, owLogLines[min(page.lines, len(owLogLines)-1)])
	}, func(out string, err error) {
		page.loaded, page.err, page.text = true, "", systemRedact(out)
		if err != nil && out == "" {
			page.err = err.Error()
		}
	})
	owToolbar(c, func() {
		if systemSelect(c, &page.source, locales.OwLogSource(), []string{locales.OwSystemLog(), locales.OwKernelLog()}) {
			page.loaded = false
			page.poll.pollNow()
		}
		if systemSelect(c, &page.lines, locales.OwLogLines(), []string{"200", "500", "2000"}) {
			page.poll.pollNow()
		}
		ui.SearchField(c, &page.filter).Placeholder(locales.OwFilter()).Label(locales.OwFilter()).Width(220).MinWidth(90).Shrink(1)
	})
	owState(c, page.loaded, page.err, page.loaded && strings.TrimSpace(page.text) == "")
	text := page.text
	if query := strings.ToLower(strings.TrimSpace(page.filter)); query != "" {
		var kept []string
		for line := range strings.SplitSeq(text, "\n") {
			if strings.Contains(strings.ToLower(line), query) {
				kept = append(kept, line)
			}
		}
		text = strings.Join(kept, "\n")
	}
	page.shown = text
	ui.TextArea(c, &page.shown).ReadOnly(true).Font("monospace").FillWidth().Grow(1).MinHeight(0).Label(locales.ServerLogs())
}
