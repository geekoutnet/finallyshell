package app

import (
	"net"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"finallyshell/internal/config"
	"finallyshell/internal/icons"
	"finallyshell/internal/sshx"
	"finallyshell/internal/widgets"
	"finallyshell/locales"

	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/ui"
)

// tunnelEditor is the connection editor's Tunnels page: the host's
// tunnels as edited, saved with the host on OK, and the dialog that adds
// or changes one.
type tunnelEditor struct {
	list  []config.Tunnel
	table ui.ListState
	row   int // the row chosen, -1 for none

	dialog     bool
	editing    int // the row the dialog changes, -1 for a new tunnel
	form       tunnelForm
	secrets    map[string]tunnelSecretEdit
	removeOpen bool
	removeRow  int
}

// tunnelForm is the dialog's fields, as typed.
type tunnelForm struct {
	kind                                         config.TunnelKind
	pacText                                      string // the PAC rules as typed
	listenPort                                   string
	public                                       bool // listens on listenHost, not the loopback
	listenHost                                   string
	targetHost, targetPort                       string
	enabled                                      bool
	name, description                            string
	socketMode, mixed, advanced, acknowledged    bool
	socket, proxyUser, proxyPassword             string
	upstreamKind                                 config.ProxyKind
	upstreamAddr, upstreamUser, upstreamPassword string
	showProxy, showUpstream                      bool
	allow, maxConns, rate, idle                  string
	// keep is the tunnel edited, whose keys this build does not know
	// are kept.
	keep config.Tunnel
	// checked is set once OK was pressed, as the editor's.
	checked bool
}

// tunnelField names a field of the dialog that can be wrong.
type tunnelField int

const (
	fieldListenPort tunnelField = iota
	fieldListenHost
	fieldTargetHost
	fieldTargetPort
	fieldTunnelSocket
	fieldTunnelAuth
	fieldTunnelUpstream
	fieldTunnelUpstreamAuth
	fieldTunnelAllow
	fieldTunnelMax
	fieldTunnelRate
	fieldTunnelIdle
	fieldTunnelPublic
)

// newTunnelEditor is the page for h's tunnels; nil for a new host.
func newTunnelEditor(h *config.Host) tunnelEditor {
	te := tunnelEditor{row: -1, editing: -1}
	if h != nil {
		te.list = slices.Clone(h.Tunnels)
	}
	return te
}

// result is the tunnels to save: nil for none, as the host had.
func (te *tunnelEditor) result() []config.Tunnel {
	if len(te.list) == 0 {
		return nil
	}
	return slices.Clone(te.list)
}

// tunnelKindLabel is the kind of a tunnel in the language shown.
func tunnelKindLabel(k config.TunnelKind) string {
	switch k {
	case config.TunnelLocal:
		return locales.TunnelKindLocal()
	case config.TunnelRemote:
		return locales.TunnelKindRemote()
	case config.TunnelDynamic:
		return locales.TunnelKindDynamic()
	case config.TunnelHTTP:
		return locales.TunKindHTTP()
	case config.TunnelRemoteDynamic:
		return locales.TunKindRemoteDynamic()
	case config.TunnelRemoteHTTP:
		return locales.TunKindRemoteHTTP()
	}
	return string(k)
}

// tunnelTarget is where t forwards to, as the tables show it.
func tunnelTarget(t *config.Tunnel) string {
	if t.TargetSocket != "" {
		return t.TargetSocket
	}
	if t.Mixed && t.Kind.IsProxy() && !t.Kind.IsRemote() {
		return locales.TunMixedExit()
	}
	if t.Kind == config.TunnelDynamic {
		return locales.TunnelSocks()
	}
	if t.Upstream != "" {
		if p, err := sshx.ParseTunnelUpstream(t.Upstream); err == nil && p != nil {
			via := locales.TunKindHTTP()
			if p.Kind == config.ProxySOCKS5 {
				via = locales.TunnelSocks()
			}
			text := via + " · " + sshx.ProxyAddr(p)
			if !t.Kind.IsProxy() {
				text = t.TargetAddr() + " · " + text
			}
			return text
		}
	}
	if t.Kind.IsProxy() {
		if t.Kind.IsRemote() {
			return locales.TunLocalExit()
		}
		return locales.TunServerExit()
	}
	return t.TargetAddr()
}

