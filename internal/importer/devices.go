package importer

import (
	"strconv"
	"strings"

	"finallyshell/internal/config"
)

func devicePort(host *config.Host, port int) {
	if port > 0 && port <= 65535 && port != host.DefaultPort() {
		host.Port = port
	}
}

func serialPortName(name string) string {
	name = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(name), ":"))
	if strings.HasPrefix(strings.ToUpper(name), "COM") {
		return strings.ToUpper(name)
	}
	return name
}

func serialSettings(baud, bits, parity, stop, flow string) *config.SerialConfig {
	settings := &config.SerialConfig{}
	if value, err := strconv.Atoi(baud); err == nil && value > 0 && value <= 100_000_000 {
		settings.Baud = value
	}
	if value, err := strconv.Atoi(bits); err == nil && value >= 5 && value <= 8 {
		settings.DataBits = value
	}
	switch strings.ToLower(parity) {
	case "odd", "o", "1":
		settings.Parity = "odd"
	case "even", "e", "2":
		settings.Parity = "even"
	}
	if value, err := strconv.ParseFloat(stop, 64); err == nil && (value == 1 || value == 1.5 || value == 2) {
		settings.StopBits = value
	}
	switch strings.ToLower(flow) {
	case "rts/cts", "rtscts", "hardware":
		settings.Flow = "rtscts"
	case "xon/xoff", "xonxoff", "software":
		settings.Flow = "xonxoff"
	}
	return settings
}
