package app

import (
	"slices"
	"strings"

	"finallyshell/internal/caddyfile"
	"finallyshell/internal/icons"
	"finallyshell/internal/monitor"
	"finallyshell/internal/widgets"
	"finallyshell/locales"
	"github.com/egoist/mygo/ui"
)

func caddySiteTypeKey(kind caddyfile.SiteType) string {
	switch kind {
	case caddyfile.Proxy:
		return "Proxy"
	case caddyfile.PHP:
		return "PHP"
	case caddyfile.Redirect:
		return "Redirect"
	case caddyfile.SPA:
		return "SPA"
	case caddyfile.Respond:
		return "Respond"
	case caddyfile.Browser:
		return "Browser"
	default:
		return "Static"
	}
}

func (form *caddySiteForm) setup(c *ui.Context, options caddySiteFormOptions) {
	spec := &form.spec
	caddySiteEntry(c, "Addresses", "example.com, www.example.com", &form.addresses)
	if strings.Contains(form.addresses, "*.") {
		caddySiteNote(c, "Wildcard")
	}
	kind := string(spec.Type)
	var kinds []caddySiteChoice
	for _, model := range []caddyfile.SiteType{caddyfile.Static, caddyfile.Proxy, caddyfile.PHP, caddyfile.Redirect, caddyfile.SPA, caddyfile.Respond, caddyfile.Browser} {
		kinds = append(kinds, caddySiteChoice{caddySiteFormText(caddySiteTypeKey(model)), string(model)})
	}
	caddySiteOptions(c, "Type", &kind, kinds...)
	spec.Type = caddyfile.SiteType(kind)
	caddySiteNote(c, caddySiteTypeKey(spec.Type)+"Hint")
	switch spec.Type {
	case caddyfile.Static, caddyfile.Browser, caddyfile.PHP, caddyfile.SPA:
		caddySiteEntry(c, "Root", "/srv/www", &spec.Root)
		if spec.Type == caddyfile.PHP {
			form.phpSettings(c, options)
		}
		if spec.Type != caddyfile.Browser {
			ui.Checkbox(c, &spec.Browse, caddySiteFormText("EnableBrowse"))
		}
		caddySiteEntry(c, "Index", "index.html index.htm", &form.index)
		caddySiteEntry(c, "Hide", `.git *.bak "private files"`, &form.hide)
	case caddyfile.Proxy:
		caddySiteEntry(c, "Upstreams", "127.0.0.1:8080 https://origin.example.com", &form.upstreams)
		if len(options.DockerPorts) > 0 {
			caddySitePick(c, "DockerPick", options.DockerPorts, func(value string) { form.upstreams = value })
		} else if form.docker {
			caddySiteNote(c, "DockerUnavailable")
		}
		caddySiteNote(c, "Websocket")
	case caddyfile.Redirect:
		caddySiteEntry(c, "RedirectTo", "https://example.com{uri}", &spec.RedirectURL)
		caddySiteLiteralOptions(c, "RedirectStatus", &spec.RedirectCode, "301", "302", "307", "308")
		caddySiteWWWHelper(c, form)
	case caddyfile.Respond:
		caddySiteEntry(c, "ResponseStatus", "200", &spec.Status)
		caddySiteArea(c, "ResponseBody", "Hello world", &spec.Body)
	}
}

func (form *caddySiteForm) phpSettings(c *ui.Context, options caddySiteFormOptions) {
	ctrField(c.Key("php-target"), caddySiteFormText("PHPUpstream"), &form.spec.PHPUpstream, "unix//run/php/php-fpm.sock", nil)
	ui.Row(c).Gap(6).Wrap().Children(func() {
		var choices []caddySiteChoice
		for _, socket := range options.PHPSockets {
			if strings.HasPrefix(socket, "/") {
				socket = "unix/" + socket
			}
			choices = append(choices, caddySiteChoice{socket, socket})
		}
		caddySitePick(c, "PHPSockets", choices, func(value string) { form.spec.PHPUpstream = value })
		if options.ReloadPHPSockets != nil && ui.Button(c, caddySiteFormText("PHPDetect")).Clicked() {
			options.ReloadPHPSockets()
		}
	})
}

