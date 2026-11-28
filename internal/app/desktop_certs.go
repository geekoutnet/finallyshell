package app

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"finallyshell/internal/certs"
	"finallyshell/internal/config"
	"finallyshell/internal/icons"
	"finallyshell/internal/widgets"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

// The SSL certificates app: this computer's TLS certificates, from Let's
// Encrypt or self-signed, made with a domain and nothing else. How a
// domain is proved is chosen on its own (desktop_certs_job.go): a DNS
// provider's API when one with credentials hosts it, else the connected
// server over HTTP (certs_http.go), else a TXT record added by hand. The
// certificates and keys stay in this computer's configuration folder
// (internal/certs), never synced; a download writes a ZIP of the formats
// web servers take (desktop_certs_dialogs.go).

const appCerts desktopAppID = "certs"

func init() {
	registerDesktopApp(desktopApp{
		id: appCerts, icon: "shield-check", tint: ui.Hex("#15803d"), name: locales.CertsApp,
		category: desktopCatTools, order: 75, onDesktop: true, single: true,
		size: desktopSize{W: 720, H: 500}, minSize: desktopSize{W: 470, H: 360},
		view: (*App).certsView, opened: (*App).certsOpened, closing: (*App).certsClosing,
	})
}

// certsState is the app's state: its windows show the same certificates,
// one issuance at a time.
type certsState struct {
	list     []certs.Cert
	listErr  string
	loaded   bool
	selected string // a certificate's ID
	// dialog is the open dialog, nil for none.
	dialog *certsDialog
	// job is the issuance under way or just ended, nil for none.
	job *certsJob
	// dns are the credentials of each DNS provider, by its ID, read from
	// the credential store when the app opens; dnsLoaded once they are.
	dns       map[string]map[string]string
	dnsLoaded bool
	dnsAsked  bool

	// Tests replace these; nil for the real ones.
	issue       func(ctx context.Context, staging bool, r certs.Request) (certs.Bundle, error)
	findHTTP    func(ctx context.Context, s *Session, domains []string) certHTTPFound
	nameservers func(ctx context.Context, domain string) ([]string, error)
	now         func() time.Time
}

// certsStore keeps the certificates in the configuration folder.
func certsStore() certs.Store { return certs.Store{Dir: filepath.Join(config.Dir(), "certs")} }

func (st *certsState) clock() time.Time {
	if st.now != nil {
		return st.now()
	}
	return time.Now()
}

func (a *App) certsOpened(w *desktopWindow, _ any) {
	a.certsReload()
	a.certsLoadDNS()
}

// certsClosing asks before closing a window whose issuance is under way:
// closing stops it.
func (a *App) certsClosing(w *desktopWindow) bool {
	if job := a.certs.job; job != nil && job.running() {
		job.cancel()
	}
	a.certs.dialog = nil
	return true
}

// certsReload reads the store's list again.
func (a *App) certsReload() {
	st := &a.certs
	list, err := certsStore().List()
	st.list, st.loaded, st.listErr = list, true, ""
	if err != nil {
		st.listErr = err.Error()
	}
	if st.selected != "" && !slices.ContainsFunc(list, func(c certs.Cert) bool { return c.ID == st.selected }) {
		st.selected = ""
	}
}

// certsLoadDNS reads the DNS providers' credentials from the credential
// store, once.
func (a *App) certsLoadDNS() {
	st := &a.certs
	if st.dnsAsked {
		return
	}
	st.dnsAsked = true
	if st.dns == nil {
		st.dns = map[string]map[string]string{}
	}
	pending := len(certs.DNSProviders)
	for _, p := range certs.DNSProviders {
		id := p.ID
		a.secret(certsDNSKey(id), func(v string, ok bool) {
			if ok {
				if values := certsDNSParse(v); len(values) > 0 {
					st.dns[id] = values
				}
			}
			if pending--; pending == 0 {
				st.dnsLoaded = true
			}
			a.redraw()
		})
	}
}

// certsDNSKey is the credential store's key of a DNS provider's
// credentials, kept together as JSON.
func certsDNSKey(id string) string { return "certs/dns/" + id }

// certsDNSReady says whether provider p has every credential it needs.
func (st *certsState) certsDNSReady(p *certs.DNSProvider) bool {
	return p != nil && certsValuesReady(p, st.dns[p.ID])
}

