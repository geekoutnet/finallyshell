package caddyfile

import (
	"encoding/base64"
	"net/netip"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
)

func validateSite(spec SiteSpec) error {
	if len(spec.Addresses) == 0 || !allSiteValues(spec.Addresses, siteAddress) {
		return SiteFieldError{"addresses"}
	}
	switch spec.Type {
	case Static, SPA, Browser, PHP:
		if !siteAbsolutePath(spec.Root) {
			return SiteFieldError{"root"}
		}
		if spec.Type == PHP && !siteUpstream(spec.PHPUpstream) {
			return SiteFieldError{"PHP upstream"}
		}
	case Proxy:
		if len(spec.Upstreams) == 0 || !allSiteValues(spec.Upstreams, siteUpstream) {
			return SiteFieldError{"upstreams"}
		}
		if !validProxy(spec.Proxy, spec.Upstreams) {
			return SiteFieldError{"reverse proxy"}
		}
	case Redirect:
		if !validRedirect(spec.RedirectURL) {
			return SiteFieldError{"redirect URL"}
		}
		if !slices.Contains([]string{"", "301", "302", "307", "308"}, spec.RedirectCode) {
			return SiteFieldError{"redirect status"}
		}
	case Respond:
		if spec.Status != "" && !siteInteger(spec.Status, 100, 599) {
			return SiteFieldError{"response status"}
		}
	default:
		return SiteFieldError{"type"}
	}
	if !allSiteValues(spec.Encode, func(value string) bool { return value == "gzip" || value == "zstd" }) {
		return SiteFieldError{"compression"}
	}
	if !validHeaders(spec.siteHeaders()) {
		return SiteFieldError{"headers"}
	}
	if !validCORS(spec.CORS) {
		return SiteFieldError{"CORS"}
	}
	if len(spec.CORS.Origins) > 0 && slices.ContainsFunc(spec.Headers, func(header Header) bool {
		name := strings.ToLower(strings.TrimLeft(header.Name, "+-?>"))
		return strings.HasPrefix(name, "access-control-") || strings.Contains(name, "*")
	}) {
		return SiteFieldError{"CORS"}
	}
	if !validSiteAuth(spec.Auth) {
		return SiteFieldError{"credentials"}
	}
	if len(spec.AuthPaths) > 0 && (len(spec.Auth) == 0 || !allSiteValues(spec.AuthPaths, siteMatchPath)) {
		return SiteFieldError{"protected paths"}
	}
	if !validSiteIP(spec.IP) {
		return SiteFieldError{"IP rules"}
	}
	if spec.MaxBody != "" && !siteSize.MatchString(spec.MaxBody) {
		return SiteFieldError{"body size"}
	}
	if !validSiteLog(spec.Log) {
		return SiteFieldError{"access log"}
	}
	if !validSiteTLS(spec.TLS) {
		return SiteFieldError{"TLS"}
	}
	seen := make(map[string]bool)
	for _, route := range spec.Routes {
		if !siteMatchPath(route.Path) || route.Path == "/*" || len(route.Upstreams) == 0 || !allSiteValues(route.Upstreams, siteUpstream) || seen[route.Path] {
			return SiteFieldError{"path routes"}
		}
		seen[route.Path] = true
	}
	return nil
}

var (
	siteDomain     = regexp.MustCompile(`^(\*\.)?[[:alnum:]_\p{L}\p{N}][[:alnum:]_.\-\p{L}\p{N}]*$`)
	siteEnv        = regexp.MustCompile(`\{\$[a-zA-Z_][a-zA-Z0-9_]*(?::[^\s{}]+)?\}`)
	siteEnvOnly    = regexp.MustCompile(`^\{\$[a-zA-Z_][a-zA-Z0-9_]*\}$`)
	siteRuntimeEnv = regexp.MustCompile(`^\{env\.[a-zA-Z_][a-zA-Z0-9_]*\}$`)
	siteHeaderName = regexp.MustCompile("^[!#$%&'*+.^_`|~0-9a-zA-Z-]+$")
	siteSize       = regexp.MustCompile(`(?i)^\d+(\.\d+)?[kmgtep]?(i?b)?$`)
	siteDuration   = regexp.MustCompile(`^-?(?:\d+(?:\.\d+)?(?:ns|us|µs|μs|ms|s|m|h|d))+?$`)
	siteUser       = regexp.MustCompile(`^[^\x00-\x20\x7f:#{}]+$`)
	siteHash       = regexp.MustCompile(`^\$2[aby]\$(0[4-9]|[12][0-9]|3[01])\$[./a-zA-Z0-9]{53}$`)
	siteModule     = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_]*$`)
)

func IsEnvReference(value string) bool {
	return siteEnvOnly.MatchString(value) || siteRuntimeEnv.MatchString(value)
}

func allSiteValues(values []string, valid func(string) bool) bool {
	for _, value := range values {
		if !valid(value) {
			return false
		}
	}
	return true
}

func siteAddress(address string) bool {
	if address == "http://" || address == "https://" {
		return true
	}
	if start := strings.LastIndex(address, "{$"); start > 0 && address[start-1] == ':' {
		if part := address[start:]; siteEnv.FindString(part) == part {
			address = address[:start] + "443"
		}
	}
	address = siteEnv.ReplaceAllString(address, "host")
	if prefix, ok := strings.CutPrefix(address, "http://"); ok {
		address = prefix
	} else {
		address = strings.TrimPrefix(address, "https://")
	}
	if address == "*" {
		return true
	}
	if strings.ContainsAny(address, "\x00\r\n,{}@?#\"`\\/") || strings.ContainsFunc(address, unicode.IsSpace) {
		return false
	}
	if address == "" {
		return false
	}
	parsed, err := url.Parse("https://" + address)
	if err != nil || parsed.Host != address || parsed.User != nil || strings.HasSuffix(address, ":") {
		return false
	}
	if port := parsed.Port(); port != "" && !siteInteger(port, 0, 65535) {
		return false
	}
	host := parsed.Hostname()
	if host == "" || host == "*" {
		return parsed.Port() != ""
	}
	if strings.HasPrefix(address, "[") {
		ip, err := netip.ParseAddr(host)
		return err == nil && ip.Is6()
	}
	return siteDomain.MatchString(host) && !strings.Contains(host, "..")
}

