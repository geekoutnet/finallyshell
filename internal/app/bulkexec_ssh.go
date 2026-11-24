package app

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"unicode"

	"finallyshell/internal/config"
	"finallyshell/internal/monitor"
	"finallyshell/internal/sshx"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
	"golang.org/x/crypto/ssh"
)

var errBulkTargetChanged = errors.New("target is no longer available")
var errBulkScriptSecret = errors.New("script contains a credential")

type bulkConnectPlan struct {
	host   *config.Host
	opts   sshx.Options
	client *ssh.Client
}

func (a *App) bulkConnectPlan(ctx context.Context, target bulkTarget) (bulkConnectPlan, error) {
	type reply struct {
		plan bulkConnectPlan
		err  error
	}
	ready := make(chan reply, 1)
	a.post(func() {
		host := a.hosts.Host(target.id)
		if !bulkEligible(host) || host.Addr != target.address || host.User != target.user || host.PortOr22() != target.port {
			ready <- reply{err: errBulkTargetChanged}
			return
		}
		if ctx.Err() != nil {
			return
		}
		original := host.Clone()
		finish := func() {
			if ctx.Err() != nil {
				return
			}
			current := a.hosts.Host(target.id)
			if !bulkEligible(current) || !hostMatchesPrivilege(current, original) {
				ready <- reply{err: errBulkTargetChanged}
				return
			}
			opts, err := a.dialOptions(current, a.passwords[current.ID], nil)
			opts.KeepAlive = a.keepAlive()
			var client *ssh.Client
			for _, session := range a.sessions {
				if session.status == statusConnected && session.client != nil && !session.isLocal() && session.Host == current && session.client.User() == current.User {
					client = session.client
					break
				}
			}
			ready <- reply{bulkConnectPlan{host: current.Clone(), opts: opts, client: client}, err}
		}
		var pending []*secretNeed
		for _, need := range a.chainSecrets(host) {
			if a.unread(need) {
				pending = append(pending, need)
			}
		}
		if len(pending) == 0 {
			finish()
			return
		}
		for _, need := range pending {
			a.readSecret(need, func() {
				pending = pending[1:]
				if len(pending) == 0 {
					finish()
				}
			})
		}
	})
	select {
	case value := <-ready:
		return value.plan, value.err
	case <-ctx.Done():
		return bulkConnectPlan{}, ctx.Err()
	}
}

func bulkExitCode(err error) int {
	var exit *ssh.ExitError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &exit):
		return exit.ExitStatus()
	default:
		return -1
	}
}

func bulkSecrets(opts sshx.Options) []string {
	secrets := []string{opts.Password, opts.Passphrase, opts.ProxyPassword}
	for hop := opts.Jump; hop != nil; hop = hop.Options.Jump {
		secrets = append(secrets, hop.Options.Password, hop.Options.Passphrase, hop.Options.ProxyPassword)
	}
	return slices.DeleteFunc(secrets, func(value string) bool { return value == "" })
}

func bulkContainsSecret(text string, secrets []string) bool {
	return strings.Contains(text, "PRIVATE KEY-----") || slices.ContainsFunc(secrets, func(secret string) bool {
		return strings.Contains(text, secret)
	})
}

func bulkScrub(text string, secrets []string) string {
	if strings.Contains(text, "PRIVATE KEY-----") {
		return "[redacted]"
	}
	ordered := slices.Clone(secrets)
	slices.SortFunc(ordered, func(left, right string) int { return len(right) - len(left) })
	var pairs []string
	for _, secret := range ordered {
		if secret != "" {
			pairs = append(pairs, secret, "[redacted]")
		}
	}
	if len(pairs) != 0 {
		text = strings.NewReplacer(pairs...).Replace(text)
	}
	return strings.Map(func(char rune) rune {
		if unicode.IsControl(char) && char != '\t' && char != '\n' && char != '\r' {
			return '�'
		}
		return char
	}, strings.ToValidUTF8(text, "�"))
}

