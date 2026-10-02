package mqttsession

import (
	"context"
	"errors"
	"slices"
	"sort"
	"sync"
	"time"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/mqttsession"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	gr "github.com/WuKongIM/WuKongIM/pkg/goroutine"
)

var (
	ErrDeadlineWorkerInvalid  = errors.New("mqttsession: invalid deadline worker configuration")
	ErrDeadlineWorkerStopping = errors.New("mqttsession: deadline worker still draining")
	ErrDeadlineScanEvidence   = errors.New("mqttsession: invalid deadline scan evidence")
)

// DeadlineSource returns current locally led logical hash Slots and bounded
// authority-fenced recovery pages. The Slot list alone is never mutation proof.
type DeadlineSource interface {
	LocalLeaderHashSlots(context.Context) ([]meta.HashSlot, error)
	ReadMQTTRecovery(context.Context, uint16, meta.MQTTRead) (meta.MQTTReadResult, error)
}

// DeadlineReconciler rereads authority before at most one lifecycle decision.
// It must honor cancellation and join effects before returning.
type DeadlineReconciler interface {
	ReconcileDeadline(context.Context, contract.Owner) error
}

// DeadlineObservation contains only aggregate counts, never identities or errors.
type DeadlineObservation struct {
	Pages, Visited, Attempts, Failures int
	Duration                           time.Duration
}

type DeadlineWorkerOptions struct {
	// Source discovers current authority-owned candidates and bounded pages.
	Source DeadlineSource
	// Reconciler owns all lifecycle policy and exact-owner checks.
	Reconciler DeadlineReconciler
	// Registry tracks the one optional MQTT deadline task owned by this node.
	Registry *gr.Registry
	// HashSlotCount must match the deployment; zero defaults to 256.
	HashSlotCount uint16
	// Interval defaults to 200ms and must be between 10ms and one minute.
	Interval time.Duration
	// TurnTimeout bounds all pages and decisions in one nonoverlapping turn.
	// It defaults to two seconds and cannot exceed one minute.
	TurnTimeout time.Duration
	// ItemTimeout bounds each source or reconciliation call, defaults to 250ms,
	// and cannot exceed TurnTimeout. Cancellation never detaches an active call.
	ItemTimeout time.Duration
	// PagesPerTurn bounds alternating Session/Will index pages, default 8, max 32.
	PagesPerTurn int
	// PageSize bounds each authority read, default/max 16 (the Will RPC limit).
	PageSize int
	// MaxVisitsPerTurn bounds inspected candidates including detached Will work.
	// It defaults to 64 and cannot exceed 256.
	MaxVisitsPerTurn int
	// Now is used only to filter candidates, never to prove owner isolation.
	Now func() time.Time
	// Observe is synchronous and must not block or call Stop on this worker.
	Observe func(DeadlineObservation)
}

// DeadlineWorker owns one joined loop, no per-session timer, goroutine or queue.
type DeadlineWorker struct {
	opts   DeadlineWorkerOptions
	mu     sync.Mutex
	run    context.Context
	cancel context.CancelFunc
	done   chan struct{}
}

type deadlineScanState struct {
	// next orders the next (hash_slot, index_kind) pair across ownership changes.
	next    uint32
	cursors map[uint32]meta.MQTTReadCursor
}

