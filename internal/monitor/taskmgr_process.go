package monitor

import (
	"context"
	"encoding/hex"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"
)

const taskProcessScript = taskSectionHeader + `m 'self'; printf '%s\n' "$$"
m 'uname'; uname '-s'
m 'cpu'; grep '-E' '^(cpu|btime )' '/proc/stat' 2>/dev/null
m 'clock'; getconf 'CLK_TCK' 2>/dev/null
m 'ps'; ps '-e' '-o' 'pid=' '-o' 'ppid=' '-o' 'user:32=' '-o' 'rss=' '-o' 'pcpu=' '-o' 'args=' 2>/dev/null || ps '-o' 'pid=,ppid=,user=,rss=,args=' 2>/dev/null
m 'stat'; cat /proc/[0-9]*/stat 2>/dev/null
m 'end'
`

type TaskProcess struct {
	Process
	Threads, Nice int
	Started       time.Time
}

type TaskProcessLister struct{ lister ProcessLister }

func (lister *TaskProcessLister) List(ctx context.Context, run MemoryRunner) ([]TaskProcess, error) {
	out, sections, err := taskReadSections(ctx, run, taskProcessScript)
	if err != nil {
		return nil, err
	}
	return lister.parseSections(out, sections)
}

func (lister *TaskProcessLister) parse(out string) ([]TaskProcess, error) {
	return lister.parseSections(out, procSections(out))
}

func (lister *TaskProcessLister) parseSections(out string, sections map[string]string) ([]TaskProcess, error) {
	list, err := lister.lister.parse(out)
	if err != nil {
		return nil, err
	}
	clock, _ := strconv.ParseUint(strings.TrimSpace(sections["clock"]), 10, 32)
	var boot int64
	for line := range strings.SplitSeq(sections["cpu"], "\n") {
		if value, ok := strings.CutPrefix(line, "btime "); ok {
			boot, _ = strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		}
	}
	stats := make(map[int]TaskProcess, len(list))
	for line := range strings.SplitSeq(sections["stat"], "\n") {
		pid, stat, ok := parseProcStat(line)
		if !ok {
			continue
		}
		nice, threads := taskProcExtra(line)
		row := TaskProcess{Threads: threads, Nice: nice}
		if clock > 0 && boot > 0 && stat.start > 0 {
			row.Started = time.Unix(boot+int64(stat.start/clock), int64(stat.start%clock)*int64(time.Second)/int64(clock))
		}
		stats[pid] = row
	}
	rows := make([]TaskProcess, 0, len(list))
	for _, proc := range list {
		row := stats[proc.PID]
		row.Process = proc
		rows = append(rows, row)
	}
	return rows, nil
}

func taskProcExtra(line string) (nice, threads int) {
	if closing := strings.LastIndexByte(line, ')'); closing >= 0 {
		fields := strings.Fields(line[closing+1:])
		if len(fields) > 17 {
			nice, _ = strconv.Atoi(fields[16])
			threads, _ = strconv.Atoi(fields[17])
		}
	}
	return
}

type TaskAction int

const (
	TaskTerminate TaskAction = iota + 1
	TaskKill
	TaskStop
	TaskContinue
	TaskRenice
)

const taskDenied = procMark + "stat:fail---"

func taskIdentityScript(pid int, start uint64, alive bool) string {
	path := privilegeQuote("/proc/" + strconv.Itoa(pid) + "/stat")
	check := "[ \"${20}\" = " + privilegeQuote(strconv.FormatUint(start, 10)) + " ] || gone\n"
	if alive {
		check += "case \"$1\" in 'Z'|'X'|'x') gone;; esac\n"
	}
	return "gone() { printf '%s\\n' '" + signalGone + "'; exit '" + strconv.Itoa(signalGoneStatus) + "'; }\n" +
		"check() {\nset -f\nif ! stat=$(cat " + path + " 2>&1); then\n" +
		"case \"$stat\" in *'Permission denied'*|*'Operation not permitted'*) printf '%s\\n' '" + taskDenied + "'; exit '4';; *) gone;; esac\nfi\n" +
		"stat=${stat##*\") \"}\nset -- $stat\n" + check + "set +f\n}\ncheck\n"
}

