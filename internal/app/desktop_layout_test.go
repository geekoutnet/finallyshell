package app

import (
	"slices"
	"strings"
	"testing"

	"finallyshell/internal/config"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

// iconIn reports where the icon id is in h: its folder, "" on the desktop,
// and whether it is there at all.
func iconIn(a *App, s *Session, id string) (string, bool) {
	h := a.desktopHost(s)
	for _, icon := range a.desktopHostIcons(s, h) {
		if icon.App == id {
			return icon.Folder, true
		}
	}
	return "", false
}

func TestDesktopDefaultLayoutGroups(t *testing.T) {
	a, _, tt, s := desktopShellTestApp(t)
	h := a.desktopHost(s)
	if h.HasOwnLayout() || a.desktopOwnLayout(s) {
		t.Fatal("a new host has a layout of its own")
	}
	for _, id := range desktopTopApps {
		if folder, ok := iconIn(a, s, string(id)); desktopAppByID(id) != nil && (!ok || folder != "") {
			t.Errorf("%s: folder %q, shown %v", id, folder, ok)
		}
	}
	for id, want := range map[string]string{"srv-docker": "preset-apps", "srv-firewall": "preset-network", "srv-logs": "preset-system", "srv-transfers": "preset-transfer", string(appSettings): "preset-system"} {
		if folder, _ := iconIn(a, s, id); folder != want {
			t.Errorf("%s in %q, want %q", id, folder, want)
		}
	}
	if got := desktopHostFolders(h); len(got) != len(desktopPresetFolders) || got[0].Name != locales.DesktopFolderSystem() {
		t.Fatalf("folders %+v", got)
	}
	// Every app shown by default is on the desktop or in a folder there.
	for _, app := range desktopAppsFor(s) {
		if _, ok := iconIn(a, s, string(app.id)); app.onDesktop && !ok {
			t.Errorf("%s missing", app.id)
		}
	}
	// The folder opens with its apps, read from the default layout.
	a.desktopOpenIcon(a.desktopOf(s), s, desktopFolderPrefix+"preset-apps")
	tt.Frame()
	if !tt.HasText(locales.DesktopFolderApps()) || !tt.HasText(desktopAppByID("srv-docker").name()) {
		t.Fatal("the preset folder opened empty")
	}
}

func TestDesktopSharedAndOwnLayouts(t *testing.T) {
	a, _, _, s := desktopShellTestApp(t)
	other := demoSession(a, "db-2", "10.0.0.22")
	// A host without a layout of its own changes the shared one.
	d := a.desktopOf(s)
	folder := a.desktopNewFolder(d, s)
	d.shell.rename = ""
	if _, ok := desktopIconFolder(a.desktopHost(other), folder); !ok {
		t.Fatal("a folder made on one host is not on the others sharing the layout")
	}
	if a.desktopPreferences().Hosts[s.Host.ID].HasOwnLayout() {
		t.Fatal("changing the shared layout gave the host one of its own")
	}
	// Its own layout starts as a copy, then goes its own way.
	a.desktopUseOwnLayout(s)
	if !a.desktopOwnLayout(s) || a.desktopOwnLayout(other) {
		t.Fatal("own layout")
	}
	if _, ok := desktopIconFolder(a.desktopHost(s), folder); !ok {
		t.Fatal("the own layout did not start from the shared one")
	}
	a.desktopFlatten(s)
	if len(a.desktopHost(s).Folders) != 0 || len(a.desktopHost(other).Folders) == 0 {
		t.Fatal("flattening one host's own layout changed the shared one")
	}
	a.desktopOrganize(other)
	if len(a.desktopHost(s).Folders) != 0 {
		t.Fatal("tidying the shared layout changed a host's own")
	}
	// Making it the shared layout: the others get it, it keeps its own.
	a.desktopAddShortcut(s, "/var/log")
	a.desktopMakeShared(s)
	if len(a.desktopHost(other).Folders) != 0 {
		t.Fatal("the shared layout is not the host's")
	}
	if _, ok := iconIn(a, other, "path:/var/log"); ok {
		t.Fatal("a shortcut to a host's path went to the shared layout")
	}
	// Back to the shared layout: its own is dropped.
	a.desktopUseSharedLayout(s)
	if a.desktopOwnLayout(s) {
		t.Fatal("still its own layout")
	}
	if _, ok := iconIn(a, s, "path:/var/log"); ok {
		t.Fatal("the dropped layout's shortcut is still shown")
	}
	// Resetting the shared layout brings back the default.
	a.desktopResetLayout(other)
	if _, ok := a.desktopPreferences().Hosts[config.DesktopSharedLayout]; ok {
		t.Fatal("the reset shared layout is still saved")
	}
	if folder, _ := iconIn(a, s, "srv-docker"); folder != "preset-apps" {
		t.Fatalf("reset: docker in %q", folder)
	}
	// Resetting a host's own layout keeps it its own.
	a.desktopUseOwnLayout(s)
	a.desktopFlatten(s)
	a.desktopResetLayout(s)
	if !a.desktopOwnLayout(s) || len(a.desktopHost(s).Folders) != len(desktopPresetFolders) {
		t.Fatalf("reset own layout: own %v, folders %+v", a.desktopOwnLayout(s), a.desktopHost(s).Folders)
	}
}

func TestDesktopShortcutGivesOwnLayout(t *testing.T) {
	a, _, _, s := desktopShellTestApp(t)
	other := demoSession(a, "db-2", "10.0.0.22")
	a.desktopAddShortcut(s, "/srv/www")
	if !a.desktopOwnLayout(s) {
		t.Fatal("a shortcut to a path did not give the host its own layout")
	}
	if _, ok := iconIn(a, s, "path:/srv/www"); !ok {
		t.Fatal("shortcut missing")
	}
	if _, ok := iconIn(a, other, "path:/srv/www"); ok {
		t.Fatal("shortcut shown on another host")
	}
	if folder, _ := iconIn(a, s, "srv-docker"); folder != "preset-apps" {
		t.Fatal("the own layout lost the shared one's folders")
	}
	if !slices.Contains(a.notices, locales.DesktopShellOwnLayoutNotice()) {
		t.Fatal("no notice")
	}
}

func TestDesktopFlattenAndOrganize(t *testing.T) {
	a, _, _, s := desktopShellTestApp(t)
	d := a.desktopOf(s)
	mine := a.desktopNewFolder(d, s)
	d.shell.rename = ""
	a.desktopMoveIcons(s, []string{"srv-docker"}, mine)
	a.desktopFlatten(s)
	h := a.desktopHost(s)
	if len(h.Folders) != 0 {
		t.Fatalf("folders left: %+v", h.Folders)
	}
	for _, app := range desktopAppsFor(s) {
		if folder, ok := iconIn(a, s, string(app.id)); !ok || folder != "" {
			t.Fatalf("%s not on the desktop", app.id)
		}
	}
	// Tidying up brings the preset folders back.
	a.desktopOrganize(s)
	if folder, _ := iconIn(a, s, "srv-docker"); folder != "preset-apps" {
		t.Fatalf("docker in %q", folder)
	}
	if folder, _ := iconIn(a, s, string(appTerminal)); folder != "" {
		t.Fatal("the terminal left the desktop")
	}
	// An app in a folder of the user's own stays there.
	mine = a.desktopNewFolder(d, s)
	d.shell.rename = ""
	a.desktopMoveIcons(s, []string{"srv-docker", "srv-db"}, mine)
	a.desktopOrganize(s)
	if folder, _ := iconIn(a, s, "srv-docker"); folder != mine {
		t.Fatal("tidying moved an app out of the user's folder")
	}
	if _, ok := iconIn(a, s, desktopFolderPrefix+mine); !ok {
		t.Fatal("tidying dropped the user's folder")
	}
	// Each folder has one icon on the desktop.
	count := map[string]int{}
	for _, icon := range a.desktopHostIcons(s, a.desktopHost(s)) {
		if strings.HasPrefix(icon.App, desktopFolderPrefix) {
			count[icon.App]++
		}
	}
	for id, n := range count {
		if n != 1 {
			t.Fatalf("%s shown %d times", id, n)
		}
	}
}

func TestDesktopMoveMenu(t *testing.T) {
	a, _, tt, s := desktopShellTestApp(t)
	d := a.desktopOf(s)
	a.desktopFlatten(s)
	tt.Frame()
	// Two selected icons move together through the menu.
	first, second := "srv-docker", "srv-db"
	pt := desktopShellIconPoint(t, a, s, first)
	x, y := desktopShellPoint(d, pt)
	tt.ClickAt(x, y)
	q := desktopShellIconPoint(t, a, s, second)
	qx, qy := desktopShellPoint(d, q)
	tt.ClickAtWith(ui.Ctrl, qx, qy)
	if !d.shell.selection[first] || !d.shell.selection[second] {
		t.Fatalf("selection %v", d.shell.selection)
	}
	tt.RightClickAt(x, y)
	desktopShellMenu(t, tt, locales.DesktopShellMoveToFolder(), locales.NewFolder())
	folder := d.shell.rename
	d.shell.rename = ""
	if folder == "" {
		t.Fatal("no new folder")
	}
	for _, id := range []string{first, second} {
		if got, _ := iconIn(a, s, id); got != folder {
			t.Fatalf("%s in %q, want %q", id, got, folder)
		}
	}
	// And out of it again, from inside the folder.
	a.desktopOpenIcon(d, s, desktopFolderPrefix+folder)
	tt.Frame()
	var inside bool
	for _, icon := range a.desktopHostIcons(s, a.desktopHost(s)) {
		inside = inside || icon.App == first && icon.Folder == folder
	}
	if !inside {
		t.Fatal("bad folder")
	}
	m := a.desktopIconsOf(d, s, first)
	if len(m) != 1 || m[0] != first {
		t.Fatalf("icons of an unselected icon: %v", m)
	}
	a.desktopMoveIcons(s, m, "")
	if got, _ := iconIn(a, s, first); got != "" {
		t.Fatal("moving out failed")
	}
}

func TestDesktopEntryMode(t *testing.T) {
	a, _, tt, s := desktopTestApp(t)
	// The default opens in the terminal.
	t2 := demoSession(a, "term", "10.0.0.31")
	a.active = slices.Index(a.sessions, t2)
	a.enterInitialMode(t2)
	if a.current() != t2 || a.desktopRowOf(t2) != nil {
		t.Fatal("the default entry mode opened the desktop")
	}
	// Desktop opens in the desktop.
	d2 := demoSession(a, "desk", "10.0.0.32")
	d2.Host.EntryMode = config.EntryDesktop
	a.active = slices.Index(a.sessions, d2)
	a.enterInitialMode(d2)
	if !a.current().isDesktopRow() || a.current().panelOf != d2 {
		t.Fatal("the desktop entry mode did not open the desktop")
	}
	// The mode used last: remembered as the user switches.
	l := demoSession(a, "last", "10.0.0.33")
	l.Host.EntryMode = config.EntryLast
	a.active = slices.Index(a.sessions, l)
	a.enterInitialMode(l)
	if a.current() != l {
		t.Fatal("nothing remembered yet, but the desktop opened")
	}
	a.openDesktop(l)
	tt.Frame()
	if got := a.desktopPreferences().Hosts[l.Host.ID].LastMode; got != desktopLastMode {
		t.Fatalf("last mode %q", got)
	}
	if got := config.LoadSettings().Desktop.Hosts[l.Host.ID].LastMode; got != desktopLastMode {
		t.Fatalf("last mode not saved: %q", got)
	}
	// A new session to it opens in the desktop, even in the background.
	again := newSession(l.Host)
	a.sessions = append(a.sessions, again)
	a.active = 0
	cur := a.current()
	a.enterInitialMode(again)
	if a.current() != cur || again.lastRow == nil || !again.lastRow.isDesktopRow() {
		t.Fatal("a background session did not keep its desktop for when it is selected")
	}
	// Back to the terminal is remembered too.
	a.active = slices.Index(a.sessions, l)
	tt.Frame()
	if got := a.desktopPreferences().Hosts[l.Host.ID].LastMode; got != "" {
		t.Fatalf("terminal not remembered: %q", got)
	}
	// A host in another mode never writes it.
	a.active = slices.Index(a.sessions, t2)
	tt.Frame()
	if _, ok := a.desktopPreferences().Hosts[t2.Host.ID]; ok {
		t.Fatal("a terminal host remembered its mode")
	}
	_ = s
}

func TestEditorEntryMode(t *testing.T) {
	a, p := testApp(t)
	saved := savedHosts(a)
	h := &config.Host{ID: config.NewHostID(), Name: "web", Addr: "10.0.0.5", User: "root", Auth: config.AuthAgent}
	a.hosts.Add(h)
	a.editConnection(h)
	tt := editorWindow(t, a, p)
	tt.SetSize(760, 1400) // the Advanced section in view, whatever the fonts' height
	tt.Frame()
	if !tt.HasText(locales.EditorEntryMode()) || !tt.HasText(locales.EditorEntryTerminal()) {
		t.Fatal("no entry mode, or not the terminal by default")
	}
	if err := tt.Click(locales.EditorEntryTerminal()); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click(locales.EditorEntryDesktop()); err != nil {
		t.Fatal(err)
	}
	if a.editor.entryMode != config.EntryDesktop {
		t.Fatalf("chose %q", a.editor.entryMode)
	}
	if err := tt.Click(locales.Ok()); err != nil {
		t.Fatal(err)
	}
	if h.EntryMode != config.EntryDesktop || !strings.Contains(string(*saved), `"entryMode":"desktop"`) {
		t.Fatalf("not saved: %q", h.EntryMode)
	}
	// Opening it again shows the mode chosen.
	a.editConnection(h)
	tt = editorWindow(t, a, p)
	if !tt.HasText(locales.EditorEntryDesktop()) {
		t.Fatal("the editor did not read the entry mode")
	}
}
