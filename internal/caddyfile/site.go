package caddyfile

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

type SiteType string

const (
	Static   SiteType = "static"
	Proxy    SiteType = "proxy"
	PHP      SiteType = "php"
	Redirect SiteType = "redirect"
	SPA      SiteType = "spa"
	Respond  SiteType = "respond"
	Browser  SiteType = "browser"
)

type SiteSpec struct {
	Addresses                               []string
	Type                                    SiteType
	Root                                    string
	Browse                                  bool
	Index, Hide                             []string
	Upstreams                               []string
	PHPUpstream                             string
	RedirectURL                             string
	RedirectCode                            string
	Status, Body                            string
	Proxy                                   ProxyOptions
	Encode                                  []string
	Headers                                 []Header
	SecurityHeaders, RemoveServer           bool
	HSTS, ReferrerPolicy, PermissionsPolicy string
	CORS                                    CORSOptions
	Auth                                    []AuthUser
	AuthPaths                               []string
	IP                                      IPOptions
	MaxBody                                 string
	Log                                     LogOptions
	TLS                                     TLSOptions
	Routes                                  []PathRoute
	Custom                                  string
}

type Header struct{ Name, Value string }
type AuthUser struct{ Name, Hash string }
type CORSOptions struct{ Origins, Methods, Headers []string }

type IPOptions struct {
	Source, Action string
	Allow, Deny    []string
}

type LogOptions struct {
	Path, RollSize, RollKeep, Format string
	Enabled                          bool
}

type TLSOptions struct {
	Mode, Email, Cert, Key, DNS, ClientAuth, CAPool, MinProtocol string
	DNSArgs                                                      []string
}

type PathRoute struct {
	Path      string
	Upstreams []string
}

type ProxyOptions struct {
	Policy, TryDuration, HealthURI, HealthInterval, HealthTimeout, HealthStatus string
	FailDuration, MaxFails, FlushInterval                                       string
	DialTimeout, ReadTimeout, WriteTimeout, KeepAlive                           string
	PolicyArgs                                                                  []string
	HeaderUp, HeaderDown                                                        []Header
	TLS, InsecureSkipVerify                                                     bool
}

var ErrSiteUnsupported = errors.New("site uses configuration not supported by the form")

type SiteFieldError struct{ Field string }

func (err SiteFieldError) Error() string { return "invalid or missing " + err.Field }

func GenerateSite(spec SiteSpec) (string, error) {
	if err := validateSite(spec); err != nil {
		return "", err
	}
	var out siteText
	for index, addr := range spec.Addresses {
		if index > 0 {
			out.WriteString(", ")
		}
		out.WriteString(addr)
	}
	out.WriteString(" {\n")
	out.depth++
	out.tls(spec.TLS)
	out.accessLog(spec.Log)
	if len(spec.Encode) > 0 {
		out.line("encode", spec.Encode...)
	}
	if spec.MaxBody != "" {
		out.block("request_body", nil, func() { out.line("max_size", spec.MaxBody) })
	}
	headers := spec.siteHeaders()
	if len(headers) > 0 {
		out.block("header", nil, func() { out.headers("", headers) })
	}
	if spec.ordered() {
		out.block("route", nil, func() { out.handlers(spec) })
	} else {
		out.handlers(spec)
	}
	if spec.Custom != "" {
		if strings.HasSuffix(strings.TrimRight(spec.Custom, " \t\r\n"), "\\") {
			return "", SiteFieldError{"custom directives"}
		}
		out.WriteString(spec.Custom)
		if !strings.HasSuffix(spec.Custom, "\n") {
			out.WriteByte('\n')
		}
	}
	out.WriteString("}\n")
	if out.err != nil {
		return "", out.err
	}
	text := out.String()
	doc, err := Parse(text)
	if err != nil || len(doc.Nodes) != 1 || doc.Nodes[0].Kind != Site || doc.Nodes[0].End != len(text)-1 {
		return "", SiteFieldError{"site syntax"}
	}
	return text, nil
}