func tunnelName(t *config.Tunnel) string {
	if t.Name != "" {
		return t.Name
	}
	return tunnelKindLabel(t.Kind)
}

func configuredTunnelAddr(t *config.Tunnel) string {
	if t.ListenPort == 0 {
		return t.ListenHostOrLoopback() + " · " + locales.TunAutomatic()
	}
	return t.ListenAddr()
}

func tunnelColumns() []ui.TableColumn {
	return []ui.TableColumn{
		{ID: "type", Title: locales.TunNameAndType(), MinWidth: 119},
		{ID: "listen", Title: locales.ColTunnelListen(), MinWidth: 102},
		{ID: "target", Title: locales.TunTargetAndExit(), MinWidth: 105},
		{ID: "options", Title: locales.TunOptions(), Width: 74},
		{ID: "enabled", Title: locales.ColTunnelEnabled(), Width: 68, Align: ui.Center},
	}
}

// tunnelsPage is the editor's Tunnels page: a table of the host's
// tunnels, with buttons to add, change and remove them.
func (a *App) tunnelsPage(c *ui.Context, ed *connEditor) {
	te := &ed.tunnels
	_, height := c.Size()
	ui.Column(c).Height(max(200, height-118)).Gap(10).Children(func() {
		a.tunnelEditorTable(c, te)
	})
	ui.Modal(c, &te.dialog, func() { te.dialogView(c) })
	if !te.dialog {
		te.form.proxyPassword, te.form.upstreamPassword = "", ""
	}
	ui.Modal(c, &te.removeOpen, func() {
		if te.removeRow < 0 || te.removeRow >= len(te.list) {
			return
		}
		ui.Column(c).Width(360).Gap(12).Children(func() {
			ui.Text(c, locales.TunRemoveQuestion(tunnelName(&te.list[te.removeRow]))).FillWidth()
			if bulkConfirmButtons(c, &te.removeOpen, locales.TunnelRemove()) {
				delete(te.secrets, te.list[te.removeRow].ID)
				te.list = slices.Delete(te.list, te.removeRow, te.removeRow+1)
				te.row = min(te.removeRow, len(te.list)-1)
			}
		})
	})
}

func (a *App) tunnelEditorTable(c *ui.Context, te *tunnelEditor) {
	widgets.Muted(c, locales.TunnelsIntro()).FontSize(12)
	list := te.list
	if te.row >= len(list) {
		te.row = -1
	}
	te.table.Selected = &te.row
	te.table.Label = func(i int) string { return tunnelName(&list[i]) + " " + list[i].ListenAddr() }
	te.table.CellMenu = func(menu *ui.Menu, row, _ int) {
		if resourceMenuItem(menu, locales.TunnelEdit(), "", false) {
			te.open(row)
		}
		if resourceMenuItem(menu, locales.TunnelRemove(), "", true) {
			te.askRemove(row)
		}
	}
	table := ui.Table(c, &te.table, tunnelColumns(), len(list), func(row, col int) {
		t := &list[row]
		muted := theme.TextMuted
		if row == te.row {
			muted = theme.AccentText.Alpha(0.8)
		}
		switch col {
		case 0:
			tunnelTypeCell(c, t)
		case 1:
			ui.Row(c).Gap(6).Children(func() {
				tunnelAddressCell(c, t, nil, "")
				if !config.IsLoopback(t.ListenHost) {
					ui.Icon(c, icons.Get("triangle-alert")).FontSize(13).TextColor(theme.Warning).Tooltip(publicWarning(t.Kind))
				}
			})
		case 2:
			text := ui.Text(c, tunnelTarget(t)).MaxLines(2).Tooltip(tunnelTarget(t))
			if t.Kind == config.TunnelDynamic {
				text.TextColor(muted)
			}
		case 3:
			ui.Row(c).Gap(4).Children(func() {
				if t.ProxyUser != "" || tunnelWantsSecret(*t, true) {
					ui.Icon(c, icons.Get("key-round")).FontSize(13).Tooltip(locales.TunAuth())
				}
				if len(t.AllowFrom) > 0 {
					ui.Icon(c, icons.Get("cable")).FontSize(13).Tooltip(locales.TunAllow())
				}
				if t.MaxConns > 0 || t.RateKBps > 0 || t.IdleSeconds > 0 {
					ui.Icon(c, icons.Get("gauge")).FontSize(13).Tooltip(locales.SectionAdvanced())
				}
			})
		case 4:
			on := t.Enabled()
			if ui.Checkbox(c, &on, "").Label(locales.TunnelStartWithSession()).Changed() {
				te.list[row].Disabled = !on
			}
		}
	}).Grow(1).MinHeight(0).Border(1, theme.Border).Radius(6)
	table.Children(func() {
		if len(list) == 0 {
			widgets.Muted(c, locales.TunnelsEmpty()).Padding(36, 12)
		}
	})
	if table.Submitted() && te.row >= 0 {
		te.open(te.row)
	}
	chosen := te.row >= 0 && te.row < len(list)
	ui.Row(c).Gap(8).Children(func() {
		if ui.Button(c, locales.TunnelAdd()).Clicked() {
			te.open(-1)
		}
		a.tunnelRecipesButton(c, a.editor)
		reason := ""
		if !chosen {
			reason = locales.TunSelectOne()
		}
		if ui.Button(c, locales.TunnelEdit()).Disabled(!chosen).Tooltip(reason).Clicked() && chosen {
			te.open(te.row)
		}
		if ui.Button(c, locales.TunnelRemove()).Disabled(!chosen).Tooltip(reason).Clicked() && chosen {
			te.askRemove(te.row)
		}
	})
	widgets.Muted(c, locales.TerminalNextConnect()).FontSize(12)
}

