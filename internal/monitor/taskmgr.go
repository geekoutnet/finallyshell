package monitor

import (
	"cmp"
	"context"
	"slices"
	"strconv"
	"strings"
	"time"
)

const taskSectionHeader = `export LC_ALL='C'
m() { printf '%s%s%s\n' '---FINALLYSHELL:' "$1" '---'; }
`

const taskPerformanceScript = taskSectionHeader + `m 'uname'; uname '-s'
m 'cpu'; cat '/proc/stat'
m 'disk'; cat '/proc/diskstats'
m 'load'; cat '/proc/loadavg'
m 'count'; set -- /proc/[0-9]*; if [ -d "$1" ]; then printf '%s\n' "$#"; fi
m 'speed'
if [ -r '/sys/devices/system/cpu/cpu0/cpufreq/scaling_cur_freq' ]; then
  cat '/sys/devices/system/cpu/cpu0/cpufreq/scaling_cur_freq'
else
  awk '-F:' '/^cpu MHz[[:space:]]*:/ {printf "%.0f\n", $2 * 1000; exit}' '/proc/cpuinfo'
fi
`

const taskHardwareScript = `m 'lscpu'; lscpu 2>/dev/null
m 'cpuinfo'; grep '-E' '^(model name|cpu MHz|cpu model|Hardware|physical id|core id|flags|Features)[[:space:]]*:' '/proc/cpuinfo'
m 'base'; cat '/sys/devices/system/cpu/cpu0/cpufreq/base_frequency' 2>/dev/null
m 'cache'
for entry in /sys/devices/system/cpu/cpu0/cache/index*; do
  if [ -r "$entry/level" ]; then
    read -r level < "$entry/level"; read -r type < "$entry/type"; read -r size < "$entry/size"
    printf '%s %s %s\n' "$level" "$type" "$size"
  fi
done
m 'block'
for entry in /sys/class/block/*; do
  if [ -r "$entry/size" ]; then
    partition='0'; if [ -f "$entry/partition" ]; then partition='1'; fi
    read -r size < "$entry/size"; printf '%s %s %s\n' "${entry##*/}" "$size" "$partition"
  fi
done
m 'mount'; cat '/proc/self/mountinfo'
m 'addr'; ip '-o' 'address' 'show' 2>/dev/null
m 'link'
for entry in /sys/class/net/*; do
  if speed=$(cat "$entry/speed" 2>/dev/null); then printf '%s %s\n' "${entry##*/}" "$speed"; fi
done
`

type TaskHardware struct {
	Model, Virtualization   string
	GHz, BaseGHz            float64
	Sockets, Cores, Threads int
	Cache                   [4]string
	Capacity                map[string]uint64
	Partitions              map[string]bool
	Mounts                  map[string][]string
	Addresses               map[string][]string
	LinkMbps                map[string]uint64
}

type TaskCPU struct {
	Name string
	Time [10]float64
}

func (cpu TaskCPU) Usage() float64 { return max(0, 100-cpu.Time[3]-cpu.Time[4]) }

type TaskDisk struct {
	Name              string
	Read, Write, IOPS float64
	Busy              float64
	Measured          bool
	Partition         bool
	Capacity          uint64
	Mounts            []string
}

type TaskPerformance struct {
	Load               [3]float64
	Logical            int
	Processes, Running int
	Blocked, Threads   int
	GHz                float64
	CPUs               []TaskCPU
	Disks              []TaskDisk
	Hardware           TaskHardware
}

type TaskSampler struct {
	cpu        map[string][10]uint64
	disks      map[string]taskDiskCounters
	hardware   TaskHardware
	at         time.Time
	hardwareAt time.Time
}

