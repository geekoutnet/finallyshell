package app

import (
	"cmp"
	"context"
	"errors"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"time"

	"finallyshell/internal/monitor"
	"finallyshell/internal/widgets"
	"finallyshell/locales"
	"github.com/egoist/mygo/ui"
)

type serverSecurityPage struct {
	opsState
	view, summary, detail, jail, row int
	filter                           string
	peer                             netip.Addr
	logins                           []monitor.SSHLogin
	users, last, sudo, settings      string
	installed, stopped               bool
	jails                            []securityJail
	table                            ui.ListState
	sort                             ui.SortOrder
	location                         netip.Addr
}

type securityJail struct {
	name string
	ips  []netip.Addr
}

func (a *App) refreshServerSecurity(s *Session) {
	page := &s.server.security
	if page.changed(s) {
		*page = serverSecurityPage{opsState: page.reset(s), row: -1}
	}
	conn, ok := a.opsConnection(s, &page.opsState)
	if !ok || opsBlocked(&page.opsState) != "" {
		return
	}
	page.loaded, page.reading, page.err = true, true, nil
	login := a.passwords[s.Host.ID]
	go func() {
		ctx, cancel := context.WithTimeout(conn.ctx, 2*time.Minute)
		defer cancel()
		users, _, _ := conn.normal(ctx, monitor.SecurityUsersScript, nil)
		session, _, _ := conn.normal(ctx, monitor.FirewallSessionScript, nil)
		peer, _ := monitor.FirewallSessionEndpoint(session)
		current, last, _ := strings.Cut(users, "---last---\n")
		var rootErr error
		root := func(script string) (string, error) {
			if rootErr != nil {
				return "", rootErr
			}
			out, _, err := conn.root(ctx, script, nil)
			if errors.Is(err, monitor.ErrPrivilegeCancelled) || errors.Is(err, monitor.ErrPrivilegeDenied) || errors.Is(err, monitor.ErrPrivilegeWrongPassword) {
				rootErr = err
			}
			return out, err
		}
		auth, authErr := root(monitor.SecurityAuthScript)
		settings, _ := root(monitor.SecuritySettingsScript)
		sudo, _ := root(monitor.SecuritySudoScript)
		status, failErr := root(monitor.Fail2banStatusScript)
		var jails []securityJail
		if failErr == nil {
			for _, name := range monitor.ParseFail2banJails(status) {
				script, _ := monitor.Fail2banJailScript(name)
				out, err := root(script)
				if err != nil {
					failErr = errors.Join(failErr, err)
				}
				jails = append(jails, securityJail{name, monitor.ParseFail2banIPs(out)})
			}
		}
		logins := monitor.ParseSSHLog(auth)
		a.post(func() {
			if !conn.current() {
				return
			}
			page.reading, page.err = false, opsReadError(errors.Join(authErr, failErr), login)
			page.logins, page.users, page.last, page.sudo, page.settings = logins, opsTail(current), opsTail(last), opsTail(sudo), opsTail(settings)
			page.installed, page.stopped, page.jails, page.peer = strings.HasPrefix(status, "installed"), monitor.Fail2banStopped(status), jails, peer
			page.jail, page.row = min(page.jail, max(len(jails)-1, 0)), -1
		})
	}()
}

func (a *App) serverSecuritySection(c *ui.Context, s *Session) {
	page := &s.server.security
	if !page.loaded || page.changed(s) {
		a.refreshServerSecurity(s)
	}
	opsToolbar(c, func() {
		sections := []string{locales.SecFailures(), locales.SecAccepted(), locales.SecUsers(), locales.SecSettings(), "Fail2ban"}
		if a.opsNarrow() {
			ui.Button(c, sections[page.view]+" ▾").PopoverMenuButton(func(menu *ui.Menu) {
				for index, section := range sections {
					if menu.Item(section).Radio(index == page.view).Chosen() {
						page.view = index
					}
				}
			})
		} else {
			ui.Segmented(c, &page.view, sections...)
		}
		page.note(c)
	})
	page.readonlyNote(c, s)
	ui.Column(c.Key(page.view)).Grow(1).MinHeight(0).FillWidth().Children(func() {
		switch page.view {
		case 0, 1:
			a.securityLogins(c, s)
		case 2:
			opsToolbar(c, func() { ui.Segmented(c, &page.detail, locales.SecCurrent(), locales.SecLast(), locales.SecSudo()) })
			opsTextView(c, [3]string{page.users, page.last, page.sudo}[page.detail], locales.SecNoData())
		case 3:
			a.securitySettings(c, page.settings)
		case 4:
			a.securityBans(c, s)
		}
	})
	a.opsWorkDialog(c, s, &page.opsState)
	if page.location.IsValid() && page.dialog == nil {
		open := true
		ui.Modal(c.Key("sec-location"), &open, func() {
			ui.Column(c).Width(280).Gap(10).Children(func() {
				ui.Text(c, page.location.String()).Selectable().Bold()
				location := a.placeOf(page.location, true)
				if location == "" {
					location = locales.SecNoLocation()
				}
				ui.Text(c, location).Selectable()
				if ui.Button(c, locales.Close()).AlignSelf(ui.End).Clicked() {
					open = false
				}
			})
		})
		if !open {
			page.location = netip.Addr{}
		}
	}
}

