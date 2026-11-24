package app

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"finallyshell/internal/config"
	"finallyshell/locales"
	"github.com/egoist/mygo/ui"
)

func TestAppLockVerifier(t *testing.T) {
	first, err := newLockVerifier("a long test password")
	if err != nil {
		t.Fatal(err)
	}
	second, err := newLockVerifier("a long test password")
	if err != nil || first == second {
		t.Fatal("verifiers need independent salts")
	}
	if !verifyLockPassword(first, "a long test password") || verifyLockPassword(first, "wrong") || verifyLockPassword("argon2id-v0$broken", "a long test password") {
		t.Fatal("incorrect verifier behaviour")
	}
}

func TestAppLockBackoff(t *testing.T) {
	now := time.Unix(100, 0)
	state := appLock{now: func() time.Time { return now }}
	for _, delay := range []time.Duration{1, 2, 4, 8, 16, 30, 30} {
		state.wrongPassword()
		if got := state.delay(); got != delay*time.Second {
			t.Fatalf("delay %v, want %v seconds", got, delay)
		}
		now = now.Add(delay * time.Second)
		if state.delay() != 0 {
			t.Fatal("retry stayed blocked after timeout")
		}
	}
}

func TestAppLockStartsBeforeHostsAndGatesChildWindows(t *testing.T) {
	t.Setenv("FINALLYSHELL_DIR", t.TempDir())
	if err := config.SaveSettings(config.Settings{LockEnabled: true}); err != nil {
		t.Fatal(err)
	}
	const name = "Private test host"
	host := &config.Host{ID: "h-lock", Name: name, Addr: "host.example.invalid"}
	if err := config.SaveHosts(&config.Store{Hosts: []*config.Host{host}}); err != nil {
		t.Fatal(err)
	}
	platform := newFakePlatform(t)
	verifier, err := newLockVerifier("fixture password")
	if err != nil {
		t.Fatal(err)
	}
	platform.put(lockVerifierKey, verifier)
	application := New(platform)
	application.NewTab()
	main := ui.NewTester(application.View, 1280, 820)
	platform.tt = main
	application.openManager()
	manager := platform.show(t, WindowManager)
	var childBuilt bool
	application.openWindow(WindowSpec{Kind: "test-child", Title: func() string { return name }, Width: 460, Height: 250}, func(c *ui.Context) {
		childBuilt = true
		ui.Text(c, name)
	})
	child := platform.show(t, "test-child")
	for _, window := range []*ui.Tester{main, manager, child} {
		window.Frame()
		if !window.HasText(locales.LockUnlock()) || window.HasText(name) {
			t.Fatalf("window content visible while locked: %q", window.Texts())
		}
		if _, ok := window.Access(name); ok {
			t.Fatal("private host in the accessibility tree")
		}
	}
	if childBuilt || platform.windows["test-child"].spec.Title() != locales.AppTitle() {
		t.Fatal("child content or title exposed while locked")
	}
	typeInto(t, child, locales.LockPassword(), "fixture password")
	child.Key(0, ui.KeyEnter)
	platform.until(t, "unlock", func() bool { return !application.Locked() })
	child.Frame()
	manager.Frame()
	if !childBuilt || !manager.HasText(name) || platform.windows["test-child"].title != name {
		t.Fatal("normal views were not restored")
	}
	application.openWindow(WindowSpec{Kind: "toast-child", Title: func() string { return name }, Width: 460, Height: 250}, func(c *ui.Context) {
		if !childBuilt {
			return
		}
		childBuilt = false
		c.Toast(name)
	})
	toast := platform.show(t, "toast-child")
	toast.Frame()
	application.LockNow()
	toast.Frame()
	if toast.HasText(name) {
		t.Fatal("a toast from the unlocked window remained visible")
	}
}

func TestAppLockIdleAndManualTriggers(t *testing.T) {
	application, platform := testApp(t)
	application.settings.LockEnabled, application.settings.LockIdleMinutes = true, 1
	now := time.Unix(100, 0)
	application.lock.now = func() time.Time { return now }
	session := demoSession(application, "test session", "host.example.invalid")
	main := ui.NewTester(application.View, 1280, 820)
	platform.tt = main
	main.Frame()
	now = now.Add(50 * time.Second)
	main.ClickAt(30, 30)
	now = now.Add(59 * time.Second)
	main.Frame()
	if application.Locked() {
		t.Fatal("input did not reset the idle timer")
	}
	now = now.Add(time.Second)
	main.Advance(time.Minute)
	if !application.Locked() || session.status != statusConnected {
		t.Fatal("idle lock did not take effect or disconnected the session")
	}
	application.unlockApp()
	key := lockShortcut()
	main.Frame()
	main.Key(key.Mods, key.Key)
	if !application.Locked() {
		t.Fatal("shortcut did not lock")
	}
	application.unlockApp()
	application.openBulkPalette(nil)
	palette := platform.show(t, "command-palette")
	palette.Frame()
	if err := palette.Click(locales.LockNow()); err != nil || !application.Locked() {
		t.Fatalf("command palette did not lock: %v", err)
	}
	application.unlockApp()
	application.WindowMinimized()
	if application.Locked() {
		t.Fatal("minimizing locked with the setting off")
	}
	application.settings.LockOnMinimize = true
	application.WindowMinimized()
	if !application.Locked() {
		t.Fatal("minimizing did not lock with the setting on")
	}
}

