package caddyfile

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

func ParseSite(node *Node) (SiteSpec, error) {
	if node == nil || node.Kind != Site {
		return SiteSpec{}, ErrSiteUnsupported
	}
	spec := SiteSpec{Addresses: slices.Clone(node.Args)}
	if !readSiteDirectives(&spec, node.Children) {
		if spec.Type == "" {
			spec.Type = siteKindHint(node.Children)
		}
		return spec, ErrSiteUnsupported
	}
	if _, err := GenerateSite(spec); err != nil {
		return spec, ErrSiteUnsupported
	}
	return spec, nil
}

func siteKindHint(nodes []*Node) SiteType {
	var hint SiteType
	for _, node := range nodes {
		switch node.Name {
		case "php_fastcgi":
			return PHP
		case "try_files":
			hint = SPA
		case "file_server":
			if hint == "" {
				hint = Static
				if argsAre(node, "browse") {
					hint = Browser
				}
			}
		case "reverse_proxy":
			return Proxy
		case "redir":
			return Redirect
		case "respond":
			return Respond
		case "route", "handle":
			if kind := siteKindHint(node.Children); kind != "" {
				return kind
			}
		}
	}
	return hint
}

func readSiteDirectives(spec *SiteSpec, nodes []*Node) bool {
	var handlers []*Node
	seen := make(map[string]bool)
	for _, node := range nodes {
		switch node.Name {
		case "tls", "log", "encode", "request_body", "header", "route":
			if seen[node.Name] {
				return false
			}
			seen[node.Name] = true
		}
		switch node.Name {
		case "tls":
			if !readSiteTLS(&spec.TLS, node) {
				return false
			}
		case "log":
			if !readSiteLog(&spec.Log, node) {
				return false
			}
		case "encode":
			if node.Block || len(node.Args) == 0 {
				return false
			}
			spec.Encode = slices.Clone(node.Args)
		case "request_body":
			value, ok := singleSetting(node, "max_size")
			if !ok {
				return false
			}
			spec.MaxBody = value
		case "header":
			if !readResponseHeaders(spec, node) {
				return false
			}
		case "route":
			if !node.Block || len(node.Args) != 0 {
				return false
			}
			if !readSiteHandlers(spec, node.Children) || !spec.ordered() || !matchesSiteRoute(*spec, node) {
				return false
			}
		default:
			handlers = append(handlers, node)
		}
	}
	if seen["route"] {
		return len(handlers) == 0
	}
	return readSiteHandlers(spec, handlers) && !spec.ordered()
}

func matchesSiteRoute(spec SiteSpec, route *Node) bool {
	var generated siteText
	generated.WriteString("example.test {\n")
	generated.block("route", nil, func() { generated.handlers(spec) })
	generated.WriteString("}\n")
	if generated.err != nil {
		return false
	}
	document, err := Parse(generated.String())
	return err == nil && len(document.Nodes) == 1 && len(document.Nodes[0].Children) == 1 && sameSiteNode(route, document.Nodes[0].Children[0])
}

func sameSiteNode(left, right *Node) bool {
	if left.Kind != right.Kind || left.Name != right.Name || left.Block != right.Block || !slices.Equal(left.Args, right.Args) || len(left.Children) != len(right.Children) {
		return false
	}
	for index, child := range left.Children {
		if !sameSiteNode(child, right.Children[index]) {
			return false
		}
	}
	return true
}

