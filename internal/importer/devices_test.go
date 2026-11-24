package importer

import (
	"os"
	"path/filepath"
	"testing"

	"finallyshell/internal/config"
)

func TestImportDevices(t *testing.T) {
	for _, tc := range []struct {
		name   string
		load   func() (*Source, error)
		port   int
		device string
		baud   int
	}{
		{"Xshell", func() (*Source, error) { return ReadXshell(xshellSample("Xshell", "Sessions")) }, 23, "COM1", 0},
		{"SecureCRT", func() (*Source, error) { return ReadSecureCRT(crtSample) }, 23, "COM5", 0},
		{"MobaXterm", func() (*Source, error) {
			path := filepath.Join(t.TempDir(), "devices.mxtsessions")
			if err := os.WriteFile(path, []byte("[Bookmarks]\nSubRep=Lab\nrouter=#98#1%console.example%2323%operator\nconsole=#131#8%COM4%115200%8%none%1%RTS/CTS\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			return ReadMobaXterm(path)
		}, 2323, "COM4", 115200},
		{"FinalShell", func() (*Source, error) {
			dir := t.TempDir()
			data := `[{"name":"router","host":"console.example","conection_type":"telnet","port":"2323","user_name":"operator"},` +
				`{"name":"console","protocol":"serial","serial_port":"COM6","baud_rate":57600,"data_bits":7,"parity":"even","stop_bits":2,"flow_control":"xon/xoff"}]`
			if err := os.WriteFile(filepath.Join(dir, "lab_connect_config.json"), []byte(data), 0o600); err != nil {
				t.Fatal(err)
			}
			return ReadFinalShell(dir)
		}, 2323, "COM6", 57600},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src, err := tc.load()
			if err != nil {
				t.Fatal(err)
			}
			router := byName(t, src, "router")
			if h := router.Host; h.Kind != config.KindTelnet || h.PortOr22() != tc.port || router.NeedsKey || router.Password != "" || h.Telnet != nil && h.Telnet.AutoLogin {
				t.Fatalf("bad Telnet import: %+v", h)
			}
			console := byName(t, src, "console")
			if h := console.Host; h.Kind != config.KindSerial || h.Addr != tc.device || h.Serial == nil || h.Serial.Baud != tc.baud || console.NeedsKey || console.Password != "" {
				t.Fatalf("bad serial import: %+v", h)
			}
		})
	}
	if SameTarget(&config.Host{Addr: "node.example", Port: 23}, &config.Host{Addr: "node.example", Kind: config.KindTelnet}) {
		t.Fatal("matched two different protocols")
	}
}
