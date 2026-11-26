package sshx

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"time"

	"finallyshell/internal/config"
)

// dialSOCKS4 asks the SOCKS4 proxy p to connect to addr, with p's user as
// the user ID. An IPv4 address goes as SOCKS4 has it; a name goes as
// SOCKS4a, for the proxy to resolve. SOCKS4 has no password and no IPv6.
func dialSOCKS4(p *config.ProxyConfig, addr string, timeout time.Duration) (net.Conn, error) {
	paddr := ProxyAddr(p)
	host, portText, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, &HopError{Proxy: paddr, Err: err}
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port <= 0 || port > 65535 {
		return nil, &HopError{Proxy: paddr, Err: fmt.Errorf("invalid port %q", portText)}
	}
	ip := net.ParseIP(host)
	if ip != nil && ip.To4() == nil {
		return nil, &HopError{Proxy: paddr, Err: errors.New("SOCKS4 does not reach IPv6 addresses")}
	}
	raw, err := net.DialTimeout("tcp", paddr, timeout)
	if err != nil {
		return nil, &HopError{Proxy: paddr, Err: err}
	}
	fail := func(err error) (net.Conn, error) {
		raw.Close()
		if errors.Is(err, os.ErrDeadlineExceeded) {
			err = os.ErrDeadlineExceeded
		}
		return nil, &HopError{Proxy: paddr, Err: err}
	}
	raw.SetDeadline(time.Now().Add(timeout))
	req := []byte{4, 1, 0, 0, 0, 0, 0, 1}
	binary.BigEndian.PutUint16(req[2:4], uint16(port))
	if ip != nil {
		copy(req[4:8], ip.To4())
	}
	req = append(append(req, p.User...), 0)
	if ip == nil {
		// SOCKS4a: 0.0.0.1 above, the name after the user ID.
		req = append(append(req, host...), 0)
	}
	if _, err := raw.Write(req); err != nil {
		return fail(err)
	}
	var reply [8]byte
	if _, err := io.ReadFull(raw, reply[:]); err != nil {
		return fail(err)
	}
	switch reply[1] {
	case 90: // granted
	case 92, 93:
		return fail(ErrProxyAuth) // identd could not confirm the user ID
	default:
		return fail(&ProxyTargetError{Addr: addr, Reason: fmt.Sprintf("SOCKS4 refused (%d)", reply[1])})
	}
	raw.SetDeadline(time.Time{})
	return newHopConn(raw, nil, addr, nil), nil
}
