package monitor

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

func TestPrivilegeOrderAndCache(t *testing.T) {
	for _, test := range []struct {
		name, probe, prefix string
		method              PrivilegeMethod
	}{
		{"root", "root", "env LC_ALL=C sh", 0},
		{"sudo without password", "sudo-n", "env LC_ALL=C sudo -n", 0},
		{"sudo with password", "sudo", "env LC_ALL=C sudo -k -S", PrivilegeSudo},
		{"su without sudo", "su", "env LC_ALL=C su", PrivilegeSu},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls, prompts int
			life, cancel := context.WithCancel(t.Context())
			defer cancel()
			priv := NewPrivilege(life, func(_ context.Context, request PrivilegeRequest) (PrivilegeResult, error) {
				calls++
				if request.Probe {
					if calls != 1 {
						t.Error("probed a cached connection")
					}
					return PrivilegeResult{Stdout: test.probe}, nil
				}
				if !strings.HasPrefix(request.Command, test.prefix) || request.PTY != (test.method == PrivilegeSu) {
					t.Error("unexpected transport")
				}
				if (len(request.Password) != 0) != (test.method != 0) {
					t.Error("unexpected credential")
				}
				return PrivilegeResult{Started: true, Authenticated: test.method != 0, Stdout: "ok"}, nil
			}, PrivilegeCredentials{Ask: func(_ context.Context, method PrivilegeMethod, wrong bool) (PrivilegeAnswer, error) {
				prompts++
				if test.method != method || wrong {
					t.Error("unexpected prompt")
				}
				return PrivilegeAnswer{Method: method, Password: []byte("fixture-only")}, nil
			}})
			for range 2 {
				out, _, err := priv.Run(t.Context(), "echo ok", PrivilegeOptions{})
				if out != "ok" || err != nil {
					t.Fatalf("run = %q, %v", out, err)
				}
			}
			if calls != 3 || prompts > 1 {
				t.Fatalf("calls = %d, prompts = %d", calls, prompts)
			}
			cancel()
			if _, _, err := priv.Run(t.Context(), "never", PrivilegeOptions{}); !errors.Is(err, context.Canceled) || calls != 3 {
				t.Fatal("disconnected runner reused")
			}
			deadline := time.After(time.Second)
			for {
				priv.gate <- struct{}{}
				forgot := priv.method == 0 && len(priv.password) == 0
				<-priv.gate
				if forgot {
					break
				}
				select {
				case <-time.After(time.Millisecond):
				case <-deadline:
					t.Fatal("disconnection did not clear the cached method and password")
				}
			}
		})
	}
}

func TestPrivilegePasswordRetries(t *testing.T) {
	const login = "fixture-login"
	var attempts, prompts int
	priv := NewPrivilege(t.Context(), func(_ context.Context, request PrivilegeRequest) (PrivilegeResult, error) {
		if request.Probe {
			return PrivilegeResult{Stdout: "sudo"}, nil
		}
		attempts++
		if strings.Contains(request.Command, string(request.Password)) {
			t.Error("password in command")
		}
		if attempts == 1 && string(request.Password) != login || attempts != 1 && string(request.Password) == login {
			t.Error("login password tried out of order or twice")
		}
		return PrivilegeResult{Stderr: "sudo: 1 incorrect password attempt"}, errors.New("exit status 1")
	}, PrivilegeCredentials{
		Login: func(context.Context) []byte { return []byte(login) },
		Ask: func(_ context.Context, method PrivilegeMethod, wrong bool) (PrivilegeAnswer, error) {
			prompts++
			if !wrong {
				t.Error("incorrect password not reported")
			}
			passwords := []string{login, "fixture-two", "fixture-three", "fixture-four"}
			return PrivilegeAnswer{Method: method, Password: []byte(passwords[min(prompts-1, len(passwords)-1)])}, nil
		},
	})
	if _, _, err := priv.Run(t.Context(), "true", PrivilegeOptions{}); !errors.Is(err, ErrPrivilegeWrongPassword) || attempts != 3 || prompts != 3 {
		t.Fatalf("attempts = %d, prompts = %d, error = %v", attempts, prompts, err)
	}
}

