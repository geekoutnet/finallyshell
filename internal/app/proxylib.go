package app

import (
	"strings"

	"finallyshell/internal/cloudsync"
	"finallyshell/internal/config"
)

// The proxy library in the app (config/proxies.go): saving, deleting and
// using its proxies, and their passwords. A proxy's password is kept in
// the credential store under libSecretKey, which syncs; the hosts that use
// the proxy keep a copy of it as their own proxy's password, as they keep
// a copy of the proxy, so that connecting reads it as before, and builds
// without the library connect too.

// libSecretKey is the credential store's key of the password of the
// library's proxy with the ID.
func libSecretKey(id string) string { return cloudsync.ProxySecretPrefix + id + "/password" }

// libMemKey is where the password of the library's proxy with the ID is
// kept in memory, among the passwords, once known.
func libMemKey(id string) string { return "lib/" + id }

// libTakesPassword reports whether p asks for a password: a proxy with a
// password and a user.
func libTakesPassword(p *config.Proxy) bool { return config.TakesPassword(p.Kind) && p.User != "" }

// saveLibraryProxy saves p in the library, with its password when
// setPassword (an empty one deletes it), and updates the hosts that use
// it, their copies of its password too. It returns the hosts changed.
func (a *App) saveLibraryProxy(p *config.Proxy, password string, setPassword bool) ([]*config.Host, error) {
	changed := a.hosts.UpdateProxy(p)
	if err := a.saveHosts(a.hosts); err != nil {
		return nil, err
	}
	switch {
	case !libTakesPassword(p) || setPassword && password == "":
		delete(a.passwords, libMemKey(p.ID))
		a.deleteSecret(libSecretKey(p.ID))
	case setPassword:
		a.passwords[libMemKey(p.ID)] = password
		a.setSecret(libSecretKey(p.ID), password)
	}
	saved := a.hosts.Proxy(p.ID)
	for _, h := range changed {
		if a.hosts.LinkedProxy(h) == saved {
			a.copyLibraryPassword(h, saved)
		}
	}
	return changed, nil
}

// deleteLibraryProxy deletes the library's proxy with the ID and its
// password. The hosts that used it keep their copies, and connect as
// before; it returns them.
func (a *App) deleteLibraryProxy(id string) ([]*config.Host, error) {
	users := a.hosts.RemoveProxy(id)
	if err := a.saveHosts(a.hosts); err != nil {
		return nil, err
	}
	delete(a.passwords, libMemKey(id))
	a.deleteSecret(libSecretKey(id))
	delete(a.proxyTests, id)
	return users, nil
}

// useLibraryProxy makes h connect through the library's proxy p, with a
// copy of it and of its password. The caller saves h.
func (a *App) useLibraryProxy(h *config.Host, p *config.Proxy) {
	config.UseProxy(h, p)
	a.copyLibraryPassword(h, p)
}

// copyLibraryPassword gives h, which uses the library's proxy p, a copy
// of p's password as its own proxy's: from memory, or read from the
// credential store first.
func (a *App) copyLibraryPassword(h *config.Host, p *config.Proxy) {
	if !libTakesPassword(p) {
		delete(a.proxySecrets(), proxySecretKey(h.ID))
		a.keepProxySecret(h, h.RemembersPassword(), false)
		return
	}
	id, hostID := p.ID, h.ID
	a.libraryPassword(id, func(pw string, ok bool) {
		host := a.hosts.Host(hostID)
		if !ok || host == nil || host.Proxy == nil || host.Proxy.ProxyID != id {
			return
		}
		a.proxySecrets()[proxySecretKey(hostID)] = pw
		a.keepProxySecret(host, host.RemembersPassword(), false)
	})
}

// libraryPassword calls then with the password of the library's proxy
// with the ID, from memory or the credential store, on the UI thread; ok
// is false when there is none.
func (a *App) libraryPassword(id string, then func(pw string, ok bool)) {
	if pw, ok := a.passwords[libMemKey(id)]; ok {
		then(pw, true)
		return
	}
	if a.secretsUnavailable() {
		then("", false)
		return
	}
	key := libSecretKey(id)
	a.secrets.queue.do(func() {
		pw, found, err := a.plat.SecretGet(key)
		a.post(func() {
			if err != nil || !found {
				then("", false)
				return
			}
			if _, known := a.passwords[libMemKey(id)]; !known {
				a.passwords[libMemKey(id)] = pw
			}
			then(a.passwords[libMemKey(id)], true)
		})
	})
}

// rememberSyncedProxy keeps for this run the password of a library's
// proxy that sync brought, as rememberSynced does the hosts'. It reports
// whether key was one.
func (a *App) rememberSyncedProxy(key, v string, keep bool) bool {
	rest, ok := strings.CutPrefix(key, cloudsync.ProxySecretPrefix)
	if !ok {
		return false
	}
	id, _, _ := strings.Cut(rest, "/")
	if keep {
		a.passwords[libMemKey(id)] = v
	} else {
		delete(a.passwords, libMemKey(id))
	}
	return true
}
