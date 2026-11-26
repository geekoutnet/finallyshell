package app

import (
	"cmp"
	"context"
	"errors"
	"net"
	"slices"
	"strconv"
	"strings"
	"time"

	"finallyshell/internal/config"
	"finallyshell/internal/geo"
	"finallyshell/internal/icons"
	"finallyshell/internal/proxycheck"
	"finallyshell/internal/sshx"
	"finallyshell/internal/widgets"
	"finallyshell/locales"

	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/ui"
)

// The proxy manager: the proxy library in a window of its own, to add,
// edit, copy, delete and test proxies, which hosts then connect through
// (editor_proxy.go). Testing a proxy tells whether it lets a connection
// through, how fast, and the address and the place it goes out from
// (internal/proxycheck).

const windowProxies WindowKind = "proxies"

// proxyTestTimeout is how long testing a proxy may take.
const proxyTestTimeout = 25 * time.Second

// proxyTest is a proxy's test: under way, or how it went.
type proxyTest struct {
	running bool
	cancel  context.CancelFunc
	res     proxycheck.Result
	err     error
	at      time.Time
}

// proxyMgrState is the proxy manager's.
type proxyMgrState struct {
	query    string
	list     ui.ListState
	selected int // row
	edit     *proxyEdit
	deleting *config.Proxy
	err      string
}

// proxyEdit is the form of a proxy being added or edited.
type proxyEdit struct {
	// id is the proxy's, "" for a new one; original is it as saved.
	id                                     string
	original                               *config.Proxy
	kind                                   config.ProxyKind
	name, addr, port, user, password, note string
	// passwordSaved says the proxy has a password saved, which an empty
	// field keeps.
	passwordSaved bool
	errs          map[string]string
	// then is called with the proxy saved, as the connection editor
	// chooses the proxy it added.
	then func(*config.Proxy)
}

// proxyEditTestKey is the key of the test of the form's proxy.
const proxyEditTestKey = "\x00edit"

// openProxyManager opens the proxy manager, or brings it to the front.
func (a *App) openProxyManager() {
	if a.Locked() {
		return
	}
	a.openWindow(WindowSpec{Kind: windowProxies, Title: locales.ProxyMgrTitle,
		Width: 980, Height: 600, MinWidth: 640, MinHeight: 380,
		Closed: func() {
			a.proxyMgr.edit, a.proxyMgr.deleting = nil, nil
			a.stopProxyTest(proxyEditTestKey)
		}}, a.proxyManagerView)
	a.redraw()
}

// proxyKindChoices are the kinds of proxy servers, in the order offered,
// with their labels.
func proxyKindChoices() ([]config.ProxyKind, []string) {
	return []config.ProxyKind{config.ProxySOCKS5, config.ProxyHTTP, config.ProxyHTTPS, config.ProxySOCKS4},
		[]string{"SOCKS5", "HTTP", "HTTPS", "SOCKS4 / 4a"}
}

func proxyKindLabel(k config.ProxyKind) string {
	kinds, labels := proxyKindChoices()
	if i := slices.Index(kinds, k); i >= 0 {
		return labels[i]
	}
	return string(k)
}

// proxyAddress is p's host:port.
func proxyAddress(p *config.Proxy) string {
	return p.Addr + ":" + strconv.Itoa(p.PortOrDefault())
}

// shownProxies are the library's proxies the search finds, by name.
func (a *App) shownProxies() []*config.Proxy {
	q := strings.ToLower(strings.TrimSpace(a.proxyMgr.query))
	var out []*config.Proxy
	for _, p := range a.hosts.Proxies {
		if q == "" || strings.Contains(strings.ToLower(p.Name+" "+p.Addr+" "+p.User+" "+p.Note), q) {
			out = append(out, p)
		}
	}
	slices.SortStableFunc(out, func(x, y *config.Proxy) int { return cmp.Compare(strings.ToLower(x.Name), strings.ToLower(y.Name)) })
	return out
}