func NewDeadlineWorker(o DeadlineWorkerOptions) (*DeadlineWorker, error) {
	if o.HashSlotCount == 0 {
		o.HashSlotCount = 256
	}
	if o.Interval == 0 {
		o.Interval = 200 * time.Millisecond
	}
	if o.TurnTimeout == 0 {
		o.TurnTimeout = 2 * time.Second
	}
	if o.ItemTimeout == 0 {
		o.ItemTimeout = 250 * time.Millisecond
	}
	if o.PagesPerTurn == 0 {
		o.PagesPerTurn = 8
	}
	if o.PageSize == 0 {
		o.PageSize = 16
	}
	if o.MaxVisitsPerTurn == 0 {
		o.MaxVisitsPerTurn = 64
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Source == nil || o.Reconciler == nil || o.Interval < 10*time.Millisecond || o.Interval > time.Minute || o.TurnTimeout <= 0 || o.TurnTimeout > time.Minute || o.ItemTimeout <= 0 || o.ItemTimeout > o.TurnTimeout || o.PagesPerTurn < 1 || o.PagesPerTurn > 32 || o.PageSize < 1 || o.PageSize > 16 || o.MaxVisitsPerTurn < 1 || o.MaxVisitsPerTurn > 256 {
		return nil, ErrDeadlineWorkerInvalid
	}
	return &DeadlineWorker{opts: o}, nil
}

// Start validates its caller, then owns a separate lifetime until joined Stop.
// Repeated starts are idempotent; a draining run cannot overlap its replacement.
func (w *DeadlineWorker) Start(ctx context.Context) error {
	if w == nil || ctx == nil {
		return ErrDeadlineWorkerInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.cancel != nil {
		if w.run.Err() != nil {
			return ErrDeadlineWorkerStopping
		}
		return nil
	}
	run, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	w.run, w.cancel, w.done = run, cancel, done
	gr.SafeGo(w.opts.Registry, gr.TaskMQTTDeadlineWorker, func() { defer close(done); w.loop(run) })
	return nil
}

// Stop cancels and joins the exact active run. Timeout retains its ownership;
// a subsequent Stop can finish the join before dependencies close or restore.
func (w *DeadlineWorker) Stop(ctx context.Context) error {
	if w == nil || ctx == nil {
		return ErrDeadlineWorkerInvalid
	}
	w.mu.Lock()
	cancel, done := w.cancel, w.done
	w.mu.Unlock()
	if cancel == nil {
		return nil
	}
	cancel()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
	}
	w.mu.Lock()
	if w.done == done {
		w.run, w.cancel, w.done = nil, nil, nil
	}
	w.mu.Unlock()
	return nil
}

func (w *DeadlineWorker) loop(ctx context.Context) {
	ticker := time.NewTicker(w.opts.Interval)
	defer ticker.Stop()
	var state deadlineScanState
	for ctx.Err() == nil {
		o := w.sweep(ctx, &state)
		if w.opts.Observe != nil {
			w.opts.Observe(o)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// sweep visits each selected stream at most once, with no concurrent child work.
// Cursors are process hints; errors never delete the underlying durable task.
func (w *DeadlineWorker) sweep(parent context.Context, state *deadlineScanState) (out DeadlineObservation) {
	started := time.Now()
	defer func() { out.Duration = time.Since(started) }()
	if parent.Err() != nil {
		return out
	}
	ctx, cancel := context.WithTimeout(parent, w.opts.TurnTimeout)
	defer cancel()
	call, done := context.WithTimeout(ctx, w.opts.ItemTimeout)
	slots, err := w.opts.Source.LocalLeaderHashSlots(call)
	if err == nil {
		err = call.Err()
	}
	done()
	if err != nil || len(slots) > int(w.opts.HashSlotCount) {
		out.Failures++
		return out
	}
	slots = slices.Clone(slots)
	slices.Sort(slots)
	led := make(map[uint16]bool, len(slots))
	for _, s := range slots {
		if uint16(s) >= w.opts.HashSlotCount || led[uint16(s)] {
			out.Failures++
			return out
		}
		led[uint16(s)] = true
	}
	for id := range state.cursors {
		if !led[uint16(id/2)] {
			delete(state.cursors, id)
		}
	}
	if len(slots) == 0 {
		state.next = 0
		return out
	}
	if state.cursors == nil {
		state.cursors = make(map[uint32]meta.MQTTReadCursor)
	}
	first := sort.Search(len(slots), func(i int) bool { return uint32(slots[i])*2+1 >= state.next })
	index := 0
	if first < len(slots) {
		index = first * 2
		if state.next > uint32(slots[first])*2 {
			index++
		}
	}
	for page := 0; page < min(w.opts.PagesPerTurn, len(slots)*2) && ctx.Err() == nil && out.Visited < w.opts.MaxVisitsPerTurn; page++ {
		slot := uint16(slots[index/2])
		id := uint32(slot)*2 + uint32(index%2)
		state.next = id + 1 // Every attempted page yields its position, including failure.
		index = (index + 1) % (len(slots) * 2)
		kind := meta.MQTTReadSessionDeadlines
		if id%2 != 0 {
			kind = meta.MQTTReadWillRecovery
		}
		q := meta.MQTTRead{Kind: kind, Limit: w.opts.PageSize, After: state.cursors[id]}
		out.Pages++
		call, done = context.WithTimeout(ctx, w.opts.ItemTimeout)
		r, e := w.opts.Source.ReadMQTTRecovery(call, slot, q)
		if e == nil {
			e = call.Err()
		}
		done()
		if e != nil {
			out.Failures++
			continue
		}
		candidates, e := deadlineCandidates(q, r)
		if e != nil {
			out.Failures++
			continue
		}
		complete := true
		for _, c := range candidates {
			if ctx.Err() != nil || out.Visited >= w.opts.MaxVisitsPerTurn {
				complete = false
				break
			}
			now := w.opts.Now().UnixMilli()
			if now <= 0 {
				out.Failures++
				complete = false
				break
			}
			out.Visited++
			if c.at > now {
				delete(state.cursors, id)
				complete = false
				break
			}
			if c.reconcile {
				out.Attempts++
				call, done = context.WithTimeout(ctx, w.opts.ItemTimeout)
				e = w.opts.Reconciler.ReconcileDeadline(call, c.owner)
				if e == nil {
					e = call.Err()
				}
				done()
				if e != nil {
					out.Failures++
				}
			}
			state.cursors[id] = c.cursor
		}
		if complete && r.Done {
			delete(state.cursors, id)
		}
	}
	return out
}