func (te *tunnelEditor) askRemove(row int) { te.removeRow, te.removeOpen = row, true }

// open shows the dialog for the tunnel in row, or a new one for -1.
func (te *tunnelEditor) open(row int) {
	t := config.Tunnel{Kind: config.TunnelLocal, TargetHost: config.LoopbackHost}
	if row >= 0 {
		t = te.list[row]
	}
	f := tunnelForm{kind: t.Kind, keep: t, enabled: t.Enabled(), targetHost: t.TargetHost, listenHost: t.ListenHost,
		name: t.Name, description: t.Description, socket: t.TargetSocket, socketMode: t.TargetSocket != "", mixed: t.Mixed, proxyUser: t.ProxyUser,
		allow: strings.Join(t.AllowFrom, "\n"), maxConns: tunnelLimitText(t.MaxConns), rate: tunnelLimitText(t.RateKBps), idle: tunnelLimitText(t.IdleSeconds)}
	if p, err := sshx.ParseTunnelUpstream(t.Upstream); err == nil && p != nil {
		f.upstreamKind, f.upstreamAddr, f.upstreamUser = p.Kind, sshx.ProxyAddr(p), p.User
	}
	if t.ListenPort != 0 {
		f.listenPort = strconv.Itoa(t.ListenPort)
	}
	if t.TargetPort != 0 {
		f.targetPort = strconv.Itoa(t.TargetPort)
	}
	if t.ListenHost != "" && t.ListenHost != config.LoopbackHost {
		f.public = true
	} else {
		f.listenHost = ""
	}
	te.form, te.editing, te.dialog = f, row, true
}

func tunnelLimitText(value int) string {
	if value == 0 {
		return ""
	}
	return strconv.Itoa(value)
}

func validTunnelLimit(value string) (int, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, true
	}
	num, err := strconv.ParseUint(value, 10, 31)
	return int(num), err == nil
}

// validPort reads a port from 1 to 65535.
func validPort(s string) (int, bool) {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	return n, err == nil && n >= 1 && n <= 65535
}

// validHost tells whether s can be an address or a host name.
func validHost(s string) bool {
	if s == "" {
		return false
	}
	if net.ParseIP(s) != nil {
		return true
	}
	return !strings.ContainsFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '.' || r == '_')
	})
}

