package app

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"finallyshell/internal/config"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

func desktopShellTestApp(t *testing.T) (*App, *fakePlatform, *ui.Tester, *Session) {
	t.Helper()
	registry := desktopApps
	desktopApps = slices.Clone(registry)
	for idx, entry := range desktopApps {
		app := *entry
		app.view, app.opened, app.closing, app.available = desktopPlaceholder, nil, nil, nil
		app.minSize = desktopSize{360, 240}
		desktopApps[idx] = &app
	}
	t.Cleanup(func() { desktopApps = registry })
	return desktopTestApp(t)
}

func TestDesktopShellWindowPointer(t *testing.T) {
	a, _, tt, s := desktopShellTestApp(t)
	w := a.desktopOpen(s, appFiles, nil)
	tt.Frame()
	d := a.desktopOf(s)
	if w.maximized || w.minimized {
		t.Fatal("new window is not floating")
	}
	x, y := d.shell.root.X+w.x+105, d.shell.root.Y+w.y+desktopTitleHeight/2
	tt.Press(x, y)
	top := a.desktopOf(s).shell.root.Y + 3
	tt.Move(x, top)
	tt.Release(x, top)
	if !w.maximized {
		t.Fatalf("drag to desktop's top did not maximize: frame=%+v, desktop=%+v, pointer=%+v, preview=%q, drag=%+v", desktopWindowRect(w), d.shell.root, d.shell.pointer, d.shell.preview, d.shell.drag)
	}
	if err := tt.Click(locales.DesktopMinimize()); err != nil || !w.minimized {
		t.Fatalf("minimize failed: %v", err)
	}
	a.desktopFocus(w)
	tt.Frame()
	if w.minimized || !w.maximized {
		t.Fatal("restoring a minimized window lost its maximized size")
	}
}

func desktopShellClick(t *testing.T, tt *ui.Tester, label string) {
	t.Helper()
	if err := tt.Click(label); err != nil {
		t.Fatal(err)
	}
}

func desktopShellMenu(t *testing.T, tt *ui.Tester, labels ...string) {
	t.Helper()
	if err := tt.ChooseMenuItem(labels...); err != nil {
		t.Fatal(err)
	}
}

func desktopShellPoint(d *desktopState, p ui.Point) (float32, float32) {
	return d.shell.root.X + p.X, d.shell.root.Y + p.Y
}

func desktopShellDrag(tt *ui.Tester, d *desktopState, from, to ui.Point) {
	x, y := desktopShellPoint(d, from)
	tt.Press(x, y)
	x, y = desktopShellPoint(d, to)
	tt.Move(x, y)
	tt.Release(x, y)
}

func desktopShellIconPoint(t *testing.T, a *App, s *Session, id string) ui.Point {
	t.Helper()
	d, h := a.desktopOf(s), a.desktopHost(s)
	g := desktopMakeGrid(d.area, a.desktopPreferences().IconSize)
	for _, cell := range g.layout(s, h, a.desktopHostIcons(s, h)) {
		if cell.icon.App == id {
			return ui.Point{X: cell.rect.X + cell.rect.W/2, Y: cell.rect.Y + cell.rect.H/2}
		}
	}
	t.Fatalf("missing icon %q", id)
	return ui.Point{}
}

