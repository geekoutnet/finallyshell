package app

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"finallyshell/internal/monitor"
	"finallyshell/locales"
)

func TestCaddyWireServiceAndAdmin(t *testing.T) {
	backend := caddyMonitorBackend{}
	var streamed string
	output, err := backend.service(t.Context(), func(_ context.Context, script string, input io.Reader, progress func(string)) (string, string, error) {
		if script != "'systemctl' 'stop' 'caddy.service'" || input != nil || progress == nil {
			t.Fatalf("stop: %q, %v", script, input)
		}
		progress("stopping")
		return "stopped", "", nil
	}, caddyPanelDemo, monitor.CaddyStop, func(line string) { streamed += line })
	if err != nil || output != "" || streamed != "stopping" {
		t.Fatalf("service = %q, %q, %v", output, streamed, err)
	}
	body := `{"secret":"payload"}`
	output, err = backend.admin(t.Context(), func(_ context.Context, script string, input io.Reader, _ func(string)) (string, string, error) {
		data, err := io.ReadAll(input)
		if err != nil || string(data) != body || strings.Contains(script, body) || !strings.Contains(script, "'--data-binary' '@-' '--' 'http://localhost:2019/load'") {
			t.Fatalf("admin command %q, %q, %v", script, data, err)
		}
		return "loaded", "", nil
	}, caddyPanelDemo, "POST", "/load", body)
	if err != nil || output != "loaded" {
		t.Fatalf("admin = %q, %v", output, err)
	}
}

func TestCaddyWireEscalationStdin(t *testing.T) {
	app, _, _, _ := overviewApp(t)
	session := app.sessions[0]
	const payload = "fixture\x00payload\n"
	session.overview.memory.privilegeRunner = func(_ context.Context, request monitor.PrivilegeRequest) (monitor.PrivilegeResult, error) {
		if request.Probe {
			return monitor.PrivilegeResult{Stdout: "sudo-n\n"}, nil
		}
		text, err := io.ReadAll(request.Stdin)
		if err != nil || string(text) != payload || strings.Contains(request.Command, "fixture") || !strings.Contains(request.Command, "sudo -n") {
			t.Fatalf("privilege payload or command: %q, %q, %v", text, request.Command, err)
		}
		return monitor.PrivilegeResult{Started: true, Authenticated: true, Stdout: "forwarded"}, nil
	}
	_, run := app.caddyAccess(session)
	output, _, err := run(t.Context(), "'cat'", strings.NewReader(payload), nil)
	if err != nil || output != "forwarded" {
		t.Fatalf("forwarding = %q, %v", output, err)
	}
}

func caddyWireRecord(tag, value string) string {
	return "CADDY " + tag + " " + base64.StdEncoding.EncodeToString([]byte(value)) + "\n"
}

func caddyWireFile(name, text string) string {
	return fmt.Sprintf("F\n%s\n%d\n%s\n", base64.StdEncoding.EncodeToString([]byte(name)), len(text), base64.StdEncoding.EncodeToString([]byte(text)))
}

func caddyWireResponses(t *testing.T, steps ...[2]string) monitor.CaddyRunner {
	t.Helper()
	index := 0
	t.Cleanup(func() {
		if index != len(steps) {
			t.Errorf("called %d of %d runner steps", index, len(steps))
		}
	})
	return func(_ context.Context, script string, input io.Reader, _ func(string)) (string, string, error) {
		if input != nil {
			data, err := io.ReadAll(input)
			if err != nil {
				t.Fatal(err)
			}
			script += "\n" + string(data)
		}
		if index >= len(steps) {
			t.Fatalf("unexpected command: %s", script)
		}
		step := steps[index]
		index++
		if !strings.Contains(script, step[0]) {
			t.Fatalf("step %d: missing %q in %s", index, step[0], script)
		}
		return step[1], "", nil
	}
}

