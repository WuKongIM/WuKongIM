package mqttsession

import (
	"container/heap"
	"context"
	"time"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/mqttsession"
)

type ownerDeadlines []*ownerEntry

func (h ownerDeadlines) Len() int { return len(h) }
func (h ownerDeadlines) Less(i, j int) bool {
	if h[i].deadline.Equal(h[j].deadline) {
		return h[i].owner.ConnectionID < h[j].owner.ConnectionID
	}
	return h[i].deadline.Before(h[j].deadline)
}
func (h ownerDeadlines) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].heapIndex = i
	h[j].heapIndex = j
}
func (h *ownerDeadlines) Push(v any) { e := v.(*ownerEntry); e.heapIndex = len(*h); *h = append(*h, e) }
func (h *ownerDeadlines) Pop() any {
	a := *h
	e := a[len(a)-1]
	a[len(a)-1] = nil
	*h = a[:len(a)-1]
	e.heapIndex = -1
	return e
}

func (m *Owners) scheduleLocked(e *ownerEntry, at time.Time) {
	e.deadline = at
	if e.heapIndex < 0 {
		heap.Push(&m.deadlines, e)
	} else {
		heap.Fix(&m.deadlines, e.heapIndex)
	}
}

// Sweep fences and closes at most limit due owners. Renewal, selection and
// fencing share the same lock, so an old deadline cannot close a renewed owner.
// A failed/draining close remains indexed for a later bounded retry.
func (m *Owners) Sweep(ctx context.Context, limit int) (int, error) {
	if m == nil || ctx == nil || limit < 1 || limit > 256 {
		return 0, ErrOwnerInvalid
	}
	visited := 0
	for visited < limit {
		if err := ctx.Err(); err != nil {
			return visited, err
		}
		m.mu.Lock()
		now := m.opts.Now()
		if len(m.deadlines) == 0 || m.deadlines[0].deadline.After(now) {
			m.mu.Unlock()
			break
		}
		e := m.deadlines[0]
		m.fenceLocked(e)
		// A closing entry still needs its next attempt moved forward; otherwise
		// a caller timeout could monopolize every future page at the heap head.
		m.scheduleLocked(e, now.Add(m.opts.CloseRetry))
		owner := e.owner
		m.mu.Unlock()
		visited++
		if err := m.Quiesce(ctx, owner); err != nil {
			return visited, err
		}
	}
	return visited, nil
}

// Close rejects new owners/operations and cancels all scopes before bounded
// transport cleanup. It can be retried after timeout; retained work is never
// erased to manufacture a successful shutdown.
func (m *Owners) Close(ctx context.Context) error {
	if m == nil || ctx == nil {
		return ErrOwnerInvalid
	}
	m.mu.Lock()
	m.stopped = true
	m.mu.Unlock()
	m.cancel(ErrOwnerStopped)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		m.mu.Lock()
		page := make([]contract.Owner, 0, min(256, len(m.entries)))
		for _, e := range m.entries {
			page = append(page, e.owner)
			if len(page) == 256 {
				break
			}
		}
		m.mu.Unlock()
		if len(page) == 0 {
			return nil
		}
		for _, owner := range page {
			if err := m.Quiesce(ctx, owner); err != nil {
				return err
			}
		}
	}
}

// OwnerSnapshot contains bounded aggregate diagnostics without owner identities.
type OwnerSnapshot struct {
	Held, Pending, Active, Closing, Operations, Deadlines int
	Stopped                                               bool
}

// Snapshot is constant-time and never scans identities under the registry lock.
func (m *Owners) Snapshot() OwnerSnapshot {
	if m == nil {
		return OwnerSnapshot{Stopped: true}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := m.counts
	out.Held, out.Deadlines, out.Stopped = len(m.entries), len(m.deadlines), m.stopped
	return out
}
