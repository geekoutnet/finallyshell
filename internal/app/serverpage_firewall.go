package app

import (
	"context"
	"errors"
	"finallyshell/internal/monitor"
	"finallyshell/internal/widgets"
	"finallyshell/locales"
	"github.com/egoist/mygo/ui"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"time"
)

type serverFirewallPage struct {
	opsState
	backend, listeners, raw, zone string
	ufw                           monitor.UFWState
	zones                         []monitor.FirewallZone
	protected                     firewallProtection
	view, row                     int
	table                         ui.ListState
}

type firewallProtection struct {
	peer   netip.Addr
	actual int
	ports  []int
}

func readFirewallProtection(ctx context.Context, conn opsConnection, configured int) (firewallProtection, error) {
	out, _, err := conn.normal(ctx, monitor.FirewallSessionScript, nil)
	peer, port := monitor.FirewallSessionEndpoint(out)
	if err != nil || !peer.IsValid() || port == 0 {
		return firewallProtection{}, monitor.ErrOpsInvalid
	}
	connections, _, err := conn.root(ctx, monitor.FirewallConnectionsScript, nil)
	if err != nil {
		return firewallProtection{}, err
	}
	ports := monitor.FirewallSSHPorts(configured, connections)
	if !slices.Contains(ports, port) {
		ports = append(ports, port)
	}
	return firewallProtection{peer, port, ports}, nil
}

func (a *App) refreshServerFirewall(s *Session) {
	page := &s.server.firewall
	if page.changed(s) {
		*page = serverFirewallPage{opsState: page.reset(s), row: -1}
	}
	conn, ok := a.opsConnection(s, &page.opsState)
	if !ok || opsBlocked(&page.opsState) != "" {
		return
	}
	configured := s.Host.PortOr22()
	login := a.passwords[s.Host.ID]
	page.loaded, page.reading, page.err = true, true, nil
	go func() {
		ctx, cancel := context.WithTimeout(conn.ctx, 2*time.Minute)
		defer cancel()
		detected, _, err := conn.normal(ctx, monitor.FirewallDetectScript, nil)
		backend, _, _ := strings.Cut(strings.TrimSpace(detected), "\n")
		var raw, stdout, stderr, listeners string
		var protected firewallProtection
		if script := map[string]string{"ufw": monitor.UFWReadScript, "firewall-cmd": monitor.FirewallZonesScript, "nft": monitor.FirewallNFTScript, "iptables": monitor.FirewallIPTablesScript}[backend]; script != "" && err == nil {
			stdout, stderr, err = conn.root(ctx, script, nil)
			if backend == "nft" || backend == "iptables" {
				raw = stdout
			}
		}
		if !errors.Is(err, monitor.ErrPrivilegeCancelled) && !errors.Is(err, monitor.ErrPrivilegeDenied) && !errors.Is(err, monitor.ErrPrivilegeWrongPassword) {
			listeners, _, _ = conn.root(ctx, monitor.FirewallListeningScript, nil)
			if backend == "ufw" || backend == "firewall-cmd" {
				protected, _ = readFirewallProtection(ctx, conn, configured)
			}
		}
		ufw, zones := monitor.UFWState{}, []monitor.FirewallZone(nil)
		if backend == "ufw" {
			ufw = monitor.ParseUFW(stdout)
		}
		if backend == "firewall-cmd" {
			zones = monitor.ParseFirewallZones(stdout)
		}
		a.post(func() {
			if !conn.current() {
				return
			}
			page.reading, page.err, page.output = false, opsReadError(err, login), opsSafeOutput(stderr, login)
			page.backend, page.ufw, page.zones, page.protected = backend, ufw, zones, protected
			page.raw, page.listeners, page.row = opsSafeOutput(raw, login), opsSafeOutput(listeners, login), -1
			if !slices.ContainsFunc(zones, func(zone monitor.FirewallZone) bool { return zone.Name == page.zone }) && len(zones) > 0 {
				page.zone = zones[0].Name
				if active := slices.IndexFunc(zones, func(zone monitor.FirewallZone) bool { return zone.Active }); active >= 0 {
					page.zone = zones[active].Name
				}
			}
		})
	}()
}

