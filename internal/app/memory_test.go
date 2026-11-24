package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"finallyshell/internal/monitor"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

const memoryBefore = `MemTotal: 984064 kB
MemFree: 112640 kB
MemAvailable: 184320 kB
Buffers: 61440 kB
Cached: 498688 kB
SReclaimable: 12288 kB
Shmem: 16384 kB
Dirty: 2048 kB
CommitLimit: 2048000 kB
Committed_AS: 2560000 kB
SwapTotal: 2097152 kB
SwapFree: 1546240 kB
SwapCached: 22528 kB
pswpin 100
pswpout 120
swappiness 60
compact_memory 1
`

type memoryRequest struct {
	script string
	reply  chan memoryReply
}

type memoryReply struct {
	stdout, stderr string
	err            error
}

func memoryApp(t *testing.T, width int) (*fakePlatform, *ui.Tester, chan memoryRequest) {
	t.Helper()
	a, p := testApp(t)
	session := demoSession(a, "web-1", "45.56.78.90")
	a.active = 0
	session.overview.info, session.overview.read = demoSysInfo(), true
	session.stats.MemUsed, session.stats.MemTotal = 781<<20, 961<<20
	session.stats.SwapUsed, session.stats.SwapTotal = 538<<20, 2<<30
	var err error
	session.stats.Memory, err = monitor.ReadMemory(t.Context(), func(context.Context, string) (string, string, error) {
		return memoryBefore, "", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	session.stats.Memory.Rates = [2]monitor.SwapRate{{PagesPerSecond: 3.5, Known: true}, {PagesPerSecond: 0, Known: true}}
	requests := make(chan memoryRequest, 4)
	session.overview.memoryRunner = func(ctx context.Context, script string) (string, string, error) {
		request := memoryRequest{script: script, reply: make(chan memoryReply, 1)}
		requests <- request
		select {
		case reply := <-request.reply:
			return reply.stdout, reply.stderr, reply.err
		case <-ctx.Done():
			return "", "", ctx.Err()
		case <-t.Context().Done():
			return "", "", t.Context().Err()
		}
	}
	height := 690
	if width < 600 {
		height = 850
	}
	tt := ui.NewTester(func(c *ui.Context) {
		useTheme(c)
		a.overviewPanel(c)
	}, width, height)
	p.tt = tt
	tt.Frame()
	tt.Frame()
	return p, tt, requests
}

func nextMemoryRequest(t *testing.T, p *fakePlatform, requests chan memoryRequest, expected string) memoryRequest {
	t.Helper()
	var request memoryRequest
	p.until(t, "memory command", func() bool {
		select {
		case request = <-requests:
			return true
		default:
			return false
		}
	})
	if !strings.Contains(request.script, expected) {
		t.Fatalf("script missing %q: %s", expected, request.script)
	}
	return request
}

func TestMemoryOverview(t *testing.T) {
	for _, width := range []int{1040, 460} {
		for _, language := range []string{"en", "zh-Hans"} {
			p, tt, _ := memoryApp(t, width)
			p.SetLocale(language)
			for _, dark := range []bool{false, true} {
				tt.SetDark(dark)
				tt.Frame()
				wantTexts(t, tt, "memory card", locales.MemoryPhysical(), "961 MB", "180 MB", "543 MB", "16 MB", "2.0 MB", "2.44 GB / 1.95 GB (125%)", "538 MB / 2.00 GB", locales.MemoryPagesRate("3.5"), locales.MemoryPagesRate("0.0"), "60", locales.OverviewTopProcs())
				card, ok := tt.AccessByRole(ui.RoleGroup, locales.SysMemory())
				if !ok || card.Bounds.W < float32(width-35) || card.Bounds.X+card.Bounds.W > float32(width) {
					t.Errorf("memory card at width %d: %+v", width, card.Bounds)
				}
				appearance := "light"
				if dark {
					appearance = "dark"
				}
				saveShot(t, tt, fmt.Sprintf("memory-%d-%s-%s.png", width, language, appearance))
			}
		}
	}
}

func TestMemoryOverviewClean(t *testing.T) {
	p, tt, requests := memoryApp(t, 1040)
	clickButton(t, tt, locales.MemoryFree())
	wantTexts(t, tt, "confirmation", locales.MemoryDropWarning())
	saveShot(t, tt, "memory-confirm-en.png")
	clickButton(t, tt, locales.Cancel())
	if len(requests) != 0 || tt.HasText(locales.MemoryDropWarning()) {
		t.Fatal("cancel started an action or left the dialog open")
	}

	clickButton(t, tt, locales.MemoryFree())
	clickButton(t, tt, locales.MemoryDropCaches())
	read := nextMemoryRequest(t, p, requests, "cat /proc/meminfo")
	if button, ok := tt.AccessByRole(ui.RoleButton, locales.MemoryFree()); !ok || !button.Disabled {
		t.Fatal("free memory should be disabled while running")
	}
	read.reply <- memoryReply{stdout: memoryBefore}
	clean := nextMemoryRequest(t, p, requests, "sync || exit $?\necho 3 > /proc/sys/vm/drop_caches")
	clean.reply <- memoryReply{}
	read = nextMemoryRequest(t, p, requests, "cat /proc/meminfo")
	after := strings.NewReplacer("184320 kB", "606208 kB", "498688 kB", "76800 kB", "112640 kB", "534528 kB").Replace(memoryBefore)
	read.reply <- memoryReply{stdout: after}
	p.until(t, "memory result", func() bool { return tt.HasText(locales.MemoryAvailableChange("180 MB", "592 MB")) })
	wantTexts(t, tt, "memory result", locales.MemoryResultChange("543 MB", "131 MB", "538 MB", "538 MB"))
	saveShot(t, tt, "memory-result-en.png")
}

func openMemoryActions(t *testing.T, tt *ui.Tester) {
	t.Helper()
	more, ok := tt.AccessByRole(ui.RoleMenuButton, locales.MemoryActions())
	if !ok {
		t.Fatal("no memory actions menu")
	}
	tt.ClickAt(more.Bounds.X+more.Bounds.W/2, more.Bounds.Y+more.Bounds.H/2)
	tt.Frame()
}

func TestMemoryOverviewDenied(t *testing.T) {
	p, tt, requests := memoryApp(t, 460)
	clickButton(t, tt, locales.MemoryFree())
	clickButton(t, tt, locales.MemoryDropCaches())
	nextMemoryRequest(t, p, requests, "cat /proc/meminfo").reply <- memoryReply{stdout: memoryBefore}
	nextMemoryRequest(t, p, requests, "sudo -n -- sh -c").reply <- memoryReply{
		stdout: "---FINALLYSHELL:memory-sudo---\n", stderr: "sudo: a password is required", err: errors.New("exit status 1"),
	}
	nextMemoryRequest(t, p, requests, "cat /proc/meminfo").reply <- memoryReply{stdout: memoryBefore}
	p.until(t, "permission error", func() bool { return tt.HasText(locales.MemoryNeedsRoot()) })
	wantTexts(t, tt, "error dialog", "sudo: a password is required")
	clickButton(t, tt, locales.Close())
	if tt.HasText(locales.MemoryNeedsRoot()) || tt.HasText(locales.MemoryAvailableChange("180 MB", "180 MB")) {
		t.Fatal("close left an error or a success message for a failed operation")
	}
}
