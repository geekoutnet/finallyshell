package monitor

import (
	"strings"
	"testing"
)

func TestOpsDiskParsers(t *testing.T) {
	result := ParseDiskScan("12\t/home/test/a b\x0096\t/home/test\x00", "/home/test", false)
	if result.Total != 96*1024 || len(result.Entries) != 1 || result.Entries[0].Path != "/home/test/a b" {
		t.Fatalf("scan: %+v", result)
	}
	result = ParseDiskScan("12\t/home/test/a'b\n96\t/home/test\n", "/home/test", false)
	if len(result.Entries) != 1 || result.Entries[0].Bytes != 12*1024 {
		t.Fatalf("busybox scan: %+v", result)
	}
	result = ParseDiskScan("90000000\t1721000000.25\t/home/test/a\nb\x00", "/home/test", true)
	if len(result.Entries) != 1 || result.Entries[0].Modified.Unix() != 1721000000 {
		t.Fatalf("find: %+v", result)
	}
	result = ParseDiskScan("F\t/home/test/app.log\x009840\t1721000000\t/home/test/app.log\x0011500\t1721000000\t/home/test\x00", "/home/test", false)
	if len(result.Entries) != 1 || !result.Entries[0].File || result.Entries[0].Modified.IsZero() {
		t.Fatalf("regular log: %+v", result)
	}
}

func TestOpsDiskCommands(t *testing.T) {
	for _, path := range []string{"/", "/etc", "/usr/cache", "/lib64", "/dev/shm", "/var/lib/docker/volumes", "/home", "/home/test/../other", "/home/test\nother"} {
		if _, err := DiskDeleteScript([]string{path}); err == nil {
			t.Errorf("allowed %q", path)
		}
	}
	script, err := DiskDeleteScript([]string{"/home/test/a'b"})
	if err != nil || !strings.Contains(script, `'/home/test/a'\''b'`) {
		t.Fatalf("delete: %q, %v", script, err)
	}
	if _, err := DiskTruncateScript("/home/test/archive.gz"); err == nil {
		t.Fatal("truncates non-log file")
	}
	script, err = DiskTruncateScript("/home/test/service.log")
	if err != nil || !strings.Contains(script, `backup="$file.bak-$stamp"`) || strings.Index(script, "'cp'") > strings.LastIndex(script, `: > "$file"`) {
		t.Fatalf("truncate without backup: %q, %v", script, err)
	}
	_, targets := ParseDiskCleanupEstimates("1024\t/var/log/syslog.1\n2048\t/var/log/web old.gz\n7\t/var/log/syslog\n")
	script, err = DiskRotatedScript(targets)
	if err != nil || len(targets) != 2 || !strings.Contains(script, `set -- '/var/log/syslog.1' '/var/log/web old.gz'`) {
		t.Fatalf("rotated logs: %v, %q, %v", targets, script, err)
	}
}
