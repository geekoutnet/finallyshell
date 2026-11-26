package app

import (
	"cmp"
	"slices"
	"strings"
	"time"

	"finallyshell/internal/monitor"
	"finallyshell/internal/widgets"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

// owIfacesPage is the Interfaces section: netifd's interfaces.
type owIfacesPage struct {
	poll     sessionPoll
	rows     []monitor.OpenWrtInterface
	loaded   bool
	err      string
	table    ui.ListState
	row      int
	selected ui.Selection[string]
	sort     ui.SortOrder
}

func (a *App) owInterfacesSection(c *ui.Context, s *Session) {
	page := &s.server.openwrt.ifaces
	a.owPoll(&page.poll, s, 10*time.Second, func() string { return monitor.OpenWrtInterfacesScript }, func(out string, err error) {
		rows, perr := monitor.ParseOpenWrtInterfaces(out)
		page.loaded, page.err = true, ""
		switch {
		case perr == nil:
			page.rows = rows
		case err != nil:
			page.err = err.Error()
		default:
			page.err = locales.OwNoUbus()
		}
	})
	rows := slices.Clone(page.rows)
	slices.SortStableFunc(rows, func(x, y monitor.OpenWrtInterface) int {
		order := 0
		switch page.sort.Column {
		case "state":
			order = cmp.Compare(owIfaceState(x), owIfaceState(y))
		case "proto":
			order = cmp.Compare(x.Proto, y.Proto)
		case "device":
			order = cmp.Compare(x.Device, y.Device)
		case "rx":
			order = cmp.Compare(x.RX, y.RX)
		case "tx":
			order = cmp.Compare(x.TX, y.TX)
		}
		if order == 0 {
			order = cmp.Compare(x.Name, y.Name)
		}
		if page.sort.Descending {
			return -order
		}
		return order
	})
	picked := func() []monitor.OpenWrtInterface {
		var out []monitor.OpenWrtInterface
		for _, r := range rows {
			if page.selected.Has(r.Name) {
				out = append(out, r)
			}
		}
		return out
	}
	menu := func(m *ui.Menu) {
		sel := picked()
		one := len(sel) == 1
		if m.Item(locales.OwIfaceRestart()).Disabled(!one).Chosen() && one {
			a.owIfaceAction(s, sel[0], true)
		}
		if m.Item(locales.OwIfaceStop()).Disabled(!one || !sel[0].Up).Danger().Chosen() && one {
			a.owIfaceAction(s, sel[0], false)
		}
	}
	owToolbar(c, func() {
		ui.MenuButton(c, locales.SvcActions(), menu)
	})
	owState(c, page.loaded, page.err, len(rows) == 0)
	page.table.Key = func(i int) any { return rows[i].Name }
	page.table.Label = func(i int) string { return rows[i].Name }
	page.table.Sort, page.table.Selection, page.table.Selected, page.table.KeepOffset = &page.sort, &page.selected, &page.row, true
	page.table.CellMenu = func(m *ui.Menu, i, _ int) {
		if !page.selected.Has(rows[i].Name) {
			page.selected.Clear()
			page.selected.Add(rows[i].Name)
		}
		menu(m)
	}
	columns := []ui.TableColumn{
		{ID: "name", Title: locales.OwIface(), Width: 110, Sortable: true},
		{ID: "state", Title: locales.SvcState(), Width: 120, Sortable: true},
		{ID: "proto", Title: locales.OwProto(), Width: 80, Sortable: true},
		{ID: "device", Title: locales.OwDevice(), Width: 100, Sortable: true},
		{ID: "ipv4", Title: "IPv4", Width: 150},
		{ID: "ipv6", Title: "IPv6", MinWidth: 140},
		{ID: "gateway", Title: locales.OwGateway(), Width: 120},
		{ID: "uptime", Title: locales.SysUptime(), Width: 90},
		{ID: "rx", Title: locales.OwRX(), Width: 85, Align: ui.End, Sortable: true},
		{ID: "tx", Title: locales.OwTX(), Width: 85, Align: ui.End, Sortable: true},
	}
	ui.Table(c, &page.table, columns, len(rows), func(i, col int) {
		r := rows[i]
		uptime := "-"
		if r.Up {
			uptime = uptimeText(time.Duration(r.Uptime) * time.Second)
		}
		text := []string{r.Name, owIfaceState(r), r.Proto, r.Device, strings.Join(r.IPv4, " "), strings.Join(r.IPv6, " "),
			r.Gateway, uptime, formatSize(int64(r.RX)), formatSize(int64(r.TX))}[col]
		cell := ui.Text(c, orDash(text)).SingleLine().Tooltip(text).MinWidth(0).Shrink(1)
		if col == 1 {
			switch {
			case r.Up:
				cell.TextColor(theme.Success)
			case r.Error != "":
				cell.TextColor(theme.Danger)
			default:
				cell.TextColor(theme.TextMuted)
			}
		}
	}).Grow(1).MinHeight(0).Label(locales.OwInterfaces())
}

// owIfaceState is an interface's state as the table shows it.
func owIfaceState(r monitor.OpenWrtInterface) string {
	switch {
	case r.Up:
		return locales.OwUp()
	case r.Pending:
		return locales.OwPending()
	case r.Error != "":
		return locales.OwDown() + " (" + r.Error + ")"
	}
	return locales.OwDown()
}

func (a *App) owIfaceAction(s *Session, r monitor.OpenWrtInterface, up bool) {
	script, err := monitor.OpenWrtInterfaceCommand(r.Name, up)
	if err != nil {
		return
	}
	title, note := locales.OwIfaceRestart(), locales.OwIfaceRestartNote()
	if !up {
		title, note = locales.OwIfaceStop(), locales.OwIfaceStopNote()
	}
	if owConnectedVia(s, r.IPv4) {
		note += "\n" + locales.OwCutsConnection()
	}
	a.owRun(s, title, r.Name, note, script, func(s *Session) { s.server.openwrt.ifaces.poll.pollNow() })
}

// owToolbar is the row over a section's table.
func owToolbar(c *ui.Context, children func()) {
	ui.Row(c).FillWidth().Padding(4, 8).Gap(6).AlignItems(ui.Center).Wrap().Children(children)
}

// owState is what a section shows over its table while it reads, when it
// failed, or when there is nothing.
func owState(c *ui.Context, loaded bool, err string, empty bool) {
	switch {
	case !loaded:
		ui.Text(c, locales.SvcReading()).Padding(6, 8).TextColor(theme.TextMuted)
	case err != "":
		ui.Text(c, err).SingleLine().Tooltip(err).Padding(4, 8).TextColor(theme.Warning)
	case empty:
		ui.Text(c, locales.OwNothing()).Padding(6, 8).TextColor(theme.TextMuted)
	}
}

// owDHCPPage is the DHCP section: dnsmasq's leases and the static ones.
type owDHCPPage struct {
	poll         sessionPoll
	leases       []monitor.OpenWrtLease
	static       []monitor.OpenWrtStaticLease
	now          int64
	loaded       bool
	err          string
	view         int // 0 the leases, 1 the static leases
	filter       string
	leaseTable   ui.ListState
	staticTable  ui.ListState
	leaseRow     int
	staticRow    int
	leaseSel     ui.Selection[string]
	staticSel    ui.Selection[string]
	form         [3]string // name, MAC, IP of the static lease to add
	formFromNote string
}

func (a *App) owDHCPSection(c *ui.Context, s *Session) {
	page := &s.server.openwrt.dhcp
	a.owPoll(&page.poll, s, 15*time.Second, func() string { return monitor.OpenWrtDHCPScript }, func(out string, err error) {
		page.loaded, page.err = true, ""
		if err != nil {
			page.err = err.Error()
			return
		}
		page.leases, page.static, page.now = monitor.ParseOpenWrtDHCP(out)
	})
	query := strings.ToLower(strings.TrimSpace(page.filter))
	match := func(parts ...string) bool {
		return strings.Contains(strings.ToLower(strings.Join(parts, " ")), query)
	}
	staticMAC := map[string]bool{}
	for _, st := range page.static {
		for _, mac := range st.MACs {
			staticMAC[mac] = true
		}
	}
	var leases []monitor.OpenWrtLease
	for _, l := range page.leases {
		if match(l.Hostname, l.IP, l.MAC) {
			leases = append(leases, l)
		}
	}
	var static []monitor.OpenWrtStaticLease
	for _, st := range page.static {
		if match(st.Name, st.IP, strings.Join(st.MACs, " ")) {
			static = append(static, st)
		}
	}
	owToolbar(c, func() {
		ui.Segmented(c, &page.view, locales.OwLeases(len(page.leases)), locales.OwStaticLeases(len(page.static))).Label(locales.OwDHCP())
		ui.SearchField(c, &page.filter).Placeholder(locales.OwFilter()).Label(locales.OwFilter()).Width(200).MinWidth(90).Shrink(1)
		ui.Spacer(c)
		if page.view == 0 {
			var chosen *monitor.OpenWrtLease
			for i := range leases {
				if page.leaseSel.Has(leases[i].MAC) {
					chosen = &leases[i]
				}
			}
			if ui.Button(c, locales.OwMakeStatic()).Disabled(chosen == nil || staticMAC[chosen.MAC]).Clicked() && chosen != nil {
				a.owAddStatic(s, chosen.Hostname, chosen.MAC, chosen.IP)
			}
		} else {
			if ui.Button(c, locales.OwAddStatic()).Clicked() {
				a.owAddStatic(s, "", "", "")
			}
			var chosen *monitor.OpenWrtStaticLease
			for i := range static {
				if page.staticSel.Has(static[i].Section) {
					chosen = &static[i]
				}
			}
			if ui.Button(c, locales.Delete()).Disabled(chosen == nil).Clicked() && chosen != nil {
				a.owDeleteStatic(s, *chosen)
			}
		}
	})
	if page.view == 0 {
		owState(c, page.loaded, page.err, len(leases) == 0)
		page.leaseTable.Key = func(i int) any { return leases[i].MAC }
		page.leaseTable.Label = func(i int) string { return leases[i].IP }
		page.leaseTable.Selection, page.leaseTable.Selected = &page.leaseSel, &page.leaseRow
		columns := []ui.TableColumn{
			{ID: "host", Title: locales.SysHostname(), MinWidth: 140},
			{ID: "ip", Title: "IPv4", Width: 140},
			{ID: "mac", Title: "MAC", Width: 150},
			{ID: "expires", Title: locales.OwExpires(), Width: 120},
			{ID: "static", Title: locales.OwStatic(), Width: 70},
		}
		ui.Table(c.Key("leases"), &page.leaseTable, columns, len(leases), func(i, col int) {
			l := leases[i]
			expires := locales.OwNever()
			if l.Expires > 0 {
				expires = uptimeText(time.Duration(max(l.Expires-page.now, 0)) * time.Second)
			}
			mark := ""
			if staticMAC[l.MAC] {
				mark = "✓"
			}
			text := []string{orDash(l.Hostname), l.IP, l.MAC, expires, mark}[col]
			ui.Text(c, text).SingleLine().Tooltip(text).MinWidth(0).Shrink(1)
		}).Grow(1).MinHeight(0).Label(locales.OwLeases(len(leases)))
		return
	}
	owState(c, page.loaded, page.err, len(static) == 0)
	page.staticTable.Key = func(i int) any { return static[i].Section }
	page.staticTable.Label = func(i int) string { return static[i].IP }
	page.staticTable.Selection, page.staticTable.Selected = &page.staticSel, &page.staticRow
	columns := []ui.TableColumn{
		{ID: "name", Title: locales.SysHostname(), MinWidth: 140},
		{ID: "mac", Title: "MAC", Width: 260},
		{ID: "ip", Title: "IPv4", Width: 140},
	}
	ui.Table(c.Key("static"), &page.staticTable, columns, len(static), func(i, col int) {
		st := static[i]
		text := []string{orDash(st.Name), strings.Join(st.MACs, " "), orDash(st.IP)}[col]
		ui.Text(c, text).SingleLine().Tooltip(text).MinWidth(0).Shrink(1)
	}).Grow(1).MinHeight(0).Label(locales.OwStaticLeases(len(static)))
}

func (a *App) owAddStatic(s *Session, name, mac, ip string) {
	page := &s.server.openwrt.dhcp
	page.form = [3]string{name, mac, ip}
	a.owForm(s, locales.OwAddStatic(), func(c *ui.Context) {
		widgets.Muted(c, locales.OwAddStaticNote()).FillWidth()
		owField(c, locales.SysHostname(), &page.form[0], "nas")
		owField(c, "MAC", &page.form[1], "aa:bb:cc:dd:ee:ff")
		owField(c, "IPv4", &page.form[2], "192.168.1.10")
	}, func() (string, error) {
		f := page.form
		return monitor.OpenWrtAddStaticLeaseCommand(strings.TrimSpace(f[0]), strings.TrimSpace(f[1]), strings.TrimSpace(f[2]))
	}, func(s *Session) { s.server.openwrt.dhcp.poll.pollNow() })
}

func (a *App) owDeleteStatic(s *Session, st monitor.OpenWrtStaticLease) {
	script, err := monitor.OpenWrtDeleteSectionCommand("dhcp", st.Section, "host", "dnsmasq")
	if err != nil {
		return
	}
	target := strings.TrimSpace(st.Name + "  " + strings.Join(st.MACs, " ") + "  " + st.IP)
	a.owRun(s, locales.OwDeleteStatic(), target, locales.OwDeleteStaticNote(), script, func(s *Session) { s.server.openwrt.dhcp.poll.pollNow() })
}

// owForwardsPage is the Port forwards section: the firewall's redirects.
type owForwardsPage struct {
	poll     sessionPoll
	rows     []monitor.OpenWrtForward
	zones    []string
	loaded   bool
	err      string
	table    ui.ListState
	row      int
	selected ui.Selection[string]
	form     monitor.OpenWrtForward
	proto    int
	src, dst int
}

func (a *App) owForwardsSection(c *ui.Context, s *Session) {
	page := &s.server.openwrt.forwards
	a.owPoll(&page.poll, s, 30*time.Second, func() string { return monitor.OpenWrtFirewallScript }, func(out string, err error) {
		page.loaded, page.err = true, ""
		if err != nil {
			page.err = err.Error()
			return
		}
		page.rows, page.zones = monitor.ParseOpenWrtFirewall(out)
	})
	rows := page.rows
	var chosen *monitor.OpenWrtForward
	for i := range rows {
		if page.selected.Has(rows[i].Section) {
			chosen = &rows[i]
		}
	}
	menu := func(m *ui.Menu) {
		if chosen == nil {
			m.Item(locales.SvcSelectOne()).Disabled(true)
			return
		}
		f := *chosen
		label := locales.OwDisable()
		if !f.Enabled {
			label = locales.OwEnable()
		}
		if m.Item(label).Chosen() {
			if script, err := monitor.OpenWrtEnableForwardCommand(f.Section, !f.Enabled); err == nil {
				a.owRun(s, label, f.Name, locales.OwForwardNote(), script, func(s *Session) { s.server.openwrt.forwards.poll.pollNow() })
			}
		}
		if m.Item(locales.Delete()).Danger().Chosen() {
			if script, err := monitor.OpenWrtDeleteSectionCommand("firewall", f.Section, "redirect", "firewall"); err == nil {
				a.owRun(s, locales.OwDeleteForward(), f.Name, locales.OwForwardNote(), script, func(s *Session) { s.server.openwrt.forwards.poll.pollNow() })
			}
		}
	}
	owToolbar(c, func() {
		if ui.Button(c, locales.OwAddForward()).Clicked() {
			a.owAddForward(s)
		}
		ui.MenuButton(c, locales.SvcActions(), menu)
	})
	owState(c, page.loaded, page.err, len(rows) == 0)
	page.table.Key = func(i int) any { return rows[i].Section }
	page.table.Label = func(i int) string { return rows[i].Name }
	page.table.Selection, page.table.Selected = &page.selected, &page.row
	page.table.CellMenu = func(m *ui.Menu, i, _ int) {
		page.selected.Clear()
		page.selected.Add(rows[i].Section)
		chosen = &rows[i]
		menu(m)
	}
	columns := []ui.TableColumn{
		{ID: "name", Title: locales.OwName(), MinWidth: 140},
		{ID: "proto", Title: locales.OwProto(), Width: 90},
		{ID: "ext", Title: locales.OwExternalPort(), Width: 110},
		{ID: "ip", Title: locales.OwInternalIP(), Width: 130},
		{ID: "int", Title: locales.OwInternalPort(), Width: 110},
		{ID: "zones", Title: locales.OwZones(), Width: 110},
		{ID: "on", Title: locales.SvcState(), Width: 90},
	}
	ui.Table(c, &page.table, columns, len(rows), func(i, col int) {
		f := rows[i]
		state := locales.OwEnabled()
		if !f.Enabled {
			state = locales.OwDisabled()
		}
		text := []string{orDash(f.Name), f.Proto, orDash(f.ExternalPort), orDash(f.InternalIP), cmp.Or(f.InternalPort, f.ExternalPort, "-"),
			orDash(f.SrcZone) + " → " + orDash(f.DstZone), state}[col]
		cell := ui.Text(c, text).SingleLine().Tooltip(text).MinWidth(0).Shrink(1)
		if col == 6 && !f.Enabled {
			cell.TextColor(theme.TextMuted)
		}
	}).Grow(1).MinHeight(0).Label(locales.OwForwards())
}

func (a *App) owAddForward(s *Session) {
	page := &s.server.openwrt.forwards
	zones := page.zones
	if len(zones) == 0 {
		zones = []string{"wan", "lan"}
	}
	page.form = monitor.OpenWrtForward{}
	page.proto = 0
	page.src, page.dst = max(slices.Index(zones, "wan"), 0), max(slices.Index(zones, "lan"), 0)
	protos := []string{"TCP+UDP", "TCP", "UDP"}
	a.owForm(s, locales.OwAddForward(), func(c *ui.Context) {
		owField(c, locales.OwName(), &page.form.Name, "NAS web")
		ui.Row(c).FillWidth().Gap(8).Children(func() {
			systemSelect(c, &page.proto, locales.OwProto(), protos)
			systemSelect(c, &page.src, locales.OwFromZone(), zones)
			systemSelect(c, &page.dst, locales.OwToZone(), zones)
		})
		owField(c, locales.OwExternalPort(), &page.form.ExternalPort, "8443")
		owField(c, locales.OwInternalIP(), &page.form.InternalIP, "192.168.1.10")
		owField(c, locales.OwInternalPort(), &page.form.InternalPort, locales.OwSamePort())
	}, func() (string, error) {
		f := page.form
		f.Name, f.ExternalPort, f.InternalIP, f.InternalPort = strings.TrimSpace(f.Name), strings.TrimSpace(f.ExternalPort), strings.TrimSpace(f.InternalIP), strings.TrimSpace(f.InternalPort)
		f.Proto = monitor.OpenWrtForwardProtos[page.proto]
		f.SrcZone, f.DstZone = zones[page.src], zones[page.dst]
		return monitor.OpenWrtAddForwardCommand(f)
	}, func(s *Session) { s.server.openwrt.forwards.poll.pollNow() })
}
