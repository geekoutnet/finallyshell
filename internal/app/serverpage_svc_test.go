package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"finallyshell/internal/monitor"
	"finallyshell/locales"
	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"
)

const systemDemoJobs = "# Scheduled reports\nMAILTO=\"\"\n*/5 * * * * /usr/local/bin/report --incremental\n0 3 * * * /usr/local/bin/archive\n#finallyshell-disabled# @reboot /usr/local/bin/warmup\n"

type systemFakeCall struct{ command, input string }

type systemFakeExit int

func (status systemFakeExit) ExitStatus() int { return int(status) }
func (status systemFakeExit) Error() string   { return fmt.Sprintf("exit status %d", status) }

type systemFake struct {
	root, writes chan systemFakeCall
	ended        chan struct{}
	reads        atomic.Int32
}

func systemTestApp(t *testing.T) (*App, *fakePlatform, *ui.Tester, *Session, *systemFake) {
	t.Helper()
	a, p := testApp(t)
	s := demoSession(a, "demo-server", "192.0.2.12")
	s.Host.User = "operator"
	tt := ui.NewTester(a.View, 1280, 820)
	p.tt = tt
	a.active = 0
	fake := &systemFake{root: make(chan systemFakeCall, 32), writes: make(chan systemFakeCall, 16), ended: make(chan struct{}, 16)}
	s.server.services.run = fake.run
	s.overview.memory.privilegeRunner = fake.privilege
	t.Cleanup(func() { a.stopPoll(&s.server.services.poll); a.stopPoll(&s.server.logs.poll) })
	a.openPanelTab(s, tabSystem)
	tt.Frame()
	p.until(t, "service list", func() bool { return s.server.services.loaded })
	return a, p, tt, s, fake
}

func (fake *systemFake) run(ctx context.Context, command string, input io.Reader, progress func(string)) (string, string, error) {
	if input != nil {
		data, _ := io.ReadAll(input)
		fake.writes <- systemFakeCall{command, string(data)}
		if progress != nil {
			progress("scheduled action finished\n")
		}
		return "", "", nil
	}
	switch {
	case strings.Contains(command, "'list-units'"):
		fake.reads.Add(1)
		return `[{"unit":"report.service","load":"loaded","active":"active","sub":"running","description":"Report worker"},{"unit":"queue.service","load":"loaded","active":"failed","sub":"failed","description":"Queue worker"},{"unit":"archive.service","load":"loaded","active":"inactive","sub":"dead","description":"Archive worker"}]`, "", nil
	case strings.Contains(command, "'list-unit-files'"):
		return "report.service enabled\nqueue.service disabled\narchive.service static\n", "", nil
	case strings.Contains(command, "'show'"):
		return "MainPID=402\nMemoryCurrent=4702208\nCanReload=yes\nId=report.service\n", "", nil
	case strings.Contains(command, "'status'"):
		return "● report.service - Report worker\n   Loaded: loaded (/usr/lib/systemd/system/report.service; enabled)\n   Active: active (running) since Wed 2026-11-25 09:49:56 UTC\n Main PID: 402 (report-worker)\n   Memory: 4.5M\n", "", nil
	case strings.Contains(command, "'cat'"):
		return "[Service]\nExecStart=/usr/local/bin/report\n", "", nil
	case strings.Contains(command, "'date' '+%z'"):
		return "+0800\n", "", nil
	case strings.Contains(command, "'crontab' '-l'"):
		return systemDemoJobs, "", nil
	case strings.Contains(command, "'journalctl'") || strings.Contains(command, "'tail'"):
		if progress != nil {
			progress("2026-11-25T10:04:36+0800 report[402]: starting report worker\n2026-11-25T10:04:37+0800 report[402]: warning: slow storage\n2026-11-25T10:04:38+0800 report[402]: error: failed to write report\n2026-11-25T10:04:39+0800 report[402]: retry succeeded\n")
			<-ctx.Done()
			fake.ended <- struct{}{}
			return "", "", ctx.Err()
		}
	case strings.Contains(command, "'test' '-r'"):
		return "", "permission denied", systemFakeExit(1)
	}
	return "", "", nil
}

