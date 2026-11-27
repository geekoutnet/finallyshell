package app

// The rest of what syncs, beyond the connections, quick commands and
// settings (sync.go): the Database tab's connections, the trusted host
// keys and each host's command history, as the payload's lists
// (cloudsync.Payload.Lists); and, with the saved passwords, the AI
// assistant's API key, the database, tunnel and Remote Desktop gateway
// passwords, and the SSH private keys of the hosts that log in with one.
//
// A private key travels by its path under ~/.ssh, the same on every
// device: a host's key path is synced as "~/.ssh/<name>", which the
// connection expands to each device's home. A device says only what
// changed in its own key files (cloudsync.KeyFilePrefix): one it adds is
// added, one it deletes or replaces is deleted everywhere. A key file
// that is there is never replaced nor deleted by a sync.
//
// Nothing here is only ever added: what is deleted on one device is
// deleted on the others, as with the connections.

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"finallyshell/internal/cloudsync"
	"finallyshell/internal/config"
	"finallyshell/internal/dbclient"
	"finallyshell/internal/sshx"
)

// historySyncLimit is how many of a host's latest commands sync.
const historySyncLimit = 500

// sshKeySecretPrefix starts the payload's key of a private key file:
// sshKeySecretPrefix + its path under ~/.ssh, with forward slashes, "#"
// and the start of its content's SHA-256, so that devices with another
// key by the same name each keep theirs (cloudsync.KeyFilePrefix).
const sshKeySecretPrefix = cloudsync.KeyFilePrefix

// keyFilesOf are the keys of the private key files among a sync's
// secrets, sorted: the files this device had (SyncState.KeyFiles).
func keyFilesOf(secrets map[string]string) []string {
	var keys []string
	for k := range secrets {
		if strings.HasPrefix(k, sshKeySecretPrefix) {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	return keys
}

// sshKeySecret is the payload's key of the private key file name, with
// content data.
func sshKeySecret(name string, data []byte) string {
	sum := sha256.Sum256(data)
	return sshKeySecretPrefix + name + "#" + hex.EncodeToString(sum[:8])
}

// sshKeyOf is the name and content hash of a private key's payload key,
// ok false for another key.
func sshKeyOf(key string) (name, sum string, ok bool) {
	rest, found := strings.CutPrefix(key, sshKeySecretPrefix)
	if !found {
		return "", "", false
	}
	name, sum, found = strings.Cut(rest, "#")
	return name, sum, found && name != "" && sum != ""
}

// sshDir is ~/.ssh, "" when there is no home.
func sshDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".ssh")
}

// sshKeyName is a key path's name under ~/.ssh, with forward slashes: ""
// for a path elsewhere.
func sshKeyName(path string) string {
	dir := sshDir()
	if path == "" || dir == "" {
		return ""
	}
	full := filepath.Clean(sshx.ExpandHome(path))
	rel, err := filepath.Rel(dir, full)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return ""
	}
	return filepath.ToSlash(rel)
}

// portableKeyPath is a key path as it syncs: "~/.ssh/<name>" for one
// under ~/.ssh, the same on every device; others as they are.
func portableKeyPath(path string) string {
	if name := sshKeyName(path); name != "" {
		return "~/.ssh/" + name
	}
	return path
}

// sshKeyPath is the file of a payload's private key name, "" for a name
// that would leave ~/.ssh.
func sshKeyPath(name string) string {
	dir := sshDir()
	clean := filepath.Clean(filepath.FromSlash(name))
	if dir == "" || name == "" || clean == "." || strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) {
		return ""
	}
	return filepath.Join(dir, clean)
}

// knownHostsPath is the app's known_hosts.
func knownHostsPath() string { return filepath.Join(config.Dir(), "known_hosts") }

// dbProfilesNow is the Database tab's connections, as the app has them:
// an error when db.json does not read.
func (a *App) dbProfilesRead() (dbProfiles, error) {
	if a.db.loaded {
		return a.db.profiles, a.db.loadErr
	}
	return loadDBProfiles(a.dbConfigPath())
}

// dbProfilesNow is dbProfilesRead without its error.
func (a *App) dbProfilesNow() dbProfiles {
	profiles, _ := a.dbProfilesRead()
	return profiles
}