func TestPrivilegeDoesNotRetryStartedAction(t *testing.T) {
	failure := errors.New("exit 1")
	var calls int
	priv := NewPrivilege(t.Context(), func(_ context.Context, request PrivilegeRequest) (PrivilegeResult, error) {
		calls++
		if request.Probe {
			return PrivilegeResult{Stdout: "sudo-n"}, nil
		}
		return PrivilegeResult{Started: true, Stderr: "sudo: a password is required"}, failure
	}, PrivilegeCredentials{})
	if _, _, err := priv.Run(t.Context(), "action", PrivilegeOptions{Stdin: strings.NewReader("input")}); !errors.Is(err, failure) || calls != 2 {
		t.Fatalf("started action ran again: calls = %d, error = %v", calls, err)
	}
}

func TestPrivilegeLoginAndStoredCredentials(t *testing.T) {
	for _, saved := range []bool{false, true} {
		var answers []string
		priv := NewPrivilege(t.Context(), func(_ context.Context, request PrivilegeRequest) (PrivilegeResult, error) {
			if request.Probe {
				return PrivilegeResult{Stdout: "sudo"}, nil
			}
			answers = append(answers, string(request.Password))
			if saved && len(answers) == 1 {
				return PrivilegeResult{Stderr: "sudo: 1 incorrect password attempt"}, errors.New("exit 1")
			}
			return PrivilegeResult{Started: true, Authenticated: true}, nil
		}, PrivilegeCredentials{
			Login: func(context.Context) []byte { return []byte("fixture-login") },
			Load: func(context.Context) PrivilegeAnswer {
				if !saved {
					t.Error("loaded stored credentials despite successful login password")
				}
				return PrivilegeAnswer{Method: PrivilegeSudo, Password: []byte("fixture-stored")}
			},
			Ask: func(context.Context, PrivilegeMethod, bool) (PrivilegeAnswer, error) {
				t.Error("known credentials unexpectedly opened a prompt")
				return PrivilegeAnswer{}, ErrPrivilegeCancelled
			},
		})
		for range 2 {
			if _, _, err := priv.Run(t.Context(), "true", PrivilegeOptions{}); err != nil {
				t.Fatal(err)
			}
		}
		want := "fixture-login fixture-login"
		if saved {
			want = "fixture-login fixture-stored fixture-stored"
		}
		if strings.Join(answers, " ") != want {
			t.Error("credentials tried out of order")
		}
	}
}

func TestPrivilegeSudoersDenialUsesSu(t *testing.T) {
	for _, denial := range []string{"fixture is not in the sudoers file.", "Sorry, user fixture may not run sudo on host."} {
		var asked bool
		priv := NewPrivilege(t.Context(), func(_ context.Context, request PrivilegeRequest) (PrivilegeResult, error) {
			if request.Probe {
				return PrivilegeResult{Stdout: "sudo"}, nil
			}
			if !request.PTY {
				return PrivilegeResult{Stderr: denial}, errors.New("exit 1")
			}
			return PrivilegeResult{Started: true, Authenticated: true}, nil
		}, PrivilegeCredentials{
			Login: func(context.Context) []byte { return []byte("fixture-login") },
			Ask: func(_ context.Context, method PrivilegeMethod, wrong bool) (PrivilegeAnswer, error) {
				asked = true
				if method != PrivilegeSu || wrong {
					t.Error("sudoers denial treated as a bad password")
				}
				return PrivilegeAnswer{Method: method, Password: []byte("fixture-root")}, nil
			},
		})
		if _, _, err := priv.Run(t.Context(), "true", PrivilegeOptions{}); err != nil || !asked {
			t.Fatalf("su fallback did not run: %v", err)
		}
	}
}

