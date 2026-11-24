package monitor

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"net"
	"net/mail"
	"path"
	"slices"
	"strings"
	"time"
)

type Certificate struct {
	File, Subject, Issuer, KeyType string
	SANs                           []string
	NotBefore, NotAfter            time.Time
}

func (cert Certificate) DaysLeft(now time.Time) int {
	if !cert.NotAfter.After(now) {
		return -int(now.Sub(cert.NotAfter)/(24*time.Hour)) - 1
	}
	return int(cert.NotAfter.Sub(now) / (24 * time.Hour))
}

func certificateFromX509(file string, leaf *x509.Certificate) Certificate {
	sans := slices.Clone(leaf.DNSNames)
	for _, addr := range leaf.IPAddresses {
		sans = append(sans, addr.String())
	}
	for _, uri := range leaf.URIs {
		sans = append(sans, uri.String())
	}
	sans = append(sans, leaf.EmailAddresses...)
	return Certificate{file, leaf.Subject.String(), leaf.Issuer.String(), leaf.PublicKeyAlgorithm.String(), sans, leaf.NotBefore, leaf.NotAfter}
}

func ParseCertificate(file, text string) (Certificate, error) {
	block, _ := pem.Decode([]byte(text))
	if block == nil || block.Type != "CERTIFICATE" {
		return Certificate{}, errors.New("no public PEM certificate found")
	}
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return Certificate{}, err
	}
	return certificateFromX509(file, leaf), nil
}

const CertificateDiscoveryCommand = `
for file in '/etc/letsencrypt/live/'*/fullchain.pem; do
    if [ -f "$file" ]; then 'printf' '%s\n' "$file"; fi
done
`

func CertificateFiles(text string, configured []string) []string {
	set := map[string]bool{}
	for _, file := range configured {
		if ValidNginxPath(file) && !strings.HasPrefix(file, "/proc/") && !strings.HasPrefix(file, "/dev/") && !strings.HasPrefix(file, "/sys/") {
			set[file] = true
		}
	}
	for line := range strings.SplitSeq(text, "\n") {
		file := strings.TrimSuffix(line, "\r")
		if ValidNginxPath(file) && strings.HasPrefix(file, "/etc/letsencrypt/live/") && path.Base(file) == "fullchain.pem" {
			set[file] = true
		}
	}
	var files []string
	for file := range set {
		files = append(files, file)
	}
	slices.Sort(files)
	return files
}

func CertificateReadCommand(files []string) string {
	if len(files) == 0 || len(files) > 30 {
		return ""
	}
	args := []string{"set", "--"}
	for _, file := range files {
		if !ValidNginxPath(file) || strings.HasPrefix(file, "/proc/") || strings.HasPrefix(file, "/dev/") || strings.HasPrefix(file, "/sys/") {
			return ""
		}
		args = append(args, file)
	}
	return nginxLF + SystemCommand(args...) + `
for file do
    'printf' '== %s\n' "$file"
    [ -f "$file" ] || continue
    'head' '-c' '262144' "$file" | 'awk' '
      /^-----BEGIN CERTIFICATE-----\r?$/ { started=1; print "-----BEGIN CERTIFICATE-----"; next }
      started && /^-----END CERTIFICATE-----\r?$/ { print "-----END CERTIFICATE-----"; exit }
      started && /^[A-Za-z0-9+\/=]+\r?$/ { print; next }
      started { exit }
    '
done
`
}

func ParseCertificateFiles(text string, files []string) ([]Certificate, []string) {
	certs := map[string]Certificate{}
	for chunk := range strings.SplitSeq(text, "== ") {
		file, raw, ok := strings.Cut(chunk, "\n")
		file = strings.TrimSuffix(file, "\r")
		if ok && slices.Contains(files, file) {
			if cert, err := ParseCertificate(file, raw); err == nil {
				certs[file] = cert
			}
		}
	}
	var result []Certificate
	var failed []string
	for _, file := range files {
		if cert, ok := certs[file]; ok {
			result = append(result, cert)
		} else {
			failed = append(failed, file)
		}
	}
	return result, failed
}

func CertificateRemoteCommand(host string) string {
	if !ValidSiteHost(host) {
		return ""
	}
	return SystemCommand("openssl", "s_client", "-connect", net.JoinHostPort(host, "443"), "-servername", host) +
		" </dev/null 2>/dev/null | " + SystemCommand("openssl", "x509", "-noout", "-dates", "-subject", "-issuer")
}

func ParseRemoteCertificate(text string) (Certificate, error) {
	var cert Certificate
	for line := range strings.SplitSeq(text, "\n") {
		key, value, _ := strings.Cut(strings.TrimSpace(line), "=")
		switch key {
		case "subject":
			cert.Subject = value
		case "issuer":
			cert.Issuer = value
		case "notBefore":
			cert.NotBefore, _ = time.Parse("Jan 2 15:04:05 2006 MST", value)
		case "notAfter":
			cert.NotAfter, _ = time.Parse("Jan 2 15:04:05 2006 MST", value)
		}
	}
	if cert.NotAfter.IsZero() || cert.NotBefore.IsZero() {
		return Certificate{}, errors.New("server did not return a TLS certificate")
	}
	return cert, nil
}

