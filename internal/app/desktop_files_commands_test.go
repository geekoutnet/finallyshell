package app

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestDesktopFilesCommands(t *testing.T) {
	for _, target := range []string{"relative", "/a/../b", "/a\nb", "/a\rb", "/a\x00b"} {
		if desktopFilesPath(target) {
			t.Errorf("accepted %q", target)
		}
	}
	for _, raw := range []string{"file:///etc/passwd", "https://user:password@example.org", "https:///missing-host", "https://example.org/\n"} {
		if desktopFilesURL(raw) {
			t.Errorf("accepted URL %q", raw)
		}
	}
	if !desktopFilesURL("https://example.org/a'b?q=1&n=2") || !desktopFilesURL("ftp://example.org/data") {
		t.Fatal("rejected supported URLs")
	}
	if got := desktopFilesCopyCommand("/home/it's a file", "/tmp/a", true, false); got != `'mv' '-nT' '--' '/home/it'"'"'s a file' '/tmp/a'` {
		t.Errorf("move = %s", got)
	}
	if got := desktopFilesModeCommand("/opt/site", "0755", true); got != `'find' '-P' '/opt/site' '!' '-type' 'l' '-exec' 'chmod' '--' '0755' '{}' '+'` {
		t.Errorf("chmod = %s", got)
	}
	if got := desktopFilesSearchCommand("/opt/site", "a[b]*"); !strings.Contains(got, `'-iname' '*a\[b\]\**'`) {
		t.Errorf("find = %s", got)
	}
}

func TestDesktopFilesArchiveAndDownloadCommands(t *testing.T) {
	for _, tc := range []struct{ command, tail string }{
		{desktopFilesCompressCommand(".tar.gz", "/srv/site", "/srv/site/.stage/a.tar.gz", []string{"a'b", "-file"}), `exec 'tar' '-czvf' '/srv/site/.stage/a.tar.gz' '-C' '/srv/site' '--' 'a'"'"'b' '-file'`},
		{desktopFilesCompressCommand(".zip", "/srv/site", "/srv/site/.stage/a.zip", []string{"a'b"}), `exec 'zip' '-ry' '/srv/site/.stage/a.zip' '--' 'a'"'"'b'`},
		{desktopFilesExtractCommand("/srv/site/a.zip", "/srv/site/.stage"), `exec 'unzip' '-n' '/srv/site/a.zip' '-d' '/srv/site/.stage'`},
		{desktopFilesExtractCommand("/srv/site/a.tgz", "/srv/site/.stage"), `exec 'tar' '--no-same-owner' '--no-same-permissions' '--keep-old-files' '-xvf' '/srv/site/a.tgz' '-C' '/srv/site/.stage'`},
		{desktopFilesExtractCommand("/srv/site/a.gz", "/srv/site/.stage"), `exec 'gzip' '-dc' '--' '/srv/site/a.gz' > '/srv/site/.stage/a'`},
		{desktopFilesOwnerCommand("/srv/a'b", "1000:1001", true), `'chown' '-h' '-R' '-P' '--' '1000:1001' '/srv/a'"'"'b'`},
	} {
		if !strings.HasSuffix(tc.command, tc.tail) {
			t.Errorf("command %q does not end in %q", tc.command, tc.tail)
		}
	}
	command := desktopFilesFetchCommand("https://example.org/a'b?q=1&n=2", "/srv/site/.stage/a file")
	for _, want := range []string{`'--proto-redir' '=http,https,ftp'`, `'--output' '/srv/site/.stage/a file' '--' 'https://example.org/a'"'"'b?q=1&n=2'`, `exec 'wget' '--no-config' '--max-redirect=10' '--progress=dot:mega' '-O' '/srv/site/.stage/a file' '--' 'https://example.org/a'"'"'b?q=1&n=2'`} {
		if !strings.Contains(command, want) {
			t.Errorf("download command %q does not contain %q", command, want)
		}
	}
}

func TestDesktopFilesArchiveInventory(t *testing.T) {
	for _, tc := range []struct {
		name, listing, stderr string
		failed                bool
	}{
		{name: "normal.tar", listing: "drwxr-xr-x root/root         0 2026-11-25 12:30 folder/\n-rw-r--r-- root/root         4 2026-11-25 12:30 folder/file\n"},
		{name: "normal.zip", listing: "Archive:  normal.zip\nZip file size: 123 bytes, number of entries: 1\n-rw-r--r--  3.0 unx        4 bx        4 stor 26-Nov-25 12:00 file\n1 file, 4 bytes uncompressed, 4 bytes compressed:  0.0%\n"},
		{name: "special.tar", listing: "lrwxrwxrwx root/root         0 2026-11-25 12:30 link -> target\n", failed: true},
		{name: "missing.zip", stderr: "desktop-files-missing:unzip\n", failed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			run := func(_ context.Context, _ string, output func(string)) (string, string, error) {
				for offset := 0; offset < len(tc.listing); offset += 7 {
					output(tc.listing[offset:min(offset+7, len(tc.listing))])
				}
				if tc.stderr != "" {
					return "", tc.stderr, errors.New("fixture tool unavailable")
				}
				return "", "", nil
			}
			if err := desktopFilesValidateArchive(t.Context(), run, "/srv/archive/"+tc.name); (err != nil) != tc.failed {
				t.Fatalf("inventory error = %v, want failed %v", err, tc.failed)
			}
		})
	}
}

func TestDesktopFilesMounts(t *testing.T) {
	fixture := "Filesystem Type 1024-blocks Used Available Capacity Mounted on\n/dev/sda1 ext4 1024 512 500 51% /\ntmpfs tmpfs 2048 0 2048 0% /run\n/dev/sdb1 xfs 8192 1 8191 1% /data with space\n/dev/bad xfs 12 bogus 2 80% /bad\n"
	drives := desktopFilesParseMounts(fixture)
	var paths []string
	for _, drive := range drives {
		paths = append(paths, drive.mount)
	}
	if !slices.Equal(paths, []string{"/", "/data with space"}) || drives[0].free != 500*1024 || drives[0].used != 51 {
		t.Fatalf("mounts = %+v", drives)
	}
}
