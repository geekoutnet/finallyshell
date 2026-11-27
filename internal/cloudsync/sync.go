package cloudsync

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Request is one sync: this device's payload, and what it last synced.
type Request struct {
	Remote   Remote
	Password string
	// Local is this device's payload. Its Secrets count only with
	// Secrets set.
	Local *Payload
	// Secrets says whether this device includes its saved passwords.
	// When it does not, the remote's are kept as they are.
	Secrets bool
	// Base is the envelope this device last synced, as the remote had
	// it; nil for none. It is what the merge compares both sides with.
	Base []byte
	// BaseSecrets says whether this device included its secrets in the
	// sync that made Base. When it did not, the secrets in Base were
	// never this device's, and are not compared with: otherwise each one
	// it lacks would count as deleted here.
	BaseSecrets bool
	// Force replaces the remote file with Local, without reading or
	// merging it: for a new sync password, or to start over.
	Force bool
	// Device and Now name the device and the time in what is uploaded.
	Device         string
	Now            func() time.Time
	AppVersion     string
	Reason         string
	Note           string
	Retention      Retention
	Approval       string
	AllowDeletion  bool
	ManualVersion  bool
	KeepServer     bool
	StrictBase     bool
	ExpectedServer string
	// AskChoice: a sync with no base to merge with (none, or one that no
	// longer opens, as after another device changed the sync password),
	// or with Stale set (this device has not synced for long), may hold
	// an old copy: it returns a *Choice, writing nothing, when this
	// device has connections the remote lacks or has otherwise, for the
	// user to choose; and otherwise merges keeping the remote's version
	// where both sides differ. PreferRemote merges so always
	// (MergePreferRemote), as the user chose.
	AskChoice    bool
	Stale        bool
	PreferRemote bool
	// KeyHint, written in the file's plaintext header, says which kind of
	// password Password is (KeyEmail, KeyCustom); "" for none.
	KeyHint string
	// AltPasswords are tried, in order, when Password does not open the
	// remote file: one that does is the sync's password from then on,
	// for writing too, and Result.Password says so. The base still opens
	// with Password, as this device sealed it.
	AltPasswords []string
	// BasePassword is the password Base is sealed with, when it is not
	// Password: this device changed its password since it last synced.
	BasePassword string

	switched bool // an alternative opened the remote file: Password is it
}

// Result is what a sync did.
type Result struct {
	// Merged is the payload both sides now have. With Request.Secrets
	// unset, its Secrets are the remote's, not to be applied.
	Merged *Payload
	// Envelope is the file the remote now has: the next Request's Base.
	Envelope []byte
	// Uploaded: the remote file was written. Created: it was new.
	Uploaded, Created bool
	// Changed: Merged differs from Local, which is to be updated.
	Changed bool
	// Conflicts is how many items changed on both sides differently;
	// this device's were kept.
	Conflicts int
	// RemoteDevice is the device that wrote the file this one read, ""
	// when it wrote none.
	RemoteDevice string
	// Password is the one of Request.AltPasswords that opened the remote
	// file, and sealed what this sync wrote and Envelope; "" when it was
	// Request.Password.
	Password                  string
	HistoryError              error
	LocalImpact, ServerImpact Changes
}

// maxAttempts is how many times Sync reads, merges and writes again when
// the file changes on the remote in between.
const maxAttempts = 3

// Sync reads the remote file, merges it with the local payload and writes
// the result back when the remote lacks something.
func Sync(ctx context.Context, req Request) (*Result, error) {
	now := req.Now
	if now == nil {
		now = time.Now
	}
	local := req.Local.Clone()
	local.Schema = PayloadSchema
	if req.Reason == "" {
		req.Reason = ReasonManual
	}
	var err error
	for range maxAttempts {
		var res *Result
		if req.Force {
			res, err = replace(ctx, req, local, now)
		} else {
			res, err = syncOnce(ctx, req, local, now)
		}
		if !errors.Is(err, ErrConflict) || req.StrictBase || req.Reason == ReasonRestore {
			return res, err
		}
	}
	return nil, err
}

