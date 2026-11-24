package cloudsync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"
)

func TestHistoryIncidentGuard(t *testing.T) {
	ctx := context.Background()
	all, damaged := &Payload{}, &Payload{}
	for index := range 27 {
		all.Folders = append(all.Folders, json.RawMessage(fmt.Sprintf(`{"id":"f%d"}`, index)))
	}
	for index := range 384 {
		entry := json.RawMessage(fmt.Sprintf(`{"id":"h%d","folder":"f%d"}`, index, index%27))
		all.Hosts = append(all.Hosts, entry)
		if index < 374 {
			entry = json.RawMessage(fmt.Sprintf(`{"id":"h%d","deletedAt":"2026-11-24T03:00:00Z"}`, index))
		}
		damaged.Hosts = append(damaged.Hosts, entry)
	}
	for _, direction := range []string{"server", "local"} {
		t.Run(direction, func(t *testing.T) {
			remote := &memRemote{}
			base := syncOK(t, Request{Remote: remote, Password: "pw", Local: all}).Envelope
			local := damaged
			if direction == "local" {
				syncOK(t, Request{Remote: remote, Password: "pw", Local: damaged, Force: true, AllowDeletion: true})
				local = all
			}
			writes := remote.puts
			_, err := Sync(ctx, Request{Remote: remote, Password: "pw", Local: local, Base: base})
			var guard *DeletionGuard
			if !errors.As(err, &guard) || remote.puts != writes {
				t.Fatalf("guard=%v, writes=%d (was %d)", err, remote.puts, writes)
			}
			impact := guard.Server
			if direction == "local" {
				impact = guard.Local
			}
			if impact.LiveRemoved != 374 || impact.LiveTotal != 384 || impact.FoldersRemoved != 27 {
				t.Fatalf("impact: %+v", impact)
			}
			changed := local.Clone()
			changed.Hosts = append(changed.Hosts, host("new", "changed after approval"))
			_, err = Sync(ctx, Request{Remote: remote, Password: "pw", Local: changed, Base: base, Approval: guard.Approval})
			if !errors.As(err, &guard) || remote.puts != writes {
				t.Fatalf("old approval covered a changed plan: %v", err)
			}
		})
	}
}

func TestHistoryRetention(t *testing.T) {
	now := time.Date(2026, 11, 24, 12, 30, 0, 0, time.UTC)
	protected := make([]Snapshot, 10)
	for index := range protected {
		protected[index] = Snapshot{ID: fmt.Sprintf("new-%02d", index), At: now.Add(-time.Duration(index) * time.Minute)}
	}
	for _, test := range []struct {
		name  string
		times []time.Time
		limit int
		want  []string
	}{{"all recent", []time.Time{now.Add(-2 * time.Hour), now.Add(-3 * time.Hour)}, 10, nil},
		{"daily", []time.Time{now.AddDate(0, 0, -8), now.AddDate(0, 0, -8).Add(-time.Hour), now.AddDate(0, 0, -9)}, 200, []string{"b"}},
		{"weekly", []time.Time{now.AddDate(0, 0, -36), now.AddDate(0, 0, -36).Add(-time.Hour)}, 200, []string{"b"}},
		{"monthly", []time.Time{now.AddDate(0, -7, 0), now.AddDate(0, -7, -1), now.AddDate(0, -8, 0)}, 200, []string{"b"}},
		{"count limit", []time.Time{now.AddDate(0, 0, -8), now.AddDate(0, 0, -9)}, 11, []string{"b"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			versions := slices.Clone(protected)
			for index, at := range test.times {
				versions = append(versions, Snapshot{ID: string(rune('a' + index)), At: at})
			}
			versions = append(versions, Snapshot{ID: "pin", At: now.AddDate(-10, 0, 0), Pinned: true})
			var removed []string
			for _, version := range Prunable(versions, now, Retention{Max: test.limit}) {
				removed = append(removed, version.ID)
			}
			if !slices.Equal(removed, test.want) {
				t.Errorf("pruned %v, want %v", removed, test.want)
			}
		})
	}
	if removed := Prunable(protected, now.AddDate(1, 0, 0), Retention{Max: 10}); len(removed) != 0 {
		t.Fatalf("newest ten not protected: %v", removed)
	}
}

func TestHistoryEveryChangeAndLegacyProtection(t *testing.T) {
	remote := &memRemote{}
	initial := hosts(host("first", "legacy"))
	plain, _ := json.Marshal(initial)
	legacy, err := Seal("old", plain, nil)
	if err != nil {
		t.Fatal(err)
	}
	remote.data = legacy
	cfg := Request{Remote: remote, Local: initial, Password: "new", Force: true}
	first := syncOK(t, cfg)
	if len(remote.history) != 2 || remote.puts != 1 {
		t.Fatalf("legacy not backed up: %d versions, %d writes", len(remote.history), remote.puts)
	}
	for id, raw := range remote.history {
		meta, err := decodeSnapshot(raw)
		if err != nil {
			t.Fatal(err)
		}
		if id == snapshotID(meta.At, meta.Device, raw) && meta.Reason == ReasonPreRestore {
			if _, err := OpenSnapshot("old", raw); err != nil {
				t.Fatal(err)
			}
		}
	}
	cfg.Force, cfg.Base = false, first.Envelope
	syncOK(t, cfg)
	if len(remote.history) != 2 || remote.puts != 1 {
		t.Fatal("no-op sync added a version")
	}
	cfg.Local = hosts(host("first", "updated"))
	syncOK(t, cfg)
	if len(remote.history) != 3 || remote.puts != 2 {
		t.Fatalf("changed sync: %d versions, %d writes", len(remote.history), remote.puts)
	}
}

