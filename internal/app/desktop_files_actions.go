package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"weak"

	"finallyshell/internal/config"
	"finallyshell/internal/monitor"
	"finallyshell/internal/remotefs"
	"finallyshell/locales"

	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"
	"github.com/pkg/sftp"
)

type desktopFilesPrompt struct {
	title, text, field, value, second, secondLabel string
	action                                         string
	err                                            string
	valid                                          func(*desktopFilesPrompt) bool
	accept                                         func(*desktopFilesPrompt)
	properties                                     *desktopFilesProperties
}

type desktopFilesJob struct {
	label, progress string
	cancel          context.CancelFunc
}

type desktopFilesWork func(context.Context, func(string)) error

func (a *App) desktopFilesConfirm(w *desktopWindow, title, text, action string, yes func()) {
	w.explorer().modal = &desktopFilesPrompt{title: title, text: text, action: action, accept: func(*desktopFilesPrompt) { yes() }}
}

func (a *App) desktopFilesWork(w *desktopWindow, label string, mutate bool, refresh []string, work desktopFilesWork, finish func(error)) {
	f, s := w.explorer(), w.session
	if f.job != nil {
		return
	}
	ctx, cancel := context.WithCancel(desktopFilesLife(s))
	job := &desktopFilesJob{label: label, cancel: cancel}
	f.job, f.err = job, nil
	go func() {
		last := time.Time{}
		err := work(ctx, func(line string) {
			if time.Since(last) < 120*time.Millisecond {
				return
			}
			last = time.Now()
			parts := strings.FieldsFunc(line, func(character rune) bool { return character == '\r' || character == '\n' })
			if len(parts) == 0 {
				return
			}
			line = strings.TrimSpace(parts[len(parts)-1])
			a.post(func() { job.progress = line })
		})
		cancel()
		a.post(func() {
			f.job = nil
			if mutate {
				a.desktopLog(s, w.app.id, label, err)
				a.desktopFilesRefreshDir(s, refresh...)
			}
			if err != nil && !errors.Is(err, context.Canceled) {
				f.err = err
			}
			if finish != nil {
				finish(err)
			}
		})
	}()
}

func desktopFilesRun(ctx context.Context, run desktopFilesExec, script string, progress func(string)) error {
	if run == nil {
		return errors.New(locales.DesktopFilesExecDisabled())
	}
	out, stderr, err := run(ctx, script, progress)
	if err != nil && stderr == "" && out != "" {
		if len(out) > 2048 {
			out = out[len(out)-2048:]
		}
		stderr = out
	}
	if tool, ok := strings.CutPrefix(strings.TrimSpace(stderr), "desktop-files-missing:"); err != nil && ok {
		return errors.New(locales.DesktopFilesMissingTool(tool))
	}
	return desktopFilesCommandError(stderr, err)
}

func (a *App) desktopFilesNew(w *desktopWindow, folder bool) {
	f, s := w.explorer(), w.session
	dir, sc := f.dir, s.sftp
	if !desktopFilesPath(dir) || sc == nil || f.job != nil {
		return
	}
	title := locales.DesktopFilesNewText()
	if folder {
		title = locales.NewFolder()
	}
	f.inline = &desktopFilesPrompt{title: title, field: locales.ColName(), action: locales.DesktopFilesCreate(),
		valid: func(p *desktopFilesPrompt) bool { return desktopFilesName(p.value) },
		accept: func(p *desktopFilesPrompt) {
			if s.sftp != sc {
				return
			}
			name := p.value
			target := path.Join(dir, name)
			a.desktopFilesWork(w, title+": "+target, true, []string{dir}, func(ctx context.Context, _ func(string)) error {
				if err := ctx.Err(); err != nil {
					return err
				}
				var err error
				if folder {
					_, err = remotefs.Mkdir(sc, dir, name)
				} else {
					_, err = remotefs.CreateFile(sc, dir, name)
				}
				return err
			}, func(err error) {
				if err == nil && f.dir == dir && f.deep == "" {
					f.reveal = target
				}
			})
		},
	}
}

