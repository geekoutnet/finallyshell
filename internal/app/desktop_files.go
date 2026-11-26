package app

import (
	"cmp"
	"context"
	"errors"
	"io/fs"
	"maps"
	"math"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"finallyshell/internal/config"
	"finallyshell/internal/remotefs"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
	"github.com/pkg/sftp"
)

func init() {
	for _, spec := range []desktopApp{
		{id: appThisPC, icon: "monitor", tint: ui.Hex("#4f7cff"), name: locales.DesktopAppThisPC, order: 10},
		{id: appFiles, icon: "folder", tint: ui.Hex("#f0a630"), name: locales.DesktopAppFiles, order: 20},
	} {
		spec.category, spec.onDesktop, spec.pinned = desktopCatSystem, true, true
		spec.size, spec.minSize = desktopSize{W: 960, H: 600}, desktopSize{W: 600, H: 420}
		spec.view, spec.opened, spec.closing = (*App).desktopFilesView, (*App).desktopFilesOpened, (*App).desktopFilesClosing
		registerDesktopApp(spec)
	}
}

type desktopFilesItem struct {
	remotefs.Entry
	target string
}

type desktopFilesExec func(context.Context, string, func(string)) (string, string, error)

type desktopFilesState struct {
	dir, home, address, query, deep string
	addressEdit                     bool
	history                         []string
	historyAt                       int
	entries                         []desktopFilesItem
	quick                           []string
	drives                          []desktopFilesDrive
	driveErr, err                   error
	free                            int64
	freeKnown, mountsRead           bool
	loading, limited                bool
	readID                          uint64
	readCancel                      context.CancelFunc
	searching                       bool
	client                          *sftp.Client
	run                             desktopFilesExec
	selected                        ui.Selection[string]
	row                             int
	list                            ui.ListState
	grid                            ui.GridState
	sidebar                         bool
	collapsed                       [3]bool
	view, sort                      int
	descending                      bool
	inline                          *desktopFilesPrompt
	modal                           *desktopFilesPrompt
	job                             *desktopFilesJob
	// busySince is when the window started reading or working, the zero
	// time while it is not: its note shows after desktopFilesBusyDelay.
	busySince time.Time
	conflict  *desktopFilesConflict
	reveal    string
	marquee   desktopFilesMarquee
	edits     map[*editTab]time.Time
}

const (
	desktopFilesDetails = iota
	desktopFilesList
	desktopFilesMedium
	desktopFilesLarge
)

func (w *desktopWindow) explorer() *desktopFilesState { return w.state.(*desktopFilesState) }

func (a *App) desktopFilesOpened(w *desktopWindow, arg any) {
	dir, _ := arg.(string)
	f := &desktopFilesState{sidebar: true, row: -1, free: -1}
	w.state = f
	if w.app.id == appThisPC {
		dir = ""
	}
	a.desktopFilesRead(w, dir, w.app.id == appFiles && dir == "", true)
}

func (a *App) desktopFilesClosing(w *desktopWindow) bool {
	f := w.explorer()
	if f.readCancel != nil {
		f.readCancel()
	}
	if f.job != nil {
		f.job.cancel()
	}
	if f.modal != nil && f.modal.properties != nil {
		f.modal.properties.cancel()
	}
	f.edits = nil
	return true
}

func desktopFilesLife(s *Session) context.Context {
	if s.ctx != nil {
		return s.ctx
	}
	return context.Background()
}

func desktopFilesRunner(s *Session, f *desktopFilesState) desktopFilesExec {
	if f.run != nil {
		return f.run
	}
	if !execAllowed(s.Host) || s.client == nil {
		return nil
	}
	client := s.client
	return func(ctx context.Context, script string, progress func(string)) (string, string, error) {
		return desktopFilesSSHExec(ctx, client, script, progress)
	}
}

func (a *App) desktopFilesRead(w *desktopWindow, dir string, home, visit bool) {
	a.desktopFilesReadLocation(w, dir, home, visit, -1)
}

