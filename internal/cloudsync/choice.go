package cloudsync

import (
	"encoding/json"
	"fmt"
	"time"
)

// Choice is what a sync that asks first (Request.AskChoice) found: this
// device and the remote have different connections, with nothing to tell
// which side is newer, as on a device syncing for the first time, or one
// not synced for long. The sync wrote and changed nothing; the user
// chooses to merge (Request.PreferRemote), to take the remote's
// (KeepServer, with ServerVersion), or to keep this device's (Force).
type Choice struct {
	// Local and Remote are the live connections on each side; Merged how
	// many a merge keeps.
	Local, Remote, Merged int
	// OnlyLocal and OnlyRemote are those on one side only; Differ those
	// on both whose settings differ, Same those alike.
	OnlyLocal, OnlyRemote, Differ, Same int
	// Who wrote the remote file last, and when.
	RemoteDevice string
	RemoteAt     time.Time
	// ServerVersion is the remote file read: taking the remote's applies
	// that file only, not one written since.
	ServerVersion string
}

func (c *Choice) Error() string {
	return fmt.Sprintf("cloudsync: %d connections here, %d on the remote: choose how to sync", c.Local, c.Remote)
}

// MergePreferRemote is Merge, but where both sides changed an item, or
// have it with nothing to compare with, it keeps the remote's: for a
// device whose copy may be old. Merge is symmetric but for that choice
// (and the order, here the remote's), so it is Merge with the sides
// swapped.
func MergePreferRemote(base, local, remote *Payload) (*Payload, int) {
	return Merge(base, remote, local)
}

// choiceOf is the Choice between local and remote, nil when there is
// nothing to choose: this device has no connection the remote lacks or
// has otherwise, or the remote has none.
func choiceOf(local, remote *Payload, raw []byte) *Choice {
	live := func(items []json.RawMessage) map[string]json.RawMessage {
		out := map[string]json.RawMessage{}
		for _, raw := range items {
			info := itemInfoOf(raw)
			if id := idOf(raw); id != "" && info.DeletedAt.IsZero() {
				out[id] = raw
			}
		}
		return out
	}
	l, r := live(local.Hosts), live(remote.Hosts)
	all := byID(remote.Hosts)
	c := &Choice{Local: len(l), Remote: len(r), RemoteDevice: remote.Device, RemoteAt: remote.UpdatedAt, ServerVersion: serverVersion(raw)}
	inBoth := 0
	for id, li := range l {
		ri, ok := all[id]
		switch {
		case !ok:
			c.OnlyLocal++
		case sameJSON(li, ri):
			c.Same++
			inBoth++
		default:
			// Changed, or deleted, on the remote.
			c.Differ++
			if _, live := r[id]; live {
				inBoth++
			}
		}
	}
	c.OnlyRemote = c.Remote - inBoth
	c.Merged = c.Remote + c.OnlyLocal
	if c.Remote == 0 || c.OnlyLocal == 0 && c.Differ == 0 {
		return nil
	}
	return c
}