// tunnel is the tunnel the form says, with the keys the one edited had
// that this build does not know.
func (f *tunnelForm) tunnel() config.Tunnel {
	t := f.keep
	t.Kind = f.kind
	t.Name, t.Description = strings.TrimSpace(f.name), strings.TrimSpace(f.description)
	t.ListenPort, _ = strconv.Atoi(strings.TrimSpace(f.listenPort))
	t.ListenHost = ""
	if f.public {
		t.ListenHost = strings.TrimSpace(f.listenHost)
	}
	if remoteSide(f.kind) && strings.EqualFold(t.ListenHost, "localhost") {
		t.ListenHost = "" // the server's 127.0.0.1
	}
	t.TargetHost, t.TargetPort, t.TargetSocket = "", 0, ""
	if f.kind == config.TunnelLocal && f.socketMode {
		t.TargetSocket = tunnelSocketInput(f.socket)
	} else if !f.kind.IsProxy() {
		t.TargetHost = strings.TrimSpace(f.targetHost)
		t.TargetPort, _ = strconv.Atoi(strings.TrimSpace(f.targetPort))
	}
	t.Mixed, t.ProxyUser, t.Upstream = false, "", ""
	if f.kind.IsProxy() {
		t.Mixed, t.ProxyUser = f.mixed && !f.kind.IsRemote(), strings.TrimSpace(f.proxyUser)
	}
	if !f.kind.IsProxy() || f.kind.IsRemote() {
		t.SystemProxy, t.PAC = false, nil
	}
	if !f.kind.IsRemote() || config.IsLoopback(t.ListenHost) {
		t.ServerRelay = false
	}
	if f.kind.IsRemote() && f.upstreamKind != "" {
		t.Upstream = f.upstreamURL()
	}
	t.AllowFrom = nil
	if allow := strings.Fields(f.allow); len(allow) > 0 {
		t.AllowFrom = allow
	}
	t.MaxConns, _ = validTunnelLimit(f.maxConns)
	t.RateKBps, _ = validTunnelLimit(f.rate)
	t.IdleSeconds, _ = validTunnelLimit(f.idle)
	t.Disabled = !f.enabled
	return t
}

// remoteSide tells whether a tunnel of kind listens on the server.
func remoteSide(k config.TunnelKind) bool { return k.IsRemote() }

// sameListener tells whether a and b would listen on the same port of
// the same machine.
func sameListener(a, b *config.Tunnel) bool {
	if remoteSide(a.Kind) != remoteSide(b.Kind) || a.ListenPort == 0 || a.ListenPort != b.ListenPort {
		return false
	}
	ha, hb := a.ListenHostOrLoopback(), b.ListenHostOrLoopback()
	wildcard := func(h string) bool { ip := net.ParseIP(h); return ip != nil && ip.IsUnspecified() }
	return strings.EqualFold(ha, hb) || net.ParseIP(ha) != nil && net.ParseIP(ha).Equal(net.ParseIP(hb)) || wildcard(ha) || wildcard(hb) ||
		strings.EqualFold(ha, "localhost") && hb == config.LoopbackHost || strings.EqualFold(hb, "localhost") && ha == config.LoopbackHost
}

func (f *tunnelForm) upstreamURL() string {
	scheme := "socks5"
	if f.upstreamKind == config.ProxyHTTP {
		scheme = "http"
	}
	addr := &url.URL{Scheme: scheme, Host: strings.TrimSpace(f.upstreamAddr)}
	if user := strings.TrimSpace(f.upstreamUser); user != "" {
		addr.User = url.User(user)
	}
	return addr.String()
}

