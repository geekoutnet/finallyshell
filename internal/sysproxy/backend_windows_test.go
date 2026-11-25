package sysproxy

import (
	"reflect"
	"testing"

	"golang.org/x/sys/windows/registry"
)

type registryFixture struct {
	enable *uint32
	values map[string]windowsText
}

func (key *registryFixture) GetIntegerValue(string) (uint64, uint32, error) {
	if key.enable == nil {
		return 0, 0, registry.ErrNotExist
	}
	return uint64(*key.enable), registry.DWORD, nil
}

func (key *registryFixture) GetStringValue(name string) (string, uint32, error) {
	value, ok := key.values[name]
	if !ok {
		return "", 0, registry.ErrNotExist
	}
	return value.Value, value.Type, nil
}

func (key *registryFixture) SetDWordValue(_ string, value uint32) error {
	key.enable = &value
	return nil
}

func (key *registryFixture) SetStringValue(name, value string) error {
	key.values[name] = windowsText{value, registry.SZ}
	return nil
}

func (key *registryFixture) SetExpandStringValue(name, value string) error {
	key.values[name] = windowsText{value, registry.EXPAND_SZ}
	return nil
}

func (key *registryFixture) DeleteValue(name string) error {
	if name == "ProxyEnable" {
		key.enable = nil
	} else {
		delete(key.values, name)
	}
	return nil
}

func (key *registryFixture) Close() error { return nil }

func TestSystemProxyWindowsBackend(t *testing.T) {
	key := &registryFixture{values: map[string]windowsText{"ProxyOverride": {"<local>;10.*", registry.EXPAND_SZ}}}
	var accessed []uint32
	notifications := 0
	backend := windowsBackend{
		func(access uint32) (proxyRegistry, error) { accessed = append(accessed, access); return key, nil },
		func() error { notifications++; return nil },
	}
	backup, err := backend.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if err := backend.Set(Settings{Address: "127.0.0.1:3128"}, backup); err != nil || key.enable == nil || *key.enable != 1 ||
		key.values["ProxyServer"].Value != "http=127.0.0.1:3128;https=127.0.0.1:3128" || key.values["ProxyOverride"].Value != "<local>;localhost;127.*;[::1]" {
		t.Fatalf("manual: %+v %v", key.values, err)
	}
	if err := backend.Set(Settings{Address: "127.0.0.1:1080", SOCKS: true}, backup); err != nil || key.values["ProxyServer"].Value != "socks=127.0.0.1:1080" {
		t.Fatalf("SOCKS: %+v %v", key.values, err)
	}
	const pac = "http://127.0.0.1:9876/proxy.pac"
	if err := backend.Set(Settings{Address: "127.0.0.1:1080", PACURL: pac}, backup); err != nil || *key.enable != 0 || len(key.values) != 1 || key.values["AutoConfigURL"].Value != pac {
		t.Fatalf("PAC: %+v %v", key.values, err)
	}
	if err := backend.Restore(backup); err != nil || key.enable != nil || !reflect.DeepEqual(key.values, map[string]windowsText{"ProxyOverride": {"<local>;10.*", registry.EXPAND_SZ}}) {
		t.Fatalf("restore: %+v %v", key.values, err)
	}
	if notifications != 4 || !reflect.DeepEqual(accessed, []uint32{registry.QUERY_VALUE, registry.SET_VALUE, registry.SET_VALUE, registry.SET_VALUE, registry.SET_VALUE}) {
		t.Fatalf("access %v, notifications %d", accessed, notifications)
	}
}
