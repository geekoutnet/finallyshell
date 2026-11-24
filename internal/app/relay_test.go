package app

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"net"
	"path"
	"strings"
	"testing"

	"github.com/pkg/sftp"
)

func TestRelayCommands(t *testing.T) {
	endpoint := relayEndpoint{"backup.example", "deploy", 2222}
	probe, err := endpoint.command("true")
	want := "'ssh' '-F' '/dev/null' '-o' 'BatchMode=yes' '-o' 'ConnectTimeout=5' '-o' 'StrictHostKeyChecking=yes' '-o' 'PreferredAuthentications=publickey' '-o' 'ForwardAgent=no' '-o' 'ClearAllForwardings=yes' '-o' 'UpdateHostKeys=no' '-p' '2222' '-l' 'deploy' '--' 'backup.example' 'true'"
	if err != nil || probe != want {
		t.Fatalf("probe = %q, %v", probe, err)
	}
	if got, err := relayTar("/some dir", []string{"-data", "it's OK"}, ""); err != nil || got != "'tar' '-C' '/some dir' '-cf' '-' './-data' './it'\"'\"'s OK'" {
		t.Fatalf("tar = %q, %v", got, err)
	}
	for _, invalid := range []relayEndpoint{{"-oProxyCommand=x", "alice", 22}, {"backup.example", "root@other", 22}, {"backup.example", "alice", 65536}} {
		if _, err := invalid.command("true"); err == nil {
			t.Fatalf("accepted %+v", invalid)
		}
	}
	for _, invalid := range []string{"..", "a/b", "a\nb", `a\..\b`, "file:stream", "file.", "file "} {
		if _, err := relayTar("/data", []string{invalid}, ""); err == nil {
			t.Fatalf("accepted name %q", invalid)
		}
	}
}

func TestRelayModeAndConflicts(t *testing.T) {
	ctx := context.Background()
	for _, probe := range []error{nil, errors.New("no public key")} {
		runs := 0
		run := func(context.Context, string) (string, string, error) { runs++; return "", "", probe }
		endpoint := relayEndpoint{"backup.example", "deploy", 22}
		if got := relayCanDirect(ctx, run, endpoint); got != (probe == nil) || runs != 1 {
			t.Fatalf("probe %v: direct=%v runs=%d", probe, got, runs)
		}
	}
	wanted := []string{"report.txt", "report (2).txt", "logs"}
	existing := map[string]bool{"report.txt": true, "logs": true}
	for _, test := range []struct {
		policy relayPolicy
		count  int
		first  string
		backup bool
	}{
		{relaySkip, 1, "report (2).txt", false},
		{relayRename, 3, "report (3).txt", false},
		{relayOverwrite, 3, "report.txt", true},
	} {
		got := relayPlan(wanted, existing, test.policy)
		if len(got) != test.count || got[0].dest != test.first || got[0].backup != test.backup {
			t.Fatalf("policy %v: %+v", test.policy, got)
		}
	}
}

func TestRelayRejectsOverlappingAliases(t *testing.T) {
	for _, test := range []struct {
		from, to, source, dest string
		want                   bool
	}{
		{"/srv", "/srv/logs/archive", "logs", "logs", true},
		{"/srv/logs", "/srv", "archive", "logs", true},
		{"/srv", "/srv", "logs", "logs", true},
		{"/srv", "/srv", "logs", "logs (2)", false},
		{"/srv", "/srv/logs-extra", "logs", "logs", false},
	} {
		if got := relayOverlaps(test.from, test.to, []relayItem{{source: test.source, dest: test.dest}}); got != test.want {
			t.Fatalf("overlap = %v for %+v", got, test)
		}
	}
}

func TestRelayStreamCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	stop := context.AfterFunc(ctx, func() { reader.CloseWithError(ctx.Err()) })
	defer stop()
	go func() { _, _ = writer.Write([]byte("first chunk")) }()
	var progress int64
	err := relayStream(ctx, io.Discard, reader, 100, func(bytes int64) { progress += bytes; cancel() })
	if !errors.Is(err, context.Canceled) || progress != 11 {
		t.Fatalf("progress = %d, err = %v", progress, err)
	}
}

type relayTestCommands struct{ sftp.FileCmder }

func (handler relayTestCommands) Filecmd(request *sftp.Request) error {
	if request.Method == "Setstat" && !request.AttrFlags().Size {
		return nil
	}
	return handler.FileCmder.Filecmd(request)
}

