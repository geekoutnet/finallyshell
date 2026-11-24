package monitor

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestDockerCtrRun(t *testing.T) {
	cmd := DockerCmd{Tool: "docker", Sudo: true}
	form := CtrConfig{Image: "example/app:1", Name: "example", Pull: true, Env: []string{"TOKEN=a'b"},
		Command: []string{"sh", "-c", "echo 'hi'"}, Ports: []CtrPort{{"::1", "8080", "80", "tcp"}}}
	script, err := cmd.CtrRunScript(form)
	want := scriptHead + "sudo -n docker 'run' '-d' '--name' 'example' '--pull' 'missing' '--publish' '[::1]:8080:80/tcp' '--env' 'TOKEN=a'\\''b' '--' 'example/app:1' 'sh' '-c' 'echo '\\''hi'\\'''\n"
	if err != nil || script != want {
		t.Fatalf("script: %q, %v; want %q", script, err, want)
	}
	preview, err := cmd.CtrRunPreview(form)
	if err != nil || !strings.Contains(preview, "TOKEN=••••") || strings.Contains(preview, "a'\\''b") {
		t.Fatalf("unsafe preview: %q, %v", preview, err)
	}
	for name, config := range map[string]CtrConfig{
		"image": {Image: "--help", Name: "valid"}, "name": {Image: "valid", Name: "a;b"},
		"port":              {Image: "valid", Name: "valid", Ports: []CtrPort{{"", "65536", "80", "tcp"}}},
		"path":              {Image: "valid", Name: "valid", WorkDir: "relative"},
		"env without value": {Image: "valid", Name: "valid", Env: []string{"HOME"}},
		"ip":                {Image: "valid", Name: "valid", Network: "custom", IPv4: "::1"},
		"compose label":     {Image: "valid", Name: "valid", Labels: []string{"com.docker.compose.project=project"}},
	} {
		t.Run(name, func(t *testing.T) {
			if line, err := cmd.CtrRunScript(config); err == nil || line != "" {
				t.Fatalf("accepted invalid form: %q, %v", line, err)
			}
		})
	}
}

