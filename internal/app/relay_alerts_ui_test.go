package app

import (
	"context"
	"errors"
	"io/fs"
	"testing"
	"time"

	"finallyshell/internal/config"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
	"golang.org/x/crypto/ssh"
)

type relayFakeIdentity struct{ ssh.Conn }

func (relayFakeIdentity) User() string { return "deploy" }

func relayUITargets(t *testing.T, a *App) (source, target *Session, targetFS relaySFTP) {
	t.Helper()
	source = demoSession(a, "Build server", "build.example")
	target = demoSession(a, "Artifact store", "artifacts.example")
	source.client, target.client = &ssh.Client{Conn: relayFakeIdentity{}}, &ssh.Client{Conn: relayFakeIdentity{}}
	source.sftp = relayTestFS(t).Client
	targetFS = relayTestFS(t)
	target.sftp = targetFS.Client
	a.active = 0
	return
}

func TestRelayDialogConfirmation(t *testing.T) {
	a, platform := testApp(t)
	source, target, targetFS := relayUITargets(t, a)
	a.hosts.Add(&config.Host{ID: "archive", Addr: "archive.example", User: "deploy"})
	if targets := a.relayTargets(source); len(targets) != 2 || targets[0].sess != target || targets[1].host.ID != "archive" {
		t.Fatalf("targets = %+v", targets)
	}
	if err := targetFS.Mkdir("/releases"); err != nil {
		t.Fatal(err)
	}
	readCalls := make(chan bool, 8)
	runCalls := make(chan struct{}, 8)
	read := func(_ context.Context, client *ssh.Client, folder string, names []string, review bool) (string, []string, map[string]fs.FileInfo, error) {
		if client != target.client || folder != "/releases" || len(names) != 2 {
			return "", nil, nil, errors.New("invalid request")
		}
		readCalls <- review
		return folder, []string{"latest", "previous"}, nil, nil
	}
	build := func() *relayDialog {
		a.openRelay(source, "/dist", []string{"release.tar", "checksums.txt"})
		dialog := a.relay
		dialog.target, dialog.host, dialog.canonical, dialog.dir = target, target.Host, "/releases", "/releases"
		dialog.readFolder = read
		dialog.run = func(_ context.Context, report func(relayProgress)) error {
			runCalls <- struct{}{}
			report(relayProgress{bytes: 8, total: 10, files: 1, totalFiles: 2, mode: relayViaApp})
			return errors.New("simulated destination write failure")
		}
		return dialog
	}
	dialog := build()
	tester := ui.NewTester(a.View, 800, 570)
	platform.tt = tester
	clickNamed(t, tester, locales.RelayReview())
	platform.until(t, "read-only review", func() bool { return dialog.review })
	if !<-readCalls || len(runCalls) != 0 || len(a.transfers.jobs) != 0 {
		t.Fatal("review performed a write")
	}
	wantTexts(t, tester, "review names", "release.tar", "checksums.txt")
	clickNamed(t, tester, locales.Cancel())
	if a.relay != nil || len(runCalls) != 0 || len(a.transfers.jobs) != 0 {
		t.Fatal("cancel started a relay")
	}
	dialog = build()
	tester.Frame()
	clickNamed(t, tester, locales.RelayReview())
	platform.until(t, "second review", func() bool { return dialog.review })
	clickNamed(t, tester, locales.RelayStart())
	platform.until(t, "failed transfer", func() bool { return len(a.transfers.jobs) == 1 && a.transfers.jobs[0].finished() })
	if len(runCalls) != 1 || a.bottomTab != tabTransfers {
		t.Fatalf("jobs = %d, calls = %d", len(a.transfers.jobs), len(runCalls))
	}
	wantTexts(t, tester, "transfer output", locales.TransferFailed("simulated destination write failure"))
}