func (spec SiteSpec) ordered() bool {
	return len(spec.IP.Allow)+len(spec.IP.Deny)+len(spec.CORS.Origins)+len(spec.Routes) > 0 ||
		spec.Type == Redirect && len(spec.Auth) > 0 || spec.Type == SPA && len(spec.AuthPaths) > 0
}

type siteText struct {
	strings.Builder
	depth int
	err   error
}

func QuoteArg(value string) (string, error) {
	if !utf8.ValidString(value) || strings.ContainsAny(value, "\x00\r") {
		return "", SiteFieldError{"directive argument"}
	}
	if value != "" && value != "{" && value != "}" && value != "{}" && !strings.ContainsFunc(value, unicode.IsSpace) && !strings.ContainsAny(value, "#\"`\\") && !strings.HasPrefix(value, "<<") {
		return value, nil
	}
	if strings.Contains(value, "\\") {
		if strings.Contains(value, "`") {
			return "", SiteFieldError{"directive argument"}
		}
		return "`" + value + "`", nil
	}
	return `"` + strings.ReplaceAll(value, `"`, `\"`) + `"`, nil
}

func (out *siteText) prefix(key string, args []string) {
	out.WriteString(strings.Repeat("\t", out.depth))
	out.WriteString(key)
	for index, value := range args {
		if key != "" || index > 0 {
			out.WriteByte(' ')
		}
		encoded, err := QuoteArg(value)
		if err != nil {
			out.err = err
			return
		}
		out.WriteString(encoded)
	}
}

func (out *siteText) line(key string, args ...string) {
	out.prefix(key, args)
	out.WriteByte('\n')
}

func (out *siteText) block(key string, args []string, body func()) {
	out.prefix(key, args)
	out.WriteString(" {\n")
	out.depth++
	body()
	out.depth--
	out.line("}")
}

func (out *siteText) set(key, value string) {
	if value != "" {
		out.line(key, value)
	}
}

func (out *siteText) headers(key string, headers []Header) {
	for _, header := range headers {
		if strings.HasPrefix(header.Name, "-") {
			out.line(key, header.Name)
		} else {
			out.line(key, header.Name, header.Value)
		}
	}
}

func (out *siteText) handlers(spec SiteSpec) {
	out.ipRules(spec.IP)
	out.cors(spec.CORS)
	if len(spec.Auth) > 0 {
		var args []string
		if len(spec.AuthPaths) > 0 {
			out.line("@fs_auth", append([]string{"path"}, spec.AuthPaths...)...)
			args = []string{"@fs_auth"}
		}
		out.block("basic_auth", args, func() {
			for _, user := range spec.Auth {
				out.line("", user.Name, user.Hash)
			}
		})
	}
	for _, route := range spec.Routes {
		out.block("handle_path", []string{route.Path}, func() { out.line("reverse_proxy", route.Upstreams...) })
	}
	if len(spec.Routes) != 0 {
		out.block("handle", nil, func() { out.mainHandler(spec) })
	} else {
		out.mainHandler(spec)
	}
}

func (out *siteText) mainHandler(spec SiteSpec) {
	switch spec.Type {
	case Static, PHP, SPA, Browser:
		out.line("root", "*", spec.Root)
		if spec.Type == PHP {
			out.line("php_fastcgi", spec.PHPUpstream)
		}
		if spec.Type == SPA {
			out.line("try_files", "{path}", "/index.html")
		}
		var args []string
		if spec.Type == Browser || spec.Browse {
			args = []string{"browse"}
		}
		if len(spec.Index)+len(spec.Hide) == 0 {
			out.line("file_server", args...)
		} else {
			out.block("file_server", args, func() {
				if len(spec.Index) > 0 {
					out.line("index", spec.Index...)
				}
				if len(spec.Hide) > 0 {
					out.line("hide", spec.Hide...)
				}
			})
		}
	case Proxy:
		out.reverseProxy(spec.Upstreams, spec.Proxy)
	case Redirect:
		out.line("redir", withWildcard(spec.RedirectURL, firstValue(spec.RedirectCode, "308"))...)
	case Respond:
		out.line("respond", withWildcard(spec.Body, firstValue(spec.Status, "200"))...)
	}
}