func (sampler *TaskSampler) Sample(ctx context.Context, run MemoryRunner) (TaskPerformance, error) {
	now := time.Now()
	full := now.Sub(sampler.hardwareAt) >= 30*time.Second
	script := taskPerformanceScript
	if full {
		script += taskHardwareScript
	}
	sections, err := taskSections(ctx, run, script+"m 'end'\n")
	if err != nil {
		return TaskPerformance{}, err
	}
	cpu := parseTaskCPU(sections["cpu"])
	if _, ok := cpu["cpu"]; !ok {
		return TaskPerformance{}, ErrScriptOutput
	}
	now = time.Now()
	if full {
		sampler.hardware, sampler.hardwareAt = parseTaskHardware(sections), now
	}
	sample := TaskPerformance{Load: parseLoad(sections["load"]), Logical: max(1, len(cpu)-1), Hardware: sampler.hardware}
	sample.Processes, _ = strconv.Atoi(strings.TrimSpace(sections["count"]))
	sample.GHz = taskGHz(sections["speed"])
	if sample.GHz == 0 {
		sample.GHz = sample.Hardware.GHz
	}
	for line := range strings.SplitSeq(sections["cpu"], "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		value, _ := strconv.Atoi(fields[1])
		switch fields[0] {
		case "procs_running":
			sample.Running = value
		case "procs_blocked":
			sample.Blocked = value
		}
	}
	if fields := strings.Fields(sections["load"]); len(fields) >= 4 {
		_, threads, _ := strings.Cut(fields[3], "/")
		sample.Threads, _ = strconv.Atoi(threads)
	}
	for name, after := range cpu {
		if before, found := sampler.cpu[name]; found {
			if percentages, ok := taskCPUPercent(before, after); ok {
				sample.CPUs = append(sample.CPUs, TaskCPU{Name: name, Time: percentages})
			}
		}
	}
	slices.SortFunc(sample.CPUs, func(left, right TaskCPU) int { return cmp.Compare(taskCPUNumber(left.Name), taskCPUNumber(right.Name)) })
	disks := parseTaskDisk(sections["disk"])
	for name, after := range disks {
		unusedLoop := strings.HasPrefix(name, "loop") && len(sampler.hardware.Mounts[after.device]) == 0
		if (unusedLoop || strings.HasPrefix(name, "ram")) && after.read == 0 && after.write == 0 {
			continue
		}
		disk := TaskDisk{Name: name, Partition: sampler.hardware.Partitions[name], Capacity: sampler.hardware.Capacity[name], Mounts: sampler.hardware.Mounts[after.device]}
		if before, found := sampler.disks[name]; found && before.device == after.device {
			disk.Read, disk.Write, disk.IOPS, disk.Busy, disk.Measured = taskDiskRates(before, after, now.Sub(sampler.at).Seconds())
		}
		sample.Disks = append(sample.Disks, disk)
	}
	slices.SortFunc(sample.Disks, func(left, right TaskDisk) int {
		if left.Partition != right.Partition {
			if left.Partition {
				return 1
			}
			return -1
		}
		return cmp.Compare(left.Name, right.Name)
	})
	sampler.cpu, sampler.disks, sampler.at = cpu, disks, now
	return sample, nil
}

func taskSections(ctx context.Context, run MemoryRunner, script string) (map[string]string, error) {
	_, sections, err := taskReadSections(ctx, run, script)
	return sections, err
}

func taskReadSections(ctx context.Context, run MemoryRunner, script string) (string, map[string]string, error) {
	if err := ctx.Err(); err != nil {
		return "", nil, err
	}
	out, _, err := run(ctx, script)
	if ctx.Err() != nil {
		return "", nil, ctx.Err()
	}
	if err != nil {
		return "", nil, err
	}
	sections := procSections(out)
	if _, ok := sections["end"]; !ok {
		return "", nil, ErrScriptOutput
	}
	if system := strings.TrimSpace(sections["uname"]); system != "" && system != "Linux" {
		return "", nil, ErrNotLinux
	}
	return out, sections, nil
}

func taskCPUNumber(name string) int {
	if name == "cpu" {
		return -1
	}
	number, _ := strconv.Atoi(strings.TrimPrefix(name, "cpu"))
	return number
}

