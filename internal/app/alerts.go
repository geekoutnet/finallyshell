package app

import (
	"fmt"
	"math"
	"time"

	"finallyshell/internal/config"
	"finallyshell/internal/monitor"
)

type alertKind string

const (
	alertUnreachable alertKind = "unreachable"
	alertCPU         alertKind = "cpu"
	alertMemory      alertKind = "memory"
	alertDisk        alertKind = "disk"
	alertLoad        alertKind = "load"
	alertService     alertKind = "service"
)

const (
	alertCooldown = 15 * time.Minute
	alertStatsGap = 15 * time.Second
)

type alertInput struct {
	kind      alertKind
	active    bool
	value     string
	threshold float64
	duration  time.Duration
}

type alertEvent struct {
	id    int64
	at    time.Time
	host  string
	kind  alertKind
	value string
	acked bool
}

type alertKey struct {
	host string
	kind alertKind
}

type alertIncident struct {
	start, sample, last time.Time
	threshold           float64
	duration            time.Duration
	sent                bool
}

type alertEngine struct {
	incidents map[alertKey]alertIncident
}

func (engine *alertEngine) evaluate(at time.Time, id, host string, input alertInput) (alertEvent, bool) {
	if engine.incidents == nil {
		engine.incidents = make(map[alertKey]alertIncident)
	}
	key := alertKey{id, input.kind}
	incident := engine.incidents[key]
	if at.Before(incident.sample) {
		return alertEvent{}, false
	}
	if incident.threshold != input.threshold || incident.duration != input.duration {
		incident.start, incident.sent = time.Time{}, false
	}
	if !input.active {
		incident.start, incident.sent = time.Time{}, false
	} else if incident.start.IsZero() || input.duration > 0 && at.Sub(incident.sample) > alertStatsGap {
		incident.start = at
	}
	incident.threshold, incident.duration, incident.sample = input.threshold, input.duration, at
	ready := input.active && !incident.sent && at.Sub(incident.start) >= input.duration &&
		(incident.last.IsZero() || at.Sub(incident.last) >= alertCooldown)
	if ready {
		incident.last, incident.sent = at, true
	}
	engine.incidents[key] = incident
	return alertEvent{at: at, host: host, kind: input.kind, value: input.value}, ready
}

func alertLimit(kind alertKind, value, threshold float64, text string, duration time.Duration) alertInput {
	return alertInput{kind: kind, active: threshold > 0 && value > threshold, value: text, threshold: threshold, duration: duration}
}

func alertMetrics(stats monitor.Stats, cores int, rules config.AlertRules) []alertInput {
	if !stats.OK {
		return nil
	}
	percent := func(value float64) string { return fmt.Sprintf("%.1f%%", value) }
	duration := time.Duration(max(1, min(rules.CPUMinutes, 60))) * time.Minute
	inputs := []alertInput{alertLimit(alertCPU, stats.CPU, rules.CPU, percent(stats.CPU), duration)}
	if stats.MemTotal > 0 {
		value := 100 * float64(stats.MemUsed) / float64(stats.MemTotal)
		inputs = append(inputs, alertLimit(alertMemory, value, rules.Memory, percent(value), 0))
	}
	var worst alertInput
	worst.kind, worst.threshold = alertDisk, rules.Disk
	worstValue := -1.0
	for _, disk := range stats.Disks {
		if disk.Size == 0 {
			continue
		}
		value := 100 * (1 - float64(disk.Avail)/float64(disk.Size))
		if value > worstValue {
			worst = alertLimit(alertDisk, value, rules.Disk, fmt.Sprintf("%s · %s", disk.Mount, percent(value)), 0)
			worstValue = value
		}
	}
	if worstValue >= 0 {
		inputs = append(inputs, worst)
	}
	if cores > 0 && !math.IsNaN(stats.Load[0]) && !math.IsInf(stats.Load[0], 0) {
		value := stats.Load[0] / float64(cores)
		inputs = append(inputs, alertLimit(alertLoad, value, rules.Load, fmt.Sprintf("%.2f × %d", value, cores), 0))
	}
	return inputs
}