func (a *App) desktopFilesRename(w *desktopWindow, item desktopFilesItem) {
	f, s := w.explorer(), w.session
	target, sc := item.target, s.sftp
	if sc == nil || !desktopFilesPath(target) || target == "/" || f.job != nil {
		return
	}
	dir := path.Dir(target)
	f.inline = &desktopFilesPrompt{title: locales.Rename(), text: target, field: locales.ColName(), value: item.Name, action: locales.DesktopFilesConfirmRename(),
		valid: func(p *desktopFilesPrompt) bool { return p.value != item.Name && desktopFilesName(p.value) },
		accept: func(p *desktopFilesPrompt) {
			if s.sftp != sc {
				return
			}
			name := p.value
			next := path.Join(dir, name)
			a.desktopFilesWork(w, locales.Rename()+": "+target+" → "+next, true, []string{dir}, func(ctx context.Context, _ func(string)) error {
				if err := ctx.Err(); err != nil {
					return err
				}
				_, err := remotefs.Rename(sc, target, name)
				return err
			}, func(err error) {
				if err == nil {
					a.desktopFilesRetarget(s, target, next)
					if f.dir == dir && f.deep == "" {
						f.reveal = next
					}
				}
			})
		},
	}
}

func (a *App) desktopFilesRetarget(s *Session, before, after string) {
	moved := func(target string) string {
		if within(target, before) {
			return after + strings.TrimPrefix(target, before)
		}
		return target
	}
	a.desktopFilesChangeHost(s, func(h *config.DesktopHost) {
		for index, target := range h.Favorites {
			h.Favorites[index] = moved(target)
		}
		for index, target := range h.Recent {
			h.Recent[index] = moved(target)
		}
		for target, note := range a.desktopFilesHost(s).Notes {
			if next := moved(target); next != target {
				delete(h.Notes, target)
				h.Notes[next] = note
			}
		}
	})
}

func desktopFilesTargets(items []desktopFilesItem) (paths, dirs []string) {
	for _, item := range items {
		if desktopFilesPath(item.target) && item.target != "/" {
			paths = append(paths, item.target)
			if dir := path.Dir(item.target); !slices.Contains(dirs, dir) {
				dirs = append(dirs, dir)
			}
		}
	}
	paths = slices.DeleteFunc(paths, func(target string) bool {
		return slices.ContainsFunc(items, func(parent desktopFilesItem) bool {
			return parent.target != target && parent.target != "/" && parent.IsDir() && !parent.Link && within(target, parent.target)
		})
	})
	return
}

func (a *App) desktopFilesDelete(w *desktopWindow, items []desktopFilesItem, permanent bool) {
	f, s := w.explorer(), w.session
	sc := s.sftp
	paths, dirs := desktopFilesTargets(items)
	if sc == nil || len(paths) == 0 || f.job != nil {
		return
	}
	title := locales.DesktopFilesTrash()
	if permanent {
		title = locales.DesktopFilesPermanent()
	}
	body := strings.Join(paths, "\n")
	if permanent {
		body = locales.DesktopFilesPermanentWarning() + "\n" + body
	}
	action := title
	if permanent {
		action = locales.DesktopFilesConfirmDeletion()
	}
	a.desktopFilesConfirm(w, title, body, action, func() {
		if s.sftp != sc {
			return
		}
		if permanent {
			a.desktopFilesRemove(w, sc, paths, dirs)
			return
		}
		a.desktopTrash(s, paths, func(err error) {
			a.desktopLog(s, w.app.id, title+": "+strings.Join(paths, ", "), err)
			a.desktopFilesRefreshDir(s, dirs...)
			if errors.Is(err, errDesktopNotImplemented) {
				f.err = errors.New(locales.DesktopFilesTrashUnavailable())
				a.desktopFilesConfirm(w, locales.DesktopFilesTrashUnavailable(), locales.DesktopFilesPermanentWarning()+"\n"+strings.Join(paths, "\n"), locales.DesktopFilesPermanent(), func() {
					a.desktopFilesRemove(w, sc, paths, dirs)
				})
			} else if err != nil {
				f.err = err
			}
		})
	})
}

func (a *App) desktopFilesRemove(w *desktopWindow, sc *sftp.Client, paths, dirs []string) {
	if w.session.sftp != sc {
		return
	}
	title := locales.DesktopFilesPermanent()
	a.desktopFilesWork(w, title+": "+strings.Join(paths, ", "), true, dirs, func(ctx context.Context, report func(string)) error {
		for index, target := range paths {
			report(fmt.Sprintf("%d/%d · %s", index+1, len(paths), target))
			if err := remotefs.RemoveAll(ctx, sc, target); err != nil {
				return err
			}
		}
		return nil
	}, nil)
}

