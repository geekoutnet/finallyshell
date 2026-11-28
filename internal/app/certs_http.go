package app

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"finallyshell/internal/monitor"
)

// The connected server answering Let's Encrypt's HTTP challenges for the
// SSL certificates app, as Caddy does on its own server: a file in the
// document root of the Nginx site of the domain, else in the folder of a
// short-lived responder on a free port 80, else (once the user agrees) in
// the folder of a temporary Nginx location. Each is tried first with a
// probe file fetched from this computer over the domain's address, so
// that the CA is asked only when it will find the file.

type certHTTPMode int

// How a server serves the challenges; the UI names each one.
const (
	certHTTPWebroot   certHTTPMode = iota + 1 // a file in a site's document root
	certHTTPResponder                         // a responder of the app's own on a free port 80
	certHTTPCaddy                             // a temporary route through Caddy's admin API
	certHTTPLocation                          // a temporary Nginx location (confirmed)
)

// certFetcher reads a URL's body: certFetch, or a test's.
type certFetcher func(ctx context.Context, url string) (string, error)

// certHTTP serves a certificate's challenges from a server: a
// challenge.Provider for certs.Issuer.
type certHTTP struct {
	ctx  context.Context
	run  systemRunner // as root
	mode certHTTPMode
	// roots is the folder each domain's files go in.
	roots map[string]string
	// The responder's process and folder.
	pid int
	dir string
	// The location's change, undone by close.
	change *monitor.NginxChange
	tree   monitor.NginxTree
	status monitor.NginxStatus
}

func (h *certHTTP) Present(domain, token, keyAuth string) error {
	script, err := monitor.ACMEWriteCommand(h.roots[domain], token, keyAuth)
	if err != nil {
		return err
	}
	_, stderr, err := h.run(h.ctx, script, nil, nil)
	return certRunError(stderr, err)
}

func (h *certHTTP) CleanUp(domain, token, _ string) error {
	script, err := monitor.ACMERemoveCommand(h.roots[domain], token)
	if err != nil {
		return err
	}
	_, stderr, err := h.run(h.ctx, script, nil, nil)
	return certRunError(stderr, err)
}

// close undoes what serving the challenges set up: stops the responder,
// or takes the location out and reloads Nginx. It runs with its own
// time, as the issuance's may be over.
func (h *certHTTP) close() error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	switch h.mode {
	case certHTTPResponder:
		script, err := monitor.ACMEResponderStopCommand(h.pid, h.dir)
		if err != nil {
			return err
		}
		_, stderr, err := h.run(ctx, script, nil, nil)
		return certRunError(stderr, err)
	case certHTTPLocation:
		if h.change == nil {
			return nil
		}
		return certUndoLocation(ctx, h.run, *h.change, h.tree, h.status)
	}
	return nil
}

// certHTTPFound is how the server can serve a certificate's challenges:
// h, ready; or confirm, a change that waits for the user's go-ahead; or
// neither, with err, one of the errCertHTTP errors (wrapped), which the UI
// explains. servers are the web servers found on port 80.
type certHTTPFound struct {
	h       *certHTTP
	confirm *certHTTPConfirm
	err     error
	servers []string
}

// certHTTPConfirm is a change to the server the user agrees to first: of
// kind, to target (a file, or the firewall's name); apply makes it, checks
// it and gives what then serves the challenges, everything undone on an
// error.
type certHTTPConfirm struct {
	kind   certConfirmKind
	target string
	apply  func(ctx context.Context) (*certHTTP, error)
	// For tests: the Nginx location's server blocks.
	lines []int
}

type certConfirmKind int

const (
	certConfirmNginx    certConfirmKind = iota + 1 // a temporary location in target, an Nginx file
	certConfirmFirewall                            // port 80 opened in target (ufw, firewalld) for a while
)

// Why a server cannot serve the challenges, for the user.
var (
	errCertHTTPPort80   = errors.New("port 80 is used by another program")
	errCertHTTPNoServer = errors.New("the server has neither Python 3 nor BusyBox to answer on port 80")
	errCertHTTPProbe    = errors.New("the domain does not reach this server over HTTP")
	errCertHTTPFirewall = errors.New("the server's firewall blocks port 80")
)

