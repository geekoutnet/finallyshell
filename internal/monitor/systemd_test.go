package monitor

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestSystemdParsers(t *testing.T) {
	const jsonText = `[{"unit":"queue@worker\u005cx2dblue.service","load":"loaded","active":"active","sub":"running","description":"Queue worker"}]`
	const plain = `● queue@worker\x2dblue.service loaded active running Queue worker`
	const name = `queue@worker\x2dblue.service`
	want := []ServiceUnit{{Name: name, Description: "Queue worker", Load: "loaded", Active: "active", Sub: "running"}}
	for _, sample := range []struct {
		text string
		json bool
	}{{jsonText, true}, {plain, false}} {
		got, err := ParseServiceUnits(sample.text, sample.json)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("units = %#v, %v", got, err)
		}
	}
	files := ParseServiceFiles("queue.service enabled enabled\nqueue@.service disabled enabled\nlegacy.service static -\nloop.service masked -\n")
	if len(files) != 4 || files[0].Enabled != "enabled" || files[1].Name != "queue@.service" || files[2].Enabled != "static" || files[3].Enabled != "masked" {
		t.Fatalf("unit files = %#v", files)
	}
	props := ParseServiceProperties("MainPID=410\r\nMemoryCurrent=4096\r\nId=" + name + "\r\nCanReload=yes\r\nActiveEnterTimestamp=Fri 2026-01-02 03:04:05 UTC\r\n\r\nId=legacy.service\r\nMemoryCurrent=[not set]\r\n")
	if got := props[name]; got.PID != 410 || got.Memory != 4096 || !got.CanReload || got.Entered == "" || props["legacy.service"].Memory != -1 {
		t.Fatalf("properties = %#v", props)
	}
	status := ParseServiceStatus("● queue.service - Queue\n Loaded: loaded (/usr/lib/systemd/system/queue.service; enabled)\n Active: active (running) since Fri 2026-01-02 03:04:05 UTC\n Main PID: 410 (queue)\n Memory: 4.0M\n")
	if !strings.HasPrefix(status.Loaded, "loaded (") || !strings.HasPrefix(status.Active, "active (running)") || status.MainPID != "410 (queue)" || status.Memory != "4.0M" {
		t.Fatalf("status = %#v", status)
	}
}

func TestSystemdReadFallback(t *testing.T) {
	read := func(_ context.Context, script string) (string, string, error) {
		switch {
		case strings.Contains(script, "'--output=json'"):
			return "", "unknown output", errors.New("exit")
		case strings.Contains(script, "'list-units'"):
			return "work.service loaded active running Worker\n", "", nil
		case strings.Contains(script, "'list-unit-files'"):
			return "work.service enabled\nspare.service disabled\n", "", nil
		case strings.Contains(script, "'show'"):
			return "Id=work.service\nMainPID=11\nMemoryCurrent=321\nCanReload=yes\n", "", nil
		}
		return "", "", errors.New("unexpected command")
	}
	rows, systemd, err := ReadServices(t.Context(), read)
	if err != nil || !systemd || len(rows) != 2 || !rows[0].CanReload || rows[0].PID != 11 || rows[1].Active != "inactive" {
		t.Fatalf("services = %#v %v %v", rows, systemd, err)
	}
	rows, systemd, err = ReadServices(t.Context(), func(_ context.Context, script string) (string, string, error) {
		if strings.Contains(script, "rc-status") {
			return "Runlevel: default\n sshd [ started ]\n cache [ stopped ]\n", "", nil
		}
		return "", "unavailable", errors.New("exit")
	})
	if err != nil || systemd || len(rows) != 2 || rows[0].Sub != "running" || rows[1].Sub != "stopped" {
		t.Fatalf("legacy = %#v %v %v", rows, systemd, err)
	}
}

func TestSystemdCommands(t *testing.T) {
	const prefix = `'env' 'LC_ALL=C' 'SYSTEMD_COLORS=0' 'SYSTEMD_PAGER=cat' 'systemctl' `
	for _, sample := range []struct {
		action string
		units  []string
		want   string
	}{
		{"restart", []string{`cache@worker\x2dblue.service`, "queue.service"}, prefix + `'restart' '--no-ask-password' '--' 'cache@worker\x2dblue.service' 'queue.service'`},
		{"enable-now", []string{"queue.service"}, prefix + `'enable' '--now' '--no-ask-password' '--' 'queue.service'`},
		{"stop", []string{"--all.service"}, ""},
		{"start", []string{"*.service"}, ""},
		{"start", []string{"bad';exit.service"}, ""},
		{"start", []string{`bad\xZZ.service`}, ""},
		{"daemon-reload", []string{"good.service"}, ""},
	} {
		got, err := ServiceActionCommand(sample.action, sample.units...)
		if got != sample.want || (err != nil) != (sample.want == "") {
			t.Errorf("%s %q: %q, %v", sample.action, sample.units, got, err)
		}
	}
	file, _ := ServiceOverridePath("queue.service")
	write, err := SystemFileWriteCommand(file, "[Service]\n", true)
	if err != nil || !strings.Contains(write, `'ln' "$backup" "$file.bak-$stamp"`) || !strings.Contains(write, prefix+`'daemon-reload'`) || strings.Contains(write, "[Service]") {
		t.Fatalf("write = %q, %v", write, err)
	}
	for _, invalid := range []string{"/etc/passwd", "/etc/cron.d/../passwd", "/etc/systemd/system/../override.conf"} {
		if _, err := SystemFileWriteCommand(invalid, "", true); err == nil {
			t.Errorf("accepted %q", invalid)
		}
	}
}
