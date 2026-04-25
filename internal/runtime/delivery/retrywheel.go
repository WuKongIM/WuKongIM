package delivery

import (
	"sort"
	"sync"
	"time"
)

type RetryWheel struct {
	mu      sync.Mutex
	entries []RetryEntry
}

func NewRetryWheel() *RetryWheel {
	return &RetryWheel{}
}

func (w *RetryWheel) Schedule(entry RetryEntry) {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.entries = append(w.entries, entry)
	sort.Slice(w.entries, func(i, j int) bool {
		return w.entries[i].When.Before(w.entries[j].When)
	})
}

func (w *RetryWheel) PopDue(now time.Time) []RetryEntry {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.entries) == 0 {
		return nil
	}
	cut := 0
	for cut < len(w.entries) && !w.entries[cut].When.After(now) {
		cut++
	}
	if cut == 0 {
		return nil
	}
	out := append([]RetryEntry(nil), w.entries[:cut]...)
	w.entries = append([]RetryEntry(nil), w.entries[cut:]...)
	return out
}

func cappedBackoffWithJitter(delays []time.Duration, attempt int) time.Duration {
	if len(delays) == 0 {
		return 0
	}
	if attempt <= 1 {
		attempt = 2
	}
	index := attempt - 2
	if index >= len(delays) {
		index = len(delays) - 1
	}
	base := delays[index]
	return base + base/10
}