// syncLists are this device's lists of the payload: nil when one of them
// does not read, so that the sync neither adds nor deletes any of them
// rather than taking an unreadable file for none (as an older build's
// payload, cloudsync.Payload.Lists).
func (a *App) syncLists() map[string][]json.RawMessage {
	lists := cloudsync.NewLists()
	item := func(v any) json.RawMessage {
		data, _ := json.Marshal(v)
		return data
	}
	// The databases, by host and connection.
	profiles, err := a.dbProfilesRead()
	if err != nil {
		return nil
	}
	hosts := make([]string, 0, len(profiles))
	for host := range profiles {
		hosts = append(hosts, host)
	}
	slices.Sort(hosts)
	for _, host := range hosts {
		for _, p := range profiles[host] {
			if p.ID == "" {
				continue
			}
			lists[cloudsync.ListDatabases] = append(lists[cloudsync.ListDatabases],
				item(map[string]any{"id": host + "/" + p.ID, "host": host, "profile": p}))
		}
	}
	// The trusted host keys, a line each.
	data, err := os.ReadFile(knownHostsPath())
	if err != nil && !os.IsNotExist(err) {
		return nil
	}
	if err == nil {
		seen := map[string]bool{}
		sc := bufio.NewScanner(bytes.NewReader(data))
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" || strings.HasPrefix(line, "#") || seen[line] {
				continue
			}
			seen[line] = true
			lists[cloudsync.ListKnownHosts] = append(lists[cloudsync.ListKnownHosts], item(map[string]string{"id": line}))
		}
	}
	// Each host's latest commands, and the local terminals'.
	ids := []string{localHistoryID}
	for _, h := range a.hosts.Hosts {
		if !h.Deleted() {
			ids = append(ids, h.ID)
		}
	}
	for _, id := range ids {
		list, err := config.LoadHistory(id)
		if err != nil {
			return nil
		}
		if len(list) == 0 {
			continue
		}
		list = list[max(0, len(list)-historySyncLimit):]
		seen := map[string]bool{}
		var items []json.RawMessage
		for _, cmd := range list {
			sum := sha256.Sum256([]byte(cmd))
			key := hex.EncodeToString(sum[:12])
			if seen[key] {
				continue
			}
			seen[key] = true
			items = append(items, item(map[string]string{"id": key, "cmd": cmd}))
		}
		lists[cloudsync.ListHistoryPrefix+id] = items
	}
	return lists
}

