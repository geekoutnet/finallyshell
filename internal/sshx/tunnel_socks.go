package sshx

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"slices"
	"strconv"
)

const (
	socksVersion   = 5
	socksNoAuth    = 0
	socksUserPass  = 2
	socksNoMethod  = 0xff
	socksConnect   = 1
	socksIPv4      = 1
	socksDomain    = 3
	socksIPv6      = 4
	socksOK        = 0
	socksFailure   = 1
	socksNotAllow  = 2
	socksNetUnr    = 3
	socksRefused   = 5
	socksBadCmd    = 7
	socksBadAddrTy = 8
)

var errTunnelSOCKS = errors.New("invalid or unsupported SOCKS5 request")

func (t *Tunnel) serveSOCKS(flow *tunnelFlow, conn net.Conn) error {
	stop := timeTunnelHandshake(conn)
	target, err := socksRequest(conn, t.spec.ProxyUser, t.opts.ProxyPassword)
	stop()
	if err != nil {
		return err
	}
	flow.entry.Target = target
	out, err := t.dialTarget(flow.ctx, "tcp", target)
	if err != nil {
		_ = socksReply(conn, socksCode(err))
		return err
	}
	defer t.untrack(out)
	if err := socksReply(conn, socksOK); err != nil {
		return err
	}
	return t.pipe(conn, out, flow)
}

func socksRequest(conn net.Conn, user, password string) (string, error) {
	var head [2]byte
	if _, err := io.ReadFull(conn, head[:]); err != nil {
		return "", err
	}
	if head[0] != socksVersion {
		return "", errTunnelSOCKS
	}
	methods := make([]byte, head[1])
	if _, err := io.ReadFull(conn, methods); err != nil {
		return "", err
	}
	method := byte(socksNoAuth)
	if user != "" {
		method = socksUserPass
	}
	if !slices.Contains(methods, method) {
		_, _ = conn.Write([]byte{socksVersion, socksNoMethod})
		return "", ErrProxyAuth
	}
	if _, err := conn.Write([]byte{socksVersion, method}); err != nil {
		return "", err
	}
	if method == socksUserPass {
		if err := socksAuthenticate(conn, user, password); err != nil {
			return "", err
		}
	}
	var req [4]byte
	if _, err := io.ReadFull(conn, req[:]); err != nil {
		return "", err
	}
	if req[0] != socksVersion || req[2] != 0 {
		return "", errTunnelSOCKS
	}
	if req[1] != socksConnect {
		_, _, _ = socksTarget(conn, req[3])
		_ = socksReply(conn, socksBadCmd)
		return "", errTunnelSOCKS
	}
	target, code, err := socksTarget(conn, req[3])
	if err != nil && code != socksOK {
		_ = socksReply(conn, code)
	}
	return target, err
}

func socksTarget(reader io.Reader, kind byte) (string, byte, error) {
	var size int
	switch kind {
	case socksIPv4:
		size = net.IPv4len
	case socksIPv6:
		size = net.IPv6len
	case socksDomain:
		var length [1]byte
		if _, err := io.ReadFull(reader, length[:]); err != nil {
			return "", socksOK, err
		}
		size = int(length[0])
	default:
		return "", socksBadAddrTy, errTunnelSOCKS
	}
	value := make([]byte, size+2)
	if _, err := io.ReadFull(reader, value); err != nil {
		return "", socksOK, err
	}
	host := string(value[:size])
	if kind != socksDomain {
		host = net.IP(value[:size]).String()
	}
	target := net.JoinHostPort(host, strconv.Itoa(int(binary.BigEndian.Uint16(value[size:]))))
	if !validTunnelTarget(target) {
		return "", socksBadAddrTy, errTunnelSOCKS
	}
	return target, socksOK, nil
}

func socksAuthenticate(conn net.Conn, user, password string) error {
	var header [2]byte
	if _, err := io.ReadFull(conn, header[:]); err != nil {
		return err
	}
	if header[0] != 1 || header[1] == 0 {
		_, _ = conn.Write([]byte{1, 1})
		return ErrProxyAuth
	}
	name := make([]byte, int(header[1])+1)
	if _, err := io.ReadFull(conn, name); err != nil {
		return err
	}
	secret := make([]byte, name[len(name)-1])
	if _, err := io.ReadFull(conn, secret); err != nil {
		return err
	}
	if !tunnelCredentialsMatch(name[:len(name)-1], secret, user, password) {
		_, _ = conn.Write([]byte{1, 1})
		return ErrProxyAuth
	}
	_, err := conn.Write([]byte{1, 0})
	return err
}

func tunnelCredentialsMatch(user, password []byte, wantUser, wantPassword string) bool {
	gotUser, gotPassword := sha256.Sum256(user), sha256.Sum256(password)
	needUser, needPassword := sha256.Sum256([]byte(wantUser)), sha256.Sum256([]byte(wantPassword))
	return subtle.ConstantTimeCompare(gotUser[:], needUser[:])&subtle.ConstantTimeCompare(gotPassword[:], needPassword[:]) == 1
}

func socksReply(conn net.Conn, code byte) error {
	_, err := conn.Write([]byte{socksVersion, code, 0, socksIPv4, 0, 0, 0, 0, 0, 0})
	return err
}

func socksCode(err error) byte {
	switch {
	case errors.Is(err, ErrTunnelDialDenied):
		return socksNotAllow
	case errors.Is(err, ErrTunnelDialRefused):
		return socksRefused
	case errors.Is(err, ErrTunnelDialNetwork):
		return socksNetUnr
	default:
		return socksFailure
	}
}