func (fake *systemFake) privilege(ctx context.Context, request monitor.PrivilegeRequest) (monitor.PrivilegeResult, error) {
	if request.Probe {
		return monitor.PrivilegeResult{Stdout: "root"}, nil
	}
	var input []byte
	if request.Stdin != nil {
		input, _ = io.ReadAll(request.Stdin)
	}
	fake.root <- systemFakeCall{request.Action, string(input)}
	switch {
	case strings.Contains(request.Action, "'head' '-c'"):
		return monitor.PrivilegeResult{Started: true, Stdout: "present\n[Service]\nRestart=on-failure\nRestartSec=3s\n"}, nil
	case strings.Contains(request.Action, "'crontab' '-u' 'root' '-l'") && request.Stdin == nil:
		return monitor.PrivilegeResult{Started: true, Stdout: systemDemoJobs}, nil
	case strings.Contains(request.Action, "'reset-failed'"):
		return monitor.PrivilegeResult{Started: true, Stderr: "simulated unit failure"}, errors.New("exit 1")
	case strings.Contains(request.Action, "'tail'"):
		<-ctx.Done()
		fake.ended <- struct{}{}
		return monitor.PrivilegeResult{Started: true}, ctx.Err()
	}
	return monitor.PrivilegeResult{Started: true, Stdout: "operation completed\n"}, nil
}

func systemClick(t *testing.T, tt *ui.Tester, label string) {
	t.Helper()
	for _, role := range []ui.Role{ui.RoleButton, ui.RoleMenuButton} {
		if item, ok := tt.AccessByRole(role, label); ok && !item.Disabled {
			tt.ClickAt(item.Bounds.X+item.Bounds.W/2, item.Bounds.Y+item.Bounds.H/2)
			tt.Frame()
			return
		}
	}
	t.Fatalf("no enabled %q; visible %q", label, tt.Texts())
}

func systemWantMenuDisabled(t *testing.T, tt *ui.Tester, label string) {
	t.Helper()
	if err := tt.ChooseMenuItem(label); err == nil || !strings.Contains(err.Error(), "is disabled") {
		t.Fatalf("menu item %q should be disabled: %v", label, err)
	}
}

func systemNext(t *testing.T, p *fakePlatform, calls <-chan systemFakeCall) systemFakeCall {
	t.Helper()
	p.until(t, "fake execution", func() bool { return len(calls) > 0 })
	return <-calls
}

func TestSystemServicesUI(t *testing.T) {
	a, p, tt, s, fake := systemTestApp(t)
	page := &s.server.services
	wantTexts(t, tt, "services", "queue.service", "report.service", locales.SvcFailedCount(1))
	page.filter = "QUEUE"
	page.sort = ui.SortOrder{Column: "name", Descending: true}
	tt.Frame()
	if len(page.shown()) != 1 || page.shown()[0].Name != "queue.service" || tt.HasText("report.service") {
		t.Fatal("service filter did not apply")
	}
	page.filter = ""
	if page.shown()[0].Name != "report.service" {
		t.Fatal("service sort did not apply")
	}
	page.selected.Add("queue.service")
	tt.Frame()
	systemClick(t, tt, locales.SvcActions())
	systemWantMenuDisabled(t, tt, locales.SvcReload())
	chooseDockerAction(t, tt, locales.SvcReset())
	wantTexts(t, tt, "reset confirmation", "queue.service", locales.SvcResetNote())
	systemClick(t, tt, locales.Cancel())
	if len(fake.root) != 0 {
		t.Fatal("cancel ran privileged action")
	}
	before := fake.reads.Load()
	systemClick(t, tt, locales.SvcActions())
	chooseDockerAction(t, tt, locales.SvcReset())
	systemClick(t, tt, locales.SvcConfirm())
	call := systemNext(t, p, fake.root)
	want, _ := monitor.ServiceActionCommand("reset-failed", "queue.service")
	if call.command != want || call.input != "" {
		t.Fatalf("call = %#v", call)
	}
	p.until(t, "systemctl failure and refresh", func() bool { return page.dialog.done && fake.reads.Load() > before })
	if page.dialog.failure != "simulated unit failure" {
		t.Fatalf("failure = %q", page.dialog.failure)
	}
	systemClick(t, tt, locales.Close())
	a.serviceDetails(s, "report.service")
	p.until(t, "unit details", func() bool { return page.dialog.body != nil })
	wantTexts(t, tt, "details", locales.SvcDetails(), locales.SvcUnitFile(), locales.SvcProperties())
}

