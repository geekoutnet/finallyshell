package certs

import (
	"archive/zip"
	"bytes"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
	"time"

	"github.com/go-acme/lego/v4/challenge"
	"github.com/pavlo-v-chernykh/keystore-go/v4"
	"software.sslmate.com/src/go-pkcs12"
)

func TestNormalizeDomains(t *testing.T) {
	got, err := NormalizeDomains([]string{" Example.COM, www.example.com\n*.Example.com", "https://api.example.com:8443/x?y", "example.com", "中文.cn"}, false)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"example.com", "www.example.com", "*.example.com", "api.example.com", "xn--fiq228c.cn"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
	for _, bad := range []string{"localhost", "a.*.example.com", "exa mple", "1.2.3.4", "[::1]:443"} {
		if _, err := NormalizeDomains([]string{bad}, false); err == nil {
			t.Errorf("%q passed", bad)
		}
	}
	if got, err := NormalizeDomains([]string{"10.0.0.1", "[::1]:443"}, true); err != nil || !slices.Equal(got, []string{"10.0.0.1", "::1"}) {
		t.Fatalf("IPs for self-signed: %q, %v", got, err)
	}
	if _, err := NormalizeDomains([]string{" , "}, false); !errors.Is(err, ErrNoDomain) {
		t.Fatalf("no domain: %v", err)
	}
}

func TestSelfSigned(t *testing.T) {
	now := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)
	for _, kt := range []KeyType{KeyEC256, KeyRSA2048} {
		b, err := SelfSigned([]string{"lan.example", "*.lan.example", "192.168.1.10"}, kt, now, SelfSignedValidity)
		if err != nil {
			t.Fatal(err)
		}
		leaf := b.Leaf()
		if !slices.Equal(leaf.DNSNames, []string{"lan.example", "*.lan.example"}) || len(leaf.IPAddresses) != 1 || leaf.Subject.CommonName != "lan.example" {
			t.Fatalf("%s: names %q, IPs %v", kt, leaf.DNSNames, leaf.IPAddresses)
		}
		if !leaf.NotAfter.Equal(now.Add(SelfSignedValidity)) {
			t.Fatalf("%s: until %v", kt, leaf.NotAfter)
		}
		if err := leaf.CheckSignature(leaf.SignatureAlgorithm, leaf.RawTBSCertificate, leaf.Signature); err != nil {
			t.Fatalf("%s: not signed by itself: %v", kt, err)
		}
		switch b.Key.(type) {
		case *ecdsa.PrivateKey:
			if kt != KeyEC256 {
				t.Fatalf("%s: ECDSA key", kt)
			}
		case *rsa.PrivateKey:
			if kt != KeyRSA2048 {
				t.Fatalf("%s: RSA key", kt)
			}
		}
	}
}

// testChain is a certificate signed by a test CA: a chain of two.
func testChain(t *testing.T, kt KeyType) Bundle {
	t.Helper()
	caKey, _ := NewKey(KeyEC256)
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Test CA"}, NotBefore: time.Now().Add(-time.Hour),
		NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	caDER, err := x509.CreateCertificate(rand.Reader, ca, ca, caKey.Public(), caKey)
	if err != nil {
		t.Fatal(err)
	}
	caCert, _ := x509.ParseCertificate(caDER)
	key, _ := NewKey(kt)
	leaf := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "example.com"}, DNSNames: []string{"example.com"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(90 * 24 * time.Hour), KeyUsage: x509.KeyUsageDigitalSignature}
	leafDER, err := x509.CreateCertificate(rand.Reader, leaf, caCert, key.Public(), caKey)
	if err != nil {
		t.Fatal(err)
	}
	leafCert, _ := x509.ParseCertificate(leafDER)
	return Bundle{Key: key, Chain: []*x509.Certificate{leafCert, caCert}}
}

