package cloudsync

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"
)

type itemInfo struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Addr      string    `json:"addr"`
	Folder    string    `json:"folder"`
	Parent    string    `json:"parent"`
	DeletedAt time.Time `json:"deletedAt"`
}

func itemInfoOf(raw json.RawMessage) itemInfo {
	var info itemInfo
	json.Unmarshal(raw, &info)
	return info
}

type DeleteImpact struct {
	LiveRemoved, LiveTotal           int
	PermanentRemoved, PermanentTotal int
	FoldersRemoved, LargestSubtree   int
}

func (d DeleteImpact) Risky() bool {
	return d.LiveRemoved > max(10, d.LiveTotal/5) ||
		d.PermanentRemoved > max(10, d.PermanentTotal/5) || d.LargestSubtree > 10
}

type DeletionGuard struct {
	Server, Local DeleteImpact
	Approval      string
	ServerVersion string
}

func (*DeletionGuard) Error() string { return "cloudsync: paused for mass deletion confirmation" }

func Deletions(before, after *Payload) DeleteImpact {
	if before == nil || after == nil {
		return DeleteImpact{}
	}
	var impact DeleteImpact
	remainingHosts := byID(after.Hosts)
	beforeFolders, afterFolders := byID(before.Folders), byID(after.Folders)
	parents := make(map[string]string, len(beforeFolders))
	removedFolders := map[string]int{}
	for id, raw := range beforeFolders {
		parents[id] = itemInfoOf(raw).Parent
		if _, exists := afterFolders[id]; !exists {
			removedFolders[id] = 0
		}
	}
	impact.FoldersRemoved = len(removedFolders)
	for id, raw := range byID(before.Hosts) {
		info := itemInfoOf(raw)
		afterRaw, exists := remainingHosts[id]
		if !info.DeletedAt.IsZero() {
			impact.PermanentTotal++
			if !exists {
				impact.PermanentRemoved++
			}
			continue
		}
		impact.LiveTotal++
		if !exists || !itemInfoOf(afterRaw).DeletedAt.IsZero() {
			impact.LiveRemoved++
		}
		if len(removedFolders) != 0 {
			seen := map[string]bool{}
			for folder := info.Folder; folder != "" && !seen[folder]; folder = parents[folder] {
				seen[folder] = true
				if size, removed := removedFolders[folder]; removed {
					removedFolders[folder] = size + 1
					impact.LargestSubtree = max(impact.LargestSubtree, size+1)
				}
			}
		}
	}
	return impact
}

func guardDeletion(req Request, local, remote, merged *Payload, raw []byte) error {
	guard := &DeletionGuard{Server: Deletions(remote, merged), Local: Deletions(local, merged)}
	if req.Force && remote == nil && len(raw) != 0 {
		previous := readSnapshotPrefix(raw)
		if previous.Counted {
			current := merged.Counts()
			guard.Server.LiveTotal, guard.Server.PermanentTotal = previous.Counts.Live, previous.Counts.Deleted
			guard.Server.LiveRemoved = max(0, previous.Counts.Live-current.Live)
			guard.Server.PermanentRemoved = max(0, previous.Counts.Deleted-current.Deleted)
			guard.Server.FoldersRemoved = max(0, previous.Counts.Folders-current.Folders)
		}
	}
	if req.AllowDeletion || (!guard.Server.Risky() && !guard.Local.Risky()) {
		return nil
	}
	encoded, _ := json.Marshal([]*Payload{local, merged})
	digest := sha256.New()
	digest.Write(raw)
	digest.Write(encoded)
	guard.Approval = hex.EncodeToString(digest.Sum(nil))
	guard.ServerVersion = serverVersion(raw)
	if guard.Approval == req.Approval {
		return nil
	}
	return guard
}

func serverVersion(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
