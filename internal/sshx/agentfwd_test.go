package sshx

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net"
	"path/filepath"
	"testing"
	"time"

	"finallyshell/internal/config"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

func managedSSHServer(t *testing.T, cfg *ssh.ServerConfig, serve func(*ssh.ServerConn, <-chan ssh.NewChannel)) string {
	t.Helper()
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(private)
	if err != nil {
		t.Fatal(err)
	}
	cfg.AddHostKey(signer)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	go func() {
		raw, err := listener.Accept()
		if err != nil {
			return
		}
		defer raw.Close()
		raw.SetDeadline(time.Now().Add(8 * time.Second))
		server, channels, requests, err := ssh.NewServerConn(raw, cfg)
		if err == nil {
			defer server.Close()
			go ssh.DiscardRequests(requests)
			serve(server, channels)
		}
	}()
	return listener.Addr().String()
}

func TestManagedAgentForward(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "agent.sock")
	local, err := net.Listen("unix", socket)
	if err != nil {
		t.Skipf("local Unix sockets unavailable: %v", err)
	}
	defer local.Close()
	ring := agent.NewKeyring()
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := ring.Add(agent.AddedKey{PrivateKey: private}); err != nil {
		t.Fatal(err)
	}
	go func() {
		conn, err := local.Accept()
		if err == nil {
			defer conn.Close()
			agent.ServeAgent(ring, conn)
		}
	}()
	requests := make(chan string, 10)
	serverConn := make(chan *ssh.ServerConn, 1)
	address := managedSSHServer(t, &ssh.ServerConfig{NoClientAuth: true}, func(server *ssh.ServerConn, channels <-chan ssh.NewChannel) {
		serverConn <- server
		for incoming := range channels {
			channel, reqs, err := incoming.Accept()
			if err != nil {
				continue
			}
			go func() {
				defer channel.Close()
				for request := range reqs {
					if request.Type == "auth-agent-req@openssh.com" {
						requests <- request.Type
					}
					request.Reply(true, nil)
				}
			}()
		}
	})
	client, err := ssh.Dial("tcp", address, &ssh.ClientConfig{User: "fixture", HostKeyCallback: ssh.InsecureIgnoreHostKey(), Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	server := <-serverConn
	for range 2 {
		shell, err := OpenShell(client, PTY{ForwardAgent: true, AgentAddress: socket, ForwardWarning: func(_ string, err error) { t.Error(err) }})
		if err != nil {
			t.Fatal(err)
		}
		select {
		case <-requests:
		case <-time.After(time.Second):
			t.Fatal("missing forwarding request")
		}
		channel, reqs, err := server.OpenChannel("auth-agent@openssh.com", nil)
		if err != nil {
			t.Fatal(err)
		}
		go ssh.DiscardRequests(reqs)
		forward := agent.NewClient(channel)
		keys, err := forward.List()
		if err != nil || len(keys) != 1 {
			t.Fatalf("list: %v, %v", keys, err)
		}
		if _, err = forward.Sign(keys[0], []byte("fixture")); err != nil {
			t.Fatal(err)
		}
		if err := forward.RemoveAll(); err == nil {
			t.Fatal("remote can remove local agent keys")
		}
		shell.Close()
		if _, err := forward.List(); err == nil {
			t.Fatal("closed shell still forwards the agent")
		}
		channel.Close()
	}
}

func TestManagedKeyVerifyRequiresSignature(t *testing.T) {
	private, public, err := GenerateKey("ed25519", "fixture", "")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "id_fixture")
	if err := WriteKeyPair(path, private, public, false, time.Now()); err != nil {
		t.Fatal(err)
	}
	key, _, _, _, err := ssh.ParseAuthorizedKey(public)
	if err != nil {
		t.Fatal(err)
	}
	for _, noAuth := range []bool{true, false} {
		configSSH := &ssh.ServerConfig{NoClientAuth: noAuth}
		configSSH.PublicKeyCallback = func(_ ssh.ConnMetadata, offered ssh.PublicKey) (*ssh.Permissions, error) {
			if ssh.FingerprintSHA256(offered) != ssh.FingerprintSHA256(key) {
				return nil, errors.New("wrong key")
			}
			return nil, nil
		}
		address := managedSSHServer(t, configSSH, func(server *ssh.ServerConn, _ <-chan ssh.NewChannel) { server.Wait() })
		host, port, _ := net.SplitHostPort(address)
		portNumber, _ := net.LookupPort("tcp", port)
		client, err := Dial(&config.Host{Addr: host, Port: portNumber, User: "fixture", Auth: config.AuthKey, KeyPath: path}, Options{
			KnownHosts: filepath.Join(t.TempDir(), "known_hosts"), VerifyKeyFingerprint: ssh.FingerprintSHA256(key), AskHostKey: func(q *HostKeyQuestion) { q.Answer <- true },
		})
		if client != nil {
			client.Close()
		}
		if noAuth && !errors.Is(err, ErrNoAuth) || !noAuth && err != nil {
			t.Fatalf("noAuth=%t: %v", noAuth, err)
		}
	}
}