func parseTaskCPU(out string) map[string][10]uint64 {
	rows := make(map[string][10]uint64)
	for line := range strings.SplitSeq(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 || !strings.HasPrefix(fields[0], "cpu") {
			continue
		}
		if name := strings.TrimPrefix(fields[0], "cpu"); name != "" {
			if _, err := strconv.ParseUint(name, 10, 32); err != nil {
				continue
			}
		}
		var counters [10]uint64
		valid := true
		for index, field := range fields[1:min(11, len(fields))] {
			value, err := strconv.ParseUint(field, 10, 64)
			if err != nil {
				valid = false
				break
			}
			counters[index] = value
		}
		if valid {
			rows[fields[0]] = counters
		}
	}
	return rows
}

func taskCPUPercent(before, after [10]uint64) (percent [10]float64, ok bool) {
	var total float64
	for index, value := range after {
		if value < before[index] {
			return percent, false
		}
		percent[index] = float64(value - before[index])
		if index < 8 {
			total += percent[index]
		}
	}
	percent[0] -= percent[8]
	percent[1] -= percent[9]
	if total == 0 || percent[0] < 0 || percent[1] < 0 {
		return percent, false
	}
	for index := range percent {
		percent[index] *= 100 / total
	}
	return percent, true
}

type taskDiskCounters struct {
	device                           string
	reads, read, writes, write, busy uint64
}

func parseTaskDisk(out string) map[string]taskDiskCounters {
	disks := make(map[string]taskDiskCounters)
	for line := range strings.SplitSeq(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 14 {
			continue
		}
		var values [5]uint64
		valid := true
		for index, field := range [...]int{3, 5, 7, 9, 12} {
			value, err := strconv.ParseUint(fields[field], 10, 64)
			if err != nil {
				valid = false
				break
			}
			values[index] = value
		}
		if valid {
			disks[fields[2]] = taskDiskCounters{device: fields[0] + ":" + fields[1], reads: values[0], read: values[1], writes: values[2], write: values[3], busy: values[4]}
		}
	}
	return disks
}

func taskDiskRates(before, after taskDiskCounters, secs float64) (read, write, iops, busy float64, ok bool) {
	if secs <= 0 || before.read > after.read || before.write > after.write || before.reads > after.reads || before.writes > after.writes || before.busy > after.busy {
		return 0, 0, 0, 0, false
	}
	return float64(after.read-before.read) * 512 / secs, float64(after.write-before.write) * 512 / secs,
		(float64(after.reads-before.reads) + float64(after.writes-before.writes)) / secs, min(100, float64(after.busy-before.busy)/secs/10), true
}

func taskKeyValues(out string) map[string]string {
	values := make(map[string]string)
	for line := range strings.SplitSeq(out, "\n") {
		if key, value, ok := strings.Cut(line, ":"); ok {
			key = strings.TrimSpace(key)
			if _, exists := values[key]; !exists {
				values[key] = strings.TrimSpace(value)
			}
		}
	}
	return values
}

func taskGHz(kHz string) float64 {
	value, _ := strconv.ParseUint(strings.TrimSpace(kHz), 10, 64)
	return float64(value) / 1e6
}

