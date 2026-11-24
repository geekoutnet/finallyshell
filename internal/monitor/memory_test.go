package monitor

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestMemoryParse(t *testing.T) {
	const full = `MemTotal: 1000 kB
MemFree: 100 kB
MemAvailable: 350 kB
Buffers: 20 kB
Cached: 240 kB
SReclaimable: 30 kB
Shmem: 40 kB
Active(file): 41 kB
Inactive(file): 42 kB
Active(anon): 43 kB
Inactive(anon): 44 kB
Dirty: 45 kB
Writeback: 46 kB
Slab: 47 kB
CommitLimit: 1200 kB
Committed_AS: 1300 kB
VmallocUsed: 48 kB
HugePages_Total: 9
HugePages_Free: 8
HugePages_Rsvd: 7
HugePages_Surp: 6
Hugepagesize: 2048 kB
SwapTotal: 500 kB
SwapFree: 300 kB
SwapCached: 60 kB
pswpin 155
pswpout 195
swappiness 60
compact_memory 1
`
	for _, tc := range []struct {
		name, input string
		values      map[string]uint64
		missing     []string
		physical    [3]uint64
		swap        [3]uint64
	}{
		{"linux", full, map[string]uint64{
			"MemTotal": 1000 << 10, "MemFree": 100 << 10, "MemAvailable": 350 << 10,
			"Buffers": 20 << 10, "Cached": 240 << 10, "SReclaimable": 30 << 10, "Shmem": 40 << 10,
			"Active(file)": 41 << 10, "Inactive(file)": 42 << 10, "Active(anon)": 43 << 10, "Inactive(anon)": 44 << 10,
			"Dirty": 45 << 10, "Writeback": 46 << 10, "Slab": 47 << 10,
			"CommitLimit": 1200 << 10, "Committed_AS": 1300 << 10, "VmallocUsed": 48 << 10,
			"HugePages_Total": 9, "HugePages_Free": 8, "HugePages_Rsvd": 7, "HugePages_Surp": 6, "Hugepagesize": 2048 << 10,
			"SwapTotal": 500 << 10, "SwapFree": 300 << 10, "SwapCached": 60 << 10,
			"pswpin": 155, "pswpout": 195, "swappiness": 60, "compact_memory": 1,
		}, nil, [3]uint64{650 << 10, 250 << 10, 100 << 10}, [3]uint64{140 << 10, 60 << 10, 300 << 10}},
		{"busybox", "MemTotal: 1000 kB\r\nMemFree: 200 kB\r\nBuffers: 20 kB\r\nCached: 80 kB\r\n",
			map[string]uint64{"MemTotal": 1000 << 10}, []string{"MemAvailable", "SReclaimable", "pswpout"}, [3]uint64{700 << 10, 100 << 10, 200 << 10}, [3]uint64{}},
		{"no swap", "SwapTotal: 0 kB\nSwapFree: 0 kB\nSwapCached: 0 kB\nswappiness 0\n",
			map[string]uint64{"SwapTotal": 0, "SwapFree": 0, "swappiness": 0}, []string{"MemTotal"}, [3]uint64{}, [3]uint64{}},
		{"container", "MemTotal: 10 kB\nMemFree: 12 kB\nCached: 20 kB\nShmem: 30 kB\nSwapTotal: 4 kB\nSwapFree: 1 kB\nSwapCached: 5 kB\npswpout invalid\n",
			map[string]uint64{"SwapTotal": 4 << 10}, []string{"MemAvailable", "swappiness", "compact_memory", "pswpout"}, [3]uint64{0, 0, 10 << 10}, [3]uint64{0, 3 << 10, 1 << 10}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mem := parseMemory(tc.input, time.Time{})
			for key, want := range tc.values {
				if value, ok := mem.Value(key); !ok || value != want {
					t.Errorf("%s = %d, %v; want %d", key, value, ok, want)
				}
			}
			for _, key := range tc.missing {
				if _, ok := mem.Value(key); ok {
					t.Errorf("%s should be missing", key)
				}
			}
			if got := mem.PhysicalParts(); got != tc.physical {
				t.Errorf("physical = %v, want %v", got, tc.physical)
			}
			if got := mem.SwapParts(); got != tc.swap {
				t.Errorf("swap = %v, want %v", got, tc.swap)
			}
		})
	}
}

func TestMemorySwapRates(t *testing.T) {
	start := time.Unix(100, 0)
	previous := parseMemory("pswpin 40\npswpout 80\n", start)
	for _, tc := range []struct {
		name, next string
		elapsed    time.Duration
		want       [2]SwapRate
	}{
		{"first sample", "pswpin 44\n", 0, [2]SwapRate{}},
		{"normal", "pswpin 45\npswpout 80\n", 2 * time.Second, [2]SwapRate{{2.5, true}, {0, true}}},
		{"reset", "pswpin 1\npswpout 84\n", time.Second, [2]SwapRate{{}, {4, true}}},
		{"missing", "pswpin 40\n", time.Second, [2]SwapRate{{0, true}, {}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseMemory(tc.next, start.Add(tc.elapsed)).SwapRates(previous); got != tc.want {
				t.Errorf("rates = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestMemoryCommands(t *testing.T) {
	for _, tc := range []struct {
		name           string
		action         MemoryAction
		stdout, stderr string
		failed         bool
		want           error
		contains       []string
	}{
		{"drop caches", MemoryDropCaches, "", "", false, nil, []string{"sync || exit $?\necho 3 > /proc/sys/vm/drop_caches"}},
		{"compact", MemoryCompact, "", "", false, nil, []string{`echo 1 > /proc/sys/vm/compact_memory`}},
		{"no compact", MemoryCompact, "", memoryCompact, true, ErrMemoryNoCompact, nil},
		{"read-only container", MemoryDropCaches, "", "sh: /proc/sys/vm/drop_caches: Read-only file system", true, ErrMemoryRestricted, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			failure := errors.New("exit status 1")
			out, err := RunMemoryAction(context.Background(), func(_ context.Context, script string) (string, string, error) {
				for _, want := range tc.contains {
					if !strings.Contains(script, want) {
						t.Errorf("script missing %q", want)
					}
				}
				if tc.failed {
					return tc.stdout, tc.stderr, failure
				}
				return tc.stdout, tc.stderr, nil
			}, tc.action)
			if !errors.Is(err, tc.want) {
				t.Errorf("error = %v, want %v", err, tc.want)
			}
			if !strings.HasPrefix(tc.stderr, "---") && out != tc.stderr {
				t.Errorf("output = %q, want %q", out, tc.stderr)
			}
		})
	}
}