func TestRelayFileMenuState(t *testing.T) {
	a, _ := testApp(t)
	source, _, _ := relayUITargets(t, a)
	tester := ui.NewTester(func(c *ui.Context) {
		useTheme(c)
		a.filePanel(c)
	}, 740, 330)
	if err := tester.RightClick("libacl.so.1"); err != nil {
		t.Fatal(err)
	}
	if err := tester.ChooseMenuItem(locales.RelaySend()); err != nil || a.relay == nil || a.relay.source != source {
		t.Fatalf("menu did not open the relay: %v", err)
	}
	a.closeRelay(a.relay)
	tester.Frame()
	if err := tester.RightClick("ld-linux-x86-64.so.2"); err != nil {
		t.Fatal(err)
	}
	_ = tester.ChooseMenuItem(locales.RelaySend())
	if a.relay != nil || relaySendReason(source, source.files.cwd, []string{"ld-linux-x86-64.so.2"}) != locales.RelayUnsupportedType() {
		t.Fatal("symbolic link exposed an enabled relay action")
	}
}

func TestRelayDragToSession(t *testing.T) {
	a, _ := testApp(t)
	source, target, _ := relayUITargets(t, a)
	tester := ui.NewTester(func(c *ui.Context) {
		useTheme(c)
		ui.Column(c).Gap(40).Children(func() {
			a.relayButton(c, source, "/data", []string{"release.tar"})
			row := ui.Box(c).Size(200, 45).Label(target.label())
			a.relayDropZone(row, target)
		})
	}, 460, 250)
	button, foundButton := tester.Find(locales.RelaySend())
	row, foundRow := tester.Find(target.label())
	if !foundButton || !foundRow {
		t.Fatalf("missing drag surface: %q", tester.Texts())
	}
	tester.Press(button.X+8, button.Y+8)
	tester.Move(button.X+20, button.Y+20)
	tester.Move(row.X+10, row.Y+10)
	tester.Release(row.X+10, row.Y+10)
	if a.relay == nil || a.relay.target != target || len(a.transfers.jobs) > 0 {
		t.Fatal("drop did not preselect the target for confirmation")
	}
}

func TestAlertsNotificationsAndList(t *testing.T) {
	a, platform := testApp(t)
	alpha := demoSession(a, "Alpha", "alpha.example").Host
	zulu := demoSession(a, "Zulu", "zulu.example").Host
	var delivered []string
	var clicked func()
	a.alerts.notify = func(title, body string, show func()) {
		delivered = append(delivered, title+" "+body)
		clicked = show
	}
	now := time.Now()
	input := alertInput{kind: alertMemory, active: true, value: "96.0%"}
	a.recordAlert(zulu, now, input)
	a.recordAlert(zulu, now.Add(time.Minute), input)
	a.recordAlert(zulu, now.Add(2*time.Minute), alertInput{kind: alertMemory})
	a.recordAlert(zulu, now.Add(4*time.Minute), input)
	a.recordAlert(alpha, now.Add(5*time.Minute), alertInput{kind: alertService, active: true, value: "docs-worker.service"})
	if len(delivered) != 2 || len(a.alerts.events) != 2 || clicked == nil {
		t.Fatalf("notifications = %v, alerts = %+v", delivered, a.alerts.events)
	}
	clicked()
	platform.until(t, "alert window", func() bool { return a.windowOpen(alertWindow) })
	tester := platform.show(t, alertWindow)
	wantTexts(t, tester, "alerts", locales.AlertTitle(), "Alpha", "Zulu", "docs-worker.service")
	a.alerts.sort = ui.SortOrder{Column: "host", Descending: true}
	tester.Frame()
	if rows := a.alerts.visible(); rows[0].host != "Zulu" || rows[1].host != "Alpha" {
		t.Fatalf("sort order = %+v", rows)
	}
	if err := tester.RightClick("96.0%"); err != nil {
		t.Fatal(err)
	}
	if err := tester.ChooseMenuItem(locales.AlertAcknowledge()); err != nil {
		t.Fatal(err)
	}
	if !a.alerts.events[0].acked {
		t.Fatal("alert did not acknowledge")
	}
	if err := tester.RightClick("96.0%"); err != nil {
		t.Fatal(err)
	}
	if err := tester.ChooseMenuItem(locales.AlertAcknowledge()); err == nil {
		t.Fatal("acknowledged alert had acknowledgment enabled")
	}
	tester.Key(0, ui.KeyEscape)
	a.alerts.unread = true
	a.alerts.query = "worker"
	tester.Frame()
	if rows := a.alerts.visible(); len(rows) != 1 || rows[0].host != "Alpha" || tester.HasText("Zulu") {
		t.Fatalf("filter = %+v; texts: %q", rows, tester.Texts())
	}
}

