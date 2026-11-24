package monitor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

type SystemRead func(context.Context, string) (string, string, error)

type ServiceUnit struct {
	Name, Description, Load, Active, Sub, Enabled string
	PID, Memory                                   int64
	Entered                                       string
	CanReload                                     bool
}

func SystemCommand(args ...string) string {
	quoted := make([]string, len(args))
	for index, arg := range args {
		quoted[index] = privilegeQuote(arg)
	}
	return strings.Join(quoted, " ")
}

var serviceName = regexp.MustCompile(`^(?:[A-Za-z0-9:_.@-]|\\x[0-9a-fA-F]{2})+\.service$`)

func ValidService(name string) bool {
	return len(name) <= 255 && !strings.HasPrefix(name, "-") && serviceName.MatchString(name)
}

func ParseServiceUnits(text string, asJSON bool) ([]ServiceUnit, error) {
	var rows []ServiceUnit
	if asJSON {
		var decoded []struct {
			Unit, Load, Active, Sub, Description string
		}
		if err := json.Unmarshal([]byte(text), &decoded); err != nil {
			return nil, err
		}
		for _, item := range decoded {
			if ValidService(item.Unit) {
				rows = append(rows, ServiceUnit{Name: item.Unit, Load: item.Load, Active: item.Active, Sub: item.Sub, Description: item.Description})
			}
		}
		return rows, nil
	}
	for line := range strings.SplitSeq(text, "\n") {
		fields := strings.Fields(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "●")))
		if len(fields) >= 4 && ValidService(fields[0]) {
			rows = append(rows, ServiceUnit{Name: fields[0], Load: fields[1], Active: fields[2], Sub: fields[3], Description: strings.Join(fields[4:], " ")})
		}
	}
	return rows, nil
}

func ParseServiceFiles(text string) []ServiceUnit {
	var rows []ServiceUnit
	for line := range strings.SplitSeq(text, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && ValidService(fields[0]) {
			rows = append(rows, ServiceUnit{Name: fields[0], Enabled: fields[1]})
		}
	}
	return rows
}

func ParseServiceProperties(text string) map[string]ServiceUnit {
	rows := make(map[string]ServiceUnit)
	for block := range strings.SplitSeq(strings.ReplaceAll(text, "\r\n", "\n"), "\n\n") {
		row := ServiceUnit{Memory: -1}
		for line := range strings.SplitSeq(block, "\n") {
			key, value, _ := strings.Cut(line, "=")
			switch key {
			case "Id":
				row.Name = value
			case "MainPID":
				row.PID, _ = strconv.ParseInt(value, 10, 64)
			case "MemoryCurrent":
				if memory, err := strconv.ParseInt(value, 10, 64); err == nil && memory >= 0 {
					row.Memory = memory
				}
			case "ActiveEnterTimestamp":
				row.Entered = value
			case "CanReload":
				row.CanReload = value == "yes"
			}
		}
		if ValidService(row.Name) {
			rows[row.Name] = row
		}
	}
	return rows
}

type ServiceStatus struct {
	Loaded, Active, MainPID, Memory string
}

func ParseServiceStatus(text string) ServiceStatus {
	var status ServiceStatus
	for line := range strings.SplitSeq(text, "\n") {
		key, value, _ := strings.Cut(strings.TrimSpace(line), ":")
		switch key {
		case "Loaded":
			status.Loaded = strings.TrimSpace(value)
		case "Active":
			status.Active = strings.TrimSpace(value)
		case "Main PID":
			status.MainPID = strings.TrimSpace(value)
		case "Memory":
			status.Memory = strings.TrimSpace(value)
		}
	}
	return status
}

func systemctl(args ...string) string {
	return SystemCommand(append([]string{"env", "LC_ALL=C", "SYSTEMD_COLORS=0", "SYSTEMD_PAGER=cat", "systemctl"}, args...)...)
}

