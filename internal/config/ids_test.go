package config

import (
	"sync"
	"testing"
)

// IDs made in a row, even at once from several goroutines, all differ.
func TestNewIDsUnique(t *testing.T) {
	const n = 2000
	var mu sync.Mutex
	seen := make(map[string]bool, 2*n)
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range n / 4 {
				h, f := NewHostID(), NewFolderID()
				mu.Lock()
				if seen[h] || seen[f] {
					t.Errorf("duplicate ID %s or %s", h, f)
				}
				seen[h], seen[f] = true, true
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
}