func siteUpstream(address string) bool {
	if strings.HasPrefix(address, "unix/") {
		return siteAbsolutePath(strings.TrimPrefix(address, "unix/"))
	}
	for _, scheme := range []string{"http://", "https://", "h2c://"} {
		if plain, ok := strings.CutPrefix(address, scheme); ok {
			return siteAddress(plain) && !strings.ContainsAny(plain, "*/")
		}
	}
	return siteAddress(address) && !strings.ContainsAny(address, "*/") && !strings.HasPrefix(address, ":")
}

func siteAbsolutePath(value string) bool {
	return strings.HasPrefix(value, "/") && !strings.ContainsAny(value, "\x00\r\n")
}

func siteMatchPath(value string) bool {
	return siteAbsolutePath(value) && !strings.ContainsFunc(value, unicode.IsSpace)
}

func siteInteger(value string, low, high int) bool {
	parsed, err := strconv.Atoi(value)
	return err == nil && parsed >= low && parsed <= high
}

func validRedirect(value string) bool {
	if strings.ContainsAny(value, "\x00\r\n\\") || strings.ContainsFunc(value, unicode.IsSpace) {
		return false
	}
	if strings.HasPrefix(value, "/") && !strings.HasPrefix(value, "//") {
		return true
	}
	parsed, err := url.Parse(strings.NewReplacer("{uri}", "/", "{path}", "/").Replace(value))
	return err == nil && parsed.Hostname() != "" && parsed.User == nil && (parsed.Scheme == "http" || parsed.Scheme == "https")
}

func validProxy(opts ProxyOptions, upstreams []string) bool {
	if !slices.Contains([]string{"", "random", "random_choose", "round_robin", "least_conn", "ip_hash", "client_ip_hash", "first", "uri_hash", "query", "header", "cookie"}, opts.Policy) {
		return false
	}
	switch opts.Policy {
	case "query", "header", "random_choose":
		if len(opts.PolicyArgs) != 1 {
			return false
		}
	case "cookie":
		if len(opts.PolicyArgs) > 2 {
			return false
		}
	default:
		if len(opts.PolicyArgs) != 0 {
			return false
		}
	}
	if opts.Policy == "cookie" && len(opts.PolicyArgs) == 2 && !siteEnvOnly.MatchString(opts.PolicyArgs[1]) {
		return false
	}
	if opts.InsecureSkipVerify && !opts.TLS && !slices.ContainsFunc(upstreams, func(value string) bool { return strings.HasPrefix(value, "https://") }) {
		return false
	}
	for _, value := range []string{opts.TryDuration, opts.HealthInterval, opts.HealthTimeout, opts.FailDuration, opts.DialTimeout, opts.ReadTimeout, opts.WriteTimeout} {
		if value != "" && value != "0" && (strings.HasPrefix(value, "-") || !siteDuration.MatchString(value)) {
			return false
		}
	}
	status := opts.HealthStatus
	if len(status) == 3 && strings.HasSuffix(status, "xx") && status[0] >= '1' && status[0] <= '5' {
		status = ""
	}
	return (opts.HealthURI == "" || siteMatchPath(opts.HealthURI)) &&
		(status == "" || siteInteger(status, 100, 599)) && (opts.MaxFails == "" || siteInteger(opts.MaxFails, 0, 1<<31-1)) &&
		(opts.KeepAlive == "" || opts.KeepAlive == "off" || opts.KeepAlive == "0" || !strings.HasPrefix(opts.KeepAlive, "-") && siteDuration.MatchString(opts.KeepAlive)) &&
		(opts.FlushInterval == "" || opts.FlushInterval == "-1" || opts.FlushInterval == "0" || siteDuration.MatchString(opts.FlushInterval)) &&
		validHeaders(opts.HeaderUp) && validHeaders(opts.HeaderDown)
}

