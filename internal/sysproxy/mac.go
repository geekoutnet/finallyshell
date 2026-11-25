package sysproxy

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"unicode"
)

type macBackend struct{ run commandRunner }

var macProtocols = []string{"webproxy", "securewebproxy", "socksfirewallproxy"}

type macProxy struct {
	Enabled bool
	Host    string
	Port    string
}

type macService struct {
	Name          string
	Manual        [3]macProxy
	PACURL        string
	PAC, Discover bool
}

func (backend macBackend) network(args ...string) (string, error) {
	return backend.run("networksetup", args...)
}

func (backend macBackend) Snapshot() (json.RawMessage, error) {
	services, err := backend.services()
	if err != nil {
		return nil, err
	}
	return json.Marshal(services)
}

func (backend macBackend) services() ([]macService, error) {
	output, err := backend.network("-listallnetworkservices")
	if err != nil {
		return nil, err
	}
	lines := strings.Split(output, "\n")
	var services []macService
	for _, line := range lines[1:] {
		name := strings.TrimSpace(line)
		if name == "" || strings.HasPrefix(name, "*") {
			continue
		}
		state, err := backend.service(name)
		if err != nil {
			return nil, err
		}
		services = append(services, state)
	}
	if len(services) == 0 {
		return nil, ErrUnsupported
	}
	return services, nil
}

func (backend macBackend) service(name string) (macService, error) {
	state := macService{Name: name}
	for index, protocol := range macProtocols {
		output, err := backend.network("-get"+protocol, name)
		if err != nil {
			return macService{}, err
		}
		values := macValues(output)
		if values["Authenticated Proxy Enabled"] != "0" || !macYesNo(values["Enabled"]) {
			return macService{}, ErrUnsupported
		}
		state.Manual[index] = macProxy{values["Enabled"] == "Yes", values["Server"], values["Port"]}
	}
	output, err := backend.network("-getautoproxyurl", name)
	if err != nil {
		return macService{}, err
	}
	values := macValues(output)
	if !macYesNo(values["Enabled"]) {
		return macService{}, ErrUnsupported
	}
	state.PAC, state.PACURL = values["Enabled"] == "Yes", values["URL"]
	if state.PACURL == "(null)" {
		state.PACURL = ""
	}
	output, err = backend.network("-getproxyautodiscovery", name)
	if err != nil {
		return macService{}, err
	}
	discovery := macValues(output)["Auto Proxy Discovery"]
	if discovery != "On" && discovery != "Off" {
		return macService{}, ErrUnsupported
	}
	state.Discover = discovery == "On"
	return state, validateMacService(state)
}

func macYesNo(value string) bool { return value == "Yes" || value == "No" }

func macValues(output string) map[string]string {
	values := map[string]string{}
	for _, line := range strings.Split(output, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if ok {
			values[strings.TrimSpace(key)] = strings.TrimSpace(value)
		}
	}
	return values
}

func validateMacService(state macService) error {
	if state.Name == "" || strings.HasPrefix(state.Name, "-") || strings.ContainsFunc(state.Name, unicode.IsControl) || !safeBackupURL(state.PACURL) || state.PAC && state.PACURL == "" {
		return ErrBackup
	}
	for _, proxy := range state.Manual {
		if _, err := strconv.ParseUint(proxy.Port, 10, 16); err != nil || strings.ContainsAny(proxy.Host, "@/ ") || strings.ContainsFunc(proxy.Host, unicode.IsControl) || proxy.Enabled && (proxy.Host == "" || proxy.Port == "0") {
			return ErrBackup
		}
	}
	return nil
}

func (backend macBackend) Set(settings Settings, original json.RawMessage) error {
	var services []macService
	if err := json.Unmarshal(original, &services); err != nil || len(services) == 0 {
		return ErrBackup
	}
	host, port, err := proxyAddress(settings.Address)
	if err != nil {
		return err
	}
	for _, service := range services {
		if err := validateMacService(service); err != nil {
			return err
		}
		state := macService{Name: service.Name, PAC: settings.PACURL != "", PACURL: settings.PACURL}
		if !state.PAC {
			if settings.SOCKS {
				state.Manual[2] = macProxy{true, host, port}
			} else {
				state.Manual[0], state.Manual[1] = macProxy{true, host, port}, macProxy{true, host, port}
			}
		}
		if err := backend.write(state, false); err != nil {
			return err
		}
	}
	return nil
}

func (backend macBackend) Restore(data json.RawMessage) error {
	var states []macService
	if err := json.Unmarshal(data, &states); err != nil || len(states) == 0 {
		return ErrBackup
	}
	for _, state := range states {
		if err := validateMacService(state); err != nil {
			return err
		}
	}
	var failures error
	for _, state := range states {
		failures = errors.Join(failures, backend.write(state, true))
	}
	return failures
}

func (backend macBackend) write(state macService, restoring bool) error {
	var failures error
	call := func(args ...string) {
		if restoring || failures == nil {
			_, err := backend.network(args...)
			failures = errors.Join(failures, err)
		}
	}
	call("-setproxyautodiscovery", state.Name, macSwitch(state.Discover))
	if state.PACURL != "" {
		call("-setautoproxyurl", state.Name, state.PACURL)
	}
	call("-setautoproxystate", state.Name, macSwitch(state.PAC))
	for index, protocol := range macProtocols {
		proxy := state.Manual[index]
		if proxy.Host != "" && proxy.Port != "0" {
			call("-set"+protocol, state.Name, proxy.Host, proxy.Port, "off")
		}
		call("-set"+protocol+"state", state.Name, macSwitch(proxy.Enabled))
	}
	return failures
}

func macSwitch(on bool) string {
	if on {
		return "on"
	}
	return "off"
}
