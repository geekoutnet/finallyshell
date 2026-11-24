package monitor

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

const memoryScript = `cat /proc/meminfo
grep -E '^pswp(in|out) ' /proc/vmstat 2>/dev/null
if read -r swappiness 2>/dev/null </proc/sys/vm/swappiness; then printf 'swappiness %s\n' "$swappiness"; fi
if [ -e /proc/sys/vm/compact_memory ]; then echo 'compact_memory 1'; fi
`

type Memory struct {
	SampledAt time.Time
	Rates     [2]SwapRate
	values    map[string]uint64
}

type SwapRate struct {
	PagesPerSecond float64
	Known          bool
}

func parseMemory(out string, at time.Time) Memory {
	mem := Memory{SampledAt: at, values: make(map[string]uint64)}
	for line := range strings.SplitSeq(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		value, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			continue
		}
		if len(fields) > 2 {
			if fields[2] != "kB" || value > math.MaxUint64/1024 {
				continue
			}
			value *= 1024
		}
		mem.values[strings.TrimSuffix(fields[0], ":")] = value
	}
	return mem
}

func (mem Memory) Value(key string) (uint64, bool) {
	value, ok := mem.values[key]
	return value, ok
}

func (mem Memory) PhysicalParts() [3]uint64 {
	total := mem.values["MemTotal"]
	free := min(mem.values["MemFree"], total)
	buffer := min(mem.values["Buffers"], total-free)
	cache := mem.values["Cached"] - min(mem.values["Cached"], mem.values["Shmem"])
	cache = min(cache, total-free-buffer)
	reclaimable := buffer + cache + min(mem.values["SReclaimable"], total-free-buffer-cache)
	return [3]uint64{total - free - reclaimable, reclaimable, free}
}

func (mem Memory) summary() (memTotal, memUsed, swapTotal, swapUsed uint64) {
	memTotal = mem.values["MemTotal"]
	available, known := mem.Value("MemAvailable")
	if known {
		memUsed = memTotal - min(available, memTotal)
	} else if _, known := mem.Value("MemFree"); known {
		memUsed = mem.PhysicalParts()[0]
	}
	swapTotal = mem.values["SwapTotal"]
	if used, known := mem.SwapUsed(); known {
		swapUsed = used
	}
	return
}

func (mem Memory) SwapUsed() (uint64, bool) {
	total, known := mem.Value("SwapTotal")
	if !known || total == 0 {
		return 0, known
	}
	free, known := mem.Value("SwapFree")
	return total - min(free, total), known && free <= total
}

func (mem Memory) SwapParts() [3]uint64 {
	used, ok := mem.SwapUsed()
	if !ok {
		return [3]uint64{}
	}
	cached := min(mem.values["SwapCached"], used)
	return [3]uint64{used - cached, cached, mem.values["SwapTotal"] - used}
}

func (mem Memory) SwapRates(previous Memory) (rates [2]SwapRate) {
	elapsed := mem.SampledAt.Sub(previous.SampledAt).Seconds()
	if elapsed <= 0 {
		return rates
	}
	for index, key := range []string{"pswpin", "pswpout"} {
		before, had := previous.Value(key)
		after, has := mem.Value(key)
		if had && has && after >= before {
			rates[index] = SwapRate{float64(after-before) / elapsed, true}
		}
	}
	return rates
}

type MemoryRunner func(context.Context, string) (stdout, stderr string, err error)

func ReadMemory(ctx context.Context, run MemoryRunner) (Memory, error) {
	sampledAt := time.Now()
	out, stderr, err := run(ctx, memoryScript)
	if err != nil {
		if stderr = strings.TrimSpace(stderr); stderr != "" {
			err = fmt.Errorf("%w: %s", err, stderr)
		}
		return Memory{}, err
	}
	mem := parseMemory(out, sampledAt)
	if mem.values["MemTotal"] == 0 {
		return Memory{}, ErrMemoryUnavailable
	}
	return mem, nil
}

var (
	ErrMemoryUnavailable = errors.New("memory counters unavailable")
	ErrMemoryPrivilege   = errors.New("needs root or passwordless sudo")
	ErrMemoryRestricted  = errors.New("kernel memory controls are restricted")
	ErrMemoryNoCompact   = errors.New("memory compaction is not available")
)

type MemoryAction int

const (
	MemoryDropCaches MemoryAction = iota + 1
	MemoryCompact
)

const (
	memoryStarted = "---FINALLYSHELL:memory-started---"
	memorySudo    = "---FINALLYSHELL:memory-sudo---"
	memoryCompact = "---FINALLYSHELL:memory-compact---"
)

func memoryActionScript(action MemoryAction) (string, error) {
	var body string
	switch action {
	case MemoryDropCaches:
		body = `sync || exit $?
echo 3 > /proc/sys/vm/drop_caches`
	case MemoryCompact:
		body = `[ -e /proc/sys/vm/compact_memory ] || { echo ` + memoryCompact + ` >&2; exit 1; }
echo 1 > /proc/sys/vm/compact_memory`
	default:
		return "", errors.New("unknown memory action")
	}
	body = "echo " + memoryStarted + "\n" + body
	quoted := "'" + strings.ReplaceAll(body, "'", `'\''`) + "'"
	return `export LC_ALL=C
if [ "$(id -u)" = 0 ]; then
    exec sh -c ` + quoted + `
fi
echo ` + memorySudo + `
exec sudo -n -- sh -c ` + quoted + "\n", nil
}

func RunMemoryAction(ctx context.Context, run MemoryRunner, action MemoryAction) (string, error) {
	script, err := memoryActionScript(action)
	if err != nil {
		return "", err
	}
	stdout, stderr, err := run(ctx, script)
	var lines []string
	markers := make(map[string]bool)
	for line := range strings.SplitSeq(stdout+"\n"+stderr, "\n") {
		switch strings.TrimSpace(line) {
		case memoryStarted, memorySudo, memoryCompact:
			markers[strings.TrimSpace(line)] = true
		default:
			if strings.TrimSpace(line) != "" {
				lines = append(lines, line)
			}
		}
	}
	output := strings.Join(lines, "\n")
	switch {
	case err == nil, ctx.Err() != nil:
	case markers[memoryCompact]:
		err = ErrMemoryNoCompact
	case markers[memorySudo] && !markers[memoryStarted]:
		err = ErrMemoryPrivilege
	case strings.Contains(output, "Read-only file system"), strings.Contains(output, "Operation not permitted"), strings.Contains(output, "Permission denied"):
		err = ErrMemoryRestricted
	}
	return output, err
}
