package app

import (
	"fmt"
	"strconv"
	"strings"

	"finallyshell/internal/config"
	"finallyshell/internal/termenc"
	"finallyshell/internal/widgets"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

// A host's connection info is everything the app knows of how to reach
// it, as lines the user can read and copy: its name and folder, address,
// port and user, how it logs in with the password or the key's
// passphrase, its proxy and jump hosts, its terminal, and the ssh command
// and URL that reach it. "Copy connection info", in the menus of the
// session list and of the connection trees, copies it all; "View
// connection info…" opens a dialog with a copy button on each line. The
// passwords come from a.passwords, or the credential store, read first
// (secrets.go): the dialog shows them masked until asked.

// connField is a line of a host's connection info.
type connField struct {
	label string
	value string
	// secret is a password or passphrase: masked in the dialog. missing
	// is one that is not saved, value then saying so; reading one the
	// store is being asked for.
	secret, missing, reading bool
}

// connInfoDialog is the dialog showing a host's connection info.
type connInfoDialog struct {
	host    *config.Host
	reading int             // the secrets being read from the store
	shown   map[string]bool // the secrets shown, by label
}

// connSecretNeeds are the secrets of h's connection info: its password or
// key's passphrase, and its proxy's password.
func connSecretNeeds(h *config.Host) []*secretNeed {
	if h.IsRDP() {
		return rdpSecrets(h)
	}
	if h.Kind == config.KindTelnet || h.Kind == config.KindSerial {
		return nil
	}
	var needs []*secretNeed
	if h.Auth != config.AuthAgent {
		needs = append(needs, loginNeed(h, h))
	}
	if usesProxyPassword(h) {
		needs = append(needs, proxyNeed(h, h))
	}
	return needs
}

// readConnSecrets reads the secrets of h's connection info the app does
// not know yet from the credential store, and calls then once they are
// read. It returns the number of reads started; none calls nothing.
func (a *App) readConnSecrets(h *config.Host, then func()) int {
	n := 0
	for _, need := range connSecretNeeds(h) {
		if a.readSecret(need, then) {
			n++
		}
	}
	return n
}

// connInfoFields are h's connection info, as the app knows it now, empty
// fields left out; reading says a secret not known yet is being read.
func (a *App) connInfoFields(h *config.Host, reading bool) []connField {
	var out []connField
	add := func(label, value string) {
		if value != "" {
			out = append(out, connField{label: label, value: value})
		}
	}
	secret := func(label string, need *secretNeed, none string) {
		f := connField{label: label, secret: true}
		v, known := a.knownSecret(need)
		switch {
		case known && v != "":
			f.value = v
		case reading && !known:
			f.value, f.reading = locales.ConnInfoReading(), true
		default:
			f.value, f.missing = none, true
		}
		out = append(out, f)
	}
	add(locales.FieldName(), h.Name)
	add(locales.ConnInfoFolder(), a.folderLabel(h.Folder))
	if h.IsRDP() {
		a.rdpConnInfoFields(h, add, secret)
		add(locales.FieldNotes(), strings.TrimSpace(h.Notes))
		return out
	}
	if h.Kind == config.KindTelnet || h.Kind == config.KindSerial {
		add(locales.FieldHostKind(), kindLook(h).label)
		if h.Kind == config.KindTelnet {
			add(locales.FieldHost(), h.Addr)
			add(locales.FieldPort(), strconv.Itoa(h.PortOr22()))
			add(locales.FieldUser(), h.User)
		} else {
			add(locales.SerialDevice(), h.Addr)
			if h.Serial != nil && h.Serial.Baud != 0 {
				add(locales.SerialBaud(), strconv.Itoa(h.Serial.Baud))
			}
		}
		add(locales.FieldEncoding(), termenc.Label(a.settings.HostEncoding(h)))
		add(locales.FieldNotes(), strings.TrimSpace(h.Notes))
		return out
	}
	add(locales.FieldHost(), h.Addr)
	add(locales.FieldPort(), strconv.Itoa(h.PortOr22()))
	add(locales.FieldUser(), h.User)
	switch h.Auth {
	case config.AuthKey:
		add(locales.SectionAuth(), locales.AuthKey())
		add(locales.FieldKeyFile(), h.KeyPath)
		secret(locales.FieldPassphrase(), loginNeed(h, h), locales.ConnInfoNoPassphrase())
	case config.AuthAgent:
		add(locales.SectionAuth(), locales.AuthAgent())
	default:
		add(locales.SectionAuth(), locales.AuthPassword())
		secret(locales.FieldPassword(), loginNeed(h, h), locales.ConnInfoNoPassword())
	}
	if p := config.ProxyOf(h); p != nil {
		switch p.Kind {
		case config.ProxySOCKS5, config.ProxyHTTP:
			kind := "SOCKS5"
			if p.Kind == config.ProxyHTTP {
				kind = "HTTP"
			}
			add(locales.ConnInfoProxyType(), kind)
			add(locales.ConnInfoProxyAddr(), hostPort(p.Addr, p.PortOrDefault()))
			add(locales.ConnInfoProxyUser(), p.User)
			if usesProxyPassword(h) {
				secret(locales.ProxyPasswordTitle(), proxyNeed(h, h), locales.ConnInfoNoPassword())
			}
		case config.ProxyJump:
			add(locales.FieldJumpHost(), a.jumpChain(h))
		}
	}
	if h.TermType != "" {
		add(locales.FieldTermType(), h.TermType)
	}
	if h.Encoding != config.EncodingDefault {
		add(locales.FieldEncoding(), termenc.Label(h.Encoding))
	}
	add(locales.FieldNotes(), strings.TrimSpace(h.Notes))
	add(locales.ConnInfoSSHCommand(), a.sshCommand(h))
	add(locales.ConnInfoURL(), sshURL(h))
	return out
}

// jumpHostsInOrder are h's jump hosts in the order ssh -J takes them: the
// one connected to first first. nil for none, or a chain that is broken.
func (a *App) jumpHostsInOrder(h *config.Host) []*config.Host {
	jumps, err := a.hosts.JumpHosts(h)
	if err != nil {
		return nil
	}
	out := make([]*config.Host, len(jumps))
	for i, j := range jumps {
		out[len(jumps)-1-i] = j
	}
	return out
}

// jumpChain is h's jump hosts, by name and target, the one connected to
// first first: "bastion (root@1.2.3.4) → inner (admin@10.0.0.2)".
func (a *App) jumpChain(h *config.Host) string {
	var names []string
	for _, j := range a.jumpHostsInOrder(h) {
		t := j.Target()
		if j.Name != "" {
			t = j.Name + " (" + t + ")"
		}
		names = append(names, t)
	}
	return strings.Join(names, " → ")
}

// hostPort is host:port, the host in brackets when it is an IPv6 address.
func hostPort(host string, port int) string {
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return host + ":" + strconv.Itoa(port)
}

// shellQuote quotes s for a POSIX shell when it needs it.
func shellQuote(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\n'\"\\$`&|;<>()*?[]#~!{}") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// sshCommand is the OpenSSH command that connects to h as the app does:
// ssh -p 22 -i key -J jump user@host.
func (a *App) sshCommand(h *config.Host) string {
	parts := []string{"ssh", "-p", strconv.Itoa(h.PortOr22())}
	if h.Auth == config.AuthKey && h.KeyPath != "" {
		parts = append(parts, "-i", shellQuote(h.KeyPath))
	}
	if jumps := a.jumpHostsInOrder(h); len(jumps) > 0 {
		var hops []string
		for _, j := range jumps {
			hop := j.User + "@" + hostPort(j.Addr, j.PortOr22())
			if j.User == "" {
				hop = hostPort(j.Addr, j.PortOr22())
			}
			hops = append(hops, hop)
		}
		parts = append(parts, "-J", shellQuote(strings.Join(hops, ",")))
	}
	target := h.Addr
	if h.User != "" {
		target = h.User + "@" + h.Addr
	}
	return strings.Join(append(parts, shellQuote(target)), " ")
}

// sshURL is h as an ssh URL: ssh://user@host:port.
func sshURL(h *config.Host) string {
	u := "ssh://"
	if h.User != "" {
		u += h.User + "@"
	}
	return u + hostPort(h.Addr, h.PortOr22())
}

// connInfoText is fields as "label: value" lines, what Copy copies:
// secrets as they are, those missing or being read left out.
func connInfoText(fields []connField) string {
	var b strings.Builder
	for _, f := range fields {
		if f.missing || f.reading {
			continue
		}
		fmt.Fprintf(&b, "%s: %s\n", f.label, f.value)
	}
	return b.String()
}

// copyConnInfo copies h's connection info, once the secrets it has are
// read: with c at once when the app knows them, else in a frame of the
// main window after they are read (writePendingCopy).
func (a *App) copyConnInfo(c *ui.Context, h *config.Host) {
	toast := locales.CopiedText(locales.ConnInfo())
	pending := 0
	pending = a.readConnSecrets(h, func() {
		if pending--; pending == 0 {
			a.pendingCopy = &clipboardCopy{text: connInfoText(a.connInfoFields(h, false)), toast: toast}
			a.redraw()
		}
	})
	if pending == 0 {
		c.WriteClipboard(connInfoText(a.connInfoFields(h, false)))
		c.Toast(toast)
	}
}

// clipboardCopy is text to put on the clipboard in the next frame, and
// the toast that says so.
type clipboardCopy struct{ text, toast string }

// writePendingCopy puts on the clipboard what a goroutine's work left
// to copy.
func (a *App) writePendingCopy(c *ui.Context) {
	if p := a.pendingCopy; p != nil {
		a.pendingCopy = nil
		c.WriteClipboard(p.text)
		c.Toast(p.toast)
	}
}

// openConnInfo opens the connection info dialog of h in the window of
// the tree m, reading the secrets it shows first.
func (a *App) openConnInfo(m *managerState, h *config.Host) {
	d := &connInfoDialog{host: h, shown: map[string]bool{}}
	m.info = d
	d.reading = a.readConnSecrets(h, func() {
		if d.reading > 0 {
			d.reading--
		}
	})
}

// connInfoMenuItems are the menu items that copy and show h's connection
// info, the dialog in the window of the tree m; do runs what changes
// the state once the menu is built.
func (a *App) connInfoMenuItems(c *ui.Context, menu *ui.Menu, m *managerState, h *config.Host, do func(func())) {
	if menu.Item(locales.CopyConnInfo()).Chosen() {
		a.copyConnInfo(c, h)
	}
	if menu.Item(locales.ViewConnInfo()).Chosen() {
		do(func() { a.openConnInfo(m, h) })
	}
}

// connInfoView is the connection info dialog of the tree m, in its
// window.
func (a *App) connInfoView(c *ui.Context, m *managerState) {
	d := m.info
	if d == nil {
		return
	}
	open := true
	fields := a.connInfoFields(d.host, d.reading > 0)
	ui.Modal(c, &open, func() {
		ui.Column(c).Width(560).Gap(12).Children(func() {
			ui.Column(c).Gap(2).Children(func() {
				ui.Text(c, locales.ConnInfo()).FontSize(16).Bold()
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					hostIcon(c, d.host, false, theme.Text)
					widgets.Muted(c, d.host.Title()).SingleLine().Shrink(1).MinWidth(0)
					kindPill(c, d.host, false, theme.Text)
				})
			})
			ui.Scroll(c).MaxHeight(520).FillWidth().Children(func() {
				ui.Column(c).FillWidth().Border(1, colors.line).Radius(6).Children(func() {
					for i, f := range fields {
						a.connFieldRow(c.Key(f.label), d, f, i > 0)
					}
				})
			})
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				if iconTextButton(c, "copy", locales.CopyAll()).Clicked() {
					c.WriteClipboard(connInfoText(fields))
					c.Toast(locales.CopiedText(locales.ConnInfo()))
				}
				ui.Spacer(c)
				if ui.PrimaryButton(c, locales.Close()).Clicked() {
					open = false
				}
			})
		})
	})
	if !open && m.info == d {
		m.info = nil
	}
}