func (page *serverFirewallPage) blocked() string {
	if why := opsBlocked(&page.opsState); why != "" {
		return why
	}
	if page.err != nil {
		return opsError(page.err)
	}
	if len(page.protected.ports) == 0 {
		return locales.FwSSHUnknown()
	}
	return ""
}

func (a *App) serverFirewallSection(c *ui.Context, s *Session) {
	page := &s.server.firewall
	if !page.loaded || page.changed(s) {
		a.refreshServerFirewall(s)
	}
	opsToolbar(c, func() {
		ui.Segmented(c, &page.view, locales.FwRules(), locales.FwListening())
		if page.backend != "" {
			widgets.Muted(c, page.backend).FontSize(12)
		}
		if port := page.protected.actual; port != 0 {
			widgets.Muted(c, locales.FwSSHPort(strconv.Itoa(port))).FontSize(12)
		}
		page.note(c)
	})
	page.readonlyNote(c, s)
	if page.view == 1 {
		opsTextView(c, page.listeners, locales.FwNoListening())
	} else {
		switch page.backend {
		case "ufw":
			a.ufwSection(c, s)
		case "firewall-cmd":
			a.firewalldSection(c, s)
		case "nft", "iptables":
			opsToolbar(c, func() { widgets.Muted(c, locales.FwReadOnly()).FontSize(12) })
			opsTextView(c, page.raw, locales.FwNoRules())
		default:
			opsTextView(c, "", locales.FwNotInstalled())
		}
	}
	a.opsWorkDialog(c, s, &page.opsState)
}

func (a *App) ufwSection(c *ui.Context, s *Session) {
	page := &s.server.firewall
	opsToolbar(c, func() {
		state := locales.FwUnknown()
		if page.ufw.Known {
			state = locales.FwInactive()
			if page.ufw.Active {
				state = locales.FwActive()
			}
		}
		ui.Text(c, state).FontSize(12).Bold()
		if page.ufw.Default != "" && !a.opsNarrow() {
			widgets.Muted(c, page.ufw.Default).FontSize(12).SingleLine().MaxWidth(375).Tooltip(page.ufw.Default)
		}
		why := page.blocked()
		if why == "" && !page.ufw.Known {
			why = locales.FwUnknown()
		}
		action := locales.FwEnable()
		if page.ufw.Active {
			action = locales.FwDisable()
		}
		if opsButton(c, action, why) {
			a.ufwToggleDialog(s, !page.ufw.Active)
		}
		if opsButton(c, locales.FwAdd(), why) {
			a.ufwAddDialog(s)
		}
		remove := why
		if remove == "" && (page.row < 0 || page.row >= len(page.ufw.Rules)) {
			remove = locales.UpdNoTargets()
		}
		if remove == "" {
			remove = page.ufwDeleteReason(page.ufw.Rules[page.row])
		}
		if opsButton(c, locales.Delete(), remove) {
			a.ufwDeleteDialog(s, page.ufw.Rules[page.row])
		}
	})
	if page.ufw.Default != "" && a.opsNarrow() {
		opsToolbar(c, func() {
			widgets.Muted(c, page.ufw.Default).FontSize(12).SingleLine().Shrink(1).MinWidth(0).Tooltip(page.ufw.Default)
		})
	}
	rows := page.ufw.Rules
	if len(rows) == 0 {
		opsTextView(c, page.output, locales.FwNoRules())
		return
	}
	state := &page.table
	state.Key = func(index int) any { return rows[index].Number }
	state.Label = func(index int) string { return rows[index].To }
	state.Selected = &page.row
	state.CellMenu = func(menu *ui.Menu, index, _ int) {
		rule := rows[index]
		if opsMenuItem(menu, locales.Delete(), page.ufwDeleteReason(rule)) {
			a.ufwDeleteDialog(s, rule)
		}
	}
	columns := []ui.TableColumn{{Title: "#", Width: 38}, {Title: locales.FwTo(), MinWidth: 68}, {Title: locales.FwAction(), Width: 90}, {Title: locales.FwSource(), MinWidth: 60}, {Title: locales.FwComment(), MinWidth: 54}}
	ui.Table(c.Key("ufw"), state, columns, len(rows), func(index, column int) {
		rule := rows[index]
		value := [5]string{strconv.Itoa(rule.Number), rule.To, rule.Action, rule.From, rule.Comment}[column]
		ui.Text(c, orDash(value)).SingleLine().Tooltip(value)
	}).Grow(1).MinHeight(0).Label(locales.FwRules())
}

