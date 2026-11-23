//go:build windows

package rdp

import (
	"errors"
	"fmt"
	"sync"
	"syscall"
	"unicode/utf16"
	"unsafe"
)

// mstsc reads the password of a connection from the Credential Manager:
// the generic credential "TERMSRV/<host>", as `cmdkey /generic:TERMSRV/
// <host> /user:<user> /pass:<password>` writes it, and the gateway's from
// the domain credential named after the gateway. The app writes them
// with CredWrite, not cmdkey, so that the password is never in a
// process's command line, and only for the logon session
// (CRED_PERSIST_SESSION): they are gone at sign-out even if the app
// does not delete them. While a host is open its credential is held;
// the last to close deletes it, and puts back a generic credential the
// user had under that name before. A domain credential the user has
// already (one mstsc saved for the gateway) is left as it is.

var (
	advapi32        = syscall.NewLazyDLL("advapi32.dll")
	procCredWriteW  = advapi32.NewProc("CredWriteW")
	procCredReadW   = advapi32.NewProc("CredReadW")
	procCredDeleteW = advapi32.NewProc("CredDeleteW")
	procCredFree    = advapi32.NewProc("CredFree")
)

const (
	credTypeGeneric        = 1
	credTypeDomainPassword = 2
	credPersistSession     = 1
	errorNotFound          = syscall.Errno(1168)
)

// credential is CREDENTIALW.
type credential struct {
	Flags              uint32
	Type               uint32
	TargetName         *uint16
	Comment            *uint16
	LastWritten        [2]uint32 // FILETIME
	CredentialBlobSize uint32
	CredentialBlob     *byte
	Persist            uint32
	AttributeCount     uint32
	Attributes         uintptr
	TargetAlias        *uint16
	UserName           *uint16
}

// savedCred is a credential as read, to write it back.
type savedCred struct {
	typ             uint32
	target, user    string
	comment         string
	blob            []byte
	persist         uint32
	hasUser, hasCmt bool
}

type credKey struct {
	typ    uint32
	target string
}

type heldCred struct {
	count  int
	before *savedCred // the user's, put back at the end
	ours   bool       // written by the app, deleted at the end
}

var held = struct {
	sync.Mutex
	creds map[credKey]*heldCred
}{creds: map[credKey]*heldCred{}}

// holdCredential keeps user and password under target, of type typ,
// until release is called: release deletes it, or puts back what was
// there, once every hold of target is released.
func holdCredential(typ uint32, target, user, password string) (release func(), err error) {
	held.Lock()
	defer held.Unlock()
	key := credKey{typ, target}
	h := held.creds[key]
	if h == nil {
		h = &heldCred{}
		before, err := readCred(typ, target)
		switch {
		case err != nil:
			return nil, err
		case before != nil && typ != credTypeGeneric:
			// The user's own, whose password cannot be read back: kept.
			h.ours = false
		default:
			h.before, h.ours = before, true
		}
		held.creds[key] = h
	}
	if h.ours {
		blob := utf16Bytes(password)
		if err := writeCred(&savedCred{typ: typ, target: target, user: user, hasUser: true, blob: blob, persist: credPersistSession}); err != nil {
			if h.count == 0 {
				delete(held.creds, key)
			}
			return nil, err
		}
	}
	h.count++
	var once sync.Once
	return func() { once.Do(func() { releaseCred(key) }) }, nil
}

func releaseCred(key credKey) {
	held.Lock()
	defer held.Unlock()
	h := held.creds[key]
	if h == nil {
		return
	}
	if h.count--; h.count > 0 {
		return
	}
	delete(held.creds, key)
	if !h.ours {
		return
	}
	if h.before != nil {
		writeCred(h.before)
		return
	}
	deleteCred(key.typ, key.target)
}

// utf16Bytes is s in UTF-16LE, as the Credential Manager keeps
// passwords, without a terminating zero.
func utf16Bytes(s string) []byte {
	u := utf16.Encode([]rune(s))
	b := make([]byte, 2*len(u))
	for i, c := range u {
		b[2*i], b[2*i+1] = byte(c), byte(c>>8)
	}
	return b
}

func u16(s string) *uint16 {
	p, err := syscall.UTF16PtrFromString(s)
	if err != nil {
		p, _ = syscall.UTF16PtrFromString("")
	}
	return p
}

func credErr(op string, err error) error { return fmt.Errorf("credential manager: %s: %w", op, err) }

// readCred reads the credential of type typ named target: nil when
// there is none. A domain credential's password is not returned.
func readCred(typ uint32, target string) (*savedCred, error) {
	var p *credential
	if r, _, err := procCredReadW.Call(uintptr(unsafe.Pointer(u16(target))), uintptr(typ), 0, uintptr(unsafe.Pointer(&p))); r == 0 {
		if errors.Is(err, errorNotFound) {
			return nil, nil
		}
		return nil, credErr("read", err)
	}
	defer procCredFree.Call(uintptr(unsafe.Pointer(p)))
	c := &savedCred{typ: typ, target: target, persist: p.Persist}
	if p.UserName != nil {
		c.user, c.hasUser = syscall.UTF16ToString(unsafe.Slice(p.UserName, wcslen(p.UserName))), true
	}
	if p.Comment != nil {
		c.comment, c.hasCmt = syscall.UTF16ToString(unsafe.Slice(p.Comment, wcslen(p.Comment))), true
	}
	if p.CredentialBlobSize > 0 && p.CredentialBlob != nil {
		c.blob = append([]byte(nil), unsafe.Slice(p.CredentialBlob, p.CredentialBlobSize)...)
	}
	return c, nil
}

func wcslen(p *uint16) int {
	n := 0
	for ptr := unsafe.Pointer(p); *(*uint16)(ptr) != 0; n++ {
		ptr = unsafe.Add(ptr, 2)
	}
	return n
}

func writeCred(s *savedCred) error {
	c := credential{
		Type:               s.typ,
		TargetName:         u16(s.target),
		Persist:            s.persist,
		CredentialBlobSize: uint32(len(s.blob)),
	}
	if s.hasUser {
		c.UserName = u16(s.user)
	}
	if s.hasCmt {
		c.Comment = u16(s.comment)
	}
	if len(s.blob) > 0 {
		c.CredentialBlob = &s.blob[0]
	}
	if r, _, err := procCredWriteW.Call(uintptr(unsafe.Pointer(&c)), 0); r == 0 {
		return credErr("write", err)
	}
	return nil
}

func deleteCred(typ uint32, target string) error {
	if r, _, err := procCredDeleteW.Call(uintptr(unsafe.Pointer(u16(target))), uintptr(typ), 0); r == 0 && !errors.Is(err, errorNotFound) {
		return credErr("delete", err)
	}
	return nil
}
