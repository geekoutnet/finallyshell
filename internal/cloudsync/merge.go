package cloudsync

import (
	"bytes"
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"time"
)

// PayloadSchema is the version of the payload this build writes. A
// remote file with a newer one is not merged, as this build would drop
// what it does not know.
const PayloadSchema = 1

// Payload is what the envelope encrypts: what is synced. Folders and
// hosts are the objects of hosts.json, as JSON, each with its "id"; this
// package does not need to know their other keys, and keeps the ones it
// does not know.
type Payload struct {
	Schema    int       `json:"schema"`
	Device    string    `json:"device,omitempty"` // the name of the device that wrote it
	UpdatedAt time.Time `json:"updatedAt,omitzero"`

	Folders []json.RawMessage `json:"folders"`
	Hosts   []json.RawMessage `json:"hosts"`
	// Proxies are the proxy library, by ID. A build with the library
	// always writes the key, [] for none (NewProxies); a build without it
	// writes none, which reads as nil: its payload says nothing of the
	// library, so merging it changes none (Merge).
	Proxies []json.RawMessage `json:"proxies"`
	Quick   *QuickCommands    `json:"quickCommands,omitempty"`
	Note    string            `json:"note,omitempty"`
	// Settings are the settings that are synced, by their key in
	// settings.json.
	Settings map[string]json.RawMessage `json:"settings"`
	// Secrets are saved passwords and passphrases, by their key in the
	// system's credential store: only from devices that include them.
	Secrets map[string]string `json:"secrets,omitempty"`
	// Lists are the rest of what syncs, by name (the List* constants):
	// lists of objects with an "id", merged as Hosts are. A build that
	// syncs them always writes the key, {} for none (NewLists); a build
	// that does not writes none, which reads as nil: its payload says
	// nothing of them, so merging it changes none, as with Proxies. A
	// list missing from a map that is there is empty.
	Lists map[string][]json.RawMessage `json:"lists"`
}

// The lists of Payload.Lists.
const (
	// ListDatabases are the Database tab's connections, as
	// {"id": "<host ID>/<connection ID>", "host": ..., "profile": {...}}.
	ListDatabases = "databases"
	// ListKnownHosts are the trusted host keys, {"id": "<line>"}, a line
	// of known_hosts each.
	ListKnownHosts = "knownHosts"
	// ListHistoryPrefix starts the name of a host's command history,
	// ListHistoryPrefix + host ID, {"id": "<hash>", "cmd": "..."} each,
	// oldest first.
	ListHistoryPrefix = "history/"
)

// NewLists is Lists of a build that syncs them, with none in them.
func NewLists() map[string][]json.RawMessage { return map[string][]json.RawMessage{} }

type QuickCommands struct {
	Groups   []json.RawMessage `json:"groups"`
	Commands []json.RawMessage `json:"commands"`
}

// Clone is a copy of p sharing no maps or lists with it.
func (p *Payload) Clone() *Payload {
	c := *p
	c.Folders = slices.Clone(p.Folders)
	c.Hosts = slices.Clone(p.Hosts)
	c.Proxies = slices.Clone(p.Proxies)
	if p.Quick != nil {
		c.Quick = &QuickCommands{Groups: slices.Clone(p.Quick.Groups), Commands: slices.Clone(p.Quick.Commands)}
	}
	c.Settings = maps.Clone(p.Settings)
	c.Secrets = maps.Clone(p.Secrets)
	if p.Lists != nil {
		c.Lists = make(map[string][]json.RawMessage, len(p.Lists))
		for k, v := range p.Lists {
			c.Lists[k] = slices.Clone(v)
		}
	}
	return &c
}

// Equal says whether a and b hold the same folders, hosts, settings and
// secrets. The order of folders and hosts, the device and the time do not
// count.
func Equal(a, b *Payload) bool {
	return sameItems(a.Folders, b.Folders) && sameItems(a.Hosts, b.Hosts) && sameItems(a.Proxies, b.Proxies) && equalQuick(a.Quick, b.Quick) &&
		maps.EqualFunc(a.Settings, b.Settings, sameJSON) && maps.Equal(a.Secrets, b.Secrets) && equalLists(a.Lists, b.Lists)
}