type securityLoginRow struct {
	key, value, place, when, method string
	ip                              netip.Addr
	count                           int
}

func (a *App) securityLoginRows(page *serverSecurityPage) []securityLoginRow {
	filter := strings.ToLower(strings.TrimSpace(page.filter))
	counts := make(map[string]securityLoginRow)
	var rows []securityLoginRow
	for index, login := range page.logins {
		if login.Accepted != (page.view == 1) {
			continue
		}
		if page.view == 1 {
			rows = append(rows, securityLoginRow{key: strconv.Itoa(index), value: login.User, ip: login.IP, place: a.placeOf(login.IP, false), when: login.When, method: login.Method})
			continue
		}
		key := login.IP.String()
		if page.summary == 1 {
			key = login.User
		}
		if page.summary == 2 {
			key = login.Day
		}
		if key == "" {
			continue
		}
		row := counts[key]
		row.key, row.value, row.count = key, key, row.count+1
		if page.summary == 0 {
			row.ip = login.IP
		}
		counts[key] = row
	}
	for _, row := range counts {
		if row.ip.IsValid() {
			row.place = a.placeOf(row.ip, false)
		}
		rows = append(rows, row)
	}
	rows = slices.DeleteFunc(rows, func(row securityLoginRow) bool {
		return !strings.Contains(strings.ToLower(row.value+" "+row.ip.String()+" "+row.place+" "+row.when), filter)
	})
	slices.SortStableFunc(rows, func(left, right securityLoginRow) int {
		var order int
		switch page.sort.Column {
		case "value":
			order = strings.Compare(left.value, right.value)
		case "place":
			order = strings.Compare(left.place, right.place)
		case "ip":
			order = left.ip.Compare(right.ip)
		case "method":
			order = strings.Compare(left.method, right.method)
		case "when":
			order = strings.Compare(left.when, right.when)
		default:
			if page.view == 1 {
				order = strings.Compare(left.when, right.when)
			} else {
				order = cmp.Compare(left.count, right.count)
			}
		}
		if order == 0 {
			order = strings.Compare(left.value, right.value)
		}
		if page.sort.Column == "" || page.sort.Descending {
			return -order
		}
		return order
	})
	return rows
}

func (a *App) securityLogins(c *ui.Context, s *Session) {
	page := &s.server.security
	opsToolbar(c, func() {
		if page.view == 0 {
			ui.Segmented(c, &page.summary, locales.SecSource(), locales.SecUsername(), locales.SecDay())
		}
		ui.SearchField(c, &page.filter).Label(locales.SecFilter()).Placeholder(locales.SecFilter()).Width(140).MinWidth(85).Shrink(1)
	})
	rows := a.securityLoginRows(page)
	if len(rows) == 0 {
		opsTextView(c, "", locales.SecNoData())
		return
	}
	columns := []ui.TableColumn{{ID: "value", Title: locales.SecUsername(), MinWidth: 90, Sortable: true}}
	if page.view == 0 {
		columns[0].Title = [3]string{locales.SecSource(), locales.SecUsername(), locales.SecDay()}[page.summary]
		columns = append(columns, ui.TableColumn{ID: "count", Title: locales.SecCount(), Width: 72, Sortable: true})
		if page.summary == 0 {
			columns = append(columns, ui.TableColumn{ID: "place", Title: locales.SecLocation(), Width: 130, Sortable: true})
		}
	} else {
		columns = append(columns, ui.TableColumn{ID: "ip", Title: locales.SecSource(), Width: 105, Sortable: true}, ui.TableColumn{ID: "when", Title: locales.SecWhen(), Width: 138, Sortable: true}, ui.TableColumn{ID: "method", Title: locales.SecMethod(), Width: 75, Sortable: true})
	}
	page.table.Key = func(index int) any { return rows[index].key }
	page.table.Label = func(index int) string { return rows[index].value }
	page.table.Sort, page.table.Selected = &page.sort, nil
	page.table.CellMenu = func(menu *ui.Menu, index, _ int) { a.securityIPMenu(c, menu, page, rows[index].ip) }
	ui.Table(c.Key(page.summary), &page.table, columns, len(rows), func(index, column int) {
		row := rows[index]
		value := row.value
		switch columns[column].ID {
		case "count":
			value = strconv.Itoa(row.count)
		case "ip":
			value = row.ip.String()
		case "place":
			value = a.placeOf(row.ip, true)
		case "when":
			value = row.when
		case "method":
			value = row.method
		}
		ui.Text(c, orDash(value)).SingleLine().Tooltip(value)
	}).Grow(1).MinHeight(0).Label(locales.ServerSecurity())
}

func (a *App) securityIPMenu(c *ui.Context, menu *ui.Menu, page *serverSecurityPage, ip netip.Addr) {
	if !ip.IsValid() {
		return
	}
	if menu.Item(locales.SecCopyIP()).Chosen() {
		c.WriteClipboard(ip.String())
	}
	if menu.Item(locales.SecLocation()).Chosen() {
		page.location = ip
	}
}

