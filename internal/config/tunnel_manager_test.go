package config

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestTunnelManagerSettings(t *testing.T) {
	t.Setenv("FINALLYSHELL_DIR", t.TempDir())
	if !LoadSettings().TunnelManager.Autostart() {
		t.Fatal("default should allow background autostart")
	}
	settings := Settings{TunnelManager: &TunnelManagerSettings{NoAutostart: true}}
	if err := SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	if LoadSettings().TunnelManager.Autostart() {
		t.Fatal("autostart opt-out was not saved")
	}
	encoded, err := json.Marshal(Settings{})
	if err != nil || bytes.Contains(encoded, []byte(`"tunnelManager"`)) {
		t.Fatalf("default should not write the setting: %s, %v", encoded, err)
	}
}
