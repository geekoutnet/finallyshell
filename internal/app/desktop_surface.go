package app

import (
	"context"
	"errors"
	"os"
	"path"
	"slices"
	"strings"
	"time"

	"finallyshell/internal/config"
	"finallyshell/internal/remotefs"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
	"github.com/pkg/sftp"
)

// The desktop's files: those of the folder Desktop in the user's home on
// the server, as Linux desktops show theirs (XDG), after the desktop's
// icons. A hidden file manager window reads the folder, so that opening,
// copying, pasting, renaming, deleting and the rest are the file
// manager's own, with its dialogs drawn on the desktop. The folder is
// made when a file is first made or pasted there, not before.

const (
	desktopFilePrefix = "file:" // a file of the desktop's folder, by name
	desktopDirPrefix  = "dir:"  // a folder in it, by name
)

// desktopSurfaceRefresh is how often the desktop reads its folder again,
// for the files other programs make there.
const desktopSurfaceRefresh = 15 * time.Second

// desktopScriptTemplate is a new shell script's text.
const desktopScriptTemplate = "#!/bin/sh\n\n"

// desktopSurface is the hidden window reading s's desktop folder, which it
// starts reading, again when the connection changed.
func (a *App) desktopSurface(d *desktopState, s *Session) *desktopWindow {
	if d.files == nil {
		d.files = &desktopWindow{id: nextDesktopWindowIDNext(), app: desktopAppByID(appFiles), session: s, state: &desktopFilesState{row: -1, free: -1}}
	}
	w := d.files
	f := w.explorer()
	sc := s.sftp
	if sc != nil && f.client != sc && !f.loading {
		f.client, f.loading = sc, true
		go func() {
			home, err := sc.Getwd()
			a.post(func() {
				if s.sftp != sc {
					return
				}
				f.loading = false
				if err != nil || !desktopFilesPath(home) {
					return
				}
				f.home, d.filesDir = home, path.Join(home, "Desktop")
				a.desktopSurfaceRead(d)
			})
		}()
	}
	return w
}

// desktopSurfaceRead reads the desktop's folder again.
func (a *App) desktopSurfaceRead(d *desktopState) {
	if d.files == nil || d.filesDir == "" {
		return
	}
	d.filesReadAt = time.Now()
	a.desktopFilesRead(d.files, d.filesDir, false, false)
}

// desktopSurfaceItems are the files and folders of the desktop's folder,
// folders first, hidden ones left out.
func desktopSurfaceItems(d *desktopState) []desktopFilesItem {
	if d.files == nil || d.filesDir == "" {
		return nil
	}
	f := d.files.explorer()
	if f.dir != d.filesDir || f.deep != "" {
		return nil
	}
	var out []desktopFilesItem
	for _, item := range f.entries {
		if !strings.HasPrefix(item.Name, ".") {
			out = append(out, item)
		}
	}
	slices.SortStableFunc(out, func(x, y desktopFilesItem) int {
		if x.IsDir() != y.IsDir() {
			if x.IsDir() {
				return -1
			}
			return 1
		}
		return strings.Compare(strings.ToLower(x.Name), strings.ToLower(y.Name))
	})
	return out
}

// desktopSurfaceIcons are the icons of the desktop's files, placed in the
// first free cells.
func desktopSurfaceIcons(d *desktopState) []config.DesktopIcon {
	var out []config.DesktopIcon
	for _, item := range desktopSurfaceItems(d) {
		prefix := desktopFilePrefix
		if item.IsDir() {
			prefix = desktopDirPrefix
		}
		out = append(out, config.DesktopIcon{App: prefix + item.Name, Col: -1, Row: -1})
	}
	return out
}

// desktopSurfaceFile tells whether id is the icon of a file or folder of
// the desktop's folder, and its name.
func desktopSurfaceFile(id string) (string, bool) {
	if name, ok := strings.CutPrefix(id, desktopFilePrefix); ok {
		return name, desktopFilesName(name)
	}
	if name, ok := strings.CutPrefix(id, desktopDirPrefix); ok {
		return name, desktopFilesName(name)
	}
	return "", false
}