func ReadServices(ctx context.Context, read SystemRead) ([]ServiceUnit, bool, error) {
	args := []string{"list-units", "--type=service", "--all", "--no-pager", "--plain", "--no-legend", "--full"}
	text, _, err := read(ctx, systemctl(append(slices.Clone(args), "--output=json")...))
	var rows []ServiceUnit
	if err == nil {
		rows, err = ParseServiceUnits(text, true)
	}
	if err != nil {
		plain, _, plainErr := read(ctx, systemctl(args...))
		if plainErr != nil {
			legacy, detail, legacyErr := read(ctx, `export LC_ALL=C
if command -v rc-status >/dev/null 2>&1; then
    exec 'rc-status' '--all'
fi
if command -v service >/dev/null 2>&1; then
    exec 'service' '--status-all'
fi
exit 127`)
			return parseLegacyServices(legacy), false, systemReadError(detail, legacyErr)
		}
		rows, _ = ParseServiceUnits(plain, false)
	}
	files, detail, fileErr := read(ctx, systemctl("list-unit-files", "--type=service", "--no-pager", "--no-legend", "--full"))
	byName := make(map[string]int, len(rows))
	var loaded []string
	for index := range rows {
		row := &rows[index]
		row.Memory = -1
		byName[row.Name] = index
		if row.Active == "active" || row.Active == "reloading" {
			loaded = append(loaded, row.Name)
		}
	}
	for _, file := range ParseServiceFiles(files) {
		if index, ok := byName[file.Name]; ok {
			rows[index].Enabled = file.Enabled
		} else {
			byName[file.Name] = len(rows)
			file.Load, file.Active, file.Sub, file.Memory = "-", "inactive", "dead", -1
			rows = append(rows, file)
		}
	}
	for names := range slices.Chunk(loaded, 64) {
		command, _ := ServiceReadCommand("show", names...)
		output, _, err := read(ctx, command)
		if err == nil {
			for name, props := range ParseServiceProperties(output) {
				if index, ok := byName[name]; ok {
					rows[index].PID, rows[index].Memory, rows[index].Entered, rows[index].CanReload = props.PID, props.Memory, props.Entered, props.CanReload
				}
			}
		}
	}
	return rows, true, systemReadError(detail, fileErr)
}

func parseLegacyServices(text string) []ServiceUnit {
	var rows []ServiceUnit
	for line := range strings.SplitSeq(text, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		row := ServiceUnit{Memory: -1}
		switch {
		case len(fields) == 4 && fields[0] == "[" && fields[2] == "]":
			row.Name = fields[3]
			row.Sub = map[string]string{"+": "running", "-": "dead", "?": "unknown"}[fields[1]]
		case fields[1] == "[" && fields[len(fields)-1] == "]":
			row.Name, row.Sub = fields[0], fields[2]
			if row.Sub == "started" {
				row.Sub = "running"
			}
		default:
			continue
		}
		rows = append(rows, row)
	}
	return rows
}

func ServiceReadCommand(action string, names ...string) (string, error) {
	var args []string
	switch action {
	case "status":
		args = []string{"status", "--no-pager", "-l", "--lines=0"}
	case "cat":
		args = []string{"cat", "--no-pager"}
	case "show":
		args = []string{"show", "--no-pager", "-p", "Id,MemoryCurrent,MainPID,ActiveEnterTimestamp,CanReload,LoadState,ActiveState,SubState,UnitFileState,FragmentPath"}
	default:
		return "", errors.New("invalid service read")
	}
	return serviceCommand(args, names)
}

func ServiceActionCommand(action string, names ...string) (string, error) {
	var args []string
	switch action {
	case "start", "stop", "restart", "reload", "enable", "disable", "mask", "unmask", "reset-failed":
		args = []string{action}
	case "enable-now":
		args = []string{"enable", "--now"}
	default:
		return "", errors.New("invalid service action")
	}
	return serviceCommand(append(args, "--no-ask-password"), names)
}

func serviceCommand(args, names []string) (string, error) {
	if len(names) == 0 {
		return "", errors.New("no service selected")
	}
	for _, name := range names {
		if !ValidService(name) {
			return "", errors.New("invalid service name")
		}
	}
	return systemctl(append(append(args, "--"), names...)...), nil
}

func ServiceOverridePath(unit string) (string, error) {
	if !ValidService(unit) {
		return "", errors.New("invalid service name")
	}
	return "/etc/systemd/system/" + unit + ".d/override.conf", nil
}

func systemReadError(detail string, err error) error {
	if err == nil || strings.TrimSpace(detail) == "" {
		return err
	}
	return fmt.Errorf("%w: %s", err, strings.TrimSpace(detail))
}