func TestStore(t *testing.T) {
	s := Store{Dir: filepath.Join(t.TempDir(), "certs")}
	if list, err := s.List(); err != nil || len(list) != 0 {
		t.Fatalf("empty store: %v, %v", list, err)
	}
	now := time.Now()
	b := testChain(t, KeyEC256)
	c, err := s.Save(Cert{Domains: []string{"example.com"}, Method: MethodHTTP, KeyType: KeyEC256, Server: "web"}, b, now)
	if err != nil {
		t.Fatal(err)
	}
	if c.ID != "example.com" || c.Issuer != "Test CA" || len(c.Password) != 16 || !c.NotAfter.Equal(b.Leaf().NotAfter) {
		t.Fatalf("saved %+v", c)
	}
	self, err := SelfSigned([]string{"*.example.com"}, KeyEC256, now, SelfSignedValidity)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Save(Cert{Domains: []string{"*.example.com"}, Method: MethodSelfSigned}, self, now); err != nil {
		t.Fatal(err)
	}
	list, err := s.List()
	if err != nil || len(list) != 2 || list[0].ID != "example.com" || list[1].ID != "wildcard.example.com-self-signed" {
		t.Fatalf("list %+v, %v", list, err)
	}
	got, gb, err := s.Get("example.com")
	if err != nil || got.Server != "web" || len(gb.Chain) != 2 || !gb.Leaf().Equal(b.Leaf()) {
		t.Fatalf("get %+v, %v", got, err)
	}
	// A renewal replaces it and keeps its password.
	renewed, err := s.Save(Cert{Domains: []string{"example.com"}, Method: MethodHTTP}, testChain(t, KeyEC256), now)
	if err != nil || renewed.Password != c.Password {
		t.Fatalf("renewal password %q, want %q (%v)", renewed.Password, c.Password, err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(filepath.Join(s.Dir, "example.com", keyFile))
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("key file mode %v, %v", info.Mode(), err)
		}
	}
	for _, bad := range []string{"..", "../x", `a\b`, ""} {
		if _, _, err := s.Get(bad); !errors.Is(err, ErrNotFound) {
			t.Errorf("Get(%q): %v", bad, err)
		}
		if err := s.Delete(bad); !errors.Is(err, ErrNotFound) {
			t.Errorf("Delete(%q): %v", bad, err)
		}
	}
	if err := s.Delete("example.com"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Get("example.com"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted: %v", err)
	}
}

func TestDaysLeft(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		after time.Duration
		want  int
	}{{90 * 24 * time.Hour, 90}, {36 * time.Hour, 1}, {time.Hour, 0}, {-time.Hour, -1}, {-49 * time.Hour, -3}} {
		if got := (Cert{NotAfter: now.Add(tc.after)}).DaysLeft(now); got != tc.want {
			t.Errorf("%v left: %d days, want %d", tc.after, got, tc.want)
		}
	}
}

