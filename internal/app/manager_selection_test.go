package app

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"finallyshell/internal/config"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

func managerClickWith(t *testing.T, tt *ui.Tester, mods ui.Modifiers, name string) {
	t.Helper()
	x, y := rowCenter(t, tt, name)
	tt.ClickAtWith(mods, x, y)
}

func managerClick(t *testing.T, tt *ui.Tester, name string) {
	t.Helper()
	if err := tt.Click(name); err != nil {
		t.Fatalf("%v; texts %q", err, tt.Texts())
	}
}

func managerShows(t *testing.T, tt *ui.Tester, names ...string) {
	t.Helper()
	for _, name := range names {
		if !tt.HasText(name) {
			t.Fatalf("missing %q; texts %q", name, tt.Texts())
		}
	}
}

func TestManagerSelectionDelete(t *testing.T) {
	a, p, tt := openTestManager(t)
	saves := 0
	a.saveHosts = func(*config.Store) error { saves++; return nil }
	managerClickWith(t, tt, 0, "web-1")
	managerClickWith(t, tt, ui.Cmd, "web-2")
	managerShows(t, tt, "Delete (2)", "2 items selected")
	saveShot(t, tt, "manager-selection-en-light.png")
	tt.Key(0, ui.KeyDelete)
	managerShows(t, tt, "Move 2 connections to Deleted?", "web-1", "web-2")
	saveShot(t, tt, "manager-selection-confirm-en-light.png")
	managerClick(t, tt, locales.Cancel())
	if a.hosts.Host("web-1").Deleted() || a.hosts.Host("web-2").Deleted() || saves != 0 {
		t.Fatalf("cancel changed hosts; saves = %d", saves)
	}
	p.SetLocale("zh-Hans")
	tt.SetSize(managerSpec.MinWidth, managerSpec.MinHeight)
	tt.SetDark(true)
	tt.Frame()
	managerShows(t, tt, "删除 (2)", "已选择 2 项")
	saveShot(t, tt, "manager-selection-zh-dark.png")
	managerClick(t, tt, "删除 (2)")
	managerShows(t, tt, "将 2 个连接移到已删除列表？")
	saveShot(t, tt, "manager-selection-confirm-zh-dark.png")
	managerClick(t, tt, locales.Delete())
	if !a.hosts.Host("web-1").Deleted() || !a.hosts.Host("web-2").Deleted() || saves != 1 {
		t.Fatalf("expected both hosts in Deleted and one save; saves = %d", saves)
	}
}

func TestManagerSelectionRange(t *testing.T) {
	a, _, tt := openTestManager(t)
	managerClickWith(t, tt, 0, "pg-main")
	managerClickWith(t, tt, ui.Shift, "web-2")
	managerShows(t, tt, "3 items selected")
	tt.Key(ui.Shift, ui.KeyUp)
	managerShows(t, tt, "2 items selected")
	tt.Key(0, ui.KeyEscape)
	managerShows(t, tt, "1 item selected")
	tt.Key(0, ui.KeyDown)
	if selected := a.manager.selectedRows(); !slices.Equal(selected, []managerItem{{host: "web-2"}}) {
		t.Fatalf("Down did not choose web-2: %+v", selected)
	}
	managerClickWith(t, tt, ui.Cmd, "web-2")
	if tt.HasText("Delete (1)") {
		t.Fatal("modifier click did not toggle the selected row off")
	}
	managerClickWith(t, tt, ui.Cmd, "web-1")
	managerClickWith(t, tt, ui.Cmd, "web-2")
	managerClickWith(t, tt, 0, "web-2")
	managerShows(t, tt, "1 item selected")
}

