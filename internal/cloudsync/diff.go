package cloudsync

import (
	"encoding/json"
	"slices"
	"strings"
)

type ChangeNames struct {
	Added, Removed, Changed []string
}

type Changes struct {
	Hosts, Folders, Commands, Groups ChangeNames
	// Settings are the settings, by key; Proxies the proxy library;
	// Databases the Database tab's connections; KnownHosts the trusted
	// host keys, by host; History the hosts with a command history, by
	// host ID. Each is compared only when after has it.
	Settings, Proxies, Databases, KnownHosts, History ChangeNames
}

// Total is how many items changes adds, removes and changes.
func (c Changes) Total() (added, removed, changed int) {
	for _, n := range []ChangeNames{c.Hosts, c.Folders, c.Commands, c.Groups, c.Settings, c.Proxies, c.Databases, c.KnownHosts, c.History} {
		added += len(n.Added)
		removed += len(n.Removed)
		changed += len(n.Changed)
	}
	return added, removed, changed
}

func Compare(before, after *Payload) Changes {
	if before == nil {
		before = &Payload{}
	}
	if after == nil {
		after = &Payload{}
	}
	changes := Changes{Hosts: diffItems(before.Hosts, after.Hosts, true), Folders: diffItems(before.Folders, after.Folders, false)}
	if after.Quick != nil {
		old := before.Quick
		if old == nil {
			old = &QuickCommands{}
		}
		changes.Commands = diffItems(old.Commands, after.Quick.Commands, false)
		changes.Groups = diffItems(old.Groups, after.Quick.Groups, false)
	}
	if after.Settings != nil {
		changes.Settings = diffMap(before.Settings, after.Settings)
	}
	if after.Proxies != nil {
		changes.Proxies = diffItems(before.Proxies, after.Proxies, false)
	}
	if after.Lists != nil {
		changes.Databases = diffItems(databaseItems(before.Lists[ListDatabases]), databaseItems(after.Lists[ListDatabases]), false)
		changes.KnownHosts = diffKnownHosts(before.Lists[ListKnownHosts], after.Lists[ListKnownHosts])
		changes.History = diffHistories(before.Lists, after.Lists)
	}
	return changes
}

// diffMap is what changed between two maps of JSON, by key.
func diffMap(before, after map[string]json.RawMessage) ChangeNames {
	var changes ChangeNames
	for k, v := range before {
		if w, ok := after[k]; !ok {
			changes.Removed = append(changes.Removed, k)
		} else if !sameJSON(v, w) {
			changes.Changed = append(changes.Changed, k)
		}
	}
	for k := range after {
		if _, ok := before[k]; !ok {
			changes.Added = append(changes.Added, k)
		}
	}
	slices.Sort(changes.Added)
	slices.Sort(changes.Removed)
	slices.Sort(changes.Changed)
	return changes
}

// databaseItems are the databases with their connection's name as the
// item's, for diffItems to name them.
func databaseItems(items []json.RawMessage) []json.RawMessage {
	out := make([]json.RawMessage, 0, len(items))
	for _, raw := range items {
		var v struct {
			ID      string `json:"id"`
			Profile struct {
				Name string `json:"name"`
				Host string `json:"host"`
			} `json:"profile"`
		}
		json.Unmarshal(raw, &v)
		named, _ := json.Marshal(map[string]any{"id": v.ID, "name": v.Profile.Name, "addr": v.Profile.Host, "item": raw})
		out = append(out, named)
	}
	return out
}

// diffKnownHosts is what changed among the trusted host keys, by the
// hosts a line names: a line is its own ID, so changed ones are a line
// removed and another added.
func diffKnownHosts(before, after []json.RawMessage) ChangeNames {
	name := func(raw json.RawMessage) string {
		host, _, _ := strings.Cut(idOf(raw), " ")
		return host
	}
	var changes ChangeNames
	b, a := byID(before), byID(after)
	for id, raw := range b {
		if !hasKey(a, id) {
			changes.Removed = append(changes.Removed, name(raw))
		}
	}
	for id, raw := range a {
		if !hasKey(b, id) {
			changes.Added = append(changes.Added, name(raw))
		}
	}
	slices.Sort(changes.Added)
	slices.Sort(changes.Removed)
	return changes
}

