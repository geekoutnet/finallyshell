package cloudsync

import (
	"context"
	"errors"
	"slices"
	"testing"
)

// A device whose password no longer opens the file tries the others it
// knows: one that opens it seals what the sync writes, the base still
// opening with the old one.
func TestSyncAltPasswords(t *testing.T) {
	r := &memRemote{}
	res := syncOK(t, Request{Remote: r, Password: "custom", Local: hosts(host("a", "A")), KeyHint: KeyCustom})
	// Another device went back to the email address.
	if _, err := Reencrypt(context.Background(), r, "custom", "me@example.com", KeyEmail, "office"); err != nil {
		t.Fatal(err)
	}
	if meta := readSnapshotPrefix(r.data); meta.KeyHint != KeyEmail || meta.Device != "office" {
		t.Fatalf("header %+v", meta)
	}
	res = syncOK(t, Request{Remote: r, Password: "custom", AltPasswords: []string{"old@example.com", "me@example.com"},
		Local: hosts(host("a", "A"), host("b", "B")), Base: res.Envelope, KeyHint: KeyEmail})
	if res.Password != "me@example.com" || !res.Uploaded {
		t.Fatalf("password %q, uploaded %v", res.Password, res.Uploaded)
	}
	p, err := openPayload("me@example.com", r.data)
	if err != nil {
		t.Fatalf("what was written: %v", err)
	}
	if got := names(p.Hosts); !slices.Equal(got, []string{"a=A", "b=B"}) {
		t.Fatalf("hosts %v", got)
	}
	if _, err := Open("me@example.com", res.Envelope); err != nil {
		t.Fatalf("the new base: %v", err)
	}
}

// None opening it, the error says what the header knows.
func TestSyncPasswordError(t *testing.T) {
	r := &memRemote{}
	syncOK(t, Request{Remote: r, Password: "secret", Local: hosts(host("a", "A")), KeyHint: KeyCustom, Device: "office"})
	_, err := Sync(context.Background(), Request{Remote: r, Password: "me@example.com", AltPasswords: []string{"x@example.com"}, Local: &Payload{}})
	var pe *PasswordError
	if !errors.As(err, &pe) || !errors.Is(err, ErrWrongPassword) {
		t.Fatalf("err %v", err)
	}
	if pe.Hint != KeyCustom || pe.Device != "office" || pe.At.IsZero() {
		t.Fatalf("%+v", *pe)
	}
}
