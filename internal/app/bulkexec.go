package app

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"finallyshell/internal/config"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

const windowBulkExec WindowKind = "bulk-exec"

const (
	bulkSetupPage = iota
	bulkResultsPage
	bulkHistoryPage
)

var bulkStatuses = []bulkStatus{bulkWaiting, bulkConnecting, bulkRunning, bulkOK, bulkFailed, bulkTimeout, bulkCancelled, bulkSkipped}

type bulkExportRequest struct {
	path     string
	rows     []bulkResult
	csv      bool
	expected os.FileInfo
}

type bulkView struct {
	page         int
	selected     map[string]bool
	folded       map[string]bool
	search       string
	code         editor.State
	output       editor.State
	timeout      string
	concurrency  string
	root, stop   bool
	keepScript   bool
	optionsOpen  bool
	run          bulkRunner
	confirm      *bulkRequest
	confirmRetry bool
	closeAsk     bool
	cancel       context.CancelFunc
	closed       bool
	rows         []bulkResult
	checked      map[string]bool
	last         bulkRequest
	current      string
	outputRow    string
	filter       bulkStatus
	sort         ui.SortOrder
	table        ui.ListState
	history      []bulkHistoryEntry
	historyErr   error
	err          error
	notice       string
	exporting    bool
	replace      *bulkExportRequest
}

func newBulkView(hosts []*config.Host, script string) *bulkView {
	view := &bulkView{
		selected: make(map[string]bool), folded: make(map[string]bool), checked: make(map[string]bool),
		timeout: "60", concurrency: "10", sort: ui.SortOrder{Column: "host"},
		code:   editor.State{Buffer: editor.NewBuffer(script), Font: codeFont, NoAutoClose: true, NoFolding: true},
		output: editor.State{Buffer: editor.NewBuffer(""), Font: codeFont, ReadOnly: true, NoFolding: true, SoftWrap: true},
	}
	for _, host := range hosts {
		if bulkEligible(host) {
			view.selected[host.ID] = true
		}
	}
	return view
}

func bulkEligible(host *config.Host) bool {
	return host != nil && !host.Deleted() && host.Kind == config.KindSSH && execAllowed(host)
}

func (a *App) bulkManagerHosts(manager *managerState, items []managerItem) []*config.Host {
	var hosts []*config.Host
	seen := make(map[string]bool)
	add := func(host *config.Host) {
		if bulkEligible(host) && !seen[host.ID] {
			seen[host.ID] = true
			hosts = append(hosts, host)
		}
	}
	for _, item := range items {
		if item.isHost() {
			add(a.hosts.Host(item.host))
		} else {
			for _, host := range a.folderHosts(manager, item.folder) {
				add(host)
			}
		}
	}
	return hosts
}

func (a *App) openBulkExec(hosts []*config.Host, script string) {
	a.showBulkExec(hosts, script, nil)
}

func (a *App) showBulkExec(hosts []*config.Host, script string, run bulkRunner) {
	if a.windowOpen(windowBulkExec) {
		a.plat.FocusWindow(windowBulkExec)
		return
	}
	view := newBulkView(hosts, script)
	view.run = run
	view.history, view.historyErr = bulkLoadHistory(bulkHistoryDir())
	canClose := func() bool {
		if view.cancel != nil {
			view.closeAsk = true
			a.redraw()
			return false
		}
		return true
	}
	a.openWindow(WindowSpec{Kind: windowBulkExec, Title: locales.BulkTitle, Width: 1060, Height: 690,
		MinWidth: 460, MinHeight: 350, OwnKeys: true, CloseRequested: canClose, Closed: func() {
			view.closed = true
			if view.cancel != nil {
				view.cancel()
			}
		}}, func(c *ui.Context) {
		if view.confirm == nil && !view.closeAsk && !view.optionsOpen && view.replace == nil && view.outputRow == "" &&
			a.closeWindowShortcut(c) && canClose() {
			a.closeWindow(windowBulkExec)
			return
		}
		a.bulkWindow(c, view)
	})
}