// desktopSurfaceIcon is the icon and tint of a file of the desktop.
func desktopSurfaceIcon(id string) (string, ui.Color) {
	name, _ := desktopSurfaceFile(id)
	if strings.HasPrefix(id, desktopDirPrefix) {
		return "folder", ui.Hex("#d99a2b")
	}
	switch strings.ToLower(path.Ext(name)) {
	case ".sh", ".bash", ".zsh":
		return "square-terminal", ui.Hex("#2f855a")
	case ".txt", ".md", ".log", ".csv":
		return "file-text", ui.Hex("#4a6fa5")
	case ".conf", ".cfg", ".ini", ".json", ".yaml", ".yml", ".toml", ".xml", ".py", ".js", ".go", ".php", ".sql", ".env":
		return "file-code", ui.Hex("#6b5bb5")
	}
	return "file", ui.Hex("#64748b")
}

// desktopSurfacePicked are the desktop's files that the icon id stands
// for: the selected ones when it is selected, else its own.
func desktopSurfacePicked(d *desktopState, id string) []desktopFilesItem {
	items := desktopSurfaceItems(d)
	var out []desktopFilesItem
	for _, item := range items {
		icon := desktopFilePrefix + item.Name
		if item.IsDir() {
			icon = desktopDirPrefix + item.Name
		}
		if icon == id || d.shell.selection[id] && d.shell.selection[icon] {
			out = append(out, item)
		}
	}
	return out
}

// desktopSurfaceSelected are the desktop's files selected.
func desktopSurfaceSelected(d *desktopState) []desktopFilesItem {
	var out []desktopFilesItem
	for _, item := range desktopSurfaceItems(d) {
		if d.shell.selection[desktopFilePrefix+item.Name] || d.shell.selection[desktopDirPrefix+item.Name] {
			out = append(out, item)
		}
	}
	return out
}

// desktopSurfaceOpen opens the desktop's file or folder id: a folder in
// the file manager, a file as the file manager opens it (text in the
// editor).
func (a *App) desktopSurfaceOpen(d *desktopState, s *Session, id string) {
	picked := desktopSurfacePicked(d, id)
	if len(picked) == 0 || d.files == nil {
		return
	}
	item := picked[0]
	for _, p := range picked {
		if desktopFilePrefix+p.Name == id || desktopDirPrefix+p.Name == id {
			item = p
		}
	}
	if item.IsDir() {
		a.desktopOpen(s, appFiles, item.target)
		return
	}
	a.desktopFilesOpenItem(d.files, item, false)
}

// desktopSurfaceRunnable tells whether item is a script the menu runs in
// a terminal: a shell script, or an executable file.
func desktopSurfaceRunnable(item desktopFilesItem) bool {
	if item.IsDir() || item.Broken || !item.Mode.IsRegular() && !item.Link {
		return false
	}
	switch strings.ToLower(path.Ext(item.Name)) {
	case ".sh", ".bash":
		return true
	}
	return item.Mode&0o111 != 0
}

// desktopSurfaceRunCommand is what a terminal in the script's folder types
// to run it: the file itself when it is executable, else through bash, or
// sh where there is no bash.
func desktopSurfaceRunCommand(item desktopFilesItem) string {
	file := relayQuote("./" + item.Name)
	if item.Mode&0o111 != 0 {
		return file
	}
	if ext := strings.ToLower(path.Ext(item.Name)); ext == ".sh" || ext == ".bash" {
		return `"$(command -v bash || command -v sh)" ` + file
	}
	return "sh " + file
}