func parseTaskHardware(sections map[string]string) TaskHardware {
	info := TaskHardware{Capacity: make(map[string]uint64), Partitions: make(map[string]bool), Mounts: make(map[string][]string), Addresses: make(map[string][]string), LinkMbps: make(map[string]uint64)}
	lscpu, cpuinfo := taskKeyValues(sections["lscpu"]), taskKeyValues(sections["cpuinfo"])
	info.Model = lscpu["Model name"]
	if info.Model == "" {
		info.Model = cpuinfo["model name"]
	}
	if info.Model == "" {
		info.Model = cpuinfo["Hardware"]
	}
	if info.Model == "" {
		info.Model = cpuinfo["cpu model"]
	}
	info.Sockets, _ = strconv.Atoi(lscpu["Socket(s)"])
	cores, _ := strconv.Atoi(lscpu["Core(s) per socket"])
	info.Cores = cores * info.Sockets
	info.Threads, _ = strconv.Atoi(lscpu["CPU(s)"])
	info.Virtualization = lscpu["Virtualization"]
	if info.Virtualization == "" {
		for _, flag := range strings.Fields(cpuinfo["flags"] + " " + cpuinfo["Features"]) {
			if flag == "vmx" {
				info.Virtualization = "VT-x"
				break
			}
			if flag == "svm" {
				info.Virtualization = "AMD-V"
				break
			}
		}
	}
	if info.Sockets == 0 || info.Cores == 0 {
		physical, cores := taskTopology(sections["cpuinfo"])
		if info.Sockets == 0 {
			info.Sockets = physical
		}
		if info.Cores == 0 {
			info.Cores = cores
		}
	}
	if hypervisor := lscpu["Hypervisor vendor"]; hypervisor != "" {
		info.Virtualization = strings.Trim(strings.Join([]string{info.Virtualization, hypervisor}, " / "), " /")
	}
	freq, _ := strconv.ParseFloat(cpuinfo["cpu MHz"], 64)
	info.GHz, info.BaseGHz = freq/1000, taskGHz(sections["base"])
	info.Cache = [4]string{lscpu["L1d cache"], lscpu["L1i cache"], lscpu["L2 cache"], lscpu["L3 cache"]}
	for line := range strings.SplitSeq(sections["cache"], "\n") {
		fields := strings.Fields(line)
		if len(fields) != 3 {
			continue
		}
		switch fields[0] {
		case "1":
			switch fields[1] {
			case "Data":
				info.Cache[0] = fields[2]
			case "Instruction":
				info.Cache[1] = fields[2]
			}
		case "2":
			info.Cache[2] = fields[2]
		case "3":
			info.Cache[3] = fields[2]
		}
	}
	for line := range strings.SplitSeq(sections["block"], "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 {
			if value, err := strconv.ParseUint(fields[1], 10, 55); err == nil {
				info.Capacity[fields[0]] = value * 512
			}
			if len(fields) >= 3 && fields[2] == "1" {
				info.Partitions[fields[0]] = true
			}
		}
	}
	for line := range strings.SplitSeq(sections["mount"], "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 6 {
			mount := strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`).Replace(fields[4])
			if !slices.Contains(info.Mounts[fields[2]], mount) {
				info.Mounts[fields[2]] = append(info.Mounts[fields[2]], mount)
			}
		}
	}
	for line := range strings.SplitSeq(sections["addr"], "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 4 && (fields[2] == "inet" || fields[2] == "inet6") {
			name, _, _ := strings.Cut(fields[1], "@")
			info.Addresses[name] = append(info.Addresses[name], fields[3])
		}
	}
	for line := range strings.SplitSeq(sections["link"], "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 {
			if value, err := strconv.ParseUint(fields[1], 10, 64); err == nil && value > 0 {
				info.LinkMbps[fields[0]] = value
			}
		}
	}
	return info
}

func taskTopology(out string) (int, int) {
	physical, cores := map[string]bool{}, map[string]bool{}
	var socket string
	for line := range strings.SplitSeq(out, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		switch key {
		case "physical id":
			socket = value
			physical[socket] = true
		case "core id":
			if socket != "" && value != "" {
				cores[socket+":"+value] = true
			}
		}
	}
	return len(physical), len(cores)
}

func TaskIfaceTotals(iface Iface) (received, sent uint64) { return iface.rx, iface.tx }

type TaskLogin struct{ User, TTY, Started, Source string }

func ReadTaskLogins(ctx context.Context, run MemoryRunner) ([]TaskLogin, error) {
	sections, err := taskSections(ctx, run, taskSectionHeader+`m 'uname'; uname '-s'
m 'who'; if who; then m 'end'; fi
`)
	if err != nil {
		return nil, err
	}
	return parseTaskLogins(sections["who"]), nil
}

func parseTaskLogins(out string) []TaskLogin {
	var logins []TaskLogin
	for line := range strings.SplitSeq(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		var source string
		if last := fields[len(fields)-1]; strings.HasPrefix(last, "(") && strings.HasSuffix(last, ")") {
			source, fields = last[1:len(last)-1], fields[:len(fields)-1]
		}
		if len(fields) < 3 {
			continue
		}
		logins = append(logins, TaskLogin{User: fields[0], TTY: fields[1], Started: strings.Join(fields[2:], " "), Source: source})
	}
	return logins
}
