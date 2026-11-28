package app

import (
	"time"

	"github.com/go-acme/lego/v4/challenge"
)

// certsWatched tells, through placed, when a provider has put a proof in
// place, which lego does not: the CA's check then starts, or, for TXT
// records added by hand, the wait for them.
type certsWatched struct {
	p      challenge.Provider
	placed func()
}

func (w certsWatched) Present(domain, token, keyAuth string) error {
	err := w.p.Present(domain, token, keyAuth)
	if err == nil {
		w.placed()
	}
	return err
}

func (w certsWatched) CleanUp(domain, token, keyAuth string) error {
	return w.p.CleanUp(domain, token, keyAuth)
}

// certsWatchedTimeout is certsWatched for a provider with its own wait.
type certsWatchedTimeout struct{ certsWatched }

func (w certsWatchedTimeout) Timeout() (timeout, interval time.Duration) {
	return w.p.(challenge.ProviderTimeout).Timeout()
}

// certsWatch wraps p, keeping its Timeout.
func certsWatch(p challenge.Provider, placed func()) challenge.Provider {
	w := certsWatched{p: p, placed: placed}
	if _, ok := p.(challenge.ProviderTimeout); ok {
		return certsWatchedTimeout{w}
	}
	return w
}
