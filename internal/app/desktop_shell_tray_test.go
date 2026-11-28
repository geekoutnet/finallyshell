package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"finallyshell/internal/monitor"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

func TestDesktopTrayClockAndCalendar(t *testing.T) {
	a, _, tt, s := desktopShellTestApp(t)
	d := a.desktopOf(s)
	for _, label := range []string{locales.DesktopTray(), locales.DesktopClock(), locales.DesktopPower()} {
		if _, ok := tt.AccessByRole(ui.RoleButton, label); !ok {
			t.Fatalf("the dock has no %q", label)
		}
	}
	desktopShellClick(t, tt, locales.DesktopClock())
	if !d.shell.clockPopup || !tt.HasText(locales.DesktopClockSettings()) {
		t.Fatal("the clock did not open its calendar")
	}
	for _, name := range strings.Split(locales.DesktopClockWeekdays(), ",") {
		if !tt.HasText(name) {
			t.Fatalf("calendar header misses %q", name)
		}
	}
	month := d.shell.clockMonth
	desktopShellClick(t, tt, locales.DesktopClockNext())
	if d.shell.clockMonth.Month() == month.Month() {
		t.Fatal("next month did not move")
	}
	desktopShellClick(t, tt, locales.DesktopClockToday())
	if !d.shell.clockMonth.Equal(month) {
		t.Fatal("today did not come back")
	}
	desktopShellClick(t, tt, locales.DesktopClockSettings())
	if d.shell.clockPopup || len(d.windowsOf(appSettings)) != 1 {
		t.Fatal("date and time settings did not open")
	}
	// The load: what the monitor read, and the task manager from it.
	desktopShellClick(t, tt, locales.DesktopTray())
	if !d.shell.trayPopup || d.shell.clockPopup || !tt.HasText(locales.DesktopAppTaskManager()) {
		t.Fatal("the load popup")
	}
	desktopShellClick(t, tt, locales.DesktopAppTaskManager())
	if len(d.windowsOf(appTaskMgr)) != 1 {
		t.Fatal("the task manager did not open")
	}
}

func TestDesktopCalendarStartsOnMonday(t *testing.T) {
	a, _, tt, s := desktopShellTestApp(t)
	d := a.desktopOf(s)
	desktopShellClick(t, tt, locales.DesktopClock())
	// November 2026 starts on a Sunday: its grid starts on October 26.
	d.shell.clockMonth = time.Date(2026, 11, 1, 0, 0, 0, 0, time.Local)
	tt.Frame()
	if !tt.HasText("26") || !tt.HasText("30") {
		t.Fatal("the grid misses the days around the month")
	}
}

func TestDesktopPowerMenu(t *testing.T) {
	a, p, tt, s := desktopShellTestApp(t)
	d := a.desktopOf(s)
	// Without a connection it can run on, restart and shut down are off.
	desktopShellClick(t, tt, locales.DesktopPower())
	if !tt.HasText(locales.DesktopPowerUnavailable()) {
		t.Fatal("no reason why restarting is off")
	}
	desktopShellClick(t, tt, locales.DesktopPowerReboot())
	if d.shell.power.dialog != nil {
		t.Fatal("a disabled restart asked")
	}
	d.shell.powerPopup = false
	// With one, it asks, then runs the script with root's rights.
	d.shell.power.runner = func(context.Context, string, func(string)) (string, string, error) { return "", "", nil }
	var ran []string
	s.overview.memory.privilegeRunner = func(_ context.Context, request monitor.PrivilegeRequest) (monitor.PrivilegeResult, error) {
		if request.Probe {
			return monitor.PrivilegeResult{Stdout: "root"}, nil
		}
		ran = append(ran, request.Command+request.Action)
		return monitor.PrivilegeResult{Started: true, Stdout: "restarting"}, nil
	}
	tt.Frame()
	desktopShellClick(t, tt, locales.DesktopPower())
	desktopShellClick(t, tt, locales.DesktopPowerReboot())
	if d.shell.power.dialog == nil || !tt.HasText(locales.DesktopPowerRebootWarning(s.label())) {
		t.Fatal("restart did not ask first")
	}
	if len(ran) != 0 {
		t.Fatal("ran before it was confirmed")
	}
	desktopShellClick(t, tt, locales.Cancel())
	if d.shell.power.dialog != nil || len(ran) != 0 {
		t.Fatal("cancel ran it")
	}
	desktopShellClick(t, tt, locales.DesktopPower())
	desktopShellClick(t, tt, locales.DesktopPowerShutdown())
	desktopShellClick(t, tt, locales.DesktopPowerShutdownAction())
	p.until(t, "shutdown ran", func() bool { return d.shell.power.dialog.work != nil && d.shell.power.dialog.work.done })
	if d.shell.power.dialog.work.err != nil || len(ran) == 0 {
		t.Fatalf("shutdown: %v, ran %d", d.shell.power.dialog.work.err, len(ran))
	}
	tt.Key(0, ui.KeyEscape) // the dialog's Close, as another Close is behind it
	if d.shell.power.dialog != nil {
		t.Fatal("the dialog stayed")
	}
}

func TestDesktopPowerScripts(t *testing.T) {
	for script, words := range map[string][]string{
		desktopRebootScript:   {"systemctl reboot", "shutdown -r now", "|| reboot", "&"},
		desktopShutdownScript: {"systemctl poweroff", "shutdown -h now", "|| poweroff", "&"},
	} {
		for _, word := range words {
			if !strings.Contains(script, word) {
				t.Errorf("%q lacks %q", script, word)
			}
		}
		if !strings.HasPrefix(script, "sync\n") {
			t.Errorf("%q does not sync first", script)
		}
	}
}

func TestDesktopPowerDisconnect(t *testing.T) {
	a, _, tt, s := desktopShellTestApp(t)
	desktopShellClick(t, tt, locales.DesktopPower())
	desktopShellClick(t, tt, locales.Disconnect())
	if s.status != statusClosed {
		t.Fatalf("status %v", s.status)
	}
	_ = a
}
