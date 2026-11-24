package sshx

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

const x11Protocol = "MIT-MAGIC-COOKIE-1"

var (
	ErrX11Display   = errors.New("invalid or unavailable local X display")
	ErrX11Untrusted = errors.New("local X server does not support untrusted authorization")
	ErrX11Request   = errors.New("server refused X11 forwarding")
	errX11Setup     = errors.New("invalid X11 setup packet")
)

type x11Display struct {
	network, address string
	screen           uint32
	cookie           []byte
	expires          time.Time
}

type x11Grant struct {
	x11Display
	life context.Context
}

const x11UntrustedSeconds = 1200

func parseXDisplay(display string) (string, string, uint32, error) {
	loopback := "127.0.0.1"
	if rest, ok := strings.CutPrefix(display, "[::1]:"); ok {
		display, loopback = "localhost:"+rest, "::1"
	}
	host, number, ok := strings.Cut(display, ":")
	if !ok {
		return "", "", 0, ErrX11Display
	}
	if strings.HasPrefix(host, "/") && runtime.GOOS == "darwin" && filepath.IsAbs(host) {
		number = strings.TrimPrefix(number, ":")
	} else if host != "" && host != "unix" && host != "localhost" && host != "127.0.0.1" {
		return "", "", 0, ErrX11Display
	}
	index, screen, hasScreen := strings.Cut(number, ".")
	port, err := strconv.ParseUint(index, 10, 16)
	if err != nil || port > 59535 {
		return "", "", 0, ErrX11Display
	}
	var sub uint64
	if hasScreen {
		sub, err = strconv.ParseUint(screen, 10, 32)
		if err != nil {
			return "", "", 0, ErrX11Display
		}
	}
	switch {
	case filepath.IsAbs(host):
		return "unix", host + ":" + index, uint32(sub), nil
	case runtime.GOOS != "windows" && (host == "" || host == "unix"):
		return "unix", "/tmp/.X11-unix/X" + strconv.FormatUint(port, 10), uint32(sub), nil
	case host == "unix":
		return "", "", 0, ErrX11Display
	default:
		return "tcp", net.JoinHostPort(loopback, strconv.FormatUint(6000+port, 10)), uint32(sub), nil
	}
}

func localXDisplay(setting string, trusted bool) (x11Display, error) {
	name := strings.TrimSpace(setting)
	if name == "" {
		name = os.Getenv("DISPLAY")
		if name == "" && runtime.GOOS == "windows" {
			name = "localhost:0"
		}
	}
	network, addr, screen, err := parseXDisplay(name)
	if err != nil {
		return x11Display{}, err
	}
	local := x11Display{network: network, address: addr, screen: screen}
	if executable, err := exec.LookPath("xauth"); err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		out, _ := exec.CommandContext(ctx, executable, "list", name).Output()
		cancel()
		for line := range strings.SplitSeq(string(out), "\n") {
			params := strings.Fields(line)
			if len(params) == 3 && params[1] == x11Protocol {
				if cookie, err := hex.DecodeString(params[2]); err == nil && len(cookie) == 16 {
					local.cookie = cookie
					break
				}
			}
		}
		clear(out)
	}
	conn, err := local.dial()
	if err != nil {
		return x11Display{}, ErrX11Display
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	if err := x11Handshake(conn, local.cookie); err != nil {
		return x11Display{}, ErrX11Display
	}
	if !trusted {
		cookie, err := x11UntrustedCookie(conn)
		if err != nil {
			return x11Display{}, ErrX11Untrusted
		}
		clear(local.cookie)
		local.cookie = cookie
		local.expires = time.Now().Add(x11UntrustedSeconds * time.Second)
	}
	return local, nil
}

func (d x11Display) dial() (net.Conn, error) {
	return net.DialTimeout(d.network, d.address, 2*time.Second)
}

func x11Pad(size int) int { return (size + 3) &^ 3 }

func x11Setup(order binary.ByteOrder, name string, cookie []byte) []byte {
	packet := make([]byte, 12+x11Pad(len(name))+x11Pad(len(cookie)))
	packet[0] = 'B'
	if order == binary.LittleEndian {
		packet[0] = 'l'
	}
	order.PutUint16(packet[2:], 11)
	order.PutUint16(packet[6:], uint16(len(name)))
	order.PutUint16(packet[8:], uint16(len(cookie)))
	copy(packet[12:], name)
	copy(packet[12+x11Pad(len(name)):], cookie)
	return packet
}