// problems are what is wrong with the dialog's fields, in the language
// shown.
func (te *tunnelEditor) problems() map[tunnelField]string {
	f := &te.form
	errs := map[tunnelField]string{}
	if port, ok := validTunnelLimit(f.listenPort); !ok || port > 65535 {
		errs[fieldListenPort] = locales.TunListenPortError()
	}
	if host := strings.TrimSpace(f.listenHost); f.public && !validHost(host) {
		errs[fieldListenHost] = locales.ErrListenAddrInvalid()
	} else if f.public && remoteSide(f.kind) && !strings.EqualFold(host, "localhost") && net.ParseIP(host) == nil {
		// Sent to the server as an IP address: x/crypto would look a
		// name up on this computer.
		errs[fieldListenHost] = locales.ErrRemoteListenAddrIP()
	}
	if f.kind == config.TunnelLocal && f.socketMode {
		if !sshx.ValidTunnelSocket(tunnelSocketInput(f.socket)) {
			errs[fieldTunnelSocket] = locales.TunSocketError()
		}
	} else if !f.kind.IsProxy() {
		if strings.TrimSpace(f.targetHost) == "" {
			errs[fieldTargetHost] = locales.ErrTargetHostRequired()
		} else if !validHost(strings.TrimSpace(f.targetHost)) {
			errs[fieldTargetHost] = locales.ErrListenAddrInvalid()
		}
		if _, ok := validPort(f.targetPort); !ok {
			errs[fieldTargetPort] = locales.ErrPortInvalid()
		}
	}
	t := f.tunnel()
	if f.kind.IsProxy() && t.ProxyUser != "" && (!validTunnelUser(t.ProxyUser) || len(f.proxyPassword) > 255 || f.proxyPassword == "" && f.keep.ProxyUser != t.ProxyUser) {
		errs[fieldTunnelAuth] = locales.TunSecretError()
	}
	if f.kind.IsRemote() && f.upstreamKind != "" {
		upstream, err := sshx.ParseTunnelUpstream(t.Upstream)
		if err != nil || upstream == nil || strings.ContainsAny(f.upstreamAddr, "@/\\?#") {
			errs[fieldTunnelUpstream] = locales.TunUpstreamError()
		} else if upstream.User != "" && (len(f.upstreamPassword) > 255 || f.upstreamPassword == "" && f.keep.Upstream != t.Upstream) {
			errs[fieldTunnelUpstreamAuth] = locales.TunSecretError()
		}
	}
	if _, err := sshx.ParseTunnelAllowList(t.AllowFrom); err != nil {
		errs[fieldTunnelAllow] = locales.TunAllowError()
	}
	for field, value := range map[tunnelField]string{fieldTunnelMax: f.maxConns, fieldTunnelRate: f.rate, fieldTunnelIdle: f.idle} {
		if _, ok := validTunnelLimit(value); !ok {
			errs[field] = locales.TunLimitError()
		}
	}
	if !config.IsLoopback(t.ListenHost) && !f.acknowledged && requiresTunnelExposureAck(t, f.keep) {
		errs[fieldTunnelPublic] = locales.TunPublicError()
	}
	if errs[fieldListenPort] == "" && errs[fieldListenHost] == "" {
		for i := range te.list {
			if i != te.editing && sameListener(&t, &te.list[i]) {
				errs[fieldListenPort] = locales.ErrTunnelDuplicate()
				break
			}
		}
	}
	return errs
}

func requiresTunnelExposureAck(next, previous config.Tunnel) bool {
	return next.ListenHost != previous.ListenHost || next.ListenPort != previous.ListenPort || next.Kind != previous.Kind ||
		next.TargetAddr() != previous.TargetAddr() || next.TargetSocket != previous.TargetSocket || next.Mixed != previous.Mixed ||
		next.Upstream != previous.Upstream || next.ProxyUser == "" && previous.ProxyUser != "" || !slices.Equal(next.AllowFrom, previous.AllowFrom)
}

func validTunnelUser(user string) bool {
	return len(user) <= 255 && !strings.Contains(user, ":") && !strings.ContainsFunc(user, unicode.IsControl)
}

func tunnelSocketInput(value string) string { return strings.Trim(value, " \t") }

// saveDialog puts the dialog's tunnel in the list, unless something is
// wrong with it.
func (te *tunnelEditor) saveDialog() {
	te.form.checked = true
	if len(te.problems()) > 0 {
		return
	}
	t := te.form.tunnel()
	if t.ProxyUser != "" || t.Upstream != "" {
		if t.ID == "" {
			t.ID = config.NewTunnelID()
		}
		edit := te.secrets[t.ID]
		if te.form.proxyPassword != "" && t.ProxyUser != "" {
			edit.proxy, edit.proxyUser = te.form.proxyPassword, t.ProxyUser
		}
		if te.form.upstreamPassword != "" && t.Upstream != "" {
			edit.upstream, edit.upstreamURL = te.form.upstreamPassword, t.Upstream
		}
		if te.secrets == nil {
			te.secrets = make(map[string]tunnelSecretEdit)
		}
		te.secrets[t.ID] = edit
	}
	if te.editing >= 0 && te.editing < len(te.list) {
		te.list[te.editing] = t
		te.row = te.editing
	} else {
		te.list = append(te.list, t)
		te.row = len(te.list) - 1
	}
	te.form.proxyPassword, te.form.upstreamPassword = "", ""
	te.dialog = false
}