func CertificateLocal(ctx context.Context, host string) (Certificate, error) {
	if !ValidSiteHost(host) {
		return Certificate{}, errors.New("invalid TLS domain")
	}
	dialer := tls.Dialer{NetDialer: &net.Dialer{Timeout: 8 * time.Second}, Config: &tls.Config{
		ServerName: host, MinVersion: tls.VersionTLS12, InsecureSkipVerify: true,
	}}
	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(host, "443"))
	if err != nil {
		return Certificate{}, err
	}
	defer conn.Close()
	state := conn.(*tls.Conn).ConnectionState()
	if len(state.PeerCertificates) == 0 {
		return Certificate{}, errors.New("peer did not present a TLS certificate")
	}
	leaf := state.PeerCertificates[0]
	options := x509.VerifyOptions{DNSName: host, Intermediates: x509.NewCertPool()}
	for _, cert := range state.PeerCertificates[1:] {
		options.Intermediates.AddCert(cert)
	}
	_, err = leaf.Verify(options)
	return certificateFromX509("", leaf), err
}

const CertbotListCommand = "'certbot' 'certificates' '--non-interactive'"
const CertbotPluginsCommand = "'certbot' 'plugins' '--non-interactive'"

const CertbotScheduleCommand = `
for timer in 'certbot.timer' 'snap.certbot.renew.timer' 'certbot-renew.timer'; do
    data=$('systemctl' 'show' '--property=LoadState,ActiveState,UnitFileState' "$timer" 2>/dev/null) || continue
    if 'printf' '%s\n' "$data" | 'grep' '-qx' 'LoadState=loaded'; then
        'printf' '%s: ' "$timer"
        'printf' '%s\n' "$data" | 'awk' '-F=' '/^(ActiveState|UnitFileState)=/ { printf "%s%s", sep, $2; sep=" / " } END { print "" }'
    fi
done
for file in '/etc/cron.d/certbot' '/etc/cron.d/certbot-renew' '/etc/cron.daily/certbot' '/etc/cron.d/acme.sh'; do
    if [ -f "$file" ]; then 'printf' '%s\n' "$file"; fi
done
if [ -x "$HOME/.acme.sh/acme.sh" ]; then 'printf' 'acme.sh\n'; fi
rootcron=$('crontab' '-l' 2>/dev/null) || :
if 'printf' '%s\n' "$rootcron" | 'grep' '-Eq' '^[[:space:]]*[^#[:space:]].*certbot'; then 'printf' 'root cron: certbot\n'; fi
if 'printf' '%s\n' "$rootcron" | 'grep' '-Eq' '^[[:space:]]*[^#[:space:]].*acme\.sh'; then 'printf' 'root cron: acme.sh\n'; fi
:
`

type CertbotCertificate struct {
	Name, Status, Path, KeyType string
	Domains                     []string
	Expiry                      time.Time
}

func ParseCertbot(text string) []CertbotCertificate {
	var certs []CertbotCertificate
	for line := range strings.SplitSeq(text, "\n") {
		key, value, _ := strings.Cut(strings.TrimSpace(line), ":")
		value = strings.TrimSpace(value)
		if key == "Certificate Name" && value != "" {
			certs = append(certs, CertbotCertificate{Name: value})
			continue
		}
		if len(certs) == 0 {
			continue
		}
		cert := &certs[len(certs)-1]
		switch key {
		case "Domains":
			cert.Domains = strings.Fields(value)
		case "Certificate Path":
			if ValidNginxPath(value) {
				cert.Path = value
			}
		case "Key Type":
			cert.KeyType = value
		case "Expiry Date":
			date, status, _ := strings.Cut(value, " (")
			cert.Status = strings.TrimSuffix(status, ")")
			for _, layout := range []string{"2006-01-02 15:04:05-07:00", "2006-01-02 15:04:05+00:00", time.RFC3339} {
				if parsed, err := time.Parse(layout, date); err == nil {
					cert.Expiry = parsed
					break
				}
			}
		}
	}
	return certs
}

func CertbotHasNginx(text string) bool {
	for line := range strings.SplitSeq(text, "\n") {
		if strings.TrimSpace(line) == "* nginx" {
			return true
		}
	}
	return false
}

func CertbotRenewCommand(dry bool) string {
	if dry {
		return SystemCommand("certbot", "renew", "--dry-run", "--non-interactive")
	}
	return SystemCommand("certbot", "renew", "--non-interactive")
}

func CertbotIssueCommand(domains []string, email string) string {
	if len(domains) == 0 || len(domains) > 30 || len(email) > 254 {
		return ""
	}
	parsed, err := mail.ParseAddress(email)
	if err != nil || parsed.Address != email || strings.ContainsAny(email, "\r\n\t \"'") || strings.HasPrefix(email, "-") {
		return ""
	}
	args := []string{"certbot", "--nginx", "--non-interactive", "--agree-tos", "--no-redirect", "--email", email}
	seen := map[string]bool{}
	for _, domain := range domains {
		key := strings.ToLower(domain)
		if !ValidSiteHost(domain) || seen[key] {
			return ""
		}
		seen[key] = true
		args = append(args, "-d", domain)
	}
	return SystemCommand(args...)
}
