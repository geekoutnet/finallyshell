package monitor

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestDesktopTrashInfo(t *testing.T) {
	root := "/media/data"
	info := "[Trash Info]\r\nPath=docs/a%20b%2B%27.txt\r\nDeletionDate=2026-05-21T16:30:42\r\nX-Finallyshell-Mode=640\nX-Finallyshell-Owner=alice:staff\nX-Finallyshell-Size=120\nX-Finallyshell-Note=%E6%B5%8B%E8%AF%95%0A%2B\n[Other]\nPath=/other"
	item := parseTrashInfo(info, root, root)
	if item.Original != "/media/data/docs/a b+'.txt" || item.Deleted != "2026-05-21T16:30:42" || item.Mode != "640" || item.Owner != "alice:staff" || item.Size != 120 || item.Note != "测试\n+" {
		t.Fatalf("metadata: %+v", item)
	}
	for _, value := range []string{"../escape", "%2Fsys%2Fkernel", "docs/%00x", "a/%ZZ", "docs//file"} {
		if got := parseTrashInfo("[Trash Info]\nPath="+value, root, root).Original; got != "" {
			t.Errorf("unsafe origin %q decoded as %q", value, got)
		}
	}
	if got := trashEncode("/home/a b+'.txt"); got != "/home/a%20b%2B%27.txt" {
		t.Errorf("encoded path: %s", got)
	}
	if got := parseTrashInfo("[Trash Info]\nPath=assets/dev/report", "", "/home/data").Original; got != "/home/data/assets/dev/report" {
		t.Errorf("relative home-trash path: %q", got)
	}
}