// equalLists says whether two Lists hold the same lists: nil is no
// lists, an empty list none.
func equalLists(a, b map[string][]json.RawMessage) bool {
	for k, v := range a {
		if len(v) > 0 && !sameItems(v, b[k]) {
			return false
		}
	}
	for k, v := range b {
		if len(v) > 0 && len(a[k]) == 0 {
			return false
		}
	}
	return true
}

// mergeLists merges Lists as Merge merges Proxies: a side without them
// (nil) is taken to have the base's, so that it neither adds nor deletes.
func mergeLists(base, local, remote map[string][]json.RawMessage) (map[string][]json.RawMessage, int) {
	if local == nil && remote == nil {
		return base, 0
	}
	if local == nil {
		local = base
	}
	if remote == nil {
		remote = base
	}
	keys := map[string]bool{}
	for _, m := range []map[string][]json.RawMessage{base, local, remote} {
		for k := range m {
			keys[k] = true
		}
	}
	out, conflicts := NewLists(), 0
	for k := range keys {
		merged, n := mergeItems(base[k], local[k], remote[k])
		conflicts += n
		if len(merged) > 0 {
			out[k] = merged
		}
	}
	return out, conflicts
}

// NewProxies is the proxy library of a build that has one, and none in
// it: the key is written, unlike a build without the library's.
func NewProxies() []json.RawMessage { return []json.RawMessage{} }

// KeyFilePrefix starts the keys of the private key files among the
// secrets: KeyFilePrefix + name + "#" + content hash. Devices may each
// have another key by the same name, so what a device says of them is
// only what changed in its own files (keyFileSecrets).
const KeyFilePrefix = "sshkey/"

// keyFileSecrets is local's secrets with its private key files as what
// this device changed since base: held are the files it had at the last
// sync, those of local the ones it has now. A file it has, and did not
// have, is added; one it had, and has no longer, deleted; of the others
// it says nothing, base's kept as they are, so that a key deleted on
// another device is not added again by one that still has the file, and
// one it never had stays.
func keyFileSecrets(local, base map[string]string, held []string) map[string]string {
	wasHeld := map[string]bool{}
	for _, k := range held {
		wasHeld[k] = true
	}
	out := map[string]string{}
	for k, v := range local {
		if !strings.HasPrefix(k, KeyFilePrefix) || !wasHeld[k] {
			out[k] = v
		}
	}
	for k, v := range base {
		if !strings.HasPrefix(k, KeyFilePrefix) {
			continue
		}
		if _, has := local[k]; has || !wasHeld[k] {
			out[k] = v
		}
	}
	return out
}

// ProxySecretPrefix starts the keys of the library's proxies' passwords
// among the secrets: "proxy/<id>/password".
const ProxySecretPrefix = "proxy/"

// withProxySecrets is secrets with the library's passwords base has, as
// a side that knows no library leaves them, where secrets has none: a
// payload without secrets stays without.
func withProxySecrets(secrets, base map[string]string) map[string]string {
	if secrets == nil {
		return nil
	}
	out := maps.Clone(secrets)
	for k, v := range base {
		if strings.HasPrefix(k, ProxySecretPrefix) {
			if _, ok := out[k]; !ok {
				out[k] = v
			}
		}
	}
	return out
}

func equalQuick(first, second *QuickCommands) bool {
	if first == nil || second == nil {
		return first == second
	}
	return slices.EqualFunc(first.Groups, second.Groups, sameJSON) && slices.EqualFunc(first.Commands, second.Commands, sameJSON)
}