func (page *serverFirewallPage) ufwDeleteReason(rule monitor.FirewallRule) string {
	if why := page.blocked(); why != "" {
		return why
	}
	if _, err := monitor.UFWDeleteScript(rule, page.protected.ports); err != nil {
		return locales.FwProtected()
	}
	return ""
}

func (a *App) firewallDialog(s *Session, title, message string, fields func(*ui.Context) func(firewallProtection) (string, error), build func(firewallProtection) (string, error)) {
	page := &s.server.firewall
	configured := s.Host.PortOr22()
	var recheck func(firewallProtection) (string, error)
	page.dialog = &opsDialog{title: title, action: title, description: message, refresh: func() { a.refreshServerFirewall(s) }, build: func(c *ui.Context) (string, string) {
		builder := build
		if fields != nil {
			builder = fields(c)
		}
		if why := page.blocked(); why != "" {
			return "", why
		}
		script, err := builder(page.protected)
		if err != nil {
			return "", locales.FwInvalid()
		}
		recheck = builder
		return script, ""
	}, prepare: func(ctx context.Context, conn opsConnection, _ string) (string, error) {
		protection, err := readFirewallProtection(ctx, conn, configured)
		if err != nil {
			return "", err
		}
		return recheck(protection)
	}}
}

func (a *App) ufwToggleDialog(s *Session, enable bool) {
	title, message := locales.FwDisable(), locales.FwDisableWarning()
	protection := s.server.firewall.protected
	if enable {
		title, message = locales.FwEnable(), locales.FwEnableWarning()+"\n"+protection.peer.String()+" → TCP "+strconv.Itoa(protection.actual)
	}
	a.firewallDialog(s, title, message, nil, func(current firewallProtection) (string, error) {
		if protection.peer != current.peer || protection.actual != current.actual {
			return "", monitor.ErrOpsInvalid
		}
		return monitor.UFWToggleScript(enable, current.actual, current.peer)
	})
}

func (a *App) ufwDeleteDialog(s *Session, rule monitor.FirewallRule) {
	message := locales.FwDeleteWarning() + "\n\n#" + strconv.Itoa(rule.Number) + " · " + rule.Action + " · " + rule.To + " · " + rule.From
	if rule.Comment != "" {
		message += " · " + rule.Comment
	}
	a.firewallDialog(s, locales.Delete(), message, nil, func(current firewallProtection) (string, error) { return monitor.UFWDeleteScript(rule, current.ports) })
}

func (a *App) ufwAddDialog(s *Session) {
	action, protocol := 0, 0
	var port, source, comment string
	a.firewallDialog(s, locales.FwAdd(), locales.FwAddWarning(), func(c *ui.Context) func(firewallProtection) (string, error) {
		ui.Row(c).FillWidth().Wrap().Gap(8).Children(func() {
			ui.Segmented(c.Key("policy"), &action, locales.FwAllow(), locales.FwDeny())
			ui.Segmented(c.Key("protocol"), &protocol, "TCP", "UDP")
		})
		firewallField(c, locales.FwPort(), &port)
		firewallField(c, locales.FwSourceOptional(), &source)
		firewallField(c, locales.FwComment(), &comment)
		chosenAction, chosenProtocol, chosenPort, chosenSource, chosenComment := action, protocol, strings.TrimSpace(port), strings.TrimSpace(source), strings.TrimSpace(comment)
		return func(current firewallProtection) (string, error) {
			return monitor.UFWAddScript([2]string{"allow", "deny"}[chosenAction], chosenPort, [2]string{"tcp", "udp"}[chosenProtocol], chosenSource, chosenComment, current.ports)
		}
	}, nil)
}

func firewallField(c *ui.Context, label string, value *string) {
	ui.TextInput(c.Key(label), value).Label(label).Placeholder(label).FillWidth()
}

type firewallZoneRow struct{ kind, value string }