// publicWarning says who else can use a tunnel of kind that listens on
// an address other than the loopback.
func publicWarning(k config.TunnelKind) string {
	if remoteSide(k) {
		return locales.TunnelPublicWarnRemote()
	}
	return locales.TunnelPublicWarnLocal()
}

// dialogView is the dialog that adds or changes a tunnel. Enter is OK,
// and Escape, as a click outside, Cancel.
func (te *tunnelEditor) dialogView(c *ui.Context) {
	f := &te.form
	var errs map[tunnelField]string
	if f.checked {
		errs = te.problems()
	}
	// Enter where no field takes it, and in a text input, which takes it as
	// submitting: the editor's Enter waits while the dialog shows.
	submit := c.Shortcut(0, ui.KeyEnter)
	input := func(e ui.Element) ui.Element {
		submit = e.Submitted() || submit
		return e
	}
	title := locales.TunnelNew()
	if te.editing >= 0 {
		title = locales.TunnelChange()
	}
	width, height := c.Size()
	ui.Column(c).Width(min(530, width-48)).Gap(10).Children(func() {
		ui.Text(c, title).FontSize(15).Bold()
		ui.Scroll(c).MaxHeight(max(150, height-155)).Children(func() {
			ui.Form(c, func() {
				ui.Field(c, locales.FieldTunnelType(), func() {
					kinds := config.TunnelKinds
					labels := make([]string, len(kinds))
					for i, k := range kinds {
						labels[i] = tunnelKindLabel(k)
					}
					chosen := tunnelKindLabel(f.kind)
					if ui.Select(c, &chosen, labels).Width(200).Changed() {
						f.kind = kinds[max(0, slices.Index(labels, chosen))]
						f.acknowledged = false
					}
				}).Description(kindHint(f.kind))
				ui.Field(c, locales.FieldListenPort(), func() {
					input(ui.TextInput(c.Key("tunnel-listen-port"), &f.listenPort).Width(90).AutoFocus())
				}).Description(locales.TunPortHint()).Error(errs[fieldListenPort])
				loopback := locales.TunnelLoopbackNote()
				if remoteSide(f.kind) {
					loopback = locales.TunnelLoopbackNoteRemote()
				}
				if f.public {
					loopback = ""
				}
				ui.Field(c, "", func() {
					if ui.Checkbox(c, &f.public, locales.TunnelOtherHosts()).Changed() && f.public && strings.TrimSpace(f.listenHost) == "" {
						f.listenHost = "0.0.0.0"
					}
				}).Description(loopback)
				if f.public {
					ui.Field(c, locales.FieldListenAddr(), func() {
						if input(ui.TextInput(c.Key("tunnel-listen-host"), &f.listenHost).Width(200)).Changed() {
							f.acknowledged = false
						}
					}).Error(errs[fieldListenHost])
					if !config.IsLoopback(strings.TrimSpace(f.listenHost)) {
						f.exposure(c, errs)
					}
				}
				if f.kind == config.TunnelLocal {
					ui.Checkbox(c, &f.socketMode, locales.TunSocketMode())
				}
				if f.kind == config.TunnelLocal && f.socketMode {
					ui.Field(c, locales.TunSocketPath(), func() {
						input(ui.TextInput(c.Key("tunnel-unix"), &f.socket).Placeholder("/var/run/docker.sock"))
					}).Error(errs[fieldTunnelSocket])
				} else if !f.kind.IsProxy() {
					placeholder := locales.TargetHostPlaceholderLocal()
					if remoteSide(f.kind) {
						placeholder = locales.TargetHostPlaceholderRemote()
					}
					ui.Field(c, locales.FieldTargetHost(), func() {
						input(ui.TextInput(c.Key("tunnel-target-host"), &f.targetHost).Placeholder(placeholder))
					}).Error(errs[fieldTargetHost])
					ui.Field(c, locales.FieldTargetPort(), func() {
						input(ui.TextInput(c.Key("tunnel-target-port"), &f.targetPort).Width(90))
					}).Error(errs[fieldTargetPort])
				}
				if f.kind.IsProxy() {
					if !f.kind.IsRemote() {
						ui.Checkbox(c, &f.mixed, locales.TunMixed())
					}
					ui.Field(c, locales.TunUser(), func() {
						input(ui.TextInput(c.Key("tunnel-proxy-user"), &f.proxyUser))
					})
					if strings.TrimSpace(f.proxyUser) != "" {
						tunnelPasswordField(c.Key("tunnel-password"), locales.TunProxyPassword(), &f.proxyPassword, &f.showProxy, errs[fieldTunnelAuth], input)
					}
				}
				if f.kind.IsRemote() {
					f.upstreamFields(c, errs, input)
				}
				ui.Field(c, "", func() {
					ui.Checkbox(c, &f.enabled, locales.TunnelStartWithSession())
				})
				ui.Checkbox(c, &f.advanced, locales.SectionAdvanced())
				if f.advanced || errs[fieldTunnelAllow] != "" || errs[fieldTunnelMax] != "" || errs[fieldTunnelRate] != "" || errs[fieldTunnelIdle] != "" {
					f.advancedFields(c, errs, input)
				}
				// A local proxy can be this computer's system proxy, with
				// PAC rules; the choice is kept with the tunnel.
				if (f.kind == config.TunnelDynamic || f.kind == config.TunnelHTTP) && !f.public {
					draft := f.keep
					draft.Kind, draft.ListenHost = f.kind, ""
					if f.pacText == "" && len(draft.PAC) > 0 {
						f.pacText = strings.Join(draft.PAC, ", ")
					}
					systemProxyControls(c, &draft, &f.pacText)
					f.keep.SystemProxy, f.keep.PAC = draft.SystemProxy, draft.PAC
				}
			})
		})
		ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
			if ui.Button(c, locales.Cancel()).Clicked() {
				te.dialog = false
			}
			if ui.PrimaryButton(c, locales.Ok()).Clicked() {
				submit = true
			}
		})
	})
	if submit {
		te.saveDialog()
	}
}

