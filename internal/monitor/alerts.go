package monitor

import (
	"context"
	"strconv"
	"strings"
)

const alertReadStart = "__finallyshell_alert_read__"

const alertCoresScript = `command -v 'getconf' >/dev/null 2>&1 && 'getconf' '_NPROCESSORS_ONLN' || 'awk' '/^cpu[0-9]+ / { count++ } END { if (count) print count }' '/proc/stat'`

const alertServicesScript = `export LC_ALL=C SYSTEMD_COLORS=0 SYSTEMD_PAGER=cat
command -v 'systemctl' >/dev/null 2>&1 || exit 0
'systemctl' '--failed' '--type=service' '--no-legend' '--no-pager' '--plain' && 'printf' '\n%s\n' '` + alertReadStart + `'
`

func AlertCores(ctx context.Context, run MemoryRunner) int {
	stdout, _, err := run(ctx, alertCoresScript+"\n")
	if err != nil {
		return 0
	}
	cores, _ := strconv.Atoi(strings.TrimSpace(stdout))
	return max(cores, 0)
}

func AlertFailedServices(ctx context.Context, run MemoryRunner) (services []string, ok bool) {
	stdout, _, err := run(ctx, alertServicesScript)
	if err != nil {
		return nil, false
	}
	return parseAlertServices(stdout)
}

func parseAlertServices(output string) (services []string, ok bool) {
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 1 && fields[0] == alertReadStart {
			return services, true
		}
		if len(fields) > 0 && fields[0] == "●" {
			fields = fields[1:]
		}
		if len(fields) >= 4 && strings.HasSuffix(fields[0], ".service") && fields[2] == "failed" {
			services = append(services, fields[0])
		}
	}
	return nil, false
}
