package certs

import (
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"time"
)

// SelfSignedValidity is how long a self-signed certificate lasts: a year,
// under the 398 days Apple's systems take.
const SelfSignedValidity = 365 * 24 * time.Hour

// SelfSigned makes a certificate for domains (and IP addresses) signed by
// its own key, of type t, valid from now for validity. No browser trusts
// it until it is added to the system's trusted certificates: it is for
// LANs and tests.
func SelfSigned(domains []string, t KeyType, now time.Time, validity time.Duration) (Bundle, error) {
	domains, err := NormalizeDomains(domains, true)
	if err != nil {
		return Bundle{}, err
	}
	key, err := NewKey(t)
	if err != nil {
		return Bundle{}, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return Bundle{}, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: domains[0], Organization: []string{"Finallyshell self-signed"}},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(validity),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	if t == KeyRSA2048 {
		tmpl.KeyUsage |= x509.KeyUsageKeyEncipherment
	}
	for _, d := range domains {
		if ip := net.ParseIP(d); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else {
			tmpl.DNSNames = append(tmpl.DNSNames, d)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, key.Public(), key)
	if err != nil {
		return Bundle{}, err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return Bundle{}, err
	}
	return Bundle{Key: key, Chain: []*x509.Certificate{cert}}, nil
}