func (a *App) proxyManagerView(c *ui.Context) {
	m := &a.proxyMgr
	proxies := a.shownProxies()
	ui.Column(c).Fill().Children(func() {
		ui.Row(c).Gap(8).Padding(10, 12).AlignItems(ui.Center).FillWidth().Children(func() {
			if ui.PrimaryButton(c, locales.ProxyMgrNew()).Clicked() {
				a.editProxy(nil, nil)
			}
			if ui.Button(c, locales.ProxyMgrTestAll()).Disabled(len(proxies) == 0).Clicked() {
				for _, p := range proxies {
					a.testLibraryProxy(p)
				}
			}
			ui.Spacer(c)
			ui.SearchField(c, &m.query).Placeholder(locales.ProxyMgrSearch()).Label(locales.ProxyMgrSearch()).Width(260).Shrink(1).MinWidth(140)
		})
		if m.err != "" {
			aiStatus(c, m.err, true)
		}
		ui.Column(c).Grow(1).MinHeight(0).Padding(0, 12, 8, 12).Children(func() {
			if len(a.hosts.Proxies) == 0 {
				a.proxyEmpty(c)
				return
			}
			a.proxyTable(c, proxies)
		})
		ui.Row(c).Gap(12).Padding(8, 12).AlignItems(ui.Center).BorderWidth(1, 0, 0, 0).BorderColor(theme.Border).Children(func() {
			widgets.Muted(c, locales.ProxyMgrCount(len(a.hosts.Proxies))).FontSize(12).Grow(1).MinWidth(0)
			widgets.Muted(c, locales.ProxyMgrTestNote()).FontSize(11).SingleLine().MinWidth(0).Shrink(1)
		})
	})
	a.proxyEditView(c)
	a.proxyDeleteView(c)
}

// proxyEmpty is the manager with no proxy yet.
func (a *App) proxyEmpty(c *ui.Context) {
	ui.Column(c).Grow(1).AlignItems(ui.Center).Justify(ui.Center).Gap(10).Children(func() {
		ui.Box(c).Size(48, 48).Radius(12).Center().Background(theme.Accent.Alpha(0.12)).Children(func() {
			ui.Icon(c, icons.Get("waypoints")).FontSize(24).TextColor(theme.Accent)
		})
		ui.Text(c, locales.ProxyMgrEmptyTitle()).FontSize(16).Bold()
		widgets.Muted(c, locales.ProxyMgrEmptyBody()).MaxWidth(420).TextAlign(ui.Center)
		if ui.PrimaryButton(c, locales.ProxyMgrNew()).Clicked() {
			a.editProxy(nil, nil)
		}
	})
}

func (a *App) proxyTable(c *ui.Context, proxies []*config.Proxy) {
	m := &a.proxyMgr
	columns := []ui.TableColumn{
		{ID: "name", Title: locales.ProxyMgrColName(), Width: 170, MinWidth: 90},
		{ID: "kind", Title: locales.ProxyMgrColKind(), Width: 92, MinWidth: 70},
		{ID: "addr", Title: locales.ProxyMgrColAddress(), Width: 190, MinWidth: 110},
		{ID: "user", Title: locales.ProxyMgrColUser(), Width: 100, MinWidth: 60},
		{ID: "hosts", Title: locales.ProxyMgrColHosts(), Width: 70, MinWidth: 50, Align: ui.End},
		{ID: "test", Title: locales.ProxyMgrColTest(), MinWidth: 200},
	}
	m.list.Selected = &m.selected
	m.list.Key = func(i int) any { return proxies[i].ID }
	m.list.Label = func(i int) string { return proxies[i].Name }
	m.list.CellMenu = func(menu *ui.Menu, row, _ int) { a.proxyMenu(menu, proxies[row]) }
	table := ui.Table(c, &m.list, columns, len(proxies), func(row, col int) {
		p := proxies[row]
		switch columns[col].ID {
		case "name":
			ui.Row(c).Gap(6).AlignItems(ui.Center).MinWidth(0).Children(func() {
				ui.Icon(c, icons.Get("waypoints")).FontSize(13).Shrink(0)
				ui.Text(c, p.Name).SingleLine().MinWidth(0).Tooltip(cmp.Or(p.Note, p.Name))
			})
		case "kind":
			ui.Text(c, proxyKindLabel(p.Kind)).SingleLine()
		case "addr":
			ui.Text(c, proxyAddress(p)).SingleLine().Tooltip(proxyAddress(p)).Selectable()
		case "user":
			widgets.Muted(c, cmp.Or(p.User, "—")).SingleLine()
		case "hosts":
			users := a.hosts.ProxyUsers(p.ID)
			tip := locales.ProxyMgrUsedBy(len(users))
			if len(users) > 0 {
				tip += "\n" + proxyHostsText(users)
			}
			widgets.Muted(c, strconv.Itoa(len(users))).SingleLine().Tooltip(tip)
		case "test":
			a.proxyTestCell(c, p)
		}
	}).Grow(1).MinHeight(0)
	if table.Submitted() && m.selected >= 0 && m.selected < len(proxies) {
		a.editProxy(proxies[m.selected], nil)
	}
}