func bulkLimit(result *bulkResult, secrets []string) {
	if result.truncated || len(result.stdout) >= monitor.BulkOutputLimit || len(result.stderr) >= 64<<10 {
		longest := 0
		for _, secret := range secrets {
			longest = max(longest, len(secret))
		}
		for _, stream := range []*string{&result.stdout, &result.stderr} {
			for size := min(len(*stream), longest-1); size > 0; size-- {
				if slices.ContainsFunc(secrets, func(secret string) bool { return strings.HasPrefix(secret, (*stream)[len(*stream)-size:]) }) {
					*stream = (*stream)[:len(*stream)-size] + "[redacted]"
					break
				}
			}
		}
	}
	result.stdout = bulkScrub(result.stdout, secrets)
	result.stderr = bulkScrub(result.stderr, secrets)
	if len(result.stdout)+len(result.stderr) > monitor.BulkOutputLimit {
		result.truncated = true
		result.stdout = strings.ToValidUTF8(result.stdout[:min(len(result.stdout), monitor.BulkOutputLimit)], "")
		result.stderr = strings.ToValidUTF8(result.stderr[:min(len(result.stderr), monitor.BulkOutputLimit-len(result.stdout))], "")
	}
	if result.err != nil {
		if detail := bulkScrub(result.err.Error(), secrets); detail != result.err.Error() {
			result.err = errors.New(detail)
		}
	}
}

func bulkErrorText(err error) string {
	var exit *ssh.ExitError
	switch {
	case err == nil:
		return ""
	case errors.Is(err, sshx.ErrBulkHostKey):
		var hop *sshx.HopError
		if errors.As(err, &hop) && hop.JumpHost != "" {
			return locales.ErrViaJump(hop.JumpHost, locales.BulkHostKey())
		}
		return locales.BulkHostKey()
	case errors.Is(err, sshx.ErrBulkAddress):
		return locales.BulkAddressInvalid()
	case errors.Is(err, errBulkTargetChanged):
		return locales.BulkTargetChanged()
	case errors.Is(err, errBulkScriptSecret):
		return locales.BulkScriptSecret()
	case errors.Is(err, monitor.ErrPrivilegeCancelled), errors.Is(err, context.Canceled):
		return locales.BulkCancelled()
	case errors.Is(err, context.DeadlineExceeded):
		return locales.BulkTimeout()
	case errors.Is(err, monitor.ErrPrivilegeWrongPassword):
		return locales.PrivWrongPassword() + " " + locales.PrivAdminNote()
	case errors.Is(err, monitor.ErrPrivilegeDenied):
		return locales.PrivNeedsRoot() + " " + locales.PrivAdminNote()
	case errors.As(err, &exit) && exit.ExitStatus() >= 0:
		return ""
	default:
		return describeError(err)
	}
}

func (a *App) bulkSSH(prompts *bulkPrompts) bulkRunner {
	return func(ctx context.Context, target bulkTarget, request bulkRequest, running func()) bulkResult {
		plan, err := a.bulkConnectPlan(ctx, target)
		if err != nil {
			return bulkResult{exit: -1, err: err}
		}
		secrets := bulkSecrets(plan.opts)
		if bulkContainsSecret(request.script, secrets) {
			return bulkResult{exit: -1, err: errBulkScriptSecret}
		}
		client := plan.client
		if client != nil && !sshx.MatchesBulkPeer(ctx, client, plan.host) {
			client = nil
		}
		if client == nil {
			client, err = sshx.DialBulk(ctx, plan.host, plan.opts)
			if err != nil {
				result := bulkResult{exit: -1, err: err}
				bulkLimit(&result, secrets)
				return result
			}
			defer client.Close()
		}
		if err := ctx.Err(); err != nil {
			return bulkResult{exit: -1, err: err}
		}
		running()
		var result bulkResult
		if request.root {
			result.stdout, result.stderr, result.err = a.bulkRoot(ctx, plan, prompts, monitor.BulkPrivilegeRunner(client), request.script)
			result.truncated = len(result.stdout) >= monitor.BulkOutputLimit-monitor.BulkRootStderrLimit || len(result.stderr) >= monitor.BulkRootStderrLimit
		} else {
			result.stdout, result.stderr, result.truncated, result.err = monitor.RunBulkScript(ctx, client, request.script)
		}
		result.exit = bulkExitCode(result.err)
		bulkLimit(&result, secrets)
		return result
	}
}

type bulkPrompts struct {
	gate   chan struct{}
	shared map[monitor.PrivilegeMethod][]byte
}

func newBulkPrompts() *bulkPrompts {
	return &bulkPrompts{gate: make(chan struct{}, 1), shared: make(map[monitor.PrivilegeMethod][]byte)}
}