func caddySitePick(c *ui.Context, key string, choices []caddySiteChoice, choose func(string)) {
	if len(choices) == 0 {
		return
	}
	label := caddySiteFormText(key)
	ui.MenuButton(c.Key(key), label, func(menu *ui.Menu) {
		menu.MaxWidth(360)
		for _, choice := range choices {
			if menu.Item(choice.label).Chosen() {
				choose(choice.value)
				c.Invalidate()
			}
		}
	}).Label(label)
}

func caddySiteWWWHelper(c *ui.Context, form *caddySiteForm) {
	addresses := strings.FieldsFunc(form.addresses, func(char rune) bool { return char == ' ' || char == '\t' || char == ',' })
	var host string
	if len(addresses) == 1 {
		host = addresses[0]
	}
	ui.Row(c).Gap(6).Wrap().Children(func() {
		for _, action := range []struct {
			key   string
			toWWW bool
		}{{"ToWWW", true}, {"FromWWW", false}} {
			spec, err := caddyfile.WWWRedirect(host, action.toWWW)
			if ui.Button(c.Key(action.key), caddySiteFormText(action.key)).Disabled(err != nil).Tooltip(caddySiteFormText("WWWHint")).Clicked() && err == nil {
				form.addresses = strings.Join(spec.Addresses, ", ")
				form.spec.RedirectURL, form.spec.RedirectCode = spec.RedirectURL, spec.RedirectCode
			}
		}
	})
}

func (form *caddySiteForm) access(c *ui.Context, inst monitor.CaddyInstall) {
	caddySiteBlock(c, "TLS", func() { form.tlsSettings(c, inst) })
	caddySiteBlock(c, "Auth", func() { form.authSettings(c) })
	caddySiteBlock(c, "IP", func() {
		caddySiteEntry(c, "AllowIP", "192.0.2.0/24 2001:db8::/32", &form.allowedIP)
		caddySiteEntry(c, "DenyIP", "198.51.100.25", &form.deniedIP)
		caddySiteLiteralOptions(c, "IPSource", &form.spec.IP.Source, "remote_ip", "client_ip")
		if form.spec.IP.Source == "client_ip" {
			caddySiteNote(c, "ClientIPHint")
		}
		caddySiteLiteralOptions(c, "IPAction", &form.spec.IP.Action, "403", "abort")
	})
	caddySiteBlock(c, "Headers", func() {
		ui.Checkbox(c, &form.spec.SecurityHeaders, caddySiteFormText("SecurityHeaders"))
		if form.spec.SecurityHeaders {
			caddySiteEntry(c, "HSTS", "max-age=31536000", &form.spec.HSTS)
			caddySiteEntry(c, "ReferrerPolicy", "strict-origin-when-cross-origin", &form.spec.ReferrerPolicy)
			caddySiteEntry(c, "PermissionsPolicy", "geolocation=(), microphone=(), camera=()", &form.spec.PermissionsPolicy)
		}
		ui.Checkbox(c, &form.spec.RemoveServer, caddySiteFormText("RemoveServer"))
		caddySiteHeaderRows(c, "CustomHeaders", &form.spec.Headers, nil)
	})
	caddySiteBlock(c, "CORS", func() {
		caddySiteEntry(c, "Origins", "https://app.example.com", &form.corsOrigins)
		caddySiteEntry(c, "Methods", "GET POST OPTIONS", &form.corsMethods)
		caddySiteEntry(c, "CORSHeaders", "Authorization Content-Type", &form.corsHeaders)
	})
}

