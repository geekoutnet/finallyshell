package app

import (
	"context"
	"encoding/csv"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"finallyshell/internal/config"
	"finallyshell/internal/monitor"
	"finallyshell/internal/sshx"
	"finallyshell/locales"
	"github.com/egoist/mygo/ui"
)

func TestBulkScheduler(t *testing.T) {
	targets := []bulkTarget{{id: "one"}, {id: "two"}, {id: "three"}, {id: "four"}}
	started := make(chan string, len(targets))
	release := make(chan struct{})
	results := make(chan bulkResult, len(targets)*3)
	var active, peak atomic.Int32
	done := make(chan struct{})
	go func() {
		defer close(done)
		bulkSchedule(t.Context(), bulkRequest{targets: targets, concurrency: 2, timeout: time.Second},
			func(ctx context.Context, target bulkTarget, _ bulkRequest, running func()) bulkResult {
				defer active.Add(-1)
				count := active.Add(1)
				for previous := peak.Load(); count > previous && !peak.CompareAndSwap(previous, count); previous = peak.Load() {
				}
				running()
				started <- target.id
				<-release
				return bulkResult{exit: 0}
			}, func(result bulkResult) { results <- result })
	}()
	for range 2 {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("workers did not start")
		}
	}
	close(release)
	<-done
	if peak.Load() != 2 {
		t.Fatalf("peak concurrency = %d", peak.Load())
	}
	close(results)
	states := make(map[string][]bulkStatus)
	for result := range results {
		states[result.target.id] = append(states[result.target.id], result.status)
	}
	for _, target := range targets {
		statuses := states[target.id]
		if len(statuses) != 3 || statuses[0] != bulkConnecting || statuses[1] != bulkRunning || statuses[2] != bulkOK {
			t.Fatalf("%s: %v", target.id, statuses)
		}
	}
}

func TestBulkRootUsesSharedEscalation(t *testing.T) {
	a, _ := testApp(t)
	plan := bulkConnectPlan{host: &config.Host{ID: "one", User: "ops", Addr: "example.invalid"}, opts: sshx.Options{Password: "sample-auth-material"}}
	var calls int
	script := "printf '%s\\n' 'it\"s working'\n"
	stdout, _, err := a.bulkRoot(t.Context(), plan, newBulkPrompts(), func(_ context.Context, request monitor.PrivilegeRequest) (monitor.PrivilegeResult, error) {
		calls++
		if request.Probe {
			return monitor.PrivilegeResult{Stdout: "sudo\n"}, nil
		}
		if request.Action != "exec 'sh' '-s'" || strings.Contains(request.Command, script) || strings.Contains(request.Command, plan.opts.Password) || !strings.Contains(request.Command, "sudo -k -S") {
			t.Fatal("unexpected elevation command")
		}
		data, readErr := io.ReadAll(request.Stdin)
		if readErr != nil || string(data) != script || string(request.Password) != plan.opts.Password {
			t.Fatal("script or credentials did not reach the expected stdin path")
		}
		return monitor.PrivilegeResult{Started: true, Authenticated: true, Stdout: "ready\n"}, nil
	}, script)
	if calls != 2 || err != nil || stdout != "ready\n" {
		t.Fatalf("calls = %d, output = %q, error = %v", calls, stdout, err)
	}
}

