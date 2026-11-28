package app

import (
	"path"
	"slices"
	"strings"
	"testing"
	"time"

	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

// TestDesktopSurfaceLive makes, runs, copies, renames and deletes files on
// the desktop of the testbox, in its ~/Desktop, then puts ~/Desktop back as
// it was.
func TestDesktopSurfaceLive(t *testing.T) {
	live := openDesktopLiveFiles(t, true)
	a, p, tt, s := live.App, live.p, live.tt, live.s
	home := live.run(t, `printf %s "$HOME"`)
	desk := path.Join(home, "Desktop")
	if live.run(t, "if [ -e "+relayQuote(desk)+" ]; then echo yes; fi") == "yes" {
		t.Skip("~/Desktop is there already; this test makes its own")
	}
	t.Cleanup(func() { live.run(t, relayQuote("rm", "-rf", "--", desk)) })
	d := a.desktopOf(s)
	p.until(t, "the desktop's folder", func() bool {
		tt.Frame()
		return d.filesDir != "" && d.files != nil && !d.files.explorer().loading
	})
	if d.filesDir != desk {
		t.Fatalf("folder %q, want %q", d.filesDir, desk)
	}
	if out := live.run(t, "if [ -e "+relayQuote(desk)+" ]; then echo made; fi"); out != "" {
		t.Fatal("opening the desktop made ~/Desktop")
	}
	f := d.files.explorer()
	wait := func(step string, done func() bool) {
		t.Helper()
		p.until(t, step, func() bool {
			tt.Frame()
			if f.conflict == nil && f.modal != nil && f.job == nil && f.modal.action == locales.DesktopFilesConfirmPaste() {
				_ = tt.Click(f.modal.action)
			}
			return done()
		})
	}
	ids := func() []string { return desktopSurfaceIDs(d) }

	// A new script: ~/Desktop made, the script executable.
	a.desktopSurfaceNew(d, s, desktopNewScript)
	tt.Frame()
	f.modal.value = "live.sh"
	desktopFilesTestConfirm(t, tt, locales.DesktopFilesCreate())
	wait("new script", func() bool { return slices.Contains(ids(), "file:live.sh") })
	script := path.Join(desk, "live.sh")
	if mode := live.run(t, "stat -c %a "+relayQuote(script)); mode != "755" {
		t.Fatalf("script mode %s", mode)
	}
	if text := live.run(t, "cat "+relayQuote(script)); text != strings.TrimSpace(desktopScriptTemplate) {
		t.Fatalf("script %q", text)
	}

	// Run in terminal: the script runs, in its folder.
	marker := live.scratch(t)
	live.run(t, "printf '%s\\n' 'pwd > "+marker+"/ran' >> "+relayQuote(script))
	pt := desktopSurfacePoint(t, a, s, "file:live.sh")
	x, y := desktopShellPoint(d, pt)
	tt.RightClickAt(x, y)
	if err := tt.ChooseMenuItem(locales.DesktopShellRunInTerminal()); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		tt.Frame()
		if out, _, err := live.exec("cat " + marker + "/ran"); err == nil && strings.TrimSpace(out) == desk {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the script did not run in its folder")
		}
		time.Sleep(200 * time.Millisecond)
	}

	// Copy and paste with the keys: the server copies it, keeping both.
	clear(d.shell.selection)
	tt.ClickAt(x, y)
	tt.Key(ui.Ctrl, ui.KeyC)
	tt.Key(ui.Ctrl, ui.KeyV)
	wait("paste", func() bool {
		if f.conflict != nil {
			_ = tt.Click(locales.DesktopFilesKeepBoth())
		}
		return slices.Contains(ids(), "file:live (1).sh")
	})

	// Rename with F2.
	pt = desktopSurfacePoint(t, a, s, "file:live (1).sh")
	x, y = desktopShellPoint(d, pt)
	clear(d.shell.selection)
	tt.ClickAt(x, y)
	tt.Key(0, ui.KeyF2)
	tt.Frame()
	if f.modal == nil {
		t.Fatal("F2 did not ask for a name")
	}
	f.modal.value = "copy.sh"
	desktopFilesTestConfirm(t, tt, locales.DesktopFilesConfirmRename())
	wait("rename", func() bool { return slices.Contains(ids(), "file:copy.sh") })

	// Delete moves it to the trash.
	pt = desktopSurfacePoint(t, a, s, "file:copy.sh")
	x, y = desktopShellPoint(d, pt)
	clear(d.shell.selection)
	tt.ClickAt(x, y)
	tt.Key(0, ui.KeyDelete)
	tt.Frame()
	if f.modal == nil {
		t.Fatal("Delete did not ask")
	}
	// The title says the same as the button: press the button.
	button, ok := tt.AccessByRole(ui.RoleButton, f.modal.action)
	if !ok {
		t.Fatal("no button to confirm")
	}
	tt.ClickAt(button.Bounds.X+button.Bounds.W/2, button.Bounds.Y+button.Bounds.H/2)
	wait("delete", func() bool { return !slices.Contains(ids(), "file:copy.sh") && f.job == nil })
	if out := live.run(t, "ls "+relayQuote(desk)); strings.Contains(out, "copy.sh") {
		t.Fatalf("still on the desktop: %s", out)
	}
	// The trash keeps it (freedesktop): clean it up with the test.
	live.run(t, `rm -rf "$HOME/.local/share/Trash/files/copy.sh"* "$HOME/.local/share/Trash/info/copy.sh"*`)

	// The power menu can restart and shut down here, but this test never
	// does: the box would stop.
	desktopShellClick(t, tt, locales.DesktopPower())
	if tt.HasText(locales.DesktopPowerUnavailable()) {
		t.Fatal("restart and shut down are off on a connected server")
	}
}