// proxyMenu is a proxy's context menu.
func (a *App) proxyMenu(menu *ui.Menu, p *config.Proxy) {
	if menu.Item(locales.ProxyMgrEdit()).Chosen() {
		a.editProxy(p, nil)
	}
	if menu.Item(locales.ProxyMgrTest()).Disabled(a.proxyTesting(p.ID)).Chosen() {
		a.testLibraryProxy(p)
	}
	if menu.Item(locales.ProxyMgrDuplicate()).Chosen() {
		a.duplicateProxy(p)
	}
	menu.Separator()
	if menu.Item(locales.Delete()).Danger().Chosen() {
		a.proxyMgr.deleting = p
	}
}

// proxyTestCell is how a proxy's last test went, with a button that
// tests it again.
func (a *App) proxyTestCell(c *ui.Context, p *config.Proxy) {
	t := a.proxyTests[p.ID]
	ui.Row(c).Gap(6).AlignItems(ui.Center).FillWidth().MinWidth(0).Children(func() {
		proxyTestSummary(c, t)
		ui.Spacer(c)
		if widgets.IconButton(c, "zap", locales.ProxyMgrTest()).Disabled(t != nil && t.running).Clicked() {
			a.testLibraryProxy(p)
		}
	})
}

// proxyTestSummary is a test's state on one line: a dot, the latency, the
// address and the place, or why it failed, the whole in its tooltip.
func proxyTestSummary(c *ui.Context, t *proxyTest) {
	switch {
	case t == nil:
		widgets.Muted(c, locales.ProxyMgrNotTested()).FontSize(12).SingleLine()
	case t.running:
		ui.Spinner(c).Size(12, 12).Shrink(0)
		widgets.Muted(c, locales.ProxyMgrTesting()).FontSize(12).SingleLine()
	case t.err != nil:
		text := proxyTestError(t.err)
		widgets.StatusDot(c, theme.Danger)
		ui.Text(c, text).FontSize(12).SingleLine().MinWidth(0).Shrink(1).Tooltip(text)
	default:
		parts := []string{proxyLatencyText(t.res.Latency)}
		place := t.res.Location.String()
		if t.res.IP != "" {
			parts = append(parts, t.res.IP)
		}
		if place != "" {
			parts = append(parts, place)
		}
		if t.res.IP == "" {
			parts = append(parts, locales.ProxyMgrNoExitInfo())
		}
		text := strings.Join(parts, " · ")
		widgets.StatusDot(c, proxyLatencyColor(t.res.Latency))
		tip := text
		if t.res.Source != "" {
			tip += "\n" + locales.ProxyMgrTestSource(t.res.Source)
		}
		ui.Text(c, text).FontSize(12).SingleLine().MinWidth(0).Shrink(1).Tooltip(tip).Selectable()
	}
}

func proxyLatencyText(d time.Duration) string {
	return strconv.FormatInt(max(d.Milliseconds(), 1), 10) + " ms"
}

// proxyLatencyColor is green for a fast proxy, amber for a slow one and
// red for a very slow one.
func proxyLatencyColor(d time.Duration) ui.Color {
	switch {
	case d < 300*time.Millisecond:
		return theme.Success
	case d < 1000*time.Millisecond:
		return theme.Warning
	}
	return theme.Danger
}

// proxyTestError says why a test failed, in the user's words.
func proxyTestError(err error) string {
	var hop *sshx.HopError
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return locales.ErrTimeout()
	case errors.As(err, &hop):
		return describeProxyFailure(hop.Err)
	}
	return err.Error()
}

