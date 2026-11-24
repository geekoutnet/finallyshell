package app

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strconv"
	"strings"

	"finallyshell/internal/config"
	"finallyshell/internal/monitor"
	"finallyshell/internal/widgets"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
	"golang.org/x/crypto/ssh"
)

type privilegeSession struct {
	client *ssh.Client
	life   context.Context
	runner *monitor.Privilege
	user   string
}

func (a *App) sessionPrivilege(s *Session) *privilegeSession {
	state := &s.overview.memory
	if cached := state.privilege; cached != nil && cached.client == s.client && cached.life.Err() == nil {
		return cached
	}
	host := *s.Host
	client := s.client
	run := state.privilegeRunner
	if run == nil {
		run = monitor.SSHPrivilegeRunner(client)
	}
	parent := s.ctx
	if parent == nil {
		parent = context.Background()
	}
	life := parent
	if client != nil {
		connected, cancel := context.WithCancel(parent)
		life = connected
		go func() {
			client.Wait()
			cancel()
		}()
	}
	user := host.User
	if client != nil {
		user = client.User()
	}
	key := privilegeSecretKey(&host, user)
	credentials := monitor.PrivilegeCredentials{
		Login: func(ctx context.Context) []byte {
			return []byte(a.privilegeSecret(ctx, func(deliver func(string)) {
				if host.User == user && hostMatchesPrivilege(s.Host, &host) && host.Auth != config.AuthKey && host.Auth != config.AuthAgent {
					deliver(a.passwords[host.ID])
				} else {
					deliver("")
				}
			}))
		},
		Load: func(ctx context.Context) monitor.PrivilegeAnswer {
			stored := a.privilegeSecret(ctx, func(deliver func(string)) {
				a.secret(key, func(value string, ok bool) {
					if ok {
						deliver(value)
					} else {
						deliver("")
					}
				})
			})
			method, password, _ := strings.Cut(stored, "\n")
			switch method {
			case "sudo":
				return monitor.PrivilegeAnswer{Method: monitor.PrivilegeSudo, Password: []byte(password)}
			case "su":
				return monitor.PrivilegeAnswer{Method: monitor.PrivilegeSu, Password: []byte(password)}
			default:
				return monitor.PrivilegeAnswer{}
			}
		},
		Ask: func(ctx context.Context, method monitor.PrivilegeMethod, wrong bool) (monitor.PrivilegeAnswer, error) {
			return a.askPrivilege(ctx, privilegeWindow(s), method, wrong, user, host.Addr)
		},
		Save: func(answer monitor.PrivilegeAnswer) {
			if !answer.Remember {
				a.post(func() {
					if hostMatchesPrivilege(s.Host, &host) && host.User == user {
						a.deleteSecret(key)
					}
				})
				return
			}
			method := "sudo"
			if answer.Method == monitor.PrivilegeSu {
				method = "su"
			}
			value := method + "\n" + string(answer.Password)
			a.post(func() {
				if hostMatchesPrivilege(s.Host, &host) && host.User == user {
					a.setSecret(key, value)
				}
			})
		},
	}
	state.privilege = &privilegeSession{client: client, life: life, runner: monitor.NewPrivilege(life, run, credentials), user: user}
	return state.privilege
}

func hostMatchesPrivilege(current, original *config.Host) bool {
	return current != nil && !current.Deleted() && current.ID == original.ID && current.User == original.User && current.Auth == original.Auth &&
		current.Addr == original.Addr && current.PortOr22() == original.PortOr22()
}

func privilegeSecretKey(host *config.Host, user string) string {
	endpoint := fmt.Sprintf("%s\x00%s\x00%d", user, host.Addr, host.PortOr22())
	return hostSecretKey(host.ID, fmt.Sprintf("privilege/%x", sha256.Sum256([]byte(endpoint))))
}

func (a *App) privilegeSecret(ctx context.Context, read func(func(string))) string {
	result := make(chan string, 1)
	a.post(func() {
		if ctx.Err() == nil {
			read(func(value string) { result <- value })
		}
	})
	select {
	case value := <-result:
		return value
	case <-ctx.Done():
		return ""
	}
}

func privilegeWindow(s *Session) WindowKind {
	return WindowKind("privilege-" + strconv.FormatInt(s.ID, 10))
}

type privilegeQuestion struct {
	method   monitor.PrivilegeMethod
	wrong    bool
	user     string
	host     string
	password string
	remember bool
}

