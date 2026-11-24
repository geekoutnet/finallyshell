package telnet

import (
	"bytes"
	"io"
	"net"
	"testing"
	"time"
)

func TestTelnetNegotiation(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()
	server.SetDeadline(time.Now().Add(3 * time.Second))
	c, err := New(client, Options{Terminal: "vt100"})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	go io.Copy(io.Discard, c)
	if err := c.Resize(255, 24); err != nil {
		t.Fatal(err)
	}
	exchange := func(send, want []byte) {
		t.Helper()
		if _, err := server.Write(send); err != nil {
			t.Fatal(err)
		}
		got := make([]byte, len(want))
		if _, err := io.ReadFull(server, got); err != nil || !bytes.Equal(got, want) {
			t.Fatalf("response = %v, %v; want %v", got, err, want)
		}
	}
	exchange([]byte{255, 251, 1}, []byte{255, 253, 1})
	exchange([]byte{255, 251, 3}, []byte{255, 253, 3})
	exchange([]byte{255, 251, 42}, []byte{255, 254, 42})
	exchange([]byte{255, 253, 31}, []byte{255, 251, 31, 255, 250, 31, 0, 255, 255, 0, 24, 255, 240})
	resize := make(chan error, 1)
	go func() { resize <- c.Resize(80, 48) }()
	want := []byte{255, 250, 31, 0, 80, 0, 48, 255, 240}
	got := make([]byte, len(want))
	if _, err := io.ReadFull(server, got); err != nil || !bytes.Equal(got, want) {
		t.Fatalf("resize = %v, %v", got, err)
	}
	if err := <-resize; err != nil {
		t.Fatal(err)
	}
	exchange([]byte{255, 253, 24}, []byte{255, 251, 24})
	exchange([]byte{255, 250, 24, 1, 255, 240}, []byte("\xff\xfa\x18\x00vt100\xff\xf0"))
}

func TestTelnetKeepAlive(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()
	server.SetReadDeadline(time.Now().Add(time.Second))
	conn, err := New(client, Options{KeepAlive: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	var reply [2]byte
	if _, err := io.ReadFull(server, reply[:]); err != nil || reply != [2]byte{255, 241} {
		t.Fatalf("keepalive = %v, %v", reply, err)
	}
}

func TestTelnetLoginAndNVT(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()
	server.SetDeadline(time.Now().Add(3 * time.Second))
	password := "fake-" + "credential"
	c, err := New(client, Options{Username: "operator", Password: password})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	finished := make(chan []byte, 1)
	go func() { out, _ := io.ReadAll(c); finished <- out }()
	for _, part := range []struct{ send, want string }{
		{"Banner\r\x00\xff\xff\r\nLo", ""}, {"gin: ", "operator\r\n"},
		{"Pass", ""}, {"word: ", password + "\r\n"},
		{password[:6], ""}, {password[6:] + "\r\nReady", ""},
	} {
		if _, err := io.WriteString(server, part.send); err != nil {
			t.Fatal(err)
		}
		if part.want != "" {
			got := make([]byte, len(part.want))
			if _, err := io.ReadFull(server, got); err != nil || string(got) != part.want {
				t.Fatal("login reply did not match")
			}
		}
	}
	go c.Write([]byte("a\xffb\rc\nd\r\n"))
	want := "a\xff\xffb\r\x00c\r\nd\r\n"
	got := make([]byte, len(want))
	if _, err := io.ReadFull(server, got); err != nil || string(got) != want {
		t.Fatalf("NVT write = %q, %v", got, err)
	}
	server.Close()
	if got := <-finished; string(got) != "Banner\r\xff\r\nLogin: Password: \r\nReady" {
		t.Fatal("terminal output did not match or contained an echoed credential")
	}
}