func (a *App) desktopFilesOpenItem(w *desktopWindow, item desktopFilesItem, newWindow bool) {
	s, f := w.session, w.explorer()
	if !desktopFilesPath(item.target) {
		return
	}
	if item.IsDir() {
		if newWindow || f.dir == "" {
			a.desktopOpen(s, appFiles, item.target)
		} else {
			a.desktopFilesRead(w, item.target, false, true)
		}
		return
	}
	if item.Broken || !item.Mode.IsRegular() && !item.Link || s.sftp == nil || f.job != nil {
		return
	}
	if desktopFilesArchiveKind(item.target) != "" {
		a.desktopFilesAskExtract(w, item, false)
		return
	}
	sc := s.sftp
	var binary bool
	a.desktopFilesWork(w, locales.OpenFile()+": "+item.target, false, nil, func(ctx context.Context, _ func(string)) error {
		info, err := sc.Stat(item.target)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return errors.New(locales.DesktopFilesUnsupportedType())
		}
		file, err := sc.Open(item.target)
		if err != nil {
			return err
		}
		defer file.Close()
		head := make([]byte, 8000)
		n, err := io.ReadFull(file, head)
		if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
			return err
		}
		binary = bytes.IndexByte(head[:n], 0) >= 0 || info.Size() > 16<<20
		return ctx.Err()
	}, func(err error) {
		if err != nil || s.sftp != sc {
			return
		}
		a.desktopFilesRecent(s, item.target)
		if !binary {
			a.openRemoteFile(s, item.target)
			if tab := a.fe.current(); tab != nil {
				if f.edits == nil {
					f.edits = map[*editTab]time.Time{}
				}
				if desktop := a.desktopOf(s); desktop != nil {
					for _, window := range desktop.windows {
						if other, ok := window.state.(*desktopFilesState); ok && other != f {
							delete(other.edits, tab)
						}
					}
				}
				f.edits[tab] = tab.savedAt
			}
			return
		}
		open := func() { a.desktopFilesOpenLocal(w, item.target) }
		switch strings.ToLower(path.Ext(item.target)) {
		case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp", ".pdf", ".mp3", ".wav", ".mp4", ".webm", ".mkv":
			open()
		default:
			a.desktopFilesConfirm(w, locales.EditorOpenWithApp(), locales.DesktopFilesOpenLocalWarning()+"\n"+item.target, locales.OpenFile(), open)
		}
	})
}

func (a *App) desktopFilesOpenLocal(w *desktopWindow, target string) {
	sc := w.session.sftp
	if sc == nil || !desktopFilesLocalName(path.Base(target)) {
		w.explorer().err = errors.New(locales.DesktopFilesBadPath())
		return
	}
	root := a.plat.TempDir()
	a.desktopFilesWork(w, locales.Download()+": "+target, false, nil, func(ctx context.Context, report func(string)) error {
		removeOld(root, 24*time.Hour)
		dir := filepath.Join(root, fmt.Sprint(time.Now().UnixNano()))
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		local, err := remotefs.Download(ctx, sc, target, dir, func(done int64) { report(formatSize(done)) })
		if err == nil {
			err = a.plat.OpenPath(local)
		}
		return err
	}, nil)
}

func desktopFilesLocalName(name string) bool {
	return relayBasename(name) && filepath.IsLocal(name)
}

func (a *App) desktopFilesDownload(w *desktopWindow, items []desktopFilesItem) {
	if len(items) == 0 {
		return
	}
	var parents []string
	names := map[string][]string{}
	for _, item := range items {
		if !desktopFilesLocalName(item.Name) || item.Broken || !item.Mode.IsRegular() && !item.IsDir() && !item.Link {
			w.explorer().err = errors.New(locales.DesktopFilesUnsupportedType())
			return
		}
		parent := path.Dir(item.target)
		if _, seen := names[parent]; !seen {
			parents = append(parents, parent)
		}
		names[parent] = append(names[parent], path.Base(item.target))
	}
	if len(parents) == 1 {
		a.downloadFrom(w.session, parents[0], names[parents[0]], "", false)
		return
	}
	start := func(dir string) { a.desktopFilesDownloadGroups(w, dir, parents, names) }
	if !a.settings.AskDownloadDir {
		start(a.downloadDir())
		return
	}
	title := locales.ChooseDownloadFolder()
	go func() {
		dir, err := a.plat.ChooseFolder(WindowMain, title)
		a.post(func() {
			if err != nil {
				w.explorer().err = err
			} else if dir != "" {
				start(dir)
			}
		})
	}()
}

