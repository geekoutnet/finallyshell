package config

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAlertSettingsRoundTrip(t *testing.T) {
	t.Setenv("FINALLYSHELL_DIR", t.TempDir())
	global := AlertRules{CPU: 82, CPUMinutes: 3, Memory: 88, Services: true}
	custom := AlertRules{Unreachable: true, Disk: 90, Load: 1.5}
	settings := Settings{AlertRules: &map[string]AlertRules{"": global, "worker": custom, "archive": {}}}
	if err := SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	got := LoadSettings()
	if got.AlertsFor("other") != global || got.AlertsFor("worker") != custom || got.AlertsFor("archive") != (AlertRules{}) {
		t.Fatalf("settings changed: %#v", got.AlertRules)
	}
	data, err := json.Marshal(Settings{})
	if err != nil || strings.Contains(string(data), "alertRules") {
		t.Fatalf("empty rules should be omitted: %s (%v)", data, err)
	}
	// The rules are the user's, on every device.
	if _, synced := SyncedSettings(settings)["alertRules"]; !synced {
		t.Fatal("alert rules should sync")
	}
}
