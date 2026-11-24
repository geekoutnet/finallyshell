package app

import (
	"context"
	"errors"
	"io"
	"net"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"finallyshell/internal/config"
	"finallyshell/internal/serial"
	"finallyshell/internal/telnet"
	"finallyshell/internal/termenc"
	"finallyshell/internal/widgets"
	"finallyshell/locales"

	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"
)

func (s *Session) isDeviceSession() bool {
	return s != nil && (s.deviceKind != config.KindSSH || s.Host != nil && (s.Host.Kind == config.KindTelnet || s.Host.Kind == config.KindSerial))
}

func deviceUnavailableText(s *Session) string {
	if s.deviceKind == config.KindSerial || s.deviceKind == config.KindSSH && s.Host.Kind == config.KindSerial {
		return locales.SerialUnavailable()
	}
	return locales.TelnetUnavailable()
}

func deviceUnavailable(c *ui.Context, s *Session) {
	ui.Column(c.Key("device-unavailable")).Grow(1).MinWidth(0).Children(func() {
		widgets.Muted(c, deviceUnavailableText(s)).Padding(12)
	})
}

func parseTelnetQuick(text string) (*config.Host, bool) {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(strings.ToLower(text), "telnet:") {
		return nil, false
	}
	parsed, err := url.Parse(text)
	if err != nil || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.ForceQuery || strings.Contains(text, "#") ||
		!validTelnetHost(parsed.Hostname()) || strings.HasSuffix(parsed.Host, ":") ||
		strings.Contains(parsed.Hostname(), ":") && !strings.HasPrefix(parsed.Host, "[") {
		return nil, true
	}
	port := config.DefaultTelnetPort
	if explicit := parsed.Port(); explicit != "" {
		port, err = strconv.Atoi(explicit)
		if err != nil || port < 1 || port > 65535 {
			return nil, true
		}
	}
	return &config.Host{Kind: config.KindTelnet, Addr: parsed.Hostname(), Port: port, Auth: config.AuthPassword}, true
}

func (a *App) openDeviceSession(tab *Session, h *config.Host, password string) {
	if tab != nil && tab.isNewTab() && !tab.isPanelTab() && slices.Contains(a.sessions, tab) {
		tab.Host = h
		a.active = slices.Index(a.sessions, tab)
	} else {
		tab = newSession(h)
		a.sessions = append(a.sessions, tab)
		if !a.settings.NoAutoSelectTab || a.current() == nil {
			a.active = len(a.sessions) - 1
		}
	}
	a.connectDevice(tab, password)
}

func (a *App) withTelnetLogin(h *config.Host, next func(string)) {
	if h.Kind != config.KindTelnet || h.Deleted() {
		return
	}
	if h.Telnet == nil || !h.Telnet.AutoLogin || h.ID == "" {
		next("")
		return
	}
	if a.readSecret(telnetSecretNeed(h), func() { a.withTelnetLogin(h, next) }) {
		return
	}
	password := a.passwords[telnetPasswordKey(h.ID)]
	if password == "" {
		next("")
		return
	}
	snapshot := h.Clone()
	q := Confirmation{Title: locales.AppTitle(), Message: locales.TelnetConfirmTitle(), Detail: locales.TelnetConfirmBody(h.Target()), OK: locales.Connect(), Cancel: locales.Cancel()}
	go func() {
		if a.plat.Confirm(q) {
			a.post(func() {
				if !h.Deleted() && h.Kind == config.KindTelnet && h.Addr == snapshot.Addr && h.Port == snapshot.Port &&
					h.User == snapshot.User && h.Encoding == snapshot.Encoding && h.Telnet != nil &&
					*h.Telnet == *snapshot.Telnet && a.passwords[telnetPasswordKey(h.ID)] == password {
					next(password)
				}
			})
		}
	}()
}

const secretTelnetPassword = "telnet-password"

func telnetPasswordKey(id string) string { return "telnet/" + id }

func telnetSecretNeed(h *config.Host) *secretNeed {
	return &secretNeed{key: telnetPasswordKey(h.ID), store: hostSecretKey(h.ID, secretTelnetPassword), host: h}
}

func (a *App) keepTelnetSecret(h *config.Host, password string, remember, unread bool) {
	setRemember(h, remember)
	if unread && remember && password == "" {
		return
	}
	if h.Telnet == nil || !h.Telnet.AutoLogin || password == "" {
		a.forgetTelnetSecret(h.ID)
		return
	}
	a.passwords[telnetPasswordKey(h.ID)] = password
	key := hostSecretKey(h.ID, secretTelnetPassword)
	if remember {
		a.setSecret(key, password)
	} else {
		a.deleteSecret(key)
	}
}

func (a *App) forgetTelnetSecret(id string) {
	delete(a.passwords, telnetPasswordKey(id))
	a.deleteSecret(hostSecretKey(id, secretTelnetPassword))
}