// certFindHTTP looks for a way for the server run reaches (as root) to
// serve the challenges of domains, none a wildcard.
func certFindHTTP(ctx context.Context, run systemRunner, domains []string, fetch certFetcher) certHTTPFound {
	var found certHTTPFound
	var inv monitor.NginxInventory
	status, _ := readSiteStatus(ctx, run)
	portOut, _, portErr := run(ctx, monitor.ACMEPort80Command, nil, nil)
	busy, servers := monitor.ParsePort80(portOut)
	found.servers = servers
	if portErr != nil {
		busy = true // unknown: no responder
	}
	nginx := status.Installed && !status.Custom && status.State == "active"
	if nginx {
		var err error
		inv, err = monitor.ReadNginxInventory(ctx, func(ctx context.Context, script string) (string, string, error) {
			return run(ctx, script, nil, nil)
		})
		if err != nil {
			nginx = false
		}
	}
	reason := errCertHTTPProbe
	// The site's own folder: nothing changes but a file.
	if nginx {
		roots := map[string]string{}
		for _, d := range domains {
			for _, root := range certWebroots(inv, d) {
				if certProbe(ctx, run, fetch, d, root) == nil {
					roots[d] = root
					break
				}
			}
		}
		if len(roots) == len(domains) {
			found.h = &certHTTP{ctx: ctx, run: run, mode: certHTTPWebroot, roots: roots}
			return found
		}
	}
	// A responder of its own on a free port 80.
	if !busy {
		out, _, err := run(ctx, monitor.ACMEResponderStartCommand, nil, nil)
		pid, dir, ok := monitor.ParseACMEResponder(out)
		switch {
		case err == nil && ok:
			h := &certHTTP{ctx: ctx, run: run, mode: certHTTPResponder, pid: pid, dir: dir, roots: map[string]string{}}
			for _, d := range domains {
				h.roots[d] = dir
			}
			if certProbeAll(ctx, h, fetch, domains) == nil {
				found.h = h
				return found
			}
			h.close()
		case strings.TrimSpace(out) == "none":
			reason = errCertHTTPNoServer
		}
	} else if !slices.Contains(servers, "nginx") {
		reason = errCertHTTPPort80
	}
	// A location for the challenges in the site's server blocks.
	if nginx && slices.Contains(servers, "nginx") {
		if file, lines := certLocationTarget(inv, domains); file != "" {
			found.confirm = &certHTTPConfirm{kind: certConfirmNginx, target: file, lines: lines,
				apply: func(ctx context.Context) (*certHTTP, error) {
					return certApplyLocation(ctx, run, file, lines, inv, status, domains, fetch)
				}}
			return found
		}
	}
	found.err = reason
	return found
}

// certApplyLocation adds a temporary location to the server blocks on
// lines of file, reloads Nginx
// and checks it with probes: what then serves the challenges, or an
// error, everything undone.
func certApplyLocation(ctx context.Context, run systemRunner, file string, lines []int, inv monitor.NginxInventory, status monitor.NginxStatus, domains []string, fetch certFetcher) (*certHTTP, error) {
	read, err := monitor.NginxReadCommand(file)
	if err != nil {
		return nil, err
	}
	original, stderr, err := run(ctx, read, nil, nil)
	if err != nil {
		return nil, certRunError(stderr, err)
	}
	text, err := monitor.NginxACMELocation(original, lines)
	if err != nil {
		return nil, err
	}
	change, err := monitor.NewNginxChange(file, original, text, time.Now(), inv.NginxTree)
	if err != nil {
		return nil, err
	}
	save, err := change.SaveCommand(original, inv.NginxTree)
	if err != nil {
		return nil, err
	}
	reload, err := monitor.NginxActionCommand("reload", status.Unit, inv.Main)
	if err != nil {
		return nil, err
	}
	if _, stderr, err := run(ctx, monitor.ACMERootCommand, nil, nil); err != nil {
		return nil, certRunError(stderr, err)
	}
	out, stderr, err := run(ctx, save, strings.NewReader(text), nil)
	if err != nil || strings.TrimSpace(out) != "finallyshell-written" {
		if err == nil {
			err = errors.New("the Nginx file was not written")
		}
		return nil, certRunError(stderr, err)
	}
	h := &certHTTP{ctx: ctx, run: run, mode: certHTTPLocation, roots: map[string]string{},
		change: &change, tree: inv.NginxTree, status: status}
	for _, d := range domains {
		h.roots[d] = monitor.ACMERoot
	}
	if _, stderr, err := run(ctx, reload, nil, nil); err != nil {
		h.close()
		return nil, certRunError(stderr, err)
	}
	// A reload returns before the new workers take the requests: the
	// probes are tried for a while.
	var probeErr error
	for range 10 {
		if probeErr = certProbeAll(ctx, h, fetch, domains); probeErr == nil {
			return h, nil
		}
		select {
		case <-ctx.Done():
			h.close()
			return nil, ctx.Err()
		case <-time.After(time.Second):
		}
	}
	h.close()
	return nil, probeErr
}

// certUndoLocation puts the file change changed back from its backup, and
// reloads Nginx.
func certUndoLocation(ctx context.Context, run systemRunner, change monitor.NginxChange, tree monitor.NginxTree, status monitor.NginxStatus) error {
	restore, err := change.RestoreCommand(tree, time.Now())
	if err != nil {
		return err
	}
	out, stderr, err := run(ctx, restore, nil, nil)
	if err != nil || strings.TrimSpace(out) != "finallyshell-written" {
		if err == nil {
			err = errors.New("the Nginx file was not restored")
		}
		return certRunError(stderr, err)
	}
	reload, err := monitor.NginxActionCommand("reload", status.Unit, tree.Main)
	if err != nil {
		return err
	}
	_, stderr, err = run(ctx, reload, nil, nil)
	return certRunError(stderr, err)
}