func TestSystemServiceActionsConfirm(t *testing.T) {
	a, platform, tester, session, fake := systemTestApp(t)
	page := &session.server.services
	units := map[string]string{
		"start": "queue.service", "stop": "report.service", "restart": "report.service", "reload": "report.service",
		"enable": "queue.service", "disable": "report.service", "enable-now": "queue.service", "mask": "queue.service", "unmask": "masked.service", "reset-failed": "queue.service",
	}
	for _, action := range serviceActions {
		t.Run(action.id, func(t *testing.T) {
			unit := units[action.id]
			row := monitor.ServiceUnit{Name: unit, Enabled: "masked", Load: "masked"}
			for _, item := range page.rows {
				if item.Name == unit {
					row = item
					break
				}
			}
			before := fake.reads.Load()
			a.confirmService(session, []monitor.ServiceUnit{row}, action)
			tester.Frame()
			wantTexts(t, tester, "confirmation", unit, action.consequence())
			systemClick(t, tester, locales.Cancel())
			if len(fake.root) != 0 {
				t.Fatal("cancel entered escalation")
			}
			a.confirmService(session, []monitor.ServiceUnit{row}, action)
			tester.Frame()
			systemClick(t, tester, locales.SvcConfirm())
			call := systemNext(t, platform, fake.root)
			want, _ := monitor.ServiceActionCommand(action.id, unit)
			if call.command != want {
				t.Fatalf("command = %q, want %q", call.command, want)
			}
			platform.until(t, "service refresh", func() bool { return page.dialog.done && fake.reads.Load() > before })
			systemClick(t, tester, locales.Close())
		})
	}
}

func systemType(t *testing.T, tester *ui.Tester, label, text string) {
	t.Helper()
	field, ok := tester.AccessByRole(ui.RoleTextField, label)
	if !ok {
		t.Fatalf("missing field %q", label)
	}
	tester.ClickAt(field.Bounds.X+field.Bounds.W/2, field.Bounds.Y+field.Bounds.H/2)
	tester.Key(ui.Ctrl, ui.KeyA)
	tester.Type(text)
	tester.Frame()
}

func TestSystemOverrideEditorConfirm(t *testing.T) {
	a, platform, tester, session, fake := systemTestApp(t)
	page := &session.server.services
	a.serviceOverride(session, "report.service")
	read := systemNext(t, platform, fake.root)
	path, _ := monitor.ServiceOverridePath("report.service")
	wantRead, _ := monitor.SystemFileReadCommand(path)
	if read.command != wantRead {
		t.Fatalf("read = %#v", read)
	}
	platform.until(t, "editable drop-in", func() bool { return page.dialog.body != nil })
	const edited = "[Service]\nRestartSec=5s\n"
	systemType(t, tester, locales.SvcEdit(), edited)
	systemClick(t, tester, locales.Save())
	wantTexts(t, tester, "drop-in confirmation", path, locales.SvcOverrideNote())
	systemClick(t, tester, locales.Cancel())
	if len(fake.root) != 0 {
		t.Fatal("cancel wrote override")
	}
	systemClick(t, tester, locales.Save())
	systemClick(t, tester, locales.SvcConfirm())
	write := systemNext(t, platform, fake.root)
	wantWrite, _ := monitor.SystemFileWriteCommand(path, "[Service]\nRestart=on-failure\nRestartSec=3s\n", true)
	if write.command != wantWrite || write.input != edited {
		t.Fatalf("write = %#v", write)
	}
	platform.until(t, "write done", func() bool { return page.dialog.done })
}

func TestSystemCronUI(t *testing.T) {
	_, p, tt, s, fake := systemTestApp(t)
	s.server.section = serverSecCron
	tt.Frame()
	page := &s.server.cron
	p.until(t, "job list", func() bool { return page.usable })
	wantTexts(t, tt, "cron", locales.CronAdd(), "*/5 * * * *", "/usr/local/bin/report --incremental")
	systemClick(t, tt, locales.CronAdd())
	wantTexts(t, tt, "job editor", locales.CronMinute(), locales.CronHour())
	systemClick(t, tt, locales.Close())
	page.picked.Add(2)
	tt.Frame()
	systemClick(t, tt, locales.SvcActions())
	systemWantMenuDisabled(t, tt, locales.SvcEnable())
	chooseDockerAction(t, tt, locales.SvcDisable())
	systemClick(t, tt, locales.Cancel())
	if len(fake.writes) != 0 {
		t.Fatal("cancel wrote crontab")
	}
	systemClick(t, tt, locales.SvcActions())
	chooseDockerAction(t, tt, locales.SvcDisable())
	systemClick(t, tt, locales.SvcConfirm())
	call := systemNext(t, p, fake.writes)
	want, _ := monitor.CronWriteCommand("", systemDemoJobs, true)
	if call.command != want || !strings.Contains(call.input, "\n"+monitor.CronDisabled+"*/5") || !strings.Contains(call.input, "MAILTO=") {
		t.Fatalf("write = %#v", call)
	}
	p.until(t, "cron refresh", func() bool { return page.dialog.done && page.usable })
}

