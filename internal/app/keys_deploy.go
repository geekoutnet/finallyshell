package app

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"finallyshell/internal/config"
	"finallyshell/internal/monitor"
	"finallyshell/internal/sshx"
	"finallyshell/internal/widgets"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

type keysDeployDialog struct {
	key                               keyRow
	hosts                             []string
	selected                          ui.Selection[string]
	filter, passphrase                string
	verify, switchAuth, clearPassword bool
	confirmed, busy                   bool
	disableConfirm                    bool
	disableAttempted                  bool
	disableSelected                   ui.Selection[string]
	err                               string
	question                          *sshx.HostKeyQuestion
	results                           []keysResult
	cancel                            context.CancelFunc
}

func (a *App) beginKeysDeploy(m *keyManager, key *keyRow) {
	d := &keysDeployDialog{key: *key, verify: true}
	for _, host := range a.hosts.Hosts {
		if !host.Deleted() && !host.IsRDP() {
			d.hosts = append(d.hosts, host.ID)
			if m.hostID == host.ID && execAllowed(host) {
				d.selected.Add(host.ID)
			}
		}
	}
	m.deploy = d
}

func (a *App) keysDeployView(c *ui.Context, m *keyManager) {
	d := m.deploy
	if d == nil {
		return
	}
	open := true
	ui.Modal(c.Key("keys-deploy"), &open, func() {
		_, height := c.Size()
		ui.Column(c).Width(keyModalWidth(c)).Gap(8).Children(func() {
			ui.Text(c, locales.KeysDeploy()).FontSize(17).Bold()
			widgets.Muted(c, filepath.Base(d.key.path)+" · "+d.key.Fingerprint()).SingleLine().FontSize(12)
			ui.Scroll(c).FillWidth().MaxHeight(max(90, height-190)).Children(func() {
				ui.Column(c).FillWidth().Gap(8).Padding(0, 8, 0, 0).Children(func() {
					switch {
					case d.question != nil:
						q := d.question
						ui.Text(c, locales.HostKeyBody(q.Host))
						ui.Text(c, q.Fingerprint).Font("monospace").Selectable()
						ui.Row(c).Gap(8).Wrap().Children(func() {
							if ui.Button(c, locales.Reject()).Clicked() {
								q.Answer <- false
								d.question = nil
							}
							if ui.PrimaryButton(c, locales.TrustAndConnect()).Clicked() {
								q.Answer <- true
								d.question = nil
							}
						})
					case d.disableConfirm:
						ui.Text(c, locales.KeysDisableWarning()).TextColor(theme.Danger)
						for _, result := range d.results {
							if result.verified {
								host := result.target.host
								selected := d.disableSelected.Has(host.ID)
								if ui.Checkbox(c.Key(host.ID), &selected, host.Target()).Changed() {
									if selected {
										d.disableSelected.Add(host.ID)
									} else {
										d.disableSelected.Remove(host.ID)
									}
								}
							}
						}
					case d.busy || len(d.results) > 0:
						a.keysDeployResults(c, d)
					case d.confirmed:
						ui.Text(c, locales.KeysDeployConfirm(d.selected.Len()))
						for _, id := range d.hosts {
							if host := a.hosts.Host(id); host != nil && d.selected.Has(id) {
								ui.Text(c.Key(id), host.Title()+" · "+host.Target())
							}
						}
						if d.switchAuth {
							widgets.Muted(c, locales.KeysSwitchConfirm())
							if d.clearPassword {
								ui.Text(c, locales.KeysClearPasswordConfirm()).TextColor(theme.Warning)
							}
						}
					default:
						a.keysDeployFields(c, d)
					}
					if d.err != "" {
						ui.Text(c, d.err).TextColor(theme.Danger)
					}
				})
			})
			ui.Row(c).FillWidth().Wrap().Gap(8).Justify(ui.End).Children(func() {
				switch {
				case d.busy:
					if ui.Button(c, locales.Cancel()).Clicked() && d.cancel != nil {
						if d.question != nil {
							d.question.Answer <- false
							d.question = nil
						}
						d.cancel()
					}
				case d.disableConfirm:
					if ui.Button(c, locales.Cancel()).AutoFocus().Clicked() {
						d.disableConfirm = false
						d.disableSelected = ui.Selection[string]{}
					}
					if ui.PrimaryButton(c, locales.KeysDisablePassword()).Disabled(d.disableSelected.Len() == 0).Clicked() {
						a.runKeysDisable(m, d)
					}
				case len(d.results) > 0:
					verified := slices.ContainsFunc(d.results, func(r keysResult) bool { return r.verified })
					if ui.Button(c, locales.KeysDisablePassword()).Disabled(!verified || d.disableAttempted).Tooltip(locales.KeysDisableRequires()).Clicked() && verified && !d.disableAttempted {
						d.disableConfirm = true
					}
					if ui.PrimaryButton(c, locales.Done()).Clicked() {
						open = false
					}
				default:
					if ui.Button(c, locales.Cancel()).Clicked() {
						open = false
					}
					if !d.confirmed {
						if ui.PrimaryButton(c, locales.KeysReview()).Disabled(d.selected.Len() == 0).Clicked() && d.selected.Len() > 0 {
							d.confirmed = true
						}
					} else if ui.PrimaryButton(c, locales.KeysDeploy()).Clicked() {
						a.runKeysDeploy(m, d)
					}
				}
			})
		})
	})
	if !open {
		if d.cancel != nil {
			d.cancel()
		}
		d.passphrase, m.deploy = "", nil
	}
}