func TestAppLockOptionsConfirmBeforeSaving(t *testing.T) {
	application, platform := testApp(t)
	application.settings.LockEnabled = true
	if err := config.SaveSettings(application.settings); err != nil {
		t.Fatal(err)
	}
	application.optionsNav.Page = pageSecurity
	application.openOptions()
	options := platform.show(t, WindowOptions)
	options.Frame()
	if err := options.Click(locales.LockMinimize()); err != nil {
		t.Fatal(err)
	}
	platform.until(t, "cancel lock option", func() bool { return !application.lock.confirming })
	if config.LoadSettings().LockOnMinimize || application.settings.LockOnMinimize {
		t.Fatal("cancelled option changed settings")
	}
	platform.answer = true
	if err := options.Click(locales.LockMinimize()); err != nil {
		t.Fatal(err)
	}
	platform.until(t, "confirm lock option", func() bool { return !application.lock.confirming })
	questions := platform.questions()
	if len(questions) != 2 || !questions[1].RequireExplicit || questions[1].Parent != WindowOptions || !config.LoadSettings().LockOnMinimize {
		t.Fatal("option was not confirmed and saved")
	}
}

func TestAppLockPasswordChangeRemoveAndBackup(t *testing.T) {
	application, platform := testApp(t)
	if err := config.SaveSettings(application.settings); err != nil {
		t.Fatal(err)
	}
	application.optionsNav.Page = pageSecurity
	application.openOptions()
	options := platform.show(t, WindowOptions)
	options.Frame()
	if err := options.Click(locales.LockSetPassword()); err != nil {
		t.Fatal(err)
	}
	typeInto(t, options, locales.LockNewPassword(), "first fixture password")
	typeInto(t, options, locales.LockRepeatPassword(), "first fixture password")
	options.Key(0, ui.KeyEnter)
	platform.until(t, "lock settings", func() bool { return application.settings.LockEnabled })
	first := platform.secrets()[lockVerifierKey]
	if !verifyLockPassword(first, "first fixture password") || !config.LoadSettings().LockEnabled {
		t.Fatal("setting the password did not persist a verifier and the setting")
	}
	backups, err := filepath.Glob(filepath.Join(config.Dir(), "settings.json.bak-*"))
	if err != nil || len(backups) != 1 {
		t.Fatalf("settings backups: %v, %v", backups, err)
	}
	form := &lockOptions{current: "first fixture password", password: "second fixture password", repeat: "second fixture password"}
	application.lock.options = form
	application.saveLockPassword(form)
	platform.until(t, "change", func() bool { return !application.lock.busy })
	if !verifyLockPassword(platform.secrets()[lockVerifierKey], "second fixture password") {
		t.Fatal("password change did not replace the verifier")
	}
	form = &lockOptions{current: "second fixture password", remove: true}
	application.lock.options = form
	application.saveLockPassword(form)
	platform.until(t, "remove", func() bool { return !application.lock.busy })
	if application.settings.LockEnabled || config.LoadSettings().LockEnabled || platform.secrets()[lockVerifierKey] != "" {
		t.Fatal("removing the password did not disable the lock")
	}
}

func TestAppLockResetConfirmsAndFailsClosed(t *testing.T) {
	application, platform := testApp(t)
	application.settings.LockEnabled = true
	host := &config.Host{ID: "h-reset", Addr: "fixture.example.invalid", User: "test"}
	application.hosts.Add(host)
	application.passwords[host.ID] = "fixture"
	for _, name := range hostSecretNames {
		platform.put(hostSecretKey(host.ID, name), "fixture")
	}
	legacyEndpoint := &config.Host{ID: "h-old", Addr: "old.example.invalid", User: "test"}
	application.setSecret(privilegeSecretKey(legacyEndpoint, legacyEndpoint.User), "fixture")
	platform.put(lockVerifierKey, "fixture")
	platform.put(syncPasswordKey, "fixture")
	platform.put(syncCredentialKey(config.SyncWebDAV), "fixture")
	platform.until(t, "credential tracking", func() bool {
		return application.secrets.queue.idle() && platform.secrets()[privilegeResetIndexKey] != ""
	})
	application.LockNow()
	application.confirmLockReset(WindowMain)
	platform.until(t, "reset cancellation", func() bool { return !application.lock.confirming })
	if !application.Locked() || platform.secrets()[lockVerifierKey] == "" || platform.quits != 0 {
		t.Fatal("cancelled reset changed state")
	}
	platform.answer = true
	platform.fail = errors.New("fixture store failure")
	application.confirmLockReset(WindowMain)
	platform.until(t, "reset error", func() bool { return !application.lock.confirming && !application.lock.busy })
	if !application.Locked() || platform.secrets()[lockVerifierKey] == "" || platform.quits != 0 {
		t.Fatal("failed reset released the lock")
	}
	platform.fail = nil
	application.confirmLockReset(WindowMain)
	platform.until(t, "completed reset", func() bool { return platform.quits == 1 })
	if len(platform.secrets()) != 0 || len(application.passwords) != 0 || config.LoadSettings().LockEnabled || !application.Locked() {
		t.Fatal("reset did not clear credentials or exposed existing content before quitting")
	}
}

func TestAppLockFailedSettingsRollsBackVerifier(t *testing.T) {
	application, platform := testApp(t)
	application.saveSettings = func(config.Settings) error { return errors.New("fixture disk failure") }
	form := &lockOptions{password: "fixture password", repeat: "fixture password"}
	application.lock.options = form
	application.saveLockPassword(form)
	platform.until(t, "verifier rollback", func() bool { return !application.lock.busy })
	if application.settings.LockEnabled || platform.secrets()[lockVerifierKey] != "" || form.message == nil || form.message() != locales.LockSettingsFailed() {
		t.Fatal("failed settings save kept a new verifier or reported success")
	}
}
