package app

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"

	"finallyshell/internal/config"
	"finallyshell/internal/monitor"
	"finallyshell/locales"
)

type exposeFirewall struct {
	backend, zone string
	allowed       bool
	inactive      bool
	ufw           monitor.UFWState
}

func tunnelFirewallStatus(ctx context.Context, conn opsConnection, port int) (exposeFirewall, error) {
	out, _, err := conn.normal(ctx, monitor.FirewallDetectScript, nil)
	if err != nil {
		return exposeFirewall{}, err
	}
	backend, _, _ := strings.Cut(strings.TrimSpace(out), "\n")
	switch backend {
	case "ufw":
		out, _, err := conn.root(ctx, monitor.UFWReadScript, nil)
		state := monitor.ParseUFW(out)
		if err != nil || !state.Known {
			return exposeFirewall{}, err
		}
		for _, rule := range state.Rules {
			if rule.Action == "ALLOW IN" && (rule.From == "Anywhere" || rule.From == "Anywhere (v6)") && tunnelFirewallPort(rule.To, port) {
				return exposeFirewall{backend: backend, inactive: !state.Active, allowed: true, ufw: state}, nil
			}
		}
		return exposeFirewall{backend: backend, inactive: !state.Active, ufw: state}, nil
	case "firewall-cmd":
		out, _, err := conn.root(ctx, monitor.FirewallZonesScript, nil)
		if err != nil {
			return exposeFirewall{}, err
		}
		zones := monitor.ParseFirewallZones(out)
		zones = slices.DeleteFunc(zones, func(zone monitor.FirewallZone) bool { return !zone.Active })
		if len(zones) != 1 || zones[0].Sources != "" {
			return exposeFirewall{}, nil
		}
		zone := zones[0]
		return exposeFirewall{backend: backend, zone: zone.Name, allowed: slices.ContainsFunc(zone.Ports, func(value string) bool {
			return strings.HasSuffix(value, "/tcp") && tunnelFirewallPort(value, port)
		})}, nil
	}
	return exposeFirewall{}, nil
}

func tunnelFirewallPort(value string, port int) bool {
	value = strings.TrimSuffix(strings.TrimSpace(strings.TrimSuffix(value, "(v6)")), "/tcp")
	first, last, found := strings.Cut(strings.ReplaceAll(value, ":", "-"), "-")
	if !found {
		last = first
	}
	low, lowErr := strconv.Atoi(first)
	high, highErr := strconv.Atoi(last)
	return lowErr == nil && highErr == nil && low > 0 && high < 65536 && port >= low && port <= high
}

type tunnelFirewallRecord struct {
	HostID, Addr, User, TunnelID, Backend, Zone, Tag string
	SSHPort, Port                                    int
}

func (rec tunnelFirewallRecord) belongs(h *config.Host) bool {
	return rec.HostID == h.ID && rec.Addr == h.Addr && rec.User == h.User && rec.SSHPort == h.PortOr22()
}

func tunnelFirewallScript(rec tunnelFirewallRecord, before exposeFirewall, protected []int, add bool) (string, error) {
	port := strconv.Itoa(rec.Port)
	if _, valid := validPort(port); !valid || len(protected) == 0 || before.backend != rec.Backend || before.zone != rec.Zone || before.inactive {
		return "", monitor.ErrOpsInvalid
	}
	switch rec.Backend {
	case "ufw":
		if add && !before.allowed {
			return monitor.UFWAddScript("allow", port, "tcp", "", rec.Tag, protected)
		}
		if !add && rec.Tag != "" {
			var commands []string
			for _, rule := range slices.Backward(before.ufw.Rules) {
				if rule.Comment == rec.Tag && rule.Action == "ALLOW IN" && (rule.From == "Anywhere" || rule.From == "Anywhere (v6)") && tunnelFirewallPort(rule.To, rec.Port) {
					script, err := monitor.UFWDeleteScript(rule, protected)
					if err != nil {
						return "", err
					}
					commands = append(commands, script)
				}
			}
			if len(commands) != 0 {
				return strings.Join(commands, "\n"), nil
			}
		}
	case "firewall-cmd":
		if add != before.allowed {
			script, err := monitor.FirewallZoneChangeScript(rec.Zone, "port", port+"/tcp", add, protected)
			if err != nil {
				return "", err
			}
			if add {
				args := " '--zone=" + rec.Zone + "' '--query-port=" + port + "/tcp'"
				script = "set -e\nfor scope in 'runtime' 'permanent'; do\n  status=0\n  if [ \"$scope\" = 'runtime' ]; then 'firewall-cmd'" + args + " >/dev/null || status=$?\n  else 'firewall-cmd' '--permanent'" + args + " >/dev/null || status=$?; fi\n  [ \"$status\" = 1 ] || exit 1\ndone\n" + script
			}
			return script, nil
		}
	}
	return "", errors.New(locales.TexpFirewallChanged())
}

var tunnelFirewallMu sync.Mutex

func tunnelFirewallRecords() ([]tunnelFirewallRecord, error) {
	return readTunnelFirewallRecords(filepath.Join(config.Dir(), "tunnel-firewall.json"))
}

func readTunnelFirewallRecords(path string) ([]tunnelFirewallRecord, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var records []tunnelFirewallRecord
	err = json.Unmarshal(data, &records)
	return records, err
}

func keepTunnelFirewallRecord(record tunnelFirewallRecord, keep bool) error {
	tunnelFirewallMu.Lock()
	defer tunnelFirewallMu.Unlock()
	dir := config.Dir()
	path := filepath.Join(dir, "tunnel-firewall.json")
	records, err := readTunnelFirewallRecords(path)
	if err != nil {
		return err
	}
	records = slices.DeleteFunc(records, func(old tunnelFirewallRecord) bool { return old.Tag == record.Tag })
	if keep {
		records = append(records, record)
	}
	data, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, ".tunnel-firewall-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, err = file.Write(data)
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}
