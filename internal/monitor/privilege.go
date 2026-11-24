package monitor

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"io"
	"strings"
	"sync/atomic"

	"golang.org/x/crypto/ssh"
)

var (
	ErrPrivilegeDenied        = errors.New("root privileges unavailable")
	ErrPrivilegeCancelled     = errors.New("privilege request cancelled")
	ErrPrivilegeWrongPassword = errors.New("incorrect privilege password")
	ErrPrivilegeNotCached     = errors.New("no authorized root method cached")
)

type PrivilegeMethod int

const (
	privilegeRoot PrivilegeMethod = iota + 1
	privilegeSudoNoninteractive
	PrivilegeSudo
	PrivilegeSu
)

type PrivilegeOptions struct {
	// Stdin is read only after the command reaches its root shell. If it
	// can block, the caller must arrange for cancellation to unblock it.
	Stdin       io.Reader
	Progress    func(string)
	ScriptStdin bool
	Interrupt   bool
}

type PrivilegeAnswer struct {
	Method   PrivilegeMethod
	Password []byte
	Remember bool
}

type PrivilegeCredentials struct {
	Login func(context.Context) []byte
	Load  func(context.Context) PrivilegeAnswer
	Ask   func(context.Context, PrivilegeMethod, bool) (PrivilegeAnswer, error)
	Save  func(PrivilegeAnswer)
}

type PrivilegeRequest struct {
	Command    string
	Action     string
	Probe      bool
	PTY        bool
	Password   []byte
	Stdin      io.Reader
	method     PrivilegeMethod
	prompt     string
	ready      string
	script     io.Reader
	progress   func(string)
	frameInput bool
	interrupt  bool
}

type PrivilegeResult struct {
	Stdout, Stderr string
	Started        bool
	Authenticated  bool
	Rejected       error
}

type PrivilegeRunner func(context.Context, PrivilegeRequest) (PrivilegeResult, error)

type Privilege struct {
	life        context.Context
	run         PrivilegeRunner
	credentials PrivilegeCredentials
	gate        chan struct{}
	method      PrivilegeMethod
	cached      atomic.Int32
	password    []byte
	triedLogin  bool
	triedStored bool
}

func NewPrivilege(life context.Context, run PrivilegeRunner, credentials PrivilegeCredentials) *Privilege {
	priv := &Privilege{life: life, run: run, credentials: credentials, gate: make(chan struct{}, 1)}
	context.AfterFunc(life, func() {
		priv.gate <- struct{}{}
		priv.forget()
		<-priv.gate
	})
	return priv
}

func (priv *Privilege) forget() {
	clear(priv.password)
	priv.password, priv.method = nil, 0
	priv.cached.Store(0)
}

func (priv *Privilege) CachedMethod() PrivilegeMethod { return PrivilegeMethod(priv.cached.Load()) }

func (priv *Privilege) TerminalCommand(command string) string {
	switch priv.CachedMethod() {
	case privilegeRoot:
		return command
	case privilegeSudoNoninteractive:
		return "sudo -n " + command
	case PrivilegeSu:
		return "su '-s' '/bin/sh' '-c' " + privilegeQuote(command) + " 'root'"
	default:
		return "sudo " + command
	}
}

// RunCached uses only an already authorized method, without opening a dialog.
// It releases the authentication gate before running so a stream cannot block other requests.
func (priv *Privilege) RunCached(ctx context.Context, script string, opts PrivilegeOptions) (string, string, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(priv.life, cancel)
	defer stop()
	select {
	case priv.gate <- struct{}{}:
	case <-ctx.Done():
		return "", "", ctx.Err()
	}
	answer := PrivilegeAnswer{Method: priv.method, Password: bytes.Clone(priv.password)}
	<-priv.gate
	defer clear(answer.Password)
	if err := priv.life.Err(); err != nil {
		return "", "", err
	}
	if answer.Method == 0 {
		return "", "", ErrPrivilegeNotCached
	}
	result, err := priv.attempt(ctx, script, opts, answer)
	if !result.Started && privilegeAuthError(err) {
		return "", "", ErrPrivilegeNotCached
	}
	return result.Stdout, result.Stderr, err
}