func TestCaddyWireCertificatesAndLogs(t *testing.T) {
	public, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	inst := caddyPanelDemo
	expires := time.Unix(2000000000, 0)
	certificate := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test root"}, IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign, NotAfter: expires}
	encode := func(child, parent *x509.Certificate) string {
		der, err := x509.CreateCertificate(rand.Reader, child, parent, public, key)
		if err != nil {
			t.Fatal(err)
		}
		return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	}
	root := encode(certificate, certificate)
	leaf := encode(&x509.Certificate{SerialNumber: big.NewInt(2), DNSNames: []string{"one.test", "two.test"}, NotAfter: expires}, certificate)
	leafPath := inst.DataDir + "/certificates/issuer/one.test/one.test.crt"
	rootPath := inst.DataDir + "/pki/authorities/local/root.crt"
	config := "{\n on_demand_tls {\n  ask http://localhost:9000\n }\n}\nexample.test {\n log {\n  output file /var/log/caddy/access.log\n }\n}\n"
	backend := caddyMonitorBackend{}
	for _, fromAPI := range []bool{false, true} {
		rows := caddyWireRecord("certificate", leafPath+"\n"+leaf)
		steps := [][2]string{{"CADDY certificate", rows + caddyWireRecord("certificate", rootPath+"\n"+root)}}
		if fromAPI {
			steps = [][2]string{{"CADDY certificate", rows}, {"/pki/ca/local/certificates", leaf + root}}
		}
		steps = append(steps, [2]string{"for file in '/etc/caddy/Caddyfile'", caddyWireFile(inst.Config, config)})
		info, err := backend.certs(t.Context(), caddyWireResponses(t, steps...), inst)
		if err != nil || len(info.Rows) != 1 || !info.OnDemand || info.Root.PEM != root || info.Root.Subject != "CN=test root" || (info.Root.Path == "") != fromAPI ||
			info.Rows[0] != (caddyCertificate{Domains: "one.test, two.test", Issuer: "CN=test root", Key: "Ed25519", Path: leafPath, Expires: expires.UTC(), PEM: leaf}) {
			t.Fatalf("certificates = %+v, %v", info, err)
		}
	}
	if got := backend.logFiles([]monitor.CaddyFile{{Path: inst.Config, Content: config}}); !reflect.DeepEqual(got, []string{"/var/log/caddy/access.log"}) {
		t.Fatalf("log files = %v", got)
	}
	inst.Method, inst.Container = monitor.CaddyDocker, "example"
	sources := caddyLogSources(inst, []string{"/data/site.log"})
	if len(sources) != 2 || sources[0].script != "exec 'docker' 'logs' '--timestamps' '--tail' '300' '--follow' 'example' 2>&1" || sources[1].script != "exec 'docker' 'exec' 'example' 'tail' '-n' '300' '-F' '--' '/data/site.log' 2>&1" {
		t.Fatalf("Docker logs = %+v", sources)
	}
}

func TestCaddyWireBackupAndManagedDiffs(t *testing.T) {
	inst := caddyPanelDemo
	backend := caddyMonitorBackend{}
	backup := "/etc/caddy/.backups/20260213120000"
	listed, err := backend.backups(t.Context(), caddyWireResponses(t, [2]string{"root='/etc/caddy/.backups'", backup + "\n/etc/other/20260213120000\n"}), inst)
	if err != nil || !reflect.DeepEqual(listed, []caddyBackup{{Path: backup}}) {
		t.Fatalf("backups = %v, %v", listed, err)
	}
	checked := func(preview caddyBackupPreview, old, after string, err error) {
		t.Helper()
		if err != nil || len(preview.Files) != 1 || preview.Files[0].Path != inst.Config || preview.Files[0].Content != old || len(preview.Change.Writes) == 0 || preview.Change.Writes[0].Content != after {
			t.Fatalf("preview = %+v, %v", preview, err)
		}
	}
	preview, err := backend.backupPreview(t.Context(), caddyWireResponses(t,
		[2]string{".finallyshell-paths", inst.Config + "\n"}, [2]string{backup + inst.Config, caddyWireFile(backup+inst.Config, "before")},
		[2]string{"for file in '/etc/caddy/Caddyfile'", caddyWireFile(inst.Config, "after")}), inst, backup)
	checked(preview, "after", "before", err)
	if _, err := backend.deleteBackup(t.Context(), caddyWireResponses(t, [2]string{"rm -rf -- '" + backup + "'", ""}), inst, backup); err != nil {
		t.Fatal(err)
	}
	const config = "one.test {\n respond 200\n}\n"
	preview, err = backend.managedPreview(t.Context(), caddyWireResponses(t, [2]string{"for file in '/etc/caddy/Caddyfile'", caddyWireFile(inst.Config, config)}), inst)
	if err != nil || len(preview.Files) != 1 || preview.Files[0].Content != config || len(preview.Change.Writes) != 2 || !strings.Contains(preview.Change.Writes[0].Content, "import") || preview.Change.Writes[1].Path != "/etc/caddy/sites/one.test.caddy" {
		t.Fatalf("managed preview = %+v, %v", preview, err)
	}
}

type caddyWireTransport func(*http.Request) (*http.Response, error)

