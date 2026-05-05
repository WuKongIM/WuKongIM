package core

import (
	"sync"
	"time"
)

// idleTracker tracks session read deadlines without scanning every session on every tick.
type idleTracker struct {
	mu      sync.Mutex
	timeout time.Duration
	heap    idleDeadlineHeap
	seq     uint64
}

// idleDeadline is one scheduled read deadline for a session state.
type idleDeadline struct {
	deadline time.Time
	seq      uint64
	state    *sessionState
}

func newIdleTracker(timeout time.Duration) *idleTracker {
	return &idleTracker{timeout: timeout}
}

// touch records a new read deadline and invalidates older deadlines for the state.
func (t *idleTracker) touch(state *sessionState, now time.Time) {
	if t == nil || state == nil || t.timeout <= 0 {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	t.seq++
	seq := t.seq
	state.idleSeq.Store(seq)
	t.heap.push(idleDeadline{
		deadline: now.Add(t.timeout),
		seq:      seq,
		state:    state,
	})
}

// remove invalidates all scheduled deadlines for the state.
func (t *idleTracker) remove(state *sessionState) {
	if t == nil || state == nil {
		return
	}
	state.idleSeq.Store(0)
}

// nextWait returns how long the monitor should wait before checking the next deadline.
func (t *idleTracker) nextWait(now time.Time) time.Duration {
	if t == nil || t.timeout <= 0 {
		return 0
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	t.pruneStaleLocked()
	if len(t.heap) == 0 {
		return t.timeout
	}
	wait := t.heap[0].deadline.Sub(now)
	if wait < 0 {
		return 0
	}
	return wait
}

// popExpired returns states whose latest read deadline has expired.
func (t *idleTracker) popExpired(now time.Time) []*sessionState {
	if t == nil {
		return nil
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	var expired []*sessionState
	for len(t.heap) > 0 {
		entry := t.heap[0]
		if !entry.deadline.After(now) {
			t.heap.pop()
			if t.entryIsStaleLocked(entry) {
				continue
			}
			expired = append(expired, entry.state)
			continue
		}
		if t.entryIsStaleLocked(entry) {
			t.heap.pop()
			continue
		}
		break
	}
	return expired
}

func (t *idleTracker) len() int {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.heap)
}

func (t *idleTracker) pruneStaleLocked() {
	for len(t.heap) > 0 && t.entryIsStaleLocked(t.heap[0]) {
		t.heap.pop()
	}
}

func (t *idleTracker) entryIsStaleLocked(entry idleDeadline) bool {
	return entry.state == nil || entry.state.idleSeq.Load() != entry.seq || entry.state.isClosed()
}

type idleDeadlineHeap []idleDeadline

func (h *idleDeadlineHeap) push(entry idleDeadline) {
	*h = append(*h, entry)
	h.up(len(*h) - 1)
}

func (h *idleDeadlineHeap) pop() idleDeadline {
	old := *h
	n := len(old)
	entry := old[0]
	last := old[n-1]
	old[n-1] = idleDeadline{}
	if n == 1 {
		*h = old[:0]
		return entry
	}
	old[0] = last
	*h = old[:n-1]
	h.down(0)
	return entry
}

func (h idleDeadlineHeap) up(child int) {
	for child > 0 {
		parent := (child - 1) / 2
		if !h[child].deadline.Before(h[parent].deadline) {
			return
		}
		h[child], h[parent] = h[parent], h[child]
		child = parent
	}
}

func (h idleDeadlineHeap) down(parent int) {
	n := len(h)
	for {
		left := parent*2 + 1
		if left >= n {
			return
		}
		smallest := left
		if right := left + 1; right < n && h[right].deadline.Before(h[left].deadline) {
			smallest = right
		}
		if !h[smallest].deadline.Before(h[parent].deadline) {
			return
		}
		h[parent], h[smallest] = h[smallest], h[parent]
		parent = smallest
	}
}