func TestDesktopShellFrameManagement(t *testing.T) {
	a, _, tt, s := desktopShellTestApp(t)
	d := a.desktopOf(s)
	w := a.desktopOpen(s, appFiles, nil)
	desktopSetWindowRect(w, ui.Rect{X: 130, Y: 150, W: 520, H: 350})
	tt.Frame()
	from := ui.Point{X: w.x + w.w - 7, Y: w.y + w.h - 7}
	x, y := desktopShellPoint(d, from)
	tt.Move(x, y)
	if got := tt.Cursor(); got != ui.CursorResizeNWSE {
		t.Fatalf("cursor %v", got)
	}
	desktopShellDrag(tt, d, from, ui.Point{X: from.X + 70, Y: from.Y + 55})
	if w.w != 590 || w.h != 405 {
		t.Fatalf("resize = %+v", desktopWindowRect(w))
	}
	desktopShellDrag(tt, d, ui.Point{X: w.x + 80, Y: w.y + 18}, ui.Point{X: 2, Y: 170})
	if w.snapped != "left" || w.maximized {
		t.Fatalf("left snap = %q, max = %v", w.snapped, w.maximized)
	}
	desktopShellDrag(tt, d, ui.Point{X: 150, Y: 18}, ui.Point{X: 480, Y: 145})
	if w.snapped != "" || w.w != 590 {
		t.Fatalf("restore from snap = %q, frame = %+v", w.snapped, desktopWindowRect(w))
	}
	x, y = desktopShellPoint(d, ui.Point{X: w.x + 65, Y: w.y + 18})
	tt.ClickAt(x, y)
	tt.ClickAt(x, y)
	if !w.maximized {
		t.Fatal("double-click did not maximize")
	}
	desktopShellClick(t, tt, locales.DesktopWinRestore())
	if w.maximized {
		t.Fatal("restore control did not restore")
	}
	other := a.desktopOpen(s, appFiles, nil)
	desktopSetWindowRect(other, ui.Rect{X: w.x + 120, Y: w.y + 50, W: 350, H: 250})
	tt.Frame()
	x, y = desktopShellPoint(d, ui.Point{X: w.x + 50, Y: w.y + 90})
	tt.ClickAt(x, y)
	if d.topWindow() != w || d.focused != w {
		t.Fatal("content press did not raise window")
	}
	x, y = desktopShellPoint(d, ui.Point{X: w.x + 90, Y: w.y + 18})
	tt.RightClickAt(x, y)
	desktopShellMenu(t, tt, locales.DesktopWinKeepOnTop())
	if !w.keepOnTop {
		t.Fatal("keep on top was not set")
	}
	a.desktopFocus(other)
	if d.topWindow() != w {
		t.Fatal("ordinary window covered a pinned window")
	}
	tt.SetSize(900, 600)
	for _, win := range d.windows {
		r := desktopWindowRect(win)
		if r.X < 0 || r.Y < 0 || r.X+r.W > d.area.W || r.Y+r.H > d.area.H-desktopDockHeight {
			t.Fatalf("resize of desktop stranded a window: %+v in %+v", r, d.area)
		}
	}
	newWindow := a.desktopOpen(s, appFiles, nil)
	if newWindow.w != 590 || newWindow.h != 405 {
		t.Fatalf("last size not remembered: %+v", desktopWindowRect(newWindow))
	}
}

