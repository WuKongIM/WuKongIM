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
	indexes map[*sessionState]int
}

// idleDeadline is one scheduled read deadline for a session state.
type idleDeadline struct {
	deadline time.Time
	state    *sessionState
}

func newIdleTracker(timeout time.Duration) *idleTracker {
	return &idleTracker{timeout: timeout, indexes: make(map[*sessionState]int)}
}

// touch records a new read deadline by updating the state's existing heap entry.
func (t *idleTracker) touch(state *sessionState, now time.Time) {
	if t == nil || state == nil || t.timeout <= 0 {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	if t.indexes == nil {
		t.indexes = make(map[*sessionState]int)
	}
	deadline := now.Add(t.timeout)
	if idx, ok := t.indexes[state]; ok && idx >= 0 && idx < len(t.heap) && t.heap[idx].state == state {
		t.heap[idx].deadline = deadline
		t.heap.fix(idx, t.indexes)
		return
	}
	t.heap.push(idleDeadline{
		deadline: deadline,
		state:    state,
	}, t.indexes)
}

// remove deletes the tracked deadline for the state.
func (t *idleTracker) remove(state *sessionState) {
	if t == nil || state == nil {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	if idx, ok := t.indexes[state]; ok {
		t.heap.removeAt(idx, t.indexes)
	}
}

func (t *idleTracker) removeRootLocked() idleDeadline {
	return t.heap.removeAt(0, t.indexes)
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
		if t.entryIsStaleLocked(entry) {
			t.removeRootLocked()
			continue
		}
		if !entry.deadline.After(now) {
			t.removeRootLocked()
			expired = append(expired, entry.state)
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
		t.removeRootLocked()
	}
}

func (t *idleTracker) entryIsStaleLocked(entry idleDeadline) bool {
	if entry.state == nil || entry.state.isClosed() {
		return true
	}
	idx, ok := t.indexes[entry.state]
	return !ok || idx < 0 || idx >= len(t.heap) || t.heap[idx].state != entry.state || !t.heap[idx].deadline.Equal(entry.deadline)
}

type idleDeadlineHeap []idleDeadline

func (h *idleDeadlineHeap) push(entry idleDeadline, indexes map[*sessionState]int) {
	*h = append(*h, entry)
	idx := len(*h) - 1
	if entry.state != nil {
		indexes[entry.state] = idx
	}
	h.up(idx, indexes)
}

func (h *idleDeadlineHeap) removeAt(index int, indexes map[*sessionState]int) idleDeadline {
	old := *h
	n := len(old)
	if index < 0 || index >= n {
		return idleDeadline{}
	}
	entry := old[index]
	last := old[n-1]
	old[n-1] = idleDeadline{}
	if entry.state != nil {
		if current, ok := indexes[entry.state]; ok && current == index {
			delete(indexes, entry.state)
		}
	}
	if index == n-1 {
		*h = old[:n-1]
		return entry
	}
	old[index] = last
	*h = old[:n-1]
	if last.state != nil {
		indexes[last.state] = index
	}
	h.fix(index, indexes)
	return entry
}

func (h idleDeadlineHeap) fix(index int, indexes map[*sessionState]int) {
	if index < 0 || index >= len(h) {
		return
	}
	if !h.down(index, indexes) {
		h.up(index, indexes)
	}
}

func (h idleDeadlineHeap) up(child int, indexes map[*sessionState]int) {
	for child > 0 {
		parent := (child - 1) / 2
		if !h[child].deadline.Before(h[parent].deadline) {
			return
		}
		h.swap(child, parent, indexes)
		child = parent
	}
}

func (h idleDeadlineHeap) down(parent int, indexes map[*sessionState]int) bool {
	n := len(h)
	moved := false
	for {
		left := parent*2 + 1
		if left >= n {
			return moved
		}
		smallest := left
		if right := left + 1; right < n && h[right].deadline.Before(h[left].deadline) {
			smallest = right
		}
		if !h[smallest].deadline.Before(h[parent].deadline) {
			return moved
		}
		h.swap(parent, smallest, indexes)
		parent = smallest
		moved = true
	}
}

func (h idleDeadlineHeap) swap(i, j int, indexes map[*sessionState]int) {
	h[i], h[j] = h[j], h[i]
	if h[i].state != nil {
		indexes[h[i].state] = i
	}
	if h[j].state != nil {
		indexes[h[j].state] = j
	}
}
