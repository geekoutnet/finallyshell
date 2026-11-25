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

type tunnelHistory struct {
	entries []TunnelConn
	next    int
}

func (t *Tunnel) record(entry TunnelConn) {
	t.mu.Lock()
	defer t.mu.Unlock()
	h := &t.history
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