func readSiteHandlers(spec *SiteSpec, nodes []*Node) bool {
	var main []*Node
	var fallback *Node
	var origins, preflights, responded int
	var denyAction, missAction bool
	for _, node := range nodes {
		switch {
		case node.Name == "@fs_ip_deny" || node.Name == "@fs_ip_miss":
			if !readSiteIP(&spec.IP, node) {
				return false
			}
		case node.Name == "abort" || node.Name == "respond" && (len(node.Args) > 0 && strings.HasPrefix(node.Args[0], "@fs_ip_")):
			if !readIPAction(&spec.IP, node, &denyAction, &missAction) {
				return false
			}
		case strings.HasPrefix(node.Name, "@fs_origin_"):
			origins++
			if node.Name != fmt.Sprintf("@fs_origin_%d", origins) || !node.Block || len(node.Args) != 0 || len(node.Children) != 3 {
				return false
			}
			origin := node.Children[0]
			if origin.Name != "header" || len(origin.Args) != 2 || origin.Args[0] != "Origin" {
				return false
			}
			spec.CORS.Origins = append(spec.CORS.Origins, origin.Args[1])
		case node.Name == "@fs_cors_unmatched" || node.Name == "@fs_cors_invalid_preflight":
		case node.Name == "respond" && argsAre(node, "@fs_cors_invalid_preflight", "403"):
		case strings.HasPrefix(node.Name, "@fs_preflight_"):
			preflights++
		case node.Name == "header":
			if !isCORSHeader(node) {
				return false
			}
		case node.Name == "route" && len(node.Args) == 1 && strings.HasPrefix(node.Args[0], "@fs_preflight_"):
			if !readPreflightRoute(&spec.CORS, node) {
				return false
			}
			responded++
		case node.Name == "@fs_auth":
			if node.Block || spec.AuthPaths != nil || len(node.Args) < 2 || node.Args[0] != "path" {
				return false
			}
			spec.AuthPaths = slices.Clone(node.Args[1:])
		case node.Name == "basic_auth":
			if len(spec.Auth) > 0 || !readSiteAuth(spec, node) {
				return false
			}
		case node.Name == "handle_path":
			if !node.Block || len(node.Args) != 1 || len(node.Children) != 1 {
				return false
			}
			proxy := node.Children[0]
			if proxy.Name != "reverse_proxy" || proxy.Block || len(proxy.Args) == 0 {
				return false
			}
			spec.Routes = append(spec.Routes, PathRoute{node.Args[0], slices.Clone(proxy.Args)})
		case node.Name == "handle":
			if fallback != nil || !node.Block || len(node.Args) != 0 {
				return false
			}
			fallback = node
		default:
			main = append(main, node)
		}
	}
	if (fallback != nil) != (len(spec.Routes) != 0) || (len(spec.IP.Deny) != 0) != denyAction || (len(spec.IP.Allow) != 0) != missAction || origins != preflights || origins != responded {
		return false
	}
	if fallback != nil {
		if len(main) != 0 {
			return false
		}
		main = fallback.Children
	}
	return readMainHandler(spec, main)
}

func readMainHandler(spec *SiteSpec, nodes []*Node) bool {
	var hasFiles, hasPHP, hasTry bool
	seen := make(map[string]bool)
	for _, node := range nodes {
		if seen[node.Name] {
			return false
		}
		seen[node.Name] = true
		switch node.Name {
		case "root":
			if node.Block || len(node.Args) == 0 || len(node.Args) > 2 || len(node.Args) == 2 && node.Args[0] != "*" {
				return false
			}
			spec.Root = node.Args[len(node.Args)-1]
		case "file_server":
			hasFiles = true
			if !readFileServer(spec, node) {
				return false
			}
		case "php_fastcgi":
			if node.Block || len(node.Args) != 1 {
				return false
			}
			hasPHP, spec.PHPUpstream = true, node.Args[0]
		case "try_files":
			if node.Block || !argsAre(node, "{path}", "/index.html") {
				return false
			}
			hasTry = true
		case "reverse_proxy":
			if spec.Type != "" || !readReverseProxy(spec, node) {
				return false
			}
			spec.Type = Proxy
		case "redir", "respond":
			if spec.Type != "" || !readSimpleHandler(spec, node) {
				return false
			}
		default:
			return false
		}
	}
	if !hasFiles {
		return !hasPHP && !hasTry && spec.Root == "" && spec.Type != ""
	}
	if spec.Type != "" || hasPHP && hasTry {
		return false
	}
	switch {
	case hasPHP:
		spec.Type = PHP
	case hasTry:
		spec.Type = SPA
	case spec.Browse:
		spec.Type = Browser
	default:
		spec.Type = Static
	}
	return true
}

func readFileServer(spec *SiteSpec, node *Node) bool {
	if len(node.Args) > 0 {
		if !argsAre(node, "browse") {
			return false
		}
		spec.Browse = true
	}
	for _, child := range node.Children {
		if child.Block || len(child.Args) == 0 {
			return false
		}
		switch child.Name {
		case "index":
			if spec.Index != nil {
				return false
			}
			spec.Index = slices.Clone(child.Args)
		case "hide":
			if spec.Hide != nil {
				return false
			}
			spec.Hide = slices.Clone(child.Args)
		default:
			return false
		}
	}
	return true
}

