package config

import (
	"sync/atomic"
	"time"
)

// lastID is the last number uniqueNano gave.
var lastID atomic.Int64

// uniqueNano is the time in nanoseconds, made larger than every number
// it gave before, so that IDs made in a row differ even where the clock
// ticks coarsely, as Windows' does: several hosts imported at once.
func uniqueNano() int64 {
	for {
		now, last := time.Now().UnixNano(), lastID.Load()
		if now <= last {
			now = last + 1
		}
		if lastID.CompareAndSwap(last, now) {
			return now
		}
	}
}
