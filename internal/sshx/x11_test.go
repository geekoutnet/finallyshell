package sshx

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"io"
	"net"
	"strconv"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func TestManagedX11Cookie(t *testing.T) {
	fake := bytes.Repeat([]byte{1}, 16)
	real := bytes.Repeat([]byte{2}, 16)
	for _, order := range []binary.ByteOrder{binary.LittleEndian, binary.BigEndian} {
		input := x11Setup(order, x11Protocol, fake)
		for _, tc := range []struct {
			name string
			want []byte
		}{
			{"cookie", real}, {"no-auth", nil},
		} {
			t.Run(tc.name, func(t *testing.T) {
				listener, err := net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				defer listener.Close()
				conn, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Close()
				name := x11Protocol
				if len(tc.want) == 0 {
					name = ""
				}
				want := x11Setup(order, name, tc.want)
				received := make(chan []byte, 1)
				go func() {
					local, err := listener.Accept()
					if err != nil {
						received <- nil
						return
					}
					defer local.Close()
					local.SetReadDeadline(time.Now().Add(time.Second))
					data := make([]byte, len(want))
					_, err = io.ReadFull(local, data)
					if err != nil {
						data = nil
					}
					received <- data
				}()
				if err := x11Replace(bytes.NewReader(input), fake, tc.want, conn); err != nil {
					t.Fatal(err)
				}
				if got := <-received; !bytes.Equal(got, want) {
					t.Fatal("setup packet not rewritten")
				}
			})
		}
		var refused bytes.Buffer
		if err := x11Replace(bytes.NewReader(input), real, fake, &refused); err == nil || refused.Len() != 0 {
			t.Fatal("accepted spoofed cookie")
		}
	}
}

func TestManagedX11Channel(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	if port < 6000 {
		t.Skip("no X11 display number for loopback port")
	}
	display := "127.0.0.1:" + strconv.Itoa(port-6000) + ".2"
	t.Setenv("XAUTHORITY", t.TempDir()+"/not-present")
	local := make(chan []byte, 1)
	go func() {
		for attempt := range 2 {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			conn.SetDeadline(time.Now().Add(time.Second))
			setup := make([]byte, 12)
			_, err = io.ReadFull(conn, setup)
			if attempt == 0 && err == nil {
				conn.Write([]byte{1, 0, 0, 11, 0, 0, 0, 0})
			}
			if attempt == 1 && err == nil {
				local <- setup
			}
			conn.Close()
		}
	}()
	type request struct {
		Single   bool
		Protocol string
		Cookie   string
		Screen   uint32
	}
	requests := make(chan request, 1)
	serverConn := make(chan *ssh.ServerConn, 1)
	address := managedSSHServer(t, &ssh.ServerConfig{NoClientAuth: true}, func(server *ssh.ServerConn, channels <-chan ssh.NewChannel) {
		serverConn <- server
		channel, reqs, err := (<-channels).Accept()
		if err != nil {
			return
		}
		defer channel.Close()
		for incoming := range reqs {
			if incoming.Type == "x11-req" {
				var req request
				if ssh.Unmarshal(incoming.Payload, &req) == nil {
					requests <- req
				}
			}
			incoming.Reply(true, nil)
		}
	})
	client, err := ssh.Dial("tcp", address, &ssh.ClientConfig{User: "fixture", HostKeyCallback: ssh.InsecureIgnoreHostKey(), Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	warning := make(chan error, 1)
	shell, err := OpenShell(client, PTY{X11: "trusted", XDisplay: display, ForwardWarning: func(_ string, err error) { warning <- err }})
	if err != nil {
		t.Fatal(err)
	}
	defer shell.Close()
	var req request
	select {
	case req = <-requests:
	case err := <-warning:
		t.Fatal(err)
	case <-time.After(time.Second):
		t.Fatal("missing x11 request")
	}
	cookie, err := hex.DecodeString(req.Cookie)
	if err != nil || len(cookie) != 16 || req.Screen != 2 || req.Protocol != x11Protocol || req.Single {
		t.Fatal("invalid x11 request")
	}
	server := <-serverConn
	channel, incoming, err := server.OpenChannel("x11", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer channel.Close()
	go ssh.DiscardRequests(incoming)
	if _, err = channel.Write(x11Setup(binary.BigEndian, x11Protocol, cookie)); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-local:
		if !bytes.Equal(got, x11Setup(binary.BigEndian, "", nil)) {
			t.Fatal("forwarded the spoofed cookie to the local display")
		}
	case <-time.After(time.Second):
		t.Fatal("local display did not receive X11 setup")
	}
}

func TestManagedX11Display(t *testing.T) {
	for _, display := range []string{"localhost:99999", "remote.example:0", "unix:-1", ":0.bad", ""} {
		if _, _, _, err := parseXDisplay(display); err == nil {
			t.Errorf("accepted %q", display)
		}
	}
	network, addr, screen, err := parseXDisplay("127.0.0.1:12.3")
	if err != nil || network != "tcp" || addr != "127.0.0.1:6012" || screen != 3 {
		t.Fatalf("unexpected display: %q %q %d %v", network, addr, screen, err)
	}
}