func x11Handshake(conn io.ReadWriter, cookie []byte) error {
	name := x11Protocol
	if len(cookie) == 0 {
		name = ""
	}
	packet := x11Setup(binary.BigEndian, name, cookie)
	defer clear(packet)
	if _, err := conn.Write(packet); err != nil {
		return err
	}
	var response [8]byte
	if _, err := io.ReadFull(conn, response[:]); err != nil || response[0] != 1 {
		return errX11Setup
	}
	_, err := io.CopyN(io.Discard, conn, int64(binary.BigEndian.Uint16(response[6:]))*4)
	return err
}

func x11UntrustedCookie(conn io.ReadWriter) ([]byte, error) {
	const ext = "SECURITY"
	order := binary.BigEndian
	lookup := make([]byte, 8+x11Pad(len(ext)))
	lookup[0] = 98
	order.PutUint16(lookup[2:], uint16(len(lookup)/4))
	order.PutUint16(lookup[4:], uint16(len(ext)))
	copy(lookup[8:], ext)
	var response [32]byte
	if _, err := conn.Write(lookup); err != nil {
		return nil, err
	}
	if _, err := io.ReadFull(conn, response[:]); err != nil || response[0] != 1 || response[8] != 1 {
		return nil, ErrX11Untrusted
	}
	opcode := response[9]
	if _, err := conn.Write([]byte{opcode, 0, 0, 2, 0, 1, 0, 0}); err != nil {
		return nil, err
	}
	if _, err := io.ReadFull(conn, response[:]); err != nil || response[0] != 1 || order.Uint16(response[8:]) != 1 {
		return nil, ErrX11Untrusted
	}
	request := make([]byte, 12+x11Pad(len(x11Protocol))+8)
	request[0], request[1] = opcode, 1
	order.PutUint16(request[2:], uint16(len(request)/4))
	order.PutUint16(request[4:], uint16(len(x11Protocol)))
	order.PutUint32(request[8:], 3)
	copy(request[12:], x11Protocol)
	order.PutUint32(request[len(request)-8:], x11UntrustedSeconds)
	order.PutUint32(request[len(request)-4:], 1)
	if _, err := conn.Write(request); err != nil {
		return nil, err
	}
	if _, err := io.ReadFull(conn, response[:]); err != nil || response[0] != 1 || order.Uint32(response[4:]) != 4 || order.Uint16(response[12:]) != 16 {
		return nil, ErrX11Untrusted
	}
	cookie := make([]byte, 16)
	_, err := io.ReadFull(conn, cookie)
	return cookie, err
}

func x11ReadSetup(src io.Reader) (binary.ByteOrder, [16]byte, error) {
	var header [12]byte
	var cookie [16]byte
	if _, err := io.ReadFull(src, header[:]); err != nil {
		return nil, cookie, err
	}
	var order binary.ByteOrder
	switch header[0] {
	case 'B':
		order = binary.BigEndian
	case 'l':
		order = binary.LittleEndian
	default:
		return nil, cookie, errX11Setup
	}
	if order.Uint16(header[2:]) != 11 || order.Uint16(header[4:]) != 0 || int(order.Uint16(header[6:])) != len(x11Protocol) || order.Uint16(header[8:]) != 16 {
		return nil, cookie, errX11Setup
	}
	protocol := make([]byte, x11Pad(len(x11Protocol)))
	if _, err := io.ReadFull(src, protocol); err != nil || !bytes.Equal(protocol[:len(x11Protocol)], []byte(x11Protocol)) {
		return nil, cookie, errX11Setup
	}
	_, err := io.ReadFull(src, cookie[:])
	return order, cookie, err
}

func x11Replace(src io.Reader, fake, real []byte, dst io.Writer) error {
	order, cookie, err := x11ReadSetup(src)
	if err != nil || subtle.ConstantTimeCompare(cookie[:], fake) != 1 {
		return errX11Setup
	}
	return x11WriteSetup(dst, order, real)
}

