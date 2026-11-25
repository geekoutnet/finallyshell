package app

import (
	"context"
	"errors"
	"slices"
	"strings"

	"finallyshell/internal/caddyfile"
	"finallyshell/internal/monitor"
	"finallyshell/internal/widgets"
	"finallyshell/locales"

	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

const caddySitePreviewHash = "$2a$14$aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

type caddySiteChoice struct{ label, value string }

type caddySiteFormOptions struct {
	PHPSockets       []string
	DockerPorts      []caddySiteChoice
	ReloadPHPSockets func()
}

type caddySiteFormAuth struct{ name, hash, password string }

type caddySiteFormRoute struct{ path, upstreams string }

type caddySiteForm struct {
	spec                                                caddyfile.SiteSpec
	addresses, index, hide, upstreams                   string
	policyArgs, authPaths, allowedIP, deniedIP, dnsArgs string
	corsOrigins, corsMethods, corsHeaders               string
	compressZstd, compressGzip, templates, docker       bool
	section                                             int
	auth                                                []caddySiteFormAuth
	routes                                              []caddySiteFormRoute
	preview                                             editor.State
	previewText                                         string
}

type caddySiteFormValue struct {
	Text                        string
	Addresses                   []string
	Err                         error
	spec                        caddyfile.SiteSpec
	passwords                   []string
	hashFailure, invalidFailure error
}

func caddySiteFormText(key string) string {
	return locales.Catalog.Format(i18n.Locale(), "csiteForm"+key, nil)
}

func newCaddySiteForm(spec caddyfile.SiteSpec) *caddySiteForm {
	if spec.Type == "" {
		spec.Type = caddyfile.Static
	}
	spec.Headers = slices.Clone(spec.Headers)
	spec.Proxy.HeaderUp = slices.Clone(spec.Proxy.HeaderUp)
	spec.Proxy.HeaderDown = slices.Clone(spec.Proxy.HeaderDown)
	form := &caddySiteForm{
		spec: spec, addresses: strings.Join(spec.Addresses, ", "),
		index: caddySiteArgsText(spec.Index), hide: caddySiteArgsText(spec.Hide), upstreams: caddySiteArgsText(spec.Upstreams),
		policyArgs: caddySiteArgsText(spec.Proxy.PolicyArgs), authPaths: caddySiteArgsText(spec.AuthPaths),
		allowedIP: strings.Join(spec.IP.Allow, " "), deniedIP: strings.Join(spec.IP.Deny, " "),
		dnsArgs: caddySiteArgsText(spec.TLS.DNSArgs), corsOrigins: caddySiteArgsText(spec.CORS.Origins),
		corsMethods: strings.Join(spec.CORS.Methods, " "), corsHeaders: strings.Join(spec.CORS.Headers, " "),
		compressZstd: slices.Contains(spec.Encode, "zstd"), compressGzip: slices.Contains(spec.Encode, "gzip"),
	}
	for _, user := range spec.Auth {
		form.auth = append(form.auth, caddySiteFormAuth{name: user.Name, hash: user.Hash})
	}
	for _, route := range spec.Routes {
		form.routes = append(form.routes, caddySiteFormRoute{path: route.Path, upstreams: caddySiteArgsText(route.Upstreams)})
	}
	return form
}

func newCaddySiteTemplates() *caddySiteForm {
	return &caddySiteForm{templates: true}
}

func (form *caddySiteForm) clearPasswords() {
	for index := range form.auth {
		form.auth[index].password = ""
	}
}

func caddySiteArgsText(values []string) string {
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		arg, err := caddyfile.QuoteArg(value)
		if err != nil {
			return strings.Join(values, " ")
		}
		quoted = append(quoted, arg)
	}
	return strings.Join(quoted, " ")
}

func (form *caddySiteForm) snapshot(inst monitor.CaddyInstall) caddySiteFormValue {
	value := form.value(inst, true)
	value.hashFailure = errors.New(caddySiteFormText("HashFailed"))
	value.invalidFailure = errors.New(caddySiteFormText("Invalid"))
	return value
}