func TestSystemCronWriteActions(t *testing.T) {
	a, platform, tester, session, fake := systemTestApp(t)
	session.server.section = serverSecCron
	tester.Frame()
	page := &session.server.cron
	platform.until(t, "job list", func() bool { return page.usable })
	systemClick(t, tester, locales.CronAdd())
	systemType(t, tester, locales.CronCommand(), "/usr/local/bin/rotate --quiet")
	systemType(t, tester, locales.CronComment(), "Rotate reports")
	systemType(t, tester, locales.CronLogPath(), "/var/log/rotate reports.log")
	systemClick(t, tester, locales.Save())
	systemClick(t, tester, locales.Cancel())
	if len(fake.writes) != 0 {
		t.Fatal("cancel wrote table")
	}
	systemClick(t, tester, locales.Save())
	systemClick(t, tester, locales.SvcConfirm())
	call := systemNext(t, platform, fake.writes)
	want, _ := monitor.CronWriteCommand("", systemDemoJobs, true)
	const appended = "# Rotate reports\n*/5 * * * * /usr/local/bin/rotate --quiet >> '/var/log/rotate reports.log' 2>&1\n"
	if call.command != want || call.input != systemDemoJobs+appended {
		t.Fatalf("add = %#v", call)
	}
	platform.until(t, "add readback", func() bool { return page.dialog.done && page.usable })
	systemClick(t, tester, locales.Close())
	page.selected = 1
	a.refreshServerCron(session)
	rootRead := systemNext(t, platform, fake.root)
	wantRootRead, _ := monitor.CronReadTransport("root")
	if rootRead.command != wantRootRead {
		t.Fatalf("root read = %#v", rootRead)
	}
	platform.until(t, "root table", func() bool { return page.usable })
	job := page.rows[0]
	a.cronChange(session, []monitor.CronJob{job}, "remove", locales.CronRemove())
	tester.Frame()
	systemClick(t, tester, locales.Cancel())
	if len(fake.root) != 0 {
		t.Fatal("cancel removed root job")
	}
	a.cronChange(session, []monitor.CronJob{job}, "remove", locales.CronRemove())
	tester.Frame()
	systemClick(t, tester, locales.SvcConfirm())
	rootWrite := systemNext(t, platform, fake.root)
	wantRootWrite, _ := monitor.CronWriteCommand("root", systemDemoJobs, true)
	if rootWrite.command != wantRootWrite || rootWrite.input != monitor.ChangeCron(systemDemoJobs, job.Line, "") {
		t.Fatalf("remove = %#v", rootWrite)
	}
	_ = systemNext(t, platform, fake.root)
	platform.until(t, "root readback", func() bool { return page.dialog.done && page.usable })
	systemClick(t, tester, locales.Close())
	a.cronRun(session, job)
	tester.Frame()
	systemClick(t, tester, locales.Cancel())
	if len(fake.root) != 0 {
		t.Fatal("cancel ran root job")
	}
	a.cronRun(session, job)
	tester.Frame()
	systemClick(t, tester, locales.SvcConfirm())
	rootRun := systemNext(t, platform, fake.root)
	wantRootRun, _ := monitor.CronRunCommand("root")
	if rootRun.command != wantRootRun || rootRun.input != job.Command+"\n" {
		t.Fatalf("manual run = %#v", rootRun)
	}
}

func TestSystemLogsUI(t *testing.T) {
	a, p, tt, s, fake := systemTestApp(t)
	a.openServiceJournal(s, "report.service")
	tt.Frame()
	page := &s.server.logs
	p.until(t, "following logs", func() bool { return len(page.lines) == 4 })
	systemClick(t, tt, locales.LogvPause())
	page.append([]string{"followed while paused"})
	if strings.Contains(page.logText(false), "followed while paused") {
		t.Fatal("pause did not freeze view")
	}
	systemClick(t, tt, locales.LogvResume())
	page.filter = "ERROR"
	if got := page.logText(false); !strings.Contains(got, "failed to write report") || strings.Contains(got, "warning") {
		t.Fatalf("filter = %q", got)
	}
	s.server.section = serverSecServices
	tt.Frame()
	p.until(t, "hidden stream stopped", func() bool { return len(fake.ended) > 0 })
	<-fake.ended
	a.setSystemLog(s, monitor.JournalSource{File: "/var/log/protected.log", Lines: 300})
	s.server.section = serverSecLogs
	tt.Frame()
	call := systemNext(t, p, fake.root)
	want, _ := monitor.JournalCommand(monitor.JournalSource{File: "/var/log/protected.log", Lines: 300})
	if !strings.HasSuffix(call.command, "\nexec "+want) || !strings.HasPrefix(call.command, "'test' '-f' '/var/log/protected.log'") {
		t.Fatalf("privileged follow = %q", call.command)
	}
	s.server.section = serverSecCron
	tt.Frame()
	p.until(t, "elevated stream stopped", func() bool { return len(fake.ended) > 0 })
}

