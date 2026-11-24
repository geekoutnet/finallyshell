package cloudsync

import (
	"encoding/json"
	"slices"
)

type ChangeNames struct {
	Added, Removed, Changed []string
}

type Changes struct {
	Hosts, Folders, Commands, Groups ChangeNames
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
	if backup.Quick != nil {
		if out.Quick == nil {
			out.Quick = &QuickCommands{}
		}
		out.Quick.Groups = addMissingItems(out.Quick.Groups, backup.Quick.Groups, false)
		out.Quick.Commands = addMissingItems(out.Quick.Commands, backup.Quick.Commands, false)
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
