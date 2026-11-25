package config

import (
	"crypto/rand"
	"encoding/hex"
	"net"
	"reflect"
	"slices"
	"strconv"
)

// TunnelKind is what a tunnel does. The first three are ssh's -L, -R and
// -D; the others are proxies the app serves itself over the same
// channels.
type TunnelKind string

const (
	// TunnelLocal listens on this computer and forwards each connection,
	// through the server, to the target as the server reaches it (-L).
	// With TargetSocket the target is a Unix socket on the server, such
	// as /var/run/docker.sock.
	TunnelLocal TunnelKind = "local"
	// TunnelRemote has the server listen and forwards each connection
	// back to the target as this computer reaches it (-R).
	TunnelRemote TunnelKind = "remote"
	// TunnelDynamic is a SOCKS5 proxy on this computer whose connections
	// go out from the server (-D): it has no target. With Mixed it also
	// answers HTTP proxy requests on the same port.
	TunnelDynamic TunnelKind = "dynamic"
	// TunnelHTTP is an HTTP proxy (CONNECT and plain HTTP) on this
	// computer whose connections go out from the server. With Mixed it
	// also answers SOCKS5 on the same port.
	TunnelHTTP TunnelKind = "http"
	// TunnelRemoteDynamic is a SOCKS5 proxy on the server whose
	// connections go out from this computer (OpenSSH's -R with no
	// target): the server reaches this computer's network, its LAN, or
	// through Upstream a proxy of this computer's.
	TunnelRemoteDynamic TunnelKind = "remoteDynamic"
	// TunnelRemoteHTTP is an HTTP proxy on the server whose connections
	// go out from this computer, for programs on the server that only
	// take http_proxy (apt, docker pull, git, curl).
	TunnelRemoteHTTP TunnelKind = "remoteHTTP"
)

// TunnelKinds are the kinds the app knows, in the order it offers them.
var TunnelKinds = []TunnelKind{TunnelLocal, TunnelRemote, TunnelDynamic, TunnelHTTP, TunnelRemoteDynamic, TunnelRemoteHTTP}

// LoopbackHost is where a tunnel listens unless the user chose an
// address: only programs on the same machine can connect.
const LoopbackHost = "127.0.0.1"

