package cloudsync

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
)

const HistoryFolder = "finallyshell-sync-history"

const (
	ReasonAuto       = "auto"
	ReasonManual     = "manual"
	ReasonRestore    = "restore"
	ReasonPreRestore = "pre-restore"
	ReasonPreSync    = "pre-sync"
	ReasonBulkDelete = "bulk-delete"
	ReasonImport     = "import"
)

var ErrHistoryUnsupported = errors.New("cloudsync: this provider manages its own revision history")

type Counts struct {
	Live     int `json:"live"`
	Deleted  int `json:"deleted"`
	Folders  int `json:"folders"`
	Commands int `json:"commands"`
}

type Snapshot struct {
	ID         string    `json:"-"`
	At         time.Time `json:"at"`
	Device     string    `json:"device,omitempty"`
	AppVersion string    `json:"appVersion,omitempty"`
	Reason     string    `json:"reason,omitempty"`
	Note       string    `json:"-"`
	Counts     Counts    `json:"counts"`
	Counted    bool      `json:"counted,omitempty"`
	Size       int64     `json:"-"`
	Pinned     bool      `json:"-"`
}

type History interface {
	List(context.Context) ([]Snapshot, error)
	GetVersion(context.Context, string) ([]byte, error)
	PutVersion(context.Context, string, []byte) error
	DeleteVersion(context.Context, string) error
	PinVersion(context.Context, string, bool) error
}

type NativeHistory interface {
	NativeRevisions() bool
}

func UsesNativeRevisions(remote Remote) bool {
	native, ok := remote.(NativeHistory)
	return ok && native.NativeRevisions()
}

const snapshotTime = "20060102T150405Z"

var (
	snapshotPattern = regexp.MustCompile(`^[0-9]{8}T[0-9]{6}Z-(?:[A-Za-z0-9_][A-Za-z0-9_-]{0,39})-[0-9a-f]{8}\.json$`)
	unsafeDevice    = regexp.MustCompile(`[^A-Za-z0-9_-]+`)
)

func ValidSnapshotID(id string) bool {
	return snapshotPattern.MatchString(id)
}

func snapshotID(at time.Time, device string, data []byte) string {
	safe := strings.Trim(unsafeDevice.ReplaceAllString(device, "_"), "-_")
	if safe == "" {
		safe = "device"
	}
	safe = safe[:min(len(safe), 40)]
	digest := sha256.Sum256(data)
	return fmt.Sprintf("%s-%s-%x.json", at.UTC().Format(snapshotTime), safe, digest[:4])
}

func snapshotFromID(id string, size int64) (Snapshot, bool) {
	if !ValidSnapshotID(id) {
		return Snapshot{}, false
	}
	at, err := time.Parse(snapshotTime, id[:16])
	if err != nil {
		return Snapshot{}, false
	}
	return Snapshot{ID: id, At: at, Device: id[17 : len(id)-14], Size: size}, true
}

func attachSnapshot(data []byte, meta Snapshot) ([]byte, error) {
	if len(data) < 2 || data[0] != '{' {
		return nil, ErrNotEnvelope
	}
	meta.At = meta.At.UTC()
	encoded, err := json.Marshal(meta)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, len(data)+len(encoded)+14)
	out = append(out, `{"snapshot":`...)
	out = append(out, encoded...)
	out = append(out, ',', '\n')
	return append(out, data[1:]...), nil
}

func replaceSnapshotHeader(data []byte, meta Snapshot) ([]byte, error) {
	if old := readSnapshotPrefix(data); !old.At.IsZero() {
		prefix := []byte(`{"snapshot":`)
		decoder := json.NewDecoder(bytes.NewReader(data[len(prefix):]))
		var header json.RawMessage
		if err := decoder.Decode(&header); err != nil {
			return nil, err
		}
		suffix := bytes.TrimLeft(data[len(prefix)+int(decoder.InputOffset()):], " \r\n\t")
		if len(suffix) == 0 || suffix[0] != ',' {
			return nil, ErrNotEnvelope
		}
		data = append([]byte{'{'}, suffix[1:]...)
	}
	return attachSnapshot(data, meta)
}

func decodeSnapshot(data []byte) (Snapshot, error) {
	var header struct {
		Snapshot Snapshot `json:"snapshot"`
	}
	err := json.Unmarshal(data, &header)
	return header.Snapshot, err
}