func (a *App) desktopFilesDownloadGroups(w *desktopWindow, dir string, parents []string, names map[string][]string) {
	s := w.session
	sc := s.sftp
	if sc == nil {
		return
	}
	var existing []string
	a.desktopFilesWork(w, locales.Download(), false, nil, func(_ context.Context, _ func(string)) error {
		used := map[string]bool{}
		for _, parent := range parents {
			for _, name := range names[parent] {
				if key := strings.ToLower(name); used[key] {
					return errors.New(locales.DesktopFilesDuplicateDownload(name))
				} else {
					used[key] = true
				}
			}
			existing = append(existing, remotefs.LocalExisting(dir, names[parent])...)
		}
		return nil
	}, func(err error) {
		if err != nil || s.sftp != sc {
			return
		}
		start := func() {
			for _, parent := range parents {
				for _, name := range names[parent] {
					a.enqueue(&transferJob{sess: s, sftp: sc, dir: download, src: path.Join(parent, name), dstDir: dir})
				}
			}
		}
		if len(existing) != 0 {
			a.desktopFilesConfirm(w, locales.Replace(), dir+"\n"+strings.Join(existing, "\n"), locales.Replace(), start)
		} else {
			start()
		}
	})
}

func (a *App) desktopFilesUpload(w *desktopWindow, local []string, folder bool) {
	s, dir := w.session, w.explorer().dir
	sc := s.sftp
	if sc == nil || !desktopFilesPath(dir) || w.explorer().modal != nil || w.explorer().job != nil {
		return
	}
	if len(local) == 0 {
		title := locales.ChooseUploadFiles()
		if folder {
			title = locales.ChooseUploadFolder()
		}
		go func() {
			paths, err := a.plat.OpenFiles(title, folder)
			a.post(func() {
				if err != nil {
					w.explorer().err = err
				} else if len(paths) > 0 && s.sftp == sc && w.explorer().modal == nil && w.explorer().job == nil {
					a.desktopFilesConfirmUpload(w, dir, paths)
				}
			})
		}()
		return
	}
	a.desktopFilesConfirmUpload(w, dir, local)
}

func (a *App) desktopFilesConfirmUpload(w *desktopWindow, dir string, local []string) {
	s, f := w.session, w.explorer()
	sc := s.sftp
	if sc == nil {
		return
	}
	names := make([]string, 0, len(local))
	for _, target := range local {
		name := filepath.Base(target)
		if !filepath.IsAbs(target) || !desktopFilesName(name) || slices.Contains(names, name) {
			f.err = errors.New(locales.DesktopFilesBadPath())
			return
		}
		names = append(names, name)
	}
	a.desktopFilesConfirm(w, locales.DesktopFilesUpload(), locales.DesktopFilesUploadTo(dir)+"\n"+strings.Join(names, "\n"), locales.DesktopFilesStartUpload(), func() {
		if s.sftp == sc {
			a.desktopFilesEnqueueUpload(w, sc, dir, local, names)
		}
	})
}

func (a *App) desktopFilesEnqueueUpload(w *desktopWindow, sc *sftp.Client, dir string, local, names []string) {
	s := w.session
	var conflicts []string
	a.desktopFilesWork(w, locales.DesktopFilesUpload(), false, nil, func(ctx context.Context, _ func(string)) error {
		for _, name := range names {
			if err := ctx.Err(); err != nil {
				return err
			}
			if _, err := sc.Lstat(path.Join(dir, name)); err == nil {
				conflicts = append(conflicts, name)
			} else if !errors.Is(err, fs.ErrNotExist) {
				return err
			}
		}
		return nil
	}, func(err error) {
		if err != nil || s.sftp != sc {
			return
		}
		start := func() {
			var jobs []*transferJob
			for _, target := range local {
				job := &transferJob{sess: s, sftp: sc, dir: upload, src: target, dstDir: dir}
				jobs = append(jobs, job)
				a.enqueue(job)
			}
			a.desktopFilesWatchTransfers(w, s, dir, locales.DesktopFilesUpload(), jobs)
		}
		if len(conflicts) > 0 {
			w.explorer().err = errors.New(locales.DesktopFilesTaken(path.Join(dir, conflicts[0])))
			return
		}
		start()
	})
}