type noHistoryWrite struct{ *memRemote }

func (noHistoryWrite) PutVersion(context.Context, string, []byte) error { return ErrAuth }

func TestHistoryBackupFailureDoesNotWriteMainFile(t *testing.T) {
	remote := &memRemote{}
	before := hosts(host("before", "Before"))
	base := syncOK(t, Request{Remote: remote, Password: "pw", Local: before}).Envelope
	_, err := Sync(context.Background(), Request{Remote: noHistoryWrite{remote}, Password: "pw", Base: base, Local: hosts(host("after", "After"))})
	if !errors.Is(err, ErrAuth) || remote.puts != 1 || !slices.Equal(base, remote.data) {
		t.Fatalf("backup error wrote main file: %v (%d writes)", err, remote.puts)
	}
}

func TestHistoryQuickCommandOrderAndContent(t *testing.T) {
	first := json.RawMessage(`{"id":"first","text":"first"}`)
	second := json.RawMessage(`{"id":"second","text":"second"}`)
	base := &Payload{Quick: &QuickCommands{Commands: []json.RawMessage{first, second}}}
	local, remote := base.Clone(), base.Clone()
	local.Quick.Commands[0] = json.RawMessage(`{"id":"first","text":"local edit"}`)
	remote.Quick.Commands = []json.RawMessage{second, json.RawMessage(`{"id":"first","text":"remote edit"}`)}
	merged, conflicts := Merge(base, local, remote)
	if conflicts != 1 || idOf(merged.Quick.Commands[0]) != "second" || !sameJSON(merged.Quick.Commands[1], local.Quick.Commands[0]) {
		t.Fatalf("reorder changed content winner: %s, %d conflicts", merged.Quick.Commands, conflicts)
	}
}

func TestHistoryGuardsPurgesAndLargeFolder(t *testing.T) {
	prior := &Payload{Folders: []json.RawMessage{json.RawMessage(`{"id":"root"}`), json.RawMessage(`{"id":"child","parent":"root"}`)}}
	for index := range 11 {
		prior.Hosts = append(prior.Hosts, json.RawMessage(fmt.Sprintf(`{"id":"h%d","folder":"child"}`, index)))
	}
	next := prior.Clone()
	next.Folders = next.Folders[1:]
	if impact := Deletions(prior, next); !impact.Risky() || impact.LargestSubtree != 11 || impact.LiveRemoved != 0 {
		t.Fatalf("folder mass delete: %+v", impact)
	}
	for index := range prior.Hosts {
		prior.Hosts[index] = json.RawMessage(fmt.Sprintf(`{"id":"h%d","deletedAt":"2026-11-24T03:00:00Z"}`, index))
	}
	next.Hosts = nil
	if impact := Deletions(prior, next); !impact.Risky() || impact.PermanentRemoved != 11 || impact.LiveRemoved != 0 {
		t.Fatalf("permanent deletion: %+v", impact)
	}
}

func TestHistoryMergeAddsMissing(t *testing.T) {
	current := &Payload{Hosts: []json.RawMessage{json.RawMessage(`{"id":"same","name":"Current"}`), json.RawMessage(`{"id":"trash","deletedAt":"2026-11-24T03:00:00Z"}`)}}
	backup := &Payload{Hosts: []json.RawMessage{json.RawMessage(`{"id":"same","name":"Old"}`), json.RawMessage(`{"id":"trash","name":"Keep deleted"}`), json.RawMessage(`{"id":"added","folder":"parent"}`), json.RawMessage(`{"id":"purged","deletedAt":"2026-11-24T03:00:00Z"}`)}, Folders: []json.RawMessage{json.RawMessage(`{"id":"parent"}`)}}
	merged := AddMissing(current, backup, nil)
	if len(merged.Hosts) != 3 || idOf(merged.Hosts[2]) != "added" || len(merged.Folders) != 1 || !slices.EqualFunc(current.Hosts, merged.Hosts[:2], sameJSON) {
		t.Fatalf("merged: %+v", merged)
	}
}

func TestHistoryGuardThreshold(t *testing.T) {
	for _, sample := range []struct {
		total, removed int
		guard          bool
	}{{10, 10, false}, {49, 10, false}, {49, 11, true}, {100, 20, false}, {100, 21, true}, {384, 77, true}} {
		if got := (DeleteImpact{LiveTotal: sample.total, LiveRemoved: sample.removed}).Risky(); got != sample.guard {
			t.Errorf("%d of %d: %t", sample.removed, sample.total, got)
		}
	}
}

func TestHistorySnapshotMetadata(t *testing.T) {
	at := time.Date(2026, 11, 24, 13, 12, 11, 0, time.FixedZone("UTC+8", 8*60*60))
	data := []byte(`{"app":"finallyshell"}`)
	meta := Snapshot{At: at, Device: "../office PC", AppVersion: "1.2.3", Reason: ReasonManual, Counts: Counts{Live: 384}, Counted: true}
	envelope, err := attachSnapshot(data, meta)
	if err != nil {
		t.Fatal(err)
	}
	id := snapshotID(at, meta.Device, envelope)
	if id[:16] != "20261124T051211Z" || !ValidSnapshotID(id) || ValidSnapshotID("../"+id) {
		t.Fatalf("invalid identifier: %q", id)
	}
	got, err := decodeSnapshot(envelope)
	if err != nil || !got.At.Equal(at) || got.Device != meta.Device || got.Counts != meta.Counts || got.Reason != ReasonManual {
		t.Fatalf("metadata %+v, %v", got, err)
	}
}
