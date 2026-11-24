package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"finallyshell/internal/dbclient"
	"finallyshell/internal/monitor"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
	"golang.org/x/crypto/ssh"
)

type dbFake struct {
	batches chan dbclient.Batch
	pages   chan dbclient.Page
	changes chan dbclient.KeyChange
	reads   chan string
	runErr  error
}

func newDBFake() *dbFake {
	return &dbFake{batches: make(chan dbclient.Batch, 16), pages: make(chan dbclient.Page, 16), changes: make(chan dbclient.KeyChange, 16), reads: make(chan string, 32)}
}

func (*dbFake) Close() error { return nil }
func (fake *dbFake) Namespaces(context.Context) ([]dbclient.Namespace, error) {
	fake.reads <- "namespaces"
	return []dbclient.Namespace{{Database: "shop"}, {Database: "warehouse"}}, nil
}
func (*dbFake) Objects(_ context.Context, scope dbclient.Namespace) ([]dbclient.Object, error) {
	return []dbclient.Object{{Database: scope.Database, Namespace: "public", Name: "orders"}, {Database: scope.Database, Namespace: "public", Name: "daily_sales", View: true}}, nil
}
func (fake *dbFake) Browse(_ context.Context, _ dbclient.Object, request dbclient.Page) (dbclient.Result, error) {
	fake.pages <- request
	result := dbclient.Result{Columns: []string{"id", "customer", "status", "note"}, More: request.Index == 0}
	for index, name := range []string{"Northwind", "Greenport", "Westside", "Sunrise", "Hilltop", "Parklane", "Lakeside", "Meadow"} {
		result.Rows = append(result.Rows, []dbclient.Cell{{Text: fmt.Sprint(101 + index + request.Index*dbclient.PageSize)}, {Text: name}, {Text: "paid"}, {Null: index%2 == 0, Text: "Delivery confirmed"}})
	}
	return result, nil
}
func (*dbFake) Structure(context.Context, dbclient.Object) (dbclient.Structure, error) {
	return dbclient.Structure{Columns: dbclient.Result{Columns: []string{"Name", "Type"}, Rows: [][]dbclient.Cell{{{Text: "id"}, {Text: "bigint"}}}}, Create: "CREATE TABLE orders (id bigint PRIMARY KEY);", Reconstructed: true}, nil
}
func (fake *dbFake) Scan(_ context.Context, pattern string, cursor uint64) (dbclient.KeyPage, error) {
	fake.reads <- fmt.Sprintf("scan:%s:%d", pattern, cursor)
	if cursor != 0 {
		return dbclient.KeyPage{Keys: []string{"cache:more"}}, nil
	}
	return dbclient.KeyPage{Keys: []string{"cache:catalog", "cache:orders", "catalog:prices", "session:id"}, Next: 23}, nil
}
func (fake *dbFake) Key(_ context.Context, key string, _ dbclient.KeyCursor) (dbclient.KeyView, error) {
	fake.reads <- key
	return dbclient.KeyView{Type: "hash", TTL: 340000, Editable: true, Data: dbclient.Result{Columns: []string{"Field", "Value"}, Rows: [][]dbclient.Cell{{{Text: "region"}, {Text: "north"}}, {{Text: "source"}, {Text: "inventory"}}, {{Text: "count"}, {Text: "42"}}}}}, nil
}
func (fake *dbFake) Run(ctx context.Context, batch dbclient.Batch) ([]dbclient.Result, error) {
	fake.batches <- batch
	if batch.Text == "SELECT pg_sleep(60)" {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if fake.runErr != nil {
		return nil, fake.runErr
	}
	if batch.Confirmed {
		return []dbclient.Result{{Affected: 2, AffectedKnown: true, Verb: "DELETE", Duration: 4 * time.Millisecond}}, nil
	}
	return []dbclient.Result{{Columns: []string{"status", "orders", "total"}, Rows: [][]dbclient.Cell{{{Text: "paid"}, {Text: "42"}, {Text: "1680.00"}}, {{Text: "pending"}, {Text: "7"}, {Text: "280.00"}}, {{Text: "shipped"}, {Text: "19"}, {Text: "760.00"}}}, Verb: "SELECT", Duration: 17 * time.Millisecond}}, nil
}
func (fake *dbFake) ChangeKey(_ context.Context, change dbclient.KeyChange) error {
	fake.changes <- change
	return nil
}

func setupDBTest(t *testing.T, language string, width, height int, layout string) (*App, *fakePlatform, *ui.Tester, *dbPage, *dbFake) {
	t.Helper()
	a, p := testApp(t)
	p.SetLocale(language)
	s := demoSession(a, "Example SSH", "ssh.invalid")
	a.active = 0
	fake := newDBFake()
	a.db.open = func(context.Context, dbclient.Profile, string, dbclient.Dialer) (dbclient.Client, error) {
		return fake, nil
	}
	switch layout {
	case "full":
		a.openPanelTab(s, tabDatabase)
	case "right":
		a.setPanelOnRight(true)
		a.rightWidth = 460
		a.openBottomTab(tabDatabase)
	default:
		a.terminalPct = 62
		a.openBottomTab(tabDatabase)
	}
	page := a.dbPageOf(s)
	tt := ui.NewTester(a.View, width, height)
	p.tt = tt
	tt.SetLocale(language)
	tt.Frame()
	tt.Frame()
	return a, p, tt, page, fake
}

func connectDBTest(t *testing.T, a *App, p *fakePlatform, page *dbPage, profile dbclient.Profile) *dbConnection {
	t.Helper()
	if err := a.dbSaveProfile(page, profile, false); err != nil {
		t.Fatal(err)
	}
	a.dbConnect(page, profile, "", false)
	p.until(t, "database connection", func() bool { return !page.busy })
	if page.err != nil {
		t.Fatal(page.err)
	}
	return page.active
}

func dbTestProfile(engine dbclient.Engine) dbclient.Profile {
	return dbclient.Profile{ID: string(engine), Name: dbEngineName(engine) + " lab", Engine: engine, Host: "127.0.0.1", Port: engine.Port(), User: "viewer", TLS: "disable"}
}

func TestDBFormAndTree(t *testing.T) {
	a, p, tt, page, fake := setupDBTest(t, "en", 1280, 820, "full")
	clickNamed(t, tt, locales.DbNew())
	clickNamed(t, tt, locales.DbSaveConnect())
	if page.form == nil || !errors.Is(page.form.err, dbclient.ErrProfile) {
		t.Fatalf("invalid profile accepted: %+v", page.form)
	}
	clickNamed(t, tt, locales.Cancel())
	conn := connectDBTest(t, a, p, page, dbTestProfile(dbclient.Postgres))
	clickNamed(t, tt, "shop")
	p.until(t, "table list", func() bool { return !page.busy })
	clickNamed(t, tt, "orders")
	p.until(t, "table data", func() bool { return !page.busy })
	wantTexts(t, tt, "data table", "Northwind", "NULL", "orders")
	<-fake.pages
	conn.where = "id > 101"
	clickNamed(t, tt, locales.DbApplyFilter())
	p.until(t, "filtered rows", func() bool { return !page.busy })
	if got := <-fake.pages; got.Where != "id > 101" || got.Index != 0 {
		t.Fatal(got)
	}
	clickNamed(t, tt, locales.DbNext())
	p.until(t, "next page", func() bool { return !page.busy })
	if got := <-fake.pages; got.Index != 1 || got.Where != "id > 101" {
		t.Fatal(got)
	}
	if header, ok := tt.AccessByRole(ui.RoleColumnHeader, "customer"); ok {
		tt.ClickAt(header.Bounds.X+header.Bounds.W/2, header.Bounds.Y+header.Bounds.H/2)
		p.until(t, "sorted rows", func() bool { return !page.busy })
		select {
		case got := <-fake.pages:
			if got.Sort != "customer" || got.Index != 0 || got.Where != "id > 101" {
				t.Fatal(got)
			}
		default:
			t.Fatal("sorting did not fetch the new first page")
		}
	} else {
		t.Fatal("sortable customer header missing")
	}
}

func TestDBRunConfirmationAndCancel(t *testing.T) {
	a, p, tt, page, fake := setupDBTest(t, "en", 1280, 820, "full")
	conn := connectDBTest(t, a, p, page, dbTestProfile(dbclient.Postgres))
	conn.section, conn.database = 2, "shop"
	conn.code.Buffer.Reset("SELECT status FROM orders")
	tt.Frame()
	clickNamed(t, tt, locales.DbRun())
	p.until(t, "query results", func() bool { return !page.busy })
	if got := <-fake.batches; got.Text != "SELECT status FROM orders" || got.Confirmed {
		t.Fatal(got)
	}
	wantTexts(t, tt, "query results", "paid", locales.DbRows(3)+" · 17ms")
	conn.code.Buffer.Reset("DELETE FROM orders")
	tt.Frame()
	clickNamed(t, tt, locales.DbRun())
	wantTexts(t, tt, "write confirmation", locales.DbConfirmQuery(conn.profile.Name+" / shop"))
	clickNamed(t, tt, locales.Cancel())
	if len(fake.batches) != 0 {
		t.Fatal("cancel executed a mutation")
	}
	clickNamed(t, tt, locales.DbRun())
	clickNamed(t, tt, locales.DbConfirm())
	p.until(t, "write and refresh", func() bool { return !page.busy })
	if got := <-fake.batches; got.Text != "DELETE FROM orders" || !got.Confirmed || got.Database != "shop" {
		t.Fatal(got)
	}
	if len(fake.reads) != 2 {
		t.Fatal("write did not reread the database tree")
	}
	conn.profile.ReadOnly = true
	clickNamed(t, tt, locales.DbRun())
	if !errors.Is(page.err, dbclient.ErrReadOnly) || len(fake.batches) != 0 {
		t.Fatalf("read-only error: %v", page.err)
	}
	conn.code.Buffer.Reset("SELECT pg_sleep(60)")
	tt.Frame()
	clickNamed(t, tt, locales.DbRun())
	clickNamed(t, tt, locales.Cancel())
	p.until(t, "cancelled query", func() bool { return !page.busy })
	if !errors.Is(page.err, context.Canceled) {
		t.Fatal(page.err)
	}
	<-fake.batches
	fake.runErr = errors.New("synthetic query error")
	conn.code.Buffer.Reset("SELECT broken FROM orders")
	tt.Frame()
	clickNamed(t, tt, locales.DbRun())
	p.until(t, "query error", func() bool { return !page.busy })
	wantTexts(t, tt, "query error", "synthetic query error")
}

func TestDBRedisDeleteAndScan(t *testing.T) {
	a, p, tt, page, fake := setupDBTest(t, "en", 1280, 820, "full")
	conn := connectDBTest(t, a, p, page, dbTestProfile(dbclient.Redis))
	clickNamed(t, tt, "cache:catalog")
	p.until(t, "Redis key", func() bool { return !page.busy })
	wantTexts(t, tt, "hash values", "north", "inventory")
	conn.profile.ReadOnly = true
	tt.Frame()
	row, ok := tt.AccessByRole(ui.RoleButton, "cache:catalog")
	if !ok {
		t.Fatal("Redis key tree row missing")
	}
	tt.RightClickAt(row.Bounds.X+row.Bounds.W/2, row.Bounds.Y+row.Bounds.H/2)
	if err := tt.ChooseMenuItem(locales.DbDeleteKeys()); err == nil || !strings.Contains(err.Error(), "is disabled") {
		t.Fatalf("delete should be disabled: %v; menu %q", err, tt.Menu())
	}
	if err := tt.ChooseMenuItem(locales.DbKey()); err != nil {
		t.Fatal(err)
	}
	p.until(t, "Redis key reopened", func() bool { return !page.busy })
	conn.profile.ReadOnly = false
	conn.chosenKeys.Add("cache:catalog")
	conn.chosenKeys.Add("cache:orders")
	a.dbDeleteKeys(page, conn, []string{"cache:catalog", "cache:orders"})
	tt.Frame()
	clickNamed(t, tt, locales.Cancel())
	if len(fake.changes) != 0 {
		t.Fatal("cancel deleted keys")
	}
	a.dbDeleteKeys(page, conn, []string{"cache:catalog", "cache:orders"})
	tt.Frame()
	clickNamed(t, tt, locales.DbConfirm())
	p.until(t, "delete and refresh", func() bool { return !page.busy })
	got := <-fake.changes
	if !got.Confirmed || !got.Delete || strings.Join(got.Keys, ",") != "cache:catalog,cache:orders" || conn.keyPicked || len(fake.reads) != 4 {
		t.Fatalf("%+v, reads=%d", got, len(fake.reads))
	}
	conn.pattern = "cache:*"
	a.dbLoadKeys(page, conn, 0, 0)
	p.until(t, "key scan", func() bool { return !page.busy })
	a.dbLoadKeys(page, conn, conn.keys.Next, 1)
	p.until(t, "next key scan", func() bool { return !page.busy })
	if len(conn.keys.Keys) != 1 || conn.keys.Keys[0] != "cache:more" {
		t.Fatal(conn.keys)
	}
}

func TestDBRedisEditRequiresConfirmation(t *testing.T) {
	a, p, tt, page, fake := setupDBTest(t, "en", 1280, 820, "full")
	conn := connectDBTest(t, a, p, page, dbTestProfile(dbclient.Redis))
	a.dbLoadKey(page, conn, "cache:catalog", dbclient.KeyCursor{}, 0)
	p.until(t, "Redis hash", func() bool { return !page.busy })
	a.dbOpenKeyEdit(page, conn, "region", "south")
	tt.Frame()
	clickNamed(t, tt, locales.DbReviewChange())
	clickNamed(t, tt, locales.Cancel())
	if len(fake.changes) != 0 {
		t.Fatal("cancel changed the hash field")
	}
	clickNamed(t, tt, locales.DbReviewChange())
	clickNamed(t, tt, locales.DbConfirm())
	p.until(t, "updated key and rescan", func() bool { return !page.busy })
	got := <-fake.changes
	if !got.Confirmed || got.Delete || got.Type != "hash" || got.Field != "region" || got.Value != "south" || len(got.Keys) != 1 || got.Keys[0] != "cache:catalog" || len(fake.reads) != 4 {
		t.Fatalf("%+v, reads=%d", got, len(fake.reads))
	}
}

func TestDBDiscoveryRoot(t *testing.T) {
	a, p, _, page, _ := setupDBTest(t, "en", 1280, 820, "full")
	a.db.script = func(_ context.Context, _ *ssh.Client, script string) (string, string, error) {
		if script == dbclient.ListenScript {
			return "LISTEN 0 128 127.0.0.1:5432 0.0.0.0:*\n", "", nil
		}
		return "", "permission denied", nil
	}
	rootCalls := make(chan string, 4)
	page.session.overview.memory.privilegeRunner = func(_ context.Context, request monitor.PrivilegeRequest) (monitor.PrivilegeResult, error) {
		rootCalls <- request.Action
		if request.Action == "" {
			return monitor.PrivilegeResult{Stdout: "root\n"}, nil
		}
		return monitor.PrivilegeResult{Started: true, Stdout: "0.0.0.0:16379->6379/tcp\n"}, nil
	}
	a.dbDiscover(page, false)
	p.until(t, "port detection", func() bool { return !page.busy })
	if len(page.endpoints) != 1 || len(rootCalls) != 0 {
		t.Fatalf("%v", page.endpoints)
	}
	a.dbDiscover(page, true)
	p.until(t, "root port detection", func() bool { return !page.busy })
	if len(page.endpoints) != 2 || page.err != nil {
		t.Fatalf("%v / %v", page.endpoints, page.err)
	}
	if first, second := <-rootCalls, <-rootCalls; first != "" || second != dbclient.DockerScript {
		t.Fatalf("%q / %q", first, second)
	}
}

func TestDBProfileBackups(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db.json")
	p := dbTestProfile(dbclient.MySQL)
	if err := saveDBProfiles(path, dbProfiles{"host": {p}}); err != nil {
		t.Fatal(err)
	}
	first, _ := os.ReadFile(path)
	p.ReadOnly = true
	if err := saveDBProfiles(path, dbProfiles{"host": {p}}); err != nil {
		t.Fatal(err)
	}
	backups, _ := filepath.Glob(path + ".bak-*")
	if len(backups) != 1 {
		t.Fatal(backups)
	}
	backup, _ := os.ReadFile(backups[0])
	profiles, err := loadDBProfiles(path)
	if string(backup) != string(first) || err != nil || !profiles["host"][0].ReadOnly {
		t.Fatalf("%s / %+v / %v", backup, profiles, err)
	}
}

func TestDBCredentialsUseStoreOnly(t *testing.T) {
	a, p, tt, page, fake := setupDBTest(t, "en", 1280, 820, "full")
	credential := "fixture-value-for-store"
	a.dbOpenForm(page, nil, dbclient.Endpoint{})
	form := page.form
	form.profile.Name, form.profile.User, form.profile.Remember, form.password = "Example", "viewer", true, credential
	a.dbSaveForm(page, form)
	p.until(t, "stored database credential", func() bool {
		return !page.busy && page.active != nil && p.fakeSecrets.secrets()[dbSecretKey(page.session.Host.ID, page.active.profile.ID)] == credential
	})
	data, err := os.ReadFile(a.dbConfigPath())
	if err != nil || strings.Contains(string(data), credential) {
		t.Fatal("credential was written outside the system store")
	}
	conn := page.active
	a.dbRun(page, conn, "DELETE FROM orders WHERE note = '"+credential+"'")
	if page.question != nil || len(fake.batches) != 0 || !errors.Is(page.err, dbclient.ErrSecret) {
		t.Fatal("a credential reached the query preview or executor")
	}
	old := conn.profile
	a.dbOpenForm(page, &old, dbclient.Endpoint{})
	page.form.profile.Host = "other.invalid"
	a.dbSaveForm(page, page.form)
	tt.Frame()
	clickNamed(t, tt, locales.DbConfirm())
	p.until(t, "old credential removed", func() bool { return !page.busy && len(p.fakeSecrets.secrets()) == 0 })
	if page.active.profile.Remember {
		t.Fatal("retargeting a connection reused its old credential")
	}
}

func TestDBScreenshots(t *testing.T) {
	if os.Getenv("SHOT_DIR") == "" {
		t.Skip("SHOT_DIR is not set")
	}
	for _, language := range []string{"en", "zh-Hans"} {
		for _, layout := range []string{"full", "short", "right"} {
			t.Run(language+"/"+layout, func(t *testing.T) {
				a, p, tt, page, _ := setupDBTest(t, language, 1280, 820, layout)
				shots := func(scene string) {
					for _, mode := range []string{"light", "dark"} {
						tt.SetDark(mode == "dark")
						tt.Move(0, 0)
						tt.Frame()
						tt.Frame()
						saveShot(t, tt, fmt.Sprintf("db-%s-%s-%s-%s.png", scene, layout, language, mode))
					}
				}
				a.dbOpenForm(page, nil, dbclient.Endpoint{})
				page.form.profile.Name, page.form.profile.User, page.form.profile.Database = "Reporting workspace", "analyst", "shop"
				shots("connect")
				page.form = nil
				conn := connectDBTest(t, a, p, page, dbTestProfile(dbclient.Postgres))
				ns := conn.namespaces[0]
				conn.open[ns] = true
				a.dbLoadObjects(page, conn, ns)
				p.until(t, "objects", func() bool { return !page.busy })
				a.dbOpenObject(page, conn, conn.objects[ns][0], 0)
				p.until(t, "data", func() bool { return !page.busy })
				shots("grid")
				conn.section = 2
				conn.code.Buffer.Reset("SELECT status, COUNT(*) AS orders, SUM(total) AS total\nFROM orders\nGROUP BY status;")
				a.dbRun(page, conn, conn.code.Buffer.String())
				p.until(t, "SQL results", func() bool { return !page.busy })
				shots("sql")
				redis := connectDBTest(t, a, p, page, dbTestProfile(dbclient.Redis))
				a.dbLoadKey(page, redis, "cache:catalog", dbclient.KeyCursor{}, 0)
				p.until(t, "Redis details", func() bool { return !page.busy })
				shots("redis")
			})
		}
	}
}