func TestPrivilegeRedactsRunnerErrors(t *testing.T) {
	const fixture = "fixture-only"
	priv := NewPrivilege(t.Context(), func(_ context.Context, request PrivilegeRequest) (PrivilegeResult, error) {
		if request.Probe {
			return PrivilegeResult{Stdout: "sudo"}, nil
		}
		if strings.Contains(request.Command+request.Action, fixture) {
			t.Error("credential was added to argv or script")
		}
		return PrivilegeResult{Started: true, Authenticated: true, Stdout: "out " + fixture, Stderr: "err " + fixture}, errors.New("exit " + fixture)
	}, PrivilegeCredentials{Login: func(context.Context) []byte { return []byte(fixture) }})
	out, stderr, err := priv.Run(t.Context(), "true", PrivilegeOptions{})
	if out != "out [redacted]" || stderr != "err [redacted]" || err == nil || err.Error() != "exit [redacted]" {
		t.Error("the credential was not redacted from returned strings")
	}
}

func TestPrivilegePTYAndRedaction(t *testing.T) {
	const secret = "fixture-only"
	input, captured := io.Pipe()
	defer input.Close()
	defer captured.Close()
	var progress strings.Builder
	request := PrivilegeRequest{PTY: true, Password: []byte(secret), prompt: "Password:", ready: "ready-token", Stdin: strings.NewReader("arg\x00\n"), frameInput: true, progress: func(s string) { progress.WriteString(s) }}
	stream := newPrivilegeStream(request)
	go func() {
		stream.write(true, []byte("Pass"))
		stream.write(true, []byte("word: "))
		line := make([]byte, len(secret)+1)
		io.ReadFull(input, line)
		if string(line) != secret+"\n" {
			t.Error("password was not sent on the PTY input")
		}
		stream.write(true, append(line, []byte("ready-")...))
		stream.write(true, []byte("token\nfirst fixture-"))
		stream.write(true, []byte("only last"))
		data, _ := io.ReadAll(input)
		if string(data) != `\0141\0162\0147\0000\0012`+"\n.\n" {
			t.Error("incorrect PTY input frames")
		}
	}()
	finished := make(chan error, 1)
	go func() { finished <- stream.send(t.Context(), captured, []byte(secret), time.Second) }()
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	result := stream.result()
	if !result.Started || result.Stdout != "first [redacted] last" || result.Stdout != progress.String() || strings.Contains(result.Stderr, secret) {
		t.Error("missing or unredacted PTY output")
	}
}

func TestPrivilegeSudoPromptIsNotCommandStderr(t *testing.T) {
	for _, beforeReady := range []bool{true, false} {
		request := PrivilegeRequest{method: PrivilegeSudo, prompt: "prompt-test:", ready: "ready-test"}
		stream := newPrivilegeStream(request)
		if beforeReady {
			stream.write(false, []byte(request.prompt))
		}
		stream.write(true, []byte(request.ready+"\n"))
		if !beforeReady {
			stream.write(false, []byte("prompt-"))
			stream.write(false, []byte("test: "))
		}
		stream.write(false, []byte("no crontab for root\n"))
		if stderr := stream.result().Stderr; stderr != "no crontab for root\n" || !CronMissing(stderr) {
			t.Errorf("beforeReady=%v: command stderr = %q", beforeReady, stderr)
		}
	}
}

func TestPrivilegePTYPromptTimeout(t *testing.T) {
	input, output := io.Pipe()
	defer input.Close()
	stream := newPrivilegeStream(PrivilegeRequest{
		method: PrivilegeSu, PTY: true, Password: []byte("fixture-only"), prompt: "Password:", ready: "ready-token",
	})
	stream.write(true, []byte("Last login notice:\nPassword: is not a prompt\n"))
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	context.AfterFunc(ctx, func() { input.Close() })
	if err := stream.send(ctx, output, []byte("fixture-only"), 10*time.Millisecond); !errors.Is(err, ErrPrivilegeDenied) {
		t.Fatalf("missing prompt did not time out: %v", err)
	}
	if result := stream.result(); result.Started || result.Stdout != "" || result.Stderr != "" {
		t.Fatal("pre-authentication output was exposed")
	}
}