// proxyTesting reports whether the test with key is under way.
func (a *App) proxyTesting(key string) bool {
	t := a.proxyTests[key]
	return t != nil && t.running
}

// testLibraryProxy tests p with its saved password.
func (a *App) testLibraryProxy(p *config.Proxy) {
	if a.proxyTesting(p.ID) {
		return
	}
	cfg := p.Config()
	if !libTakesPassword(p) {
		a.runProxyTest(p.ID, cfg, "")
		return
	}
	id := p.ID
	a.setProxyTest(id, &proxyTest{running: true})
	a.libraryPassword(id, func(pw string, _ bool) {
		delete(a.proxyTests, id)
		a.runProxyTest(id, cfg, pw)
	})
}

func (a *App) setProxyTest(key string, t *proxyTest) {
	if a.proxyTests == nil {
		a.proxyTests = map[string]*proxyTest{}
	}
	a.proxyTests[key] = t
}

// runProxyTest tests the proxy cfg with password, keeping how it goes
// under key.
func (a *App) runProxyTest(key string, cfg config.ProxyConfig, password string) {
	a.stopProxyTest(key)
	ctx, cancel := context.WithTimeout(context.Background(), proxyTestTimeout)
	t := &proxyTest{running: true, cancel: cancel}
	a.setProxyTest(key, t)
	lang := geo.Lang(i18n.Locale())
	go func() {
		defer cancel()
		res, err := proxycheck.Check(ctx, func(ctx context.Context, addr string) (net.Conn, error) {
			return dialProxyContext(ctx, &cfg, password, addr)
		}, lang)
		a.post(func() {
			if a.proxyTests[key] != t {
				return // stopped, or tested again
			}
			t.running, t.cancel, t.res, t.err, t.at = false, nil, res, err, time.Now()
		})
	}()
	a.redraw()
}

// dialProxyContext opens a connection to addr through the proxy cfg, as
// sshx.DialProxy does, giving up when ctx ends.
func dialProxyContext(ctx context.Context, cfg *config.ProxyConfig, password, addr string) (net.Conn, error) {
	timeout := 12 * time.Second
	if deadline, ok := ctx.Deadline(); ok {
		timeout = min(timeout, time.Until(deadline))
	}
	type dialed struct {
		conn net.Conn
		err  error
	}
	done := make(chan dialed, 1)
	go func() {
		conn, err := sshx.DialProxy(cfg, password, addr, timeout)
		done <- dialed{conn, err}
	}()
	select {
	case d := <-done:
		return d.conn, d.err
	case <-ctx.Done():
		go func() {
			if d := <-done; d.conn != nil {
				d.conn.Close()
			}
		}()
		return nil, ctx.Err()
	}
}

// stopProxyTest stops the test with key, forgetting it.
func (a *App) stopProxyTest(key string) {
	if t := a.proxyTests[key]; t != nil && t.cancel != nil {
		t.cancel()
	}
	delete(a.proxyTests, key)
}

// duplicateProxy opens the form for a copy of p, named so.
func (a *App) duplicateProxy(p *config.Proxy) {
	c := p.Clone()
	c.ID, c.Name = "", locales.ProxyMgrCopyName(p.Name)
	a.editProxy(c, nil)
	a.proxyMgr.edit.id, a.proxyMgr.edit.original = "", nil
	pw := a.passwords[libMemKey(p.ID)]
	if pw != "" {
		a.proxyMgr.edit.password = pw
	}
}

// editProxy opens the form for p, nil for a new proxy; then, when not
// nil, gets the proxy once saved.
func (a *App) editProxy(p *config.Proxy, then func(*config.Proxy)) {
	e := &proxyEdit{kind: config.ProxySOCKS5, port: "1080", then: then, errs: map[string]string{}}
	if p != nil {
		e.id, e.original, e.kind, e.name, e.addr, e.user, e.note = p.ID, p, p.Kind, p.Name, p.Addr, p.User, p.Note
		e.port = strconv.Itoa(p.PortOrDefault())
		if p.ID != "" && libTakesPassword(p) {
			e.passwordSaved = true
		}
	}
	a.stopProxyTest(proxyEditTestKey)
	a.proxyMgr.edit, a.proxyMgr.err = e, ""
	if !a.windowOpen(windowProxies) {
		a.openProxyManager()
	}
	a.plat.FocusWindow(windowProxies)
	a.redraw()
}