func TestDesktopTrashLocalScripts(t *testing.T) {
	shell := "/bin/sh"
	if runtime.GOOS == "windows" {
		shell = filepath.Join(os.Getenv("ProgramFiles"), "Git", "bin", "sh.exe")
	} else if runtime.GOOS != "linux" {
		t.Skip("requires Linux commands or Git for Windows")
	}
	if _, err := os.Stat(shell); err != nil {
		t.Skip(err)
	}
	runShell := func(ctx context.Context, script string) (string, string, error) {
		cmd := exec.CommandContext(ctx, shell, "-s")
		cmd.Env = append(os.Environ(), "MSYS=enable_pcon", "CYGWIN=nodosfilewarning")
		cmd.Stdin = strings.NewReader(script)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		return string(out), stderr.String(), err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	base := filepath.ToSlash(t.TempDir())
	if runtime.GOOS == "windows" {
		base = "/" + strings.ToLower(base[:1]) + base[2:]
	}
	run := func(ctx context.Context, script string) (string, string, error) {
		return runShell(ctx, "HOME="+privilegeQuote(base+"/home")+"; XDG_DATA_HOME="+privilegeQuote(base+"/data")+"\n"+script)
	}
	local := func(script string) string {
		t.Helper()
		out, stderr, err := run(ctx, "set '-e'\n"+script+"\n")
		if err != nil {
			t.Fatalf("fixture: %v, %s", err, stderr)
		}
		return out
	}
	parent := base + "/projects"
	original := parent + "/Q'1 + report.txt"
	quoted := privilegeQuote(original)
	local("mkdir '-p' '--' " + privilegeQuote(base+"/home") + " " + privilegeQuote(parent) + "\nprintf original >" + quoted)
	first, err := TrashPut(ctx, run, original, "备注 % +\n", false)
	if err != nil {
		t.Fatal(err)
	}
	info := first.Store + "/info/" + first.Name + ".trashinfo"
	metadata := local("cat '--' " + privilegeQuote(info))
	if !strings.Contains(metadata, "Path="+trashEncode(original)+"\n") || !strings.Contains(metadata, "X-Finallyshell-Note=%E5%A4%87%E6%B3%A8%20%25%20%2B%0A\n") {
		t.Fatalf("unexpected trashinfo: %q", metadata)
	}
	local("printf newer >" + quoted)
	second, err := TrashPut(ctx, run, original, "", false)
	if err != nil || second.Name != first.Name+".2" {
		t.Fatalf("second item: %+v, %v", second, err)
	}
	local("printf replacement >" + quoted)
	local("printf orphan >" + privilegeQuote(first.Store+"/files/.orphan") + "\ntouch '-t' '200101010000' " + privilegeQuote(first.Store+"/info/old.trashinfo"))
	inventory, err := TrashList(ctx, run, false)
	if err != nil || len(inventory.Items) != 3 || len(inventory.Orphans) != 1 || TrashSize(inventory.Items) != 19 {
		t.Fatalf("listing: %+v, %v", inventory, err)
	}
	for _, item := range inventory.Items {
		if item.Name == first.Name {
			first = item
		} else if item.Name == second.Name {
			second = item
		} else if item.Original != "" {
			t.Fatalf("orphan has origin: %+v", item)
		}
	}
	var conflict *TrashConflict
	if _, err := TrashRestore(ctx, run, first, TrashCheck, ""); !errors.As(err, &conflict) || conflict.Stamp == "" {
		t.Fatalf("expected conflict, got %v", err)
	}
	kept, err := TrashRestore(ctx, run, first, TrashKeepBoth, conflict.Stamp)
	if err != nil || kept != original+" (restored)" || local("cat '--' "+privilegeQuote(kept)) != "original" || local("cat '--' "+quoted) != "replacement" {
		t.Fatalf("keep both: %q, %v", kept, err)
	}
	if _, err := TrashRestoreWithNote(ctx, run, second, TrashReplace, conflict.Stamp, "keep the newer note"); err != nil || local("cat '--' "+quoted) != "newer" {
		t.Fatalf("replace: %v", err)
	}
	inventory, err = TrashList(ctx, run, false)
	if err != nil || len(inventory.Items) != 2 {
		t.Fatalf("replacement not recoverable: %+v, %v", inventory, err)
	}
	for _, item := range inventory.Items {
		if item.Original == original && item.Note != "keep the newer note" {
			t.Fatalf("replacement lost metadata: %+v", item)
		}
	}
	if err := TrashEmpty(ctx, run, inventory.Items); err != nil {
		t.Fatal(err)
	}
	inventory, err = TrashList(ctx, run, false)
	if err != nil || len(inventory.Items) != 0 {
		t.Fatalf("not empty: %+v, %v", inventory, err)
	}
	mount := base + "/partition with spaces"
	partitionPaths := strings.ReplaceAll(mount, " ", `\040`)
	partitionRun := func(ctx context.Context, script string) (string, string, error) {
		script = strings.Replace(script, "init_roots\n", "init_roots\nmountpoints=$(printf '%s\\n%s' \"$mountpoints\" "+privilegeQuote(partitionPaths)+")\n", 1)
		return run(ctx, script)
	}
	directory := mount + "/staging/test folder"
	local("mkdir '-p' '--' " + privilegeQuote(directory) + "\nprintf 'sample' >" + privilegeQuote(directory+"/data"))
	trashed, err := TrashPut(ctx, partitionRun, directory, "", false)
	if err != nil || trashed.Mount != mount || !strings.HasPrefix(trashed.Store, mount+"/.Trash-") {
		t.Fatalf("partition trash: %+v, %v", trashed, err)
	}
	info = trashed.Store + "/info/" + trashed.Name + ".trashinfo"
	if got := local("cat '--' " + privilegeQuote(info)); !strings.Contains(got, "\nPath=staging/test%20folder\n") {
		t.Fatalf("expected partition-relative metadata: %q", got)
	}
	cache := trashed.Store + "/directorysizes"
	if got := local("cat '--' " + privilegeQuote(cache)); !strings.HasSuffix(got, " test%20folder\n") {
		t.Fatalf("missing directory size cache: %q", got)
	}
	inventory, err = TrashList(ctx, partitionRun, false)
	if err != nil || len(inventory.Items) != 1 || !inventory.Items[0].Directory || inventory.Items[0].Original != directory {
		t.Fatalf("partition listing: %+v, %v", inventory, err)
	}
	local("rmdir '--' " + privilegeQuote(mount+"/staging"))
	item := inventory.Items[0]
	got, err := TrashRestore(ctx, partitionRun, item, TrashCheck, "")
	if err != nil || got != directory || local("cat '--' "+privilegeQuote(directory+"/data")) != "sample" {
		t.Fatalf("restore missing parent: %q, %v", got, err)
	}
	if got := local("cat '--' " + privilegeQuote(cache)); got != "" {
		t.Fatalf("restored directory still listed in cache: %q", got)
	}
	if err := TrashDelete(ctx, partitionRun, item); !errors.Is(err, ErrTrashChanged) {
		t.Fatalf("stale deletion must fail: %v", err)
	}
	if _, err := TrashPut(ctx, run, base+"/home", "", false); !errors.Is(err, ErrTrashPath) {
		t.Fatalf("home must be protected: %v", err)
	}
}

func TestDesktopTrashCancelledBatch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	items := []TrashItem{{Store: "/home/ops/Trash", Name: "first", stamp: "first version"}, {Store: "/home/ops/Trash", Name: "second", stamp: "second version"}}
	calls := 0
	err := TrashEmpty(ctx, func(_ context.Context, script string) (string, string, error) {
		calls++
		if !strings.HasSuffix(script, "\npurge '/home/ops/Trash' 'first' 'first version'\n") {
			t.Fatal("unexpected purge command")
		}
		cancel()
		return "", "", nil
	}, items)
	if calls != 1 || !errors.Is(err, context.Canceled) {
		t.Fatalf("expected first item only, got %d operations and %v", calls, err)
	}
}
