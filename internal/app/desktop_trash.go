package app

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"path"
	"slices"
	"strings"
	"time"

	"finallyshell/internal/config"
	"finallyshell/internal/monitor"
	"finallyshell/internal/widgets"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
	"golang.org/x/crypto/ssh"
)

func init() {
	registerDesktopApp(desktopApp{
		id: appTrash, icon: "trash-2", tint: ui.Hex("#458e87"), name: locales.DesktopAppTrash,
		category: desktopCatSystem, order: 40, onDesktop: true, pinned: true,
		size: desktopSize{W: 900, H: 540}, minSize: desktopSize{W: 430, H: 320}, single: true,
		view: (*App).desktopTrashView, opened: (*App).desktopTrashOpened,
		closing: func(_ *App, w *desktopWindow) bool {
			if state, ok := w.state.(*desktopTrashState); ok && state.cancel != nil {
				state.cancel()
			}
			return true
		},
	})
}

type desktopTrashRunners struct {
	user, root, cachedRoot monitor.TrashRunner
	loginRoot              bool
}

type desktopTrashState struct {
	client         *ssh.Client
	runners        desktopTrashRunners
	items          []monitor.TrashItem
	selected       map[trashItemID]bool
	query, message string
	sortBy         trashColumn
	ascending      bool
	loaded, dirty  bool
	rootVisible    bool
	working        string
	cancel         context.CancelFunc
	question       *desktopTrashQuestion
	conflict       *desktopTrashConflict
	// widths are the widths the user dragged the columns to.
	widths widgets.ColumnWidths
}

type trashItemID struct {
	store, name string
	root        bool
}

func trashID(item monitor.TrashItem) trashItemID {
	return trashItemID{item.Store, item.Name, item.Root}
}

type desktopTrashQuestion struct {
	items []monitor.TrashItem
	title string
	purge bool
}

type desktopTrashConflict struct {
	item   monitor.TrashItem
	answer chan monitor.TrashRestoreMode
}

func (a *App) desktopTrashRunners(s *Session) desktopTrashRunners {
	if s == nil || !systemReady(s, s.client) || s.client == nil {
		return desktopTrashRunners{}
	}
	client := s.client
	priv := a.sessionPrivilege(s).runner
	options := monitor.PrivilegeOptions{ScriptStdin: true, Interrupt: true}
	return desktopTrashRunners{
		user: monitor.TrashSSHRunner(client),
		root: func(ctx context.Context, script string) (string, string, error) {
			return priv.Run(ctx, script, options)
		},
		cachedRoot: func(ctx context.Context, script string) (string, string, error) {
			return priv.RunCached(ctx, script, options)
		},
		loginRoot: client.User() == "root",
	}
}

func (a *App) desktopTrashOpened(w *desktopWindow, arg any) {
	state, ok := w.state.(*desktopTrashState)
	if !ok {
		state = &desktopTrashState{client: w.session.client, runners: a.desktopTrashRunners(w.session), sortBy: trashDate, selected: map[trashItemID]bool{}}
		w.state = state
	}
	if runners, ok := arg.(desktopTrashRunners); ok {
		state.runners = runners
	}
	if state.question == nil {
		a.desktopTrashRefresh(w, state, false)
	}
}

func (a *App) desktopTrashChanged(s *Session) {
	if desktop := a.desktopOf(s); desktop != nil {
		for _, win := range desktop.windowsOf(appTrash) {
			state := win.state.(*desktopTrashState)
			if state.cancel != nil || state.question != nil {
				state.dirty = true
			} else {
				a.desktopTrashRefresh(win, state, false)
			}
		}
	}
}

func trashDisplayError(err error) string {
	switch {
	case errors.Is(err, monitor.ErrTrashPath):
		return locales.DesktopTrashUnsafePath()
	case errors.Is(err, monitor.ErrTrashChanged):
		return locales.DesktopTrashItemChanged()
	default:
		return systemError("", err)
	}
}

func (a *App) desktopTrash(s *Session, paths []string, done func(error)) {
	a.desktopTrashTask(s, paths, nil, done)
}

func (a *App) desktopTrashTask(s *Session, paths []string, progress func(int, int), done func(error)) context.CancelFunc {
	if s.isPanelTab() {
		s = s.panelOf
	}
	return a.desktopTrashWith(s, paths, progress, done, a.desktopTrashRunners(s))
}