func (a *App) desktopFilesReadLocation(w *desktopWindow, dir string, home, visit bool, historyIndex int) {
	f, s := w.explorer(), w.session
	if dir != "" && !desktopFilesPath(dir) {
		f.err = errors.New(locales.DesktopFilesBadPath())
		return
	}
	if f.readCancel != nil {
		f.readCancel()
	}
	f.readID++
	id := f.readID
	sc, run := s.sftp, desktopFilesRunner(s, f)
	f.client = sc
	if sc == nil {
		f.loading, f.err = false, errors.New(locales.DesktopFilesDisconnected())
		f.entries, f.quick, f.deep = nil, nil, ""
		f.selected.Clear()
		if len(f.history) == 0 {
			f.dir, f.address = dir, dir
		}
		return
	}
	f.loading, f.searching, f.err = true, false, nil
	ctx, cancel := context.WithTimeout(desktopFilesLife(s), 25*time.Second)
	f.readCancel = cancel
	knownHome := f.home
	readMounts := dir == "" && !home || !f.mountsRead
	go func() {
		defer cancel()
		if knownHome == "" {
			knownHome, _ = sc.Getwd()
			if !desktopFilesPath(knownHome) {
				knownHome = "/"
			}
		}
		if home {
			dir = knownHome
		}
		var entries []desktopFilesItem
		var quick []string
		var err error
		free := int64(-1)
		freeKnown := false
		if dir == "" {
			for _, target := range []string{knownHome, "/var/log", "/etc", "/var/www", "/opt", "/tmp"} {
				if info, statErr := sc.Stat(target); statErr == nil && info.IsDir() && !slices.Contains(quick, target) {
					quick = append(quick, target)
				}
			}
		} else {
			var list []remotefs.Entry
			list, err = remotefs.ReadDir(sc, dir)
			for _, entry := range list {
				if desktopFilesName(entry.Name) {
					entries = append(entries, desktopFilesItem{entry, path.Join(dir, entry.Name)})
				}
			}
			if _, ok := sc.HasExtension("statvfs@openssh.com"); ok && err == nil {
				if stats, statErr := sc.StatVFS(dir); statErr == nil {
					if stats.Frsize > 0 && stats.Bavail <= math.MaxInt64/stats.Frsize {
						free, freeKnown = int64(stats.Bavail*stats.Frsize), true
					}
				}
			}
		}
		a.post(func() {
			if f.readID != id || s.sftp != sc {
				return
			}
			f.loading, f.home = false, knownHome
			if err != nil {
				f.err = &dirError{dir, err}
				return
			}
			if visit || dir != f.dir {
				if historyIndex >= 0 {
					f.historyAt = historyIndex
				} else if len(f.history) == 0 || f.history[f.historyAt] != dir {
					f.history = append(f.history[:min(len(f.history), f.historyAt+1)], dir)
					f.historyAt = len(f.history) - 1
				}
				f.query, f.row = "", -1
				f.selected.Clear()
				f.list.ScrollTo(0, ui.Start)
				f.grid.ScrollTo(0, ui.Start)
			}
			f.dir, f.address, f.entries, f.quick, f.deep = dir, dir, entries, quick, ""
			f.free, f.freeKnown, f.limited = free, freeKnown, false
			if f.reveal != "" {
				f.selected.Clear()
				f.selected.Add(f.reveal)
				f.reveal = ""
			}
			if visit && dir != "" {
				a.desktopFilesRecent(s, dir)
			}
			w.title = ""
			if dir != "" {
				w.title = path.Base(dir)
			}
		})
		if readMounts && run != nil {
			out, stderr, mountErr := run(ctx, desktopFilesMountCommand, nil)
			drives := desktopFilesParseMounts(out)
			drives = slices.DeleteFunc(drives, func(drive desktopFilesDrive) bool {
				info, err := sc.Stat(drive.mount)
				return err == nil && !info.IsDir()
			})
			a.post(func() {
				if f.readID == id && s.sftp == sc {
					f.drives, f.mountsRead, f.driveErr = drives, mountErr == nil, desktopFilesCommandError(stderr, mountErr)
				}
			})
		}
	}()
}