func (a *App) desktopFilesWatchTransfers(w *desktopWindow, target *Session, dir, label string, jobs []*transferJob) {
	life := desktopFilesLife(target)
	go func() {
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		for len(jobs) != 0 {
			select {
			case <-life.Done():
				return
			case <-ticker.C:
			}
			remaining := make(chan []*transferJob, 1)
			a.post(func() {
				pending := make([]*transferJob, 0, len(jobs))
				changed := false
				for _, job := range jobs {
					if !job.finished() {
						pending = append(pending, job)
						continue
					}
					changed = true
					err := job.err
					if job.state == transferCanceled && err == nil {
						err = context.Canceled
					}
					a.desktopLog(target, w.app.id, label+": "+job.name()+" → "+dir, err)
				}
				if changed {
					a.desktopFilesRefreshDir(target, dir, w.explorer().dir)
					for _, job := range jobs {
						if job.finished() && job.err != nil && !errors.Is(job.err, context.Canceled) {
							w.explorer().err = job.err
						}
					}
				}
				remaining <- pending
			})
			select {
			case jobs = <-remaining:
			case <-life.Done():
				return
			}
		}
	}()
}

type desktopFilesClipboard struct {
	owner weak.Pointer[App]
	s     *Session
	paths []string
	cut   bool
	text  string
}

var desktopFilesClip *desktopFilesClipboard

func (a *App) desktopFilesCopy(c *ui.Context, s *Session, items []desktopFilesItem, cut bool) {
	paths, _ := desktopFilesTargets(items)
	if len(paths) == 0 {
		return
	}
	text := strings.Join(paths, "\n")
	desktopFilesClip = &desktopFilesClipboard{owner: weak.Make(a), s: s, paths: paths, cut: cut, text: text}
	c.Services().WriteClipboard(text)
}

func (a *App) desktopFilesClipboard(c *ui.Context) *desktopFilesClipboard {
	clip := desktopFilesClip
	if clip == nil || clip.owner.Value() != a || clip.s.sftp == nil || !slices.Contains(a.sessions, clip.s) || c.Services().ReadClipboard() != clip.text {
		return nil
	}
	return clip
}

func desktopFilesHasLocalClipboard(c *ui.Context) bool {
	formats := c.Services().ClipboardFormats()
	return slices.Contains(formats, transfer.FileList) || slices.Contains(formats, transfer.URIList)
}

func (a *App) desktopFilesPaste(c *ui.Context, w *desktopWindow) {
	if desktopFilesHasLocalClipboard(c) {
		data := c.Services().ReadClipboardData(transfer.FileList, transfer.URIList)
		if files, err := data.Files(); err == nil && len(files) > 0 {
			a.desktopFilesUpload(w, files, false)
			return
		}
	}
	if clip := a.desktopFilesClipboard(c); clip != nil {
		a.desktopFilesPasteRemote(w, clip)
	}
}

type desktopFilesPasteItem struct {
	from, to string
	existing fs.FileInfo
	replace  bool
}

type desktopFilesConflict struct {
	clip     *desktopFilesClipboard
	from, to *sftp.Client
	dir      string
	items    []desktopFilesPasteItem
	names    map[string]bool
	index    int
	applyAll bool
}

func desktopFilesPastePlan(ctx context.Context, sc *sftp.Client, clip *desktopFilesClipboard, dir string) (*desktopFilesConflict, error) {
	plan := &desktopFilesConflict{clip: clip, dir: dir, names: map[string]bool{}}
	entries, err := sc.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		plan.names[entry.Name()] = true
	}
	for _, from := range clip.paths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		to := path.Join(dir, path.Base(from))
		if !desktopFilesPath(from) || from == "/" || !desktopFilesPath(to) {
			return nil, errRelayPath
		}
		info, err := sc.Lstat(to)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
		if slices.ContainsFunc(plan.items, func(item desktopFilesPasteItem) bool { return item.to == to }) {
			to = plan.keepBoth(to)
			info = nil
		}
		plan.items = append(plan.items, desktopFilesPasteItem{from: from, to: to, existing: info})
		plan.names[path.Base(to)] = true
	}
	return plan, nil
}

func (p *desktopFilesConflict) keepBoth(target string) string {
	base := path.Base(target)
	ext := path.Ext(base)
	if ext == base {
		ext = ""
	}
	for index := 1; ; index++ {
		name := fmt.Sprintf("%s (%d)%s", strings.TrimSuffix(base, ext), index, ext)
		if !p.names[name] {
			p.names[name] = true
			return path.Join(p.dir, name)
		}
	}
}