func certProbeAll(ctx context.Context, h *certHTTP, fetch certFetcher, domains []string) error {
	for _, d := range domains {
		if err := certProbe(ctx, h.run, fetch, d, h.roots[d]); err != nil {
			return err
		}
	}
	return nil
}

// certProbe puts a probe file in root and fetches it over domain's
// address, as the CA will: nil when it comes back.
func certProbe(ctx context.Context, run systemRunner, fetch certFetcher, domain, root string) error {
	token, keyAuth := certRandom(), certRandom()+"."+certRandom()
	write, err := monitor.ACMEWriteCommand(root, token, keyAuth)
	if err != nil {
		return err
	}
	if _, stderr, err := run(ctx, write, nil, nil); err != nil {
		return certRunError(stderr, err)
	}
	defer func() {
		if remove, err := monitor.ACMERemoveCommand(root, token); err == nil {
			run(context.WithoutCancel(ctx), remove, nil, nil)
		}
	}()
	body, err := fetch(ctx, "http://"+domain+monitor.ACMEPath+token)
	if err != nil {
		return fmt.Errorf("%w: %v", errCertHTTPProbe, err)
	}
	if strings.TrimSpace(body) != keyAuth {
		return errCertHTTPProbe
	}
	return nil
}

func certRandom() string {
	b := make([]byte, 18)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// certFetch reads url as Let's Encrypt does: following redirects, to
// HTTPS too, without checking its certificate.
func certFetch(ctx context.Context, url string) (string, error) {
	client := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{
		Proxy: http.ProxyFromEnvironment, TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return string(b), err
}

// certWebroots are the document roots that may answer domain on port 80:
// those of the sites named after it, then those of the default servers.
func certWebroots(inv monitor.NginxInventory, domain string) []string {
	var named, fallback []string
	for _, site := range inv.Sites {
		if !certServes80(site) || site.Root == "" || !monitor.ValidNginxPath(site.Root) {
			continue
		}
		switch {
		case certNameMatch(site.Names, domain):
			if !slices.Contains(named, site.Root) {
				named = append(named, site.Root)
			}
		case slices.ContainsFunc(site.Listen, func(l monitor.NginxListen) bool { return l.Default && l.Port == 80 }):
			if !slices.Contains(fallback, site.Root) {
				fallback = append(fallback, site.Root)
			}
		}
	}
	for _, root := range fallback {
		if !slices.Contains(named, root) {
			named = append(named, root)
		}
	}
	return named
}

// certServes80 says whether site answers plain HTTP on port 80: one with
// no listen does.
func certServes80(site monitor.NginxSite) bool {
	if len(site.Listen) == 0 {
		return true
	}
	return slices.ContainsFunc(site.Listen, func(l monitor.NginxListen) bool { return l.Port == 80 && !l.TLS })
}

// certNameMatch says whether one of names, a site's server_name, takes
// domain: the same name, "*.parent" or ".parent" (regular expressions are
// not followed).
func certNameMatch(names []string, domain string) bool {
	domain = strings.ToLower(domain)
	for _, n := range names {
		n = strings.ToLower(n)
		switch {
		case n == domain:
			return true
		case strings.HasPrefix(n, "*.") && strings.HasSuffix(domain, n[1:]) && !strings.Contains(strings.TrimSuffix(domain, n[1:]), "."):
			return true
		case strings.HasPrefix(n, ".") && (domain == n[1:] || strings.HasSuffix(domain, n)):
			return true
		}
	}
	return false
}

// certLocationTarget is the file and the lines of the server blocks of
// domains, where a temporary location goes: all in one file Nginx's
// editor may change, or "".
func certLocationTarget(inv monitor.NginxInventory, domains []string) (string, []int) {
	file := ""
	var lines []int
	for _, d := range domains {
		matched := false
		for _, site := range inv.Sites {
			if !certNameMatch(site.Names, d) {
				continue
			}
			// A site enabled by a link in sites-enabled is changed in
			// sites-available, the file the link points at.
			target, _, _ := inv.Target(site)
			if file == "" {
				file = target
			}
			if target != file {
				return "", nil
			}
			if !slices.Contains(lines, site.Line) {
				lines = append(lines, site.Line)
			}
			matched = true
		}
		if !matched {
			return "", nil
		}
	}
	if file == "" || !monitor.NginxEditable(file) {
		return "", nil
	}
	slices.Sort(lines)
	return file, lines
}

// certRunError is err with the server's message, nil for none.
func certRunError(stderr string, err error) error {
	if err == nil {
		return nil
	}
	if msg := strings.TrimSpace(siteClean(stderr)); msg != "" {
		return fmt.Errorf("%w: %s", err, msg)
	}
	return err
}