// connFieldRow is a line of the dialog: its label, its value, a secret
// masked unless shown, with the button that shows it, and Copy.
func (a *App) connFieldRow(c *ui.Context, d *connInfoDialog, f connField, line bool) {
	row := ui.Row(c).Gap(10).Padding(2, 6, 2, 10).AlignItems(ui.Center).MinHeight(28)
	if line {
		row.BorderWidth(1, 0, 0, 0).BorderColor(colors.line)
	}
	row.Children(func() {
		widgets.Muted(c, f.label).Width(130).Shrink(0).SingleLine()
		shown := d.shown[f.label]
		value := f.value
		switch {
		case f.missing || f.reading:
			widgets.Muted(c, value).Grow(1).MinWidth(0)
		case f.secret && !shown:
			ui.Text(c, "••••••••").Grow(1).MinWidth(0).Label(f.label)
		default:
			ui.Text(c, value).Grow(1).MinWidth(0).Selectable()
		}
		if f.secret {
			icon, label := "eye", locales.ShowSecret()
			if shown {
				icon, label = "eye-off", locales.HideSecret()
			}
			eye := widgets.IconButton(c, icon, label+" "+f.label)
			if f.missing || f.reading {
				eye.Invisible()
			} else if eye.Clicked() {
				d.shown[f.label] = !shown
			}
		}
		cp := widgets.IconButton(c, "copy", locales.Copy()+" "+f.label)
		if f.missing || f.reading {
			cp.Invisible()
		} else if cp.Clicked() {
			c.WriteClipboard(f.value)
			c.Toast(locales.CopiedText(f.label))
		}
	})
}
