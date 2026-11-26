package config

import (
	"fmt"
	"maps"
	"reflect"
	"slices"
)

// The proxy library: proxies saved once, by name, which hosts connect
// through. A host keeps a copy of the proxy it uses in its own Proxy, with
// ProxyID naming the library's: builds that know no library connect
// through the copy, and editing the library's proxy updates the copies.
// A copy that no longer matches the library's was changed on its own, as
// a build without the library does: it is the host's own proxy then.

// Proxy is a proxy of the library. Its password is in the credential
// store, as the hosts' are, never here.
type Proxy struct {
	ID   string    `json:"id"`
	Name string    `json:"name"`
	Kind ProxyKind `json:"kind"`
	Addr string    `json:"addr"`
	Port int       `json:"port,omitempty"`
	User string    `json:"user,omitempty"`
	Note string    `json:"note,omitempty"`

	extra extras
}

// NewProxyID is the ID of a new proxy of the library.
func NewProxyID() string { return fmt.Sprintf("p%d", uniqueNano()) }

// PortOrDefault is the proxy's port, the usual one for its kind when
// unset.
func (p *Proxy) PortOrDefault() int {
	if p.Port == 0 {
		return DefaultProxyPort(p.Kind)
	}
	return p.Port
}

// Config is the host's copy of p.
func (p *Proxy) Config() ProxyConfig {
	return ProxyConfig{Kind: p.Kind, Addr: p.Addr, Port: p.Port, User: p.User, ProxyID: p.ID}
}

// Clone is a copy of p sharing nothing with it.
func (p *Proxy) Clone() *Proxy {
	c := *p
	c.extra = maps.Clone(p.extra)
	return &c
}

// same reports whether the host's proxy c is a copy of p as it is.
func (p *Proxy) same(c *ProxyConfig) bool {
	return c != nil && c.Kind == p.Kind && c.Addr == p.Addr && c.PortOrDefault() == p.PortOrDefault() && c.User == p.User
}

type proxyLibJSON Proxy

var proxyLibKeys = knownKeys(reflect.TypeFor[proxyLibJSON]())

func (p Proxy) MarshalJSON() ([]byte, error) { return encodeObject(proxyLibJSON(p), p.extra) }

func (p *Proxy) UnmarshalJSON(data []byte) error {
	var v proxyLibJSON
	ex, err := decodeObject(data, &v, proxyLibKeys)
	if err != nil {
		return err
	}
	*p = Proxy(v)
	p.extra = ex
	return nil
}

// Proxy is the proxy of the library with the ID; nil for "" or none.
func (s *Store) Proxy(id string) *Proxy {
	if id == "" {
		return nil
	}
	for _, p := range s.Proxies {
		if p.ID == id {
			return p
		}
	}
	return nil
}

// LinkedProxy is the proxy of the library h connects through: nil for a
// host with a proxy of its own, none, or a copy changed on its own.
func (s *Store) LinkedProxy(h *Host) *Proxy {
	if h == nil || h.Proxy == nil || h.Proxy.ProxyID == "" {
		return nil
	}
	if p := s.Proxy(h.Proxy.ProxyID); p != nil && p.same(h.Proxy) {
		return p
	}
	return nil
}

// ProxyUsers are the hosts that connect through the library's proxy with
// the ID, deleted ones too.
func (s *Store) ProxyUsers(id string) []*Host {
	var out []*Host
	for _, h := range s.Hosts {
		if h.Proxy != nil && h.Proxy.ProxyID == id {
			out = append(out, h)
		}
	}
	return out
}

// UseProxy makes h connect through the library's proxy p: a copy of it,
// naming it, keeping what the host's proxy held that this build does not
// know.
func UseProxy(h *Host, p *Proxy) {
	c := p.Config()
	if h.Proxy != nil {
		c.extra = maps.Clone(h.Proxy.extra)
	}
	h.Proxy = &c
}

// UpdateProxy makes the library's proxy what next says, and the copies of
// the hosts that use it follow; a host whose copy was changed on its own,
// as before the change it no longer matched, keeps it as its own proxy.
// It returns the hosts it changed.
func (s *Store) UpdateProxy(next *Proxy) []*Host {
	var changed []*Host
	if old := s.Proxy(next.ID); old != nil {
		for _, h := range s.ProxyUsers(next.ID) {
			if old.same(h.Proxy) {
				UseProxy(h, next)
			} else {
				h.Proxy.ProxyID = "" // its own now
			}
			changed = append(changed, h)
		}
		*old = *next.Clone()
		return changed
	}
	s.Proxies = append(s.Proxies, next.Clone())
	return nil
}

// RemoveProxy deletes the library's proxy with the ID. The hosts that use
// it keep their copies, as proxies of their own: they connect as before.
// It returns those hosts.
func (s *Store) RemoveProxy(id string) []*Host {
	users := s.ProxyUsers(id)
	for _, h := range users {
		h.Proxy.ProxyID = ""
	}
	s.Proxies = slices.DeleteFunc(s.Proxies, func(p *Proxy) bool { return p.ID == id })
	return users
}