// kindHint says what a tunnel of kind does.
func kindHint(k config.TunnelKind) string {
	switch k {
	case config.TunnelRemote:
		return locales.TunnelHintRemote()
	case config.TunnelDynamic:
		return locales.TunnelHintDynamic()
	case config.TunnelHTTP:
		return locales.TunHintHTTP()
	case config.TunnelRemoteDynamic:
		return locales.TunHintRemoteDynamic()
	case config.TunnelRemoteHTTP:
		return locales.TunHintRemoteHTTP()
	}
	return locales.TunnelHintLocal()
}

func (f *tunnelForm) exposure(c *ui.Context, errs map[tunnelField]string) {
	strong := f.kind.IsProxy() && strings.TrimSpace(f.proxyUser) == ""
	color := theme.Warning
	text := publicWarning(f.kind)
	if strong {
		color, text = theme.Danger, locales.TunOpenProxy()
	} else if f.kind.IsProxy() {
		text = locales.TunPublicAuth()
	}
	ui.Column(c).Gap(5).Padding(8, 10).Radius(6).Background(color.Alpha(0.12)).Children(func() {
		ui.Row(c).Gap(8).Children(func() {
			ui.Icon(c, icons.Get("triangle-alert")).FontSize(15).TextColor(color)
			ui.Text(c, text).FontSize(12).Grow(1).MinWidth(0)
		})
		if f.kind.IsRemote() {
			if f.kind.IsProxy() {
				widgets.Muted(c, locales.TunGatewayPorts()).FontSize(12).FillWidth()
			}
			ui.Link(c, locales.TunSeePublic(), tunnelPublicDocs()).FontSize(12)
		}
		ui.Field(c, "", func() {
			ui.Checkbox(c, &f.acknowledged, locales.TunPublicConfirm())
		}).Error(errs[fieldTunnelPublic])
	})
}

func tunnelPublicDocs() string {
	const base = "https://github.com/geekoutnet/finallyshell/blob/dev/docs/"
	if strings.HasPrefix(i18n.Locale(), "zh") {
		return base + "zh-CN/features.md#隧道"
	}
	return base + "en/features.md#tunnels"
}