func validHeaders(headers []Header) bool {
	for _, header := range headers {
		name := header.Name
		if len(name) > 0 && strings.ContainsRune("+-?>", rune(name[0])) && name != "-*" {
			name = name[1:]
		}
		if !siteHeaderName.MatchString(name) || name == "-" || name == "import" || header.Name == "defer" || header.Name == "match" || strings.ContainsAny(header.Value, "\x00\r\n") || strings.HasPrefix(header.Name, "-") && header.Value != "" {
			return false
		}
	}
	return true
}

func validCORS(opts CORSOptions) bool {
	if len(opts.Origins) == 0 {
		return len(opts.Methods)+len(opts.Headers) == 0
	}
	if len(opts.Methods) == 0 || !allSiteValues(opts.Methods, siteHeaderName.MatchString) || !allSiteValues(opts.Headers, siteHeaderName.MatchString) {
		return false
	}
	seen := make(map[string]bool)
	for _, value := range opts.Origins {
		if seen[value] || strings.ContainsFunc(value, unicode.IsSpace) || strings.ContainsAny(value, "\x00\r\n") {
			return false
		}
		seen[value] = true
		if value == "*" {
			return len(opts.Origins) == 1
		}
		parsed, err := url.Parse(value)
		if err != nil || parsed.Hostname() == "" || !siteAddress(parsed.Host) || strings.ContainsAny(parsed.Host, "*") || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return false
		}
	}
	return true
}

func validSiteAuth(users []AuthUser) bool {
	seen := make(map[string]bool)
	for _, user := range users {
		if seen[user.Name] || user.Name == "import" || !siteUser.MatchString(user.Name) || !IsBcryptHash(user.Hash) {
			return false
		}
		seen[user.Name] = true
	}
	return true
}

func validSiteIP(opts IPOptions) bool {
	return slices.Contains([]string{"", "remote_ip", "client_ip"}, opts.Source) &&
		slices.Contains([]string{"", "403", "abort"}, opts.Action) &&
		allSiteValues(opts.Allow, siteIP) && allSiteValues(opts.Deny, siteIP)
}

func IsBcryptHash(value string) bool {
	if len(value) == 80 && !strings.ContainsAny(value, "\r\n") {
		decoded, err := base64.StdEncoding.Strict().DecodeString(value)
		if err != nil {
			return false
		}
		value = string(decoded)
	}
	return siteHash.MatchString(value)
}

func siteIP(value string) bool {
	if value == "private_ranges" {
		return true
	}
	if address, err := netip.ParseAddr(value); err == nil {
		return address.Zone() == ""
	}
	_, err := netip.ParsePrefix(value)
	return err == nil
}

func validSiteTLS(opts TLSOptions) bool {
	if opts.Mode != "email" && opts.Email != "" || opts.Mode != "custom" && (opts.Cert != "" || opts.Key != "") || opts.Mode != "dns" && (opts.DNS != "" || len(opts.DNSArgs) > 0) {
		return false
	}
	switch opts.Mode {
	case "", "auto", "internal":
	case "email":
		if name, host, ok := strings.Cut(opts.Email, "@"); !ok || name == "" || !siteDomain.MatchString(host) || strings.ContainsAny(name, "\x00\t\r\n @") {
			return false
		}
	case "custom":
		if !siteAbsolutePath(opts.Cert) || !siteAbsolutePath(opts.Key) {
			return false
		}
	case "dns":
		if !siteModule.MatchString(opts.DNS) || !allSiteValues(opts.DNSArgs, func(value string) bool {
			return siteEnvOnly.MatchString(value) || siteRuntimeEnv.MatchString(value)
		}) {
			return false
		}
	default:
		return false
	}
	return slices.Contains([]string{"", "tls1.2", "tls1.3"}, opts.MinProtocol) &&
		slices.Contains([]string{"", "request", "require", "verify_if_given", "require_and_verify"}, opts.ClientAuth) &&
		(opts.CAPool == "" || opts.ClientAuth != "" && siteAbsolutePath(opts.CAPool))
}

func validSiteLog(opts LogOptions) bool {
	return (opts.Path == "" && opts.RollSize == "" && opts.RollKeep == "" || siteAbsolutePath(opts.Path)) &&
		(opts.RollSize == "" || siteSize.MatchString(opts.RollSize)) &&
		(opts.RollKeep == "" || siteInteger(opts.RollKeep, 0, 1<<31-1)) && slices.Contains([]string{"", "json", "console"}, opts.Format)
}
