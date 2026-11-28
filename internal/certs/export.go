package certs

import (
	"archive/zip"
	"bytes"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/pavlo-v-chernykh/keystore-go/v4"
	"software.sslmate.com/src/go-pkcs12"
)

// Format is a set of files a kind of server takes.
type Format string

const (
	FormatNginx  Format = "nginx"  // fullchain.pem and privkey.pem: Nginx, Caddy, most panels
	FormatApache Format = "apache" // the certificate, its chain and its key, as .crt and .key
	FormatIIS    Format = "iis"    // a password-protected .pfx (PKCS#12): IIS, Windows, Tomcat 8.5+
	FormatTomcat Format = "tomcat" // a password-protected .jks: Java and older Tomcat
)

// Formats are every format, in the order the download shows them.
var Formats = []Format{FormatNginx, FormatApache, FormatIIS, FormatTomcat}

// Export is what Export writes.
type Export struct {
	// Name is the files' base name, such as "example.com".
	Name    string
	Bundle  Bundle
	Formats []Format
	// Password protects the .pfx and .jks files.
	Password string
	// Readme, when not empty, goes in the ZIP as README.txt.
	Readme string
	// Time stamps the files, the zero time for now.
	Time time.Time
}

// WriteZip writes e as a ZIP: a folder for each format.
func (e Export) WriteZip(w io.Writer) error {
	if e.Bundle.Leaf() == nil || e.Bundle.Key == nil {
		return errors.New("certs: nothing to export")
	}
	if len(e.Formats) == 0 {
		return errors.New("certs: no format")
	}
	when := e.Time
	if when.IsZero() {
		when = time.Now()
	}
	keyPEM, err := e.Bundle.KeyPEM()
	if err != nil {
		return err
	}
	zw := zip.NewWriter(w)
	add := func(name string, data []byte) error {
		f, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate, Modified: when})
		if err != nil {
			return err
		}
		_, err = f.Write(data)
		return err
	}
	if e.Readme != "" {
		if err := add("README.txt", []byte(e.Readme)); err != nil {
			return err
		}
	}
	for _, f := range e.Formats {
		var err error
		switch f {
		case FormatNginx:
			if err = add("nginx/fullchain.pem", e.Bundle.FullChainPEM()); err == nil {
				err = add("nginx/privkey.pem", keyPEM)
			}
		case FormatApache:
			err = add("apache/"+e.Name+".crt", e.Bundle.CertPEM())
			if chain := e.Bundle.ChainPEM(); err == nil && len(chain) > 0 {
				err = add("apache/"+e.Name+".chain.crt", chain)
			}
			if err == nil {
				err = add("apache/"+e.Name+".key", keyPEM)
			}
		case FormatIIS:
			var pfx []byte
			if pfx, err = e.Bundle.PFX(e.Password); err == nil {
				err = add("iis/"+e.Name+".pfx", pfx)
			}
		case FormatTomcat:
			var jks []byte
			if jks, err = e.Bundle.JKS(e.Name, e.Password, when); err == nil {
				err = add("tomcat/"+e.Name+".jks", jks)
			}
		default:
			err = fmt.Errorf("certs: unknown format %q", f)
		}
		if err != nil {
			return err
		}
	}
	return zw.Close()
}

// PFX is the bundle as a PKCS#12 file protected by password, with AES-256
// and SHA-256, which Windows 10 1709 and Server 2019, OpenSSL 1.1.1 and
// Java 12 and later read.
func (b Bundle) PFX(password string) ([]byte, error) {
	return pkcs12.Modern2023.Encode(b.Key, b.Leaf(), b.Chain[1:], password)
}

// JKS is the bundle as a Java keystore under alias, its key and the store
// both protected by password.
func (b Bundle) JKS(alias, password string, created time.Time) ([]byte, error) {
	der, err := x509.MarshalPKCS8PrivateKey(b.Key)
	if err != nil {
		return nil, err
	}
	chain := make([]keystore.Certificate, len(b.Chain))
	for i, c := range b.Chain {
		chain[i] = keystore.Certificate{Type: "X509", Content: c.Raw}
	}
	ks := keystore.New()
	if err := ks.SetPrivateKeyEntry(alias, keystore.PrivateKeyEntry{CreationTime: created, PrivateKey: der, CertificateChain: chain}, []byte(password)); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := ks.Store(&buf, []byte(password)); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