func TestDesktopShellIconsAndFolders(t *testing.T) {
	a, _, tt, s := desktopShellTestApp(t)
	d := a.desktopOf(s)
	first, second := string(appThisPC), string(appFiles)
	p, q := desktopShellIconPoint(t, a, s, first), desktopShellIconPoint(t, a, s, second)
	x, y := desktopShellPoint(d, p)
	tt.ClickAt(x, y)
	if !d.shell.selection[first] || len(d.windows) != 0 {
		t.Fatal("single click did not just select")
	}
	x, y = desktopShellPoint(d, q)
	tt.ClickAtWith(ui.Ctrl, x, y)
	if !d.shell.selection[first] || !d.shell.selection[second] {
		t.Fatal("Ctrl click did not extend selection")
	}
	tt.Key(0, ui.KeyEnter)
	if len(d.windows) != 2 {
		t.Fatalf("Enter opened %d windows", len(d.windows))
	}
	for _, w := range slices.Clone(d.windows) {
		a.desktopClose(w)
	}
	tt.Frame()
	desktopShellDrag(tt, d, ui.Point{X: 110, Y: 6}, ui.Point{X: 12, Y: max(p.Y, q.Y) + 1})
	if !d.shell.selection[first] || !d.shell.selection[second] {
		t.Fatalf("marquee selection: %v", d.shell.selection)
	}
	x, y = desktopShellPoint(d, ui.Point{X: 320, Y: 240})
	tt.RightClickAt(x, y)
	desktopShellMenu(t, tt, locales.DesktopShellNew(), locales.NewFolder())
	tt.Frame()
	edit := d.shell.rename
	if edit == "" {
		t.Fatal("folder did not start inline rename")
	}
	tt.Key(ui.Ctrl, ui.KeyA)
	tt.Type("Workspace")
	tt.Key(0, ui.KeyEnter)
	h := a.desktopHost(s)
	if n := len(h.Folders); n != len(desktopPresetFolders)+1 || h.Folders[n-1].Name != "Workspace" {
		t.Fatalf("folder name: %+v", h.Folders)
	}
	folder := desktopFolderPrefix + edit
	dst := desktopShellIconPoint(t, a, s, folder)
	desktopShellDrag(tt, d, p, dst)
	h = a.desktopHost(s)
	if !slices.ContainsFunc(h.Icons, func(i config.DesktopIcon) bool { return i.App == first && i.Folder == edit }) {
		t.Fatalf("folder drop: %+v", h.Icons)
	}
	x, y = desktopShellPoint(d, dst)
	tt.ClickAt(x, y)
	tt.ClickAt(x, y)
	if d.shell.folder != edit {
		t.Fatal("double click did not open folder")
	}
	inside := ui.Point{X: d.shell.folderRect.X + 35, Y: d.shell.folderRect.Y + 74}
	desktopShellDrag(tt, d, inside, ui.Point{X: 18, Y: 18})
	if !slices.ContainsFunc(a.desktopHost(s).Icons, func(i config.DesktopIcon) bool { return i.App == first && i.Folder == "" }) {
		t.Fatal("dragging out did not restore icon")
	}
	desktopShellClick(t, tt, locales.DesktopShellDeleteFolder())
	desktopShellClick(t, tt, locales.Cancel())
	if len(a.desktopHost(s).Folders) != len(desktopPresetFolders)+1 {
		t.Fatal("cancel deleted a folder")
	}
	desktopShellClick(t, tt, locales.DesktopShellDeleteFolder())
	desktopShellClick(t, tt, locales.Delete())
	if len(a.desktopHost(s).Folders) != len(desktopPresetFolders) {
		t.Fatal("confirmed folder was not deleted")
	}
	a.desktopAddShortcut(s, "relative")
	a.desktopAddShortcut(s, "/var/no\nthing")
	a.desktopAddShortcut(s, "/var/log")
	a.desktopAddShortcut(s, "/var//log/")
	h = a.desktopHost(s)
	shortcuts := 0
	for _, icon := range h.Icons {
		if strings.HasPrefix(icon.App, desktopPathPrefix) {
			shortcuts++
			if icon.App != "path:/var/log" {
				t.Fatalf("invalid shortcut %q", icon.App)
			}
		}
	}
	if shortcuts != 1 {
		t.Fatalf("got %d path shortcuts", shortcuts)
	}
	a.changeDesktopPreferences(func(p *config.DesktopSettings) { p.SingleClick = true })
	tt.Frame()
	x, y = desktopShellPoint(d, desktopShellIconPoint(t, a, s, first))
	tt.ClickAt(x, y)
	if len(d.windowsOf(appThisPC)) != 1 {
		t.Fatal("single-click mode did not open app")
	}
}