// diffHistories is which hosts' command histories differ.
func diffHistories(before, after map[string][]json.RawMessage) ChangeNames {
	var changes ChangeNames
	for k, v := range before {
		id, ok := strings.CutPrefix(k, ListHistoryPrefix)
		if !ok || len(v) == 0 {
			continue
		}
		if w := after[k]; len(w) == 0 {
			changes.Removed = append(changes.Removed, id)
		} else if !sameItems(v, w) {
			changes.Changed = append(changes.Changed, id)
		}
	}
	for k, v := range after {
		id, ok := strings.CutPrefix(k, ListHistoryPrefix)
		if ok && len(v) > 0 && len(before[k]) == 0 {
			changes.Added = append(changes.Added, id)
		}
	}
	slices.Sort(changes.Added)
	slices.Sort(changes.Removed)
	slices.Sort(changes.Changed)
	return changes
}

func diffItems(before, after []json.RawMessage, live bool) ChangeNames {
	first, second := byID(before), byID(after)
	var changes ChangeNames
	for id, raw := range first {
		old := itemInfoOf(raw)
		value, exists := second[id]
		next := itemInfoOf(value)
		switch {
		case !exists || live && old.DeletedAt.IsZero() && !next.DeletedAt.IsZero():
			changes.Removed = append(changes.Removed, itemLabel(old))
		case live && !old.DeletedAt.IsZero() && next.DeletedAt.IsZero():
			changes.Added = append(changes.Added, itemLabel(next))
		case !sameJSON(raw, value):
			changes.Changed = append(changes.Changed, itemLabel(next))
		}
	}
	for id, raw := range second {
		if _, exists := first[id]; !exists {
			changes.Added = append(changes.Added, itemLabel(itemInfoOf(raw)))
		}
	}
	slices.Sort(changes.Added)
	slices.Sort(changes.Removed)
	slices.Sort(changes.Changed)
	return changes
}

func itemLabel(item itemInfo) string {
	if item.Name != "" {
		return item.Name
	}
	if item.Addr != "" {
		return item.Addr
	}
	return item.ID
}

func AddMissing(local, backup *Payload, deleted map[string]bool) *Payload {
	out := local.Clone()
	out.Folders = addMissingItems(out.Folders, backup.Folders, false)
	hosts := backup.Hosts
	if len(deleted) != 0 {
		hosts = slices.DeleteFunc(slices.Clone(hosts), func(raw json.RawMessage) bool { return deleted[idOf(raw)] })
	}
	out.Hosts = addMissingItems(out.Hosts, hosts, true)
	if backup.Proxies != nil {
		out.Proxies = addMissingItems(out.Proxies, backup.Proxies, false)
	}
	if backup.Quick != nil {
		if out.Quick == nil {
			out.Quick = &QuickCommands{}
		}
		out.Quick.Groups = addMissingItems(out.Quick.Groups, backup.Quick.Groups, false)
		out.Quick.Commands = addMissingItems(out.Quick.Commands, backup.Quick.Commands, false)
	}
	if backup.Lists != nil && out.Lists != nil {
		for k, v := range backup.Lists {
			out.Lists[k] = addMissingItems(out.Lists[k], v, false)
		}
	}
	return out
}

func addMissingItems(existing, incoming []json.RawMessage, liveOnly bool) []json.RawMessage {
	kept := byID(existing)
	for _, raw := range incoming {
		item := itemInfoOf(raw)
		if item.ID == "" || (liveOnly && !item.DeletedAt.IsZero()) || hasKey(kept, item.ID) {
			continue
		}
		kept[item.ID] = raw
		existing = append(existing, raw)
	}
	return existing
}