func (request PrivilegeRequest) ReportProgress(chunk string) {
	if request.progress != nil {
		request.progress(redactPrivilegeText(chunk, request.Password))
	}
}

// Run serializes authentication within a connection and never retries an action
// that reached its root shell. Progress reports sanitized stdout as it arrives.
func (priv *Privilege) Run(ctx context.Context, script string, opts PrivilegeOptions) (string, string, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(priv.life, cancel)
	defer stop()
	select {
	case priv.gate <- struct{}{}:
		defer func() { <-priv.gate }()
	case <-ctx.Done():
		return "", "", ctx.Err()
	}
	if err := priv.life.Err(); err != nil {
		return "", "", err
	}
	if err := ctx.Err(); err != nil {
		return "", "", err
	}
	return priv.runAction(ctx, script, opts)
}

func (priv *Privilege) runAction(ctx context.Context, script string, opts PrivilegeOptions) (string, string, error) {
	tried := make(map[PrivilegeMethod]map[[32]byte]bool)
	try := func(answer PrivilegeAnswer) (PrivilegeResult, error) {
		if len(answer.Password) != 0 {
			digest := sha256.Sum256(answer.Password)
			if tried[answer.Method][digest] {
				return PrivilegeResult{}, ErrPrivilegeWrongPassword
			}
			if tried[answer.Method] == nil {
				tried[answer.Method] = make(map[[32]byte]bool)
			}
			tried[answer.Method][digest] = true
		}
		result, err := priv.attempt(ctx, script, opts, answer)
		if result.Started {
			method, password := answer.Method, answer.Password
			if method == PrivilegeSudo && !result.Authenticated {
				method, password = privilegeSudoNoninteractive, nil
			}
			if priv.method != method || !bytes.Equal(priv.password, password) {
				next := bytes.Clone(password)
				priv.forget()
				priv.method, priv.password = method, next
				priv.cached.Store(int32(method))
			}
		}
		return result, err
	}
	method := priv.method
	wrong := false
	if method != 0 {
		result, err := try(PrivilegeAnswer{Method: method, Password: priv.password})
		if result.Started || !privilegeAuthError(err) {
			return result.Stdout, result.Stderr, err
		}
		priv.forget()
		method, wrong = privilegeFallback(method, err)
	} else {
		result, err := priv.run(ctx, PrivilegeRequest{Command: privilegeProbe, Probe: true})
		if err != nil {
			return "", "", err
		}
		switch strings.TrimSpace(result.Stdout) {
		case "root":
			method = privilegeRoot
		case "sudo-n":
			method = privilegeSudoNoninteractive
		case "sudo":
			method = PrivilegeSudo
		case "su":
			method = PrivilegeSu
		default:
			return "", "", ErrPrivilegeDenied
		}
		if method < PrivilegeSudo {
			result, err := try(PrivilegeAnswer{Method: method})
			if result.Started || !privilegeAuthError(err) {
				return result.Stdout, result.Stderr, err
			}
			method, wrong = privilegeFallback(method, err)
		}
	}
	if method == PrivilegeSudo && !priv.triedLogin && priv.credentials.Login != nil {
		priv.triedLogin = true
		password := priv.credentials.Login(ctx)
		if len(password) != 0 {
			result, err := try(PrivilegeAnswer{Method: method, Password: password})
			clear(password)
			if result.Started || !privilegeAuthError(err) {
				return result.Stdout, result.Stderr, err
			}
			method, wrong = privilegeFallback(method, err)
		}
	}
	if method != 0 && !priv.triedStored && priv.credentials.Load != nil {
		priv.triedStored = true
		answer := priv.credentials.Load(ctx)
		if len(answer.Password) != 0 && (answer.Method == method || method == PrivilegeSudo && answer.Method == PrivilegeSu) {
			result, err := try(answer)
			clear(answer.Password)
			if result.Started || !privilegeAuthError(err) {
				return result.Stdout, result.Stderr, err
			}
			method, wrong = privilegeFallback(answer.Method, err)
		}
		clear(answer.Password)
	}
	for entered := 0; entered < 3; entered++ {
		if method == 0 || priv.credentials.Ask == nil {
			return "", "", ErrPrivilegeDenied
		}
		if err := ctx.Err(); err != nil {
			return "", "", err
		}
		answer, err := priv.credentials.Ask(ctx, method, wrong)
		if err != nil {
			clear(answer.Password)
			return "", "", err
		}
		if answer.Method != method && answer.Method != PrivilegeSu {
			clear(answer.Password)
			return "", "", ErrPrivilegeDenied
		}
		result, err := try(answer)
		if result.Authenticated && priv.credentials.Save != nil && ctx.Err() == nil {
			priv.credentials.Save(answer)
		}
		clear(answer.Password)
		if result.Started || !privilegeAuthError(err) {
			return result.Stdout, result.Stderr, err
		}
		method, wrong = privilegeFallback(answer.Method, err)
	}
	if wrong {
		return "", "", ErrPrivilegeWrongPassword
	}
	return "", "", ErrPrivilegeDenied
}