func readSimpleHandler(spec *SiteSpec, node *Node) bool {
	args := node.Args
	if len(args) > 0 && args[0] == "*" {
		args = args[1:]
	} else if len(args) > 0 && isSiteMatcher(args[0]) {
		return false
	}
	if node.Block || len(args) < 1 || len(args) > 2 {
		return false
	}
	if node.Name == "redir" {
		spec.Type, spec.RedirectURL, spec.RedirectCode = Redirect, args[0], "302"
		if len(args) == 2 {
			spec.RedirectCode = args[1]
			switch spec.RedirectCode {
			case "permanent":
				spec.RedirectCode = "301"
			case "temporary":
				spec.RedirectCode = "302"
			}
		}
		return true
	}
	spec.Type, spec.Body, spec.Status = Respond, args[0], "200"
	if len(args) == 2 {
		spec.Status = args[1]
	} else if len(args[0]) == 3 {
		if code, err := strconv.Atoi(args[0]); err == nil && code > 0 {
			spec.Body, spec.Status = "", args[0]
		}
	}
	return true
}

func isSiteMatcher(value string) bool {
	return value == "*" || strings.HasPrefix(value, "@") || strings.HasPrefix(value, "/")
}

func readSiteAuth(spec *SiteSpec, node *Node) bool {
	if !node.Block || len(node.Children) == 0 || len(node.Args) != 0 && !(len(spec.AuthPaths) > 0 && argsAre(node, "@fs_auth")) || len(spec.AuthPaths) != 0 && len(node.Args) == 0 {
		return false
	}
	for _, child := range node.Children {
		if child.Block || len(child.Args) != 1 || !IsBcryptHash(child.Args[0]) {
			return false
		}
		spec.Auth = append(spec.Auth, AuthUser{child.Name, child.Args[0]})
	}
	return true
}

func readSiteIP(opts *IPOptions, node *Node) bool {
	var args []string
	if node.Name == "@fs_ip_deny" && !node.Block && opts.Deny == nil {
		args = node.Args
	} else if node.Name == "@fs_ip_miss" && node.Block && len(node.Args) == 0 && len(node.Children) == 1 && opts.Allow == nil {
		child := node.Children[0]
		if child.Name != "not" || child.Block {
			return false
		}
		args = child.Args
	} else {
		return false
	}
	if len(args) < 2 || opts.Source != "" && opts.Source != args[0] {
		return false
	}
	opts.Source = args[0]
	if node.Name == "@fs_ip_deny" {
		opts.Deny = slices.Clone(args[1:])
	} else {
		opts.Allow = slices.Clone(args[1:])
	}
	return true
}

func readIPAction(opts *IPOptions, node *Node, deny, miss *bool) bool {
	if node.Block || len(node.Args) == 0 {
		return false
	}
	var matched *bool
	switch node.Args[0] {
	case "@fs_ip_deny":
		matched = deny
	case "@fs_ip_miss":
		matched = miss
	default:
		return false
	}
	if *matched {
		return false
	}
	action := "403"
	if node.Name == "abort" && len(node.Args) == 1 {
		action = "abort"
	} else if node.Name != "respond" || len(node.Args) != 2 || node.Args[1] != "403" {
		return false
	}
	if opts.Action != "" && opts.Action != action {
		return false
	}
	opts.Action, *matched = action, true
	return true
}

func isCORSHeader(node *Node) bool {
	if !node.Block {
		return argsAre(node, "@fs_cors_unmatched", "-Access-Control-Allow-Origin") || len(node.Args) == 3 && strings.HasPrefix(node.Args[0], "@fs_origin_") && node.Args[1] == ">Access-Control-Allow-Origin"
	}
	return len(node.Args) == 0 && (len(node.Children) == 2 && node.Children[0].Name == "+Vary" && node.Children[1].Name == "defer" || len(node.Children) == 3 && node.Children[0].Name == "-Access-Control-Allow-Methods")
}