func (a *App) reconnectDevice(s *Session) {
	if s.Host.Kind == config.KindTelnet {
		a.withTelnetLogin(s.Host, func(password string) {
			if slices.Contains(a.sessions, s) {
				a.connectDevice(s, password)
			}
		})
		return
	}
	if s.Host.Kind == config.KindSerial {
		a.connectDevice(s, "")
	}
}

func (a *App) connectDevice(s *Session, password string) {
	s.disconnect()
	s.status, s.err = statusConnecting, nil
	s.deviceKind = s.Host.Kind
	s.forwardWarnings = nil
	h := s.Host.Clone()
	ctx, cancel := context.WithCancel(context.Background())
	s.ctx, s.cancel = ctx, cancel
	p := newPane()
	s.panes.reset(p)
	enc := a.settings.HostEncoding(h)
	opts := a.terminalOptions(h)
	opts.OnKey = keySequences(a.settings.HostBackspace(h), a.settings.HostDelete(h), func() *terminal.Terminal { return p.term })
	opts.OnExit = func(int) { a.post(func() { a.paneExited(s, p) }) }
	dial := a.telnetDial
	if dial == nil {
		dial = (&net.Dialer{Timeout: 12 * time.Second}).DialContext
	}
	open := a.serialOpen
	if open == nil {
		open = serial.Open
	}
	go func() {
		stream, err := openDeviceStream(ctx, h, password, enc, dial, open)
		a.post(func() {
			if ctx.Err() != nil || s.ctx != ctx || s.status != statusConnecting {
				if stream != nil {
					stream.Close()
				}
				return
			}
			if err == nil {
				opts.Conn = stream
				var echo *serialEcho
				if h.Kind == config.KindSerial && h.Serial != nil && h.Serial.LocalEcho {
					echo = &serialEcho{ReadWriteCloser: stream}
					opts.Conn = echo
				}
				p.term, err = terminal.New(opts)
				if err == nil {
					if echo != nil {
						echo.term = p.term
					}
					s.panes.opened(p)
					s.panes.refocus = true
					s.status = statusConnected
					a.noteConnected(h)
					return
				}
				stream.Close()
			}
			s.status, s.err = statusFailed, err
			cancel()
		})
	}()
}

func openDeviceStream(ctx context.Context, h *config.Host, password string, enc config.Encoding,
	dial func(context.Context, string, string) (net.Conn, error), open func(string, config.SerialConfig) (io.ReadWriteCloser, error),
) (io.ReadWriteCloser, error) {
	if h.Kind == config.KindSerial {
		var settings config.SerialConfig
		if h.Serial != nil {
			settings = *h.Serial
		}
		stream, err := open(h.Addr, settings)
		if err != nil {
			return nil, err
		}
		return termenc.Wrap(stream, enc), nil
	}
	if !validTelnetHost(h.Addr) || h.PortOr22() < 1 || h.PortOr22() > 65535 {
		return nil, errTelnetAddress
	}
	raw, err := dial(ctx, "tcp", net.JoinHostPort(h.Addr, strconv.Itoa(h.PortOr22())))
	if err != nil {
		return nil, err
	}
	options := telnet.Options{Terminal: h.TermTypeOrDefault(), CRAsNewline: true, Encoding: enc}
	if cfg := h.Telnet; cfg != nil {
		if cfg.KeepAlive {
			options.KeepAlive = 30 * time.Second
		}
		if cfg.AutoLogin {
			options.Username, options.Password = h.User, password
			options.LoginPrompt, options.PasswordPrompt = cfg.LoginPrompt, cfg.PasswordPrompt
		}
	}
	stream, err := telnet.New(raw, options)
	if err != nil {
		raw.Close()
		return nil, err
	}
	return termenc.Wrap(stream, enc), nil
}

var errTelnetAddress = errors.New("invalid Telnet address")

type serialEcho struct {
	io.ReadWriteCloser
	term       *terminal.Terminal
	escape     byte
	previousCR bool
	tail       []byte
}

func (s *serialEcho) Write(data []byte) (int, error) {
	count, err := s.ReadWriteCloser.Write(data)
	if count == 0 {
		return count, err
	}
	text := append(s.tail, data[:count]...)
	s.tail = nil
	var out []byte
	for len(text) != 0 {
		value := text[0]
		if s.escape != 0 {
			if s.escape == 1 && (value == '[' || value == 'O') {
				s.escape = 2
			} else if s.escape == 1 || value >= 0x40 && value <= 0x7e {
				s.escape = 0
			}
			text = text[1:]
			continue
		}
		switch {
		case value == '\x1b':
			s.escape = 1
		case value == '\r' || value == '\n' && !s.previousCR:
			out = append(out, '\r', '\n')
		case value == '\b' || value == 127:
			out = append(out, '\b', ' ', '\b')
		case value >= 32:
			if !utf8.FullRune(text) {
				s.tail = append([]byte(nil), text...)
				text = nil
				continue
			}
			_, size := utf8.DecodeRune(text)
			out = append(out, text[:size]...)
			text = text[size:]
			s.previousCR = false
			continue
		}
		s.previousCR = value == '\r'
		text = text[1:]
	}
	if len(out) != 0 {
		s.term.Feed(out)
	}
	return count, err
}