func (a *App) desktopFilesHistory(w *desktopWindow, index int) {
	f := w.explorer()
	if index >= 0 && index < len(f.history) && index != f.historyAt && !f.loading {
		a.desktopFilesReadLocation(w, f.history[index], false, true, index)
	}
}

func (a *App) desktopFilesRefresh(w *desktopWindow) {
	f := w.explorer()
	if f.deep != "" && desktopFilesRunner(w.session, f) != nil {
		a.desktopFilesSearch(w, f.deep)
		return
	}
	a.desktopFilesRead(w, f.dir, len(f.history) == 0 && f.dir == "" && w.app.id == appFiles, false)
}

func (a *App) desktopFilesRefreshDir(s *Session, dirs ...string) {
	if d := a.desktopOf(s); d != nil {
		for _, w := range d.windows {
			if f, ok := w.state.(*desktopFilesState); ok && (slices.Contains(dirs, f.dir) || f.deep != "") {
				a.desktopFilesRefresh(w)
			}
		}
	}
	for _, dir := range dirs {
		a.refreshIfShowing(s, dir)
	}
}

func (a *App) desktopFilesCheckEdits(w *desktopWindow) {
	f := w.explorer()
	for tab, seen := range f.edits {
		if tab.savedAt.After(seen) {
			f.edits[tab] = tab.savedAt
			a.desktopLog(w.session, w.app.id, locales.Save()+": "+tab.path, nil)
			a.desktopFilesRefreshDir(w.session, path.Dir(tab.path))
		}
		if !slices.Contains(a.fe.tabs, tab) {
			delete(f.edits, tab)
		}
	}
}

func (a *App) desktopFilesHost(s *Session) config.DesktopHost {
	if a.settings.Desktop == nil || s.Host == nil {
		return config.DesktopHost{}
	}
	return a.settings.Desktop.Hosts[s.Host.ID]
}

func (a *App) desktopFilesChangeHost(s *Session, edit func(*config.DesktopHost)) {
	if s.Host == nil || s.Host.ID == "" {
		return
	}
	next := config.DesktopSettings{}
	if a.settings.Desktop != nil {
		next = *a.settings.Desktop
	}
	next.Hosts = maps.Clone(next.Hosts)
	if next.Hosts == nil {
		next.Hosts = map[string]config.DesktopHost{}
	}
	host := next.Hosts[s.Host.ID]
	host.Favorites, host.Recent, host.Notes = slices.Clone(host.Favorites), slices.Clone(host.Recent), maps.Clone(host.Notes)
	edit(&host)
	next.Hosts[s.Host.ID] = host
	a.changeSettings(func() { a.settings.Desktop = &next })
}

func (a *App) desktopFilesRecent(s *Session, target string) {
	if recent := a.desktopFilesHost(s).Recent; len(recent) > 0 && recent[0] == target {
		return
	}
	a.desktopFilesChangeHost(s, func(host *config.DesktopHost) {
		host.Recent = slices.DeleteFunc(host.Recent, func(p string) bool { return p == target })
		host.Recent = append([]string{target}, host.Recent...)
		host.Recent = slices.Clip(host.Recent[:min(20, len(host.Recent))])
	})
}

func (a *App) desktopFilesFavorite(s *Session, target string, enabled bool) {
	a.desktopFilesChangeHost(s, func(host *config.DesktopHost) {
		if enabled && !slices.Contains(host.Favorites, target) {
			host.Favorites = append(host.Favorites, target)
		} else if !enabled {
			host.Favorites = slices.DeleteFunc(host.Favorites, func(p string) bool { return p == target })
		}
	})
}

func (a *App) desktopFilesNote(s *Session, target, note string) {
	if a.desktopFilesHost(s).Notes[target] == note {
		return
	}
	a.desktopFilesChangeHost(s, func(host *config.DesktopHost) {
		if note == "" {
			delete(host.Notes, target)
			return
		}
		if host.Notes == nil {
			host.Notes = map[string]string{}
		}
		host.Notes[target] = note
	})
}

