package cloudsync

import (
	"errors"
	"time"
)

// The kinds of sync password a file's header can name (Request.KeyHint,
// Snapshot.KeyHint): never the password, only which kind it is, so that
// another device of the same account knows what to try or to ask for.
const (
	KeyEmail  = "email"  // the account's email address
	KeyCustom = "custom" // one the user set
)

// PasswordError is ErrWrongPassword with what the file's header says of
// the password it was sealed with, and who wrote it: errors.Is(err,
// ErrWrongPassword) holds for it.
type PasswordError struct {
	Hint   string // KeyEmail, KeyCustom, or "" when the header says none
	Device string
	At     time.Time
}

func (e *PasswordError) Error() string { return ErrWrongPassword.Error() }
func (e *PasswordError) Unwrap() error { return ErrWrongPassword }

// openRemote opens the remote file with req.Password, else with each of
// req.AltPasswords: the payload and the password that opened it. None
// opening it, the error is a *PasswordError.
func openRemote(req Request, data []byte) (*Payload, string, error) {
	p, err := openPayload(req.Password, data)
	if !errors.Is(err, ErrWrongPassword) {
		return p, req.Password, err
	}
	for _, alt := range req.AltPasswords {
		if alt == "" || alt == req.Password {
			continue
		}
		if p, err := openPayload(alt, data); err == nil {
			return p, alt, nil
		}
	}
	meta := readSnapshotPrefix(data)
	return nil, "", &PasswordError{Hint: meta.KeyHint, Device: meta.Device, At: meta.At}
}

// ReadSnapshot is the plaintext header of a file: who wrote it, when,
// and the kind of password it is sealed with. Zero for none.
func ReadSnapshot(data []byte) Snapshot { return readSnapshotPrefix(data) }