func privilegeAuthError(err error) bool {
	return errors.Is(err, ErrPrivilegeWrongPassword) || errors.Is(err, ErrPrivilegeDenied) || errors.Is(err, errPrivilegePasswordRequired)
}

var errPrivilegePasswordRequired = errors.New("sudo password required")

func privilegeFallback(method PrivilegeMethod, err error) (PrivilegeMethod, bool) {
	if errors.Is(err, ErrPrivilegeWrongPassword) {
		return method, true
	}
	switch method {
	case privilegeSudoNoninteractive:
		if errors.Is(err, errPrivilegePasswordRequired) {
			return PrivilegeSudo, false
		}
		return PrivilegeSu, false
	case PrivilegeSudo:
		return PrivilegeSu, false
	default:
		return 0, false
	}
}

func (priv *Privilege) attempt(ctx context.Context, script string, opts PrivilegeOptions, answer PrivilegeAnswer) (PrivilegeResult, error) {
	if err := ctx.Err(); err != nil {
		return PrivilegeResult{}, err
	}
	if answer.Method >= PrivilegeSudo && (len(answer.Password) == 0 || bytes.ContainsAny(answer.Password, "\r\n\x00")) {
		return PrivilegeResult{}, ErrPrivilegeWrongPassword
	}
	if answer.Method == PrivilegeSu && bytes.IndexFunc(answer.Password, func(char rune) bool { return char < ' ' || char == '\x7f' }) >= 0 {
		return PrivilegeResult{}, ErrPrivilegeWrongPassword
	}
	request := privilegeCommand(script, opts, answer)
	result, err := priv.run(ctx, request)
	var exit *ssh.ExitError
	if !result.Started && ctx.Err() == nil {
		if result.Rejected != nil {
			err = result.Rejected
		} else if reason := privilegeRejection(answer.Method, result.Stderr+"\n"+result.Stdout); reason != nil {
			err = reason
		} else if err == nil || errors.As(err, &exit) && exit.ExitStatus() >= 0 {
			err = ErrPrivilegeDenied
		}
	}
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	if len(answer.Password) != 0 {
		result.Stdout = redactPrivilegeText(result.Stdout, answer.Password)
		result.Stderr = redactPrivilegeText(result.Stderr, answer.Password)
		if err != nil && !privilegeAuthError(err) && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			if detail := redactPrivilegeText(err.Error(), answer.Password); detail != err.Error() {
				err = errors.New(detail)
			}
		}
	}
	return result, err
}

func redactPrivilegeText(text string, password []byte) string {
	if len(password) != 0 && bytes.Contains([]byte(text), password) {
		return string(bytes.ReplaceAll([]byte(text), password, privilegeReplacement(password)))
	}
	return text
}

func privilegeReplacement(password []byte) []byte {
	if bytes.Contains([]byte("[redacted]"), password) {
		return []byte("*")
	}
	return []byte("[redacted]")
}

func privilegeQuote(text string) string { return "'" + strings.ReplaceAll(text, "'", `'\''`) + "'" }

