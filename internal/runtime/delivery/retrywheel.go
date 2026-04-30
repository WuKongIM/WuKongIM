package delivery

import (
	"sync"
	"time"
)

// RetryWheel keeps pending route retry entries ordered by due time.
type RetryWheel struct {
	mu      sync.Mutex
	entries []RetryEntry
}

// NewRetryWheel constructs an empty retry wheel.
func NewRetryWheel() *RetryWheel {
	return &RetryWheel{}
}

// Schedule adds one retry entry in O(log n) time.
func (w *RetryWheel) Schedule(entry RetryEntry) {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.entries = append(w.entries, entry)
	retryWheelSiftUp(w.entries, len(w.entries)-1)
}

// PopDue removes and returns entries whose due time is not after now.
func (w *RetryWheel) PopDue(now time.Time) []RetryEntry {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.entries) == 0 {
		return nil
	}
	if w.entries[0].When.After(now) {
		return nil
	}
	out := make([]RetryEntry, 0)
	for len(w.entries) > 0 && !w.entries[0].When.After(now) {
		out = append(out, w.entries[0])
		last := len(w.entries) - 1
		w.entries[0] = w.entries[last]
		w.entries[last] = RetryEntry{}
		w.entries = w.entries[:last]
		retryWheelSiftDown(w.entries, 0)
	}
	return out
}

func retryWheelSiftUp(entries []RetryEntry, idx int) {
	for idx > 0 {
		parent := (idx - 1) / 2
		if !entries[idx].When.Before(entries[parent].When) {
			return
		}
		entries[idx], entries[parent] = entries[parent], entries[idx]
		idx = parent
	}
}

func retryWheelSiftDown(entries []RetryEntry, idx int) {
	for {
		left := 2*idx + 1
		if left >= len(entries) {
			return
		}
		smallest := left
		right := left + 1
		if right < len(entries) && entries[right].When.Before(entries[left].When) {
			smallest = right
		}
		if !entries[smallest].When.Before(entries[idx].When) {
			return
		}
		entries[idx], entries[smallest] = entries[smallest], entries[idx]
		idx = smallest
	}
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