func (a *App) desktopFilesPasteRemote(w *desktopWindow, clip *desktopFilesClipboard) {
	f, s := w.explorer(), w.session
	sc, dir, origin := s.sftp, f.dir, clip.s.sftp
	if sc == nil || origin == nil || !desktopFilesPath(dir) || f.job != nil {
		return
	}
	var plan *desktopFilesConflict
	a.desktopFilesWork(w, locales.DesktopFilesPaste(), false, nil, func(ctx context.Context, _ func(string)) error {
		canonical, err := sc.RealPath(dir)
		if err != nil || !desktopFilesPath(canonical) {
			return errors.Join(errRelayPath, err)
		}
		plan, err = desktopFilesPastePlan(ctx, sc, clip, canonical)
		return err
	}, func(err error) {
		if err == nil && s.sftp == sc && clip.s.sftp == origin && len(plan.items) > 0 {
			plan.from, plan.to = origin, sc
			a.desktopFilesNextConflict(w, plan)
		}
	})
}

func (a *App) desktopFilesNextConflict(w *desktopWindow, plan *desktopFilesConflict) {
	f := w.explorer()
	for plan.index < len(plan.items) {
		if plan.items[plan.index].existing != nil {
			f.conflict = plan
			return
		}
		plan.index++
	}
	f.conflict = nil
	var labels []string
	for _, item := range plan.items {
		if item.to != "" {
			labels = append(labels, item.from+" → "+item.to)
		}
	}
	if len(labels) == 0 {
		return
	}
	title := locales.DesktopFilesPaste()
	if plan.clip.cut && plan.clip.s == w.session {
		title = locales.DesktopFilesMove()
	} else if plan.clip.s != w.session {
		title = locales.DesktopFilesAcross(plan.clip.s.label(), w.session.label())
	}
	a.desktopFilesConfirm(w, title, strings.Join(labels, "\n"), locales.DesktopFilesConfirmPaste(), func() {
		a.desktopFilesCommitPaste(w, plan, title)
	})
}

func (a *App) desktopFilesResolve(w *desktopWindow, plan *desktopFilesConflict, policy relayPolicy) {
	for plan.index < len(plan.items) {
		item := &plan.items[plan.index]
		plan.index++
		if item.existing != nil {
			switch policy {
			case relayOverwrite:
				item.replace = true
			case relayRename:
				item.to, item.existing = plan.keepBoth(item.to), nil
			default:
				item.to = ""
			}
		}
		if !plan.applyAll {
			break
		}
	}
	a.desktopFilesNextConflict(w, plan)
}