func (form *caddySiteForm) value(inst monitor.CaddyInstall, includePasswords bool) caddySiteFormValue {
	if form.templates {
		return caddySiteFormValue{Err: errors.New(caddySiteFormText("TemplatePick"))}
	}
	spec := form.spec
	result := caddySiteFormValue{}
	spec.Addresses = strings.FieldsFunc(form.addresses, func(char rune) bool {
		return char == ',' || char == ' ' || char == '\t' || char == '\r' || char == '\n'
	})
	result.Addresses = spec.Addresses
	for _, input := range []struct {
		name, text string
		values     *[]string
	}{
		{"ProtectedPaths", form.authPaths, &spec.AuthPaths}, {"AllowIP", form.allowedIP, &spec.IP.Allow},
		{"DenyIP", form.deniedIP, &spec.IP.Deny}, {"Origins", form.corsOrigins, &spec.CORS.Origins},
		{"Methods", form.corsMethods, &spec.CORS.Methods}, {"CORSHeaders", form.corsHeaders, &spec.CORS.Headers},
	} {
		if !caddySiteFormArgs(&result, input.values, input.name, input.text) {
			return result
		}
	}
	switch spec.Type {
	case caddyfile.Static, caddyfile.Browser, caddyfile.PHP, caddyfile.SPA:
		spec.Root = strings.TrimSpace(spec.Root)
		if !caddySiteFormArgs(&result, &spec.Index, "Index", form.index) || !caddySiteFormArgs(&result, &spec.Hide, "Hide", form.hide) {
			return result
		}
	default:
		spec.Root, spec.Browse, spec.Index, spec.Hide = "", false, nil, nil
	}
	if spec.Type != caddyfile.PHP {
		spec.PHPUpstream = ""
	}
	if spec.Type == caddyfile.Proxy {
		if !caddySiteFormArgs(&result, &spec.Upstreams, "Upstreams", form.upstreams) {
			return result
		}
		spec.Proxy.PolicyArgs = nil
		if caddySitePolicyArgs(spec.Proxy.Policy) && !caddySiteFormArgs(&result, &spec.Proxy.PolicyArgs, "PolicyArgs", form.policyArgs) {
			return result
		}
		spec.Proxy.HeaderUp = caddySiteNonemptyHeaders(spec.Proxy.HeaderUp)
		spec.Proxy.HeaderDown = caddySiteNonemptyHeaders(spec.Proxy.HeaderDown)
	} else {
		spec.Upstreams, spec.Proxy = nil, caddyfile.ProxyOptions{}
	}
	if spec.TLS.Mode == "dns" {
		if !caddySiteFormArgs(&result, &spec.TLS.DNSArgs, "DNSArgs", form.dnsArgs) {
			return result
		}
		if spec.TLS.DNS != "" && !caddyfile.HasDNSProvider(inst.Modules, spec.TLS.DNS) {
			result.Err = errors.New(caddySiteFormText("DNSMissing"))
			return result
		}
	} else {
		spec.TLS.DNS, spec.TLS.DNSArgs = "", nil
	}
	if spec.TLS.Mode != "custom" {
		spec.TLS.Cert, spec.TLS.Key = "", ""
	}
	if spec.TLS.Mode != "email" {
		spec.TLS.Email = ""
	}
	if spec.TLS.ClientAuth == "" {
		spec.TLS.CAPool = ""
	}
	if spec.Type != caddyfile.Redirect {
		spec.RedirectURL, spec.RedirectCode = "", ""
	}
	if spec.Type != caddyfile.Respond {
		spec.Status, spec.Body = "", ""
	}
	spec.Headers = caddySiteNonemptyHeaders(spec.Headers)
	spec.Encode = nil
	for _, codec := range form.spec.Encode {
		if codec == "zstd" && form.compressZstd || codec == "gzip" && form.compressGzip {
			spec.Encode = append(spec.Encode, codec)
		}
	}
	if form.compressZstd && !slices.Contains(spec.Encode, "zstd") {
		spec.Encode = append(spec.Encode, "zstd")
	}
	if form.compressGzip && !slices.Contains(spec.Encode, "gzip") {
		spec.Encode = append(spec.Encode, "gzip")
	}
	spec.Auth = nil
	preview := make([]caddyfile.AuthUser, 0, len(form.auth))
	for _, row := range form.auth {
		name := strings.TrimSpace(row.name)
		if name == "" && row.password == "" && row.hash == "" {
			continue
		}
		if row.hash == "" && row.password == "" {
			result.Err = errors.New(caddySiteFormText("PasswordRequired"))
			return result
		}
		if row.password != "" && strings.ContainsAny(row.password, "\r\n\x00") {
			result.Err = errors.New(caddySiteFormText("PasswordLine"))
			return result
		}
		spec.Auth = append(spec.Auth, caddyfile.AuthUser{Name: name, Hash: row.hash})
		if includePasswords {
			result.passwords = append(result.passwords, row.password)
		}
		preview = append(preview, caddyfile.AuthUser{Name: name, Hash: caddySitePreviewHash})
	}
	spec.Routes = nil
	for _, row := range form.routes {
		if strings.TrimSpace(row.path) == "" && strings.TrimSpace(row.upstreams) == "" {
			continue
		}
		route := caddyfile.PathRoute{Path: strings.TrimSpace(row.path)}
		if !caddySiteFormArgs(&result, &route.Upstreams, "Upstreams", row.upstreams) {
			return result
		}
		spec.Routes = append(spec.Routes, route)
	}
	result.spec = spec
	spec.Auth = preview
	text, err := caddyfile.GenerateSite(spec)
	result.Err = caddySiteFieldReason(err)
	result.Text = strings.ReplaceAll(text, caddySitePreviewHash, "<"+caddySiteFormText("HiddenHash")+">")
	return result
}

