package app

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"finallyshell/internal/config"
	"finallyshell/internal/sshx"
	"finallyshell/internal/totp"
	"finallyshell/locales"
	"github.com/egoist/mygo/ui"
)

const totpFixture = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"

func TestTOTPWaitsForNextPeriod(t *testing.T) {
	secret, err := totp.Parse(totpFixture)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(57, 0)
	var waited []time.Duration
	got, expiry, err := freshTOTP(context.Background(), secret, func() time.Time { return now }, func(_ context.Context, duration time.Duration) error {
		waited = append(waited, duration)
		now = now.Add(duration)
		return nil
	})
	if err != nil || !slices.Equal(waited, []time.Duration{3 * time.Second}) || got != secret.Code(time.Unix(60, 0)) || expiry.Unix() != 90 {
		t.Fatal("TOTP did not wait for the next complete period")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := freshTOTP(ctx, secret, time.Now, waitTOTP); !errors.Is(err, context.Canceled) {
		t.Fatal("cancel did not abort generation")
	}
}

func TestTOTPManualPromptAndCancel(t *testing.T) {
	application, platform := testApp(t)
	host := config.Host{ID: "h-totp", Addr: "test.example.invalid", User: "test"}
	application.hosts.Add(&host)
	opts, err := application.dialOptionsFor(context.Background(), &host, "", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	type reply struct {
		code string
		err  error
	}
	result := make(chan reply, 1)
	for index, cancelled := range []bool{false, true} {
		go func() {
			code, err := opts.AskVerificationCode("Verification code: ")
			result <- reply{code, err}
		}()
		kind := []WindowKind{"totp-prompt-1", "totp-prompt-2"}[index]
		platform.until(t, "verification dialog", func() bool { return application.windowOpen(kind) })
		window := platform.show(t, kind)
		window.Frame()
		if !window.HasText("Verification code:") || !window.HasText(host.Target()) {
			t.Fatal("verification dialog is missing the server prompt")
		}
		if cancelled {
			if err := window.Click(locales.Cancel()); err != nil {
				t.Fatal(err)
			}
		} else {
			typeInto(t, window, locales.TotpCode(), "012345")
			window.Key(0, ui.KeyEnter)
		}
		select {
		case got := <-result:
			if cancelled && !errors.Is(got.err, sshx.ErrVerificationCode) || !cancelled && (got.code != "012345" || got.err != nil) {
				t.Fatal("wrong verification response")
			}
		case <-time.After(2 * time.Second):
			t.Fatal("verification dialog did not finish")
		}
	}
}

func TestTOTPDialOptionsUseEachHostsSecret(t *testing.T) {
	application, platform := testApp(t)
	jump := &config.Host{ID: "h-jump", Addr: "jump.example.invalid", User: "test", Auth: config.AuthPassword}
	target := &config.Host{ID: "h-target", Addr: "target.example.invalid", User: "test", Auth: config.AuthKey, Proxy: &config.ProxyConfig{Kind: config.ProxyJump, JumpHost: jump.ID}}
	application.hosts.Add(jump)
	application.hosts.Add(target)
	fixture := map[string]string{jump.ID: "MY", target.ID: totpFixture}
	for id, value := range fixture {
		platform.put(hostSecretKey(id, secretTOTP), value)
	}
	opts, err := application.dialOptionsFor(t.Context(), target, "", nil, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		id  string
		ask func(string) (string, error)
	}{{jump.ID, opts.Jump.Options.AskVerificationCode}, {target.ID, opts.AskVerificationCode}} {
		secret, err := totp.Parse(fixture[item.id])
		if err != nil {
			t.Fatal(err)
		}
		result := make(chan string, 1)
		go func() {
			code, _ := item.ask("OTP:")
			result <- code
		}()
		var got string
		platform.until(t, "stored code", func() bool {
			select {
			case got = <-result:
				return true
			default:
				return false
			}
		})
		if got != secret.Code(time.Now()) || application.totpPromptSeq != 0 {
			t.Fatal("did not use the secret of the host receiving the challenge")
		}
	}
}

func TestTOTPEditAndCopyOnlyFromCredentialStore(t *testing.T) {
	application, platform := testApp(t)
	application.saveHosts = config.SaveHosts
	host := &config.Host{ID: "h-totp", Name: "Test gateway", Addr: "test.example.invalid", User: "test", Auth: config.AuthPassword, NoRememberPassword: true}
	application.hosts.Add(host)
	if err := application.saveHosts(application.hosts); err != nil {
		t.Fatal(err)
	}
	application.openEditor(host, WindowMain)
	platform.until(t, "TOTP status", func() bool { return !application.totpStatus[host.ID].checked.IsZero() })
	ed := application.editor
	ed.totpText = "invalid!"
	application.saveEditor()
	if ed.saveErr == nil || ed.saveErr() != locales.TotpInvalid() || len(platform.questions()) != 0 {
		t.Fatal("invalid entry was not rejected before confirmation")
	}
	ed.totpText = totpFixture
	application.saveEditor()
	platform.until(t, "cancel TOTP save", func() bool { return !ed.totpBusy })
	key := hostSecretKey(host.ID, secretTOTP)
	if platform.secrets()[key] != "" || len(platform.questions()) != 1 {
		t.Fatal("cancelled TOTP save changed credentials")
	}
	platform.answer = true
	application.saveEditor()
	platform.until(t, "save TOTP", func() bool { return !application.windowOpen(WindowEditor) })
	secret, err := totp.Parse(platform.secrets()[key])
	if err != nil {
		t.Fatal("confirmed TOTP was not stored")
	}
	backups, err := filepath.Glob(filepath.Join(config.Dir(), "hosts.json.bak-*"))
	if err != nil || len(backups) != 1 {
		t.Fatalf("connection backups: %v, %v", backups, err)
	}
	for _, path := range append(backups, filepath.Join(config.Dir(), "hosts.json")) {
		data, err := os.ReadFile(path)
		if err != nil || bytes.Contains(data, []byte(totpFixture)) || bytes.Contains(data, []byte("otpauth")) {
			t.Fatal("TOTP data in connection settings or backup")
		}
	}
	var syncSecrets map[string]string
	application.readSyncSecrets(false, func(values map[string]string) { syncSecrets = values })
	if syncSecrets != nil {
		t.Fatal("secrets included in sync without opt-in")
	}
	application.readSyncSecrets(true, func(values map[string]string) { syncSecrets = values })
	platform.until(t, "opt-in secrets", func() bool { return syncSecrets != nil })
	if syncSecrets[key] == "" {
		t.Fatal("saved TOTP not in opted-in sync")
	}
	application.openManager()
	manager := platform.show(t, WindowManager)
	manager.Frame()
	delete(application.totpStatus, host.ID)
	manager.RightClickAt(rowCenter(t, manager, host.Name))
	if err := manager.ChooseMenuItem(locales.TotpCopy()); err == nil {
		t.Fatal("copy was enabled before reading the credential store")
	}
	platform.until(t, "menu credential status", func() bool { return application.totpStatus[host.ID].available })
	manager.Frame()
	before := time.Now()
	if err := manager.ChooseMenuItem(locales.TotpCopy()); err != nil {
		t.Fatal(err)
	}
	platform.until(t, "clipboard copy", func() bool { return application.totpCopies[WindowManager].code != "" })
	manager.Frame()
	if got := manager.Clipboard(); got != secret.Code(before) && got != secret.Code(time.Now()) {
		t.Fatal("menu did not copy a current code")
	}
	application.applySyncedSecrets(nil, map[string]string{lockVerifierKey: "invalid verifier", hostSecretKey("nonexistent", secretTOTP): secret.Encode()}, &application.sync)
	if !application.secrets.queue.idle() {
		platform.until(t, "secret queue", application.secrets.queue.idle)
	}
	if platform.secrets()[lockVerifierKey] != "" || platform.secrets()[hostSecretKey("nonexistent", secretTOTP)] != "" {
		t.Fatal("sync accepted a device-local or unknown-host credential")
	}
	application.openEditor(host, WindowMain)
	ed = application.editor
	ed.totpChange = totpRemove
	application.saveEditor()
	platform.until(t, "remove TOTP", func() bool { return !application.windowOpen(WindowEditor) })
	if platform.secrets()[key] != "" || !host.NoRememberPassword {
		t.Fatal("removing TOTP did not clear its credential or changed the login setting")
	}
	manager.RightClickAt(rowCenter(t, manager, host.Name))
	if err := manager.ChooseMenuItem(locales.TotpCopy()); err == nil {
		t.Fatal("copy was enabled after removing TOTP")
	}
}

func TestAppLockTOTPScreenshots(t *testing.T) {
	if os.Getenv("SHOT_DIR") == "" {
		t.Skip("SHOT_DIR not set")
	}
	for _, language := range []string{"en", "zh-Hans"} {
		for _, appearance := range []string{"light", "dark"} {
			t.Run(language+"/"+appearance, func(t *testing.T) {
				application, platform := testApp(t)
				platform.SetLocale(language)
				dark := appearance == "dark"
				shot := func(tester *ui.Tester, scene string, width, height int) {
					tester.SetSize(width, height)
					tester.SetDark(dark)
					tester.SetLocale(language)
					tester.SetScale(1)
					tester.Frame()
					saveShot(t, tester, scene+"-"+language+"-"+appearance+".png")
				}
				application.settings.LockEnabled = true
				application.settings.LockIdleMinutes = 5
				application.optionsNav.Page = pageSecurity
				application.openOptions()
				shot(platform.show(t, WindowOptions), "security-options", 760, 600)
				host := &config.Host{ID: "h-fixture", Name: "Staging example", Addr: "ssh.example.invalid", User: "operator", Auth: config.AuthPassword}
				application.hosts.Add(host)
				application.openEditor(host, WindowMain)
				shot(platform.show(t, WindowEditor), "totp-field", 680, 700)
				var code string
				prompt := "Enter the verification code from your authenticator:"
				if language == "zh-Hans" {
					prompt = "请输入身份验证器中的验证码："
				}
				tester := ui.NewTester(func(c *ui.Context) {
					useTheme(c)
					application.totpPromptView(c, &code, host.Target(), prompt, func(string) {})
				}, 460, 205)
				shot(tester, "totp-prompt", 460, 205)
				application.LockNow()
				main := ui.NewTester(application.View, 1280, 820)
				shot(main, "app-lock-full", 1280, 820)
				shot(main, "app-lock-short", 460, 250)
			})
		}
	}
}