func TestBulkRootRememberAndUseForAll(t *testing.T) {
	a, platform := testApp(t)
	hosts := bulkDemoHosts(a)
	a.openBulkExec(hosts, "printf 'ready'")
	const sample = "fixture-only-auth-material"
	prompts := newBulkPrompts()
	result := make(chan error, 1)
	run := func(_ context.Context, request monitor.PrivilegeRequest) (monitor.PrivilegeResult, error) {
		if request.Probe {
			return monitor.PrivilegeResult{Stdout: "sudo"}, nil
		}
		if string(request.Password) != sample || strings.Contains(request.Command, sample) {
			return monitor.PrivilegeResult{}, errors.New("incorrect test credential path")
		}
		return monitor.PrivilegeResult{Started: true, Authenticated: true, Stdout: sample}, nil
	}
	go func() {
		for _, host := range hosts[:2] {
			stdout, _, err := a.bulkRoot(t.Context(), bulkConnectPlan{host: host.Clone()}, prompts, run, "printf 'ready'")
			if err != nil {
				result <- err
				return
			}
			if strings.Contains(stdout, sample) {
				result <- errors.New("credential leaked into output")
				return
			}
		}
		result <- nil
	}()
	platform.until(t, "elevation prompt", func() bool { return a.windowOpen(windowBulkPassword) })
	dialog := platform.show(t, windowBulkPassword)
	typeInto(t, dialog, locales.PrivPassword(), sample)
	if dialog.HasText(sample) {
		t.Fatal("password is visible")
	}
	clickNamed(t, dialog, locales.BulkUsePasswordAll())
	clickNamed(t, dialog, locales.PrivRemember())
	clickNamed(t, dialog, locales.PrivRun())
	// One remembered password, besides the index the app lock's reset
	// keeps of them.
	remembered := func() int {
		n := 0
		for key := range platform.secrets() {
			if isPrivilegeCredentialKey(key) {
				n++
			}
		}
		return n
	}
	platform.until(t, "both hosts elevated", func() bool { return len(result) == 1 && remembered() == 1 })
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	if a.windowOpen(windowBulkPassword) {
		t.Fatal("shared password prompted again")
	}
}

func TestBulkUnknownHostNoPrompt(t *testing.T) {
	a, platform := testApp(t)
	host := &config.Host{ID: "unknown", Name: "Untrusted", Addr: "example.invalid", User: "ops"}
	a.hosts.Add(host)
	a.passwords[host.ID] = "sample-auth-material"
	view := newBulkView([]*config.Host{host}, "printf 'safe sample\\n'")
	request, problem := a.bulkDraft(view)
	if problem != "" {
		t.Fatal(problem)
	}
	a.bulkStart(view, request, false)
	platform.until(t, "untrusted host", func() bool { return view.cancel == nil })
	if len(view.rows) != 1 || view.rows[0].status != bulkFailed || bulkErrorText(view.rows[0].err) != locales.BulkHostKey() || len(a.windows) != 0 {
		t.Fatalf("unexpected result: %+v, windows: %v", view.rows, a.windows)
	}
	if _, err := os.Stat(filepath.Join(config.Dir(), "known_hosts")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("strict lookup must not create known_hosts")
	}
}