func (form *caddySiteForm) tlsSettings(c *ui.Context, inst monitor.CaddyInstall) {
	tls := &form.spec.TLS
	if tls.Mode == "auto" {
		tls.Mode = ""
	}
	caddySiteOptions(c, "TLSMode", &tls.Mode,
		caddySiteChoice{caddySiteFormText("TLSAuto"), ""}, caddySiteChoice{caddySiteFormText("TLSEmail"), "email"},
		caddySiteChoice{caddySiteFormText("TLSInternal"), "internal"}, caddySiteChoice{caddySiteFormText("TLSCustom"), "custom"},
		caddySiteChoice{caddySiteFormText("TLSDNS"), "dns"})
	switch tls.Mode {
	case "email":
		caddySiteEntry(c, "ACMEEmail", "admin@example.com", &tls.Email)
	case "custom":
		caddySiteEntry(c, "Certificate", "/etc/caddy/certs/site.crt", &tls.Cert)
		caddySiteEntry(c, "PrivateKey", "/etc/caddy/certs/site.key", &tls.Key)
	case "dns":
		caddySiteEntry(c, "DNSProvider", "cloudflare", &tls.DNS)
		caddySiteEntry(c, "DNSArgs", `{$CLOUDFLARE_API_TOKEN}`, &form.dnsArgs)
		caddySiteNote(c, "DNSHint")
		if tls.DNS != "" && !caddyfile.HasDNSProvider(inst.Modules, tls.DNS) {
			ui.Text(c, caddySiteFormText("DNSMissing")).TextColor(theme.Warning).FontSize(12).FillWidth()
		}
	}
	caddySiteLiteralOptions(c, "TLSMin", &tls.MinProtocol, "tls1.2", "tls1.3")
	caddySiteLiteralOptions(c, "TLSClientAuth", &tls.ClientAuth, "request", "require", "verify_if_given", "require_and_verify")
	if tls.ClientAuth != "" {
		caddySiteEntry(c, "TLSCAPool", "/etc/caddy/client-ca.pem", &tls.CAPool)
	}
}

func (form *caddySiteForm) authSettings(c *ui.Context) {
	caddySiteEntry(c, "ProtectedPaths", "/private/* /admin/*", &form.authPaths)
	caddySiteNote(c, "PasswordHint")
	remove := -1
	for index := range form.auth {
		row := &form.auth[index]
		ui.Column(c.Key(index)).FillWidth().Gap(3).Children(func() {
			ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
				ui.TextInput(c.Key("user"), &row.name).Label(locales.FieldUser()).Placeholder(locales.FieldUser()).Grow(1).MinWidth(0)
				placeholder := locales.FieldPassword()
				if row.hash != "" {
					placeholder = caddySiteFormText("PasswordUnchanged")
				}
				ui.TextInput(c.Key("password"), &row.password).Password().Label(locales.FieldPassword()).Placeholder(placeholder).Grow(2).MinWidth(0)
				if caddySiteRemove(c) {
					remove = index
				}
			})
		})
	}
	form.auth = ctrRemoveAt(form.auth, remove)
	if ui.Button(c, caddySiteFormText("AddUser")).AlignSelf(ui.Start).Clicked() {
		form.auth = append(form.auth, caddySiteFormAuth{})
	}
}

func caddySiteRemove(c *ui.Context) bool {
	return widgets.IconButton(c, "trash-2", caddySiteFormText("RemoveRow")).Shrink(0).Clicked()
}

func caddySiteHeaderRows(c *ui.Context, key string, rows *[]caddyfile.Header, presets []caddyfile.Header) {
	caddySiteBlock(c, key, func() {
		remove := -1
		for index := range *rows {
			row := &(*rows)[index]
			ui.Row(c.Key(index)).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
				ui.TextInput(c.Key("name"), &row.Name).Label(caddySiteFormText("HeaderName")).Placeholder("X-Frame-Options").Grow(1).MinWidth(0)
				ui.TextInput(c.Key("value"), &row.Value).Label(caddySiteFormText("HeaderValue")).Placeholder("DENY").Grow(1).MinWidth(0)
				if caddySiteRemove(c) {
					remove = index
				}
			})
		}
		*rows = ctrRemoveAt(*rows, remove)
		ui.Row(c).Gap(6).Wrap().Children(func() {
			if ui.Button(c, caddySiteFormText("AddHeader")).Clicked() {
				*rows = append(*rows, caddyfile.Header{})
			}
			var choices []caddySiteChoice
			for _, preset := range presets {
				choices = append(choices, caddySiteChoice{preset.Name + " → " + preset.Value, preset.Name})
			}
			caddySitePick(c, "HeaderPresets", choices, func(value string) {
				for _, preset := range presets {
					if preset.Name == value {
						if index := slices.IndexFunc(*rows, func(row caddyfile.Header) bool { return strings.EqualFold(row.Name, value) }); index >= 0 {
							(*rows)[index] = preset
						} else {
							*rows = append(*rows, preset)
						}
					}
				}
			})
		})
	})
}