func syncOnce(ctx context.Context, req Request, local *Payload, now func() time.Time) (*Result, error) {
	res, err := syncWith(ctx, &req, local, now)
	if res != nil && req.switched {
		res.Password = req.Password
	}
	return res, err
}

func syncWith(ctx context.Context, req *Request, local *Payload, now func() time.Time) (*Result, error) {
	data, v, err := req.Remote.Get(ctx)
	if errors.Is(err, ErrNotFound) {
		if req.KeepServer {
			return nil, err
		}
		res, err := upload(ctx, *req, local, nil, nil, nil, now)
		if res != nil {
			res.Created = true
			res.ServerImpact = Compare(nil, local)
		}
		return res, err
	}
	if err != nil {
		return nil, err
	}
	remote, password, err := openRemote(*req, data)
	if err != nil {
		return nil, err
	}
	basePassword := req.Password
	if req.BasePassword != "" {
		basePassword = req.BasePassword
	}
	if password != req.Password {
		req.Password, req.switched = password, true // seals what is written
	}
	if req.KeepServer {
		if req.ExpectedServer != "" && req.ExpectedServer != serverVersion(data) {
			return nil, ErrConflict
		}
		if req.ExpectedServer == "" {
			if err := guardDeletion(*req, local, nil, remote, data); err != nil {
				return nil, err
			}
		}
		if remote.Quick == nil {
			remote.Quick = local.Quick
		}
		return &Result{Merged: remote, Envelope: data, Changed: !Equal(remote, local), RemoteDevice: remote.Device, LocalImpact: Compare(local, remote)}, nil
	}
	var base *Payload
	if req.Base != nil {
		base, _ = openPayload(basePassword, req.Base) // none if it no longer opens
		if base != nil && !req.BaseSecrets {
			base.Secrets = nil
		}
	}
	prefer := req.PreferRemote
	if req.AskChoice && (base == nil || req.Stale) {
		if c := choiceOf(local, remote, data); c != nil {
			return nil, c
		}
		prefer = true
	}
	if !req.Secrets {
		local = local.Clone()
		local.Secrets = remote.Secrets
	}
	merge := Merge
	if prefer {
		merge = MergePreferRemote
	}
	merged, conflicts := merge(base, local, remote)
	if err := guardDeletion(*req, local, remote, merged, data); err != nil {
		return nil, err
	}
	if Equal(merged, remote) && !req.ManualVersion {
		return &Result{
			Merged: remote, Envelope: data, Changed: !Equal(remote, local),
			Conflicts: conflicts, RemoteDevice: remote.Device, LocalImpact: Compare(local, remote),
		}, nil
	}
	res, err := upload(ctx, *req, merged, remote, data, &v, now)
	if res != nil {
		res.Changed = !Equal(merged, local)
		res.Conflicts = conflicts
		res.RemoteDevice = remote.Device
		res.LocalImpact, res.ServerImpact = Compare(local, merged), Compare(remote, merged)
	}
	return res, err
}

// replace writes local in place of whatever the remote has, with a new
// salt, as the password may be new.
func replace(ctx context.Context, req Request, local *Payload, now func() time.Time) (*Result, error) {
	data, v, err := req.Remote.Get(ctx)
	switch {
	case errors.Is(err, ErrNotFound):
		if req.StrictBase && len(req.Base) != 0 {
			return nil, ErrConflict
		}
		res, err := upload(ctx, req, local, nil, nil, nil, now)
		if res != nil {
			res.Created, res.ServerImpact = true, Compare(nil, local)
		}
		return res, err
	case err != nil:
		return nil, err
	}
	if req.StrictBase && !bytes.Equal(req.Base, data) {
		return nil, ErrConflict
	}
	remote, _ := openPayload(req.Password, data)
	if !req.Secrets && remote != nil {
		local.Secrets = remote.Secrets
	}
	if err := guardDeletion(req, local, remote, local, data); err != nil {
		return nil, err
	}
	res, err := upload(ctx, req, local, remote, data, &v, now)
	if res != nil {
		res.ServerImpact = Compare(remote, local)
	}
	return res, err
}

