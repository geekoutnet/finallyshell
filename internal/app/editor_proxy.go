package app

// The connection editor's Proxy page: connect directly, through a SOCKS5
// or HTTP proxy, or through another saved host, a jump host. Connecting
// through them is in proxy.go and internal/sshx/proxy.go.

import (
	"errors"
	"strconv"
	"strings"

	"finallyshell/internal/config"
	"finallyshell/internal/icons"
	"finallyshell/internal/widgets"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

// The Proxy page's fields that can be wrong.
const (
	fieldProxyAddr editorField = 100 + iota
	fieldProxyPort
	fieldJumpHost
	fieldLibraryProxy
)

// proxyLibrary is the Proxy page's choice of a proxy of the library
// (proxymgr.go): the host saves a copy of it, naming it.
const proxyLibrary config.ProxyKind = "\x00library"

// proxyFieldOrder are the Proxy page's fields, in the order they show.
var proxyFieldOrder = []editorField{fieldLibraryProxy, fieldProxyAddr, fieldProxyPort, fieldJumpHost}

// proxyFields are the Proxy page's fields, as typed. Those of a kind not
// chosen are kept while the editor is open, and not saved.
type proxyFields struct {
	kind                       config.ProxyKind
	addr, port, user, password string
	jump                       string // the jump host's ID
	lib                        string // the library's proxy's ID

	// saved is the host's proxy as the editor opened, whose JSON keys
	// this build does not know saving keeps; nil for none.
	saved *config.ProxyConfig
	// hosts are the saved hosts, which the jump host is one of.
	hosts *config.Store
}

// proxyFieldsOf are the Proxy page's fields for h, nil for a new host.
func (a *App) proxyFieldsOf(h *config.Host) proxyFields {
	pf := proxyFields{hosts: a.hosts}
	if h == nil || h.Proxy == nil {
		return pf
	}
	p := h.Proxy
	pf.saved = p
	pf.kind, pf.addr, pf.user, pf.jump = p.Kind, p.Addr, p.User, p.JumpHost
	if lib := a.hosts.LinkedProxy(h); lib != nil {
		pf.kind, pf.lib = proxyLibrary, lib.ID
	}
	if port := p.PortOrDefault(); port != 0 {
		pf.port = strconv.Itoa(port)
	}
	pf.password = a.proxySecrets()[proxySecretKey(h.ID)]
	return pf
}

// setKind chooses the kind, with its usual port while the port is empty
// or the last kind's.
func (pf *proxyFields) setKind(k config.ProxyKind) {
	if p := strings.TrimSpace(pf.port); p == "" || p == strconv.Itoa(config.DefaultProxyPort(pf.kind)) {
		pf.port = ""
		if port := config.DefaultProxyPort(k); port != 0 {
			pf.port = strconv.Itoa(port)
		}
	}
	pf.kind = k
}

// proxyProblems adds to errs what is wrong with the Proxy page.
func (ed *connEditor) proxyProblems(errs map[editorField]string) {
	pf := &ed.proxy
	switch pf.kind {
	case proxyLibrary:
		if pf.hosts.Proxy(pf.lib) == nil {
			errs[fieldLibraryProxy] = locales.ProxyLibraryRequired()
		}
	case config.ProxySOCKS5, config.ProxySOCKS4, config.ProxyHTTP, config.ProxyHTTPS:
		if strings.TrimSpace(pf.addr) == "" {
			errs[fieldProxyAddr] = locales.ErrHostRequired()
		}
		if port, err := strconv.Atoi(strings.TrimSpace(pf.port)); err != nil || port < 1 || port > 65535 {
			errs[fieldProxyPort] = locales.ErrPortInvalid()
		}
	case config.ProxyJump:
		j := pf.hosts.Host(pf.jump)
		if j == nil || j.Deleted() || j.ID == ed.id {
			errs[fieldJumpHost] = locales.ErrJumpHostRequired()
			return
		}
		// The chain from here, as this host would be saved.
		self := &config.Host{ID: ed.id, Name: strings.TrimSpace(ed.name), Addr: strings.TrimSpace(ed.addr),
			Proxy: &config.ProxyConfig{Kind: config.ProxyJump, JumpHost: pf.jump}}
		if self.Title() == "" {
			self.Name = locales.NewConnection()
		}
		_, err := pf.hosts.JumpHosts(self)
		var loop *config.JumpLoopError
		var missing *config.JumpError
		switch {
		case errors.As(err, &loop):
			errs[fieldJumpHost] = locales.ErrJumpLoop(strings.Join(loop.Names, " → "))
		case errors.As(err, &missing):
			errs[fieldJumpHost] = locales.ErrJumpHostMissing(missing.From)
		}
	}
}

// saveProxy saves the Proxy page into h, and keeps the proxy's password
// as the host's: in proxySecrets, not in hosts.json. A proxy with a user
// and no password asks for it as the host connects.
func (a *App) saveProxy(h *config.Host, pf *proxyFields) {
	key := proxySecretKey(h.ID)
	delete(a.proxySecrets(), key)
	if pf.kind == config.ProxyNone {
		h.Proxy = nil
		return
	}
	if pf.kind == proxyLibrary {
		if lib := a.hosts.Proxy(pf.lib); lib != nil {
			a.useLibraryProxy(h, lib)
			pf.saved = h.Proxy
		}
		return
	}
	// A new value, not the saved one changed: a session connecting
	// reads its copy of the host's.
	var p config.ProxyConfig
	if pf.saved != nil {
		p = *pf.saved
	}
	// Typed here: the host's own proxy, no longer the library's.
	p.Kind, p.Addr, p.Port, p.User, p.JumpHost, p.ProxyID = pf.kind, "", 0, "", "", ""
	switch pf.kind {
	case config.ProxyJump:
		p.JumpHost = pf.jump
	default:
		p.Addr, p.User = strings.TrimSpace(pf.addr), strings.TrimSpace(pf.user)
		p.Port, _ = strconv.Atoi(strings.TrimSpace(pf.port))
		if config.TakesPassword(p.Kind) && p.User != "" && pf.password != "" {
			a.proxySecrets()[key] = pf.password
		}
	}
	h.Proxy = &p
	pf.saved = &p
}

// jumpChoice is a host the Proxy page offers as the jump host.
type jumpChoice struct {
	host  *config.Host
	label string
}

// jumpChoices are the hosts that can be the jump host of the host with
// the ID: the others not deleted, in order, but Remote Desktop hosts,
// each labeled with its name and user@host, told apart when two read
// the same.
func jumpChoices(hosts *config.Store, id string) []jumpChoice {
	var out []jumpChoice
	seen := map[string]int{}
	for _, h := range hosts.Live() {
		if h.ID == id || !h.IsSSH() {
			continue
		}
		label := h.Target()
		if h.Name != "" {
			label = h.Name + " (" + label + ")"
		}
		if seen[label]++; seen[label] > 1 {
			label += " #" + strconv.Itoa(seen[label])
		}
		out = append(out, jumpChoice{h, label})
	}
	return out
}

// proxyKinds are the kinds the Proxy page offers, in order, with their
// labels.
func proxyKinds() ([]config.ProxyKind, []string) {
	return []config.ProxyKind{config.ProxyNone, proxyLibrary, config.ProxySOCKS5, config.ProxyHTTP, config.ProxyHTTPS, config.ProxySOCKS4, config.ProxyJump},
		[]string{locales.ProxyNone(), locales.ProxyFromLibrary(), "SOCKS5", "HTTP", "HTTPS", "SOCKS4 / 4a", locales.ProxyJumpHost()}
}

// proxyPage is the editor's Proxy page. input marks a text input whose
// Enter is OK.
func (a *App) proxyPage(c *ui.Context, ed *connEditor, errs map[editorField]string, input func(ui.Element) ui.Element) {
	pf := &ed.proxy
	ui.Form(c, func() {
		ui.Field(c, locales.FieldProxyType(), func() {
			kinds, labels := proxyKinds()
			chosen := labels[0]
			for i, k := range kinds {
				if k == pf.kind {
					chosen = labels[i]
				}
			}
			if ui.Select(c, &chosen, labels).Width(220).Changed() {
				for i, l := range labels {
					if l == chosen {
						pf.setKind(kinds[i])
					}
				}
			}
		}).Description(proxyKindHint(pf.kind))
		switch pf.kind {
		case proxyLibrary:
			a.proxyLibraryField(c, pf, errs[fieldLibraryProxy])
		case config.ProxySOCKS5, config.ProxySOCKS4, config.ProxyHTTP, config.ProxyHTTPS:
			ui.Field(c, locales.FieldHost(), func() {
				input(ui.TextInput(c, &pf.addr).Placeholder(locales.FieldHostPlaceholder()))
			}).Error(errs[fieldProxyAddr])
			ui.Field(c, locales.FieldPort(), func() {
				input(ui.TextInput(c, &pf.port).Width(90))
			}).Error(errs[fieldProxyPort])
			ui.Field(c, locales.FieldUser(), func() {
				input(ui.TextInput(c, &pf.user).Placeholder(locales.ProxyUserPlaceholder()))
			})
			if config.TakesPassword(pf.kind) && strings.TrimSpace(pf.user) != "" {
				ui.Field(c, locales.FieldPassword(), func() {
					input(ui.TextInput(c, &pf.password).Password())
				}).Description(a.rememberNote(ed.remember))
			}
			ui.Field(c, "", func() {
				if ui.Button(c, locales.ProxySaveToLibrary()).Clicked() {
					a.saveTypedProxyToLibrary(pf)
				}
			}).Description(locales.ProxySaveToLibraryNote())
		case config.ProxyJump:
			choices := jumpChoices(pf.hosts, ed.id)
			if len(choices) == 0 {
				ui.Field(c, locales.FieldJumpHost(), func() {
					widgets.Muted(c, locales.NoJumpHosts())
				}).Error(errs[fieldJumpHost])
				break
			}
			ui.Field(c, locales.FieldJumpHost(), func() {
				jumpHostSelect(c, &pf.jump, choices)
			}).Error(errs[fieldJumpHost]).Description(locales.JumpHostNote())
		}
	})
}

func jumpHostSelect(c *ui.Context, selected *string, choices []jumpChoice) {
	parts := ui.SelectBase(c, selected)
	trigger := parts.Trigger.Width(320).Shrink(1).MinWidth(0).Gap(6).Padding(4.5, 10.5).AlignItems(ui.Center).
		Radius(theme.Radius).Border(1, theme.Border).Background(theme.Surface).Label(locales.FieldJumpHost()).Value("")
	if trigger.Hovered() {
		trigger.Background(theme.SurfaceHover)
	}
	trigger.Children(func() {
		found := false
		for _, choice := range choices {
			if choice.host.ID == *selected {
				found = true
				trigger.Value(choice.label)
				hostIcon(c, choice.host, false, theme.Text)
				ui.Text(c, choice.label).SingleLine().Grow(1).MinWidth(0)
				kindPill(c, choice.host, false, theme.Text)
				break
			}
		}
		if !found {
			ui.Spacer(c).MinHeight(16)
		}
		ui.Icon(c, icons.Get("chevron-down")).FontSize(12).TextColor(theme.TextMuted).Shrink(0)
	})
	parts.Popup(func(panel ui.Element) {
		panel.Margin(3, 0, 0, 0).Padding(3).Radius(theme.Radius+2).Background(theme.Background).
			Border(1, theme.Border).Shadow(0, 6, 20, 0, ui.RGBA(0, 0, 0, 0.18))
		ui.Scroll(c).MaxHeight(240).Children(func() {
			for _, choice := range choices {
				item := parts.Item(choice.host.ID).Label(choice.label).Gap(6).AlignItems(ui.Center).Padding(4.5, 7.5).Radius(theme.Radius)
				color := theme.Text
				if item.Highlighted() {
					item.Background(theme.Accent)
					color = theme.AccentText
				} else if choice.host.ID == *selected {
					item.Background(theme.Surface)
				}
				item.Children(func() {
					hostIcon(c, choice.host, item.Highlighted(), color)
					ui.Text(c, choice.label).SingleLine().Grow(1).MinWidth(0).TextColor(color)
					kindPill(c, choice.host, item.Highlighted(), color)
				})
			}
		})
	})
}

// proxyKindHint says what the kind of proxy does.
func proxyKindHint(k config.ProxyKind) string {
	switch k {
	case proxyLibrary:
		return locales.ProxyLibraryHint()
	case config.ProxySOCKS5:
		return locales.ProxySOCKS5Hint()
	case config.ProxyHTTP:
		return locales.ProxyHTTPHint()
	case config.ProxyHTTPS:
		return locales.ProxyMgrKindHTTPS()
	case config.ProxySOCKS4:
		return locales.ProxyMgrKindSOCKS4()
	case config.ProxyJump:
		return locales.ProxyJumpHint()
	}
	return locales.ProxyNoneHint()
}

// proxyLibraryField chooses the library's proxy the host connects
// through, with its last test, and the buttons that test it, add one and
// open the proxy manager.
func (a *App) proxyLibraryField(c *ui.Context, pf *proxyFields, problem string) {
	ui.Field(c, locales.ProxyFromLibrary(), func() {
		ui.Column(c).Gap(8).FillWidth().Children(func() {
			if len(a.hosts.Proxies) > 0 {
				var labels []string
				chosen := ""
				for _, p := range a.hosts.Proxies {
					label := p.Name + " — " + proxyKindLabel(p.Kind) + " " + proxyAddress(p)
					labels = append(labels, label)
					if p.ID == pf.lib {
						chosen = label
					}
				}
				if ui.Select(c, &chosen, labels).Width(360).Shrink(1).MinWidth(0).Label(locales.ProxyFromLibrary()).Changed() {
					for i, l := range labels {
						if l == chosen {
							pf.lib = a.hosts.Proxies[i].ID
						}
					}
				}
			} else {
				widgets.Muted(c, locales.ProxyLibraryEmpty())
			}
			if p := a.hosts.Proxy(pf.lib); p != nil {
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					proxyTestSummary(c, a.proxyTests[p.ID])
				})
			}
			ui.Row(c).Gap(6).Wrap().Children(func() {
				if p := a.hosts.Proxy(pf.lib); p != nil && ui.Button(c, locales.ProxyMgrTest()).Disabled(a.proxyTesting(p.ID)).Clicked() {
					a.testLibraryProxy(p)
				}
				if ui.Button(c, locales.ProxyMgrNew()).Clicked() {
					a.editProxy(nil, func(p *config.Proxy) { pf.lib = p.ID })
				}
				if ui.Button(c, locales.ProxyManage()).Clicked() {
					a.openProxyManager()
				}
			})
		})
	}).Error(problem).Description(locales.ProxyLibraryNote())
}

// saveTypedProxyToLibrary opens the proxy manager's form with the proxy
// typed on the Proxy page; once saved, the host uses it from the library.
func (a *App) saveTypedProxyToLibrary(pf *proxyFields) {
	port, _ := strconv.Atoi(strings.TrimSpace(pf.port))
	p := &config.Proxy{Kind: pf.kind, Addr: strings.TrimSpace(pf.addr), Port: port, User: strings.TrimSpace(pf.user)}
	p.Name = p.Addr
	a.editProxy(p, func(saved *config.Proxy) { pf.kind, pf.lib = proxyLibrary, saved.ID })
	a.proxyMgr.edit.id, a.proxyMgr.edit.original = "", nil
	a.proxyMgr.edit.password = pf.password
}