func (a *App) bulkTargets(view *bulkView) []bulkTarget {
	var targets []bulkTarget
	for _, host := range a.hosts.Live() {
		if view.selected[host.ID] && bulkEligible(host) {
			targets = append(targets, bulkTarget{id: host.ID, name: host.Title(), address: host.Addr, user: host.User, port: host.PortOr22(),
				endpoint: host.User + "@" + net.JoinHostPort(host.Addr, strconv.Itoa(host.PortOr22()))})
		}
	}
	return targets
}

func (a *App) bulkManagerItem(menu *ui.Menu, manager *managerState, items []managerItem, later *[]func()) {
	hosts := a.bulkManagerHosts(manager, items)
	if menu.Item(locales.BulkOpen()).Disabled(len(hosts) == 0).Subtitle(bulkReason(len(hosts) == 0, locales.BulkNoTargets())).Chosen() {
		*later = append(*later, func() { a.openBulkExec(hosts, "") })
	}
}

func (a *App) bulkCurrentHost(session *Session) []*config.Host {
	if session != nil && !session.isLocal() && bulkEligible(session.Host) {
		return []*config.Host{session.Host}
	}
	return nil
}

func (a *App) bulkQuickEntry(c *ui.Context, session *Session) {
	if ui.Button(c, locales.BulkTitle()).Label(locales.BulkOpen()).Tooltip(locales.BulkOpen()).Clicked() {
		a.openBulkExec(a.bulkCurrentHost(session), "")
	}
}

func (a *App) bulkDraft(view *bulkView) (bulkRequest, string) {
	request := bulkRequest{targets: a.bulkTargets(view), root: view.root, stop: view.stop, keepScript: view.keepScript}
	if len(request.targets) == 0 {
		return request, locales.BulkNoTargets()
	}
	request.script = strings.ReplaceAll(view.code.Buffer.String(), "\r\n", "\n")
	if strings.TrimSpace(request.script) == "" || strings.ContainsRune(request.script, 0) || len(request.script) > 256<<10 {
		return request, locales.BulkScriptInvalid()
	}
	if bulkContainsSecret(request.script, nil) {
		return request, locales.BulkScriptSecret()
	}
	for _, secret := range a.passwords {
		if secret != "" && strings.Contains(request.script, secret) {
			return request, locales.BulkScriptSecret()
		}
	}
	seconds, _ := strconv.Atoi(view.timeout)
	request.concurrency, _ = strconv.Atoi(view.concurrency)
	if seconds < 1 || seconds > 86400 || request.concurrency < 1 || request.concurrency > bulkMaxWorkers {
		return request, locales.BulkLimitsInvalid()
	}
	request.timeout = time.Duration(seconds) * time.Second
	return request, ""
}

func bulkRetry(view *bulkView, failed bool) bulkRequest {
	request := view.last
	request.targets = nil
	for _, result := range view.rows {
		if failed && (result.status == bulkFailed || result.status == bulkTimeout) || !failed && view.checked[result.target.id] && result.status.final() {
			request.targets = append(request.targets, result.target)
		}
	}
	return request
}

func (a *App) bulkStart(view *bulkView, request bulkRequest, retry bool) {
	if view.cancel != nil || len(request.targets) == 0 {
		return
	}
	view.page, view.err, view.notice = bulkResultsPage, nil, ""
	view.filter, view.current = "", ""
	view.checked = make(map[string]bool)
	if !retry {
		view.rows = nil
	}
	indices := make(map[string]int, len(request.targets))
	for index, result := range view.rows {
		indices[result.target.id] = index
	}
	for _, target := range request.targets {
		row := bulkResult{target: target, status: bulkWaiting, exit: -1}
		if index, ok := indices[target.id]; ok {
			view.rows[index] = row
		} else {
			indices[target.id] = len(view.rows)
			view.rows = append(view.rows, row)
		}
	}
	view.last = request
	ctx, cancel := context.WithCancel(context.Background())
	view.cancel = cancel
	prompts := newBulkPrompts()
	run := view.run
	if run == nil {
		run = a.bulkSSH(prompts)
	}
	started := time.Now()
	go func() {
		counts := make(map[bulkStatus]int)
		var lock sync.Mutex
		storeScript := request.keepScript
		bulkSchedule(ctx, request, run, func(result bulkResult) {
			if result.status.final() {
				lock.Lock()
				counts[result.status]++
				storeScript = storeScript && !errors.Is(result.err, errBulkScriptSecret)
				lock.Unlock()
			}
			a.post(func() {
				if !view.closed {
					view.rows[indices[result.target.id]] = result
				}
			})
		})
		cancel()
		for _, password := range prompts.shared {
			clear(password)
		}
		entry := bulkHistoryEntry{Time: started, Summary: counts, Timeout: int(request.timeout / time.Second),
			Workers: request.concurrency, Root: request.root, Stop: request.stop}
		for _, target := range request.targets {
			entry.IDs = append(entry.IDs, target.id)
		}
		if storeScript {
			entry.Script = request.script
		}
		err := bulkSaveHistory(bulkHistoryDir(), entry)
		a.post(func() {
			view.cancel = nil
			view.historyErr = err
			if err == nil {
				view.history = append([]bulkHistoryEntry{entry}, view.history...)
				view.history = view.history[:min(len(view.history), bulkHistoryLimit)]
			}
		})
	}()
}