func tunnelPasswordField(c *ui.Context, label string, value *string, show *bool, problem string, input func(ui.Element) ui.Element) {
	ui.Field(c, label, func() {
		ui.Row(c).Gap(6).Children(func() {
			field := input(ui.TextInput(c, value).Grow(1).MinWidth(0))
			action := locales.HideSecret()
			if !*show {
				field.Password()
				action = locales.ShowSecret()
			}
			if ui.Button(c, action).Clicked() {
				*show = !*show
			}
		})
	}).Description(locales.TunSecretHint()).Error(problem)
}

func (f *tunnelForm) upstreamFields(c *ui.Context, errs map[tunnelField]string, input func(ui.Element) ui.Element) {
	kinds := []config.ProxyKind{"", config.ProxySOCKS5, config.ProxyHTTP}
	labels := []string{locales.TunUpstreamDirect(), locales.TunnelSocks(), locales.TunKindHTTP()}
	selected := labels[max(0, slices.Index(kinds, f.upstreamKind))]
	ui.Field(c, locales.TunUpstream(), func() {
		if ui.Select(c, &selected, labels).Changed() {
			f.upstreamKind = kinds[max(0, slices.Index(labels, selected))]
		}
	})
	if f.upstreamKind != "" {
		ui.Field(c, locales.TunUpstreamAddress(), func() {
			input(ui.TextInput(c.Key("tunnel-upstream-address"), &f.upstreamAddr).Placeholder("127.0.0.1:1080"))
		}).Error(errs[fieldTunnelUpstream])
		ui.Field(c, locales.TunUpstreamUser(), func() {
			input(ui.TextInput(c.Key("tunnel-upstream-user"), &f.upstreamUser))
		})
		if strings.TrimSpace(f.upstreamUser) != "" {
			tunnelPasswordField(c.Key("tunnel-upstream-password"), locales.TunUpstreamPassword(), &f.upstreamPassword, &f.showUpstream, errs[fieldTunnelUpstreamAuth], input)
		}
	}
}

func (f *tunnelForm) advancedFields(c *ui.Context, errs map[tunnelField]string, input func(ui.Element) ui.Element) {
	ui.Field(c, locales.FieldName(), func() { input(ui.TextInput(c, &f.name)) })
	ui.Field(c, locales.TunDescription(), func() { ui.TextArea(c, &f.description).Lines(2, 3) })
	ui.Field(c, locales.TunAllow(), func() {
		ui.TextArea(c, &f.allow).Lines(2, 4).Placeholder("127.0.0.1\n192.168.1.0/24")
	}).Description(locales.TunAllowHint()).Error(errs[fieldTunnelAllow])
	for _, field := range []struct {
		label string
		value *string
		field tunnelField
	}{
		{locales.TunMaxConns(), &f.maxConns, fieldTunnelMax}, {locales.TunRate(), &f.rate, fieldTunnelRate}, {locales.TunIdle(), &f.idle, fieldTunnelIdle},
	} {
		ui.Field(c, field.label, func() { input(ui.TextInput(c, field.value).Width(96)) }).Error(errs[field.field])
	}
	widgets.Muted(c, locales.TunLimitsHint()).FontSize(12)
}

func tunnelTypeCell(c *ui.Context, t *config.Tunnel) {
	label, name := tunnelKindLabel(t.Kind), tunnelName(t)
	icon := "arrow-down-to-line"
	if t.Kind.IsRemote() {
		icon = "arrow-up-from-line"
	}
	if t.Kind.IsProxy() {
		icon = "network"
	}
	tooltip := name + " · " + label
	if t.Description != "" {
		tooltip += "\n" + t.Description
	}
	ui.Row(c).Gap(5).AlignItems(ui.Center).MinWidth(0).Children(func() {
		ui.Icon(c, icons.Get(icon)).FontSize(13).Shrink(0)
		ui.Column(c).MinWidth(0).Grow(1).Tooltip(tooltip).Children(func() {
			ui.Text(c, name).MaxLines(2)
			if t.Name != "" {
				widgets.Muted(c, label).FontSize(11).SingleLine()
			}
		})
	})
}