func (a *App) securitySettings(c *ui.Context, text string) {
	ui.Scroll(c).FillWidth().Grow(1).MinHeight(0).Padding(10, 14).Children(func() {
		if strings.TrimSpace(text) == "" {
			widgets.Muted(c, locales.SecNoData())
			return
		}
		for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
			name, value, _ := strings.Cut(line, " ")
			var advice string
			switch name {
			case "permitrootlogin":
				name, advice = "PermitRootLogin", locales.SecRootAdvice()
			case "passwordauthentication":
				name, advice = "PasswordAuthentication", locales.SecPasswordAdvice()
			case "port":
				name, advice = "Port", locales.SecPortAdvice()
			default:
				continue
			}
			ui.Column(c.Key(line)).Padding(4, 0).Gap(2).Children(func() {
				ui.Text(c, name+": "+value).Bold().Selectable()
				widgets.Muted(c, advice).FontSize(12)
			})
		}
	})
}

func (a *App) securityBans(c *ui.Context, s *Session) {
	page := &s.server.security
	if len(page.jails) == 0 {
		message := locales.SecNoJails()
		if !page.installed {
			message = locales.SecNoFail2ban()
		} else if page.stopped {
			message = locales.SecFail2banStopped()
		}
		opsTextView(c, "", message)
		return
	}
	opsToolbar(c, func() {
		ui.Button(c, page.jails[page.jail].name).Label(locales.SecJail()).PopoverMenuButton(func(menu *ui.Menu) {
			for index, jail := range page.jails {
				if menu.Item(jail.name).Radio(index == page.jail).Chosen() {
					page.jail, page.row = index, -1
				}
			}
		})
		why := opsBlocked(&page.opsState)
		if why == "" && !page.peer.IsValid() {
			why = locales.FwSSHUnknown()
		}
		if opsButton(c, locales.SecBan(), why) {
			a.securityBanDialog(s, page.jails[page.jail].name, netip.Addr{}, true)
		}
	})
	jail := page.jails[page.jail]
	if len(jail.ips) == 0 {
		opsTextView(c, "", locales.SecNoBans())
		return
	}
	state := &page.table
	state.Key = func(index int) any { return jail.ips[index] }
	state.Label = func(index int) string { return jail.ips[index].String() }
	state.Sort, state.Selected = nil, &page.row
	state.CellMenu = func(menu *ui.Menu, index, _ int) {
		ip := jail.ips[index]
		a.securityIPMenu(c, menu, page, ip)
		if opsMenuItem(menu, locales.SecUnban(), opsBlocked(&page.opsState)) {
			a.securityBanDialog(s, jail.name, ip, false)
		}
	}
	opsToolbar(c, func() {
		why := opsBlocked(&page.opsState)
		if why == "" && (page.row < 0 || page.row >= len(jail.ips)) {
			why = locales.UpdNoTargets()
		}
		if opsButton(c, locales.SecUnban(), why) {
			a.securityBanDialog(s, jail.name, jail.ips[page.row], false)
		}
	})
	ui.Table(c.Key(jail.name), state, []ui.TableColumn{{Title: locales.SecSource()}, {Title: locales.SecLocation()}}, len(jail.ips), func(index, column int) {
		value := jail.ips[index].String()
		if column == 1 {
			value = a.placeOf(jail.ips[index], true)
		}
		ui.Text(c, orDash(value)).SingleLine().Tooltip(value)
	}).Grow(1).MinHeight(0).Label("Fail2ban")
}

func (a *App) securityBanDialog(s *Session, jail string, address netip.Addr, ban bool) {
	page := &s.server.security
	title, message, input := locales.SecUnban(), locales.SecUnbanWarning(), address.String()
	var confirmed netip.Addr
	if ban {
		title, message, input = locales.SecBan(), locales.SecBanWarning(), ""
	}
	dialog := &opsDialog{title: title, action: title, description: message + "\n\n" + jail, refresh: func() { a.refreshServerSecurity(s) }}
	dialog.build = func(c *ui.Context) (string, string) {
		if ban {
			ui.TextInput(c, &input).Label(locales.SecSource()).Placeholder(locales.SecSource()).FillWidth()
			ip, _ := netip.ParseAddr(strings.TrimSpace(input))
			if !page.peer.IsValid() || ip.Unmap() == page.peer {
				return "", locales.SecBanProtected()
			}
			confirmed = ip.Unmap()
		} else {
			ui.Text(c, input).Selectable()
		}
		script, err := monitor.Fail2banChangeScript(jail, strings.TrimSpace(input), ban)
		if err != nil {
			return "", locales.DuInvalid()
		}
		return script, ""
	}
	if ban {
		dialog.prepare = func(ctx context.Context, conn opsConnection, script string) (string, error) {
			out, _, err := conn.normal(ctx, monitor.FirewallSessionScript, nil)
			peer, _ := monitor.FirewallSessionEndpoint(out)
			if err != nil || !peer.IsValid() || confirmed == peer {
				return "", monitor.ErrOpsInvalid
			}
			return script, nil
		}
	}
	page.dialog = dialog
}
