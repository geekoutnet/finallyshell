package sshx

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"

	"finallyshell/internal/config"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

var ErrBulkHostKey = errors.New("host key not trusted")
var ErrBulkAddress = errors.New("invalid SSH destination or proxy address")

func MatchesBulkPeer(ctx context.Context, client *ssh.Client, host *config.Host) bool {
	if client.User() != host.User {
		return false
	}
	peer, port, err := net.SplitHostPort(client.RemoteAddr().String())
	if err != nil || port != strconv.Itoa(host.PortOr22()) {
		return false
	}
	if strings.EqualFold(peer, host.Addr) {
		return true
	}
	connectedIP := net.ParseIP(peer)
	if connectedIP == nil || config.ProxyOf(host) != nil {
		return false
	}
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host.Addr)
	if err != nil {
		return false
	}
	for _, address := range addresses {
		if connectedIP.Equal(address.IP) {
			return true
		}
	}
	return false
}

func bulkAddressValid(address string, port int) bool {
	return address != "" && port >= 1 && port <= 65535 && !strings.ContainsAny(address, "/\\?#[ ]@") &&
		strings.IndexFunc(address, func(char rune) bool { return unicode.IsControl(char) || unicode.IsSpace(char) }) < 0
}

func bulkHostKeyCallback(path string) (ssh.HostKeyCallback, error) {
	if path == "" {
		path = filepath.Join(config.Dir(), "known_hosts")
	}
	check, err := knownhosts.New(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrBulkHostKey
	}
	if err != nil {
		return nil, err
	}
	return func(host string, address net.Addr, key ssh.PublicKey) error {
		if err := check(host, address, key); err != nil {
			return ErrBulkHostKey
		}
		return nil
	}, nil
}

func DialBulk(ctx context.Context, host *config.Host, opts Options) (*ssh.Client, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if host == nil || !bulkAddressValid(host.Addr, host.PortOr22()) {
		return nil, ErrBulkAddress
	}
	check, err := bulkHostKeyCallback(opts.KnownHosts)
	if err != nil {
		return nil, err
	}
	type authResult struct {
		methods []ssh.AuthMethod
		agent   *agentLogin
		err     error
	}
	auth := make(chan authResult)
	go func() {
		methods, agent, err := authMethods(host, opts)
		select {
		case auth <- authResult{methods, agent, err}:
		case <-ctx.Done():
			if agent != nil {
				agent.conn.Close()
			}
		}
	}()
	var login authResult
	select {
	case login = <-auth:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if login.err != nil {
		return nil, login.err
	}
	if login.agent != nil {
		defer login.agent.conn.Close()
	}
	addr := net.JoinHostPort(host.Addr, strconv.Itoa(host.PortOr22()))
	conn, err := bulkRoute(ctx, host, opts, addr)
	if err != nil {
		return nil, err
	}
	stop := context.AfterFunc(ctx, func() {
		if login.agent != nil {
			login.agent.conn.Close()
		}
		conn.Close()
	})
	defer stop()
	config := &ssh.ClientConfig{User: host.User, Auth: login.methods, HostKeyCallback: check}
	wire, channels, requests, err := ssh.NewClientConn(conn, addr, config)
	if ctx.Err() != nil {
		conn.Close()
		return nil, ctx.Err()
	}
	if err != nil {
		if login.agent != nil && !errors.Is(err, ErrBulkHostKey) {
			err = login.agent.refused(err)
		}
		return nil, err
	}
	client := ssh.NewClient(wire, channels, requests)
	if opts.KeepAlive.Interval > 0 {
		go KeepAlive(context.Background(), client, opts.KeepAlive)
	}
	return client, nil
}

func bulkRoute(ctx context.Context, host *config.Host, opts Options, addr string) (net.Conn, error) {
	dialer := net.Dialer{Timeout: 15 * time.Second}
	proxy := config.ProxyOf(host)
	if proxy == nil {
		return dialer.DialContext(ctx, "tcp", addr)
	}
	if proxy.Kind == config.ProxyJump {
		if opts.Jump == nil || opts.Jump.Host == nil {
			return nil, ErrNoJumpHost
		}
		jump := opts.Jump.Host
		options := opts.Jump.Options
		options.KeepAlive = opts.KeepAlive
		client, err := DialBulk(ctx, jump, options)
		if err == nil {
			var conn net.Conn
			conn, err = client.DialContext(ctx, "tcp", addr)
			if err == nil {
				return newHopConn(conn, nil, addr, client), nil
			}
			client.Close()
		}
		return nil, &HopError{JumpHost: jump.Title(), HostID: jump.ID, Err: err}
	}
	if proxy.Kind != config.ProxySOCKS5 && proxy.Kind != config.ProxyHTTP {
		return nil, errors.New("unknown proxy kind")
	}
	if !bulkAddressValid(proxy.Addr, proxy.PortOrDefault()) {
		return nil, ErrBulkAddress
	}
	dial := dialSOCKS5
	if proxy.Kind == config.ProxyHTTP {
		dial = dialHTTP
	}
	timeout := 15 * time.Second
	if deadline, ok := ctx.Deadline(); ok {
		timeout = min(timeout, time.Until(deadline))
		if timeout <= 0 {
			return nil, context.DeadlineExceeded
		}
	}
	type routeResult struct {
		conn net.Conn
		err  error
	}
	done := make(chan routeResult)
	go func() {
		conn, err := dial(proxy, opts.ProxyPassword, addr, timeout)
		select {
		case done <- routeResult{conn, err}:
		case <-ctx.Done():
			if conn != nil {
				conn.Close()
			}
		}
	}()
	select {
	case result := <-done:
		return result.conn, result.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