func (p *Payload) Counts() Counts {
	counts := Counts{Folders: len(p.Folders)}
	if p.Quick != nil {
		counts.Commands = len(p.Quick.Commands)
	}
	for _, host := range p.Hosts {
		if itemInfoOf(host).DeletedAt.IsZero() {
			counts.Live++
		} else {
			counts.Deleted++
		}
	}
	return counts
}

func readSnapshotPrefix(data []byte) Snapshot {
	prefix := []byte(`{"snapshot":`)
	if !bytes.HasPrefix(data, prefix) {
		return Snapshot{}
	}
	var meta Snapshot
	if json.NewDecoder(bytes.NewReader(data[len(prefix):])).Decode(&meta) != nil {
		return Snapshot{}
	}
	return meta
}

func (s Snapshot) withHeader(header Snapshot) Snapshot {
	if s.Size == 0 {
		s.Size = header.Size
	}
	if !header.At.IsZero() {
		header.ID, header.Size, header.Pinned = s.ID, s.Size, s.Pinned
		return header
	}
	return s
}

type Retention struct {
	AllDays, DailyDays, WeeklyWeeks, Max int
}

func (r Retention) Defaults() Retention {
	if r.AllDays <= 0 {
		r.AllDays = 7
	}
	if r.DailyDays <= 0 {
		r.DailyDays = 30
	}
	if r.WeeklyWeeks <= 0 {
		r.WeeklyWeeks = 26
	}
	if r.Max <= 0 {
		r.Max = 200
	}
	r.WeeklyWeeks = min(r.WeeklyWeeks, 520)
	r.DailyDays = min(r.DailyDays, r.WeeklyWeeks*7)
	r.AllDays = min(r.AllDays, r.DailyDays)
	r.Max = max(r.Max, 10)
	return r
}

func Prunable(versions []Snapshot, now time.Time, retention Retention) []Snapshot {
	r := retention.Defaults()
	now = now.UTC()
	ordered := slices.Clone(versions)
	SortSnapshots(ordered)
	buckets := map[string]bool{}
	kept := 0
	var remove []Snapshot
	for index, entry := range ordered {
		at := entry.At.UTC()
		var key string
		switch {
		case !at.Before(now.AddDate(0, 0, -r.AllDays)):
		case !at.Before(now.AddDate(0, 0, -r.DailyDays)):
			key = at.Format("2006-01-02")
		case !at.Before(now.AddDate(0, 0, -7*r.WeeklyWeeks)):
			year, week := at.ISOWeek()
			key = fmt.Sprintf("%d-W%02d", year, week)
		default:
			key = at.Format("2006-01")
		}
		if index < 10 || entry.Pinned || key == "" || (!buckets[key] && kept < r.Max) {
			if !entry.Pinned {
				kept++
			}
			buckets[key] = true
		} else {
			remove = append(remove, entry)
		}
	}
	return remove
}

func SortSnapshots(versions []Snapshot) {
	slices.SortFunc(versions, func(first, second Snapshot) int {
		if order := second.At.Compare(first.At); order != 0 {
			return order
		}
		return strings.Compare(second.ID, first.ID)
	})
}

type basicHistory interface {
	listHistory(context.Context, bool) ([]Snapshot, error)
}

func pruneHistory(ctx context.Context, remote Remote, now time.Time, retention Retention, protect ...string) error {
	var versions []Snapshot
	var err error
	if fast, ok := remote.(basicHistory); ok {
		versions, err = fast.listHistory(ctx, false)
	} else {
		versions, err = remote.List(ctx)
	}
	if err != nil {
		return err
	}
	for index := range versions {
		if slices.Contains(protect, versions[index].ID) {
			versions[index].Pinned = true
		}
	}
	for _, entry := range Prunable(versions, now, retention) {
		if err := remote.DeleteVersion(ctx, entry.ID); err != nil && !errors.Is(err, ErrNotFound) && !errors.Is(err, ErrPinned) {
			return err
		}
	}
	return nil
}

var ErrPinned = errors.New("cloudsync: unpin this snapshot before deleting it")

func OpenSnapshot(password string, data []byte) (*Payload, error) {
	return openPayload(password, data)
}

func DescribeRevision(entry Snapshot, data []byte) Snapshot {
	entry.Size = int64(len(data))
	return entry.withHeader(readSnapshotPrefix(data))
}