func (form *caddySiteForm) advanced(c *ui.Context) {
	spec := &form.spec
	if spec.Type == caddyfile.Proxy {
		form.proxySettings(c)
	}
	caddySiteBlock(c, "Compression", func() {
		ui.Row(c).Wrap().Gap(10).Children(func() {
			ui.Checkbox(c, &form.compressZstd, "zstd")
			ui.Checkbox(c, &form.compressGzip, "gzip")
		})
	})
	caddySiteEntry(c, "MaxBody", "20MB", &spec.MaxBody)
	caddySiteBlock(c, "Routes", func() { form.routesSettings(c) })
	caddySiteBlock(c, "Logging", func() {
		caddySiteEntry(c, "LogPath", "/var/log/caddy/access.log", &spec.Log.Path)
		caddySiteEntry(c, "LogRollSize", "100MiB", &spec.Log.RollSize)
		caddySiteEntry(c, "LogRollKeep", "10", &spec.Log.RollKeep)
		caddySiteLiteralOptions(c, "LogFormat", &spec.Log.Format, "json", "console")
	})
	caddySiteArea(c, "Custom", "import shared_headers", &spec.Custom)
	caddySiteNote(c, "CustomHint")
}

func (form *caddySiteForm) routesSettings(c *ui.Context) {
	caddySiteNote(c, "RoutesHint")
	remove := -1
	for index := range form.routes {
		row := &form.routes[index]
		ui.Column(c.Key(index)).FillWidth().Gap(4).Padding(6).Border(1, theme.Border).Radius(4).Children(func() {
			ui.Row(c).FillWidth().Gap(6).AlignItems(ui.End).Children(func() {
				caddySiteEntry(c, "RoutePath", "/api/*", &row.path)
				if caddySiteRemove(c) {
					remove = index
				}
			})
			caddySiteEntry(c, "Upstreams", "127.0.0.1:9001", &row.upstreams)
		})
	}
	form.routes = ctrRemoveAt(form.routes, remove)
	if ui.Button(c, caddySiteFormText("AddRoute")).AlignSelf(ui.Start).Clicked() {
		form.routes = append(form.routes, caddySiteFormRoute{})
	}
}

