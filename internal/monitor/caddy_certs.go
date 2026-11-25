package monitor

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"path"
	"slices"
	"strings"
	"time"
)

// CaddyCertificate is the public leaf certificate (or internal root CA)
// parsed from a Caddy storage file. No private key is read.
type CaddyCertificate struct {
	Path, Subject, Issuer, KeyType string
	SANs                           []string
	NotBefore, NotAfter            time.Time
	IsCA                           bool
	PEM                            string
}

// CaddyCertificates lists and reads up to 256 public .crt files from Caddy
// storage, including its local CA root, then decodes the first PEM certificate
// in each file. It returns any valid rows with a diagnostic for malformed PEM.
func CaddyCertificates(ctx context.Context, run CaddyRunner, inst CaddyInstall) ([]CaddyCertificate, error) {
	if !caddyPath(inst.DataDir) || inst.DataDir == "/etc" {
		return nil, ErrCaddyUnsafe
	}
	root := path.Join(inst.DataDir, "certificates")
	ca := path.Join(inst.DataDir, "pki/authorities/local/root.crt")
	script := fmt.Sprintf(`export LC_ALL=C
root=%s
ca=%s
emit() {
  [ -f "$1" ] && [ ! -L "$1" ] || return '0'
  size=$(wc -c < "$1") || return '1'
  [ "$size" -le '65536' ] || return '1'
  printf 'CADDY certificate '
  { printf '%%s\n' "$1"; cat -- "$1"; } | base64 | tr -d '\n'
  printf '\n'
}
emit "$ca" || exit '1'
if [ -d "$root" ]; then
  find "$root" -type f -name '*.crt' | sort | while IFS= read -r file; do
    count=$((${count-0}+1)); [ "$count" -le '255' ] || exit '1'
    emit "$file" || exit '1'
  done
fi`, privilegeQuote(root), privilegeQuote(ca))
	var rows []caddyRecord
	var err error
	if inst.Method == CaddyDocker && inst.ContainerDataUnmapped {
		if !caddyIdentifier.MatchString(inst.Container) {
			return nil, ErrCaddyUnsafe
		}
		stdout, stderr, runErr := run(ctx, SystemCommand("docker", "exec", "-i", inst.Container, "sh", "-s"), strings.NewReader(script), nil)
		if runErr != nil {
			return nil, caddyCommandError(runErr, strings.TrimSpace(stderr))
		}
		rows, err = caddyRecords(stdout)
	} else {
		rows, err = caddyRunRecords(ctx, run, script)
	}
	if err != nil {
		return nil, err
	}
	var certs []CaddyCertificate
	var invalid []string
	for _, row := range rows {
		if row.tag != "certificate" {
			continue
		}
		name, data, found := strings.Cut(row.value, "\n")
		if !found || !caddyPath(name) || (name != ca && !strings.HasPrefix(name, root+"/")) || len(data) > 65536 {
			return nil, ErrCaddyUnsafe
		}
		block, _ := pem.Decode([]byte(data))
		if block == nil || block.Type != "CERTIFICATE" {
			invalid = append(invalid, name)
			continue
		}
		parsed, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			invalid = append(invalid, name)
			continue
		}
		cert := CaddyCertificate{Path: name, Subject: parsed.Subject.String(), Issuer: parsed.Issuer.String(), KeyType: parsed.PublicKeyAlgorithm.String(), SANs: slices.Clone(parsed.DNSNames), NotBefore: parsed.NotBefore, NotAfter: parsed.NotAfter, IsCA: parsed.IsCA, PEM: string(pem.EncodeToMemory(block))}
		for _, ip := range parsed.IPAddresses {
			cert.SANs = append(cert.SANs, ip.String())
		}
		cert.SANs = append(cert.SANs, parsed.EmailAddresses...)
		for _, uri := range parsed.URIs {
			cert.SANs = append(cert.SANs, uri.String())
		}
		certs = append(certs, cert)
	}
	if len(invalid) != 0 {
		return certs, fmt.Errorf("invalid Caddy certificates: %s", strings.Join(invalid, ", "))
	}
	return certs, nil
}

// CaddyLocalCA returns the public local authority certificate chain from the
// admin API. It returns an API error if the endpoint is absent.
func CaddyLocalCA(ctx context.Context, run CaddyRunner, inst CaddyInstall) (string, error) {
	return CaddyAdmin(ctx, run, inst, "GET", "/pki/ca/local/certificates", "")
}
