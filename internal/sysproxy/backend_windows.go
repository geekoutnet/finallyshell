package sysproxy

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const internetSettings = `Software\Microsoft\Windows\CurrentVersion\Internet Settings`

type proxyRegistry interface {
	GetIntegerValue(string) (uint64, uint32, error)
	GetStringValue(string) (string, uint32, error)
	SetDWordValue(string, uint32) error
	SetStringValue(string, string) error
	SetExpandStringValue(string, string) error
	DeleteValue(string) error
	Close() error
}

type windowsBackend struct {
	open   func(uint32) (proxyRegistry, error)
	notify func() error
}

func NativeBackend() Backend {
	return windowsBackend{func(access uint32) (proxyRegistry, error) {
		key, err := registry.OpenKey(registry.CURRENT_USER, internetSettings, access)
		return key, err
	}, notifyWinINet}
}

type windowsText struct {
	Value string
	Type  uint32
}

type windowsState struct {
	Version               int
	Enable                *uint32
	Server, Override, URL *windowsText
}

func (backend windowsBackend) Snapshot() (json.RawMessage, error) {
	key, err := backend.open(registry.QUERY_VALUE)
	if err != nil {
		return nil, err
	}
	defer key.Close()
	state := windowsState{Version: 1}
	if value, kind, err := key.GetIntegerValue("ProxyEnable"); err == nil {
		if kind != registry.DWORD {
			return nil, ErrBackup
		}
		enable := uint32(value)
		state.Enable = &enable
	} else if !errors.Is(err, registry.ErrNotExist) {
		return nil, err
	}
	for name, dest := range map[string]**windowsText{"ProxyServer": &state.Server, "ProxyOverride": &state.Override, "AutoConfigURL": &state.URL} {
		if value, kind, err := key.GetStringValue(name); err == nil {
			*dest = &windowsText{value, kind}
		} else if !errors.Is(err, registry.ErrNotExist) {
			return nil, err
		}
	}
	if state.Server != nil && strings.Contains(state.Server.Value, "@") || state.URL != nil && !safeBackupURL(state.URL.Value) {
		return nil, ErrUnsupported
	}
	return json.Marshal(state)
}

func (backend windowsBackend) Set(settings Settings, _ json.RawMessage) error {
	var enable uint32 = 1
	proxy := "http=" + settings.Address + ";https=" + settings.Address
	if settings.SOCKS {
		proxy = "socks=" + settings.Address
	}
	state := windowsState{Enable: &enable, Server: &windowsText{proxy, registry.SZ}, Override: &windowsText{"<local>;localhost;127.*;[::1]", registry.SZ}}
	if settings.PACURL != "" {
		enable = 0
		state.Server, state.Override = nil, nil
		state.URL = &windowsText{settings.PACURL, registry.SZ}
	}
	return backend.write(state)
}

func (backend windowsBackend) Restore(data json.RawMessage) error {
	var state *windowsState
	if err := json.Unmarshal(data, &state); err != nil || state == nil || state.Version != 1 {
		return ErrBackup
	}
	for _, value := range []*windowsText{state.Server, state.Override, state.URL} {
		if value != nil && value.Type != registry.SZ && value.Type != registry.EXPAND_SZ {
			return ErrBackup
		}
	}
	return backend.write(*state)
}

func (backend windowsBackend) write(state windowsState) error {
	key, err := backend.open(registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	remove := func(name string) error {
		if err := key.DeleteValue(name); !errors.Is(err, registry.ErrNotExist) {
			return err
		}
		return nil
	}
	var failures error
	for _, entry := range []struct {
		name  string
		value *windowsText
	}{
		{"ProxyServer", state.Server}, {"ProxyOverride", state.Override}, {"AutoConfigURL", state.URL},
	} {
		switch {
		case entry.value == nil:
			err = remove(entry.name)
		case entry.value.Type == registry.SZ:
			err = key.SetStringValue(entry.name, entry.value.Value)
		case entry.value.Type == registry.EXPAND_SZ:
			err = key.SetExpandStringValue(entry.name, entry.value.Value)
		default:
			err = ErrBackup
		}
		failures = errors.Join(failures, err)
	}
	if state.Enable == nil {
		err = remove("ProxyEnable")
	} else {
		err = key.SetDWordValue("ProxyEnable", *state.Enable)
	}
	return errors.Join(failures, err, backend.notify())
}

func notifyWinINet() error {
	proc := windows.NewLazySystemDLL("wininet.dll").NewProc("InternetSetOptionW")
	if err := proc.Find(); err != nil {
		return err
	}
	const settingsChanged, refresh = 39, 37
	var failures error
	for _, option := range []uintptr{settingsChanged, refresh} {
		if result, _, err := proc.Call(0, option, 0, 0); result == 0 {
			failures = errors.Join(failures, fmt.Errorf("WinINet option %d: %w", option, err))
		}
	}
	return failures
}