func TestDesktopShellDockLauncherAndKeys(t *testing.T) {
	a, _, tt, s := desktopShellTestApp(t)
	d := a.desktopOf(s)
	key := func(id shortcutID) { k := a.shortcut(id); tt.Key(k.Mods, k.Key) }
	a.backToTerminal(a.desktopRowOf(s))
	tt.Frame()
	key(scOpenDesktop)
	if !a.current().isDesktopRow() {
		t.Fatal("open desktop shortcut did not select it")
	}
	key(scFullScreen)
	if !a.fullScreen.on || d.shell.root.W != 1280 {
		t.Fatalf("desktop full screen = %v, bounds = %+v", a.fullScreen.on, d.shell.root)
	}
	key(scFullScreen)
	key(scDesktopLauncher)
	if !d.shell.launcher || !tt.Focused(locales.DesktopShellSearch()) {
		t.Fatal("launcher did not focus search")
	}
	tt.Type(locales.DesktopAppBrowser())
	if !slices.Contains(tt.Texts(), locales.DesktopShellTools()) || slices.Contains(tt.Texts(), locales.DesktopShellSystem()) {
		t.Fatalf("launcher results: %q", tt.Texts())
	}
	tt.Key(0, ui.KeyEnter)
	if d.shell.launcher || len(d.windowsOf(appBrowser)) != 1 {
		t.Fatal("Enter did not launch the only match")
	}
	key(scDesktopClose)
	if len(d.windows) != 0 {
		t.Fatal("close shortcut did not close window")
	}
	app := desktopAppByID(appFiles)
	w := a.desktopOpen(s, app.id, nil)
	tt.Frame()
	key(scDesktopSnapRight)
	if w.snapped != "right" {
		t.Fatal("snap right shortcut ignored")
	}
	key(scDesktopMaximize)
	if w.snapped != "" || w.maximized {
		t.Fatal("maximize/restore did not restore a snapped window")
	}
	key(scDesktopMaximize)
	if !w.maximized {
		t.Fatal("maximize shortcut ignored")
	}
	if d.shell.covered {
		t.Fatal("default maximization covered dock")
	}
	a.changeDesktopPreferences(func(p *config.DesktopSettings) { p.CoverDock = true })
	tt.Frame()
	if !d.shell.covered || d.shell.reveal {
		t.Fatal("maximized window did not hide dock")
	}
	x, y := desktopShellPoint(d, ui.Point{X: d.area.W / 2, Y: d.area.H - 2})
	tt.Move(x, y)
	if !d.shell.reveal {
		t.Fatal("pointer did not reveal dock")
	}
	x, y = desktopShellPoint(d, ui.Point{X: d.area.W / 2, Y: d.area.H - 100})
	tt.Move(x, y)
	if d.shell.reveal {
		t.Fatal("dock did not auto-hide")
	}
	key(scDesktopMinimize)
	if !w.minimized {
		t.Fatal("minimize shortcut ignored")
	}
	a.desktopDockActivate(d, s, app, d.windowsOf(app.id))
	if w.minimized || d.focused != w {
		t.Fatal("dock did not restore")
	}
	a.desktopDockActivate(d, s, app, d.windowsOf(app.id))
	if !w.minimized {
		t.Fatal("dock did not minimize focused window")
	}
	key(scDesktopCycle)
	if w.minimized {
		t.Fatal("cycle did not activate minimized window")
	}
	key(scDesktopShow)
	if !w.minimized {
		t.Fatal("show desktop did not hide active window")
	}
	key(scDesktopShow)
	if w.minimized {
		t.Fatal("show desktop did not restore window")
	}
	a.desktopOpen(s, appFiles, nil)
	a.desktopDockActivate(d, s, app, d.windowsOf(app.id))
	if d.shell.dockPopup != app.id {
		t.Fatal("multiple windows did not open a picker")
	}
}

