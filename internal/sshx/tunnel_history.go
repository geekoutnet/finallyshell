package sshx

import "time"

// TunnelConn describes a completed or rejected incoming connection.
type TunnelConn struct {
	Started        time.Time
	Client, Target string
	Up, Down       int64
	Duration       time.Duration
	Err            error
}

const tunnelHistorySize = 200

// TunnelRateSamples is the number of one-second buckets in each traffic history.
const TunnelRateSamples = 16

type tunnelRateSlot struct {
	second   int64
	up, down int64
}

func (t *Tunnel) traffic(up bool, bytes int64) {
	now := time.Now()
	second := now.Unix()
	t.mu.Lock()
	t.lastTraffic = now
	slot := &t.rates[second%TunnelRateSamples]
	if slot.second != second {
		*slot = tunnelRateSlot{second: second}
	}
	if up {
		slot.up += bytes
		t.up.Add(bytes)
	} else {
		slot.down += bytes
		t.down.Add(bytes)
	}
	t.mu.Unlock()
	t.changed()
}

func (st *TunnelStats) readRates(slots [TunnelRateSamples]tunnelRateSlot, now time.Time) {
	for idx := range TunnelRateSamples {
		second := now.Unix() - int64(TunnelRateSamples-1-idx)
		slot := slots[second%TunnelRateSamples]
		if slot.second != second {
			continue
		}
		st.UpHistory[idx], st.DownHistory[idx] = float64(slot.up), float64(slot.down)
		if idx >= TunnelRateSamples-3 {
			st.UpRate += float64(slot.up) / 3
			st.DownRate += float64(slot.down) / 3
		}
	}
}

type tunnelHistory struct {
	entries []TunnelConn
	next    int
}

func (t *Tunnel) record(entry TunnelConn) {
	t.mu.Lock()
	defer t.mu.Unlock()
	h := &t.history
	if entry.Err != nil {
		t.lastErr = entry.Err
	}
	if len(h.entries) < tunnelHistorySize {
		h.entries = append(h.entries, entry)
		return
	}
	h.entries[h.next] = entry
	h.next = (h.next + 1) % tunnelHistorySize
}

// Conns returns an independent snapshot of the most recent connections, newest first.
func (t *Tunnel) Conns() []TunnelConn {
	t.mu.Lock()
	defer t.mu.Unlock()
	h := &t.history
	entries := make([]TunnelConn, len(h.entries))
	for idx := range entries {
		entries[idx] = h.entries[(h.next+len(entries)-1-idx)%len(entries)]
	}
	return entries
}
