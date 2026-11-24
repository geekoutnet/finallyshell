package monitor

import (
	"encoding/json"
	"errors"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

func (cmd DockerCmd) CtrStatsScript(ids ...string) (string, error) {
	if err := checkRefs(ids); err != nil {
		return "", err
	}
	return scriptHead + cmd.Line(append([]string{"stats", "--no-stream", "--format", "{{json .}}"}, ids...)...) + "\n", nil
}

func ParseCtrStats(containers []Container, output string) map[string]ContainerStats {
	samples := make(map[string]ContainerStats, len(containers))
	forLines(output, func(line []byte) {
		var sample struct {
			statsJSON
			CPU  string
			PIDS json.RawMessage
		}
		if json.Unmarshal(line, &sample) != nil {
			return
		}
		id := sample.ID
		if id == "" {
			id = sample.Container
		}
		index := -1
		if id != "" && id != "--" {
			index = slices.IndexFunc(containers, func(container Container) bool { return strings.HasPrefix(container.ID, id) })
		} else if sample.Name != "" {
			index = slices.IndexFunc(containers, func(container Container) bool { return container.Name == sample.Name })
		}
		if index < 0 {
			return
		}
		cpu, pids := sample.CPUPerc, sample.PIDs
		if cpu == "" {
			cpu = sample.CPU
		}
		if len(pids) == 0 {
			pids = sample.PIDS
		}
		pidsText := strings.Trim(string(pids), `" `)
		stats := ContainerStats{CPU: parsePercent(cpu), MemPercent: parsePercent(sample.MemPerc)}
		stats.PIDs, _ = strconv.Atoi(pidsText)
		stats.MemUsed, stats.MemLimit = parseSizePair(sample.MemUsage)
		stats.NetRx, stats.NetTx = parseSizePair(sample.NetIO)
		stats.BlockRead, stats.BlockWrite = parseSizePair(sample.BlockIO)
		samples[containers[index].ID] = stats
	})
	return samples
}

type CtrFileOp string

const (
	CtrFileToServer   CtrFileOp = "download"
	CtrFileFromServer CtrFileOp = "upload"
	CtrFileDelete     CtrFileOp = "delete"
	CtrFileMkdir      CtrFileOp = "mkdir"
	CtrFileRename     CtrFileOp = "rename"
)

func (cmd DockerCmd) ctrExec(id string, args ...string) string {
	return cmd.Line(append([]string{"exec", "-e", "LC_ALL=C", id}, args...)...)
}

func (cmd DockerCmd) CtrListScript(id, dir string) (string, error) {
	if err := checkRefs([]string{id}); err != nil {
		return "", err
	}
	if !ctrPath(dir) || path.Clean(dir) != dir {
		return "", CtrConfigError("path")
	}
	dir = strings.TrimSuffix(dir, "/") + "/."
	return scriptHead + cmd.ctrExec(id, "ls", "-lan", "--quoting-style=c", "--time-style=+%s", "--", dir) + " 2>/dev/null || " +
		cmd.ctrExec(id, "ls", "-lanq", "--", dir) + "\n", nil
}

func (cmd DockerCmd) CtrFileScript(op CtrFileOp, id, first, second string) (string, error) {
	if err := checkRefs([]string{id}); err != nil {
		return "", err
	}
	safe := func(value string) bool { return ctrPath(value) && path.Clean(value) == value && value != "/" }
	if !safe(first) || (op == CtrFileRename || op == CtrFileToServer || op == CtrFileFromServer) && !safe(second) {
		return "", CtrConfigError("path")
	}
	var line string
	switch op {
	case CtrFileToServer, CtrFileFromServer:
		if strings.Contains(second, ":") {
			return "", CtrConfigError("path")
		}
		source, dest := id+":"+first, second
		if op == CtrFileFromServer {
			source, dest = dest, source
		}
		line = cmd.Line("cp", "--", source, dest)
		guard := `parent=${1%/*}; [ -x "${parent:-/}" ] && [ ! -e "$1" ] && [ ! -L "$1" ]`
		check := "sh '-c' " + dockerQuote(guard) + " 'docker-copy' " + dockerQuote(second)
		if op == CtrFileFromServer {
			check = cmd.ctrExec(id, "sh", "-c", guard, "docker-copy", first)
		}
		line = check + " || exit 73\n" + line
	case CtrFileDelete:
		line = cmd.ctrExec(id, "rm", "-r", "--", first)
	case CtrFileMkdir:
		line = cmd.ctrExec(id, "mkdir", "--", first)
	case CtrFileRename:
		if first == second {
			return "", CtrConfigError("path")
		}
		line = cmd.ctrExec(id, "mv", "-nT", "--", first, second)
	default:
		return "", errors.New("unknown container file operation")
	}
	return scriptHead + line + "\n", nil
}

type CtrFile struct {
	Name, Mode, Modified string
	Size                 uint64
}

var ctrFileLine = regexp.MustCompile(`^([bcdlps-][rwxstST-]{9})[.+@]?\s+\d+\s+\S+\s+\S+\s+(\d+|\d+,\s*\d+)\s+(?:(-?\d+)|([A-Z][a-z]{2}\s+\d+\s+\d+(?::\d+)?)) (.+)$`)

func ParseCtrFiles(output string) ([]CtrFile, error) {
	var files []CtrFile
	for line := range strings.SplitSeq(strings.TrimRight(output, "\r\n"), "\n") {
		line = strings.TrimSuffix(line, "\r")
		if line == "" || strings.HasPrefix(line, "total ") {
			continue
		}
		fields := ctrFileLine.FindStringSubmatch(line)
		if fields == nil {
			return nil, errors.New("unrecognized container file listing")
		}
		name, modified := fields[5], fields[4]
		if fields[3] != "" {
			var err error
			name, err = ctrQuotedFilename(name, fields[1][0] == 'l')
			if err != nil {
				return nil, err
			}
			modified = fields[3]
		} else {
			if strings.Contains(name, "?") || fields[1][0] == 'l' && strings.Count(name, " -> ") != 1 {
				return nil, errors.New("ambiguous file name in BusyBox listing")
			}
			if fields[1][0] == 'l' {
				name, _, _ = strings.Cut(name, " -> ")
			}
		}
		if name == "." || name == ".." {
			continue
		}
		if name == "" || strings.ContainsAny(name, "/\x00") {
			return nil, errors.New("invalid entry in container file listing")
		}
		size, _ := strconv.ParseUint(fields[2], 10, 64)
		files = append(files, CtrFile{Name: name, Mode: fields[1], Size: size, Modified: modified})
	}
	return files, nil
}

func ctrQuotedFilename(text string, link bool) (string, error) {
	quoted, err := strconv.QuotedPrefix(text)
	if err != nil || !strings.HasPrefix(quoted, `"`) {
		return "", errors.New("missing quoted file name in GNU listing")
	}
	rest := text[len(quoted):]
	if !link && rest != "" || link && !strings.HasPrefix(rest, " -> ") {
		return "", errors.New("ambiguous file name in GNU listing")
	}
	return strconv.Unquote(quoted)
}