var privilegeProbe = "sh -c " + privilegeQuote(`export LC_ALL=C
uid=$(id -u) || exit 1
if [ "$uid" = 0 ]; then
    echo root
elif ! command -v sudo >/dev/null 2>&1; then
    echo su
elif denial=$(sudo -n -u root -- true 2>&1); then
    echo sudo-n
else
    case "$denial" in
        *'is not in the sudoers file'*|*'may not run sudo'*|*'is not allowed to run sudo'*|*'must have a tty to run sudo'*) echo su ;;
        *) echo sudo ;;
    esac
fi`)

func privilegeCommand(script string, opts PrivilegeOptions, answer PrivilegeAnswer) PrivilegeRequest {
	request := PrivilegeRequest{Action: script, Password: answer.Password, method: answer.Method, ready: "ready-" + rand.Text(), Stdin: opts.Stdin, progress: opts.Progress, interrupt: opts.Interrupt}
	body := `export LC_ALL=C
[ "$(id -u)" = 0 ] || exit 125
printf '%s\n' ` + request.ready + "\n"
	command := "exec sh -c " + privilegeQuote(script)
	if opts.Stdin == nil && (opts.ScriptStdin || answer.Method < PrivilegeSudo) {
		command = "exec sh -s"
		request.script = strings.NewReader(script)
	}
	switch answer.Method {
	case privilegeRoot:
		request.Command = "env LC_ALL=C sh -c " + privilegeQuote(body+command)
	case privilegeSudoNoninteractive:
		request.Command = "env LC_ALL=C sudo -n -u root -- sh -c " + privilegeQuote(body+command)
	case PrivilegeSudo:
		// A private prompt prevents feeding a password to an already authorized
		// command. The script is shell-quoted in argv, not behind the password
		// in stdin: sudo/PAM may retry reads. Payload waits for the root marker.
		request.prompt = "password-" + rand.Text() + ":"
		request.Command = "env LC_ALL=C sudo -k -S -p " + request.prompt + " -u root -- sh -c " + privilegeQuote(body+command)
	case PrivilegeSu:
		request.PTY, request.prompt = true, "Password:"
		if request.script != nil {
			request.frameInput = true
			command = privilegeFrameDecoder + "sh -s"
		} else if opts.Stdin != nil {
			request.frameInput = true
			command = privilegeFrameDecoder + "sh -c " + privilegeQuote(script)
		} else {
			command += " </dev/null"
		}
		request.Command = "env LC_ALL=C su -s /bin/sh -c " + privilegeQuote(body+command) + " root"
	}
	return request
}

const privilegeFrameDecoder = `while IFS= read -r frame && [ "$frame" != . ]; do printf '%b' "$frame"; done | `

func privilegeRejection(method PrivilegeMethod, text string) error {
	for line := range strings.SplitSeq(text, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case method == PrivilegeSu && strings.HasPrefix(line, "su: Authentication failure"):
			return ErrPrivilegeWrongPassword
		case method == PrivilegeSu && strings.HasPrefix(line, "su:"):
			return ErrPrivilegeDenied
		case method == PrivilegeSu && (strings.HasPrefix(line, "env: su:") || strings.HasPrefix(line, "env: 'su':") || strings.HasPrefix(line, "env: ‘su’:")):
			return ErrPrivilegeDenied
		case method == PrivilegeSudo && (line == "Sorry, try again." || strings.HasPrefix(line, "sudo: ") && (strings.HasSuffix(line, " incorrect password attempt") || strings.HasSuffix(line, " incorrect password attempts"))):
			return ErrPrivilegeWrongPassword
		case strings.HasPrefix(line, "sudo: ") && strings.HasSuffix(line, "a password is required"):
			return errPrivilegePasswordRequired
		case strings.HasPrefix(line, "sudo: "), strings.HasSuffix(line, "is not in the sudoers file."), strings.Contains(line, " may not run sudo "), strings.HasSuffix(line, " may not run sudo."):
			return ErrPrivilegeDenied
		}
	}
	return nil
}