func (transport caddyWireTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func TestCaddyWireLifecycle(t *testing.T) {
	backend := caddyMonitorBackend{releaseClient: &http.Client{Transport: caddyWireTransport(func(request *http.Request) (*http.Response, error) {
		if request.URL.Host != "api.github.com" || request.URL.Path != "/repos/caddyserver/caddy/releases/latest" {
			t.Fatal(request.URL)
		}
		if _, ok := request.Context().Deadline(); !ok {
			t.Fatal("missing release deadline")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"tag_name":"v2.15.1"}`))}, nil
	})}}
	version, err := backend.latest(t.Context())
	if err != nil || version != "v2.15.1" {
		t.Fatalf("latest = %q, %v", version, err)
	}
	inst := caddyPanelDemo
	inst.Method, inst.Modules = monitor.CaddyBinary, nil
	ready := caddyWireRecord("host", "/usr/bin/caddy\nbinary\nv2.15.1") + caddyWireRecord("done", "")
	aptReady := caddyWireRecord("host", "/usr/bin/caddy\napt\nv2.15.1") + caddyWireRecord("done", "")
	for _, step := range []struct {
		name string
		run  func(monitor.CaddyRunner) (string, error)
		cmds [][2]string
		want string
	}{
		{"install-binary", func(run monitor.CaddyRunner) (string, error) {
			return backend.install(t.Context(), run, monitor.CaddyInstall{}, caddyInstallOptions{Method: monitor.CaddyBinary, Version: version, Enable: true, OpenPorts: true, Firewall: "firewalld"}, nil)
		}, [][2]string{{"record 'done'", caddyWireRecord("done", "")}, {"systemctl 'enable' 'caddy.service'", ""}, {"record 'done'", ready}, {"record 'config'", ""}}, version},
		{"update-binary", func(run monitor.CaddyRunner) (string, error) {
			return backend.update(t.Context(), run, inst, "2.15.1", false, nil)
		},
			[][2]string{{"fetch_binary 'v2.15.1'", ""}, {"record 'done'", ready}, {"record 'config'", ""}}, version},
		{"repair-binary", func(run monitor.CaddyRunner) (string, error) {
			return backend.update(t.Context(), run, inst, inst.Version, true, nil)
		},
			[][2]string{{"fetch_binary 'v2.10.0'", ""}, {"record 'done'", ready}, {"record 'config'", ""}}, version},
		{"update-package", func(run monitor.CaddyRunner) (string, error) {
			return backend.update(t.Context(), run, caddyPanelDemo, "2.15.1", false, nil)
		},
			[][2]string{{"apt-get 'install' '-y' '--only-upgrade' 'caddy'", ""}, {"record 'done'", aptReady}, {"record 'config'", ""}}, version},
		{"remove", func(run monitor.CaddyRunner) (string, error) {
			return backend.uninstall(t.Context(), run, inst, false, nil)
		}, [][2]string{{"cp -an", ""}}, ""},
		{"purge", func(run monitor.CaddyRunner) (string, error) {
			return backend.uninstall(t.Context(), run, inst, true, nil)
		}, [][2]string{{`"$backup/removed-data"`, ""}}, ""},
		{"binary-migration", func(run monitor.CaddyRunner) (string, error) {
			return backend.migrateBinary(t.Context(), run, inst, nil)
		},
			[][2]string{{"99-finallyshell-caddy.conf", ""}, {"record 'done'", aptReady}, {"record 'config'", ""}}, version},
	} {
		t.Run(step.name, func(t *testing.T) {
			result, err := step.run(caddyWireResponses(t, step.cmds...))
			if err != nil || result != step.want {
				t.Fatalf("result = %q, %v", result, err)
			}
		})
	}
}

func TestCaddyWireNginxAndUnsupportedActions(t *testing.T) {
	backend := caddyMonitorBackend{}
	dump := "# configuration file /etc/nginx/nginx.conf:\nhttp {\n server { listen 80; server_name demo.test; return 301 https://demo.test; }\n server { listen 443 ssl; server_name demo.test; root /srv/test; location /other { deny all; } }\n}\n"
	drafts, err := backend.nginxToCaddy(nil, dump)
	if err != nil || len(drafts) != 2 || !strings.Contains(drafts[0].Content, "redir") || len(drafts[0].Notes) != 0 || len(drafts[1].Notes) == 0 || strings.Contains(drafts[0].Content, "https://demo.test {") {
		t.Fatalf("drafts = %+v, %v", drafts, err)
	}
	inst := caddyPanelDemo
	inst.AdminAddr = "off"
	actions := (&App{}).caddyServiceActions(&Session{}, inst)
	if actions[3].reason != locales.CpanelAdminOff() || caddyAdminReason(inst) != locales.CpanelAdminOff() || caddyUpdateReason(inst, true) != locales.CwireNoRepair() {
		t.Fatalf("missing disabled reasons: %+v", actions)
	}
	inst.Method, inst.Container, inst.ContainerCompose = monitor.CaddyDocker, "compose-caddy", true
	if caddyUpdateReason(inst, false) != locales.CwireDockerLifecycle() || caddyUninstallReason(inst, false) != locales.CwireDockerLifecycle() {
		t.Fatal("Compose lifecycle controls must direct users to Compose")
	}
	if _, err := backend.nginxCutover(t.Context(), caddyWireResponses(t), inst, monitor.CaddyChange{}, nil); !errors.Is(err, monitor.ErrCaddyUnsafe) {
		t.Fatal(err)
	}
}