func TestRelayAlertScreenshots(t *testing.T) {
	for _, language := range []string{"en", "zh-Hans"} {
		for _, dark := range []bool{false, true} {
			appearance := "light"
			if dark {
				appearance = "dark"
			}
			variant := language + "-" + appearance
			t.Run(variant, func(t *testing.T) {
				a, platform := testApp(t)
				platform.SetLocale(language)
				source, target, _ := relayUITargets(t, a)
				a.relay = &relayDialog{source: source, target: target, sourceClient: source.client, confirmedClient: target.client,
					host: target.Host, from: "/srv/builds", dir: "/srv/releases", canonical: "/srv/releases", names: []string{"release.tar", "checksums.txt"},
					folders: []string{"current", "previous", "staged"}}
				newTester := func(view func(*ui.Context), width, height int) *ui.Tester {
					tester := ui.NewTesterWith(view, width, height, ui.TesterOptions{Dark: dark})
					tester.SetLocale(language)
					tester.Frame()
					return tester
				}
				main := newTester(a.View, 1280, 820)
				wantTexts(t, main, "send form", locales.RelaySend(), "Artifact store", "current")
				saveShot(t, main, "relay-send-"+variant+".png")
				a.relay = nil
				job := &transferJob{id: 1, sess: source, dir: serverRelay, src: "release.tar", dstDir: target.files.cwd,
					state: transferRunning, done: 32 << 20, total: 72 << 20, speed: 8 << 20,
					relay: &relayJob{target: target, sourceName: source.label(), label: target.label(), mode: relayViaApp, files: 7, totalFiles: 12, measured: true}}
				a.transfers.jobs = []*transferJob{job}
				a.openPanelTab(source, tabTransfers)
				main.Frame()
				saveShot(t, main, "relay-transfers-full-"+variant+".png")
				a.active, a.bottomTab = 0, tabTransfers
				a.terminalPct = 64
				main.Frame()
				saveShot(t, main, "relay-transfers-short-"+variant+".png")
				a.bottomSide, a.rightWidth = config.PanelRight, 460
				main.Frame()
				wantTexts(t, main, "narrow transfer", "release.tar", locales.RelayProgress(locales.RelayViaApp(), 7, 12))
				saveShot(t, main, "relay-transfers-narrow-"+variant+".png")
				a.settings.AlertRules = &map[string]config.AlertRules{"": {Unreachable: true, CPU: 85, CPUMinutes: 4, Memory: 90, Disk: 90, Load: 1.5, Services: true}}
				a.optionsNav.Page = pageAlerts
				options := newTester(func(c *ui.Context) { useTheme(c); a.optionsView(c) }, 760, 600)
				wantTexts(t, options, "alert options", locales.AlertTitle(), locales.AlertCPUPercent(), locales.AlertLoadFactor())
				saveShot(t, options, "alerts-options-"+variant+".png")
				at := time.Date(2026, 11, 25, 8, 30, 5, 0, time.Local)
				a.alerts.events = []alertEvent{
					{id: 1, at: at, host: target.label(), kind: alertService, value: "docs-worker.service", acked: true},
					{id: 2, at: at.Add(time.Minute), host: source.label(), kind: alertDisk, value: "/srv · 94.1%"},
					{id: 3, at: at.Add(2 * time.Minute), host: source.label(), kind: alertCPU, value: "91.2%"},
				}
				alerts := newTester(func(c *ui.Context) { useTheme(c); a.alertList(c) }, 820, 460)
				wantTexts(t, alerts, "alert values", "91.2%", "docs-worker.service")
				saveShot(t, alerts, "alerts-list-"+variant+".png")
			})
		}
	}
}
