// Package certs issues TLS certificates and keeps them: from Let's Encrypt
// through ACME (Issuer), proving a domain with a DNS record or an HTTP file,
// or self-signed (SelfSigned). A Store keeps them on this computer, and
// Export writes one as a ZIP of the formats web servers take.
package certs

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"strings"

	"golang.org/x/net/idna"
)

// KeyType is the kind of a certificate's private key.
type KeyType string

const (
	// KeyEC256 is an ECDSA key on P-256: small and fast, what every current
	// client takes. The default.
	KeyEC256 KeyType = "ec256"
	// KeyRSA2048 is a 2048-bit RSA key, for old systems that take no ECDSA.
	KeyRSA2048 KeyType = "rsa2048"
)

// NewKey makes a private key of type t, "" for KeyEC256.
func NewKey(t KeyType) (crypto.Signer, error) {
	switch t {
	case "", KeyEC256:
		return ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	case KeyRSA2048:
		return rsa.GenerateKey(rand.Reader, 2048)
	}
	return nil, fmt.Errorf("certs: unknown key type %q", t)
}

// Bundle is a certificate with its private key: Chain is the certificate
// first, then the intermediates that vouch for it, without the root.
type Bundle struct {
	Key   crypto.Signer
	Chain []*x509.Certificate
}

// Leaf is the certificate itself, nil for none.
func (b Bundle) Leaf() *x509.Certificate {
	if len(b.Chain) == 0 {
		return nil
	}
	return b.Chain[0]
}

// CertPEM is the certificate alone, as PEM.
func (b Bundle) CertPEM() []byte { return certsPEM(b.Chain[:1]) }

// ChainPEM is the intermediates, as PEM: empty for a self-signed one.
func (b Bundle) ChainPEM() []byte { return certsPEM(b.Chain[1:]) }

// FullChainPEM is the certificate and the intermediates, as PEM: what
// Nginx's ssl_certificate takes.
func (b Bundle) FullChainPEM() []byte { return certsPEM(b.Chain) }

// KeyPEM is the private key as PKCS#8 PEM.
func (b Bundle) KeyPEM() ([]byte, error) {
	der, err := x509.MarshalPKCS8PrivateKey(b.Key)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), nil
}

func certsPEM(chain []*x509.Certificate) []byte {
	var out []byte
	for _, c := range chain {
		out = append(out, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.Raw})...)
	}
	return out
}

// ParseBundle reads a bundle from the PEM of its chain and of its key.
func ParseBundle(chainPEM, keyPEM []byte) (Bundle, error) {
	chain, err := ParseChain(chainPEM)
	if err != nil {
		return Bundle{}, err
	}
	block, _ := pem.Decode(keyPEM)
	if block == nil {
		return Bundle{}, errors.New("certs: no private key")
	}
	key, err := parseKey(block)
	if err != nil {
		return Bundle{}, err
	}
	return Bundle{Key: key, Chain: chain}, nil
}

// ParseChain reads the certificates of PEM, in order.
func ParseChain(chainPEM []byte) ([]*x509.Certificate, error) {
	var chain []*x509.Certificate
	for rest := chainPEM; ; {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		c, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, err
		}
		chain = append(chain, c)
	}
	if len(chain) == 0 {
		return nil, errors.New("certs: no certificate")
	}
	return chain, nil
}

func parseKey(block *pem.Block) (crypto.Signer, error) {
	switch block.Type {
	case "EC PRIVATE KEY":
		return x509.ParseECPrivateKey(block.Bytes)
	case "RSA PRIVATE KEY":
		return x509.ParsePKCS1PrivateKey(block.Bytes)
	}
	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	s, ok := k.(crypto.Signer)
	if !ok {
		return nil, fmt.Errorf("certs: unsupported private key %T", k)
	}
	return s, nil
}

// NormalizeDomains checks the names a certificate is for, one per entry or
// separated by spaces, commas or new lines, and gives them in lower case,
// international ones in their ASCII form, without duplicates, in the order
// given. A wildcard is "*." and a domain; ips allows IP addresses, which
// only a self-signed certificate takes.
func NormalizeDomains(in []string, ips bool) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, entry := range in {
		for _, name := range strings.FieldsFunc(entry, func(r rune) bool {
			return r == ' ' || r == ',' || r == '\n' || r == '\r' || r == '\t' || r == '，' || r == '、'
		}) {
			d, err := normalizeDomain(name, ips)
			if err != nil {
				return nil, err
			}
			if !seen[d] {
				seen[d] = true
				out = append(out, d)
			}
		}
	}
	if len(out) == 0 {
		return nil, ErrNoDomain
	}
	return out, nil
}

// ErrNoDomain says that no domain was given.
var ErrNoDomain = errors.New("certs: no domain")

// DomainError is a name that cannot be a certificate's.
type DomainError struct {
	Name   string
	Reason string
}

func (e *DomainError) Error() string { return fmt.Sprintf("certs: %q: %s", e.Name, e.Reason) }

func normalizeDomain(name string, ips bool) (string, error) {
	name = strings.TrimSuffix(strings.TrimSpace(name), ".")
	// A URL pasted whole: its host.
	if i := strings.Index(name, "://"); i >= 0 {
		name = name[i+3:]
	}
	if i := strings.IndexAny(name, "/?#"); i >= 0 {
		name = name[:i]
	}
	if h, _, err := net.SplitHostPort(name); err == nil {
		name = h
	}
	if ip := net.ParseIP(strings.Trim(name, "[]")); ip != nil {
		if !ips {
			return "", &DomainError{name, "an IP address needs a self-signed certificate"}
		}
		return ip.String(), nil
	}
	wild := strings.HasPrefix(name, "*.")
	base := strings.TrimPrefix(name, "*.")
	ascii, err := idna.Lookup.ToASCII(base)
	if err != nil || ascii == "" {
		return "", &DomainError{name, "not a domain name"}
	}
	if strings.Contains(ascii, "*") {
		return "", &DomainError{name, "a wildcard goes only first, as *.example.com"}
	}
	if !strings.Contains(ascii, ".") {
		return "", &DomainError{name, "not a full domain name"}
	}
	if wild {
		return "*." + ascii, nil
	}
	return ascii, nil
}

// IsWildcard says whether domain is a wildcard, "*.example.com".
func IsWildcard(domain string) bool { return strings.HasPrefix(domain, "*.") }