func (a *App) firewalldSection(c *ui.Context, s *Session) {
	page := &s.server.firewall
	index := slices.IndexFunc(page.zones, func(zone monitor.FirewallZone) bool { return zone.Name == page.zone })
	if index < 0 {
		opsTextView(c, page.output, locales.FwNoRules())
		return
	}
	zone := page.zones[index]
	var rows []firewallZoneRow
	for _, value := range zone.Ports {
		rows = append(rows, firewallZoneRow{"port", value})
	}
	for _, value := range zone.Services {
		rows = append(rows, firewallZoneRow{"service", value})
	}
	for _, value := range zone.Rich {
		rows = append(rows, firewallZoneRow{"rich", value})
	}
	opsToolbar(c, func() {
		ui.Button(c, zone.Name).Label(locales.FwZone()).PopoverMenuButton(func(menu *ui.Menu) {
			for _, item := range page.zones {
				entry := menu.Item(item.Name).Radio(item.Name == page.zone)
				if item.Active {
					entry.Subtitle(locales.FwActive())
				}
				if entry.Chosen() {
					page.zone, page.row = item.Name, -1
				}
			}
		})
		if zone.Active {
			widgets.Muted(c, locales.FwActive()).FontSize(12)
		}
		if value := strings.TrimSpace(zone.Interfaces + " " + zone.Sources); value != "" {
			widgets.Muted(c, value).FontSize(12).SingleLine().MaxWidth(120).Tooltip(value)
		}
		if opsButton(c, locales.FwAdd(), page.blocked()) {
			a.firewalldDialog(s, zone.Name, firewallZoneRow{})
		}
		why := page.blocked()
		if why == "" && (page.row < 0 || page.row >= len(rows)) {
			why = locales.UpdNoTargets()
		}
		if why == "" {
			why = page.zoneRemovalReason(zone.Name, rows[page.row])
		}
		if opsButton(c, locales.Delete(), why) {
			a.firewalldDialog(s, zone.Name, rows[page.row])
		}
	})
	if len(rows) == 0 {
		opsTextView(c, "", locales.FwNoRules())
		return
	}
	state := &page.table
	state.Key = func(index int) any { return rows[index] }
	state.Label = func(index int) string { return rows[index].value }
	state.Selected = &page.row
	state.CellMenu = func(menu *ui.Menu, index, _ int) {
		if opsMenuItem(menu, locales.Delete(), page.zoneRemovalReason(zone.Name, rows[index])) {
			a.firewalldDialog(s, zone.Name, rows[index])
		}
	}
	ui.Table(c.Key(zone.Name), state, []ui.TableColumn{{Title: locales.FwKind(), Width: 110}, {Title: locales.FwRules()}}, len(rows), func(index, column int) {
		row := rows[index]
		value := row.value
		if column == 0 {
			value = map[string]string{"port": locales.FwPort(), "service": locales.FwService(), "rich": locales.FwRich()}[row.kind]
		}
		ui.Text(c, value).SingleLine().Tooltip(value)
	}).Grow(1).MinHeight(0).Label(locales.FwRules())
}

func (page *serverFirewallPage) zoneRemovalReason(zone string, row firewallZoneRow) string {
	if why := page.blocked(); why != "" {
		return why
	}
	if row.kind == "rich" {
		return locales.FwReadOnly()
	}
	if _, err := monitor.FirewallZoneChangeScript(zone, row.kind, row.value, false, page.protected.ports); err != nil {
		return locales.FwProtected()
	}
	return ""
}

func (a *App) firewalldDialog(s *Session, zone string, row firewallZoneRow) {
	if row.kind != "" {
		a.firewallDialog(s, locales.Delete(), locales.FwZoneDeleteWarning()+"\n\n"+zone+" · "+row.value, nil, func(current firewallProtection) (string, error) {
			return monitor.FirewallZoneChangeScript(zone, row.kind, row.value, false, current.ports)
		})
		return
	}
	var kind int
	var input string
	a.firewallDialog(s, locales.FwAdd(), locales.FwZoneAddWarning()+"\n\n"+zone, func(c *ui.Context) func(firewallProtection) (string, error) {
		ui.Segmented(c, &kind, locales.FwPort(), locales.FwService())
		label := "443/tcp"
		if kind == 1 {
			label = "https"
		}
		firewallField(c, label, &input)
		chosenKind, chosenValue := [2]string{"port", "service"}[kind], strings.TrimSpace(input)
		return func(current firewallProtection) (string, error) {
			return monitor.FirewallZoneChangeScript(zone, chosenKind, chosenValue, true, current.ports)
		}
	}, nil)
}