// Merge merges local and remote three ways, against base, the payload
// both last agreed on (nil when there is none). Each folder and host (by
// its ID), each setting and each secret is merged on its own:
//
//   - changed on one side only, that side's is kept;
//   - deleted on one side and unchanged on the other, it is deleted;
//   - changed on both sides, local's is kept: the device syncing later
//     wins;
//   - with no base, nothing is deleted: what either side has is kept,
//     local's when both have it.
//
// It returns how many items changed on both sides differently.
func Merge(base, local, remote *Payload) (merged *Payload, conflicts int) {
	if base == nil {
		base = &Payload{}
	}
	m := &Payload{Schema: PayloadSchema}
	var n int
	m.Folders, n = mergeItems(base.Folders, local.Folders, remote.Folders)
	conflicts += n
	m.Hosts, n = mergeItems(base.Hosts, local.Hosts, remote.Hosts)
	conflicts += n
	// A side a build without the proxy library wrote left it as it was,
	// with its passwords: it neither adds nor deletes a proxy.
	localProxies, remoteProxies := local.Proxies, remote.Proxies
	localSecrets, remoteSecrets := local.Secrets, remote.Secrets
	if localProxies == nil {
		localProxies, localSecrets = base.Proxies, withProxySecrets(local.Secrets, base.Secrets)
	}
	if remoteProxies == nil {
		remoteProxies, remoteSecrets = base.Proxies, withProxySecrets(remote.Secrets, base.Secrets)
	}
	m.Proxies, n = mergeItems(base.Proxies, localProxies, remoteProxies)
	conflicts += n
	if m.Proxies == nil {
		m.Proxies = NewProxies()
	}
	m.Quick, n = mergeQuick(base.Quick, local.Quick, remote.Quick)
	conflicts += n
	m.Settings, n = mergeMap(base.Settings, local.Settings, remote.Settings, sameJSON)
	conflicts += n
	m.Lists, n = mergeLists(base.Lists, local.Lists, remote.Lists)
	conflicts += n
	m.Secrets, n = mergeMap(base.Secrets, localSecrets, remoteSecrets, func(a, b string) bool { return a == b })
	conflicts += n
	if len(m.Secrets) == 0 {
		m.Secrets = nil
	}
	return m, conflicts
}

func mergeQuick(base, local, remote *QuickCommands) (*QuickCommands, int) {
	if local == nil {
		return remote, 0
	}
	if remote == nil {
		return local, 0
	}
	if base == nil {
		base = &QuickCommands{}
	}
	groups, groupConflicts := mergeOrderedItems(base.Groups, local.Groups, remote.Groups)
	commands, commandConflicts := mergeOrderedItems(base.Commands, local.Commands, remote.Commands)
	return &QuickCommands{Groups: groups, Commands: commands}, groupConflicts + commandConflicts
}

func mergeOrderedItems(base, local, remote []json.RawMessage) ([]json.RawMessage, int) {
	commonOrder := func(items []json.RawMessage, other map[string]json.RawMessage) []string {
		var order []string
		for _, item := range items {
			id := idOf(item)
			if hasKey(other, id) {
				order = append(order, id)
			}
		}
		return order
	}
	if !slices.Equal(commonOrder(local, byID(remote)), commonOrder(remote, byID(local))) {
		localReordered := !slices.Equal(commonOrder(local, byID(base)), commonOrder(base, byID(local)))
		remoteReordered := !slices.Equal(commonOrder(remote, byID(base)), commonOrder(base, byID(remote)))
		if remoteReordered && !localReordered {
			merged, conflicts := mergeItems(base, local, remote)
			indices := byID(merged)
			ordered := make([]json.RawMessage, 0, len(merged))
			for _, item := range remote {
				id := idOf(item)
				if value, ok := indices[id]; ok {
					ordered = append(ordered, value)
					delete(indices, id)
				}
			}
			for _, item := range merged {
				id := idOf(item)
				if id == "" || hasKey(indices, id) {
					ordered = append(ordered, item)
				}
			}
			return ordered, conflicts
		}
	}
	return mergeItems(base, local, remote)
}