func (a *App) desktopTrashWith(s *Session, paths []string, progress func(int, int), done func(error), runners desktopTrashRunners) context.CancelFunc {
	if runners.user == nil {
		if done != nil {
			a.post(func() { done(errors.New(locales.DesktopTrashUnavailable())) })
		}
		return func() {}
	}
	paths = slices.Clone(paths)
	seen := make(map[string]bool, len(paths))
	paths = slices.DeleteFunc(paths, func(original string) bool {
		duplicate := seen[original]
		seen[original] = true
		return duplicate
	})
	notes := make(map[string]string, len(paths))
	if a.settings.Desktop != nil {
		for _, original := range paths {
			notes[original] = a.settings.Desktop.Hosts[s.Host.ID].Notes[original]
		}
	}
	type result struct {
		original string
		item     monitor.TrashItem
		err      error
	}
	ctx, cancel := context.WithTimeout(systemContext(s), 20*time.Minute)
	go func() {
		defer cancel()
		results := make([]result, 0, len(paths))
		for index, original := range paths {
			if err := ctx.Err(); err != nil {
				for _, pending := range paths[index:] {
					results = append(results, result{original: pending, err: err})
				}
				break
			}
			if progress != nil {
				a.post(func() { progress(index+1, len(paths)) })
			}
			item, err := monitor.TrashPut(ctx, runners.user, original, notes[original], false)
			if errors.Is(err, monitor.ErrTrashPermission) && runners.root != nil && !runners.loginRoot {
				item, err = monitor.TrashPut(ctx, runners.root, original, notes[original], true)
			}
			results = append(results, result{original, item, err})
		}
		a.post(func() {
			var failures []error
			var rootCount int
			var rootStore string
			for _, entry := range results {
				text := locales.DesktopTrashMoveFailed(entry.original)
				if entry.item.Store != "" {
					text = locales.DesktopTrashMoved(entry.original, entry.item.Store)
					if entry.item.Root || runners.loginRoot {
						rootCount++
						rootStore = entry.item.Store
					}
				}
				if entry.err != nil {
					failure := errors.New(trashDisplayError(entry.err))
					a.desktopLog(s, appTrash, text, failure)
					failures = append(failures, fmt.Errorf("%s: %w", entry.original, failure))
					continue
				}
				a.desktopLog(s, appTrash, text, nil)
			}
			if rootCount == 1 {
				a.notify(locales.DesktopTrashRootNotice(rootStore))
			} else if rootCount > 1 {
				a.notify(locales.DesktopTrashRootBatch(rootCount))
			}
			a.desktopTrashChanged(s)
			if done != nil {
				done(errors.Join(failures...))
			}
		})
	}()
	return cancel
}

func (a *App) desktopTrashRefresh(w *desktopWindow, state *desktopTrashState, requestRoot bool) {
	runners := state.runners
	if state.cancel != nil || runners.user == nil {
		return
	}
	ctx, cancel := context.WithTimeout(systemContext(w.session), 3*time.Minute)
	state.cancel, state.working, state.dirty = cancel, locales.DesktopTrashLoading(), false
	go func() {
		defer cancel()
		user, userErr := monitor.TrashList(ctx, runners.user, false)
		var root monitor.TrashInventory
		var rootErr error
		run := runners.cachedRoot
		if requestRoot {
			run = runners.root
		}
		rootRead := false
		if !runners.loginRoot && run != nil && ctx.Err() == nil {
			root, rootErr = monitor.TrashList(ctx, run, true)
			rootRead = rootErr == nil
		}
		if errors.Is(rootErr, monitor.ErrPrivilegeNotCached) {
			rootErr = nil
		}
		cancelled := ctx.Err() != nil
		a.post(func() {
			for _, orphan := range append(user.Orphans, root.Orphans...) {
				a.desktopLog(w.session, appTrash, locales.DesktopTrashCleaned(orphan), nil)
			}
			state.cancel, state.working = nil, ""
			if !cancelled && userErr == nil {
				state.loaded = true
				state.items = append(user.Items, root.Items...)
				remaining := make(map[trashItemID]bool)
				for _, item := range state.items {
					if state.selected[trashID(item)] {
						remaining[trashID(item)] = true
					}
				}
				state.selected = remaining
			} else if !cancelled && userErr != nil {
				state.loaded = false
				state.items = nil
				clear(state.selected)
			}
			if !cancelled {
				state.rootVisible = rootRead
			}
			if failure := errors.Join(userErr, rootErr); failure != nil && !cancelled {
				state.message = trashDisplayError(failure)
			}
			if warnings := append(user.Warnings, root.Warnings...); len(warnings) > 0 && state.message == "" && !cancelled {
				state.message = locales.DesktopTrashUnknownSize(warnings[0])
			}
			if state.dirty && !cancelled && a.desktopTrashCurrent(w, state) {
				a.desktopTrashRefresh(w, state, false)
			}
		})
	}()
}

func (a *App) desktopTrashCurrent(w *desktopWindow, state *desktopTrashState) bool {
	desktop := a.desktopOf(w.session)
	return desktop != nil && slices.Contains(desktop.windows, w) && w.state == state && w.session.client == state.client
}

