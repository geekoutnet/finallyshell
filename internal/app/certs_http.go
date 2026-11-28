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
// SSL certificates app, as Caddy does on its own server, without stopping
// or disturbing what runs there. In this order, from no change to the
// most: a file in a folder a web server on port 80 already serves (an
// Nginx site's, Caddy's, Apache's or a usual one); a short-lived responder
// of the app's own on a free port 80 (Python 3, BusyBox or Perl), with
// the firewall opened for a while once the user agrees when it blocks
// port 80; a temporary route through Caddy's admin API when Caddy holds
// port 80; else, once the user agrees, the split of port 80
// (monitor/acme_split.go), which answers the challenges and passes every
// other connection to whatever holds port 80 (Nginx, Apache, Docker,
// Traefik or another), which is never stopped. Each way is tried first
// with a probe file fetched from this computer over the domain's address,
// so that the CA is asked only when it will find the file, and close
// undoes everything.

type certHTTPMode int

// How a server serves the challenges; the UI names each one.
const (
	certHTTPWebroot   certHTTPMode = iota + 1 // a file in a site's document root
	certHTTPResponder                         // a responder of the app's own on a free port 80
	certHTTPCaddy                             // a temporary route through Caddy's admin API
	certHTTPSplit                             // the split of port 80 (confirmed)
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
	// The routes put in Caddy, by @id.
	caddy       monitor.CaddyInstall
	caddyRoutes []string
	// The split's proxy, in dir, on splitPort; its rules too.
	splitPID, splitPort int
	// firewall is the firewall port 80 was opened in.
	firewall string
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
// takes the Caddy routes out, undoes the split (its rules, then its
// proxy), and closes the firewall again. It runs with its own time, as the
// issuance's may be over, and goes through every step whatever fails.
func (h *certHTTP) close() error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	var errs []error
	if h.pid > 0 {
		if script, err := monitor.ACMEResponderStopCommand(h.pid, h.dir); err != nil {
			errs = append(errs, err)
		} else if _, stderr, err := h.run(ctx, script, nil, nil); err != nil {
			errs = append(errs, certRunError(stderr, err))
		}
		h.pid = 0
	}
	for _, id := range h.caddyRoutes {
		if target, err := monitor.ACMECaddyRouteTarget(id); err == nil {
			if _, err := monitor.CaddyAdmin(ctx, monitor.CaddyRunner(h.run), h.caddy, "DELETE", target, ""); err != nil {
				errs = append(errs, err)
			}
		}
	}
	h.caddyRoutes = nil
	if h.splitPID > 0 {
		if script, err := monitor.ACMESplitStopCommand(h.splitPID, h.dir, h.splitPort); err != nil {
			errs = append(errs, err)
		} else if out, stderr, err := h.run(ctx, script, nil, nil); err != nil {
			errs = append(errs, certRunError(stderr, err))
		} else if strings.TrimSpace(out) != "left 0" {
			errs = append(errs, fmt.Errorf("the split's firewall rules are still there: %s", strings.TrimSpace(out)))
		}
		h.splitPID = 0
	}
	if h.firewall != "" {
		if script, err := monitor.ACMEFirewallCloseCommand(h.firewall); err != nil {
			errs = append(errs, err)
		} else if _, stderr, err := h.run(ctx, script, nil, nil); err != nil {
			errs = append(errs, certRunError(stderr, err))
		}
		h.firewall = ""
	}
	return errors.Join(errs...)
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
// kind, through target (the firewall, "ufw" or "firewalld"; or the tool
// the split's rule goes in, "iptables" or "nft"); apply makes it, checks
// it and gives what then serves the challenges, everything undone on an
// error.
type certHTTPConfirm struct {
	kind   certConfirmKind
	target string
	apply  func(ctx context.Context) (*certHTTP, error)
}

type certConfirmKind int

const (
	// certConfirmSplit splits port 80 for a minute or so: the program on
	// it (certHTTPFound.servers) sees its visitors come from the server's
	// own address meanwhile.
	certConfirmSplit    certConfirmKind = iota + 1
	certConfirmFirewall                 // port 80 opened in target (ufw, firewalld) for a while
)

// Why a server cannot serve the challenges, for the user.
var (
	errCertHTTPPort80   = errors.New("port 80 is used by another program")
	errCertHTTPNoServer = errors.New("the server has neither Python 3, BusyBox nor Perl to answer on port 80")
	errCertHTTPProbe    = errors.New("the domain does not reach this server over HTTP")
	errCertHTTPFirewall = errors.New("the server's firewall blocks port 80")
	// errCertHTTPICP: a page a Chinese cloud puts in front of a site whose
	// domain has no ICP filing (备案) came back instead of the file; only
	// DNS proves the domain then.
	errCertHTTPICP = errors.New("the cloud provider blocks the domain until it has an ICP filing")
)

// certServer is what certFindHTTP learns of a server first.
type certServer struct {
	status monitor.NginxStatus
	// nginx says whether Nginx runs, with inv its sites.
	nginx bool
	inv   monitor.NginxInventory
	// busy says whether something listens on port 80, programs what.
	busy     bool
	programs []string
	survey   monitor.ACMESurvey
	// Caddy, when its admin API answers: its HTTP servers and the folders
	// they serve.
	caddy        monitor.CaddyInstall
	caddyServers []monitor.CaddyServer
	caddyRoots   []string
	// upstream is the address the split reaches port 80's program at.
	upstream string
	// probeErr is the last error of the probes of the folders.
	probeErr error
}

// certLookAt learns what serves port 80 on the server run reaches.
func certLookAt(ctx context.Context, run systemRunner) certServer {
	var srv certServer
	srv.status, _ = readSiteStatus(ctx, run)
	out, _, err := run(ctx, monitor.ACMEPort80Command, nil, nil)
	srv.busy, srv.programs = monitor.ParsePort80(out)
	srv.upstream = monitor.ParsePort80Upstream(out)
	if err != nil {
		srv.busy = true // unknown: no responder of its own
		srv.upstream = ""
	}
	srv.nginx = srv.status.Installed && !srv.status.Custom && srv.status.State == "active"
	if srv.nginx {
		var err error
		srv.inv, err = monitor.ReadNginxInventory(ctx, func(ctx context.Context, script string) (string, string, error) {
			return run(ctx, script, nil, nil)
		})
		srv.nginx = err == nil
	}
	if out, _, err := run(ctx, monitor.ACMESurveyCommand, nil, nil); err == nil {
		srv.survey = monitor.ParseACMESurvey(out)
	}
	if slices.Contains(srv.programs, "caddy") || srv.status.Caddy != "" {
		caddyRun := monitor.CaddyRunner(run)
		if inst, err := monitor.DetectCaddy(ctx, caddyRun); err == nil && inst.AdminAddr != "off" {
			if out, err := monitor.CaddyAdmin(ctx, caddyRun, inst, "GET", monitor.ACMECaddyServers, ""); err == nil {
				if servers, roots, err := monitor.ParseCaddyServers(out); err == nil {
					srv.caddy, srv.caddyServers, srv.caddyRoots = inst, servers, roots
				}
			}
		}
	}
	return srv
}

// certMaxRoots is how many folders are tried for a domain.
const certMaxRoots = 8

// certRootsFor are the folders that may serve domain on port 80, the
// likeliest first: its Nginx site's, Nginx's default server's, then
// Caddy's, Apache's and the usual ones.
func certRootsFor(srv certServer, domain string) []string {
	var roots []string
	add := func(list []string) {
		for _, r := range list {
			if len(roots) < certMaxRoots && !slices.Contains(roots, r) {
				roots = append(roots, r)
			}
		}
	}
	if srv.nginx {
		add(certWebroots(srv.inv, domain))
	}
	add(srv.caddyRoots)
	add(srv.survey.Roots)
	return roots
}

// certFindHTTP looks for a way for the server run reaches (as root) to
// serve the challenges of domains, none a wildcard.
func certFindHTTP(ctx context.Context, run systemRunner, domains []string, fetch certFetcher) certHTTPFound {
	srv := certLookAt(ctx, run)
	return srv.find(ctx, run, domains, fetch)
}

func (srv certServer) find(ctx context.Context, run systemRunner, domains []string, fetch certFetcher) certHTTPFound {
	found := certHTTPFound{servers: srv.programs}
	// A folder a web server on port 80 serves: nothing changes but a file.
	if srv.busy {
		roots := map[string]string{}
		for _, d := range domains {
			for _, root := range certRootsFor(srv, d) {
				err := certProbe(ctx, run, fetch, d, root)
				if err == nil {
					roots[d] = root
					break
				}
				srv.probeErr = err
				if errors.Is(err, errCertHTTPICP) {
					break // the cloud answers, not the server
				}
			}
		}
		if len(roots) == len(domains) {
			found.h = &certHTTP{ctx: ctx, run: run, mode: certHTTPWebroot, roots: roots}
			return found
		}
	}
	// A responder of its own on a free port 80, the firewall opened first
	// when it blocks the port and the user agrees.
	if !srv.busy {
		h, err := certStartResponder(ctx, run, fetch, domains)
		switch {
		case h != nil:
			found.h = h
		case errors.Is(err, errCertHTTPNoServer), errors.Is(err, errCertHTTPICP):
			found.err = err
		case srv.survey.Firewall != "" && srv.survey.Blocked:
			fw := srv.survey.Firewall
			found.confirm = &certHTTPConfirm{kind: certConfirmFirewall, target: fw,
				apply: func(ctx context.Context) (*certHTTP, error) {
					return certOpenFirewall(ctx, run, fw, domains, fetch)
				}}
		default:
			found.err = err
		}
		return found
	}
	// Caddy on port 80: a route of its own through the admin API.
	if slices.Contains(srv.programs, "caddy") && len(srv.caddyServers) > 0 && srv.caddy.Method != monitor.CaddyDocker {
		if h, err := certCaddyRoute(ctx, run, fetch, domains, srv); err == nil {
			found.h = h
			return found
		}
	}
	if errors.Is(srv.probeErr, errCertHTTPICP) {
		found.err = srv.probeErr
		return found
	}
	// Whatever else holds port 80: the split, once the user agrees.
	if upstream := srv.upstream; srv.survey.Python3 && srv.survey.Redirect != "" && upstream != "" {
		found.confirm = &certHTTPConfirm{kind: certConfirmSplit, target: srv.survey.Redirect,
			apply: func(ctx context.Context) (*certHTTP, error) {
				return certSplit(ctx, run, fetch, domains, upstream)
			}}
		return found
	}
	switch {
	case srv.survey.Firewall != "" && srv.survey.Blocked:
		found.err = fmt.Errorf("%w (%s)", errCertHTTPFirewall, srv.survey.Firewall)
	default:
		holders := strings.Join(srv.programs, ", ")
		if holders == "" {
			holders = "unknown"
		}
		found.err = fmt.Errorf("%w: %s", errCertHTTPPort80, holders)
	}
	return found
}

// certSplit splits port 80, passing other connections to port 80 of
// upstream, and checks it with probes: what then serves the challenges,
// or an error, everything undone.
func certSplit(ctx context.Context, run systemRunner, fetch certFetcher, domains []string, upstream string) (*certHTTP, error) {
	start, err := monitor.ACMESplitStartCommand(upstream)
	if err != nil {
		return nil, err
	}
	out, stderr, err := run(ctx, start, nil, nil)
	if err != nil {
		return nil, certRunError(stderr, err)
	}
	pid, dir, port, ok := monitor.ParseACMESplit(out)
	if !ok {
		if strings.TrimSpace(out) == "none" {
			return nil, errCertHTTPNoServer
		}
		return nil, certRunError(stderr, errors.New("the split of port 80 did not start"))
	}
	h := &certHTTP{ctx: ctx, run: run, mode: certHTTPSplit, splitPID: pid, splitPort: port, dir: dir, roots: map[string]string{}}
	for _, d := range domains {
		h.roots[d] = dir
	}
	rules, err := monitor.ACMESplitRulesCommand(port)
	if err != nil {
		h.close()
		return nil, err
	}
	if out, stderr, err := run(ctx, rules, nil, nil); err != nil || !strings.HasPrefix(strings.TrimSpace(out), "rules ") {
		h.close()
		if err == nil {
			err = errors.New("the server has neither iptables nor nft")
		}
		return nil, certRunError(stderr, err)
	}
	var probeErr error
	for range 3 {
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

// certStartResponder starts a responder on the free port 80 and checks it
// with probes: what then serves the challenges, or errCertHTTPNoServer
// (wrapped) when none starts, or the probe's error, the responder stopped.
func certStartResponder(ctx context.Context, run systemRunner, fetch certFetcher, domains []string) (*certHTTP, error) {
	out, stderr, err := run(ctx, monitor.ACMEResponderStartCommand, nil, nil)
	if err != nil {
		return nil, certRunError(stderr, err)
	}
	if strings.TrimSpace(out) == "none" {
		return nil, errCertHTTPNoServer
	}
	pid, dir, ok := monitor.ParseACMEResponder(out)
	if !ok {
		return nil, fmt.Errorf("%w: the responder did not start", errCertHTTPNoServer)
	}
	h := &certHTTP{ctx: ctx, run: run, mode: certHTTPResponder, pid: pid, dir: dir, roots: map[string]string{}}
	for _, d := range domains {
		h.roots[d] = dir
	}
	if err := certProbeAll(ctx, h, fetch, domains); err != nil {
		h.close()
		return nil, err
	}
	return h, nil
}

// certOpenFirewall lets port 80 in through firewall for a while, then
// starts a responder: what then serves the challenges, which closes the
// firewall again, or an error, everything undone.
func certOpenFirewall(ctx context.Context, run systemRunner, firewall string, domains []string, fetch certFetcher) (*certHTTP, error) {
	open, err := monitor.ACMEFirewallOpenCommand(firewall)
	if err != nil {
		return nil, err
	}
	out, stderr, err := run(ctx, open, nil, nil)
	if err != nil {
		return nil, certRunError(stderr, err)
	}
	opened := strings.TrimSpace(out) == "opened"
	h, err := certStartResponder(ctx, run, fetch, domains)
	if err != nil {
		if opened {
			(&certHTTP{run: run, firewall: firewall}).close()
		}
		return nil, err
	}
	if opened {
		h.firewall = firewall
	}
	return h, nil
}

// certCaddyRoute puts a route for the challenges at the start of each of
// Caddy's HTTP servers through its admin API, and checks it with probes:
// what then serves the challenges, or an error, the routes taken out.
func certCaddyRoute(ctx context.Context, run systemRunner, fetch certFetcher, domains []string, srv certServer) (*certHTTP, error) {
	if _, stderr, err := run(ctx, monitor.ACMERootCommand, nil, nil); err != nil {
		return nil, certRunError(stderr, err)
	}
	caddyRun := monitor.CaddyRunner(run)
	h := &certHTTP{ctx: ctx, run: run, mode: certHTTPCaddy, caddy: srv.caddy, roots: map[string]string{}}
	for _, d := range domains {
		h.roots[d] = monitor.ACMERoot
	}
	for i, server := range srv.caddyServers {
		id := monitor.CaddyRouteID(i)
		target, body, err := monitor.ACMECaddyRoute(server, id)
		if err != nil {
			h.close()
			return nil, err
		}
		// One a stopped app left behind would make the ID a duplicate.
		if old, err := monitor.ACMECaddyRouteTarget(id); err == nil {
			monitor.CaddyAdmin(ctx, caddyRun, srv.caddy, "DELETE", old, "")
		}
		if _, err := monitor.CaddyAdmin(ctx, caddyRun, srv.caddy, "PUT", target, body); err != nil {
			h.close()
			return nil, err
		}
		h.caddyRoutes = append(h.caddyRoutes, id)
	}
	var err error
	for range 3 {
		if err = certProbeAll(ctx, h, fetch, domains); err == nil {
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
	return nil, err
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
	if strings.TrimSpace(body) == keyAuth && err == nil {
		return nil
	}
	if certICPPage(body) {
		return errCertHTTPICP
	}
	if err != nil {
		return fmt.Errorf("%w: %v", errCertHTTPProbe, err)
	}
	return errCertHTTPProbe
}

// certICPPage says whether body is the page a Chinese cloud (Alibaba,
// Tencent, Huawei…) shows for a domain with no ICP filing.
func certICPPage(body string) bool {
	head := strings.ToLower(body)
	if !strings.Contains(head, "<html") && !strings.Contains(head, "<!doctype") && !strings.Contains(head, "<head") {
		return false
	}
	for _, mark := range []string{"备案", "icp", "beian", "未备案", "网站暂时无法访问"} {
		if strings.Contains(head, mark) {
			return true
		}
	}
	return false
}

func certRandom() string {
	b := make([]byte, 18)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// certFetch reads url as Let's Encrypt does: following redirects, to
// HTTPS too, without checking its certificate. The start of the body
// comes back with the error of a status but 200, for the probe to tell a
// cloud's ICP page.
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
	b, err := io.ReadAll(io.LimitReader(resp.Body, 16<<10))
	if resp.StatusCode != http.StatusOK {
		return string(b), fmt.Errorf("HTTP %d", resp.StatusCode)
	}
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