// upload seals p and writes it in place of prev, the envelope read (nil
// for none).
func upload(ctx context.Context, req Request, p, previous *Payload, prevData []byte, prev *Version, now func() time.Time) (*Result, error) {
	out := p.Clone()
	out.Schema, out.Device, out.UpdatedAt = PayloadSchema, req.Device, now().UTC()
	out.Note = req.Note
	plain, err := json.Marshal(out)
	if err != nil {
		return nil, err
	}
	salt := prevData
	if req.Force && previous == nil {
		salt = nil
	}
	env, err := Seal(req.Password, plain, salt)
	if err != nil {
		return nil, err
	}
	meta := Snapshot{At: out.UpdatedAt, Device: req.Device, AppVersion: req.AppVersion, Reason: req.Reason, Counts: out.Counts(), Counted: true, KeyHint: req.KeyHint}
	env, err = attachSnapshot(env, meta)
	if err != nil {
		return nil, err
	}
	if len(env) > maxResponse {
		return nil, fmt.Errorf("cloudsync: the encrypted file exceeds %d bytes", maxResponse)
	}
	var protected []string
	if !UsesNativeRevisions(req.Remote) {
		if req.Reason == ReasonRestore && len(prevData) != 0 {
			metadata := Snapshot{At: out.UpdatedAt, Device: req.Device, AppVersion: req.AppVersion, Reason: ReasonPreRestore}
			if previous != nil {
				metadata.Counts, metadata.Counted = previous.Counts(), true
			}
			if tagged, err := replaceSnapshotHeader(prevData, metadata); err == nil {
				prevData = tagged
			}
		}
		if len(prevData) != 0 {
			oldMeta := readSnapshotPrefix(prevData)
			if oldMeta.At.IsZero() {
				oldMeta = Snapshot{At: out.UpdatedAt, Reason: ReasonPreSync}
				if previous != nil {
					oldMeta.Device, oldMeta.Counts, oldMeta.Counted = previous.Device, previous.Counts(), true
					if !previous.UpdatedAt.IsZero() {
						oldMeta.At = previous.UpdatedAt
					}
				}
				if req.Force {
					oldMeta.Reason = ReasonPreRestore
				}
				if tagged, err := attachSnapshot(prevData, oldMeta); err == nil {
					prevData = tagged
				}
			}
			id := snapshotID(oldMeta.At, oldMeta.Device, prevData)
			if err := req.Remote.PutVersion(ctx, id, prevData); err != nil {
				return nil, fmt.Errorf("cloudsync: save previous version: %w", err)
			}
			protected = append(protected, id)
		}
		id := snapshotID(meta.At, meta.Device, env)
		if err := req.Remote.PutVersion(ctx, id, env); err != nil {
			return nil, fmt.Errorf("cloudsync: save new version: %w", err)
		}
		protected = append(protected, id)
		if _, err := req.Remote.Put(ctx, env, prev); err != nil {
			if errors.Is(err, ErrConflict) {
				_ = req.Remote.DeleteVersion(ctx, id)
			}
			return nil, err
		}
		return &Result{Merged: out, Envelope: env, Uploaded: true, HistoryError: pruneHistory(ctx, req.Remote, out.UpdatedAt, req.Retention, protected...)}, nil
	}
	if _, err := req.Remote.Put(ctx, env, prev); err != nil {
		return nil, err
	}
	return &Result{Merged: out, Envelope: env, Uploaded: true}, nil
}

// openPayload decrypts and reads an envelope.
func openPayload(password string, data []byte) (*Payload, error) {
	plain, err := Open(password, data)
	if err != nil {
		return nil, err
	}
	var p Payload
	if err := json.Unmarshal(plain, &p); err != nil {
		return nil, ErrNotEnvelope
	}
	if p.Schema > PayloadSchema {
		return nil, ErrNewerFormat
	}
	return &p, nil
}
