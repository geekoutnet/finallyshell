package app

import (
	"net/netip"
	"regexp"
	"strings"
	"unicode"

	"finallyshell/internal/config"
	"finallyshell/internal/widgets"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

const (
	fieldTelnetUserPrompt editorField = 300 + iota
	fieldTelnetPasswordPrompt
	fieldTelnetPassword
)

const editorTelnetLogin = "telnet-login"

var telnetFieldOrder = []editorField{fieldTelnetPassword, fieldTelnetUserPrompt, fieldTelnetPasswordPrompt}

func validTelnetHost(host string) bool {
	if host == "" || strings.ContainsAny(host, "/\\@?#[]") || strings.ContainsFunc(host, func(ch rune) bool {
		return unicode.IsSpace(ch) || unicode.IsControl(ch)
	}) {
		return false
	}
	if strings.ContainsAny(host, ":%") {
		_, err := netip.ParseAddr(host)
		return err == nil
	}
	return true
}

func telnetHostName(host string) string {
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		return host[1 : len(host)-1]
	}
	return host
}

func (ed *connEditor) telnetProblems(errs map[editorField]string) {
	if ed.addr != "" && !validTelnetHost(telnetHostName(strings.TrimSpace(ed.addr))) {
		errs[fieldAddr] = locales.TelnetHostInvalid()
	}
	if !validTermType(strings.TrimSpace(ed.termType)) {
		errs[fieldTermType] = locales.ErrTermTypeInvalid()
	}
	if !ed.telnet.AutoLogin {
		return
	}
	if strings.TrimSpace(ed.user) == "" {
		errs[fieldUser] = locales.ErrUserRequired()
	} else if strings.ContainsFunc(ed.user, unicode.IsControl) {
		errs[fieldUser] = locales.TelnetCredentialInvalid()
	}
	if strings.ContainsFunc(ed.password, unicode.IsControl) {
		errs[fieldTelnetPassword] = locales.TelnetCredentialInvalid()
	}
	for field, prompt := range map[editorField]string{
		fieldTelnetUserPrompt: ed.telnet.LoginPrompt, fieldTelnetPasswordPrompt: ed.telnet.PasswordPrompt,
	} {
		if prompt != "" {
			if expr, err := regexp.Compile(prompt); err != nil || expr.MatchString("") {
				errs[field] = locales.TelnetPromptInvalid()
			}
		}
	}
}

func (a *App) saveTerminalOnly(h *config.Host, ed *connEditor) {
	h.KeyPath, h.ForwardAgent, h.X11Forward = "", false, ""
	h.Proxy, h.Tunnels = nil, nil
	delete(a.proxySecrets(), proxySecretKey(h.ID))
	a.keepProxySecret(h, false, false)
	h.TermType, h.Encoding = strings.TrimSpace(ed.termType), ed.encoding
	h.Backspace, h.Delete = ed.backspace, ed.delete
}

func (a *App) saveTelnet(h *config.Host, ed *connEditor) {
	a.saveTerminalOnly(h, ed)
	h.Addr = telnetHostName(h.Addr)
	h.User, h.Auth = strings.TrimSpace(ed.user), config.AuthPassword
	options := ed.telnet
	h.Telnet = &options
	a.keepTelnetSecret(h, ed.password, options.AutoLogin && ed.remember, ed.readingSecret)
}

func (a *App) telnetPages(ed *connEditor, errs map[editorField]string, input func(ui.Element) ui.Element) []widgets.NavPage {
	return []widgets.NavPage{
		{ID: editorSSH, Title: locales.TelnetKind, View: func(c *ui.Context) { a.telnetPage(c, ed, errs, input) }},
		{ID: editorTelnetLogin, Title: locales.SectionAuth, View: func(c *ui.Context) { a.telnetLoginPage(c, ed, errs, input) }},
		{ID: editorTerminal, Title: locales.EditorPageTerminal, View: func(c *ui.Context) { a.terminalPage(c, ed, errs, input) }},
	}
}

func (a *App) telnetPage(c *ui.Context, ed *connEditor, errs map[editorField]string, input func(ui.Element) ui.Element) {
	ui.Form(c, func() {
		ui.Fieldset(c, locales.SectionGeneral(), func() {
			kindField(c, ed)
			widgets.Muted(c, locales.TelnetPlaintext()).FontSize(12)
			ui.Field(c, locales.FieldName(), func() { input(ui.TextInput(c, &ed.name).Placeholder(locales.FieldNamePlaceholder())) })
			ui.Field(c, locales.FieldHost(), func() { input(ui.TextInput(c, &ed.addr).Placeholder(locales.FieldHostPlaceholder()).AutoFocus()) }).Error(errs[fieldAddr])
			ui.Field(c, locales.FieldPort(), func() { input(ui.TextInput(c, &ed.port).Width(90)) }).Error(errs[fieldPort])
			ui.Field(c, "", func() { ui.Checkbox(c, &ed.telnet.KeepAlive, locales.TelnetKeepAlive()) })
			ui.Field(c, locales.FieldNotes(), func() { ui.TextArea(c, &ed.notes).Lines(2, 4) })
		})
	})
}

func (a *App) telnetLoginPage(c *ui.Context, ed *connEditor, errs map[editorField]string, input func(ui.Element) ui.Element) {
	ui.Form(c, func() {
		ui.Field(c, "", func() { ui.Checkbox(c, &ed.telnet.AutoLogin, locales.TelnetAutoLogin()) }).Description(locales.TelnetAutoLoginNote())
		if !ed.telnet.AutoLogin {
			return
		}
		ui.Field(c, locales.FieldUser(), func() { input(ui.TextInput(c, &ed.user)) }).Error(errs[fieldUser])
		ui.Field(c, locales.FieldPassword(), func() { input(ui.TextInput(c, &ed.password).Password()) }).Error(errs[fieldTelnetPassword])
		a.rememberField(c, &ed.remember, false)
		ui.Field(c, locales.TelnetLoginPrompt(), func() { input(ui.TextInput(c, &ed.telnet.LoginPrompt).Placeholder("(?i)login:|username:")) }).Error(errs[fieldTelnetUserPrompt])
		ui.Field(c, locales.TelnetPasswordPrompt(), func() { input(ui.TextInput(c, &ed.telnet.PasswordPrompt).Placeholder("(?i)password:")) }).Error(errs[fieldTelnetPasswordPrompt])
	})
}
