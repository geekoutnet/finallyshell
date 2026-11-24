package monitor

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestCertificatesPEMAndExpiry(t *testing.T) {
	now := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	leaf := &x509.Certificate{SerialNumber: big.NewInt(42), Subject: pkix.Name{CommonName: "front.example.org"},
		DNSNames: []string{"front.example.org", "www.example.org"}, NotBefore: now.Add(-24 * time.Hour), NotAfter: now.Add(27 * 24 * time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, leaf, leaf, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	file := "/etc/letsencrypt/live/front/fullchain.pem"
	text := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	certs, failed := ParseCertificateFiles("== "+file+"\n"+text+text, []string{file, "/etc/nginx/missing.pem"})
	if len(certs) != 1 || !reflect.DeepEqual(failed, []string{"/etc/nginx/missing.pem"}) {
		t.Fatalf("%+v %v", certs, failed)
	}
	cert := certs[0]
	if cert.Subject != "CN=front.example.org" || cert.Issuer != cert.Subject || !reflect.DeepEqual(cert.SANs, leaf.DNSNames) || cert.KeyType != "ECDSA" || !cert.NotBefore.Equal(leaf.NotBefore) || cert.DaysLeft(now) != 27 || cert.DaysLeft(cert.NotAfter) != -1 || cert.DaysLeft(cert.NotAfter.Add(-time.Second)) != 0 {
		t.Fatalf("%+v", cert)
	}
	if _, err := ParseCertificate(file, strings.ReplaceAll(text, "CERTIFICATE", "PRIVATE KEY")); err == nil {
		t.Fatal("accepted a private PEM block")
	}
	files := CertificateFiles(file+"\r\n/proc/fake/fullchain.pem\n", []string{file, "$variable", "/dev/something"})
	if !reflect.DeepEqual(files, []string{file}) || CertificateReadCommand(files) == "" || CertificateReadCommand([]string{"/dev/something"}) != "" {
		t.Fatalf("unexpected certificate candidates: %v", files)
	}
}

func TestCertificatesRemoteAndCertbot(t *testing.T) {
	remote, err := ParseRemoteCertificate("subject=CN = example.org\nissuer=CN = Test CA\nnotBefore=Oct  1 00:00:00 2026 GMT\nnotAfter=Jan 30 00:00:00 2027 GMT\n")
	if err != nil || remote.NotAfter.Month() != time.January || remote.Subject != "CN = example.org" || remote.Issuer != "CN = Test CA" {
		t.Fatalf("%+v: %v", remote, err)
	}
	const listing = `Found the following certs:
  Certificate Name: front.example.org
    Serial Number: 1234
    Key Type: ECDSA
    Domains: front.example.org www.example.org
    Expiry Date: 2026-12-24 14:30:45+00:00 (VALID: 29 days)
    Certificate Path: /etc/letsencrypt/live/front.example.org/fullchain.pem
    Private Key Path: /etc/letsencrypt/live/front.example.org/privkey.pem
  Certificate Name: older.example.org
    Key Type: RSA
    Domains: older.example.org
    Expiry Date: 2026-10-05 07:30:00+00:00 (INVALID: EXPIRED)
    Certificate Path: /etc/letsencrypt/live/older.example.org/fullchain.pem
`
	certs := ParseCertbot(listing)
	if len(certs) != 2 || certs[0].Expiry.IsZero() || certs[0].KeyType != "ECDSA" || len(certs[0].Domains) != 2 || certs[1].Status != "INVALID: EXPIRED" || strings.Contains(certs[0].Path, "privkey") {
		t.Fatalf("%+v", certs)
	}
	if !CertbotHasNginx("Plugins:\n* nginx\nDescription: Nginx Web Server plugin\n") || CertbotHasNginx("* webroot\nDescription: works with nginx\n") {
		t.Fatal("bad plugin detection")
	}
	if got := CertbotRenewCommand(true); got != "'certbot' 'renew' '--dry-run' '--non-interactive'" {
		t.Fatalf("%q", got)
	}
	for _, invalid := range []string{"localhost", "127.0.0.1", "*.example.org", "-bad.example.org", "example.org'", "example.org\nsecond", "-d", ""} {
		if ValidSiteHost(invalid) || CertificateRemoteCommand(invalid) != "" || CertbotIssueCommand([]string{invalid}, "admin@example.org") != "" {
			t.Errorf("accepted %q", invalid)
		}
	}
}
