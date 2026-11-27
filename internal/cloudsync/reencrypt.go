package cloudsync

import (
	"context"
	"errors"
	"time"
)

// Reencrypt changes the sync password of the remote file: it reads the
// file, opens it with oldPassword and writes the same content sealed with
// newPassword, under a new salt, keeping its plaintext snapshot header.
// Nothing is merged, so what other devices wrote stays as it is. A file
// that newPassword opens already is left alone. The write is conditional
// on the file read, and read again when another device wrote meanwhile.
//
// It returns the file the remote now has, the next sync's base. Versions
// kept before (a gist's revisions, a WebDAV or S3 history) stay sealed
// with the old password; WebDAV and S3 keep the replaced file as a
// version first, as a sync does.
//
// hint is the new password's kind (KeyEmail, KeyCustom), and device the
// device that changes it, both written in the plaintext header.
func Reencrypt(ctx context.Context, remote Remote, oldPassword, newPassword, hint, device string) ([]byte, error) {
	for range maxAttempts {
		data, v, err := remote.Get(ctx)
		if err != nil {
			return nil, err
		}
		plain, err := Open(oldPassword, data)
		if err != nil {
			if _, again := Open(newPassword, data); again == nil {
				return data, nil
			}
			return nil, err
		}
		env, err := Seal(newPassword, plain, nil)
		if err != nil {
			return nil, err
		}
		meta := readSnapshotPrefix(data)
		meta.KeyHint, meta.Device, meta.At = hint, device, time.Now()
		if env, err = attachSnapshot(env, meta); err != nil {
			return nil, err
		}
		if !UsesNativeRevisions(remote) {
			old := readSnapshotPrefix(data)
			if old.At.IsZero() {
				old.At = time.Now()
			}
			if err := remote.PutVersion(ctx, snapshotID(old.At, old.Device, data), data); err != nil {
				return nil, err
			}
		}
		_, err = remote.Put(ctx, env, &v)
		if errors.Is(err, ErrConflict) {
			continue
		}
		if err != nil {
			return nil, err
		}
		return env, nil
	}
	return nil, ErrConflict
}
