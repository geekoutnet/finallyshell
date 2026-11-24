package dbclient

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"strconv"
	"sync"
	"time"
)

func Open(ctx context.Context, profile Profile, password string, dial Dialer) (Client, error) {
	if err := profile.Validate(); err != nil {
		return nil, err
	}
	if dial == nil {
		return nil, ErrConnect
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	throughSSH := func(ctx context.Context, _, _ string) (net.Conn, error) {
		conn, err := dial(ctx, "tcp", profile.address())
		if err != nil {
			return nil, err
		}
		return withDeadlines(conn), nil
	}
	if profile.Engine == Redis {
		return openRedis(ctx, profile, password, throughSSH)
	}
	return openSQL(ctx, profile, password, throughSSH)
}

func (p Profile) tlsConfig() *tls.Config {
	if p.TLS == "disable" {
		return nil
	}
	return &tls.Config{MinVersion: tls.VersionTLS12, ServerName: p.Host, InsecureSkipVerify: p.TLS == "require"}
}

func (p Profile) defaultDatabase() string {
	if p.Database == "" && p.Engine == Postgres {
		return "postgres"
	}
	return p.Database
}

func (p Profile) redisDB() int {
	db, _ := strconv.Atoi(p.Database)
	return db
}

func withDeadlines(remote net.Conn) net.Conn {
	local, proxy := net.Pipe()
	bridge := &tunnelConn{Conn: local, proxy: proxy, remote: remote}
	go func() { defer bridge.Close(); _, _ = io.Copy(proxy, remote) }()
	go func() {
		defer remote.Close()
		_, _ = io.Copy(remote, proxy)
		if half, ok := remote.(interface{ CloseWrite() error }); ok {
			_ = half.CloseWrite()
		}
	}()
	return bridge
}

type tunnelConn struct {
	net.Conn
	proxy, remote net.Conn
	once          sync.Once
}

func (conn *tunnelConn) Close() error {
	conn.once.Do(func() { conn.Conn.Close(); conn.proxy.Close(); conn.remote.Close() })
	return nil
}

func (conn *tunnelConn) RemoteAddr() net.Addr { return conn.remote.RemoteAddr() }
