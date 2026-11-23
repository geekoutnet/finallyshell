//go:build windows

package rdp

import (
	"bytes"
	"os"
	"os/exec"
	"testing"
)

// TestHoldCredential writes dummy credentials into this user's
// Credential Manager, for the logon session, and deletes them: only with
// FINALLYSHELL_TEST_CREDMAN=1.
func TestHoldCredential(t *testing.T) {
	if os.Getenv("FINALLYSHELL_TEST_CREDMAN") != "1" {
		t.Skip("set FINALLYSHELL_TEST_CREDMAN=1 to write to the Credential Manager")
	}
	const target = "TERMSRV/finallyshell-test.invalid"
	t.Cleanup(func() { deleteCred(credTypeGeneric, target) })
	deleteCred(credTypeGeneric, target)

	read := func() *savedCred {
		t.Helper()
		c, err := readCred(credTypeGeneric, target)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	// None before: written while held, twice, and deleted after the last.
	r1, err := holdCredential(credTypeGeneric, target, `CORP\tester`, "pässword")
	if err != nil {
		t.Fatal(err)
	}
	c := read()
	if c == nil || c.user != `CORP\tester` || !bytes.Equal(c.blob, utf16Bytes("pässword")) || c.persist != credPersistSession {
		t.Fatalf("held %+v", c)
	}
	// cmdkey, which writes what mstsc reads, lists it.
	if out, err := exec.Command("cmdkey", "/list:"+target).CombinedOutput(); err != nil || !bytes.Contains(out, []byte(target)) || !bytes.Contains(out, []byte(`CORP\tester`)) {
		t.Errorf("cmdkey /list: %s (%v)", out, err)
	}
	r2, err := holdCredential(credTypeGeneric, target, `CORP\tester`, "second")
	if err != nil {
		t.Fatal(err)
	}
	r1()
	r1() // once only
	if c := read(); c == nil || !bytes.Equal(c.blob, utf16Bytes("second")) {
		t.Fatalf("released while held: %+v", c)
	}
	r2()
	if c := read(); c != nil {
		t.Fatalf("left behind: %+v", c)
	}

	// The user's own is put back.
	mine := &savedCred{typ: credTypeGeneric, target: target, user: "me", hasUser: true, blob: utf16Bytes("mine"), persist: credPersistSession}
	if err := writeCred(mine); err != nil {
		t.Fatal(err)
	}
	r3, err := holdCredential(credTypeGeneric, target, "other", "theirs")
	if err != nil {
		t.Fatal(err)
	}
	if c := read(); c == nil || c.user != "other" {
		t.Fatalf("held %+v", c)
	}
	r3()
	if c := read(); c == nil || c.user != "me" || !bytes.Equal(c.blob, mine.blob) {
		t.Fatalf("not put back: %+v", c)
	}
}
