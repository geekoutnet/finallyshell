package app

import (
	"strings"

	"finallyshell/internal/config"
	"finallyshell/internal/sshx"
	"finallyshell/locales"
)

type tunnelSecretEdit struct {
	proxy, proxyUser      string
	upstream, upstreamURL string
}

func tunnelSecretKey(hostID, tunnelID string, upstream bool) string {
	if hostID == "" || tunnelID == "" || strings.ContainsAny(hostID+tunnelID, "/\\\x00\r\n") {
		return ""
	}
	name := "password"
	if upstream {
		name = "upstream/password"
	}
	return hostSecretKey(hostID, "tunnel/"+tunnelID+"/"+name)
}

func tunnelWantsSecret(t config.Tunnel, upstream bool) bool {
	if !upstream {
		return t.Kind.IsProxy() && t.ProxyUser != ""
	}
	p, err := sshx.ParseTunnelUpstream(t.Upstream)
	return t.Kind.IsRemote() && err == nil && p != nil && p.User != ""
}

// tunnelOptions reads runtime-only credentials in queue order, on the UI thread at callback.
// A missing password stays empty so the engine fails closed before opening a listener.
func (a *App) tunnelOptions(hostID string, spec config.Tunnel, notify func(), done func(sshx.TunnelOptions)) {
	opts := sshx.TunnelOptions{Notify: notify}
	proxyKey, upstreamKey := tunnelSecretKey(hostID, spec.ID, false), tunnelSecretKey(hostID, spec.ID, true)
	read := func(key string, next func(string)) {
		if a.secretsUnavailable() {
			next("")
			return
		}
		a.secrets.queue.do(func() {
			value, ok, err := a.plat.SecretGet(key)
			if !ok || err != nil {
				value = ""
			}
			a.post(func() { next(value) })
		})
	}
	readUpstream := func() {
		if upstreamKey == "" || !tunnelWantsSecret(spec, true) {
			done(opts)
			return
		}
		read(upstreamKey, func(value string) {
			opts.UpstreamPassword = value
			done(opts)
		})
	}
	if proxyKey == "" || !tunnelWantsSecret(spec, false) {
		readUpstream()
		return
	}
	read(proxyKey, func(value string) {
		opts.ProxyPassword = value
		readUpstream()
	})
}

func (a *App) saveTunnelSecrets(h *config.Host, previous []config.Tunnel, edits map[string]tunnelSecretEdit) {
	current := make(map[string]config.Tunnel, len(h.Tunnels))
	for _, spec := range h.Tunnels {
		current[spec.ID] = spec
	}
	for _, spec := range previous {
		next := current[spec.ID]
		for _, upstream := range []bool{false, true} {
			changed := spec.ProxyUser != next.ProxyUser
			if upstream {
				changed = spec.Upstream != next.Upstream
			}
			if tunnelWantsSecret(spec, upstream) && (changed || !tunnelWantsSecret(next, upstream)) {
				if key := tunnelSecretKey(h.ID, spec.ID, upstream); key != "" {
					a.deleteSecret(key)
				}
			}
		}
	}
	for id, edit := range edits {
		spec := current[id]
		if edit.proxy != "" && edit.proxyUser == spec.ProxyUser && tunnelWantsSecret(spec, false) {
			a.setTunnelSecret(tunnelSecretKey(h.ID, id, false), edit.proxy)
		}
		if edit.upstream != "" && edit.upstreamURL == spec.Upstream && tunnelWantsSecret(spec, true) {
			a.setTunnelSecret(tunnelSecretKey(h.ID, id, true), edit.upstream)
		}
	}
}

func (a *App) setTunnelSecret(key, value string) {
	if key == "" || a.lock.resetting || a.Locked() && a.quitApproved {
		return
	}
	a.secrets.queue.do(func() {
		if err := a.plat.SecretSet(key, value); err != nil {
			a.post(func() { a.notify(locales.TunSecretStoreError()) })
		}
	})
}
