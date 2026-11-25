package monitor

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestCaddyValidateAndPassword(t *testing.T) {
	inst := CaddyInstall{Binary: "/usr/bin/caddy", Config: "/etc/caddy/Caddyfile"}
	ctx := context.Background()
	secret := "space and 'quotes'"
	run := func(_ context.Context, script string, stdin io.Reader, progress func(string)) (string, string, error) {
		if strings.Contains(script, "'hash-password'") {
			if strings.Contains(script, secret) || strings.Contains(script, "--plaintext") || progress != nil || stdin == nil {
				t.Fatal("password leaked or stdin missing")
			}
			data, _ := io.ReadAll(stdin)
			if string(data) != secret+"\n" {
				t.Fatal("password not sent on stdin")
			}
			return "$2a$10$" + strings.Repeat("Z", 53) + "\n", "", nil
		}
		script = caddyTestScript(t, script, stdin)
		if !strings.Contains(script, "'adapt'") || !strings.Contains(script, "'validate'") || !strings.Contains(script, "mktemp -d") {
			t.Fatalf("incorrect preview script: %s", script)
		}
		return "", "bad config", io.ErrUnexpectedEOF
	}
	if _, err := ValidateCaddyConfig(ctx, run, inst, []CaddyFile{{Path: inst.Config, Content: "invalid\n"}}); !errors.Is(err, ErrCaddyInvalid) {
		t.Fatal(err)
	}
	if hash, err := HashCaddyPassword(ctx, run, inst, secret); err != nil || !strings.HasPrefix(hash, "$2a$") {
		t.Fatalf("hash=%q err=%v", hash, err)
	}
}

func TestCaddyFormatReportsFormattedOutput(t *testing.T) {
	run := func(_ context.Context, script string, stdin io.Reader, _ func(string)) (string, string, error) {
		data, _ := io.ReadAll(stdin)
		if script != "'/usr/bin/caddy' 'fmt' '-'" || string(data) != "unformatted" {
			t.Fatalf("script=%q input=%q", script, data)
		}
		return "formatted\n", "Error: the input is not formatted", io.ErrUnexpectedEOF
	}
	if output, err := FormatCaddyfile(context.Background(), run, CaddyInstall{Binary: "/usr/bin/caddy"}, "unformatted"); err != nil || output != "formatted\n" {
		t.Fatalf("output=%q err=%v", output, err)
	}
}

func TestCaddyAdminAndLogs(t *testing.T) {
	inst := CaddyInstall{Unit: "custom-caddy.service", AdminAddr: "unix//run/caddy's socket"}
	ctx := context.Background()
	run := func(_ context.Context, script string, stdin io.Reader, _ func(string)) (string, string, error) {
		for _, want := range []string{`'--unix-socket' '/run/caddy'\''s socket'`, `'http://localhost/load'`, `'--data-binary' '@-'`} {
			if !strings.Contains(script, want) {
				t.Fatalf("missing %q: %s", want, script)
			}
		}
		data, _ := io.ReadAll(stdin)
		if string(data) != "{}" {
			t.Fatal("stdin missing body")
		}
		return "{}", "", nil
	}
	if _, err := CaddyAdmin(ctx, run, inst, "POST", "/load", "{}"); err != nil {
		t.Fatal(err)
	}
	if _, err := CaddyAdmin(ctx, run, inst, "GET", "//elsewhere", ""); err == nil {
		t.Fatal("accepted invalid request target")
	}
	if script, err := CaddyLogsScript(inst, 30, true); err != nil || script != "'journalctl' '-u' 'custom-caddy.service' '-o' 'short-iso' '-n' '30' '-f' '--no-pager'" {
		t.Fatalf("script=%q err=%v", script, err)
	}
	files := []CaddyFile{{Path: caddyDefaultConfig, Content: "site {\n log {\n  output file \"/var/log/site's log\"\n }\n respond `output file /fake`\n}\n"}}
	if logs := CaddyLogFiles(files); len(logs) != 1 || logs[0] != "/var/log/site's log" {
		t.Fatalf("logs=%v", logs)
	}
}

type caddyHTTPTransport func(*http.Request) (*http.Response, error)

func (transport caddyHTTPTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return transport(req)
}

func TestCaddyLatestRelease(t *testing.T) {
	client := &http.Client{Transport: caddyHTTPTransport(func(req *http.Request) (*http.Response, error) {
		if req.URL.Host != "api.github.com" || req.URL.Path != "/repos/caddyserver/caddy/releases/latest" {
			t.Fatal(req.URL)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"tag_name":"v2.10.2"}`))}, nil
	})}
	if release, err := LatestCaddyRelease(context.Background(), client); err != nil || release != "v2.10.2" {
		t.Fatalf("release=%q err=%v", release, err)
	}
}

func TestCaddyCertificateParsing(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "example.com"}, DNSNames: []string{"example.com"}, NotBefore: time.Unix(100, 0), NotAfter: time.Unix(200, 0)}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, public, private)
	if err != nil {
		t.Fatal(err)
	}
	data := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	run := func(context.Context, string, io.Reader, func(string)) (string, string, error) {
		return "CADDY certificate " + base64.StdEncoding.EncodeToString(append([]byte("/data/caddy/certificates/public/example.com/example.com.crt\n"), data...)) + "\n", "", nil
	}
	certs, err := CaddyCertificates(context.Background(), run, CaddyInstall{DataDir: "/data/caddy"})
	if err != nil || len(certs) != 1 || certs[0].Subject != "CN=example.com" || certs[0].KeyType != "Ed25519" || !certs[0].NotAfter.Equal(cert.NotAfter) {
		t.Fatalf("certificates=%+v err=%v", certs, err)
	}
}