func taskActionScript(pid int, start uint64, action TaskAction, nice int) (string, error) {
	if pid <= 1 || start == 0 {
		return "", ErrNoProcess
	}
	var command string
	quotedPID := privilegeQuote(strconv.Itoa(pid))
	switch action {
	case TaskTerminate, TaskKill, TaskStop, TaskContinue:
		signal := [...]string{TaskTerminate: "TERM", TaskKill: "KILL", TaskStop: "STOP", TaskContinue: "CONT"}[action]
		command = "kill '-s' " + privilegeQuote(signal) + " " + quotedPID
	case TaskRenice:
		if nice < -20 || nice > 19 {
			return "", errors.New("priority out of range")
		}
		command = "renice '-n' " + privilegeQuote(strconv.Itoa(nice)) + " '-p' " + quotedPID
	default:
		return "", errors.New("unknown process action")
	}
	return "export LC_ALL='C'\n" + taskIdentityScript(pid, start, true) + command + "\n", nil
}

func RunTaskAction(ctx context.Context, run, root MemoryRunner, pid int, start uint64, action TaskAction, nice int) error {
	script, err := taskActionScript(pid, start, action, nice)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	out, stderr, err := run(ctx, script)
	if ctx.Err() == nil && err != nil && !strings.Contains(out, signalGone) && root != nil && (strings.Contains(out, taskDenied) || taskPermission(stdoutAndErr(out, stderr))) {
		out, stderr, err = root(ctx, script)
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if strings.Contains(out, signalGone) {
		return ErrNoProcess
	}
	if strings.Contains(out, taskDenied) {
		return ErrPrivilegeDenied
	}
	if err != nil && !privilegeAuthError(err) && !errors.Is(err, ErrPrivilegeCancelled) {
		if message := stdoutAndErr(out, stderr); message != "" {
			return &SignalError{Msg: cleanKillMessage(message)}
		}
	}
	return err
}

func stdoutAndErr(out, stderr string) string { return strings.TrimSpace(stderr + "\n" + out) }

func taskPermission(text string) bool {
	return strings.Contains(text, "Operation not permitted") || strings.Contains(text, "Permission denied")
}

type TaskProperty int

const (
	TaskBasic TaskProperty = iota
	TaskMaps
	TaskFiles
	TaskEnv
	TaskSockets
)

type TaskMap struct {
	Range, Access, Path string
	Bytes               uint64
}

type TaskFD struct {
	Number int
	Target string
}

type TaskEnvVar struct {
	Name, value string
	Sensitive   bool
}

func (env TaskEnvVar) Display(reveal bool) string {
	if env.Sensitive && !reveal {
		return "••••••"
	}
	return env.value
}

type TaskStatus struct {
	PPID, Threads int
	RSS           uint64
}

type TaskDetails struct {
	Basic     ProcessDetails
	Status    TaskStatus
	Nice      int
	Maps      []TaskMap
	Files     []TaskFD
	Env       []TaskEnvVar
	Sockets   []Socket
	Missing   []string
	Truncated bool
}

func taskDetailsScript(pid int, start uint64, tab TaskProperty) (string, error) {
	if pid <= 0 || start == 0 {
		return "", ErrNoProcess
	}
	path := privilegeQuote("/proc/" + strconv.Itoa(pid))
	script := taskSectionHeader + taskIdentityScript(pid, start, false) + "path=" + path + `
x() { name="$1"; shift; m "$name"; if "$@" 2>/dev/null; then printf '\n'; m "$name:ok"; else printf '\n'; m "$name:fail"; fi; }
`
	switch tab {
	case TaskBasic:
		script += `x 'stat' cat "$path/stat"
x 'status' cat "$path/status"
x 'owner' ls '-ld' "$path"
x 'cmdline' head '-c' '131073' "$path/cmdline"
x 'exe' readlink "$path/exe"
x 'cwd' readlink "$path/cwd"
`
	case TaskMaps:
		script += `x 'maps' head '-c' '262145' "$path/maps"
`
	case TaskFiles, TaskSockets:
		script += `files() {
  [ -r "$path/fd" ] && [ -x "$path/fd" ] || return '1'
  count='0'; partial='0'
  for fd in "$path"/fd/[0-9]*; do
    if [ -L "$fd" ]; then
      if [ "$count" -ge '512' ]; then m 'truncated'; break; fi
      count=$((count+1))
      if target=$(readlink "$fd" 2>/dev/null); then
        printf '%s\t%s\000' "${fd##*/}" "$target"
      else
        partial='1'
      fi
    fi
  done
  if [ "$partial" = '1' ]; then printf '\n'; m 'partial-fd'; fi
}
x 'fd' files
`
		if tab == TaskSockets {
			script += `for proto in 'tcp' 'tcp6' 'udp' 'udp6'; do
  if [ -e "$path/net/$proto" ]; then x "$proto" head '-c' '262145' "$path/net/$proto"; fi
done
`
		}
	case TaskEnv:
		script += `environment() {
  if [ -r "$path/environ" ]; then
    content=$(head '-c' '131073' "$path/environ" 2>/dev/null | od '-An' '-v' '-tx1') || return '1'
    [ -n "$content" ] || head '-c' '1' "$path/environ" >/dev/null 2>&1 || return '1'
    printf '%s\n' "$content"
  else
    return '1'
  fi
}
x 'envhex' environment
`
	default:
		return "", ErrScriptOutput
	}
	return script + "check\nm 'end'\n", nil
}

func ReadTaskDetails(ctx context.Context, run MemoryRunner, pid int, start uint64, tab TaskProperty) (TaskDetails, error) {
	script, err := taskDetailsScript(pid, start, tab)
	if err != nil {
		return TaskDetails{}, err
	}
	if err := ctx.Err(); err != nil {
		return TaskDetails{}, err
	}
	out, _, err := run(ctx, script)
	if ctx.Err() != nil {
		return TaskDetails{}, ctx.Err()
	}
	sections := procSections(out)
	if _, ok := sections["gone"]; ok {
		return TaskDetails{}, ErrNoProcess
	}
	if _, denied := sections["stat:fail"]; denied {
		return TaskDetails{Missing: []string{"stat"}}, nil
	}
	if err != nil {
		return TaskDetails{}, err
	}
	if _, ok := sections["end"]; !ok {
		return TaskDetails{}, ErrScriptOutput
	}
	return parseTaskDetails(pid, tab, out, sections)
}

func parseTaskDetails(pid int, tab TaskProperty, out string, sections map[string]string) (TaskDetails, error) {
	var details TaskDetails
	_, details.Truncated = sections["truncated"]
	read := func(name string, maxBytes int, delimiter byte) string {
		if _, ok := sections[name+":ok"]; !ok {
			details.Missing = append(details.Missing, name)
			return ""
		}
		value := sections[name]
		if maxBytes > 0 && len(value) > maxBytes {
			details.Truncated = true
			if last := strings.LastIndexByte(value[:maxBytes], delimiter); last >= 0 {
				return value[:last+1]
			}
			return ""
		}
		return value
	}
	switch tab {
	case TaskBasic:
		basic, err := parseDetails(pid, out)
		if err != nil {
			return details, err
		}
		details.Status = parseTaskStatus(read("status", 0, '\n'))
		details.Nice, _ = taskProcExtra(sections["stat"])
		if _, stat, ok := parseProcStat(sections["stat"]); ok && details.Status.PPID == 0 {
			details.Status.PPID = stat.ppid
		}
		basic.Args = splitNUL(read("cmdline", 131072, 0))
		read("owner", 0, '\n')
		if basic.PathDenied {
			read("exe", 0, '\n')
		}
		if basic.CwdDenied {
			read("cwd", 0, '\n')
		}
		details.Basic = basic
	case TaskMaps:
		details.Maps = parseTaskMaps(read("maps", 262144, '\n'))
	case TaskFiles, TaskSockets:
		files := read("fd", 0, 0)
		details.Files = parseTaskFD(files)
		if _, partial := sections["partial-fd"]; partial {
			details.Missing = append(details.Missing, "fd")
		}
		if tab == TaskSockets {
			inodes := make(map[uint64]bool)
			for _, fd := range details.Files {
				if inode, ok := strings.CutPrefix(fd.Target, "socket:["); ok && strings.HasSuffix(inode, "]") {
					if id, err := strconv.ParseUint(strings.TrimSuffix(inode, "]"), 10, 64); err == nil && id != 0 {
						inodes[id] = true
					}
				}
			}
			for _, name := range []string{"tcp", "tcp6", "udp", "udp6"} {
				if _, ok := sections[name]; !ok {
					continue
				}
				for _, socket := range parseProcNet(read(name, 262144, '\n'), strings.TrimSuffix(name, "6")) {
					if inodes[socket.inode] {
						details.Sockets = append(details.Sockets, socket.Socket)
					}
				}
			}
		}
	case TaskEnv:
		encoded := read("envhex", 0, '\n')
		if len(details.Missing) == 0 {
			content, err := hex.DecodeString(strings.Join(strings.Fields(encoded), ""))
			if err != nil {
				return details, ErrScriptOutput
			}
			defer clear(content)
			details.Truncated = len(content) > 131072
			details.Env = parseTaskEnvironment(string(content[:min(len(content), 131072)]))
		}
	}
	return details, nil
}

func parseTaskStatus(out string) TaskStatus {
	values := taskKeyValues(out)
	ppid, _ := strconv.Atoi(values["PPid"])
	threads, _ := strconv.Atoi(values["Threads"])
	rss, _, _ := strings.Cut(values["VmRSS"], " ")
	return TaskStatus{PPID: ppid, Threads: threads, RSS: parseKB(rss)}
}

func parseTaskMaps(out string) []TaskMap {
	var maps []TaskMap
	for line := range strings.SplitSeq(out, "\n") {
		fields, path := fieldsN(line, 5)
		if len(fields) != 5 {
			continue
		}
		first, last, ok := strings.Cut(fields[0], "-")
		start, startErr := strconv.ParseUint(first, 16, 64)
		end, endErr := strconv.ParseUint(last, 16, 64)
		if ok && startErr == nil && endErr == nil && end >= start {
			maps = append(maps, TaskMap{Range: fields[0], Access: fields[1], Path: path, Bytes: end - start})
		}
	}
	return maps
}

func parseTaskFD(out string) []TaskFD {
	var files []TaskFD
	for line := range strings.SplitSeq(out, "\x00") {
		id, target, ok := strings.Cut(line, "\t")
		if number, err := strconv.Atoi(id); ok && err == nil && number >= 0 {
			files = append(files, TaskFD{Number: number, Target: target})
		}
	}
	slices.SortFunc(files, func(left, right TaskFD) int { return left.Number - right.Number })
	return files
}

func parseTaskEnvironment(out string) []TaskEnvVar {
	var result []TaskEnvVar
	for len(out) > 0 {
		entry, rest, complete := strings.Cut(out, "\x00")
		if !complete {
			break
		}
		out = rest
		name, value, valid := strings.Cut(entry, "=")
		if !valid || name == "" {
			continue
		}
		upper := strings.ToUpper(name)
		sensitive := strings.Contains(upper, "PASS") || strings.Contains(upper, "TOKEN") || strings.Contains(upper, "SECRET") || strings.Contains(upper, "KEY")
		result = append(result, TaskEnvVar{Name: name, value: value, Sensitive: sensitive})
	}
	return result
}
