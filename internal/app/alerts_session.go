package app

import (
	"context"
	"errors"
	"net"
	"slices"
	"strings"
	"time"

	"finallyshell/internal/config"
	"finallyshell/internal/monitor"
	"finallyshell/locales"

	"github.com/egoist/mygo"
	"golang.org/x/crypto/ssh"
)

type alertSession struct {
	life   context.Context
	cancel context.CancelFunc
	cores  int
	sample time.Time
	policy config.AlertRules
}

type alertPeer struct {
	life context.Context
	lost bool
}

func (a *App) alertSessionStarted(s *Session, client *ssh.Client, life context.Context) {
	if a.alerts.peers == nil {
		a.alerts.peers = make(map[*Session]alertPeer)
	}
	a.alerts.peers[s] = alertPeer{life: life}
	a.alertReachability(s, false, "")
	a.syncAlertSessions()
	go func() {
		_ = client.Wait()
		if life.Err() == nil {
			a.post(func() {
				if life.Err() != nil || s.client != client || s.ctx != life {
					return
				}
				a.alerts.peers[s] = alertPeer{life: life, lost: true}
				a.alertReachability(s, true, locales.AlertDisconnected())
				a.transfers.cancelRelaySession(s)
				a.showTransfers()
				a.syncAlertSessions()
			})
		}
	}()
}

func (a *App) alertDialFailed(s *Session, err error) {
	var networkErr *net.OpError
	if errors.As(err, &networkErr) && networkErr.Op == "dial" {
		a.alertReachability(s, true, locales.AlertConnectFailed())
	}
}

func (a *App) alertReachability(s *Session, down bool, value string) {
	if s.Host == nil || s.Host.Kind != config.KindSSH || !a.settings.AlertsFor(s.Host.ID).Unreachable {
		return
	}
	if down && slices.ContainsFunc(a.sessions, func(other *Session) bool {
		return other != s && other.Host != nil && other.Host.ID == s.Host.ID && other.client != nil && other.status == statusConnected && !a.alerts.peers[other].lost
	}) {
		return
	}
	a.recordAlert(s.Host, time.Now(), alertInput{kind: alertUnreachable, active: down, value: value})
}

func (a *App) syncAlertSessions() {
	state := &a.alerts
	for s, peer := range state.peers {
		if s.ctx != peer.life || peer.life.Err() != nil {
			delete(state.peers, s)
		}
	}
	if state.sessions == nil {
		state.sessions = make(map[*Session]*alertSession)
	}
	active := make(map[string]*Session)
	for _, s := range a.sessions {
		if !s.isPanelTab() && s.Host != nil && s.Host.Kind == config.KindSSH && execAllowed(s.Host) &&
			s.status == statusConnected && s.client != nil && s.ctx != nil && s.ctx.Err() == nil && !state.peers[s].lost && active[s.Host.ID] == nil {
			active[s.Host.ID] = s
		}
	}
	for s, run := range state.sessions {
		if s.Host == nil || active[s.Host.ID] != s || s.ctx != run.life || a.settings.AlertsFor(s.Host.ID) != run.policy {
			run.cancel()
			delete(state.sessions, s)
		}
	}
	for hostID, s := range active {
		if state.sessions[s] != nil {
			continue
		}
		policy := a.settings.AlertsFor(hostID)
		if policy.CPU <= 0 && policy.Memory <= 0 && policy.Disk <= 0 && policy.Load <= 0 && !policy.Services {
			continue
		}
		ctx, cancel := context.WithCancel(s.ctx)
		run := &alertSession{life: s.ctx, cancel: cancel, policy: policy}
		state.sessions[s] = run
		if policy.Load > 0 || policy.Services {
			go a.readAlerts(ctx, s, s.client, run)
		}
	}
}

func (a *App) readAlerts(ctx context.Context, s *Session, client *ssh.Client, run *alertSession) {
	read := func(ctx context.Context, script string) (string, string, error) {
		return monitor.RunScript(ctx, client, script)
	}
	if a.alerts.read != nil {
		read = a.alerts.read
	}
	var cores int
	for ctx.Err() == nil {
		request, cancel := context.WithTimeout(ctx, 10*time.Second)
		if run.policy.Load > 0 && cores == 0 {
			cores = monitor.AlertCores(request, read)
		}
		var failed []string
		var checked bool
		if run.policy.Services {
			failed, checked = monitor.AlertFailedServices(request, read)
		}
		cancel()
		cores, failed, checked := cores, failed, checked
		a.post(func() {
			if ctx.Err() != nil || a.alerts.sessions[s] != run || s.status != statusConnected {
				return
			}
			run.cores = cores
			if checked {
				slices.Sort(failed)
				a.recordAlert(s.Host, time.Now(), alertInput{kind: alertService, active: len(failed) > 0, value: strings.Join(failed, ", ")})
			}
		})
		if cores > 0 && !run.policy.Services {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Minute):
		}
	}
}

func (a *App) alertStats(s *Session, stats monitor.Stats) {
	run := a.alerts.sessions[s]
	if run == nil || s.status != statusConnected || !stats.OK || !stats.Memory.SampledAt.After(run.sample) {
		return
	}
	run.sample = stats.Memory.SampledAt
	for _, input := range alertMetrics(stats, run.cores, run.policy) {
		a.recordAlert(s.Host, run.sample, input)
	}
}

func (a *App) stopAlerts() {
	for _, run := range a.alerts.sessions {
		run.cancel()
	}
	a.alerts.sessions = nil
	a.alerts.peers = nil
}

type alertNotifier func(title, body string, clicked func())

func (a *App) recordAlert(host *config.Host, at time.Time, input alertInput) {
	state := &a.alerts
	event, ok := state.engine.evaluate(at, host.ID, host.Title(), input)
	if !ok {
		return
	}
	state.sequence++
	event.id = state.sequence
	if len(state.events) == 500 {
		state.events = slices.Delete(state.events, 0, 1)
	}
	state.events = append(state.events, event)
	title, body := locales.AlertNotification(event.host, alertLabel(event.kind)), event.value
	notify := state.notify
	if notify == nil {
		notify = a.desktopAlert
	}
	notify(title, body, func() {
		a.post(func() {
			a.plat.FocusWindow(WindowMain)
			a.openAlerts()
		})
	})
}

func (a *App) desktopAlert(title, body string, clicked func()) {
	note := mygo.NewNotification(mygo.NotificationOptions{Title: title, Body: body, Group: "server-alerts"})
	off := note.OnClick(clicked)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if err := note.ShowContext(ctx); err != nil {
			off()
			a.post(func() { a.notify(title + ": " + body) })
		}
	}()
}