func TestExport(t *testing.T) {
	for _, kt := range []KeyType{KeyEC256, KeyRSA2048} {
		b := testChain(t, kt)
		var buf bytes.Buffer
		e := Export{Name: "example.com", Bundle: b, Formats: Formats, Password: "s3cretPassw0rd", Readme: "hello"}
		if err := e.WriteZip(&buf); err != nil {
			t.Fatal(err)
		}
		zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
		if err != nil {
			t.Fatal(err)
		}
		files := map[string][]byte{}
		for _, f := range zr.File {
			r, _ := f.Open()
			files[f.Name], _ = io.ReadAll(r)
			r.Close()
		}
		var names []string
		for n := range files {
			names = append(names, n)
		}
		slices.Sort(names)
		want := []string{"README.txt", "apache/example.com.chain.crt", "apache/example.com.crt", "apache/example.com.key",
			"iis/example.com.pfx", "nginx/fullchain.pem", "nginx/privkey.pem", "tomcat/example.com.jks"}
		if !slices.Equal(names, want) {
			t.Fatalf("%s: files %q", kt, names)
		}

		nginx, err := ParseBundle(files["nginx/fullchain.pem"], files["nginx/privkey.pem"])
		if err != nil || len(nginx.Chain) != 2 {
			t.Fatalf("%s: nginx files: %v", kt, err)
		}
		apache, err := ParseBundle(append(files["apache/example.com.crt"], files["apache/example.com.chain.crt"]...), files["apache/example.com.key"])
		if err != nil || len(apache.Chain) != 2 || !apache.Leaf().Equal(b.Leaf()) {
			t.Fatalf("%s: apache files: %v", kt, err)
		}

		key, leaf, cas, err := pkcs12.DecodeChain(files["iis/example.com.pfx"], "s3cretPassw0rd")
		if err != nil || !leaf.Equal(b.Leaf()) || len(cas) != 1 || key == nil {
			t.Fatalf("%s: pfx: %v", kt, err)
		}
		if _, _, _, err := pkcs12.DecodeChain(files["iis/example.com.pfx"], "wrong"); err == nil {
			t.Fatalf("%s: pfx opened with a wrong password", kt)
		}

		ks := keystore.New()
		if err := ks.Load(bytes.NewReader(files["tomcat/example.com.jks"]), []byte("s3cretPassw0rd")); err != nil {
			t.Fatalf("%s: jks: %v", kt, err)
		}
		entry, err := ks.GetPrivateKeyEntry("example.com", []byte("s3cretPassw0rd"))
		if err != nil || len(entry.CertificateChain) != 2 || !bytes.Equal(entry.CertificateChain[0].Content, b.Leaf().Raw) {
			t.Fatalf("%s: jks entry: %v", kt, err)
		}
		if _, err := x509.ParsePKCS8PrivateKey(entry.PrivateKey); err != nil {
			t.Fatalf("%s: jks key: %v", kt, err)
		}
	}

	// A self-signed certificate has no chain file; a format alone.
	self, _ := SelfSigned([]string{"lan.example"}, KeyEC256, time.Now(), SelfSignedValidity)
	var buf bytes.Buffer
	if err := (Export{Name: "lan.example", Bundle: self, Formats: []Format{FormatApache}}).WriteZip(&buf); err != nil {
		t.Fatal(err)
	}
	zr, _ := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	var names []string
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	if !slices.Equal(names, []string{"apache/lan.example.crt", "apache/lan.example.key"}) {
		t.Fatalf("self-signed apache files %q", names)
	}
	if err := (Export{Name: "x", Bundle: self}).WriteZip(io.Discard); err == nil {
		t.Fatal("no format passed")
	}
}

func TestDNSProviders(t *testing.T) {
	ids := map[string]bool{}
	for _, p := range DNSProviders {
		if ids[p.ID] || p.Name == "" || len(p.Fields) == 0 || p.New == nil {
			t.Fatalf("provider %+v", p.ID)
		}
		ids[p.ID] = true
		if _, err := FindDNSProvider(p.ID).Provider(nil); !errors.As(err, new(*MissingFieldError)) {
			t.Errorf("%s with nothing: %v", p.ID, err)
		}
	}
	if FindDNSProvider("nope") != nil {
		t.Fatal("found an unknown provider")
	}
	// Values are trimmed, and an empty field with a default takes it.
	hw := *FindDNSProvider("huaweicloud")
	var got map[string]string
	hw.New = func(v map[string]string) (challenge.Provider, error) { got = v; return nil, nil }
	if _, err := hw.Provider(map[string]string{"id": " ak ", "secret": "sk"}); err != nil {
		t.Fatal(err)
	}
	if got["id"] != "ak" || got["region"] != "cn-north-4" {
		t.Fatalf("values %v", got)
	}
	if _, err := FindDNSProvider("cloudflare").Provider(map[string]string{"token": "t0ken"}); err != nil {
		t.Fatalf("cloudflare: %v", err)
	}
}
