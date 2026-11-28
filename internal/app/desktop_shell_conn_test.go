package app

import (
	"errors"
	"net"
	"testing"

	"finallyshell/locales"
)

func TestDesktopConnCardConnecting(t *testing.T) {
	a, _, tt, s := desktopShellTestApp(t)
	d := a.desktopOf(s)
	connecting := locales.ConnectingTo(s.target())
	if tt.HasText(connecting) || tt.HasText(locales.Reconnect()) {
		t.Fatal("a connected desktop shows the connection's card")
	}
	s.status = statusConnecting
	tt.Frame()
	if !tt.HasText(connecting) {
		t.Fatalf("no connecting card; texts %q", tt.Texts())
	}
	saveShot(t, tt, "desktop-conn-connecting.png")

	s.status = statusConnected
	tt.Frame()
	if tt.HasText(connecting) || !tt.HasText(locales.StatusConnected()) {
		t.Fatalf("no connected confirmation; texts %q", tt.Texts())
	}
	saveShot(t, tt, "desktop-conn-connected.png")
	tt.Advance(desktopConnConfirm / 2)
	if !tt.HasText(locales.StatusConnected()) {
		t.Fatal("the confirmation went too soon")
	}
	tt.Advance(desktopConnConfirm)
	if tt.HasText(locales.StatusConnected()) || !d.shell.conn.connected.IsZero() {
		t.Fatal("the confirmation stays")
	}
}

func TestDesktopConnCardLostAndReconnect(t *testing.T) {
	a, p, tt, s := desktopShellTestApp(t)
	d := a.desktopOf(s)
	// A port nothing listens on: reconnecting fails at once, without SSH.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s.Host.Addr, s.Host.Port = "127.0.0.1", l.Addr().(*net.TCPAddr).Port
	l.Close()
	a.passwords[s.Host.ID] = "secret"

	s.status, s.err = statusClosed, errConnectionLost
	tt.Frame()
	if !tt.HasText(locales.ConnectionLost()) || !tt.HasText(locales.Reconnect()) || !tt.HasText(locales.SwitchToTerminalMode()) {
		t.Fatalf("no lost card; texts %q", tt.Texts())
	}
	saveShot(t, tt, "desktop-conn-lost.png")

	desktopShellClick(t, tt, locales.Reconnect())
	if s.status != statusConnecting {
		t.Fatalf("Reconnect did not connect again: status %d", s.status)
	}
	tt.Frame()
	if !tt.HasText(locales.ConnectingTo(s.target())) || tt.HasText(locales.Reconnect()) {
		t.Fatalf("no connecting card on reconnect; texts %q", tt.Texts())
	}
	p.until(t, "the connection failing", func() bool { return s.status != statusConnecting })
	if s.status != statusFailed {
		t.Fatalf("status %d (%v)", s.status, s.err)
	}
	detail := describeError(s.err)
	if !tt.HasText(locales.StatusFailed()) || !tt.HasText(detail) || !tt.HasText(locales.Reconnect()) {
		t.Fatalf("no failed card with %q; texts %q", detail, tt.Texts())
	}
	saveShot(t, tt, "desktop-conn-failed.png")

	desktopShellClick(t, tt, locales.DesktopConnDismiss())
	if !d.shell.conn.dismissed || tt.HasText(detail) || tt.HasText(locales.Reconnect()) {
		t.Fatalf("closing the card did not hide it: dismissed %v, sessions %d, texts %q", d.shell.conn.dismissed, len(a.sessions), tt.Texts())
	}
	// A new failure shows it again.
	s.status = statusConnecting
	tt.Frame()
	s.status, s.err = statusFailed, errors.New("boom")
	tt.Frame()
	if !tt.HasText("boom") {
		t.Fatalf("a new failure stays hidden; texts %q", tt.Texts())
	}
	desktopShellClick(t, tt, locales.SwitchToTerminalMode())
	if a.current() != s {
		t.Fatal("the card did not switch to terminal mode")
	}
}

func TestDesktopConnCardUserDisconnect(t *testing.T) {
	a, _, tt, s := desktopShellTestApp(t)
	desktopShellClick(t, tt, locales.DesktopPower())
	desktopShellClick(t, tt, locales.Disconnect())
	tt.Frame()
	if s.status != statusClosed || s.err != nil {
		t.Fatalf("status %d (%v)", s.status, s.err)
	}
	if !tt.HasText(locales.StatusDisconnected()) || !tt.HasText(locales.Reconnect()) || tt.HasText(locales.ConnectionLost()) {
		t.Fatalf("no disconnected card; texts %q", tt.Texts())
	}
	saveShot(t, tt, "desktop-conn-disconnected.png")
	if a.desktopOf(s).shell.powerPopup {
		t.Fatal("the power menu stays open")
	}
}