// applySyncLists makes this device's databases and command histories
// those of lists, and changes its trusted host keys as lists changed them
// from before, this device's when the sync started: a key deleted on
// another device is deleted here, and one trusted here during the sync
// stays. nil lists (an older build's payload) leave them all.
func (a *App) applySyncLists(lists, before map[string][]json.RawMessage) error {
	if lists == nil {
		return nil
	}
	var errs []error
	// The databases.
	next := dbProfiles{}
	for _, raw := range lists[cloudsync.ListDatabases] {
		var v struct {
			Host    string           `json:"host"`
			Profile dbclient.Profile `json:"profile"`
		}
		if json.Unmarshal(raw, &v) == nil && v.Host != "" && v.Profile.ID != "" {
			next[v.Host] = append(next[v.Host], v.Profile)
		}
	}
	if cur, err := a.dbProfilesRead(); err == nil && !sameJSONValue(cur, next) && (len(cur) > 0 || len(next) > 0) {
		if err := saveDBProfiles(a.dbConfigPath(), next); err != nil {
			errs = append(errs, err)
		} else if a.db.loaded {
			a.db.profiles = next
		}
	}
	// The trusted host keys.
	if err := applyKnownHosts(knownHostLines(lists[cloudsync.ListKnownHosts]), knownHostLines(before[cloudsync.ListKnownHosts])); err != nil {
		errs = append(errs, err)
	}
	// The command histories: the synced latest commands, after the older
	// ones only this device keeps.
	for name, items := range lists {
		id, ok := strings.CutPrefix(name, cloudsync.ListHistoryPrefix)
		if !ok || id == "" {
			continue
		}
		var synced []string
		for _, raw := range items {
			var v struct {
				Cmd string `json:"cmd"`
			}
			if json.Unmarshal(raw, &v) == nil && v.Cmd != "" {
				synced = append(synced, v.Cmd)
			}
		}
		cur, _ := config.LoadHistory(id)
		kept := cur[:max(0, len(cur)-historySyncLimit)]
		in := map[string]bool{}
		for _, c := range synced {
			in[c] = true
		}
		var next []string
		for _, c := range kept {
			if !in[c] {
				next = append(next, c)
			}
		}
		next = append(next, synced...)
		if !slices.Equal(cur, next) {
			if err := config.SetHistory(id, next); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

// knownHostLines are the lines of a list of trusted host keys.
func knownHostLines(items []json.RawMessage) map[string]bool {
	lines := map[string]bool{}
	for _, raw := range items {
		var v struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(raw, &v) == nil && v.ID != "" && !strings.ContainsAny(v.ID, "\r\n") {
			lines[v.ID] = true
		}
	}
	return lines
}

// applyKnownHosts changes known_hosts as the sync changed its lines from
// before to after: those it added are added, those it deleted deleted.
// The others, comments and the lines trusted since the sync started,
// stay as they are.
func applyKnownHosts(after, before map[string]bool) error {
	return sshx.UpdateKnownHosts(knownHostsPath(), func(data []byte) ([]byte, bool) {
		return knownHostsChanged(data, after, before)
	})
}

// knownHostsChanged is applyKnownHosts' change of known_hosts' content
// data, and whether there is one.
func knownHostsChanged(data []byte, after, before map[string]bool) ([]byte, bool) {
	var out []string
	have, changed := map[string]bool{}, false
	for _, line := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if before[trimmed] && !after[trimmed] {
			changed = true
			continue
		}
		have[trimmed] = true
		out = append(out, line)
	}
	added := make([]string, 0)
	for line := range after {
		if !before[line] && !have[line] {
			added = append(added, line)
		}
	}
	slices.Sort(added)
	if len(added) == 0 && !changed {
		return nil, false
	}
	out = append(out, added...)
	return []byte(strings.Join(out, "\n") + "\n"), true
}

// sameJSONValue says whether a and b marshal the same.
func sameJSONValue(a, b any) bool {
	x, err1 := json.Marshal(a)
	y, err2 := json.Marshal(b)
	return err1 == nil && err2 == nil && bytes.Equal(x, y)
}

// writeFileAtomic writes data to path through a file beside it.
func writeFileAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// extraSecretKeys are the keys of the secrets that sync besides the
// hosts' and the proxies': the AI assistant's API key, the databases',
// tunnels' and Remote Desktop gateways' passwords, and the hosts'
// private key files under ~/.ssh.
func (a *App) extraSecretKeys() []string {
	var keys []string
	if ai := a.settings.AI; ai != nil && ai.Endpoint != "" {
		keys = append(keys, desktopAISecret(ai.Endpoint))
	}
	for host, list := range a.dbProfilesNow() {
		for _, p := range list {
			if p.ID != "" {
				keys = append(keys, dbSecretKey(host, p.ID))
			}
		}
	}
	seen := map[string]bool{}
	for _, h := range a.hosts.Hosts {
		if h.Deleted() {
			continue
		}
		for _, t := range h.Tunnels {
			for _, upstream := range []bool{false, true} {
				if k := tunnelSecretKey(h.ID, t.ID, upstream); k != "" {
					keys = append(keys, k)
				}
			}
		}
		if h.IsRDP() {
			keys = append(keys, gatewaySecretKey(h.ID))
		}
		if name := sshKeyName(h.KeyPath); name != "" && !seen[name] {
			seen[name] = true
			if data, err := os.ReadFile(sshKeyPath(name)); err == nil && len(data) > 0 && len(data) < 64<<10 {
				keys = append(keys, sshKeySecret(name, data))
			}
		}
	}
	return keys
}

// syncKeyAllowed says whether a sync may keep the secret of key here:
// one of syncSecretKeys, or the private key of a host's key path, which
// this device may lack.
func (a *App) syncKeyAllowed(allowed map[string]bool, key string) bool {
	if allowed[key] {
		return true
	}
	name, _, ok := sshKeyOf(key)
	if !ok {
		return false
	}
	for _, h := range a.hosts.Hosts {
		if !h.Deleted() && sshKeyName(h.KeyPath) == name {
			return true
		}
	}
	return false
}

// fileSecret is the private key file a secret's key names, "" for a
// secret of the credential store, "-" for a key never read nor written
// (a name that would leave ~/.ssh).
func fileSecret(key string) string {
	if !strings.HasPrefix(key, sshKeySecretPrefix) {
		return ""
	}
	name, _, ok := sshKeyOf(key)
	if !ok {
		return "-"
	}
	if p := sshKeyPath(name); p != "" {
		return p
	}
	return "-"
}

// secretGet reads a secret for a sync: a private key from its file, the
// others from the credential store.
func (a *App) secretGet(key string) (string, bool, error) {
	path := fileSecret(key)
	if path == "" {
		return a.plat.SecretGet(key)
	}
	if path == "-" {
		return "", false, nil
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if sshKeySecret(strings.Split(strings.TrimPrefix(key, sshKeySecretPrefix), "#")[0], data) != key {
		return "", false, nil // another key by that name: this device's own
	}
	return string(data), true, nil
}

// secretSet keeps a secret a sync brought: a private key in its file,
// never over one that is there, the others in the credential store. A
// private key is never deleted.
func (a *App) secretSet(key, value string, del bool) error {
	path := fileSecret(key)
	switch {
	case path == "":
		if del {
			return a.plat.SecretDelete(key)
		}
		return a.plat.SecretSet(key, value)
	case path == "-" || del:
		return nil
	}
	err := sshx.InstallPrivateKey(path, []byte(value))
	if errors.Is(err, os.ErrExist) {
		return nil // this device's own key stays
	}
	return err
}
