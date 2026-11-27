package cloudsync

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
)

// An old copy on a device that never synced: the old version of a
// connection the remote changed since, and one only it has. Without a
// base the plain merge would keep the old version; a sync that asks
// stops instead, and PreferRemote keeps the remote's.
func TestSyncChoiceForAnOldCopy(t *testing.T) {
	r := &memRemote{}
	cloud := hosts(host("a", "A-new"), host("b", "B"), host("c", "C"))
	syncOK(t, Request{Remote: r, Password: "pw", Local: cloud, Device: "busy"})
	old := hosts(host("a", "A-old"), host("z", "Z"))

	_, err := Sync(context.Background(), Request{Remote: r, Password: "pw", Local: old, AskChoice: true, PreferRemote: true})
	var c *Choice
	if !errors.As(err, &c) {
		t.Fatalf("err %v, want a Choice", err)
	}
	want := Choice{Local: 2, Remote: 3, Merged: 4, OnlyLocal: 1, OnlyRemote: 2, Differ: 1, RemoteDevice: "busy"}
	got := *c
	got.RemoteAt, got.ServerVersion = want.RemoteAt, ""
	if got != want || c.ServerVersion == "" {
		t.Fatalf("choice %+v", *c)
	}
	if r.puts != 1 {
		t.Fatal("asking wrote the file")
	}

	// Merge: both sides kept, the remote's version of a.
	res := syncOK(t, Request{Remote: r, Password: "pw", Local: old, PreferRemote: true})
	if got := names(res.Merged.Hosts); !slices.Equal(got, []string{"a=A-new", "b=B", "c=C", "z=Z"}) {
		t.Fatalf("merged %v", got)
	}

	// The plain merge, for comparison, keeps this device's old version.
	r2 := &memRemote{}
	syncOK(t, Request{Remote: r2, Password: "pw", Local: cloud})
	res = syncOK(t, Request{Remote: r2, Password: "pw", Local: old})
	if got := names(res.Merged.Hosts); !slices.Contains(got, "a=A-old") {
		t.Fatalf("plain merge %v", got)
	}
}

// A base that no longer opens, as after another device changed the sync
// password, is no base: the sync asks. So does a stale one.
func TestSyncChoiceBaseUnusable(t *testing.T) {
	r := &memRemote{}
	res := syncOK(t, Request{Remote: r, Password: "old", Local: hosts(host("a", "A"))})
	syncOK(t, Request{Remote: r, Password: "new", Local: hosts(host("a", "A2"), host("b", "B")), Force: true})
	_, err := Sync(context.Background(), Request{Remote: r, Password: "new", Local: hosts(host("a", "A"), host("z", "Z")), Base: res.Envelope, AskChoice: true})
	var c *Choice
	if !errors.As(err, &c) {
		t.Fatalf("err %v", err)
	}
	// The same with a base that opens, but stale.
	res = syncOK(t, Request{Remote: r, Password: "new", Local: hosts(host("a", "A2"), host("b", "B")), Base: nil, PreferRemote: true})
	_, err = Sync(context.Background(), Request{Remote: r, Password: "new", Local: hosts(host("a", "A3"), host("z", "Z")), Base: res.Envelope, AskChoice: true, Stale: true})
	if !errors.As(err, &c) {
		t.Fatalf("stale: err %v", err)
	}
	// Not stale, a base that opens: three ways, no question.
	if _, err := Sync(context.Background(), Request{Remote: r, Password: "new", Local: hosts(host("a", "A3"), host("z", "Z")), Base: res.Envelope, AskChoice: true}); err != nil {
		t.Fatal(err)
	}
}

// A connection the remote deleted that this device still has counts as
// different, not as this device's own.
func TestSyncChoiceDeletedOnRemote(t *testing.T) {
	gone, _ := json.Marshal(map[string]any{"id": "a", "name": "A", "deletedAt": "2026-01-01T00:00:00Z"})
	c := choiceOf(hosts(host("a", "A"), host("z", "Z")), hosts(gone, host("b", "B")), []byte("x"))
	if c == nil || c.Differ != 1 || c.OnlyLocal != 1 || c.Remote != 1 || c.OnlyRemote != 1 || c.Merged != 2 {
		t.Fatalf("%+v", c)
	}
}

// Nothing to choose: this device has only what the remote has, or the
// remote has nothing; the sync goes on.
func TestSyncChoiceNotNeeded(t *testing.T) {
	r := &memRemote{}
	syncOK(t, Request{Remote: r, Password: "pw", Local: hosts(host("a", "A"), host("b", "B"))})
	res := syncOK(t, Request{Remote: r, Password: "pw", Local: hosts(host("a", "A")), AskChoice: true, PreferRemote: true})
	if got := names(res.Merged.Hosts); !slices.Equal(got, []string{"a=A", "b=B"}) {
		t.Fatalf("merged %v", got)
	}
	syncOK(t, Request{Remote: r, Password: "pw", Local: &Payload{}, AskChoice: true})
	if c := choiceOf(hosts(host("a", "A")), &Payload{}, nil); c != nil {
		t.Fatalf("an empty remote asks: %+v", c)
	}
}

// Lists merge as hosts do; a side an older build wrote, without them,
// neither adds nor deletes any.
func TestMergeLists(t *testing.T) {
	list := func(ids ...string) []json.RawMessage {
		var out []json.RawMessage
		for _, id := range ids {
			out = append(out, host(id, id))
		}
		return out
	}
	base := &Payload{Lists: map[string][]json.RawMessage{"a": list("1", "2")}}
	local := &Payload{Lists: map[string][]json.RawMessage{"a": list("1", "2", "3"), "b": list("x")}}
	remote := &Payload{Lists: map[string][]json.RawMessage{"a": list("2")}} // deleted 1
	m, _ := Merge(base, local, remote)
	if got := names(m.Lists["a"]); !slices.Equal(got, []string{"2=2", "3=3"}) {
		t.Fatalf("a %v", got)
	}
	if got := names(m.Lists["b"]); !slices.Equal(got, []string{"x=x"}) {
		t.Fatalf("b %v", got)
	}
	// An older build's remote, without lists: this device's stay.
	m, _ = Merge(base, local, &Payload{})
	if got := names(m.Lists["a"]); !slices.Equal(got, []string{"1=1", "2=2", "3=3"}) {
		t.Fatalf("against an older build: %v", got)
	}
	if !Equal(&Payload{Lists: NewLists()}, &Payload{}) || Equal(local, base) {
		t.Fatal("Equal")
	}
	// What an older build reads and writes back keeps them: the key is
	// written even with none.
	data, _ := json.Marshal(&Payload{Lists: NewLists()})
	if !strings.Contains(string(data), `"lists":{}`) {
		t.Fatalf("%s", data)
	}
}
