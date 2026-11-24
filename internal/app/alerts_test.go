package app

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"finallyshell/internal/config"
	"finallyshell/internal/monitor"
	"finallyshell/locales"
)

func TestAlertsContinuousThresholdAndDedup(t *testing.T) {
	now := time.Date(2026, 1, 4, 3, 0, 0, 0, time.UTC)
	rules := config.AlertRules{CPU: 80, CPUMinutes: 1, Memory: 90, Disk: 80, Load: 2}
	var state alertEngine
	var events []alertEvent
	feed := func(seconds int, cpu float64) {
		at := now.Add(time.Duration(seconds) * time.Second)
		stats := monitor.Stats{OK: true, CPU: cpu, MemUsed: 91, MemTotal: 100,
			Disks: []monitor.Disk{{Mount: "/", Size: 100, Avail: 10}, {Mount: "/srv", Size: 100, Avail: 20}},
			Load:  [3]float64{4.5}}
		for _, input := range alertMetrics(stats, 2, rules) {
			if event, ok := state.evaluate(at, "host", "Example", input); ok {
				events = append(events, event)
			}
		}
	}
	feed(0, 81)
	feed(2, 79)
	for second := 4; second < 64; second += 2 {
		feed(second, 81)
	}
	if len(events) != 3 {
		t.Fatalf("early events = %+v", events)
	}
	feed(64, 81)
	feed(66, 81)
	feed(1000, 81)
	if len(events) != 4 || events[3].kind != alertCPU {
		t.Fatalf("events = %+v", events)
	}
	feed(1002, 20)
	for second := 1004; second <= 1064; second += 2 {
		feed(second, 81)
	}
	if len(events) != 5 || events[4].kind != alertCPU {
		t.Fatalf("new incident missing: %+v", events)
	}
}

func TestAlertsSettingsOverride(t *testing.T) {
	settings := config.Settings{AlertRules: &map[string]config.AlertRules{"": {CPU: 80}, "example": {}}}
	if settings.AlertsFor("example").CPU != 0 || settings.AlertsFor("other").CPU != 80 {
		t.Fatalf("incorrect host override: %+v", settings.AlertRules)
	}
}

func TestAlertsReachabilityWithMultipleSessions(t *testing.T) {
	a, _ := testApp(t)
	source, other, _ := relayUITargets(t, a)
	other.Host = source.Host
	a.settings.AlertRules = &map[string]config.AlertRules{"": {Unreachable: true}}
	var count int
	a.alerts.notify = func(string, string, func()) { count++ }
	a.alertReachability(source, true, locales.AlertDisconnected())
	if count != 0 {
		t.Fatal("another connected session still reaches this host")
	}
	a.alerts.peers = map[*Session]alertPeer{other: {life: context.Background(), lost: true}}
	a.alertDialFailed(source, errors.New("authentication failed"))
	if count != 0 {
		t.Fatal("authentication error was reported as unreachable")
	}
	a.alertDialFailed(source, &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("unreachable")})
	a.alertReachability(source, true, locales.AlertDisconnected())
	if count != 1 || a.alerts.events[0].kind != alertUnreachable {
		t.Fatalf("notifications = %d, events = %+v", count, a.alerts.events)
	}
}