func readPreflightRoute(opts *CORSOptions, node *Node) bool {
	if !node.Block || len(node.Children) != 2 || node.Children[1].Name != "respond" || node.Children[1].Block || !argsAre(node.Children[1], "204") {
		return false
	}
	header := node.Children[0]
	if header.Name != "header" || !header.Block || len(header.Args) != 0 || len(header.Children) < 2 || len(header.Children) > 3 || header.Children[0].Name != "Access-Control-Allow-Origin" {
		return false
	}
	methods := header.Children[1]
	if methods.Name != "Access-Control-Allow-Methods" || methods.Block || len(methods.Args) != 1 {
		return false
	}
	var headers []string
	if len(header.Children) == 3 {
		allowed := header.Children[2]
		if allowed.Name != "Access-Control-Allow-Headers" || allowed.Block || len(allowed.Args) != 1 {
			return false
		}
		headers = strings.Split(allowed.Args[0], ", ")
	}
	values := strings.Split(methods.Args[0], ", ")
	if opts.Methods != nil && (!slices.Equal(opts.Methods, values) || !slices.Equal(opts.Headers, headers)) {
		return false
	}
	opts.Methods, opts.Headers = values, headers
	return true
}

func readResponseHeaders(spec *SiteSpec, node *Node) bool {
	if node.Block {
		if len(node.Args) > 0 || len(node.Children) == 0 {
			return false
		}
		for _, child := range node.Children {
			if child.Block || !addSiteHeader(spec, append([]string{child.Name}, child.Args...)) {
				return false
			}
		}
	} else if !addSiteHeader(spec, node.Args) {
		return false
	}
	headers := spec.Headers
	if len(headers) >= 5 && headers[0].Name == "Strict-Transport-Security" && headers[0].Value != "" && headers[1] == (Header{"X-Content-Type-Options", "nosniff"}) && headers[2] == (Header{"X-Frame-Options", "DENY"}) && headers[3].Name == "Referrer-Policy" && headers[3].Value != "" && headers[4].Name == "Permissions-Policy" && headers[4].Value != "" {
		spec.SecurityHeaders, spec.HSTS, spec.ReferrerPolicy, spec.PermissionsPolicy = true, headers[0].Value, headers[3].Value, headers[4].Value
		spec.Headers = headers[5:]
	}
	if len(spec.Headers) > 0 && strings.EqualFold(spec.Headers[0].Name, "-Server") && !spec.RemoveServer {
		spec.RemoveServer = true
		spec.Headers = spec.Headers[1:]
	}
	return true
}

func addSiteHeader(spec *SiteSpec, args []string) bool {
	header, ok := decodeHeader(args)
	if !ok {
		return false
	}
	if len(spec.Headers) == 0 && strings.EqualFold(header.Name, "-Server") && !spec.RemoveServer {
		spec.RemoveServer = true
		return true
	}
	spec.Headers = append(spec.Headers, header)
	return true
}

func decodeHeader(args []string) (Header, bool) {
	if len(args) == 1 && strings.HasPrefix(args[0], "-") {
		return Header{Name: args[0]}, true
	}
	if len(args) != 2 || isSiteMatcher(args[0]) {
		return Header{}, false
	}
	return Header{args[0], args[1]}, true
}

func readReverseProxy(spec *SiteSpec, node *Node) bool {
	if len(node.Args) == 0 || isSiteMatcher(node.Args[0]) {
		return false
	}
	spec.Upstreams = slices.Clone(node.Args)
	opts := &spec.Proxy
	fields := map[string]*string{
		"lb_try_duration": &opts.TryDuration, "health_uri": &opts.HealthURI, "health_interval": &opts.HealthInterval,
		"health_timeout": &opts.HealthTimeout, "health_status": &opts.HealthStatus, "fail_duration": &opts.FailDuration,
		"max_fails": &opts.MaxFails, "flush_interval": &opts.FlushInterval,
	}
	seen := make(map[string]bool)
	for _, child := range node.Children {
		switch child.Name {
		case "header_up", "header_down":
			header, ok := decodeHeader(child.Args)
			if child.Block || !ok {
				return false
			}
			if child.Name == "header_up" {
				opts.HeaderUp = append(opts.HeaderUp, header)
			} else {
				opts.HeaderDown = append(opts.HeaderDown, header)
			}
			continue
		case "lb_policy":
			if child.Block || len(child.Args) == 0 {
				return false
			}
			opts.Policy, opts.PolicyArgs = child.Args[0], slices.Clone(child.Args[1:])
		case "transport":
			if !readProxyTransport(opts, child) {
				return false
			}
		default:
			if !readSiteValue(child, fields) {
				return false
			}
		}
		if seen[child.Name] {
			return false
		}
		seen[child.Name] = true
	}
	return true
}