// Tunnel is a port forwarded through a host's connection.
type Tunnel struct {
	// ID names the tunnel among the host's, for its secrets and the
	// tunnel manager; NewTunnelID makes one. Old tunnels have none until
	// they are saved again (EnsureTunnelIDs).
	ID   string     `json:"id,omitempty"`
	Kind TunnelKind `json:"kind"`
	// Name is what the user calls the tunnel, "" for a generated label;
	// Description is a longer free text.
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`

	// ListenHost is the address the tunnel listens on, on this computer
	// or, for the remote kinds, on the server: "" for LoopbackHost. Any
	// other address lets other machines in, which the user has to choose.
	ListenHost string `json:"listenHost,omitempty"`
	// ListenPort is the port it listens on; 0 lets the system (or the
	// server) pick a free one, which the running tunnel reports.
	ListenPort int `json:"listenPort,omitempty"`
	// TargetHost and TargetPort are where connections go: unset for the
	// proxy kinds, whose clients name it.
	TargetHost string `json:"targetHost,omitempty"`
	TargetPort int    `json:"targetPort,omitempty"`
	// TargetSocket is a Unix socket path on the server that a
	// TunnelLocal forwards to instead of TargetHost:TargetPort.
	TargetSocket string `json:"targetSocket,omitempty"`

	// Mixed makes a TunnelDynamic or TunnelHTTP answer both SOCKS5 and
	// HTTP proxy requests on its port.
	Mixed bool `json:"mixed,omitempty"`
	// ProxyUser turns on username/password authentication for the proxy
	// kinds; the password is a secret of the host's, never in hosts.json.
	ProxyUser string `json:"proxyUser,omitempty"`
	// Upstream is a proxy of this computer's that the remote kinds go
	// out through instead of connecting directly: socks5://host:port or
	// http://host:port, "" for direct.
	Upstream string `json:"upstream,omitempty"`

	// AllowFrom, when set, lets in only clients whose address is in one
	// of these CIDRs or addresses; the others are closed at once.
	AllowFrom []string `json:"allowFrom,omitempty"`
	// MaxConns caps the connections open at once, 0 for no cap.
	MaxConns int `json:"maxConns,omitempty"`
	// RateKBps caps each direction's throughput of the whole tunnel in
	// KiB/s, 0 for no cap.
	RateKBps int `json:"rateKBps,omitempty"`
	// IdleSeconds closes a connection that moved no byte for that long,
	// 0 for never.
	IdleSeconds int `json:"idleSeconds,omitempty"`

	// Disabled keeps the tunnel from starting with the session; it can
	// still be started by hand. The inverse, so that a Tunnel{} is on.
	Disabled bool `json:"disabled,omitempty"`
	// Background starts the tunnel when the app starts, on a connection
	// of its own without a terminal, kept up by the tunnel manager.
	Background bool `json:"background,omitempty"`
	// SystemProxy makes a running local proxy tunnel this computer's
	// system proxy; PAC, when set, sends only these domains and CIDRs
	// through it and the rest directly.
	SystemProxy bool     `json:"systemProxy,omitempty"`
	PAC         []string `json:"pac,omitempty"`
	// ServerRelay makes a remote kind listening on another address than
	// loopback reachable even where sshd's GatewayPorts is off: the
	// tunnel listens on the server's loopback and a small relay the app
	// starts on the server listens on ListenHost.
	ServerRelay bool `json:"serverRelay,omitempty"`

	extra extras
}

// NewTunnelID returns an ID for a new tunnel.
func NewTunnelID() string {
	var b [6]byte
	rand.Read(b[:])
	return "t" + hex.EncodeToString(b[:])
}

// EnsureTunnelIDs gives every tunnel of h without an ID one.
func (h *Host) EnsureTunnelIDs() {
	for i := range h.Tunnels {
		if h.Tunnels[i].ID == "" {
			h.Tunnels[i].ID = NewTunnelID()
		}
	}
}

// Enabled says whether t starts as the session connects.
func (t *Tunnel) Enabled() bool { return !t.Disabled }

// IsRemote tells whether t listens on the server.
func (k TunnelKind) IsRemote() bool {
	return k == TunnelRemote || k == TunnelRemoteDynamic || k == TunnelRemoteHTTP
}

// IsProxy tells whether t is a proxy whose clients name the target.
func (k TunnelKind) IsProxy() bool {
	return k == TunnelDynamic || k == TunnelHTTP || k == TunnelRemoteDynamic || k == TunnelRemoteHTTP
}

// Known tells whether this build knows the kind.
func (k TunnelKind) Known() bool { return slices.Contains(TunnelKinds, k) }

// ListenHostOrLoopback is the address t listens on.
func (t *Tunnel) ListenHostOrLoopback() string {
	if t.ListenHost == "" {
		return LoopbackHost
	}
	return t.ListenHost
}

// ListenAddr is host:port that t listens on.
func (t *Tunnel) ListenAddr() string {
	return net.JoinHostPort(t.ListenHostOrLoopback(), strconv.Itoa(t.ListenPort))
}

// TargetAddr is host:port that t forwards to; "" for the proxy kinds and
// a Unix socket target.
func (t *Tunnel) TargetAddr() string {
	if t.Kind.IsProxy() || t.TargetSocket != "" {
		return ""
	}
	return net.JoinHostPort(t.TargetHost, strconv.Itoa(t.TargetPort))
}

// IsLoopback tells whether a tunnel listening on host, a ListenHost,
// lets in only the machine it listens on: "", localhost, or a loopback
// address.
func IsLoopback(host string) bool {
	if host == "" || host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

type tunnelJSON Tunnel

var tunnelKeys = knownKeys(reflect.TypeFor[tunnelJSON]())

func (t Tunnel) MarshalJSON() ([]byte, error) { return encodeObject(tunnelJSON(t), t.extra) }

func (t *Tunnel) UnmarshalJSON(data []byte) error {
	var v tunnelJSON
	ex, err := decodeObject(data, &v, tunnelKeys)
	if err != nil {
		return err
	}
	*t = Tunnel(v)
	t.extra = ex
	return nil
}