func TestDesktopShellDockPointer(t *testing.T) {
	a, _, tt, s := desktopShellTestApp(t)
	d := a.desktopOf(s)
	a.desktopSetPinned([]string{string(appThisPC), string(appFiles), string(appTrash)})
	tt.Frame()
	tt.Frame()
	point := func(id desktopAppID) ui.Point {
		t.Helper()
		for _, pos := range d.shell.dockPositions {
			if pos.id == id {
				return ui.Point{X: pos.center, Y: d.area.H - 34}
			}
		}
		t.Fatalf("no dock tile for %q", id)
		return ui.Point{}
	}
	click := func(id desktopAppID) { x, y := desktopShellPoint(d, point(id)); tt.ClickAt(x, y) }
	click(appFiles)
	if len(d.windowsOf(appFiles)) != 1 {
		t.Fatalf("dock did not open Files: positions=%+v, pointer=%+v, windows=%v", d.shell.dockPositions, d.shell.pointer, len(d.windows))
	}
	w := d.windowsOf(appFiles)[0]
	click(appFiles)
	if !w.minimized {
		t.Fatal("dock did not minimize")
	}
	click(appFiles)
	if w.minimized || d.focused != w {
		t.Fatal("dock did not restore and focus")
	}
	other := a.desktopOpen(s, appFiles, nil)
	w.title = "/var/log"
	other.title = "/home"
	tt.Frame()
	click(appFiles)
	if d.shell.dockPopup != appFiles {
		t.Fatal("picker did not open")
	}
	if !slices.Contains(tt.Texts(), w.title) {
		t.Fatal("picker omitted window")
	}
	tt.Key(0, ui.KeyEscape)
	if d.shell.dockPopup != "" {
		t.Fatal("Escape did not close picker")
	}
	desktopShellDrag(tt, d, point(appThisPC), point(appTrash))
	if got := a.desktopPinned(); !slices.Equal(got, []string{string(appFiles), string(appTrash), string(appThisPC)}) {
		t.Fatalf("reordered pins: %v", got)
	}
	x, y := desktopShellPoint(d, point(appFiles))
	tt.RightClickAt(x, y)
	desktopShellMenu(t, tt, locales.DesktopDockCloseAll())
	if len(d.windowsOf(appFiles)) != 0 {
		t.Fatal("Close all did not close both windows")
	}
	a.desktopToggleLauncher(d)
	tt.Frame()
	x, y = desktopShellPoint(d, ui.Point{X: d.area.W - 8, Y: 20})
	tt.ClickAt(x, y)
	if d.shell.launcher {
		t.Fatal("outside press did not close launcher")
	}
}

func TestDesktopShellHostMenuAndClock(t *testing.T) {
	a, _, tt, s := desktopShellTestApp(t)
	d := a.desktopOf(s)
	tt.Advance(time.Minute)
	clock := func() string {
		for _, text := range tt.Texts() {
			if _, err := time.Parse("15:04", text); err == nil {
				return text
			}
		}
		return ""
	}
	before := clock()
	tt.Advance(time.Minute)
	if after := clock(); before == "" || after == "" || before == after {
		t.Fatalf("dock clock did not advance: %q → %q", before, after)
	}
	other := demoSession(a, "db-2", "10.0.0.22")
	a.openDesktop(s)
	tt.Frame()
	x, y := desktopShellPoint(d, d.shell.hostButton)
	tt.ClickAt(x, y)
	if !d.shell.hostPopup || !slices.Contains(tt.Texts(), s.statusText()) {
		t.Fatal("host popup did not show status")
	}
	desktopShellClick(t, tt, locales.SwitchToTerminalMode())
	if a.current() != s {
		t.Fatal("back to terminal did not select the connection")
	}
	a.desktopFromHost(&a.connTree, other.Host)
	tt.Frame()
	if a.current() != a.desktopRowOf(other) {
		t.Fatal("host entry did not reuse the connected session")
	}
}