func (form *caddySiteForm) proxySettings(c *ui.Context) {
	proxy := &form.spec.Proxy
	caddySiteBlock(c, "LoadBalancing", func() {
		caddySiteLiteralOptions(c, "LBPolicy", &proxy.Policy, "random", "random_choose", "round_robin", "least_conn", "ip_hash", "client_ip_hash", "first", "header", "cookie", "uri_hash", "query")
		if caddySitePolicyArgs(proxy.Policy) {
			caddySiteEntry(c, "PolicyArgs", "X-Session-ID", &form.policyArgs)
		}
		caddySiteEntry(c, "LBTry", "5s", &proxy.TryDuration)
	})
	caddySiteBlock(c, "HealthChecks", func() {
		for _, field := range []struct {
			key, hint string
			value     *string
		}{
			{"HealthURI", "/health", &proxy.HealthURI}, {"HealthInterval", "30s", &proxy.HealthInterval},
			{"HealthTimeout", "5s", &proxy.HealthTimeout}, {"HealthStatus", "200", &proxy.HealthStatus},
			{"FailDuration", "30s", &proxy.FailDuration}, {"MaxFails", "2", &proxy.MaxFails},
		} {
			caddySiteEntry(c, field.key, field.hint, field.value)
		}
	})
	caddySiteHeaderRows(c, "HeaderUp", &proxy.HeaderUp, []caddyfile.Header{{Name: "Host", Value: "{upstream_hostport}"}, {Name: "X-Real-IP", Value: "{remote_host}"}})
	caddySiteHeaderRows(c, "HeaderDown", &proxy.HeaderDown, []caddyfile.Header{{Name: "-Server"}})
	caddySiteBlock(c, "Transport", func() {
		ui.Checkbox(c, &proxy.TLS, caddySiteFormText("ProxyTLS"))
		if proxy.TLS || proxy.InsecureSkipVerify || strings.Contains(form.upstreams, "https://") {
			ui.Checkbox(c, &proxy.InsecureSkipVerify, caddySiteFormText("ProxyInsecure"))
			if proxy.InsecureSkipVerify {
				ui.Text(c, caddySiteFormText("ProxyInsecureWarning")).TextColor(theme.Warning).FontSize(12).FillWidth()
			}
		}
		for _, field := range []struct {
			key, hint string
			value     *string
		}{
			{"DialTimeout", "3s", &proxy.DialTimeout}, {"ReadTimeout", "30s", &proxy.ReadTimeout},
			{"WriteTimeout", "30s", &proxy.WriteTimeout}, {"KeepAlive", "2m", &proxy.KeepAlive},
			{"FlushInterval", "-1", &proxy.FlushInterval},
		} {
			caddySiteEntry(c, field.key, field.hint, field.value)
		}
		caddySiteNote(c, "FlushHint")
	})
}

func (form *caddySiteForm) templateView(c *ui.Context, height, width float32, options caddySiteFormOptions) {
	labels := map[string][2]string{
		"static": {"Static", "file"}, "proxy": {"Proxy", "arrow-down-up"}, "app": {"App", "terminal"},
		"wordpress": {"WordPress", "globe"}, "spa": {"SPA", "layout-grid"}, "www": {"WWW", "external-link"},
		"browser": {"BrowserAuth", "key-round"}, "gateway": {"Gateway", "route"}, "docker": {"Docker", "container"},
		"websocket": {"WebsocketApp", "activity"}, "balancer": {"Balancer", "network"},
	}
	templates := caddyfile.SiteTemplateIDs()
	caddySiteNote(c, "TemplatePick")
	columns := 2
	if width < 590 {
		columns = 1
	}
	ui.Scroll(c.Key("templates")).FillWidth().MaxHeight(height).KeepScrollbars(true).Children(func() {
		ui.Column(c).FillWidth().Gap(8).Padding(0, 12, 2, 0).Children(func() {
			for start := 0; start < len(templates); start += columns {
				ui.Row(c.Key(start)).FillWidth().Gap(8).Children(func() {
					for _, id := range templates[start:min(start+columns, len(templates))] {
						key, icon := labels[id][0], labels[id][1]
						ui.Column(c.Key(id)).Grow(1).MinWidth(0).Gap(3).Padding(8, 10).Background(theme.Surface).Border(1, colors.line).Radius(6).Children(func() {
							label := caddySiteFormText(key)
							button := ui.Button(c, "").FillWidth().Label(label)
							button.Children(func() {
								ui.Icon(c, icons.Get(icon)).FontSize(13).Shrink(0).TextColor(theme.Accent)
								ui.Text(c, label).MinWidth(0)
							})
							if button.Clicked() {
								spec := caddyfile.SiteTemplate(id)
								spec.Addresses = nil
								if id == "www" {
									spec.RedirectURL = ""
								}
								next := newCaddySiteForm(spec)
								if id == "docker" {
									next.docker = true
									next.upstreams = ""
									if len(options.DockerPorts) == 1 {
										next.upstreams = options.DockerPorts[0].value
									}
								}
								*form = *next
								c.Invalidate()
							}
							caddySiteNote(c, key+"Hint")
						})
					}
					if start+columns > len(templates) && columns == 2 {
						ui.Box(c).Grow(1).MinWidth(0)
					}
				})
			}
		})
	})
}