func (a *App) keysDeployFields(c *ui.Context, d *keysDeployDialog) {
	ui.TextInput(c, &d.filter).Placeholder(locales.KeysHostFilter()).FillWidth()
	filter := strings.ToLower(strings.TrimSpace(d.filter))
	var shown []*config.Host
	for _, id := range d.hosts {
		if host := a.hosts.Host(id); host != nil && !host.Deleted() && !host.IsRDP() && strings.Contains(strings.ToLower(host.Title()+" "+host.Target()), filter) {
			shown = append(shown, host)
		}
	}
	if len(shown) == 0 {
		widgets.Muted(c, locales.KeysNoHosts())
	} else {
		available := slices.ContainsFunc(shown, execAllowed)
		all := available && !slices.ContainsFunc(shown, func(host *config.Host) bool { return execAllowed(host) && !d.selected.Has(host.ID) })
		if ui.Checkbox(c, &all, locales.KeysSelectShown()).Disabled(!available).Changed() {
			for _, host := range shown {
				if all && execAllowed(host) {
					d.selected.Add(host.ID)
				} else {
					d.selected.Remove(host.ID)
				}
			}
		}
		for _, host := range shown {
			allowed := execAllowed(host)
			if !allowed {
				d.selected.Remove(host.ID)
			}
			selected := d.selected.Has(host.ID)
			ui.Column(c.Key(host.ID)).FillWidth().Gap(1).Padding(3, 5).Children(func() {
				if ui.Checkbox(c, &selected, host.Title()).Disabled(!allowed).Changed() {
					if selected {
						d.selected.Add(host.ID)
					} else {
						d.selected.Remove(host.ID)
					}
				}
				hint := host.Target()
				if !allowed {
					hint = locales.KeysExecDisabled()
				}
				widgets.Muted(c, hint).FontSize(11).SingleLine().PaddingStart(21)
			})
		}
	}
	ui.Checkbox(c, &d.verify, locales.KeysVerify())
	ui.Checkbox(c, &d.switchAuth, locales.KeysSwitch()).Disabled(!d.verify)
	if !d.verify {
		d.switchAuth = false
	}
	if d.switchAuth {
		ui.Checkbox(c, &d.clearPassword, locales.KeysClearPassword())
	} else {
		d.clearPassword = false
	}
	if d.verify && d.key.Encrypted {
		ui.Field(c, locales.KeysPassphrase(), func() { ui.TextInput(c, &d.passphrase).Password().FillWidth() })
	}
}

func (a *App) keysDeployResults(c *ui.Context, d *keysDeployDialog) {
	if d.busy {
		ui.Spinner(c).Size(16, 16).Label(locales.KeysWorking())
	}
	for _, result := range d.results {
		host := result.target.host
		ui.Column(c.Key(host.ID)).FillWidth().Padding(8, 10).Gap(4).Radius(6).Background(colors.note).Children(func() {
			ui.Text(c, host.Title()+" · "+host.Target()).Bold().SingleLine()
			status := locales.KeysAppended()
			if result.verified {
				status = locales.KeysVerified()
			}
			if result.switched {
				status = locales.KeysSwitched()
			}
			if result.disabled {
				status = locales.KeysDisabled()
			}
			if result.err != nil {
				if !result.appended {
					status = locales.KeysDeployFailed()
				} else if !result.verified {
					status = locales.KeysVerifyFailed()
				} else if d.disableAttempted {
					status = locales.KeysDisableFailed()
				} else {
					status = locales.KeysSwitchFailed()
				}
				ui.Text(c, status).TextColor(theme.Danger)
				ui.Text(c, describeError(result.err)).FontSize(12).Selectable()
			} else {
				ui.Text(c, status).TextColor(theme.Success)
			}
			if result.output != "" {
				ui.Text(c, result.output).FontSize(12).Selectable()
			}
		})
	}
}