func TestSystemLogsFilterAndBuffer(t *testing.T) {
	for _, sample := range []struct {
		line  string
		level int
	}{{"error: open failed", 3}, {"WARN: slow disk", 2}, {"archive succeeded", 1}, {"ordinary startup", 0}} {
		if got := systemLogLevel(sample.line, []string{"archive"}); got != sample.level {
			t.Errorf("%q = %d", sample.line, got)
		}
	}
	match, err := logMatcher(`^worker \d+$`, true, false)
	if err != nil || !match("Worker 42") || match("test Worker 42") {
		t.Fatal("regex matching failed")
	}
	if _, err := logMatcher("[", true, false); err == nil {
		t.Fatal("invalid regex accepted")
	}
	match, _ = logMatcher("Error", false, true)
	if match("error") || !match("Error!") {
		t.Fatal("case matching failed")
	}
	buffer := newSystemChunks(2)
	buffer.add("old\r\nlatest\npass")
	buffer.add("word=[fixture credential]\n")
	if got := strings.Join(buffer.take(false), "\n"); got != "latest\npassword=[redacted]" {
		t.Fatalf("buffer = %q", got)
	}
}

func TestSystemSectionsScreenshots(t *testing.T) {
	if os.Getenv("SHOT_DIR") == "" {
		t.Skip("SHOT_DIR is not set")
	}
	for _, language := range []string{"en", "zh-Hans"} {
		t.Run(language, func(t *testing.T) {
			a, p, tt, s, _ := systemTestApp(t)
			p.SetLocale(language)
			done := make(chan struct{})
			term, err := terminal.New(terminal.Options{Conn: idleConn{done}, Font: a.termFont, Theme: a.termTheme, DarkTheme: a.termTheme})
			if err != nil {
				close(done)
				t.Fatal(err)
			}
			t.Cleanup(func() { close(done); term.Close() })
			s.setTerm(term)
			term.Feed([]byte("operator@demo:~$ "))
			for _, mode := range []string{"light", "dark"} {
				tt.SetDark(mode == "dark")
				shot := func(scene string) {
					tt.Frame()
					tt.Frame()
					saveShot(t, tt, "system-"+scene+"-"+language+"-"+mode+".png")
				}
				for _, section := range []struct {
					id   int
					name string
				}{{serverSecServices, "services"}, {serverSecLogs, "logs"}, {serverSecCron, "cron"}} {
					a.openPanelTab(s, tabSystem)
					s.server.section = section.id
					if section.id == serverSecLogs {
						a.openServiceJournal(s, "report.service")
						s.server.logs.keywords = "retry"
					}
					tt.Frame()
					if section.id == serverSecLogs {
						p.until(t, "log rows", func() bool { return len(s.server.logs.lines) >= 4 })
					}
					if section.id == serverSecCron {
						p.until(t, "cron rows", func() bool { return s.server.cron.usable })
					}
					shot(section.name + "-full")
					a.active = 0
					a.openServerPage(s, section.id)
					shot(section.name + "-short")
					a.setPanelOnRight(true)
					shot(section.name + "-right")
					a.setPanelOnRight(false)
				}
				a.openPanelTab(s, tabSystem)
				s.server.section = serverSecServices
				a.serviceDetails(s, "report.service")
				p.until(t, "details", func() bool { return s.server.services.dialog.body != nil })
				shot("unit-details")
				a.serviceOverride(s, "report.service")
				p.until(t, "override editor", func() bool { return s.server.services.dialog.body != nil })
				shot("override-editor")
				s.server.services.dialog = nil
				s.server.section = serverSecCron
				a.cronEditor(s, &s.server.cron.rows[0])
				shot("cron-editor")
				s.server.cron.dialog = nil
			}
		})
	}
}
