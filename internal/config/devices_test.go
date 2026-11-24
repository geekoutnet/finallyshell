package config

import (
	"encoding/json"
	"testing"
)

func TestDeviceHostsRoundTrip(t *testing.T) {
	store, migrated, err := ParseHosts([]byte(`[{"id":"h1","name":"old","addr":"old.example","user":"operator","auth":"password"}]`))
	if err != nil || !migrated {
		t.Fatal(err)
	}
	if host := store.Live()[0]; !host.IsSSH() || host.PortOr22() != 22 || !host.MonitorEnabled() {
		t.Fatalf("legacy host: %+v", host)
	}
	telnet := &Host{Addr: "console.example", Kind: KindTelnet, Telnet: &TelnetConfig{AutoLogin: true, LoginPrompt: "operator:", KeepAlive: true}}
	serial := &Host{Addr: "COM9", Kind: KindSerial, Serial: &SerialConfig{Baud: 115200, DataBits: 7, Parity: "even", StopBits: 2, Flow: "rtscts", LineEnding: "crlf"}}
	for _, host := range []*Host{telnet, serial} {
		store.Add(host)
	}
	data, err := json.Marshal(store)
	if err != nil {
		t.Fatal(err)
	}
	var loaded Store
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatal(err)
	}
	network, device := loaded.Host(telnet.ID), loaded.Host(serial.ID)
	if network == nil || network.Kind != KindTelnet || network.PortOr22() != 23 || *network.Telnet != *telnet.Telnet || network.MonitorEnabled() {
		t.Fatalf("Telnet: %+v", network)
	}
	if device == nil || device.Kind != KindSerial || device.PortOr22() != 0 || *device.Serial != *serial.Serial || device.Target() != "COM9" || device.MonitorEnabled() {
		t.Fatalf("serial: %+v", device)
	}
	network.Clone().Telnet.AutoLogin = false
	device.Clone().Serial.Baud = 4800
	if !network.Telnet.AutoLogin || device.Serial.Baud != 115200 {
		t.Fatal("clones share device options")
	}
}