// proxyOf is the form as a proxy, with what is wrong with it.
func (e *proxyEdit) proxy() (*config.Proxy, map[string]string) {
	errs := map[string]string{}
	p := &config.Proxy{ID: e.id, Kind: e.kind, Name: strings.TrimSpace(e.name), Addr: strings.TrimSpace(e.addr),
		User: strings.TrimSpace(e.user), Note: strings.TrimSpace(e.note)}
	if e.original != nil {
		p = e.original.Clone()
		p.Kind, p.Name, p.Addr, p.User, p.Note = e.kind, strings.TrimSpace(e.name), strings.TrimSpace(e.addr), strings.TrimSpace(e.user), strings.TrimSpace(e.note)
	}
	if p.Addr == "" || strings.ContainsAny(p.Addr, " /\\@") {
		errs["addr"] = locales.ProxyMgrAddrRequired()
	}
	port, err := strconv.Atoi(strings.TrimSpace(e.port))
	if err != nil || port < 1 || port > 65535 {
		errs["port"] = locales.ProxyMgrPortInvalid()
	}
	p.Port = port
	if p.Name == "" {
		p.Name = p.Addr
	}
	return p, errs
}

// saveProxyEdit saves the form into the library.
func (a *App) saveProxyEdit() {
	e := a.proxyMgr.edit
	p, errs := e.proxy()
	e.errs = errs
	if len(errs) > 0 {
		return
	}
	if p.ID == "" {
		p.ID = config.NewProxyID()
	}
	// An empty field keeps the saved password; a new proxy has none.
	setPassword := e.password != "" || !e.passwordSaved
	if _, err := a.saveLibraryProxy(p, e.password, setPassword); err != nil {
		a.proxyMgr.err = locales.ProxyMgrSaveFailed(err.Error())
		return
	}
	a.stopProxyTest(proxyEditTestKey)
	a.proxyMgr.edit = nil
	if saved := a.hosts.Proxy(p.ID); e.then != nil && saved != nil {
		e.then(saved)
	}
	a.redraw()
}

// testProxyEdit tests the form's proxy as it is typed, with the saved
// password when the field is empty.
func (a *App) testProxyEdit() {
	e := a.proxyMgr.edit
	p, errs := e.proxy()
	e.errs = errs
	if len(errs) > 0 {
		return
	}
	cfg := p.Config()
	if e.password != "" || !e.passwordSaved || !libTakesPassword(p) {
		a.runProxyTest(proxyEditTestKey, cfg, e.password)
		return
	}
	a.setProxyTest(proxyEditTestKey, &proxyTest{running: true})
	a.libraryPassword(p.ID, func(pw string, _ bool) {
		if a.proxyMgr.edit != e {
			return
		}
		delete(a.proxyTests, proxyEditTestKey)
		a.runProxyTest(proxyEditTestKey, cfg, pw)
	})
}