func (a *App) certsView(c *ui.Context, w *desktopWindow) {
	st := &a.certs
	if !st.loaded {
		a.certsReload()
	}
	ui.Column(c).Fill().MinHeight(0).Background(colors.overviewPage).Children(func() {
		ui.Row(c).FillWidth().Padding(8, 12).Gap(6).AlignItems(ui.Center).Wrap().BorderWidth(0, 0, 1, 0).BorderColor(colors.line).Children(func() {
			ui.Text(c, locales.CertsApp()).Bold().FontSize(15).Grow(1).MinWidth(0)
			busy := st.job != nil && st.job.running()
			if ui.PrimaryButton(c, locales.CertsNew()).Disabled(busy).Clicked() {
				a.certsOpenNew(w.session, false)
			}
			if ui.Button(c, locales.CertsSelfSigned()).Disabled(busy).Clicked() {
				a.certsOpenNew(w.session, true)
			}
			if widgets.IconButton(c, "settings", locales.CertsDNSSettings()).Clicked() {
				a.certsOpenDNS()
			}
		})
		if st.job != nil && st.dialog == nil {
			a.certsJobBanner(c)
		}
		ui.Scroll(c).Grow(1).MinHeight(0).FillWidth().Children(func() {
			ui.Column(c).FillWidth().Padding(10, 12).Gap(8).Children(func() {
				if st.listErr != "" {
					ui.Text(c, st.listErr).TextColor(theme.Danger).Selectable()
				}
				if len(st.list) == 0 {
					a.certsEmpty(c, w)
					return
				}
				for _, cert := range st.list {
					a.certsRow(c.Key(cert.ID), w, cert)
				}
			})
		})
		a.certsDialogView(c, w)
	})
}

// certsEmpty is the list with no certificate yet: what the app does, and
// the button that starts.
func (a *App) certsEmpty(c *ui.Context, w *desktopWindow) {
	ui.Column(c).FillWidth().AlignItems(ui.Center).Padding(40, 20).Gap(12).Children(func() {
		ui.Icon(c, icons.Get("shield-check")).FontSize(42).TextColor(theme.Accent)
		ui.Text(c, locales.CertsEmptyTitle()).Bold().FontSize(15)
		widgets.Muted(c, locales.CertsEmptyNote()).TextAlign(ui.Center).MaxWidth(420)
		busy := a.certs.job != nil && a.certs.job.running()
		if ui.PrimaryButton(c, locales.CertsNew()).Disabled(busy).Clicked() {
			a.certsOpenNew(w.session, false)
		}
	})
}

// certsRow is a certificate of the list: its domains, how they were
// proved, when it expires; selected, its actions.
func (a *App) certsRow(c *ui.Context, w *desktopWindow, cert certs.Cert) {
	st := &a.certs
	now := st.clock()
	selected := st.selected == cert.ID
	busy := st.job != nil && st.job.running()
	card := ui.Column(c).FillWidth().MinWidth(0).Padding(10, 12).Gap(6).Radius(9).Background(colors.card).Border(1, colors.line)
	if selected {
		card.Border(1, theme.Accent)
	}
	card.ContextMenu(func(m *ui.Menu) { a.certsMenu(m, w, cert, busy) })
	card.Children(func() {
		// What selects it: a button of its own, so that the actions below
		// stay buttons of theirs for screen readers.
		head := ui.Column(c).FillWidth().MinWidth(0).Gap(6).Cursor(ui.CursorPointer).Role(ui.RoleButton).
			Label(strings.Join(cert.Domains, ", "))
		if head.Clicked() {
			st.selected = cert.ID
		}
		head.Children(func() {
			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, icons.Get(certsMethodIcon(cert.Method))).FontSize(16).TextColor(certsExpiryColor(cert, now)).Shrink(0)
				ui.Text(c, strings.Join(cert.Domains, ", ")).Bold().SingleLine().Ellipsis("…").Grow(1).MinWidth(0).Tooltip(strings.Join(cert.Domains, "\n"))
				if cert.Staging {
					certsBadge(c, locales.CertsStagingBadge(), theme.Warning)
				}
				if cert.Method == certs.MethodSelfSigned {
					certsBadge(c, locales.CertsSelfSignedBadge(), theme.TextMuted)
				}
			})
			ui.Row(c).FillWidth().Gap(12).Wrap().Children(func() {
				widgets.Muted(c, certsMethodLabel(cert)).FontSize(12)
				widgets.Muted(c, locales.CertsExpires(cert.NotAfter.Local().Format("2006-01-02"))).FontSize(12)
				ui.Text(c, certsDaysText(cert, now)).FontSize(12).TextColor(certsExpiryColor(cert, now))
			})
		})
		if selected {
			ui.Row(c).Gap(6).Wrap().Children(func() {
				if ui.PrimaryButton(c, locales.CertsDownload()).Clicked() {
					a.certsOpenDownload(cert)
				}
				if ui.Button(c, locales.CertsRenew()).Disabled(busy).Clicked() {
					if cert.Method == certs.MethodSelfSigned {
						a.certsRenewSelfSigned(cert)
					} else {
						a.certsRenew(w.session, cert)
					}
				}
				if ui.Button(c, locales.Delete()).Disabled(busy).Clicked() {
					st.dialog = &certsDialog{kind: certsDialogDelete, cert: cert}
				}
			})
		}
	})
}