func (a *App) desktopFilesCommitPaste(w *desktopWindow, plan *desktopFilesConflict, title string) {
	f, s, source := w.explorer(), w.session, plan.clip.s
	sc, run := s.sftp, desktopFilesRunner(s, f)
	if sc == nil || source.sftp == nil || f.job != nil || plan.from != source.sftp || plan.to != sc {
		return
	}
	if source != s {
		a.desktopFilesRelay(w, plan, title)
		return
	}
	if run == nil {
		f.err = errors.New(locales.DesktopFilesExecDisabled())
		return
	}
	dirs := []string{plan.dir, f.dir}
	for _, item := range plan.items {
		if plan.clip.cut {
			dirs = append(dirs, path.Dir(item.from))
		}
	}
	var completed []desktopFilesPasteItem
	a.desktopFilesWork(w, title+": "+plan.dir, true, dirs, func(ctx context.Context, report func(string)) error {
		for index, item := range plan.items {
			if item.to == "" {
				continue
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			parent, err := sc.RealPath(path.Dir(item.from))
			if err != nil || !desktopFilesPath(parent) {
				return errors.Join(errRelayPath, err)
			}
			from := path.Join(parent, path.Base(item.from))
			if within(item.to, from) || within(from, item.to) {
				return errRelayOverlap
			}
			if real, err := sc.RealPath(plan.dir); err != nil || real != plan.dir {
				return errRelayChanged
			}
			report(fmt.Sprintf("%d/%d · %s → %s", index+1, len(plan.items), item.from, item.to))
			if err := desktopFilesPublishCopy(ctx, sc, run, from, item, plan.clip.cut); err != nil {
				return err
			}
			completed = append(completed, item)
		}
		return nil
	}, func(_ error) {
		if !plan.clip.cut {
			return
		}
		var moved []string
		for _, item := range completed {
			a.desktopFilesRetarget(s, item.from, item.to)
			moved = append(moved, item.from)
		}
		if desktopFilesClip == plan.clip && len(moved) > 0 {
			left := slices.DeleteFunc(slices.Clone(plan.clip.paths), func(target string) bool { return slices.Contains(moved, target) })
			desktopFilesClip = nil
			if len(left) > 0 {
				updated := *plan.clip
				updated.paths = left
				desktopFilesClip = &updated
			}
		}
	})
}

func desktopFilesPublishCopy(ctx context.Context, sc *sftp.Client, run desktopFilesExec, from string, item desktopFilesPasteItem, cut bool) error {
	backup := ""
	current, err := sc.Lstat(item.to)
	if item.replace {
		if err != nil || item.existing == nil || current.Mode() != item.existing.Mode() || current.Size() != item.existing.Size() || !current.ModTime().Equal(item.existing.ModTime()) {
			return errors.Join(errRelayChanged, err)
		}
		backup = item.to + ".bak-" + time.Now().UTC().Format("20060102150405.000000000")
		if err := sc.Rename(item.to, backup); err != nil {
			return err
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return errors.Join(errRelayChanged, err)
	}
	verb := "cp"
	if cut {
		verb = "mv"
	}
	command := desktopFilesTool(verb) + "exec " + desktopFilesCopyCommand(from, item.to, cut, false)
	err = desktopFilesRun(ctx, run, command, nil)
	if err == nil && cut {
		if _, statErr := sc.Lstat(from); !errors.Is(statErr, fs.ErrNotExist) {
			err = errors.Join(errRelayChanged, statErr)
		}
	}
	if err != nil {
		if backup != "" {
			if _, statErr := sc.Lstat(item.to); errors.Is(statErr, fs.ErrNotExist) {
				err = errors.Join(err, sc.Rename(backup, item.to))
			} else {
				err = errors.Join(err, errors.New(locales.DesktopFilesBackupAt(backup)))
			}
		}
		return err
	}
	return nil
}

func (a *App) desktopFilesRelay(w *desktopWindow, plan *desktopFilesConflict, title string) {
	target, source := w.session, plan.clip.s
	if source.client == nil || target.client == nil || !relayDirectory(plan.dir) {
		w.explorer().err = errors.New(locales.DesktopFilesDisconnected())
		return
	}
	jobs := map[string]*relayJob{}
	var order []string
	for _, item := range plan.items {
		if item.to == "" {
			continue
		}
		dir, from, to := path.Dir(item.from), path.Base(item.from), path.Base(item.to)
		if !relayDirectory(dir) || !relayBasename(from) || !relayBasename(to) {
			w.explorer().err = errRelayPath
			return
		}
		job := jobs[dir]
		if job == nil {
			job = &relayJob{target: target, sourceName: source.label(), label: target.label(), src: source.client, dst: target.client,
				srcLife: source.ctx, dstLife: target.ctx, task: relayTask{from: dir, to: plan.dir, mode: relayAuto,
					existing: map[string]fs.FileInfo{}, samePeer: relaySamePeer(source, target),
					endpoint: relayEndpoint{target.Host.Addr, target.client.User(), target.Host.PortOr22()}}}
			if execAllowed(source.Host) && execAllowed(target.Host) && config.ProxyOf(target.Host) == nil {
				client := job.src
				job.task.run = func(ctx context.Context, script string) (string, string, error) {
					return monitor.RunScript(ctx, client, script)
				}
			}
			jobs[dir], order = job, append(order, dir)
		}
		job.task.items = append(job.task.items, relayItem{source: from, dest: to, backup: item.replace})
		job.task.existing[to] = item.existing
	}
	var transfers []*transferJob
	for _, dir := range order {
		job := jobs[dir]
		names := make([]string, 0, len(job.task.items))
		for _, item := range job.task.items {
			names = append(names, item.source)
		}
		transfer := &transferJob{sess: source, dir: serverRelay, src: names[0], srcs: names, dstDir: plan.dir, relay: job}
		transfers = append(transfers, transfer)
		a.enqueue(transfer)
	}
	a.desktopFilesWatchTransfers(w, target, plan.dir, title, transfers)
}