func TestManagerSelectionMove(t *testing.T) {
	a, _, tt := openTestManager(t)
	saves := 0
	a.saveHosts = func(*config.Store) error { saves++; return nil }
	managerClickWith(t, tt, 0, "web-1")
	managerClickWith(t, tt, ui.Cmd, "web-2")
	fromX, fromY := rowCenter(t, tt, "web-2")
	toX, toY := rowCenter(t, tt, "Lab")
	dragTo(tt, fromX, fromY, toX, toY)
	if a.hosts.Host("web-1").Folder != "lab" || a.hosts.Host("web-2").Folder != "lab" || saves != 1 {
		t.Fatalf("drag did not move both hosts in one save; saves = %d", saves)
	}
}

func TestManagerSelectionRestore(t *testing.T) {
	a, _, tt := openTestManager(t)
	saves := 0
	a.saveHosts = func(*config.Store) error { saves++; return nil }
	a.hosts.SoftDelete("vps", time.Now())
	managerClick(t, tt, locales.ShowDeleted())
	managerClickWith(t, tt, 0, "old-box")
	managerClickWith(t, tt, ui.Cmd, "vps")
	rowMenu(t, tt, "old-box", "Restore 2 selected connections")
	if a.hosts.Host("old").Deleted() || a.hosts.Host("vps").Deleted() || saves != 1 {
		t.Fatalf("restore did not restore both hosts in one save; saves = %d", saves)
	}
}

func TestManagerSelectionSearch(t *testing.T) {
	a, _, tt := openTestManager(t)
	managerClickWith(t, tt, 0, "vps")
	managerClick(t, tt, locales.SearchHosts())
	tt.Type("web-")
	tt.Key(0, ui.KeyDown)
	tt.Key(ui.Cmd, ui.KeyA)
	managerShows(t, tt, "3 items selected")
	tt.Key(0, ui.KeyDelete)
	managerShows(t, tt, "Delete 3 selected items?", "Production", "web-1", "web-2")
	managerClick(t, tt, locales.Delete())
	for _, id := range []string{"web-1", "web-2", "pg", "pi", "vps"} {
		if got := a.hosts.Host(id).Deleted(); got != strings.HasPrefix(id, "web-") {
			t.Errorf("%s deleted = %v", id, got)
		}
	}
	if a.hosts.Folder("prod") != nil || a.hosts.Folder("db").Parent != "" {
		t.Fatal("the matching folder should be removed without removing its hidden subfolder")
	}
}

func TestManagerSelectionFilters(t *testing.T) {
	a, _, tt := openTestManager(t)
	remote := rdpHost(a)
	remote.Folder = "lab"
	tt.Frame()
	managerClick(t, tt, locales.SelectAll())
	managerShows(t, tt, "9 items selected")
	chooseKindOption(t, tt, locales.FolderFilter(), "Lab")
	chooseKindOption(t, tt, locales.FieldHostKind(), locales.RemoteDesktop())
	managerClickWith(t, tt, 0, "win-1")
	tt.Key(ui.Cmd, ui.KeyA)
	if selected := a.manager.selectedRows(); !slices.Equal(selected, []managerItem{{host: remote.ID}}) {
		t.Fatalf("all included filtered-out rows: %+v", selected)
	}
}

func TestManagerSelectionCheckboxes(t *testing.T) {
	_, _, tt := openTestManager(t)
	check := func(checked, mixed bool) {
		t.Helper()
		box, ok := tt.AccessByRole(ui.RoleCheckBox, locales.SelectAll())
		if !ok || box.Checked != checked || box.Mixed != mixed {
			t.Fatalf("select all checkbox: %+v; checked = %v, mixed = %v", box, checked, mixed)
		}
	}
	check(false, false)
	managerClick(t, tt, "Select web-1")
	check(false, true)
	managerClick(t, tt, "Select web-2")
	managerShows(t, tt, "2 items selected")
	managerClick(t, tt, locales.SelectAll())
	check(true, false)
	managerShows(t, tt, "8 items selected")
	managerClick(t, tt, locales.SelectAll())
	check(false, false)
	if tt.HasText("Delete (8)") || tt.HasText("8 items selected") {
		t.Fatal("the toolbar still shows a selection")
	}
	managerClick(t, tt, locales.CollapseAll())
	managerClick(t, tt, locales.SelectAll())
	managerShows(t, tt, "3 items selected")
}

