package sysproxy

import (
	"encoding/json"
	"errors"
	"strings"
)

type gnomeBackend struct {
	run       commandRunner
	supported bool
}

type gnomeKey struct{ suffix, key string }

var gnomeKeys = []gnomeKey{
	{"", "mode"}, {"", "autoconfig-url"}, {"", "use-same-proxy"},
	{".http", "host"}, {".http", "port"}, {".http", "use-authentication"},
	{".https", "host"}, {".https", "port"}, {".socks", "host"}, {".socks", "port"},
	{".ftp", "host"}, {".ftp", "port"},
}

func (backend gnomeBackend) Snapshot() (json.RawMessage, error) {
	if !backend.supported {
		return nil, ErrUnsupported
	}
	values := make([]string, len(gnomeKeys))
	for index, entry := range gnomeKeys {
		value, err := backend.run("gsettings", "get", "org.gnome.system.proxy"+entry.suffix, entry.key)
		if err != nil {
			return nil, err
		}
		values[index] = strings.TrimSpace(value)
	}
	if !safeBackupURL(strings.Trim(values[1], "'\"")) {
		return nil, ErrUnsupported
	}
	for _, index := range []int{3, 6, 8, 10} {
		if strings.ContainsAny(values[index], "@/\r\n") {
			return nil, ErrUnsupported
		}
	}
	return json.Marshal(values)
}

func (backend gnomeBackend) Set(settings Settings, _ json.RawMessage) error {
	host, port, err := proxyAddress(settings.Address)
	if err != nil {
		return err
	}
	values := []string{"'manual'", "''", "false", "''", "0", "false", "''", "0", "''", "0", "''", "0"}
	if settings.PACURL != "" {
		values[0], values[1] = "'auto'", "'"+settings.PACURL+"'"
	} else if settings.SOCKS {
		values[8], values[9] = "'"+host+"'", port
	} else {
		values[3], values[4], values[6], values[7] = "'"+host+"'", port, "'"+host+"'", port
	}
	return backend.write(values, false)
}

func (backend gnomeBackend) Restore(data json.RawMessage) error {
	var values []string
	if err := json.Unmarshal(data, &values); err != nil || len(values) != len(gnomeKeys) {
		return ErrBackup
	}
	return backend.write(values, true)
}

func (backend gnomeBackend) write(values []string, restoring bool) error {
	if !backend.supported {
		return ErrUnsupported
	}
	var failures error
	for offset := 1; offset <= len(gnomeKeys); offset++ {
		index := offset % len(gnomeKeys)
		entry := gnomeKeys[index]
		_, err := backend.run("gsettings", "set", "org.gnome.system.proxy"+entry.suffix, entry.key, values[index])
		if err != nil && !restoring {
			return err
		}
		failures = errors.Join(failures, err)
	}
	return failures
}
