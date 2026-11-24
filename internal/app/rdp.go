package app

// Remote Desktop hosts (config.KindRDP): the app does not speak RDP, it
// opens the system's client on the host (internal/rdp), with the
// password it knows. Opening one, wherever a host is opened
// (openSessionIn), reads its passwords from the credential store, asks
// for its own as an SSH host's when the store has none, and starts the
// client on a goroutine; no session is made. The editor's pages for it
// are in editor_rdp.go.

import (
	"errors"
	"runtime"
	"strconv"

	"finallyshell/internal/config"
	"finallyshell/internal/rdp"
	"finallyshell/internal/widgets"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

// rdpStarter starts the Remote Desktop client: rdp.Start, which tests
// replace. wait, which the app calls on a goroutine, returns once the
// client is done.
type rdpStarter func(rdp.Request) (wait func() error, err error)

// gatewaySecretKey is where the password of the Remote Desktop Gateway
// of the host with the ID is kept among a.passwords.
func gatewaySecretKey(hostID string) string { return "rdp-gateway/" + hostID }

// usesGatewayPassword says whether opening h, a Remote Desktop host,
// takes its gateway's password: a gateway with a user of its own that
// logs in with a password, or lets the user choose.
func usesGatewayPassword(h *config.Host) bool {
	r := config.RDPOf(h)
	return h.IsRDP() && r.UsesGateway() && !r.GatewaySameLogin && r.GatewayUser != "" && r.GatewayAuth != config.GatewayAuthSmartCard
}

// gatewayNeed is the password of h's gateway. It is read from the
// store, but never asked for: the client asks for it when it needs it.
func gatewayNeed(h *config.Host) *secretNeed {
	r := config.RDPOf(h)
	return &secretNeed{key: gatewaySecretKey(h.ID), gateway: true, title: locales.RdpGatewayPasswordTitle,
		who: r.GatewayUser + "@" + r.GatewayHost, host: h, store: hostSecretKey(h.ID, secretGatewayPassword)}
}

// rdpSecrets are the secrets opening h, a Remote Desktop host, takes: its
// password, when it has a user, and its gateway's.
func rdpSecrets(h *config.Host) []*secretNeed {
	var needs []*secretNeed
	if h.User != "" {
		needs = append(needs, loginNeed(h, h))
	}
	if usesGatewayPassword(h) {
		needs = append(needs, gatewayNeed(h))
	}
	return needs
}

// openRDP opens h, a Remote Desktop host, with the system's client: once
// the passwords the app does not know are read from the store, and its
// own asked for when the store has none. An empty one starts the client
// all the same, which asks for it.
func (a *App) openRDP(h *config.Host) {
	if a.readChainSecrets(h, func() { a.openRDP(h) }) {
		return
	}
	if need := a.missingSecret(h); need != nil {
		a.askSecret(need, func() { a.openRDP(h) })
		return
	}
	req := rdp.Request{Host: h.Clone(), Password: a.passwords[h.ID]}
	if usesGatewayPassword(h) {
		req.GatewayPassword = a.passwords[gatewaySecretKey(h.ID)]
	}
	start := a.rdpStart
	if start == nil {
		start = rdp.Start
	}
	wait, err := start(req)
	if err != nil {
		a.rdpFailed(h, err)
		return
	}
	// No way to know it logged in: the client started is as far as the
	// app sees, so what the prompt was given is kept now.
	a.loggedIn(h)
	a.noteConnected(h)
	note := locales.RdpOpening()
	if runtime.GOOS == "darwin" {
		note = locales.RdpOpeningNoPassword()
	}
	a.notify(note)
	title := h.Title()
	go func() {
		err := wait()
		var exit *rdp.ExitError
		if errors.As(err, &exit) {
			a.post(func() { a.rdpErr = &rdpError{host: title, err: err} })
		}
	}()
}

// rdpFailed says that the client could not be started on h, and forgets
// what the prompt was given, which nothing tried.
func (a *App) rdpFailed(h *config.Host, err error) {
	for _, n := range a.chainSecrets(h) {
		delete(a.secrets.untried, n.key)
	}
	a.rdpErr = &rdpError{host: h.Title(), err: err}
	a.redraw()
}

// rdpError is the dialog saying the Remote Desktop client could not open
// a host.
type rdpError struct {
	host string
	err  error
}

// describeRDPError says err, from starting the client or from the client
// failing, in the language shown.
func describeRDPError(err error) string {
	var exit *rdp.ExitError
	switch {
	case errors.Is(err, rdp.ErrNoClient):
		switch runtime.GOOS {
		case "windows":
			return locales.RdpNoMstsc()
		case "darwin":
			return locales.RdpNoMacClient()
		}
		return locales.RdpNoFreeRDP()
	case errors.As(err, &exit):
		if exit.Output != "" {
			return locales.RdpClientFailedOutput(exit.Code, exit.Output)
		}
		return locales.RdpClientFailed(exit.Code)
	}
	return err.Error()
}

// rdpErrorView is the dialog of a.rdpErr.
func (a *App) rdpErrorView(c *ui.Context) {
	e := a.rdpErr
	if e == nil {
		return
	}
	open := true
	ui.Modal(c, &open, func() {
		ui.Column(c).Width(440).Gap(12).Children(func() {
			ui.Text(c, locales.RdpCannotOpen()).FontSize(16).Bold()
			widgets.Muted(c, e.host).SingleLine()
			ui.Scroll(c).MaxHeight(240).FillWidth().Children(func() {
				ui.Text(c, describeRDPError(e.err)).Selectable()
			})
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.PrimaryButton(c, locales.Ok()).Clicked() {
					open = false
				}
			})
		})
	})
	if !open && a.rdpErr == e {
		a.rdpErr = nil
	}
}

// rdpConnInfoFields are the lines of a Remote Desktop host's connection
// info after its name and folder (connInfoFields): where it connects,
// who logs in, its password, and its gateway.
func (a *App) rdpConnInfoFields(h *config.Host, add func(label, value string), secret func(label string, need *secretNeed, none string)) {
	r := config.RDPOf(h)
	add(locales.FieldHostKind(), locales.KindRDP())
	add(locales.FieldHost(), h.Addr)
	add(locales.FieldPort(), strconv.Itoa(h.PortOr22()))
	add(locales.FieldUser(), h.User)
	add(locales.FieldDomain(), r.Domain)
	if h.User != "" {
		secret(locales.FieldPassword(), loginNeed(h, h), locales.ConnInfoNoPassword())
	}
	if r.UsesGateway() {
		add(locales.RdpGatewayHost(), r.GatewayHost)
		if !r.GatewaySameLogin {
			add(locales.RdpGatewayUser(), r.GatewayUser)
		}
		if usesGatewayPassword(h) {
			secret(locales.RdpGatewayPasswordTitle(), gatewayNeed(h), locales.ConnInfoNoPassword())
		}
	}
	if r.Admin {
		add(locales.RdpAdmin(), locales.Yes())
	}
	add(locales.ConnInfoRDPAddress(), hostPort(h.Addr, h.PortOr22()))
}