// desktopSurfaceMenu is the menu of the desktop's file id: run a script,
// then the file manager's own items.
func (a *App) desktopSurfaceMenu(c *ui.Context, m *ui.Menu, d *desktopState, s *Session, id string) {
	picked := desktopSurfacePicked(d, id)
	if len(picked) == 0 || d.files == nil {
		return
	}
	if len(picked) == 1 && desktopSurfaceRunnable(picked[0]) {
		item := picked[0]
		if m.Item(locales.DesktopShellRunInTerminal()).Disabled(s.status != statusConnected).Chosen() && s.status == statusConnected {
			a.desktopOpen(s, appTerminal, desktopTerminalRun{dir: path.Dir(item.target), command: desktopSurfaceRunCommand(item)})
		}
		m.Separator()
	}
	a.desktopFilesItemMenu(c, d.files, m, picked)
}

// desktopSurfaceNewMenu is the items of the desktop's New menu that make
// files: a folder, a text file, a shell script.
func (a *App) desktopSurfaceNewMenu(sub *ui.Menu, d *desktopState, s *Session) {
	ready := s.sftp != nil && d.filesDir != "" && d.files != nil && d.files.explorer().job == nil
	for _, kind := range []struct {
		label string
		kind  int
	}{{locales.NewFolder(), desktopNewDir}, {locales.DesktopFilesNewText(), desktopNewText}, {locales.DesktopShellNewScript(), desktopNewScript}} {
		if sub.Item(kind.label).Disabled(!ready).Chosen() && ready {
			a.desktopSurfaceNew(d, s, kind.kind)
		}
	}
}

// What desktopSurfaceNew makes.
const (
	desktopNewDir = iota
	desktopNewText
	desktopNewScript
)

// desktopSurfaceNew asks for the name of a new folder, text file or shell
// script on the desktop, then makes it, and the desktop's folder first if
// needed.
func (a *App) desktopSurfaceNew(d *desktopState, s *Session, kind int) {
	w := a.desktopSurface(d, s)
	f, sc, dir := w.explorer(), s.sftp, d.filesDir
	if sc == nil || dir == "" || f.job != nil {
		return
	}
	title, name := locales.NewFolder(), locales.NewFolder()
	switch kind {
	case desktopNewText:
		title, name = locales.DesktopFilesNewText(), locales.DesktopShellNewTextName()
	case desktopNewScript:
		title, name = locales.DesktopShellNewScript(), locales.DesktopShellNewScriptName()
	}
	taken := func(name string) bool {
		return slices.ContainsFunc(desktopSurfaceItems(d), func(item desktopFilesItem) bool { return item.Name == name })
	}
	f.modal = &desktopFilesPrompt{title: title, text: locales.DesktopShellFilesHint(dir), field: locales.ColName(), value: name, action: locales.DesktopFilesCreate(),
		valid: func(p *desktopFilesPrompt) bool { return desktopFilesName(p.value) && !taken(p.value) },
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
				return desktopSurfaceMake(sc, dir, name, kind)
			}, func(err error) {
				if err != nil {
					return
				}
				clear(d.shell.selection)
				icon := desktopFilePrefix + name
				if kind == desktopNewDir {
					icon = desktopDirPrefix + name
				}
				if d.shell.selection == nil {
					d.shell.selection = map[string]bool{}
				}
				d.shell.selection[icon] = true
			})
		},
	}
}

// desktopSurfaceMake makes the folder, text file or script name in dir,
// making dir first: never over a file that is there.
func desktopSurfaceMake(sc *sftp.Client, dir, name string, kind int) error {
	if err := sc.MkdirAll(dir); err != nil {
		return err
	}
	switch kind {
	case desktopNewDir:
		_, err := remotefs.Mkdir(sc, dir, name)
		return err
	case desktopNewScript:
		target, err := remotefs.CreateFile(sc, dir, name)
		if err != nil {
			return err
		}
		file, err := sc.OpenFile(target, os.O_WRONLY|os.O_TRUNC)
		if err != nil {
			return err
		}
		_, err = file.Write([]byte(desktopScriptTemplate))
		if closeErr := file.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			return err
		}
		return sc.Chmod(target, 0o755)
	default:
		_, err := remotefs.CreateFile(sc, dir, name)
		return err
	}
}