func x11WriteSetup(dst io.Writer, order binary.ByteOrder, cookie []byte) error {
	name := x11Protocol
	if len(cookie) == 0 {
		name = ""
	}
	packet := x11Setup(order, name, cookie)
	defer clear(packet)
	_, err := dst.Write(packet)
	return err
}

type x11Forward struct {
	mu       sync.Mutex
	displays map[[16]byte]x11Grant
}

var x11Forwards struct {
	sync.Mutex
	clients map[*ssh.Client]*x11Forward
}

func requestX11Forward(client *ssh.Client, session *ssh.Session, setting string, trusted bool, warn func(error)) (func(), error) {
	local, err := localXDisplay(setting, trusted)
	if err != nil {
		return nil, err
	}
	var fake [16]byte
	if _, err := rand.Read(fake[:]); err != nil {
		return nil, err
	}
	forward, err := x11Forwarder(client, warn)
	if err != nil {
		return nil, err
	}
	life, cancel := context.WithCancel(context.Background())
	forward.mu.Lock()
	forward.displays[fake] = x11Grant{x11Display: local, life: life}
	forward.mu.Unlock()
	remove := func() {
		cancel()
		forward.mu.Lock()
		delete(forward.displays, fake)
		forward.mu.Unlock()
	}
	request := struct {
		Single   bool
		Protocol string
		Cookie   string
		Screen   uint32
	}{false, x11Protocol, hex.EncodeToString(fake[:]), local.screen}
	ok, err := session.SendRequest("x11-req", true, ssh.Marshal(request))
	if err != nil || !ok {
		remove()
		return nil, ErrX11Request
	}
	return remove, nil
}

func x11Forwarder(client *ssh.Client, warn func(error)) (*x11Forward, error) {
	x11Forwards.Lock()
	defer x11Forwards.Unlock()
	if forward := x11Forwards.clients[client]; forward != nil {
		return forward, nil
	}
	channels := client.HandleChannelOpen("x11")
	if channels == nil {
		return nil, ErrX11Request
	}
	forward := &x11Forward{displays: make(map[[16]byte]x11Grant)}
	if x11Forwards.clients == nil {
		x11Forwards.clients = make(map[*ssh.Client]*x11Forward)
	}
	x11Forwards.clients[client] = forward
	go func() {
		defer func() {
			x11Forwards.Lock()
			delete(x11Forwards.clients, client)
			x11Forwards.Unlock()
		}()
		pending := make(chan struct{}, 32)
		for incoming := range channels {
			select {
			case pending <- struct{}{}:
				go forward.serve(incoming, pending, warn)
			default:
				incoming.Reject(ssh.ResourceShortage, "too many X11 channels")
			}
		}
	}()
	return forward, nil
}

func (f *x11Forward) serve(incoming ssh.NewChannel, pending chan struct{}, warn func(error)) {
	defer func() { <-pending }()
	channel, requests, err := incoming.Accept()
	if err != nil {
		return
	}
	defer channel.Close()
	go ssh.DiscardRequests(requests)
	timer := time.AfterFunc(5*time.Second, func() { channel.Close() })
	order, cookie, err := x11ReadSetup(channel)
	if !timer.Stop() || err != nil {
		return
	}
	f.mu.Lock()
	local, ok := f.displays[cookie]
	f.mu.Unlock()
	if !ok {
		return
	}
	if !local.expires.IsZero() && !time.Now().Before(local.expires) {
		warn(ErrX11Untrusted)
		return
	}
	conn, err := local.dial()
	if err != nil {
		warn(ErrX11Display)
		return
	}
	defer conn.Close()
	stop := context.AfterFunc(local.life, func() {
		conn.Close()
		channel.Close()
	})
	defer stop()
	conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	if err := x11WriteSetup(conn, order, local.cookie); err != nil {
		warn(ErrX11Display)
		return
	}
	conn.SetWriteDeadline(time.Time{})
	done := make(chan struct{})
	go func() {
		io.Copy(conn, channel)
		if half, ok := conn.(interface{ CloseWrite() error }); ok {
			half.CloseWrite()
		} else {
			conn.Close()
		}
		close(done)
	}()
	io.Copy(channel, conn)
	channel.Close()
	conn.Close()
	<-done
}
