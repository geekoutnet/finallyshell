package monitor

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"io"
	"net"
	"regexp"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func privilegeTestClient(t *testing.T, serve func(ssh.Channel, <-chan *ssh.Request)) *ssh.Client {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatal(err)
	}
	server := &ssh.ServerConfig{NoClientAuth: true}
	server.AddHostKey(signer)
	go func() {
		socket, err := listener.Accept()
		if err != nil {
			return
		}
		defer socket.Close()
		_, channels, requests, err := ssh.NewServerConn(socket, server)
		if err != nil {
			return
		}
		go ssh.DiscardRequests(requests)
		for next := range channels {
			channel, requests, err := next.Accept()
			if err == nil {
				go func() {
					defer channel.Close()
					serve(channel, requests)
				}()
			}
		}
	}()
	client, err := ssh.Dial("tcp", listener.Addr().String(), &ssh.ClientConfig{
		User: "fixture", HostKeyCallback: ssh.FixedHostKey(signer.PublicKey()), Timeout: 3 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	return client
}

func TestPrivilegeSSHStdin(t *testing.T) {
	const password = "fixture-only"
	for _, method := range []string{"root", "sudo-n", "sudo", "su", "sudo-retry", "su-reject", "su-unavailable"} {
		t.Run(method, func(t *testing.T) {
			client := privilegeTestClient(t, func(channel ssh.Channel, requests <-chan *ssh.Request) {
				pty := false
				for request := range requests {
					switch request.Type {
					case "pty-req":
						pty = true
						request.Reply(true, nil)
					case "exec":
						var payload struct{ Command string }
						if err := ssh.Unmarshal(request.Payload, &payload); err != nil {
							t.Error(err)
							return
						}
						request.Reply(true, nil)
						if payload.Command == "sh -s" {
							_, _ = io.Copy(io.Discard, channel)
							probe := method
							switch method {
							case "sudo-retry":
								probe = "sudo"
							case "su-reject", "su-unavailable":
								probe = "su"
							}
							io.WriteString(channel, probe+"\n")
							privilegeTestExit(channel, 0)
							return
						}
						wantPTY := method == "su" || strings.HasPrefix(method, "su-")
						if strings.Contains(payload.Command, password) || pty != wantPTY {
							t.Error("password in exec request or unexpected PTY")
						}
						privilegeTestAction(t, channel, payload.Command, method, password)
						return
					default:
						request.Reply(false, nil)
					}
				}
			})
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			var prompts int
			var progress strings.Builder
			priv := NewPrivilege(ctx, SSHPrivilegeRunner(client), PrivilegeCredentials{Ask: func(_ context.Context, mode PrivilegeMethod, wrong bool) (PrivilegeAnswer, error) {
				prompts++
				if prompts == 2 {
					if !wrong {
						t.Error("authentication failure not reported as wrong password")
					}
					return PrivilegeAnswer{}, ErrPrivilegeCancelled
				}
				return PrivilegeAnswer{Method: mode, Password: []byte(password)}, nil
			}})
			out, stderr, err := priv.Run(ctx, "test-command 'quoted'", PrivilegeOptions{Stdin: strings.NewReader("payload\x00\n"), Progress: func(s string) { progress.WriteString(s) }})
			switch method {
			case "sudo-retry", "su-reject":
				if !errors.Is(err, ErrPrivilegeCancelled) || prompts != 2 {
					t.Fatalf("wrong password handling: %v, %d prompts", err, prompts)
				}
			case "su-unavailable":
				if !errors.Is(err, ErrPrivilegeDenied) || prompts != 1 {
					t.Fatalf("su denial: %v, %d prompts", err, prompts)
				}
			default:
				if err != nil || out != "started" || out != progress.String() || stderr != "" {
					t.Fatalf("command output or exit status unexpected: %v", err)
				}
			}
		})
	}
}

func privilegeTestAction(t *testing.T, channel ssh.Channel, command, method, password string) {
	t.Helper()
	input := bufio.NewReader(channel)
	prompt := regexp.MustCompile(`password-[A-Z2-7]+:`).FindString(command)
	pty := strings.HasPrefix(command, "env LC_ALL=C su -s ")
	if method == "su-unavailable" {
		io.WriteString(channel.Stderr(), "env: 'su': No such file or directory\n")
		privilegeTestExit(channel, 127)
		return
	}
	if prompt != "" || pty {
		stream := channel.Stderr()
		if pty {
			prompt, stream = "Password:", channel
		}
		io.WriteString(stream, prompt[:4])
		io.WriteString(stream, prompt[4:]+" ")
		line, err := input.ReadString('\n')
		if err != nil || line != password+"\n" {
			t.Error("password line not received")
			return
		}
		switch method {
		case "sudo-retry":
			io.WriteString(stream, "Sorry, try again.\n"+prompt+" ")
		case "su-reject":
			io.WriteString(stream, "\r\nsu: Authentication failure\r\n")
		}
		if method == "sudo-retry" || method == "su-reject" {
			if data, _ := io.ReadAll(input); len(data) != 0 {
				t.Error("sent more input after rejection")
			}
			privilegeTestExit(channel, 1)
			return
		}
		if pty {
			io.WriteString(stream, line)
		}
	}
	ready := regexp.MustCompile(`ready-[A-Z2-7]+`).FindString(command)
	if ready == "" || !strings.Contains(command, "test-command") {
		t.Error("missing script or root marker")
		return
	}
	io.WriteString(channel, ready+"\r\nstarted")
	data, _ := io.ReadAll(input)
	want := "payload\x00\n"
	if pty {
		want = `\0160\0141\0171\0154\0157\0141\0144\0000\0012` + "\n.\n"
	}
	if string(data) != want {
		t.Error("wrong command stdin")
	}
	privilegeTestExit(channel, 0)
}

func privilegeTestExit(channel ssh.Channel, status uint32) {
	channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{status}))
}