// proxyEditView is the form, in a modal over the manager.
func (a *App) proxyEditView(c *ui.Context) {
	e := a.proxyMgr.edit
	if e == nil {
		return
	}
	open := true
	ui.Modal(c, &open, func() {
		ui.Column(c).Width(480).MaxWidthPercent(92).Gap(12).Children(func() {
			title := locales.ProxyMgrNewTitle()
			if e.id != "" {
				title = locales.ProxyMgrEditTitle()
			}
			ui.Text(c, title).FontSize(17).Bold()
			ui.Form(c, func() {
				ui.Field(c, locales.ProxyMgrName(), func() {
					ui.TextInput(c, &e.name).Placeholder(locales.ProxyMgrNameHint()).FillWidth()
				})
				ui.Field(c, locales.FieldProxyType(), func() {
					kinds, labels := proxyKindChoices()
					chosen := proxyKindLabel(e.kind)
					if ui.Select(c, &chosen, labels).Width(200).Changed() {
						for i, l := range labels {
							if l == chosen {
								old := strconv.Itoa(config.DefaultProxyPort(e.kind))
								e.kind = kinds[i]
								if strings.TrimSpace(e.port) == "" || e.port == old {
									e.port = strconv.Itoa(config.DefaultProxyPort(e.kind))
								}
							}
						}
					}
				}).Description(proxyKindNote(e.kind))
				ui.Field(c, locales.FieldHost(), func() {
					ui.TextInput(c, &e.addr).Placeholder(locales.FieldHostPlaceholder()).FillWidth()
				}).Error(e.errs["addr"])
				ui.Field(c, locales.FieldPort(), func() {
					ui.TextInput(c, &e.port).Width(100)
				}).Error(e.errs["port"])
				userLabel := locales.FieldUser()
				if e.kind == config.ProxySOCKS4 {
					userLabel = locales.ProxyMgrUserID()
				}
				ui.Field(c, userLabel, func() {
					ui.TextInput(c, &e.user).Placeholder(locales.ProxyUserPlaceholder()).FillWidth()
				})
				if config.TakesPassword(e.kind) && strings.TrimSpace(e.user) != "" {
					hint := ""
					if e.passwordSaved {
						hint = locales.ProxyMgrPasswordKeep()
					}
					ui.Field(c, locales.FieldPassword(), func() {
						ui.TextInput(c, &e.password).Password().Placeholder(hint).FillWidth()
					}).Description(locales.ProxyMgrPasswordNote())
				}
				ui.Field(c, locales.ProxyMgrNote(), func() {
					ui.TextInput(c, &e.note).FillWidth()
				})
			})
			if e.id != "" {
				if n := len(a.hosts.ProxyUsers(e.id)); n > 0 {
					widgets.Muted(c, locales.ProxyMgrEditUsed(n)).FontSize(12).MaxWidthPercent(100)
				}
			}
			ui.Row(c).Gap(8).AlignItems(ui.Center).MinHeight(28).Children(func() {
				proxyTestSummary(c, a.proxyTests[proxyEditTestKey])
			})
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				if ui.Button(c, locales.ProxyMgrTest()).Disabled(a.proxyTesting(proxyEditTestKey)).Clicked() {
					a.testProxyEdit()
				}
				ui.Spacer(c)
				if ui.Button(c, locales.Cancel()).Clicked() {
					open = false
				}
				if ui.PrimaryButton(c, locales.ProxyMgrSave()).Clicked() {
					a.saveProxyEdit()
				}
			})
		})
	})
	if !open {
		a.stopProxyTest(proxyEditTestKey)
		a.proxyMgr.edit = nil
	}
}

// proxyKindNote says what a kind of proxy is.
func proxyKindNote(k config.ProxyKind) string {
	switch k {
	case config.ProxyHTTPS:
		return locales.ProxyMgrKindHTTPS()
	case config.ProxySOCKS4:
		return locales.ProxyMgrKindSOCKS4()
	}
	return ""
}

// proxyDeleteView asks before deleting a proxy, saying which hosts keep
// it as their own.
func (a *App) proxyDeleteView(c *ui.Context) {
	p := a.proxyMgr.deleting
	if p == nil {
		return
	}
	open := true
	users := a.hosts.ProxyUsers(p.ID)
	ui.Modal(c, &open, func() {
		ui.Column(c).Width(420).MaxWidthPercent(90).Gap(10).Children(func() {
			ui.Text(c, locales.ProxyMgrDeleteTitle(p.Name)).FontSize(16).Bold().MaxWidthPercent(100)
			body := locales.ProxyMgrDeleteBody()
			if len(users) > 0 {
				body = locales.ProxyMgrDeleteUsed(len(users))
			}
			widgets.Muted(c, body).MaxWidthPercent(100)
			ui.Row(c).Gap(7).Justify(ui.End).Children(func() {
				if ui.Button(c, locales.Cancel()).Clicked() {
					open = false
				}
				if ui.PrimaryButton(c, locales.Delete()).Clicked() {
					if _, err := a.deleteLibraryProxy(p.ID); err != nil {
						a.proxyMgr.err = locales.ProxyMgrSaveFailed(err.Error())
					}
					open = false
				}
			})
		})
	})
	if !open {
		a.proxyMgr.deleting = nil
	}
}

// proxyHostsText lists hosts by title, for a tooltip.
func proxyHostsText(hosts []*config.Host) string {
	names := make([]string, 0, len(hosts))
	for _, h := range hosts {
		names = append(names, h.Title())
	}
	return strings.Join(names, "\n")
}