func TestDesktopShellPersonalizationControls(t *testing.T) {
	a, p, tt, s := desktopShellTestApp(t)
	w := a.desktopOpen(s, appSettings, "personalize")
	settings := *w.app
	settings.view = func(a *App, c *ui.Context, _ *desktopWindow) { a.desktopPersonalizePage(c) }
	w.app = &settings
	desktopSetWindowRect(w, ui.Rect{X: 100, Y: 4, W: 780, H: 730})
	tt.Frame()
	desktopShellClick(t, tt, locales.DesktopWallpaperAurora())
	if a.desktopPreferences().Wallpaper != "builtin:aurora" {
		t.Fatal("gallery selection was not applied")
	}
	desktopShellClick(t, tt, locales.DesktopWallpaperReset())
	if a.desktopPreferences().Wallpaper != "" {
		t.Fatal("wallpaper did not reset")
	}
	path := filepath.Join(t.TempDir(), "wall.png")
	writeImage(t, path, 64, 32)
	p.pickImage(path)
	desktopShellClick(t, tt, locales.DesktopWallpaperChoose())
	p.until(t, "desktop wallpaper", func() bool { return !a.desktopWall.choosing && a.desktopWall.image != nil })
	if picks := p.imagePicks(); len(picks) != 1 || picks[0].parent != WindowMain {
		t.Fatalf("image dialog: %+v", picks)
	}
	if _, err := config.DesktopWallpaperPath(a.desktopPreferences().Wallpaper); err != nil {
		t.Fatal(err)
	}
	desktopShellClick(t, tt, locales.DesktopShellSingleClick())
	if !a.desktopPreferences().SingleClick {
		t.Fatal("personalization did not set single-click mode")
	}
}

func TestDesktopShellSettingsPersistence(t *testing.T) {
	a, _, _, s := desktopShellTestApp(t)
	d := a.desktopOf(s)
	other := demoSession(a, "db-2", "10.0.0.22")
	a.desktopAddShortcut(s, "/home")
	before := a.settings.Desktop
	a.changeDesktopIcons(s, func(h *config.DesktopHost) { h.Icons = nil; h.AutoArrange = true })
	if len(before.Hosts[s.Host.ID].Icons) == 0 || before.Hosts[s.Host.ID].AutoArrange {
		t.Fatal("edited old settings through a pointer")
	}
	if len(a.desktopHostIcons(other, a.desktopHost(other))) == 0 {
		t.Fatal("one host's layout changed another")
	}
	a.desktopSetPinned(nil)
	a.desktopSetWallpaper("builtin:dusk", nil)
	a.changeDesktopPreferences(func(p *config.DesktopSettings) { p.StackWindows = true; p.IconSize = "small" })
	first, second := a.desktopOpen(s, appFiles, nil), a.desktopOpen(s, appFiles, nil)
	if first.x != second.x || first.y != second.y {
		t.Fatalf("stack mismatch: %+v %+v", first, second)
	}
	loaded := config.LoadSettings()
	p := loaded.Desktop
	if p == nil || p.Wallpaper != "builtin:dusk" || !p.StackWindows || p.IconSize != "small" || p.Pinned == nil || len(*p.Pinned) != 0 {
		t.Fatalf("settings not saved: %+v", p)
	}
	h := p.Hosts[s.Host.ID]
	if !h.AutoArrange || !h.IconsConfigured || len(a.desktopHostIcons(s, h)) != 0 {
		t.Fatalf("empty desktop did not survive load: %+v", h)
	}
	if len(d.windows) != 2 {
		t.Fatal("bad test desktop")
	}
}