func (a *App) bulkRoot(ctx context.Context, plan bulkConnectPlan, prompts *bulkPrompts, run monitor.PrivilegeRunner, script string) (string, string, error) {
	key := privilegeSecretKey(plan.host, plan.host.User)
	var asked, reused bool
	credentials := monitor.PrivilegeCredentials{
		Login: func(context.Context) []byte { return []byte(plan.opts.Password) },
		Load: func(ctx context.Context) monitor.PrivilegeAnswer {
			stored := a.privilegeSecret(ctx, func(deliver func(string)) {
				a.secret(key, func(value string, _ bool) { deliver(value) })
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
			select {
			case prompts.gate <- struct{}{}:
				defer func() { <-prompts.gate }()
			case <-ctx.Done():
				return monitor.PrivilegeAnswer{}, ctx.Err()
			}
			if !reused && !asked && len(prompts.shared[method]) > 0 && !bytes.Equal(prompts.shared[method], []byte(plan.opts.Password)) {
				reused = true
				return monitor.PrivilegeAnswer{Method: method, Password: bytes.Clone(prompts.shared[method])}, nil
			}
			if asked {
				if wrong {
					return monitor.PrivilegeAnswer{}, monitor.ErrPrivilegeWrongPassword
				}
				return monitor.PrivilegeAnswer{}, monitor.ErrPrivilegeDenied
			}
			asked = true
			answer, all, err := a.bulkAskPassword(ctx, plan.host, method, wrong)
			if err == nil && all {
				clear(prompts.shared[answer.Method])
				prompts.shared[answer.Method] = bytes.Clone(answer.Password)
			}
			return answer, err
		},
		Save: func(answer monitor.PrivilegeAnswer) {
			if !answer.Remember {
				return
			}
			method := "sudo"
			if answer.Method == monitor.PrivilegeSu {
				method = "su"
			}
			value := method + "\n" + string(answer.Password)
			a.post(func() {
				if hostMatchesPrivilege(a.hosts.Host(plan.host.ID), plan.host) {
					a.setSecret(key, value)
				}
			})
		},
	}
	checked := func(ctx context.Context, request monitor.PrivilegeRequest) (monitor.PrivilegeResult, error) {
		if len(request.Password) > 0 && strings.Contains(script, string(request.Password)) {
			return monitor.PrivilegeResult{}, errBulkScriptSecret
		}
		return run(ctx, request)
	}
	return monitor.NewPrivilege(ctx, checked, credentials).Run(ctx, "exec 'sh' '-s'", monitor.PrivilegeOptions{Stdin: strings.NewReader(script)})
}

const windowBulkPassword WindowKind = "bulk-password"

func (a *App) bulkAskPassword(ctx context.Context, host *config.Host, method monitor.PrivilegeMethod, wrong bool) (monitor.PrivilegeAnswer, bool, error) {
	type response struct {
		answer monitor.PrivilegeAnswer
		all    bool
	}
	reply := make(chan response, 1)
	opened := false
	a.post(func() {
		if ctx.Err() != nil || !a.windowOpen(windowBulkExec) || a.windowOpen(windowBulkPassword) {
			reply <- response{}
			return
		}
		question := &privilegeQuestion{method: method, wrong: wrong, user: host.User, host: host.Addr}
		var all, submit bool
		opened = true
		a.openWindow(WindowSpec{Kind: windowBulkPassword, Parent: windowBulkExec, Modal: true, Title: locales.PrivTitle,
			Width: 480, Height: 315, MinWidth: 420, MinHeight: 315, Closed: func() {
				opened = false
				var answer monitor.PrivilegeAnswer
				if submit && ctx.Err() == nil {
					answer = monitor.PrivilegeAnswer{Method: question.method, Password: []byte(question.password), Remember: question.remember}
				}
				question.password = ""
				reply <- response{answer, all}
			}}, func(c *ui.Context) {
			ui.Column(c).Fill().Children(func() {
				ui.Checkbox(c, &all, locales.BulkUsePasswordAll()).Margin(12, 22, 0, 22)
				a.privilegeView(c, question, method == monitor.PrivilegeSudo, func(ok bool) {
					submit = ok
					a.closeWindow(windowBulkPassword)
				})
			})
		})
	})
	select {
	case outcome := <-reply:
		if outcome.answer.Method == 0 {
			return outcome.answer, false, monitor.ErrPrivilegeCancelled
		}
		return outcome.answer, outcome.all, nil
	case <-ctx.Done():
		a.post(func() {
			if opened {
				a.closeWindow(windowBulkPassword)
			}
			select {
			case outcome := <-reply:
				clear(outcome.answer.Password)
			default:
			}
		})
		return monitor.PrivilegeAnswer{}, false, ctx.Err()
	}
}
