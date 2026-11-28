package certs

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"io/fs"
	"math/big"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Method is how a certificate was issued: how its domains were proved.
type Method string

const (
	MethodSelfSigned Method = "self-signed" // signed by its own key
	MethodHTTP       Method = "http"        // a file served by the connected server
	MethodDNS        Method = "dns"         // a TXT record added through a DNS provider's API
	MethodDNSManual  Method = "dns-manual"  // a TXT record added by hand
)

// Cert is a certificate the Store keeps: what it is, not its key.
type Cert struct {
	// ID is the name of its folder in the store: its first domain, with
	// "-self-signed" for a self-signed one.
	ID      string   `json:"-"`
	Domains []string `json:"domains"`
	Method  Method   `json:"method"`
	// Provider is the DNS provider of MethodDNS, by its ID; Server the
	// host of MethodHTTP, by its name: what a renewal uses again.
	Provider  string    `json:"provider,omitempty"`
	Server    string    `json:"server,omitempty"`
	KeyType   KeyType   `json:"keyType"`
	Issuer    string    `json:"issuer"`
	Staging   bool      `json:"staging,omitempty"` // from Let's Encrypt's test CA, which nothing trusts
	NotBefore time.Time `json:"notBefore"`
	NotAfter  time.Time `json:"notAfter"`
	Issued    time.Time `json:"issued"`
	// Password protects the PFX and JKS files of its downloads: random,
	// made once, so that every download of it takes the same.
	Password string `json:"password"`
}

// DaysLeft is how many whole days the certificate has left at now,
// negative once it has expired.
func (c Cert) DaysLeft(now time.Time) int {
	d := c.NotAfter.Sub(now)
	if d < 0 {
		return -int((-d).Hours()/24) - 1
	}
	return int(d.Hours() / 24)
}

// CertID is the ID of a certificate for domains issued by method.
func CertID(domains []string, method Method) string {
	id := "cert"
	if len(domains) > 0 {
		id = strings.Replace(domains[0], "*.", "wildcard.", 1)
		id = strings.Map(func(r rune) rune {
			switch {
			case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '-':
				return r
			}
			return '_'
		}, strings.ToLower(id))
	}
	if method == MethodSelfSigned {
		id += "-self-signed"
	}
	return id
}

// Store keeps certificates in Dir, one folder each: meta.json,
// fullchain.pem and privkey.pem, readable by the user alone.
type Store struct{ Dir string }

const (
	metaFile  = "meta.json"
	chainFile = "fullchain.pem"
	keyFile   = "privkey.pem"
)

// ErrNotFound says that the store has no such certificate.
var ErrNotFound = errors.New("certs: no such certificate")

// List is the certificates, those that expire first first.
func (s Store) List() ([]Cert, error) {
	entries, err := os.ReadDir(s.Dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Cert
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		c, err := s.meta(e.Name())
		if err != nil {
			continue // a folder that is not a certificate's, or half written
		}
		out = append(out, c)
	}
	slices.SortFunc(out, func(a, b Cert) int {
		if c := a.NotAfter.Compare(b.NotAfter); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out, nil
}

func (s Store) meta(id string) (Cert, error) {
	data, err := os.ReadFile(filepath.Join(s.Dir, id, metaFile))
	if errors.Is(err, fs.ErrNotExist) {
		return Cert{}, ErrNotFound
	}
	if err != nil {
		return Cert{}, err
	}
	var c Cert
	if err := json.Unmarshal(data, &c); err != nil {
		return Cert{}, err
	}
	c.ID = id
	return c, nil
}

// Get is the certificate id and its bundle.
func (s Store) Get(id string) (Cert, Bundle, error) {
	if !validID(id) {
		return Cert{}, Bundle{}, ErrNotFound
	}
	c, err := s.meta(id)
	if err != nil {
		return Cert{}, Bundle{}, err
	}
	chain, err := os.ReadFile(filepath.Join(s.Dir, id, chainFile))
	if err != nil {
		return Cert{}, Bundle{}, err
	}
	key, err := os.ReadFile(filepath.Join(s.Dir, id, keyFile))
	if err != nil {
		return Cert{}, Bundle{}, err
	}
	b, err := ParseBundle(chain, key)
	return c, b, err
}

// Save keeps b as the certificate c describes, replacing one of the same
// ID, a renewal: its dates and issuer are b's, its ID CertID's, and its
// password the replaced one's, or a new one. It returns what it kept.
func (s Store) Save(c Cert, b Bundle, now time.Time) (Cert, error) {
	leaf := b.Leaf()
	if leaf == nil {
		return Cert{}, errors.New("certs: no certificate")
	}
	c.ID = CertID(c.Domains, c.Method)
	c.NotBefore, c.NotAfter, c.Issued = leaf.NotBefore, leaf.NotAfter, now
	c.Issuer = leaf.Issuer.CommonName
	if len(leaf.Issuer.Organization) > 0 && c.Issuer == "" {
		c.Issuer = leaf.Issuer.Organization[0]
	}
	if old, err := s.meta(c.ID); err == nil && old.Password != "" {
		c.Password = old.Password
	}
	if c.Password == "" {
		c.Password = NewPassword()
	}
	keyPEM, err := b.KeyPEM()
	if err != nil {
		return Cert{}, err
	}
	meta, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return Cert{}, err
	}
	dir := filepath.Join(s.Dir, c.ID)
	// The key and chain first, the description last: a store that has
	// the description has the rest.
	for _, f := range []struct {
		name string
		data []byte
	}{{keyFile, keyPEM}, {chainFile, b.FullChainPEM()}, {metaFile, append(meta, '\n')}} {
		if err := writePrivate(filepath.Join(dir, f.name), f.data); err != nil {
			return Cert{}, err
		}
	}
	return c, nil
}

// Delete removes the certificate id, key and all.
func (s Store) Delete(id string) error {
	if !validID(id) {
		return ErrNotFound
	}
	return os.RemoveAll(filepath.Join(s.Dir, id))
}

// validID says whether id names a folder in the store, not one elsewhere.
func validID(id string) bool {
	return id != "" && id != "." && id != ".." && !strings.ContainsAny(id, `/\:`)
}

// writePrivate writes data to path through a temporary file, readable by
// the user alone, in a folder only they can open.
func writePrivate(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, err = f.Write(data)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmp, 0o600)
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		os.Remove(tmp)
	}
	return err
}

// NewPassword is a random password for PFX and JKS files: 16 letters and
// digits, none that read alike (0 and O, 1, l and I).
func NewPassword() string {
	const set = "abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := make([]byte, 16)
	for i := range b {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(set))))
		if err != nil {
			panic(err) // crypto/rand does not fail
		}
		b[i] = set[n.Int64()]
	}
	return string(b)
}