func withWildcard(value, status string) []string {
	if value == "*" || strings.HasPrefix(value, "/") || strings.HasPrefix(value, "@") {
		return []string{"*", value, status}
	}
	return []string{value, status}
}

func firstValue(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

func (out *siteText) ipRules(opts IPOptions) {
	source := firstValue(opts.Source, "remote_ip")
	action := func(matcher string) {
		if opts.Action == "abort" {
			out.line("abort", matcher)
		} else {
			out.line("respond", matcher, "403")
		}
	}
	if len(opts.Deny) > 0 {
		out.line("@fs_ip_deny", append([]string{source}, opts.Deny...)...)
		action("@fs_ip_deny")
	}
	if len(opts.Allow) > 0 {
		out.block("@fs_ip_miss", nil, func() { out.line("not", append([]string{source}, opts.Allow...)...) })
		action("@fs_ip_miss")
	}
}

func (out *siteText) cors(opts CORSOptions) {
	if len(opts.Origins) == 0 {
		return
	}
	out.block("header", nil, func() {
		out.line("+Vary", "Origin, Access-Control-Request-Method, Access-Control-Request-Headers")
		out.line("defer")
	})
	for index, origin := range opts.Origins {
		matchOrigin := fmt.Sprintf("@fs_origin_%d", index+1)
		preflight := fmt.Sprintf("@fs_preflight_%d", index+1)
		out.block(preflight, nil, func() {
			out.line("header", "Origin", origin)
			out.line("method", "OPTIONS")
			for _, method := range opts.Methods {
				out.line("header", "Access-Control-Request-Method", method)
			}
		})
		out.block("route", []string{preflight}, func() {
			out.block("header", nil, func() {
				out.line("Access-Control-Allow-Origin", origin)
				out.line("Access-Control-Allow-Methods", strings.Join(opts.Methods, ", "))
				if len(opts.Headers) > 0 {
					out.line("Access-Control-Allow-Headers", strings.Join(opts.Headers, ", "))
				}
			})
			out.line("respond", "204")
		})
		out.block(matchOrigin, nil, func() {
			out.line("header", "Origin", origin)
			out.line("header", "!Access-Control-Request-Method")
			out.line("method", opts.Methods...)
		})
		out.line("header", matchOrigin, ">Access-Control-Allow-Origin", origin)
	}
	out.block("@fs_cors_invalid_preflight", nil, func() {
		out.line("header", "Origin", "*")
		out.line("header", "Access-Control-Request-Method", "*")
		out.line("method", "OPTIONS")
	})
	out.line("respond", "@fs_cors_invalid_preflight", "403")
	out.block("@fs_cors_unmatched", nil, func() {
		out.block("not", nil, func() {
			for _, origin := range opts.Origins {
				out.line("header", "Origin", origin)
			}
			out.line("header", "!Access-Control-Request-Method")
			out.line("method", opts.Methods...)
		})
	})
	out.line("header", "@fs_cors_unmatched", "-Access-Control-Allow-Origin")
	out.block("header", nil, func() {
		out.line("-Access-Control-Allow-Methods")
		out.line("-Access-Control-Allow-Headers")
		out.line("-Access-Control-Allow-Credentials")
	})
}

func (spec SiteSpec) siteHeaders() []Header {
	var headers []Header
	if spec.SecurityHeaders {
		headers = []Header{
			{"Strict-Transport-Security", firstValue(spec.HSTS, "max-age=31536000")},
			{"X-Content-Type-Options", "nosniff"}, {"X-Frame-Options", "DENY"},
			{"Referrer-Policy", firstValue(spec.ReferrerPolicy, "strict-origin-when-cross-origin")},
			{"Permissions-Policy", firstValue(spec.PermissionsPolicy, "camera=(), microphone=(), geolocation=()")},
		}
	}
	if spec.RemoveServer {
		headers = append(headers, Header{Name: "-Server"})
	}
	return append(headers, spec.Headers...)
}

func (out *siteText) accessLog(opts LogOptions) {
	if opts.Path == "" && opts.Format == "" {
		if opts.Enabled {
			out.line("log")
		}
		return
	}
	out.block("log", nil, func() {
		if opts.Path != "" {
			args := []string{"file", opts.Path}
			if opts.RollSize == "" && opts.RollKeep == "" {
				out.line("output", args...)
			} else {
				out.block("output", args, func() {
					out.set("roll_size", opts.RollSize)
					out.set("roll_keep", opts.RollKeep)
				})
			}
		}
		out.set("format", opts.Format)
	})
}

func (out *siteText) tls(opts TLSOptions) {
	var args []string
	switch opts.Mode {
	case "email":
		args = []string{opts.Email}
	case "internal":
		args = []string{"internal"}
	case "custom":
		args = []string{opts.Cert, opts.Key}
	}
	if opts.DNS == "" && opts.ClientAuth == "" && opts.MinProtocol == "" {
		if len(args) > 0 {
			out.line("tls", args...)
		}
		return
	}
	out.block("tls", args, func() {
		if opts.Mode == "dns" {
			out.line("dns", append([]string{opts.DNS}, opts.DNSArgs...)...)
		}
		out.set("protocols", opts.MinProtocol)
		if opts.ClientAuth != "" {
			out.block("client_auth", nil, func() {
				out.line("mode", opts.ClientAuth)
				if opts.CAPool != "" {
					out.block("trust_pool", []string{"file"}, func() { out.line("pem_file", opts.CAPool) })
				}
			})
		}
	})
}

func (out *siteText) reverseProxy(upstreams []string, opts ProxyOptions) {
	if !opts.configured() {
		out.line("reverse_proxy", upstreams...)
		return
	}
	out.block("reverse_proxy", upstreams, func() {
		if opts.Policy != "" {
			out.line("lb_policy", append([]string{opts.Policy}, opts.PolicyArgs...)...)
		}
		out.set("lb_try_duration", opts.TryDuration)
		out.set("health_uri", opts.HealthURI)
		out.set("health_interval", opts.HealthInterval)
		out.set("health_timeout", opts.HealthTimeout)
		out.set("health_status", opts.HealthStatus)
		out.set("fail_duration", opts.FailDuration)
		out.set("max_fails", opts.MaxFails)
		out.headers("header_up", opts.HeaderUp)
		out.headers("header_down", opts.HeaderDown)
		out.set("flush_interval", opts.FlushInterval)
		if opts.transport() {
			out.block("transport", []string{"http"}, func() {
				if opts.TLS {
					out.line("tls")
				}
				if opts.InsecureSkipVerify {
					out.line("tls_insecure_skip_verify")
				}
				out.set("dial_timeout", opts.DialTimeout)
				out.set("read_timeout", opts.ReadTimeout)
				out.set("write_timeout", opts.WriteTimeout)
				out.set("keepalive", opts.KeepAlive)
			})
		}
	})
}

func (opts ProxyOptions) configured() bool {
	return opts.Policy != "" || opts.TryDuration != "" || opts.HealthURI != "" || opts.HealthInterval != "" || opts.HealthTimeout != "" || opts.HealthStatus != "" || opts.FailDuration != "" || opts.MaxFails != "" || opts.FlushInterval != "" || len(opts.HeaderUp)+len(opts.HeaderDown) > 0 || opts.transport()
}

func (opts ProxyOptions) transport() bool {
	return opts.TLS || opts.InsecureSkipVerify || opts.DialTimeout != "" || opts.ReadTimeout != "" || opts.WriteTimeout != "" || opts.KeepAlive != ""
}