func (a *App) desktopTrashApply(w *desktopWindow, state *desktopTrashState, question *desktopTrashQuestion) {
	ctx, cancel := context.WithCancel(systemContext(w.session))
	state.cancel, state.working, state.message = cancel, locales.DesktopTrashProgress(1, len(question.items)), ""
	runners := state.runners
	var notes map[string]string
	if a.settings.Desktop != nil && !question.purge {
		notes = maps.Clone(a.settings.Desktop.Hosts[w.session.Host.ID].Notes)
	}
	go func() {
		defer cancel()
		type operation struct {
			item        monitor.TrashItem
			destination string
			err         error
		}
		var operations []operation
		for index, item := range question.items {
			if ctx.Err() != nil {
				break
			}
			a.post(func() { state.working = locales.DesktopTrashProgress(index+1, len(question.items)) })
			run := runners.user
			if item.Root {
				run = runners.root
			}
			result := operation{item: item}
			if question.purge {
				result.err = monitor.TrashDelete(ctx, run, item)
			} else {
				result.destination, result.err = a.desktopTrashRestore(ctx, w, state, run, item, notes[item.Original])
				if result.destination == "" && result.err == nil {
					continue
				}
			}
			operations = append(operations, result)
		}
		cancelErr := ctx.Err()
		a.post(func() {
			var problems []string
			restoredNotes := make(map[string]string)
			for _, entry := range operations {
				target := entry.item.Original
				if target == "" || question.purge {
					target = path.Join(entry.item.Store, "files", entry.item.Name)
				}
				if entry.destination != "" {
					target = entry.destination
				}
				var logErr error
				if entry.err != nil {
					logErr = errors.New(trashDisplayError(entry.err))
					problems = append(problems, entry.item.Name+": "+logErr.Error())
				} else if entry.destination != "" {
					restoredNotes[entry.destination] = entry.item.Note
				}
				text := locales.DesktopTrashRestored(target)
				if question.purge {
					text = locales.DesktopTrashPurged(target)
				}
				a.desktopLog(w.session, appTrash, text, logErr)
			}
			a.desktopTrashRestoreNotes(w.session.Host.ID, restoredNotes)
			if cancelErr != nil && len(problems) == 0 {
				problems = append(problems, trashDisplayError(cancelErr))
			}
			state.cancel, state.working, state.conflict = nil, "", nil
			state.message = strings.Join(problems, "\n")
			clear(state.selected)
			if a.desktopTrashCurrent(w, state) {
				a.desktopTrashRefresh(w, state, false)
			}
		})
	}()
}

func (a *App) desktopTrashRestoreNotes(hostID string, restored map[string]string) {
	if len(restored) == 0 {
		return
	}
	settings := config.DesktopSettings{}
	if a.settings.Desktop != nil {
		settings = *a.settings.Desktop
	}
	hosts := maps.Clone(settings.Hosts)
	if hosts == nil {
		hosts = make(map[string]config.DesktopHost)
	}
	host := hosts[hostID]
	host.Notes = maps.Clone(host.Notes)
	if host.Notes == nil {
		host.Notes = make(map[string]string)
	}
	changed := false
	for destination, note := range restored {
		if host.Notes[destination] == note {
			continue
		}
		if note == "" {
			delete(host.Notes, destination)
		} else {
			host.Notes[destination] = note
		}
		changed = true
	}
	if !changed {
		return
	}
	hosts[hostID] = host
	settings.Hosts = hosts
	a.changeSettings(func() { a.settings.Desktop = &settings })
}

func (a *App) desktopTrashRestore(ctx context.Context, w *desktopWindow, state *desktopTrashState, run monitor.TrashRunner, item monitor.TrashItem, replacementNote string) (string, error) {
	mode, stamp := monitor.TrashCheck, ""
	for {
		result, err := monitor.TrashRestoreWithNote(ctx, run, item, mode, stamp, replacementNote)
		if result.Displaced != "" {
			a.post(func() {
				a.desktopLog(w.session, appTrash, locales.DesktopTrashMoved(item.Original, result.Displaced), nil)
			})
		}
		var conflict *monitor.TrashConflict
		if !errors.As(err, &conflict) {
			return result.Destination, err
		}
		answer := make(chan monitor.TrashRestoreMode, 1)
		a.post(func() {
			if ctx.Err() == nil && a.desktopTrashCurrent(w, state) {
				state.conflict = &desktopTrashConflict{item, answer}
			} else {
				answer <- ""
			}
		})
		select {
		case mode = <-answer:
			if mode == "" {
				return "", nil
			}
			stamp = conflict.Stamp
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
}