func (a *App) bulkRestore(view *bulkView, entry bulkHistoryEntry) {
	view.selected = make(map[string]bool)
	view.search = ""
	for _, id := range entry.IDs {
		if bulkEligible(a.hosts.Host(id)) {
			view.selected[id] = true
		}
	}
	view.code.Buffer.Set(entry.Script)
	view.timeout, view.concurrency = strconv.Itoa(entry.Timeout), strconv.Itoa(entry.Workers)
	view.root, view.stop, view.keepScript = entry.Root, entry.Stop, entry.Script != ""
	view.page, view.err, view.notice = bulkSetupPage, nil, ""
	if missing := len(entry.IDs) - len(view.selected); missing > 0 {
		view.notice = locales.BulkHistoryMissing(missing)
	}
}

func (a *App) bulkPickExport(view *bulkView, csv bool) {
	if view.exporting || view.cancel != nil || len(view.rows) == 0 {
		return
	}
	view.exporting, view.err, view.notice = true, nil, ""
	rows := slices.Clone(view.rows)
	title := locales.BulkExport()
	format, extension := "Text", "txt"
	if csv {
		format, extension = "CSV", "csv"
	}
	go func() {
		path, err := a.plat.SaveFile(windowBulkExec, title, "bulk-results."+extension, format, []string{extension})
		var info os.FileInfo
		if err == nil && path != "" {
			parent, resolveErr := filepath.EvalSymlinks(filepath.Dir(path))
			if resolveErr == nil {
				path = filepath.Join(parent, filepath.Base(path))
			}
			abs, absErr := filepath.Abs(path)
			history, historyErr := filepath.Abs(bulkHistoryDir())
			rel, relErr := filepath.Rel(history, abs)
			if resolved, linkErr := filepath.EvalSymlinks(history); linkErr == nil {
				rel, relErr = filepath.Rel(resolved, abs)
			}
			if absErr != nil || historyErr != nil || relErr == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				err = errors.New(locales.BulkExportInvalid())
			} else {
				info, err = os.Lstat(path)
				if errors.Is(err, os.ErrNotExist) {
					err = nil
				} else if err == nil && !info.Mode().IsRegular() {
					err = errors.New(locales.BulkExportInvalid())
				}
			}
		}
		a.post(func() {
			view.exporting, view.err = false, err
			if err != nil || path == "" || view.closed {
				return
			}
			request := &bulkExportRequest{path: path, rows: rows, csv: csv, expected: info}
			if info != nil {
				view.replace = request
			} else {
				a.bulkExport(view, request)
			}
		})
	}()
}

func (a *App) bulkExport(view *bulkView, request *bulkExportRequest) {
	view.exporting = true
	go func() {
		var report string
		var err error
		if request.csv {
			report, err = bulkCSV(request.rows)
		} else {
			report = bulkReport(request.rows)
		}
		if err == nil {
			err = bulkWriteExport(request.path, report, request.expected)
		}
		a.post(func() {
			view.exporting, view.err = false, err
			if err == nil {
				view.notice = locales.BulkExportDone()
			}
		})
	}()
}
