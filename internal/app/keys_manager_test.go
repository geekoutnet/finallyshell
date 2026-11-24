package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"finallyshell/internal/config"
	"finallyshell/internal/monitor"
	"finallyshell/internal/sshx"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

func managedKeysFixture(t *testing.T) (*App, *fakePlatform, *keyManager, *config.Host) {
	t.Helper()
	a, p := testApp(t)
	dir := t.TempDir()
	m := &keyManager{dir: dir}
	for _, fixture := range []struct{ name, comment string }{{"id_operations", "operations@workstation"}, {"id_backup", "backup@workstation"}} {
		private, public, err := sshx.GenerateKey("ed25519", fixture.comment, "")
		if err != nil {
			t.Fatal(err)
		}
		if err := sshx.WriteKeyPair(filepath.Join(dir, fixture.name), private, public, false, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	var err error
	m.rows, err = listKeys(dir, nil)
	if err != nil || len(m.rows) != 2 {
		t.Fatalf("fixture keys: %v, %v", m.rows, err)
	}
	host := &config.Host{ID: "web", Name: "Build server", Addr: "build.example", User: "release", Auth: config.AuthPassword}
	a.hosts.Add(host)
	a.hosts.Add(&config.Host{ID: "db", Name: "Backup server", Addr: "backup.example", User: "backup", Auth: config.AuthPassword})
	a.passwords[host.ID] = "fixture password"
	return a, p, m, host
}

func keysTester(a *App, p *fakePlatform, m *keyManager, width, height int, dark bool) *ui.Tester {
	tester := ui.NewTester(func(c *ui.Context) { useTheme(c); a.keysView(c, m) }, width, height)
	tester.SetLocale(p.locale)
	tester.SetDark(dark)
	tester.Frame()
	p.tt = tester
	return tester
}

func TestKeysManagerUI(t *testing.T) {
	a, p, m, host := managedKeysFixture(t)
	tester := keysTester(a, p, m, 900, 600, false)
	wantTexts(t, tester, "list", "id_operations", "id_backup")
	m.query = "operations@"
	tester.Frame()
	if tester.HasText("id_backup") || !tester.HasText("id_operations") {
		t.Fatal("key filter failed")
	}
	m.query = ""
	m.sort = ui.SortOrder{Column: "name", Descending: true}
	if got := filepath.Base(m.shown()[0].path); got != "id_operations" {
		t.Fatalf("wrong order: %s", got)
	}
	tester.RightClickAt(rowCenter(t, tester, "id_operations"))
	if err := tester.ChooseMenuItem(locales.KeysCopyPublic()); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(tester.Clipboard(), "ssh-ed25519 ") {
		t.Fatal("public key not copied")
	}
	clickButton(t, tester, locales.KeysGenerate())
	if m.form == nil {
		t.Fatal("generate form not open")
	}
	m.form.name, m.form.comment = "id_new", "fixture@workstation"
	m.form.pass, m.form.again = "fixture one", "fixture two"
	tester.Frame()
	clickButton(t, tester, locales.KeysGenerate())
	wantTexts(t, tester, "passphrase validation", locales.KeysPassMismatch())
	if _, err := os.Stat(filepath.Join(m.dir, "id_new")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("validation created a file")
	}
	clickButton(t, tester, locales.Cancel())
	if m.form != nil {
		t.Fatal("cancel did not close the form")
	}
	m.selected = filepath.Join(m.dir, "id_operations")
	host.KeyPath = m.selected
	tester.Frame()
	clickButton(t, tester, locales.Delete())
	clickButton(t, tester, locales.Cancel())
	if _, err := os.Stat(m.selected); err != nil {
		t.Fatal(err)
	}
	clickButton(t, tester, locales.Delete())
	clickButton(t, tester, locales.Delete())
	wantTexts(t, tester, "second confirmation", locales.KeysDeleteUsed(1))
	if _, err := os.Stat(m.selected); err != nil {
		t.Fatal(err)
	}
	clickButton(t, tester, locales.Delete())
	p.until(t, "key removed and list reread", func() bool {
		return len(m.rows) == 2 && !tester.HasText("operations@workstation") && m.message == locales.KeysDeleted() && !m.loading
	})
	if _, err := os.Stat(m.selected); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("confirmed delete did not remove key")
	}
}

func TestKeysDeployUIConfirmation(t *testing.T) {
	a, p, m, host := managedKeysFixture(t)
	key := m.rows[0]
	const failure = "fixture: permission denied"
	commands := make(chan string, 4)
	privilege := make(chan monitor.PrivilegeRequest, 2)
	m.backend.connect = func(_ context.Context, host *config.Host, opts sshx.Options) (keysRemote, error) {
		if host.Auth == config.AuthKey {
			commands <- "verified " + opts.VerifyKeyFingerprint
			return keysRemote{privilege: func(_ context.Context, request monitor.PrivilegeRequest) (monitor.PrivilegeResult, error) {
				privilege <- request
				if request.Probe {
					return monitor.PrivilegeResult{Stdout: "sudo-n"}, nil
				}
				return monitor.PrivilegeResult{Started: true, Stdout: "fixture reload"}, nil
			}}, nil
		}
		if opts.Password != "fixture password" {
			return keysRemote{}, errors.New("missing fixture credential")
		}
		return keysRemote{run: func(_ context.Context, script string) (string, string, error) {
			commands <- script
			return "", failure, nil
		}}, nil
	}
	m.hostID, m.selected = host.ID, key.path
	tester := keysTester(a, p, m, 900, 600, false)
	clickButton(t, tester, locales.KeysDeploy())
	clickButton(t, tester, locales.KeysReview())
	wantTexts(t, tester, "deployment confirmation", host.Target())
	clickButton(t, tester, locales.Cancel())
	if len(commands) != 0 || m.deploy != nil {
		t.Fatal("cancel ran deployment")
	}
	clickButton(t, tester, locales.KeysDeploy())
	m.deploy.switchAuth = true
	tester.Frame()
	clickButton(t, tester, locales.KeysReview())
	clickButton(t, tester, locales.KeysDeploy())
	p.until(t, "deployment results", func() bool { return m.deploy != nil && !m.deploy.busy && len(m.deploy.results) != 0 })
	wantTexts(t, tester, "result", locales.KeysSwitched(), failure)
	expected, err := keysAuthorizedScript(key.Authorized())
	if err != nil || len(commands) != 2 || <-commands != expected || <-commands != "verified "+key.Fingerprint() || host.KeyPath != key.path || host.Auth != config.AuthKey {
		t.Fatal("unexpected commands or saved authentication")
	}
	clickButton(t, tester, locales.KeysDisablePassword())
	wantTexts(t, tester, "strong warning", locales.KeysDisableWarning())
	clickButton(t, tester, locales.Cancel())
	if len(commands) != 0 || m.deploy.disableAttempted {
		t.Fatal("cancel disabled password authentication")
	}
	clickButton(t, tester, locales.KeysDisablePassword())
	if err := tester.Click(host.Target()); err != nil {
		t.Fatal(err)
	}
	clickButton(t, tester, locales.KeysDisablePassword())
	p.until(t, "password authentication result", func() bool { return !m.deploy.busy && m.deploy.results[0].disabled })
	wantTexts(t, tester, "disable result", locales.KeysDisabled(), "fixture reload")
	if len(commands) != 1 || <-commands != "verified "+key.Fingerprint() || len(privilege) != 2 || !(<-privilege).Probe || (<-privilege).Action != keysDisablePasswordScript {
		t.Fatal("disabling password authentication did not use a fresh key login and shared escalation")
	}
}

func TestKeysSwitchPreservesStoredPassword(t *testing.T) {
	a, p, _, host := managedKeysFixture(t)
	password := hostSecretKey(host.ID, secretPassword)
	a.setSecret(password, "fixture password")
	old := host.Clone()
	if err := a.keysSwitchHost(old, "id_fixture", "fixture passphrase", false); err != nil {
		t.Fatal(err)
	}
	a.saveSSH(host, &connEditor{auth: config.AuthKey, keyPath: "id_fixture", user: host.User, passphrase: "fixture passphrase", remember: true})
	p.until(t, "stored host password and passphrase", func() bool { return len(p.secrets()) == 2 && a.secrets.queue.idle() })
	if p.secrets()[password] != "fixture password" {
		t.Fatal("switch or editor save removed the old password")
	}
	if err := a.keysSwitchHost(host.Clone(), "id_fixture", "fixture passphrase", true); err != nil {
		t.Fatal(err)
	}
	p.until(t, "explicit removal of stored password", func() bool { return len(p.secrets()) == 1 && a.secrets.queue.idle() })
}

func TestKeysScreenshots(t *testing.T) {
	for _, language := range []string{"en", "zh-Hans"} {
		for _, appearance := range []string{"light", "dark"} {
			t.Run(language+"/"+appearance, func(t *testing.T) {
				a, p, m, host := managedKeysFixture(t)
				p.SetLocale(language)
				dark := appearance == "dark"
				m.selected = filepath.Join(m.dir, "id_operations")
				host.KeyPath = m.selected
				suffix := fmt.Sprintf("%s-%s.png", language, appearance)
				tester := keysTester(a, p, m, 1280, 820, dark)
				saveShot(t, tester, "keys-manager-"+suffix)
				short := keysTester(a, p, m, 460, 310, dark)
				saveShot(t, short, "keys-short-"+suffix)
				clickButton(t, tester, locales.KeysGenerate())
				m.form.comment = "release@workstation"
				tester.Frame()
				saveShot(t, tester, "keys-generate-"+suffix)
				clickButton(t, tester, locales.Cancel())
				clickButton(t, tester, locales.KeysDeploy())
				d := m.deploy
				d.results = []keysResult{
					{target: keysTarget{host: host}, appended: true, verified: true, switched: true},
					{target: keysTarget{host: a.hosts.Host("db")}, appended: true, err: errors.New("fixture: key refused")},
				}
				tester.Frame()
				saveShot(t, tester, "keys-deploy-"+suffix)
				a.openEditor(host, WindowMain)
				a.editor.nav.Page = editorTerminal
				a.editor.forwardAgent, a.editor.x11Forward = true, "untrusted"
				editor := p.show(t, WindowEditor)
				editor.SetDark(dark)
				editor.Frame()
				wantTexts(t, editor, "forwarding controls", locales.FwdAgent(), locales.FwdX11())
				saveShot(t, editor, "keys-editor-"+suffix)
			})
		}
	}
}