func TestManagerSelectionMixed(t *testing.T) {
	a, _, tt := openTestManager(t)
	saves := 0
	a.saveHosts = func(*config.Store) error { saves++; return nil }
	managerClick(t, tt, locales.ShowDeleted())
	managerClickWith(t, tt, 0, "Production")
	managerClickWith(t, tt, ui.Cmd, "web-1")
	managerClickWith(t, tt, ui.Cmd, "old-box")
	rowMenu(t, tt, "web-1", "Delete 3 selected items")
	managerShows(t, tt, "Delete 3 selected items?", "Production", "web-1", "old-box",
		locales.ManagerTrashBody(1), locales.ManagerDeleteFoldersBody(), locales.ManagerDeleteForeverSelectionBody(1))
	managerClick(t, tt, locales.Delete())
	if a.hosts.Folder("prod") != nil || a.hosts.Folder("db").Parent != "" || a.hosts.Host("web-2").Folder != "" || a.hosts.Host("web-2").Deleted() {
		t.Fatal("unselected folder contents were not moved up intact")
	}
	if !a.hosts.Host("web-1").Deleted() || a.hosts.Host("old") != nil || saves != 1 {
		t.Fatalf("live and deleted hosts not handled separately; saves = %d", saves)
	}
}

func TestManagerSelectionManyNames(t *testing.T) {
	a, _, tt := openTestManager(t)
	a.hosts.Host("pi").Proxy = &config.ProxyConfig{Kind: config.ProxyJump, JumpHost: "vps"}
	for index := range 12 {
		name := fmt.Sprintf("server-%02d", index)
		a.hosts.Add(&config.Host{ID: name, Name: name})
	}
	tt.SetSize(managerSpec.MinWidth, managerSpec.MinHeight)
	managerClick(t, tt, locales.CollapseAll())
	managerClick(t, tt, locales.SelectAll())
	managerClick(t, tt, "Delete (15)")
	managerShows(t, tt, "Delete 15 selected items?", "and 5 more", locales.ManagerDeleteJumpWarning(1))
	if a.manager.question.more != 5 || len(a.manager.question.names) != 10 {
		t.Fatalf("preview showed %d names and %d more", len(a.manager.question.names), a.manager.question.more)
	}
	managerClick(t, tt, locales.Cancel())
}

func TestManagerSelectionPermanent(t *testing.T) {
	a, _, tt := openTestManager(t)
	saves := 0
	a.saveHosts = func(*config.Store) error { saves++; return nil }
	a.hosts.SoftDelete("vps", time.Now())
	a.passwords["old"], a.passwords[proxySecretKey("vps")] = "pw", "proxy"
	managerClick(t, tt, locales.ShowDeleted())
	managerClickWith(t, tt, 0, "old-box")
	managerClickWith(t, tt, ui.Cmd, "vps")
	managerShows(t, tt, "Delete permanently (2)")
	tt.Key(0, ui.KeyDelete)
	managerShows(t, tt, "Permanently delete 2 connections?", "2 connections will be permanently deleted and cannot be restored.")
	managerClick(t, tt, locales.Cancel())
	if a.hosts.Host("old") == nil || a.hosts.Host("vps") == nil || saves != 0 {
		t.Fatalf("cancel removed hosts; saves = %d", saves)
	}
	managerClick(t, tt, "Delete permanently (2)")
	managerClick(t, tt, locales.DeleteForever())
	if a.hosts.Host("old") != nil || a.hosts.Host("vps") != nil || saves != 1 || len(a.passwords) != 0 {
		t.Fatalf("permanent deletion did not remove hosts and secrets in one save; saves = %d", saves)
	}
}