func relayTestFS(t *testing.T) relaySFTP {
	t.Helper()
	clientConn, serverConn := net.Pipe()
	handlers := sftp.InMemHandler()
	handlers.FileCmd = relayTestCommands{handlers.FileCmd}
	server := sftp.NewRequestServer(serverConn, handlers)
	t.Cleanup(func() { server.Close() })
	go func() { _ = server.Serve() }()
	client, err := sftp.NewClientPipe(clientConn, clientConn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	return relaySFTP{client}
}

func relayTestWrite(t *testing.T, client relaySFTP, name, content string) {
	t.Helper()
	writer, err := client.write(name)
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.WriteString(writer, content)
	if err = errors.Join(err, writer.Close()); err != nil {
		t.Fatal(err)
	}
}

func relayTestRead(t *testing.T, client relaySFTP, name string) string {
	t.Helper()
	reader, err := client.read(name)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	body, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func TestRelaySFTPStaging(t *testing.T) {
	for _, test := range []struct {
		name                 string
		policy               relayPolicy
		cancel, lateConflict bool
	}{
		{name: "skip", policy: relaySkip}, {name: "rename", policy: relayRename}, {name: "backup", policy: relayOverwrite},
		{name: "cancel", policy: relayOverwrite, cancel: true}, {name: "new conflict", policy: relayRename, lateConflict: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			source, target := relayTestFS(t), relayTestFS(t)
			for _, dir := range []string{"/data", "/data/logs", "/data/logs/empty"} {
				if err := source.Mkdir(dir); err != nil {
					t.Fatal(err)
				}
			}
			if err := target.Mkdir("/out"); err != nil {
				t.Fatal(err)
			}
			relayTestWrite(t, source, "/data/report.txt", "new report")
			relayTestWrite(t, source, "/data/logs/info.txt", strings.Repeat("log\n", 30_000))
			relayTestWrite(t, target, "/out/report.txt", "old report")
			previous, err := target.Lstat("/out/report.txt")
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var readings []relayProgress
			plan := relayPlan([]string{"report.txt", "logs"}, map[string]bool{"report.txt": true}, test.policy)
			task := relayTask{from: "/data", to: "/out", items: plan, mode: relayAuto, existing: map[string]fs.FileInfo{"report.txt": previous}}
			if test.lateConflict {
				relayTestWrite(t, target, "/out/report (2).txt", "created after review")
			}
			err = task.move(ctx, source, target, func(name string) error {
				return relayRemoveStage(context.Background(), target.Client, name)
			}, func(progress relayProgress) {
				readings = append(readings, progress)
				if test.cancel && progress.bytes > 0 {
					cancel()
				}
			})
			switch {
			case test.cancel:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("cancel = %v", err)
				}
			case test.lateConflict:
				if !errors.Is(err, errRelayChanged) || relayTestRead(t, target, "/out/report (2).txt") != "created after review" {
					t.Fatalf("late conflict = %v", err)
				}
			default:
				if err != nil {
					t.Fatal(err)
				}
				last := readings[len(readings)-1]
				if last.files != last.totalFiles || last.bytes != last.total || last.mode != relayViaApp {
					t.Fatalf("final progress = %+v", last)
				}
				for _, progress := range readings {
					if progress.bytes > progress.total || progress.files > progress.totalFiles {
						t.Fatalf("invalid progress: %+v", progress)
					}
				}
				if relayTestRead(t, target, "/out/logs/info.txt") != strings.Repeat("log\n", 30_000) {
					t.Fatal("log contents changed")
				}
				if empty, err := target.Lstat("/out/logs/empty"); err != nil || !empty.IsDir() {
					t.Fatalf("missing empty folder: %v", err)
				}
			}
			entries, err := target.ReadDir("/out")
			if err != nil {
				t.Fatal(err)
			}
			var backup string
			for _, entry := range entries {
				if strings.HasPrefix(entry.Name(), relayStagePrefix) {
					t.Fatalf("staging was left behind: %s", entry.Name())
				}
				if strings.HasPrefix(entry.Name(), "report.txt.bak-") {
					backup = entry.Name()
				}
			}
			if test.policy == relayOverwrite && !test.cancel {
				if backup == "" || relayTestRead(t, target, path.Join("/out", backup)) != "old report" || relayTestRead(t, target, "/out/report.txt") != "new report" {
					t.Fatal("backup or replacement is incorrect")
				}
			} else if relayTestRead(t, target, "/out/report.txt") != "old report" || backup != "" {
				t.Fatal("original changed")
			}
		})
	}
}

func TestRelayDirectStagedRunner(t *testing.T) {
	source, target := relayTestFS(t), relayTestFS(t)
	if err := source.Mkdir("/source"); err != nil {
		t.Fatal(err)
	}
	if err := target.Mkdir("/out"); err != nil {
		t.Fatal(err)
	}
	relayTestWrite(t, source, "/source/data", "archive data")
	var scripts []string
	endpoint := relayEndpoint{"backup.example", "deploy", 22}
	task := relayTask{from: "/source", to: "/out", items: []relayItem{{source: "data", dest: "data"}}, endpoint: endpoint}
	task.run = func(_ context.Context, script string) (string, string, error) {
		scripts = append(scripts, script)
		if len(scripts) == 1 {
			return "", "", nil
		}
		entries, err := target.ReadDir("/out")
		if err != nil || len(entries) != 1 {
			return "", "", errors.New("staging folder was not created")
		}
		staging := path.Join("/out", entries[0].Name())
		want, err := relayPipeline("/source", []string{"data"}, staging, endpoint)
		if err != nil || want != script {
			return "", "", errors.New("unexpected transfer pipeline")
		}
		writer, err := target.write(path.Join(staging, "data"))
		if err != nil {
			return "", "", err
		}
		_, err = io.WriteString(writer, "archive data")
		return "", "", errors.Join(err, writer.Close())
	}
	var final relayProgress
	err := task.move(context.Background(), source, target, func(name string) error {
		return relayRemoveStage(context.Background(), target.Client, name)
	}, func(p relayProgress) { final = p })
	probe, _ := endpoint.command("true")
	if err != nil || len(scripts) != 2 || scripts[0] != probe+"\n" || final.mode != relayDirect || final.files != 1 {
		t.Fatalf("direct progress=%+v scripts=%q error=%v", final, scripts, err)
	}
	if relayTestRead(t, target, "/out/data") != "archive data" {
		t.Fatal("direct transfer did not publish verified data")
	}
}