func (a *App) askPrivilege(ctx context.Context, kind WindowKind, method monitor.PrivilegeMethod, wrong bool, user, host string) (monitor.PrivilegeAnswer, error) {
	answer := make(chan monitor.PrivilegeAnswer, 1)
	var opened bool
	a.post(func() {
		if ctx.Err() != nil {
			return
		}
		if a.windowOpen(kind) {
			answer <- monitor.PrivilegeAnswer{}
			return
		}
		q := &privilegeQuestion{method: method, wrong: wrong, user: user, host: host}
		spec := WindowSpec{Kind: kind, Title: locales.PrivTitle, Width: 480, Height: 235, MinWidth: 420, MinHeight: 235, Modal: true}
		if sheets || wrong || a.secretsUnavailable() {
			spec.Height += 38
			spec.MinHeight = spec.Height
		}
		var submitted bool
		spec.Closed = func() {
			opened = false
			password := q.password
			q.password = ""
			if !submitted || ctx.Err() != nil {
				answer <- monitor.PrivilegeAnswer{}
				return
			}
			answer <- monitor.PrivilegeAnswer{Method: q.method, Password: []byte(password), Remember: q.remember}
		}
		opened = true
		a.openWindow(spec, func(c *ui.Context) {
			a.privilegeView(c, q, method == monitor.PrivilegeSudo, func(ok bool) {
				submitted = ok
				a.closeWindow(kind)
			})
		})
	})
	select {
	case reply := <-answer:
		if reply.Method == 0 {
			return reply, monitor.ErrPrivilegeCancelled
		}
		return reply, nil
	case <-ctx.Done():
		a.post(func() {
			if opened {
				a.closeWindow(kind)
			}
			select {
			case reply := <-answer:
				clear(reply.Password)
			default:
			}
		})
		return monitor.PrivilegeAnswer{}, ctx.Err()
	}
}

func (a *App) privilegeView(c *ui.Context, q *privilegeQuestion, allowSudo bool, finish func(bool)) {
	ui.Column(c).Fill().Padding(18, 22).Gap(10).Children(func() {
		prompt := locales.PrivSudoPrompt(q.user + "@" + q.host)
		switchLabel, other := locales.PrivUseSu(), monitor.PrivilegeSu
		if q.method == monitor.PrivilegeSu {
			prompt = locales.PrivSuPrompt(q.host)
			switchLabel, other = locales.PrivUseSudo(), monitor.PrivilegeSudo
		}
		ui.Text(c, prompt).FontSize(16).Bold()
		if q.wrong {
			ui.Text(c, locales.PrivWrongPassword()).TextColor(theme.Danger)
		} else if !allowSudo {
			widgets.Muted(c, locales.PrivSudoUnavailable()).FontSize(12)
		}
		submit := ui.TextInput(c.Key(q.method), &q.password).Label(locales.PrivPassword()).Placeholder(locales.PrivPassword()).Password().AutoFocus().FillWidth().Submitted()
		ui.Checkbox(c, &q.remember, locales.PrivRemember()).Disabled(a.secretsUnavailable())
		if a.secretsUnavailable() {
			widgets.Muted(c, a.rememberNote(false)).FontSize(12)
		}
		if allowSudo && ui.Button(c, switchLabel).AlignSelf(ui.Start).Clicked() {
			q.method, q.password, q.wrong = other, "", false
			submit = false
		}
		ui.Row(c).Justify(ui.End).Gap(8).Margin(4, 0, 0, 0).Children(func() {
			if ui.Button(c, locales.Cancel()).Clicked() {
				finish(false)
			}
			if ui.PrimaryButton(c, locales.PrivRun()).Disabled(q.password == "").Clicked() || submit && q.password != "" {
				finish(true)
			}
		})
	})
}

func privilegeHelp(c *ui.Context, user string) {
	ui.Text(c, locales.PrivNeedsRoot())
	ui.Text(c, locales.PrivAdminNote()).FontSize(12)
	quoted := "'" + strings.ReplaceAll(user, "'", `'\''`) + "'"
	if user != "" && strings.Trim(user, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-.") == "" && !strings.HasPrefix(user, "-") {
		quoted = user
	} else {
		quoted = "-- " + quoted
	}
	sudo, wheel := "usermod -aG sudo "+quoted, "usermod -aG wheel "+quoted
	ui.Column(c).FillWidth().Padding(9, 11).Gap(4).Radius(6).Background(colors.note).Children(func() {
		widgets.Muted(c, locales.PrivDebian()).FontSize(11)
		ui.Text(c, sudo).Font("monospace").Selectable().FontSize(12)
		widgets.Muted(c, locales.PrivRhel()).FontSize(11).Margin(4, 0, 0, 0)
		ui.Text(c, wheel).Font("monospace").Selectable().FontSize(12)
	})
	if ui.Button(c, locales.PrivCopyCommands()).AlignSelf(ui.Start).Clicked() {
		c.WriteClipboard(sudo + "\n" + wheel)
	}
}