func readProxyTransport(opts *ProxyOptions, node *Node) bool {
	if !argsAre(node, "http") || len(node.Children) == 0 {
		return false
	}
	fields := map[string]*string{"dial_timeout": &opts.DialTimeout, "read_timeout": &opts.ReadTimeout, "write_timeout": &opts.WriteTimeout, "keepalive": &opts.KeepAlive}
	seen := make(map[string]bool)
	for _, child := range node.Children {
		if seen[child.Name] {
			return false
		}
		seen[child.Name] = true
		switch child.Name {
		case "tls", "tls_insecure_skip_verify":
			if child.Block || len(child.Args) != 0 {
				return false
			}
			if child.Name == "tls" {
				opts.TLS = true
			} else {
				opts.InsecureSkipVerify = true
			}
		default:
			if !readSiteValue(child, fields) {
				return false
			}
		}
	}
	return true
}

func readSiteTLS(opts *TLSOptions, node *Node) bool {
	switch len(node.Args) {
	case 0:
	case 1:
		if node.Args[0] == "internal" {
			opts.Mode = "internal"
		} else {
			opts.Mode, opts.Email = "email", node.Args[0]
		}
	case 2:
		opts.Mode, opts.Cert, opts.Key = "custom", node.Args[0], node.Args[1]
	default:
		return false
	}
	seen := make(map[string]bool)
	for _, child := range node.Children {
		if seen[child.Name] {
			return false
		}
		seen[child.Name] = true
		switch child.Name {
		case "dns":
			if child.Block || len(child.Args) == 0 || opts.Mode != "" {
				return false
			}
			opts.Mode, opts.DNS, opts.DNSArgs = "dns", child.Args[0], slices.Clone(child.Args[1:])
		case "protocols":
			if child.Block || len(child.Args) != 1 {
				return false
			}
			opts.MinProtocol = child.Args[0]
		case "client_auth":
			if !readClientAuth(opts, child) {
				return false
			}
		default:
			return false
		}
	}
	return opts.Mode != "" || len(node.Children) > 0
}

func readClientAuth(opts *TLSOptions, node *Node) bool {
	if !node.Block || len(node.Args) != 0 {
		return false
	}
	for _, child := range node.Children {
		switch child.Name {
		case "mode":
			if child.Block || len(child.Args) != 1 || opts.ClientAuth != "" {
				return false
			}
			opts.ClientAuth = child.Args[0]
		case "trust_pool":
			if !argsAre(child, "file") || !child.Block || len(child.Children) != 1 || opts.CAPool != "" {
				return false
			}
			pem := child.Children[0]
			if pem.Name != "pem_file" || pem.Block || len(pem.Args) != 1 {
				return false
			}
			opts.CAPool = pem.Args[0]
		default:
			return false
		}
	}
	return opts.ClientAuth != ""
}

func readSiteLog(opts *LogOptions, node *Node) bool {
	if len(node.Args) != 0 {
		return false
	}
	opts.Enabled = true
	seen := make(map[string]bool)
	for _, child := range node.Children {
		if seen[child.Name] {
			return false
		}
		seen[child.Name] = true
		switch child.Name {
		case "output":
			if len(child.Args) != 2 || child.Args[0] != "file" {
				return false
			}
			opts.Path = child.Args[1]
			fields := map[string]*string{"roll_size": &opts.RollSize, "roll_keep": &opts.RollKeep}
			for _, option := range child.Children {
				if !readSiteValue(option, fields) {
					return false
				}
				delete(fields, option.Name)
			}
		case "format":
			if child.Block || len(child.Args) != 1 {
				return false
			}
			opts.Format = child.Args[0]
		default:
			return false
		}
	}
	return true
}

func argsAre(node *Node, args ...string) bool { return slices.Equal(node.Args, args) }

func singleSetting(node *Node, key string) (string, bool) {
	if !node.Block || len(node.Args) != 0 || len(node.Children) != 1 {
		return "", false
	}
	child := node.Children[0]
	if child.Name != key || child.Block || len(child.Args) != 1 || child.Args[0] == "" {
		return "", false
	}
	return child.Args[0], true
}

func readSiteValue(node *Node, fields map[string]*string) bool {
	field := fields[node.Name]
	if field == nil || node.Block || len(node.Args) != 1 || node.Args[0] == "" {
		return false
	}
	*field = node.Args[0]
	return true
}