// pick is the three-way decision for one item: whether to keep it, and
// whether local's is kept (else remote's). inB, inL and inR say where it
// is; the eq functions compare it.
func pick(inB, inL, inR bool, lEqB, rEqB, lEqR func() bool) (keep, useLocal, conflict bool) {
	switch {
	case inL && inR:
		if lEqR() {
			return true, true, false
		}
		if inB && lEqB() {
			return true, false, false
		}
		if inB && rEqB() {
			return true, true, false
		}
		return true, true, inB // with no base, it is a first sync rather than a conflict
	case inL: // deleted on the remote, or new here
		if inB && lEqB() {
			return false, false, false
		}
		return true, true, false
	case inR: // deleted here, or new on the remote
		if inB && rEqB() {
			return false, false, false
		}
		return true, false, false
	}
	return false, false, false
}

func mergeMap[V any](base, local, remote map[string]V, eq func(a, b V) bool) (map[string]V, int) {
	out := map[string]V{}
	conflicts := 0
	keys := map[string]bool{}
	for k := range local {
		keys[k] = true
	}
	for k := range remote {
		keys[k] = true
	}
	for k := range keys {
		b, inB := base[k]
		l, inL := local[k]
		r, inR := remote[k]
		keep, useLocal, conflict := pick(inB, inL, inR,
			func() bool { return eq(l, b) }, func() bool { return eq(r, b) }, func() bool { return eq(l, r) })
		if conflict {
			conflicts++
		}
		if !keep {
			continue
		}
		if useLocal {
			out[k] = l
		} else {
			out[k] = r
		}
	}
	return out, conflicts
}

// mergeItems merges lists of objects by their "id": the result has
// local's order, then what only the remote has, in its order. Objects
// without an ID are kept from local and dropped from the remote.
func mergeItems(base, local, remote []json.RawMessage) ([]json.RawMessage, int) {
	b, r := byID(base), byID(remote)
	l := byID(local)
	out := []json.RawMessage{}
	conflicts := 0
	decide := func(id string) {
		bi, inB := b[id]
		li, inL := l[id]
		ri, inR := r[id]
		keep, useLocal, conflict := pick(inB, inL, inR,
			func() bool { return sameJSON(li, bi) }, func() bool { return sameJSON(ri, bi) }, func() bool { return sameJSON(li, ri) })
		if conflict {
			conflicts++
		}
		if !keep {
			return
		}
		if useLocal {
			out = append(out, li)
		} else {
			out = append(out, ri)
		}
	}
	for _, item := range local {
		id := idOf(item)
		if id == "" {
			out = append(out, item)
			continue
		}
		decide(id)
	}
	for _, item := range remote {
		if id := idOf(item); id != "" && !hasKey(l, id) {
			decide(id)
		}
	}
	return out, conflicts
}

func hasKey(m map[string]json.RawMessage, k string) bool { _, ok := m[k]; return ok }

func idOf(item json.RawMessage) string {
	var v struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(item, &v) != nil {
		return ""
	}
	return v.ID
}

func byID(items []json.RawMessage) map[string]json.RawMessage {
	m := make(map[string]json.RawMessage, len(items))
	for _, it := range items {
		if id := idOf(it); id != "" {
			if _, dup := m[id]; !dup {
				m[id] = it
			}
		}
	}
	return m
}

// sameItems says whether two lists hold the same objects, in any order.
func sameItems(a, b []json.RawMessage) bool {
	if len(a) != len(b) {
		return false
	}
	ca, cb := canonicalAll(a), canonicalAll(b)
	slices.Sort(ca)
	slices.Sort(cb)
	return slices.Equal(ca, cb)
}

func canonicalAll(items []json.RawMessage) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = string(canonical(it))
	}
	return out
}

// sameJSON says whether a and b are the same JSON value, whatever the
// order of their keys and their spacing.
func sameJSON(a, b json.RawMessage) bool { return bytes.Equal(canonical(a), canonical(b)) }

// canonical is v re-encoded with sorted keys and no spacing.
func canonical(v json.RawMessage) []byte {
	var x any
	d := json.NewDecoder(bytes.NewReader(v))
	d.UseNumber()
	if d.Decode(&x) != nil {
		return v
	}
	out, err := json.Marshal(x)
	if err != nil {
		return v
	}
	return out
}