func TestBulkHistoryAndExportBackup(t *testing.T) {
	dir := t.TempDir()
	historyDir := filepath.Join(dir, "history")
	for index := range 24 {
		entry := bulkHistoryEntry{Time: time.Unix(int64(index), 0), IDs: []string{"host-id"}, Script: "printf 'demo\\n'", Summary: map[bulkStatus]int{bulkOK: 1}}
		if err := bulkSaveHistory(historyDir, entry); err != nil {
			t.Fatal(err)
		}
	}
	history, err := bulkLoadHistory(historyDir)
	if err != nil || len(history) != 20 || history[0].Time.Unix() != 23 || history[19].Time.Unix() != 4 || history[0].Script == "" {
		t.Fatalf("unexpected history: %+v; %v", history, err)
	}
	path := filepath.Join(dir, "results.csv")
	if err := bulkWriteExport(path, "old", nil); err != nil {
		t.Fatal(err)
	}
	if err := bulkWriteExport(path, "must not overwrite", nil); !errors.Is(err, os.ErrExist) {
		t.Fatalf("unconfirmed overwrite: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := bulkWriteExport(path, "new", info); err != nil {
		t.Fatal(err)
	}
	backups, err := filepath.Glob(path + ".bak-*")
	if err != nil || len(backups) != 1 {
		t.Fatalf("backup: %v, %v", backups, err)
	}
	old, _ := os.ReadFile(backups[0])
	newData, _ := os.ReadFile(path)
	if string(old) != "old" || string(newData) != "new" {
		t.Fatal("did not preserve old content or replace export")
	}
}

func bulkDemoHosts(a *App) []*config.Host {
	folder := a.hosts.AddFolder("Staging", "")
	var hosts []*config.Host
	for index, name := range []string{"API worker", "Batch worker", "Cache node", "Windows desktop"} {
		host := &config.Host{ID: name, Name: name, Folder: folder.ID, Addr: []string{"api.example.invalid", "batch.example.invalid", "cache.example.invalid", "desktop.example.invalid"}[index], User: "ops"}
		if index == 3 {
			host.Kind = config.KindRDP
		}
		a.hosts.Add(host)
		hosts = append(hosts, host)
	}
	return hosts
}

func TestBulkWindowConfirmsAndRetries(t *testing.T) {
	a, platform := testApp(t)
	hosts := bulkDemoHosts(a)
	view := newBulkView(nil, "printf '%s\\n' 'node ready'")
	calls := make(chan string, 5)
	view.run = func(_ context.Context, target bulkTarget, request bulkRequest, running func()) bulkResult {
		running()
		calls <- target.id + ":" + request.script
		if target.id == hosts[1].ID {
			return bulkResult{exit: 9, stderr: "fixture failure\nsecond line"}
		}
		return bulkResult{exit: 0, stdout: "fixture healthy\n"}
	}
	tester := ui.NewTester(func(c *ui.Context) { useTheme(c); a.bulkWindow(c, view) }, 1280, 820)
	platform.tt = tester
	tester.Frame()
	wantTexts(t, tester, "targets", locales.BulkTargets(), locales.BulkSSHOnly())
	if desktop, ok := tester.AccessByRole(ui.RoleCheckBox, hosts[3].Name); !ok || !desktop.Disabled {
		t.Fatalf("remote desktop can be selected: %+v", desktop)
	}
	view.search = "Staging"
	tester.Frame()
	wantTexts(t, tester, "folder matches", hosts[0].Name, hosts[1].Name)
	view.search = "cache"
	tester.Frame()
	if tester.HasText(hosts[0].Name) || !tester.HasText(hosts[2].Name) {
		t.Fatal("target search did not filter hosts")
	}
	clickNamed(t, tester, locales.BulkSelectMatches())
	if selected := a.bulkTargets(view); len(selected) != 1 || selected[0].id != hosts[2].ID {
		t.Fatalf("select matching hosts included hidden hosts: %v", selected)
	}
	view.search = ""
	tester.Frame()
	clickNamed(t, tester, "Staging")
	if len(a.bulkTargets(view)) != 3 {
		t.Fatalf("selection = %v", view.selected)
	}
	clickNamed(t, tester, locales.BulkRun())
	wantTexts(t, tester, "confirmation", locales.BulkConfirm(3), hosts[1].Target()+":22")
	clickNamed(t, tester, locales.Cancel())
	if len(calls) != 0 || view.cancel != nil {
		t.Fatal("cancelling started a run")
	}
	clickNamed(t, tester, locales.BulkRun())
	clickNamed(t, tester, locales.BulkRunNow())
	platform.until(t, "initial run", func() bool { return view.cancel == nil && len(view.history) == 1 })
	if len(calls) != 3 || bulkCounts(view.rows)[bulkFailed] != 1 || view.history[0].Script != "" {
		t.Fatalf("unexpected initial run: calls = %d, rows = %+v", len(calls), view.rows)
	}
	for range 3 {
		if call := <-calls; !strings.HasSuffix(call, ":printf '%s\\n' 'node ready'") {
			t.Fatalf("unexpected script: %q", call)
		}
	}
	wantTexts(t, tester, "results", "fixture healthy", "fixture failure", "2 ok · 1 failed")
	view.filter = bulkFailed
	tester.Frame()
	if got := bulkVisible(view); len(got) != 1 || got[0].target.id != hosts[1].ID {
		t.Fatalf("unexpected filter: %v", got)
	}
	clickNamed(t, tester, hosts[1].Name+" · "+locales.BulkFailed())
	clickNamed(t, tester, locales.BulkViewOutput())
	if !strings.Contains(view.output.Buffer.String(), "second line") {
		t.Fatal("full stderr missing from output viewer")
	}
	clickNamed(t, tester, locales.Close())
	clickNamed(t, tester, locales.BulkRetryFailed())
	wantTexts(t, tester, "retry confirmation", locales.BulkConfirm(1))
	clickNamed(t, tester, locales.BulkRunNow())
	platform.until(t, "retry", func() bool { return view.cancel == nil && len(view.history) == 2 })
	if len(calls) != 1 || !strings.HasPrefix(<-calls, hosts[1].ID+":") || bulkCounts(view.rows)[bulkOK] != 2 {
		t.Fatal("retry ran more than the failed host or replaced successful results")
	}
	clickNamed(t, tester, locales.ManagerSelectRow(hosts[0].Name))
	if request := bulkRetry(view, false); len(request.targets) != 1 || request.targets[0].id != hosts[0].ID {
		t.Fatalf("unexpected selected retry: %v", request.targets)
	}
	clickNamed(t, tester, locales.BulkRetrySelected())
	wantTexts(t, tester, "selected retry", locales.BulkConfirm(1), hosts[0].Target()+":22")
	clickNamed(t, tester, locales.Cancel())
	if len(calls) != 0 {
		t.Fatal("cancelled selected retry ran a command")
	}
	view.sort = ui.SortOrder{Column: "host", Descending: true}
	if ordered := bulkVisible(view); !slices.EqualFunc(ordered, []string{hosts[2].ID, hosts[1].ID, hosts[0].ID}, func(row bulkResult, id string) bool { return row.target.id == id }) {
		t.Fatalf("unexpected sort: %v", ordered)
	}
}

func TestBulkEntries(t *testing.T) {
	a, platform := testApp(t)
	hosts := bulkDemoHosts(a)
	a.showManager(false)
	manager := platform.show(t, WindowManager)
	manager.Frame()
	managerClickWith(t, manager, 0, hosts[0].Name)
	clickNamed(t, manager, locales.BulkTitle())
	bulk := platform.show(t, windowBulkExec)
	bulk.Frame()
	wantTexts(t, bulk, "manager selection", locales.BulkSelected(1))
	a.closeWindow(windowBulkExec)
	a.openBulkPalette(hosts[:1])
	palette := platform.show(t, "command-palette")
	palette.Frame()
	clickNamed(t, palette, locales.BulkOpen())
	bulk = platform.show(t, windowBulkExec)
	bulk.Frame()
	wantTexts(t, bulk, "command palette selection", locales.BulkSelected(1))
	a.closeWindow(windowBulkExec)
	session := demoSession(a, "Worker console", "worker.example.invalid")
	a.active = len(a.sessions) - 1
	a.openBottomTab(tabCommands)
	main := ui.NewTester(a.View, 1280, 820)
	main.Frame()
	clickNamed(t, main, locales.BulkOpen())
	bulk = platform.show(t, windowBulkExec)
	bulk.Frame()
	wantTexts(t, bulk, "commands selection", locales.BulkSelected(1), session.Host.Name)
}

func TestBulkDraftAndRedaction(t *testing.T) {
	a, _ := testApp(t)
	hosts := bulkDemoHosts(a)
	view := newBulkView(hosts[:1], "printf '%s\\n' 'name with spaces'")
	for _, test := range []struct {
		timeout, workers, script, problem string
	}{
		{"60", "50", "printf '%s\\n' 'name with spaces'", ""},
		{"0", "10", "true", locales.BulkLimitsInvalid()},
		{"60", "51", "true", locales.BulkLimitsInvalid()},
		{"60", "10", "true\x00", locales.BulkScriptInvalid()},
		{"60", "10", " ", locales.BulkScriptInvalid()},
		{"60", "10", strings.Repeat("x", 256<<10+1), locales.BulkScriptInvalid()},
		{"60", "10", "echo -----BEGIN PRIVATE KEY-----", locales.BulkScriptSecret()},
	} {
		view.timeout, view.concurrency = test.timeout, test.workers
		view.code.Buffer.Set(test.script)
		request, problem := a.bulkDraft(view)
		if problem != test.problem || problem == "" && request.script != test.script {
			t.Fatalf("unexpected validation result for %s/%s: %q", test.timeout, test.workers, problem)
		}
	}
	const sample = "fixture-secret-material"
	result := bulkResult{stdout: "with " + sample, stderr: "partial fixture-secret", truncated: true}
	bulkLimit(&result, []string{sample})
	if result.stdout != "with [redacted]" || result.stderr != "partial [redacted]" {
		t.Fatal("credential or partial credential was not removed")
	}
}

func TestBulkCancelAllAndClose(t *testing.T) {
	a, platform := testApp(t)
	hosts := bulkDemoHosts(a)
	started := make(chan struct{})
	ended := make(chan struct{})
	a.showBulkExec(hosts[:1], "printf 'fixture\\n'", func(ctx context.Context, _ bulkTarget, _ bulkRequest, running func()) bulkResult {
		running()
		close(started)
		<-ctx.Done()
		close(ended)
		return bulkResult{exit: -1, err: ctx.Err()}
	})
	tester := platform.show(t, windowBulkExec)
	platform.tt = tester
	tester.Frame()
	clickNamed(t, tester, locales.BulkRun())
	clickNamed(t, tester, locales.BulkRunNow())
	platform.until(t, "running status", func() bool {
		_, ok := tester.Access(hosts[0].Name + " · " + locales.BulkRunning())
		return ok
	})
	if platform.userClose(windowBulkExec) {
		t.Fatal("window closed without confirming cancellation")
	}
	tester.Frame()
	wantTexts(t, tester, "close confirmation", locales.BulkCloseRunning())
	clickNamed(t, tester, locales.Cancel())
	tester.RightClickAt(rowCenter(t, tester, hosts[0].Name+" · "+locales.BulkRunning()))
	tester.Frame()
	if view, ok := tester.Access(locales.BulkViewOutput()); !ok || !view.Disabled {
		t.Fatalf("output menu enabled while running: %+v", view)
	}
	tester.Key(0, ui.KeyEscape)
	clickNamed(t, tester, locales.BulkCancelAll())
	platform.until(t, "cancelled status and history", func() bool {
		history, _ := bulkLoadHistory(bulkHistoryDir())
		return tester.HasText(locales.BulkCancelled()) && len(history) == 1
	})
	select {
	case <-ended:
	default:
		t.Fatal("cancellation did not reach the runner")
	}
}

func TestBulkScreenshots(t *testing.T) {
	if os.Getenv("SHOT_DIR") == "" {
		t.Skip("SHOT_DIR is not set")
	}
	for _, lang := range []string{"en", "zh-Hans"} {
		for _, mode := range []string{"light", "dark"} {
			t.Run(lang+"/"+mode, func(t *testing.T) {
				a, platform := testApp(t)
				platform.SetLocale(lang)
				a.quick().AddCommand(&config.QuickCommand{Name: "Worker health", Text: "uptime\ndf -h /"}, "")
				hosts := bulkDemoHosts(a)
				view := newBulkView(hosts, "#!/bin/sh\nprintf '%s\\n' 'Checking worker health'\nuptime\ndf -h /\n")
				from := func(c *ui.Context) { useTheme(c); a.bulkWindow(c, view) }
				tester := ui.NewTester(from, 1280, 820)
				tester.SetDark(mode == "dark")
				shot := func(screen *ui.Tester, scene string) {
					screen.Frame()
					screen.Frame()
					saveShot(t, screen, "bulk-"+scene+"-"+lang+"-"+mode+".png")
				}
				shot(tester, "setup")
				compact := ui.NewTester(from, 460, 350)
				compact.SetDark(mode == "dark")
				shot(compact, "compact")
				main := ui.NewTester(a.View, 1280, 820)
				main.SetDark(mode == "dark")
				session := demoSession(a, "Worker console", "worker.example.invalid")
				a.active = len(a.sessions) - 1
				a.openBottomTab(tabCommands)
				shot(main, "commands-short")
				a.setPanelOnRight(true)
				shot(main, "commands-right")
				a.setPanelOnRight(false)
				a.openPanelTab(session, tabCommands)
				shot(main, "commands-full")
				request, problem := a.bulkDraft(view)
				if problem != "" {
					t.Fatal(problem)
				}
				view.last, view.page, view.cancel = request, bulkResultsPage, func() {}
				view.rows = []bulkResult{
					{target: request.targets[0], status: bulkRunning, exit: -1},
					{target: request.targets[1], status: bulkConnecting, exit: -1},
					{target: request.targets[2], status: bulkWaiting, exit: -1},
				}
				shot(tester, "running")
				view.cancel = nil
				view.rows[0] = bulkResult{target: request.targets[0], status: bulkOK, exit: 0, duration: 824 * time.Millisecond, stdout: "Checking worker health\nload average: 0.18, 0.24, 0.26\n/dev/vda1   80G   14G   66G  18% /\n"}
				view.rows[1] = bulkResult{target: request.targets[1], status: bulkFailed, exit: 7, duration: 2 * time.Second, stdout: "Checking worker health\n", stderr: "health-check: worker service is not ready\nCheck the service status before retrying.\n"}
				view.rows[2] = bulkResult{target: request.targets[2], status: bulkTimeout, exit: -1, duration: time.Minute, err: context.DeadlineExceeded}
				view.current, view.checked[hosts[1].ID] = hosts[1].ID, true
				shot(tester, "finished")
				shot(compact, "compact-results")
				view.outputRow = hosts[1].ID
				shot(tester, "output")
			})
		}
	}
}

func TestBulkSchedulerStopsAndCancels(t *testing.T) {
	for _, test := range []struct {
		name    string
		first   bulkStatus
		pending bulkStatus
		stop    bool
	}{
		{"timeout", bulkTimeout, bulkSkipped, true},
		{"failure", bulkFailed, bulkSkipped, true},
		{"cancel", bulkCancelled, bulkCancelled, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var finals []bulkStatus
			var calls int
			bulkSchedule(ctx, bulkRequest{targets: []bulkTarget{{id: "a"}, {id: "b"}}, concurrency: 1, timeout: 10 * time.Millisecond, stop: test.stop},
				func(ctx context.Context, _ bulkTarget, _ bulkRequest, running func()) bulkResult {
					calls++
					running()
					if test.first == bulkFailed {
						return bulkResult{exit: 7, err: errors.New("script failed")}
					}
					if test.first == bulkCancelled {
						cancel()
					}
					<-ctx.Done()
					return bulkResult{exit: -1, err: ctx.Err()}
				}, func(result bulkResult) {
					if result.status.final() {
						finals = append(finals, result.status)
					}
				})
			if calls != 1 || len(finals) != 2 || finals[0] != test.first || finals[1] != test.pending {
				t.Fatalf("calls = %d, final states = %v", calls, finals)
			}
		})
	}
}

func TestBulkCSV(t *testing.T) {
	rows := []bulkResult{{target: bulkTarget{name: `one,"two"`}, status: bulkFailed, exit: 7, duration: 1234 * time.Millisecond, stdout: "line 1\nline 2, \"quoted\""},
		{target: bulkTarget{name: "=1+1"}, status: bulkOK, exit: 0, stdout: "  @SUM(1,2)"}}
	data, err := bulkCSV(rows)
	if err != nil {
		t.Fatal(err)
	}
	records, err := csv.NewReader(strings.NewReader(data)).ReadAll()
	if err != nil || len(records) != 3 {
		t.Fatalf("records = %v, error = %v", records, err)
	}
	if row := records[1]; row[0] != rows[0].target.name || row[1] != "failed" || row[2] != "7" || row[3] != "1.234s" || !strings.Contains(row[4], rows[0].stdout) {
		t.Fatalf("first row = %q", row)
	}
	if row := records[2]; row[0] != "'=1+1" || !strings.HasPrefix(row[4], "'") {
		t.Fatalf("unescaped formula: %q", row)
	}
}
