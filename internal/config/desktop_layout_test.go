package config

import (
	"encoding/json"
	"testing"
)

// An older version knows hosts' layouts but not the shared layout or the
// mode used last: saving and syncing what it read keeps the shared layout,
// an entry of hosts like any other.
func TestDesktopSharedLayoutSurvivesOlderVersions(t *testing.T) {
	type oldHost struct {
		Icons           []DesktopIcon   `json:"icons,omitempty"`
		IconsConfigured bool            `json:"iconsConfigured,omitempty"`
		AutoArrange     bool            `json:"autoArrange,omitempty"`
		Folders         []DesktopFolder `json:"folders,omitempty"`
		Favorites       []string        `json:"favorites,omitempty"`
		Browser         string          `json:"browser,omitempty"`
	}
	type oldDesktop struct {
		Wallpaper string             `json:"wallpaper,omitempty"`
		Hosts     map[string]oldHost `json:"hosts,omitempty"`
	}
	shared := DesktopHost{
		Icons:           []DesktopIcon{{App: "folder:preset-apps", Col: -1, Row: -1}, {App: "srv-docker", Col: -1, Row: -1, Folder: "preset-apps"}},
		Folders:         []DesktopFolder{{ID: "preset-apps", Name: "Apps"}},
		IconsConfigured: true,
	}
	s := Settings{Desktop: &DesktopSettings{Hosts: map[string]DesktopHost{
		DesktopSharedLayout: shared,
		"h1":                {LastMode: "desktop", Favorites: []string{"/srv"}},
	}}}
	// Through sync, as this version sends it.
	synced := SyncedSettings(s)
	// Read and written back by the older version.
	var old oldDesktop
	if err := json.Unmarshal(synced["desktop"], &old); err != nil {
		t.Fatal(err)
	}
	back, err := json.Marshal(old)
	if err != nil {
		t.Fatal(err)
	}
	got := WithSyncedSettings(Settings{}, map[string]json.RawMessage{"desktop": back})
	if got.Desktop == nil {
		t.Fatal("desktop lost")
	}
	h, ok := got.Desktop.Hosts[DesktopSharedLayout]
	if !ok || !h.HasOwnLayout() || len(h.Icons) != 2 || len(h.Folders) != 1 || h.Icons[1].Folder != "preset-apps" {
		t.Fatalf("shared layout after an older version: %+v", h)
	}
	if h1 := got.Desktop.Hosts["h1"]; h1.HasOwnLayout() || len(h1.Favorites) != 1 {
		t.Fatalf("host after an older version: %+v", h1)
	}
	// This version's own round trip keeps the mode used last.
	if got := WithSyncedSettings(Settings{}, synced); got.Desktop.Hosts["h1"].LastMode != "desktop" {
		t.Fatal("sync lost the mode used last")
	}
}

// The entry mode of a host survives an older version, which keeps the
// keys it does not know.
func TestHostEntryModeKeptByOlderVersions(t *testing.T) {
	h := Host{ID: "a", Name: "web", Addr: "10.0.0.1", User: "root", EntryMode: EntryLast}
	data, err := json.Marshal(&h)
	if err != nil {
		t.Fatal(err)
	}
	var back Host
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if back.EntryMode != EntryLast {
		t.Fatalf("entry mode %q", back.EntryMode)
	}
	var plain Host
	if err := json.Unmarshal([]byte(`{"id":"b","name":"x","addr":"y","user":"root"}`), &plain); err != nil || plain.EntryMode != EntryTerminal {
		t.Fatalf("default entry mode %q, %v", plain.EntryMode, err)
	}
}