func caddySiteFormArgs(result *caddySiteFormValue, target *[]string, label, text string) bool {
	var err error
	*target, err = caddyfile.SplitArgs(strings.TrimSpace(text))
	if err != nil {
		result.Err = caddySiteBadField(caddySiteFormText(label))
	}
	return err == nil
}

func caddySiteBadField(label string) error {
	return errors.New(locales.CsiteFormBadField(label))
}

func caddySiteFieldReason(err error) error {
	if err == nil {
		return nil
	}
	var fieldError caddyfile.SiteFieldError
	if errors.As(err, &fieldError) {
		if key, ok := map[string]string{
			"addresses": "Addresses", "root": "Root", "upstreams": "Upstreams", "PHP upstream": "PHPUpstream",
			"redirect URL": "RedirectTo", "redirect status": "RedirectStatus", "response status": "ResponseStatus",
			"headers": "Headers", "compression": "Compression", "CORS": "CORS", "credentials": "Auth",
			"protected paths": "ProtectedPaths", "IP rules": "IP", "body size": "MaxBody", "access log": "Logging",
			"TLS": "TLS", "path routes": "Routes", "reverse proxy": "Proxy", "custom directives": "Custom",
		}[fieldError.Field]; ok {
			return caddySiteBadField(caddySiteFormText(key))
		}
	}
	return errors.New(caddySiteFormText("Invalid"))
}

func (value caddySiteFormValue) render(ctx context.Context, hash func(context.Context, string) (string, error)) (string, error) {
	if value.Err != nil {
		return "", value.Err
	}
	spec := value.spec
	spec.Auth = slices.Clone(spec.Auth)
	for index, password := range value.passwords {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if password == "" {
			continue
		}
		if hash == nil {
			return "", value.hashFailure
		}
		hashed, err := hash(ctx, password)
		if err != nil {
			switch {
			case ctx.Err() != nil:
				return "", ctx.Err()
			case errors.Is(err, monitor.ErrPrivilegeCancelled):
				return "", monitor.ErrPrivilegeCancelled
			case errors.Is(err, monitor.ErrPrivilegeDenied), errors.Is(err, monitor.ErrPrivilegeWrongPassword):
				return "", monitor.ErrPrivilegeDenied
			default:
				return "", value.hashFailure
			}
		}
		hashed = strings.TrimSpace(hashed)
		if !caddyfile.IsBcryptHash(hashed) || hashed == password {
			return "", value.hashFailure
		}
		spec.Auth[index].Hash = hashed
	}
	text, err := caddyfile.GenerateSite(spec)
	if err != nil {
		return "", value.invalidFailure
	}
	return text, nil
}

func caddySiteNonemptyHeaders(rows []caddyfile.Header) []caddyfile.Header {
	var headers []caddyfile.Header
	for _, row := range rows {
		if row.Name != "" || row.Value != "" {
			headers = append(headers, row)
		}
	}
	return headers
}

func caddySitePolicyArgs(policy string) bool {
	return policy == "query" || policy == "header" || policy == "cookie" || policy == "random_choose"
}