func (a *App) runKeysDeploy(m *keyManager, d *keysDeployDialog) {
	if d.busy || !d.confirmed {
		return
	}
	if d.verify && d.key.Encrypted && d.passphrase == "" {
		d.err, d.confirmed = locales.KeysNeedsPassphrase(), false
		return
	}
	script, err := keysAuthorizedScript(d.key.Authorized())
	if err != nil {
		d.err = locales.KeysNoPublic()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	d.cancel, d.busy, d.err = cancel, true, ""
	ids := slices.DeleteFunc(slices.Clone(d.hosts), func(id string) bool { return !d.selected.Has(id) })
	verify, switchAuth, clearPassword, passphrase, key := d.verify, d.switchAuth, d.clearPassword, d.passphrase, d.key
	go func() {
		if !sshx.KeyMatches(key.path, passphrase, key.Fingerprint(), verify) {
			a.post(func() {
				cancel()
				d.busy, d.cancel, d.confirmed, d.err = false, nil, false, locales.KeysFileChanged()
				a.refreshKeys(m)
			})
			return
		}
		for _, id := range ids {
			if ctx.Err() != nil {
				break
			}
			perHost, stop := context.WithTimeout(ctx, keysHostTimeout)
			target := a.keysTarget(perHost, d, id, false)
			result := m.backend.deploy(perHost, target, script, key.path, passphrase, key.Fingerprint(), verify)
			stop()
			a.post(func() {
				if d != m.deploy {
					return
				}
				if switchAuth && result.verified && ctx.Err() == nil {
					result.err = a.keysSwitchHost(target.host, key.path, passphrase, clearPassword)
					result.switched = result.err == nil
				}
				result.target.opts = sshx.Options{}
				d.results = append(d.results, result)
			})
		}
		a.post(func() {
			cancel()
			d.busy, d.cancel = false, nil
			a.refreshKeys(m)
		})
	}()
}

func (a *App) keysTarget(ctx context.Context, d *keysDeployDialog, id string, onlyKey bool) keysTarget {
	result := make(chan keysTarget, 1)
	finish := func() {
		host := a.hosts.Host(id)
		if host == nil || host.Deleted() || host.IsRDP() {
			result <- keysTarget{host: &config.Host{ID: id}, err: errors.New(locales.ErrHostDeleted())}
			return
		}
		if !execAllowed(host) {
			result <- keysTarget{host: host.Clone(), err: errors.New(locales.KeysExecDisabled())}
			return
		}
		ask := func(q *sshx.HostKeyQuestion) {
			a.post(func() {
				if ctx.Err() != nil {
					q.Answer <- false
				} else {
					d.question = q
				}
			})
			context.AfterFunc(ctx, func() {
				a.post(func() {
					if d.question == q {
						d.question = nil
						select {
						case q.Answer <- false:
						default:
						}
					}
				})
			})
		}
		for _, need := range a.chainSecrets(host) {
			if onlyKey && need.host == host && !need.proxy {
				continue
			}
			if _, known := a.knownSecret(need); !known && !need.passphrase {
				result <- keysTarget{host: host.Clone(), err: errors.New(locales.KeysMissingCredentials())}
				return
			}
		}
		opts, err := a.dialOptions(host, a.passwords[id], ask)
		result <- keysTarget{host: host.Clone(), opts: opts, err: err}
	}
	a.post(func() {
		host := a.hosts.Host(id)
		if host == nil || host.Deleted() || host.IsRDP() {
			finish()
			return
		}
		read := func() {
			if !a.readChainSecrets(host, finish) {
				finish()
			}
		}
		if onlyKey || host.Auth != config.AuthKey || !a.readSecret(loginNeed(host, host), read) {
			read()
		}
	})
	select {
	case target := <-result:
		return target
	case <-ctx.Done():
		return keysTarget{host: &config.Host{ID: id}, err: ctx.Err()}
	}
}

func (a *App) keysSwitchHost(original *config.Host, path, passphrase string, clearPassword bool) error {
	current := a.hosts.Host(original.ID)
	if !keysSameHost(current, original) || current.Auth != original.Auth || current.KeyPath != original.KeyPath {
		return errors.New(locales.KeysHostChanged())
	}
	if err := backupKeyHostConfig(filepath.Join(config.Dir(), "hosts.json"), time.Now()); err != nil {
		return err
	}
	oldAuth, oldPath := current.Auth, current.KeyPath
	current.Auth, current.KeyPath = config.AuthKey, path
	if err := a.saveHosts(a.hosts); err != nil {
		current.Auth, current.KeyPath = oldAuth, oldPath
		return err
	}
	delete(a.secrets.untried, current.ID)
	a.setLogin(current, passphrase)
	if current.RemembersPassword() && passphrase != "" {
		a.setSecret(loginSecretKey(current), passphrase)
	} else {
		a.deleteSecret(loginSecretKey(current))
	}
	if clearPassword {
		a.deleteSecret(hostSecretKey(current.ID, secretPassword))
	}
	if a.editor != nil && a.editor.id == current.ID {
		a.editor.auth, a.editor.keyPath, a.editor.passphrase = current.Auth, path, passphrase
		if clearPassword {
			a.editor.password = ""
		}
	}
	a.redraw()
	return nil
}

func (a *App) saveManagedKeyLogin(h *config.Host, secret string, remember, unread bool) {
	setRemember(h, remember)
	delete(a.secrets.untried, h.ID)
	if unread && remember && secret == "" {
		return
	}
	a.setLogin(h, secret)
	if remember && secret != "" {
		a.setSecret(loginSecretKey(h), secret)
	} else {
		a.deleteSecret(loginSecretKey(h))
	}
}

func backupKeyHostConfig(path string, now time.Time) error {
	input, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer input.Close()
	stamp := now.Format("20060102150405")
	backup := path + ".bak-" + stamp
	for attempt := 0; ; attempt++ {
		if attempt > 0 {
			backup = path + "." + strconv.Itoa(attempt) + ".bak-" + stamp
		}
		output, err := os.OpenFile(backup, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return err
		}
		_, err = io.Copy(output, input)
		err = errors.Join(err, output.Close())
		if err != nil {
			os.Remove(backup)
		}
		return err
	}
}

func keysSameHost(current, original *config.Host) bool {
	return current != nil && !current.Deleted() && !current.IsRDP() && current.ID == original.ID && current.Addr == original.Addr && current.User == original.User && current.PortOr22() == original.PortOr22() && reflect.DeepEqual(current.Proxy, original.Proxy)
}

func (a *App) runKeysDisable(m *keyManager, d *keysDeployDialog) {
	if d.busy || !d.disableConfirm || d.disableAttempted || d.disableSelected.Len() == 0 {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	d.cancel, d.busy, d.disableConfirm, d.disableAttempted = cancel, true, false, true
	results, key, passphrase := slices.Clone(d.results), d.key, d.passphrase
	selected := make(map[string]bool)
	for _, result := range results {
		if result.verified && d.disableSelected.Has(result.target.host.ID) {
			selected[result.target.host.ID] = true
		}
	}
	go func() {
		defer cancel()
		for index, result := range results {
			if !selected[result.target.host.ID] || ctx.Err() != nil {
				continue
			}
			perHost, stop := context.WithTimeout(ctx, keysHostTimeout)
			target := a.keysTarget(perHost, d, result.target.host.ID, true)
			var remote keysRemote
			err := target.err
			if err == nil && !keysSameHost(target.host, result.target.host) {
				err = errors.New(locales.KeysHostChanged())
			}
			keyHost, opts := target.keyLogin(key.path, passphrase, key.Fingerprint())
			opts.AskHostKey = nil
			if err == nil {
				remote, err = m.backend.connect(perHost, keyHost, opts)
			}
			if err == nil {
				out, stderr, runErr := a.keysDisableRemote(perHost, remote, keyHost)
				result.output, err = keysOutput(out, stderr, ""), runErr
				if remote.close != nil {
					remote.close()
				}
			}
			stop()
			result.disabled, result.err = err == nil, err
			a.post(func() { d.results[index] = result })
		}
		a.post(func() { d.busy, d.cancel, d.passphrase = false, nil, "" })
	}()
}

func (a *App) keysDisableRemote(ctx context.Context, remote keysRemote, host *config.Host) (string, string, error) {
	ready := make(chan *monitor.Privilege, 1)
	a.post(func() {
		if ctx.Err() != nil || !keysSameHost(a.hosts.Host(host.ID), host) || remote.client == nil && remote.privilege == nil {
			ready <- nil
			return
		}
		session := newSession(host)
		session.client, session.ctx = remote.client, ctx
		session.overview.memory.privilegeRunner = remote.privilege
		ready <- a.sessionPrivilege(session).runner
	})
	select {
	case runner := <-ready:
		if runner != nil {
			return runner.Run(ctx, keysDisablePasswordScript, monitor.PrivilegeOptions{})
		}
		return "", "", errors.New(locales.KeysHostChanged())
	case <-ctx.Done():
		return "", "", ctx.Err()
	}
}