func TestDockerCtrEditPreservesInspect(t *testing.T) {
	ct := Container{ID: strings.Repeat("a", 64), Name: "svc", Inspect: json.RawMessage(`{
  "Id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
  "Image":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
  "Config":{"Image":"example/app:1","Labels":{"keep":"yes"},"Healthcheck":{"Test":["CMD","true"]}},
  "HostConfig":{"NetworkMode":"main","SecurityOpt":["no-new-privileges"],"Tmpfs":{"/run":"rw"}},
  "NetworkSettings":{"Networks":{"main":{"IPAMConfig":{"IPv4Address":"192.0.2.8"}},"aux":{"Aliases":["worker"]}}},
  "Mounts":[{"Type":"volume","Name":"anonymous","Destination":"/data"}],"State":{"Running":true}
}`)}
	form, err := CtrConfigOf(&ct)
	if err != nil {
		t.Fatal(err)
	}
	form.Memory = "64m"
	script, err := (DockerCmd{}).CtrEditScript(&ct, form, time.Unix(0, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	checkSyntax(t, script)
	for _, want := range []string{`"Healthcheck":`, `"Labels":{"keep":"yes"}`, `"SecurityOpt":["no-new-privileges"]`,
		`"Tmpfs":{"/run":"rw"}`, `"Source":"anonymous"`, `"Memory":67108864`, `/networks/aux/connect`, `192.0.2.8`,
		`svc-old-19700101-000000`, `'stop'`, `'rename'`, `'start' "$new"`, `rollback`, `"Image":"sha256:`,
		`{{.Name}} {{.State.Status}} {{json .Config}} {{json .HostConfig}}`, `network' 'disconnect' 'main'`, `detached0=1`} {
		if !strings.Contains(script, want) {
			t.Errorf("script missing %q", want)
		}
	}
	ct.Project = "compose"
	if _, err := (DockerCmd{}).CtrEditScript(&ct, form, time.Now()); err == nil {
		t.Fatal("allowed edit of Compose-managed container")
	}
}

func TestDockerCtrFilesAndStats(t *testing.T) {
	cmd := DockerCmd{Tool: "podman", Sudo: true}
	for action, want := range map[CtrFileOp]string{
		CtrFileToServer:   `sudo -n podman 'cp' '--' 'box:/file'\''s' '/staging/a'`,
		CtrFileFromServer: `sudo -n podman 'cp' '--' '/staging/a' 'box:/file'\''s'`,
		CtrFileRename:     `sudo -n podman 'exec' '-e' 'LC_ALL=C' 'box' 'mv' '-nT' '--' '/file'\''s' '/staging/a'`,
	} {
		script, err := cmd.CtrFileScript(action, "box", "/file's", "/staging/a")
		if err != nil || !strings.HasSuffix(script, want+"\n") || action != CtrFileRename && !strings.Contains(script, `! -L "$1"`) {
			t.Errorf("%s: %q, %v, want %q", action, script, err, want)
		}
	}
	if _, err := cmd.CtrFileScript(CtrFileDelete, "box", "/tmp/..", ""); err == nil {
		t.Fatal("accepted an aliased root path")
	}
	listing := `total 12
drwxr-xr-x 2 root root 4096 1723161600 "."
-rw-r--r-- 1 root root 123 1723161600 "two words.txt"
lrwxrwxrwx 1 root root 5 1723161600 "shortcut" -> "target"
-rw-r--r-- 1 root root 24 Aug  9 12:14 BusyBox file
`
	files, err := ParseCtrFiles(listing)
	if err != nil || len(files) != 3 || files[0].Name != "two words.txt" || files[1].Name != "shortcut" || files[2].Name != "BusyBox file" {
		t.Fatalf("files: %+v, %v", files, err)
	}
	stats := ParseCtrStats([]Container{{ID: "abcdef1234567890", Name: "box"}}, `{"ID":"abcdef123456","CPUPerc":"7.5%","MemUsage":"12MiB / 32MiB","NetIO":"1kB / 2kB","BlockIO":"0B / 8kB","PIDs":"2"}`)
	if sample := stats["abcdef1234567890"]; sample.CPU != 7.5 || sample.MemUsed != 12<<20 || sample.NetTx != 2000 || sample.BlockWrite != 8000 || sample.PIDs != 2 {
		t.Fatalf("stats: %+v", sample)
	}
	stats = ParseCtrStats([]Container{{ID: "abcdef1234567890", Name: "box"}}, `{"ID":"abcdef123456","CPU":"5.25%","MemUsage":"2.1MB / 5MB","NetIO":"800B / 9kB","PIDS":4}`)
	if sample := stats["abcdef1234567890"]; sample.CPU != 5.25 || sample.NetRx != 800 || sample.PIDs != 4 {
		t.Fatalf("Podman stats: %+v", sample)
	}
	for _, filename := range []string{`"link -> unusual"`, `"with\nnewline"`, `"with \"quotes\""`} {
		rows, err := ParseCtrFiles(fmt.Sprintf("lrwxrwxrwx 1 0 0 2 1723161600 %s -> \"/tmp\"\n", filename))
		if err != nil || len(rows) != 1 {
			t.Fatalf("could not parse %q: %v", filename, err)
		}
	}
	if _, err := ParseCtrFiles("-rw------- 1 0 0 7 Aug  9 12:14 ambiguous?file"); err == nil {
		t.Fatal("accepted ambiguous BusyBox file name")
	}
}

func TestDockerCtrExportAndCommit(t *testing.T) {
	for _, cmd := range []DockerCmd{{}, {Tool: "docker", Sudo: true}, {Tool: "podman"}} {
		script, err := cmd.CtrExportScript("example", "/tmp/box's backup.tar")
		if err != nil || !strings.Contains(script, `dest='/tmp/box'\''s backup.tar'`) ||
			!strings.Contains(script, `ln -T -- "$tmp" "$dest"`) || !strings.Contains(script, cmd.Line("export")) {
			t.Fatalf("export: %q, %v", script, err)
		}
		checkSyntax(t, script)
		line, err := cmd.CtrCommitScript("example", "org/app:snapshot", "It's ready", "Dev", true)
		want := scriptHead + cmd.prefix() + ` 'commit' '--pause=true' '--message' 'It'\''s ready' '--author' 'Dev' '--' 'example' 'org/app:snapshot'` + "\n"
		if err != nil || line != want {
			t.Fatalf("commit: %q, %v", line, err)
		}
		if _, err := cmd.CtrExportScript("example", "/tmp/.."); err == nil {
			t.Fatal("accepted an aliased root path")
		}
	}
}