// desktopSurfacePaste pastes what was copied or cut into the desktop's
// folder, making it first if needed.
func (a *App) desktopSurfacePaste(c *ui.Context, d *desktopState, s *Session) {
	w := a.desktopSurface(d, s)
	f, sc, dir := w.explorer(), s.sftp, d.filesDir
	if sc == nil || dir == "" || f.job != nil {
		return
	}
	if f.dir == dir {
		a.desktopFilesPaste(c, w)
		return
	}
	a.desktopFilesWork(w, locales.DesktopFilesPaste(), false, nil, func(ctx context.Context, _ func(string)) error {
		return sc.MkdirAll(dir)
	}, func(err error) {
		if err == nil && s.sftp == sc {
			f.dir = dir
			a.desktopFilesPaste(c, w)
		}
	})
}

// desktopSurfaceCanPaste tells whether there is something to paste.
func (a *App) desktopSurfaceCanPaste(c *ui.Context, s *Session, d *desktopState) bool {
	return s.sftp != nil && d.filesDir != "" && (a.desktopFilesClipboard(c) != nil || desktopFilesHasLocalClipboard(c))
}

// desktopSurfaceKeys are the desktop's keys for its files: copy, cut,
// paste, delete and rename, as a file manager's.
func (a *App) desktopSurfaceKeys(c *ui.Context, d *desktopState, s *Session) {
	if d.files == nil || d.shell.folder != "" || d.shell.rename != "" {
		return
	}
	f := d.files.explorer()
	busy := f.job != nil || f.modal != nil || f.conflict != nil
	picked := desktopSurfaceSelected(d)
	key := func(k ui.Key) bool { return c.Shortcut(ui.Cmd, k) || c.Shortcut(ui.Ctrl, k) }
	switch {
	case key(ui.KeyC) && len(picked) > 0:
		a.desktopFilesCopy(c, s, picked, false)
	case key(ui.KeyX) && len(picked) > 0:
		a.desktopFilesCopy(c, s, picked, true)
	case key(ui.KeyV) && !busy && a.desktopSurfaceCanPaste(c, s, d):
		a.desktopSurfacePaste(c, d, s)
	case c.Shortcut(0, ui.KeyDelete) && len(picked) > 0 && !busy:
		a.desktopFilesDelete(d.files, picked, false)
	case c.Shortcut(ui.Shift, ui.KeyDelete) && len(picked) > 0 && !busy:
		a.desktopFilesDelete(d.files, picked, true)
	case c.Shortcut(0, ui.KeyF2) && len(picked) == 1 && !busy:
		a.desktopFilesRename(d.files, picked[0])
	case c.Shortcut(0, ui.KeyF5):
		a.desktopSurfaceRead(d)
	}
}

// desktopSurfaceDialogs draws the hidden file manager's dialogs on the
// desktop, its questions as dialogs too, and tells what failed.
func (a *App) desktopSurfaceDialogs(c *ui.Context, d *desktopState, s *Session) {
	if d.files == nil {
		return
	}
	f := d.files.explorer()
	if f.inline != nil {
		f.modal, f.inline = f.inline, nil
	}
	a.desktopFilesDialog(c, d.files, d.area.H)
	if f.err != nil {
		var dirErr *dirError
		if !errors.As(f.err, &dirErr) {
			a.notify(f.err.Error())
		}
		f.err = nil
	}
	if s.sftp != nil && d.filesDir != "" && !f.loading && f.job == nil {
		if since := time.Since(d.filesReadAt); since >= desktopSurfaceRefresh {
			a.desktopSurfaceRead(d)
		} else {
			c.After(desktopSurfaceRefresh - since)
		}
	}
}

// desktopSurfaceWindow tells whether w is a desktop's hidden window of its
// files.
func (a *App) desktopSurfaceWindow(w *desktopWindow) bool {
	d := a.desktopOf(w.session)
	return d != nil && d.files == w
}