func (f *desktopFilesState) visible() []desktopFilesItem {
	query := strings.ToLower(f.query)
	shown := make([]desktopFilesItem, 0, len(f.entries))
	for _, entry := range f.entries {
		if f.deep != "" || strings.Contains(strings.ToLower(entry.Name), query) {
			shown = append(shown, entry)
		}
	}
	slices.SortFunc(shown, func(first, second desktopFilesItem) int {
		if first.IsDir() != second.IsDir() {
			if first.IsDir() {
				return -1
			}
			return 1
		}
		order := 0
		switch f.sort {
		case 1:
			order = cmp.Compare(first.Size, second.Size)
		case 2:
			order = first.ModTime.Compare(second.ModTime)
		case 3:
			order = strings.Compare(strings.ToLower(path.Ext(first.Name)), strings.ToLower(path.Ext(second.Name)))
		}
		if order == 0 {
			order = strings.Compare(strings.ToLower(first.Name), strings.ToLower(second.Name))
		}
		if order == 0 {
			order = strings.Compare(first.target, second.target)
		}
		if f.descending {
			return -order
		}
		return order
	})
	return shown
}

func (f *desktopFilesState) chosen(shown []desktopFilesItem) []desktopFilesItem {
	var items []desktopFilesItem
	for _, item := range shown {
		if f.selected.Has(item.target) {
			items = append(items, item)
		}
	}
	return items
}

func (a *App) desktopFilesSearch(w *desktopWindow, query string) {
	f, s := w.explorer(), w.session
	run, dir := desktopFilesRunner(s, f), f.dir
	if run == nil || !desktopFilesPath(dir) || strings.TrimSpace(query) == "" || strings.ContainsAny(query, "\x00\r\n") {
		return
	}
	if f.readCancel != nil {
		f.readCancel()
	}
	f.readID++
	id := f.readID
	f.loading, f.searching, f.err = true, true, nil
	ctx, cancel := context.WithTimeout(desktopFilesLife(s), time.Minute)
	f.readCancel = cancel
	go func() {
		defer cancel()
		out, stderr, err := run(ctx, desktopFilesSearchCommand(dir, query), nil)
		items, limited := desktopFilesSearchItems(out)
		a.post(func() {
			if id != f.readID {
				return
			}
			f.loading, f.searching = false, false
			if err != nil {
				if !errors.Is(err, context.Canceled) {
					f.err = desktopFilesCommandError(stderr, err)
				}
				return
			}
			f.entries, f.limited, f.deep = items, limited, query
			f.row = -1
			f.selected.Clear()
			f.list.ScrollTo(0, ui.Start)
			if stderr != "" && !limited {
				f.err = errors.New(locales.DesktopFilesSearchPartial(strings.TrimSpace(stderr)))
			}
		})
	}()
}

func (f *desktopFilesState) cancelSearch() {
	f.readID++
	f.readCancel()
	f.readCancel = nil
	f.loading, f.searching = false, false
}

func desktopFilesSearchItems(text string) ([]desktopFilesItem, bool) {
	fields := strings.Split(text, "\x00")
	var items []desktopFilesItem
	for len(fields) > 7 {
		group := fields[:7]
		fields = fields[7:]
		if !desktopFilesPath(group[6]) {
			continue
		}
		if len(items) >= desktopFilesSearchLimit {
			return items, true
		}
		size, _ := strconv.ParseInt(group[1], 10, 64)
		stamp, _ := strconv.ParseFloat(group[2], 64)
		perm, _ := strconv.ParseUint(group[3], 8, 12)
		uid, _ := strconv.ParseUint(group[4], 10, 32)
		gid, _ := strconv.ParseUint(group[5], 10, 32)
		mode := desktopFilesGoMode(uint32(perm))
		if group[0] == "d" {
			mode |= fs.ModeDir
		} else if group[0] == "l" {
			mode |= fs.ModeSymlink
		} else if group[0] != "f" {
			mode |= fs.ModeIrregular
		}
		items = append(items, desktopFilesItem{remotefs.Entry{Name: path.Base(group[6]), Mode: mode, Link: group[0] == "l", Size: size, ModTime: time.Unix(int64(stamp), 0), UID: uint32(uid), GID: uint32(gid)}, group[6]})
	}
	return items, false
}