func (form *caddySiteForm) view(c *ui.Context, height float32, inst monitor.CaddyInstall, options caddySiteFormOptions) caddySiteFormValue {
	width, _ := c.Size()
	if form.templates {
		form.templateView(c, min(525, max(85, height-195)), width, options)
		return form.value(inst, false)
	}
	height = min(430, max(85, height-222))
	const previewSection = 3
	narrow := width < 680
	sections := []string{caddySiteFormText("Setup"), caddySiteFormText("Access"), caddySiteFormText("Advanced")}
	if narrow {
		sections = append(sections, caddySiteFormText("Preview"))
	} else if form.section == previewSection {
		form.section = 0
	}
	ui.ScrollHorizontal(c).FillWidth().Scrollbars(false).Children(func() {
		ui.Segmented(c, &form.section, sections...).Label(caddySiteFormText("Setup"))
	})
	var value caddySiteFormValue
	ui.Row(c).FillWidth().AlignItems(ui.Start).Gap(12).Children(func() {
		if form.section != previewSection {
			ui.Scroll(c.Key(form.section)).Grow(1).MinWidth(0).Height(height).KeepScrollbars(true).Children(func() {
				ui.Column(c).FillWidth().Padding(0, 12, 2, 0).Gap(8).Children(func() {
					switch form.section {
					case 0:
						form.setup(c, options)
					case 1:
						form.access(c, inst)
					case 2:
						form.advanced(c)
					}
				})
			})
		}
		value = form.value(inst, false)
		if !narrow || form.section == previewSection {
			ui.Column(c.Key("site-preview")).Grow(1).MinWidth(0).Gap(4).Children(func() {
				label := caddySiteFormText("Preview")
				if !narrow {
					ui.Text(c, label).Bold().FontSize(13)
				}
				if form.preview.Buffer == nil {
					form.preview = editor.State{Buffer: editor.NewBuffer(value.Text), ReadOnly: true, TabSize: 2,
						SoftWrap: true, NoFolding: true, NoActiveLine: true, NoScrollPastEnd: true, KeepScrollbars: true}
					form.preview.Buffer.SetLanguage(editor.LanguageNamed("Caddyfile"))
					form.previewText = value.Text
				} else if form.previewText != value.Text {
					form.preview.Buffer.Set(value.Text)
					form.previewText = value.Text
				}
				form.preview.Label, form.preview.Placeholder = label, caddySiteFormText("PreviewEmpty")
				previewHeight := height
				if !narrow {
					previewHeight -= 21
				}
				editor.View(c, &form.preview).FillWidth().Height(previewHeight).Border(1, colors.line).Radius(6).Clip()
			})
		}
	})
	if value.Err != nil {
		ui.Text(c, value.Err.Error()).TextColor(theme.Warning).FontSize(12).FillWidth()
	}
	return value
}

func caddySiteNote(c *ui.Context, key string) {
	widgets.Muted(c, caddySiteFormText(key)).FontSize(12).FillWidth()
}

func caddySiteBlock(c *ui.Context, key string, body func()) {
	ui.Column(c.Key(key)).FillWidth().Gap(6).Children(func() {
		ui.Text(c, caddySiteFormText(key)).FontSize(13).Bold()
		body()
	})
}

func caddySiteEntry(c *ui.Context, key, hint string, value *string) {
	label := caddySiteFormText(key)
	ctrField(c.Key(key), label, value, hint, nil)
}

func caddySiteOptions(c *ui.Context, key string, value *string, choices ...caddySiteChoice) {
	label := caddySiteFormText(key)
	var labels []string
	for _, choice := range choices {
		labels = append(labels, choice.label)
	}
	selected := ""
	for _, choice := range choices {
		if choice.value == *value {
			selected = choice.label
			break
		}
	}
	if selected == "" && len(labels) != 0 {
		selected = labels[0]
	}
	ui.Column(c.Key(key)).Gap(3).FillWidth().Children(func() {
		widgets.Muted(c, label).FontSize(12)
		if ui.Select(c, &selected, labels).Label(label).FillWidth().MinWidth(0).Changed() {
			for _, choice := range choices {
				if selected == choice.label {
					*value = choice.value
					break
				}
			}
		}
	})
}

func caddySiteLiteralOptions(c *ui.Context, key string, value *string, choices ...string) {
	options := []caddySiteChoice{{caddySiteFormText("Default"), ""}}
	for _, choice := range choices {
		options = append(options, caddySiteChoice{choice, choice})
	}
	if *value != "" && !slices.Contains(choices, *value) {
		options = append(options, caddySiteChoice{*value, *value})
	}
	caddySiteOptions(c, key, value, options...)
}

func caddySiteArea(c *ui.Context, key, hint string, value *string) {
	ctrArea(c.Key(key), caddySiteFormText(key), hint, value, false)
}