func TestDesktopShellWallpaperImport(t *testing.T) {
	t.Setenv("FINALLYSHELL_DIR", t.TempDir())
	file, err := os.Create(filepath.Join(t.TempDir(), "wall.png"))
	if err != nil {
		t.Fatal(err)
	}
	if err = png.Encode(file, image.NewRGBA(image.Rect(0, 0, 64, 32))); err != nil {
		t.Fatal(err)
	}
	if err = file.Close(); err != nil {
		t.Fatal(err)
	}
	name, bmp, err := desktopImportWallpaper(file.Name())
	if err != nil || bmp == nil {
		t.Fatalf("import failed: %v", err)
	}
	if w, h := ui.ImageSize(bmp); w != 64 || h != 32 {
		t.Fatalf("image is %gx%g", w, h)
	}
	if path, err := config.DesktopWallpaperPath(name); err != nil || filepath.Dir(path) != config.Dir() {
		t.Fatalf("copy path %q: %v", path, err)
	}
	if _, err := config.DesktopWallpaperPath("desktop-wallpaper-../../secret.png"); err == nil {
		t.Fatal("unsafe wallpaper filename accepted")
	}
}

func TestDesktopShellShortcutDefaults(t *testing.T) {
	for _, goos := range []string{"darwin", "windows", "linux"} {
		for id := scOpenDesktop; id <= scDesktopLauncher; id++ {
			k := shortcutKey(&config.Shortcuts{}, goos, id)
			if k.none() || len(shortcutConflicts(&config.Shortcuts{}, goos, id, k)) > 0 {
				t.Errorf("%s: conflicting desktop shortcut %v for %v", goos, k, id)
			}
		}
	}
}

// TestDesktopShellWindowPastEdges moves a window partly past the
// desktop's left, right and bottom edges, keeping part of its title bar on
// the desktop, and resizes it there without it jumping back.
func TestDesktopShellWindowPastEdges(t *testing.T) {
	a, _, tt, s := desktopShellTestApp(t)
	w := a.desktopOpen(s, appFiles, nil)
	tt.Frame()
	d := a.desktopOf(s)
	area := desktopWorkArea(d, false)
	drag := func(grab, to ui.Point) {
		x, y := desktopShellPoint(d, grab)
		tt.Press(x, y)
		tt.Move(x+6, y)
		x, y = desktopShellPoint(d, to)
		tt.Move(x, y)
		tt.Release(x, y)
		tt.Frame()
	}
	// The pointer stays 20 DIPs inside the edges, short of snapping.
	grab := func() ui.Point { return ui.Point{X: w.x + 60, Y: w.y + desktopTitleHeight/2} }
	drag(grab(), ui.Point{X: 20, Y: 200})
	if w.maximized || w.snapped != "" || w.x >= 0 || w.x+w.w < desktopReachableW {
		t.Fatalf("past the left edge: x=%v w=%v snapped=%q", w.x, w.w, w.snapped)
	}
	drag(grab(), ui.Point{X: area.W - 20, Y: 200})
	if w.snapped != "" || w.x+w.w <= area.W || w.x > area.W-desktopReachableW {
		t.Fatalf("past the right edge: x=%v w=%v area=%v", w.x, w.w, area.W)
	}
	drag(grab(), ui.Point{X: area.W / 2, Y: area.H - 20})
	if w.maximized || w.y+w.h <= area.H || w.y > area.H-desktopTitleHeight {
		t.Fatalf("past the bottom edge: y=%v h=%v area=%v", w.y, w.h, area.H)
	}
	// However far it goes, the title bar stays in reach.
	r := desktopReachable(ui.Rect{X: -5000, Y: 5000, W: w.w, H: w.h}, w, area)
	if r.X+r.W != desktopReachableW || r.Y != area.H-desktopTitleHeight {
		t.Fatalf("far left and down: %+v", r)
	}
	r = desktopReachable(ui.Rect{X: 5000, Y: -300, W: w.w, H: w.h}, w, area)
	if r.X != area.W-desktopReachableW || r.Y != 0 {
		t.Fatalf("far right and up: %+v", r)
	}
	// Placing a window still puts all of it on the desktop.
	a.placeDesktopWindow(d, w)
	if w.x < 0 || w.x+w.w > area.W || w.y < 0 || w.y+w.h > area.H {
		t.Fatalf("placed at %+v in %+v", desktopWindowRect(w), area)
	}
}