func (a *App) certsMenu(m *ui.Menu, w *desktopWindow, cert certs.Cert, busy bool) {
	if m.Item(locales.CertsDownload()).Chosen() {
		a.certsOpenDownload(cert)
	}
	if m.Item(locales.CertsRenew()).Disabled(busy).Chosen() {
		if cert.Method == certs.MethodSelfSigned {
			a.certsRenewSelfSigned(cert)
		} else {
			a.certsRenew(w.session, cert)
		}
	}
	m.Separator()
	if m.Item(locales.Delete()).Disabled(busy).Chosen() {
		a.certs.dialog = &certsDialog{kind: certsDialogDelete, cert: cert}
	}
}

func certsBadge(c *ui.Context, text string, color ui.Color) {
	ui.Text(c, text).FontSize(11).TextColor(color).Padding(1, 6).Radius(4).Border(1, color).Shrink(0)
}

// certsExpiryColor is green with a month or more left, yellow under 30
// days, red under 7 and once expired.
func certsExpiryColor(cert certs.Cert, now time.Time) ui.Color {
	switch days := cert.DaysLeft(now); {
	case days < 7:
		return theme.Danger
	case days < 30:
		return theme.Warning
	}
	return theme.Success
}

func certsDaysText(cert certs.Cert, now time.Time) string {
	days := cert.DaysLeft(now)
	if days < 0 {
		return locales.CertsExpired()
	}
	return locales.CertsDaysLeft(days)
}

func certsMethodIcon(m certs.Method) string {
	switch m {
	case certs.MethodSelfSigned:
		return "lock"
	case certs.MethodHTTP:
		return "server"
	}
	return "globe"
}

// certsMethodLabel says how a certificate's domains were proved.
func certsMethodLabel(cert certs.Cert) string {
	switch cert.Method {
	case certs.MethodSelfSigned:
		return locales.CertsMethodSelfSigned()
	case certs.MethodHTTP:
		if cert.Server != "" {
			return locales.CertsMethodHTTPOn(cert.Server)
		}
		return locales.CertsMethodHTTP()
	case certs.MethodDNS:
		if p := certs.FindDNSProvider(cert.Provider); p != nil {
			return locales.CertsMethodDNSOn(p.Name)
		}
		return locales.CertsMethodDNS()
	case certs.MethodDNSManual:
		return locales.CertsMethodManual()
	}
	return string(cert.Method)
}

// certsRenewSelfSigned makes a new self-signed certificate for the same
// names, in place of the old one.
func (a *App) certsRenewSelfSigned(cert certs.Cert) {
	st := &a.certs
	b, err := certs.SelfSigned(cert.Domains, cert.KeyType, st.clock(), certs.SelfSignedValidity)
	if err == nil {
		_, err = certsStore().Save(certs.Cert{Domains: cert.Domains, Method: certs.MethodSelfSigned, KeyType: cert.KeyType}, b, st.clock())
	}
	if err != nil {
		st.listErr = err.Error()
		return
	}
	a.certsReload()
}

// certsDomainsText is domains as one line.
func certsDomainsText(domains []string) string { return strings.Join(domains, ", ") }

// certsParseDomains splits what the user typed; ips allows IP addresses,
// for a self-signed certificate.
func certsParseDomains(text string, ips bool) ([]string, string) {
	domains, err := certs.NormalizeDomains([]string{text}, ips)
	if err == nil {
		return domains, ""
	}
	var de *certs.DomainError
	switch {
	case errors.Is(err, certs.ErrNoDomain):
		return nil, locales.CertsNoDomain()
	case errors.As(err, &de):
		if !ips && net.ParseIP(strings.Trim(de.Name, "[]")) != nil {
			return nil, locales.CertsIPNeedsSelfSigned(de.Name)
		}
		return nil, locales.CertsBadDomain(de.Name)
	}
	return nil, err.Error()
}
