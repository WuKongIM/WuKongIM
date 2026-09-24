package mqttsession

import (
	"context"
	"errors"
	"slices"
	"sort"
	"sync"
	"time"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/mqttsession"
	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/db/meta"
	gr "github.com/WuKongIM/WuKongIM/pkg/goroutine"
)

var (
	ErrReplayWorkerInvalid  = errors.New("mqttsession: invalid replay worker configuration")
	ErrReplayWorkerStopping = errors.New("mqttsession: replay worker still draining")
)

// ReplaySource discovers current locally led hash Slots and authoritative source
// pages. Ownership hints alone do not authorize content effects or source release.
type ReplaySource interface {
	LocalLeaderHashSlots(context.Context) ([]meta.HashSlot, error)
	ReadMQTTRecovery(context.Context, uint16, meta.MQTTRead) (meta.MQTTReadResult, error)
}

// ReplayStepper rereads authority and durable progress before one bounded turn.
// It owns no worker, preserves the input cursor and joins call-local work;
// admitted durable commits remain owned by the cluster after a lost reply.
type ReplayStepper interface {
	Step(context.Context, meta.MQTTBindingOwner, contract.ReplayCursor) (contract.ReplayStepResult, error)
}

// ReplayObservation contains only aggregate work counts, never source identities.
type ReplayObservation struct {
	Pages, Visited, Attempts, Failures           int
	Anchored, Repaired, Completed, Continuations int
	// RetirementCommits includes idempotent decisions, not completed replica GC.
	RetirementCommits int
	Duration          time.Duration
}

type ReplayWorkerOptions struct {
	Source  ReplaySource
	Stepper ReplayStepper
	// Registry owns one optional singleton per node, with no per-source task labels.
	Registry *gr.Registry
	// HashSlotCount must match the deployment; zero defaults to 256.
	HashSlotCount uint16
	// Interval defaults to 200ms; allowed values are 10ms through one minute.
	Interval time.Duration
	// TurnTimeout bounds a whole sweep; default 10s, maximum one minute.
	TurnTimeout time.Duration
	// ReadTimeout defaults to 250ms; StepTimeout defaults to 5s. Both must fit
	// TurnTimeout. Cancellation never detaches an unfinished dependency call.
	ReadTimeout, StepTimeout time.Duration
	// PagesPerTurn limits selected Slots (default 8, maximum 32); each is read once.
	PagesPerTurn int
	// PageSize bounds distinct source discovery (default 16, maximum 64).
	PageSize int
	// MaxVisitsPerTurn bounds attempted sources (default 64, maximum 256).
	MaxVisitsPerTurn int
	// Observe is synchronous and must neither block nor call Stop on this worker.
	Observe func(ReplayObservation)
}

// ReplayWorker owns one joined loop and at most one continuation per hash Slot.
// Source cardinality cannot grow a process map, queue or goroutine cohort.
type ReplayWorker struct {
	opts   ReplayWorkerOptions
	mu     sync.Mutex
	run    context.Context
	cancel context.CancelFunc
	done   chan struct{}
}

type replaySlotScan struct {
	after meta.MQTTBindingOwner
	pass  uint64
	// pending pins only a successful, advancing scan to a finite committed target.
	pending meta.MQTTBindingOwner
	cursor  contract.ReplayCursor
}
type replayScanState struct {
	next  uint32
	slots map[uint16]*replaySlotScan
}

func NewReplayWorker(o ReplayWorkerOptions) (*ReplayWorker, error) {
	if o.HashSlotCount == 0 {
		o.HashSlotCount = 256
	}
	if o.Interval == 0 {
		o.Interval = 200 * time.Millisecond
	}
	if o.TurnTimeout == 0 {
		o.TurnTimeout = 10 * time.Second
	}
	if o.ReadTimeout == 0 {
		o.ReadTimeout = 250 * time.Millisecond
	}
	if o.StepTimeout == 0 {
		o.StepTimeout = 5 * time.Second
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
	if o.Source == nil || o.Stepper == nil || o.Interval < 10*time.Millisecond || o.Interval > time.Minute || o.TurnTimeout <= 0 || o.TurnTimeout > time.Minute || o.ReadTimeout <= 0 || o.ReadTimeout > o.TurnTimeout || o.StepTimeout <= 0 || o.StepTimeout > o.TurnTimeout || o.PagesPerTurn < 1 || o.PagesPerTurn > 32 || o.PageSize < 1 || o.PageSize > 64 || o.MaxVisitsPerTurn < 1 || o.MaxVisitsPerTurn > 256 {
		return nil, ErrReplayWorkerInvalid
	}
	return &ReplayWorker{opts: o}, nil
}

// Start validates its caller, then owns an independent lifetime until joined Stop.
// A cancelled run cannot overlap a restart, even when a dependency drains late.
func (w *ReplayWorker) Start(ctx context.Context) error {
	if w == nil || ctx == nil {
		return ErrReplayWorkerInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.cancel != nil {
		if w.run.Err() != nil {
			return ErrReplayWorkerStopping
		}
		return nil
	}
	run, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	w.run, w.cancel, w.done = run, cancel, done
	gr.SafeGo(w.opts.Registry, gr.TaskMQTTReplayWorker, func() { defer close(done); w.loop(run) })
	return nil
}

// Stop cancels and joins this exact run before dependencies may stop or restore.
// Timeout retains ownership; a later Stop finishes the same join.
func (w *ReplayWorker) Stop(ctx context.Context) error {
	if w == nil || ctx == nil {
		return ErrReplayWorkerInvalid
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

func (w *ReplayWorker) loop(ctx context.Context) {
	ticker := time.NewTicker(w.opts.Interval)
	defer ticker.Stop()
	var state replayScanState
	for ctx.Err() == nil {
		out := w.sweep(ctx, &state)
		if w.opts.Observe != nil {
			w.opts.Observe(out)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// sweep rotates Slots independently of the source scan. An unavailable source
// yields, while a successful finite journal scan retains exactly one continuation.
func (w *ReplayWorker) sweep(parent context.Context, state *replayScanState) (out ReplayObservation) {
	started := time.Now()
	defer func() { out.Duration = time.Since(started) }()
	if parent.Err() != nil {
		return out
	}
	ctx, cancel := context.WithTimeout(parent, w.opts.TurnTimeout)
	defer cancel()
	call, done := context.WithTimeout(ctx, w.opts.ReadTimeout)
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
	for slot := range state.slots {
		if !led[slot] {
			delete(state.slots, slot)
		}
	}
	if len(slots) == 0 {
		state.next = 0
		return out
	}
	if state.slots == nil {
		state.slots = make(map[uint16]*replaySlotScan)
	}
	index := sort.Search(len(slots), func(i int) bool { return uint32(slots[i]) >= state.next })
	if index == len(slots) {
		index = 0
	}
	for page := 0; page < min(w.opts.PagesPerTurn, len(slots)) && ctx.Err() == nil && out.Visited < w.opts.MaxVisitsPerTurn; page++ {
		slot := uint16(slots[index])
		state.next = uint32(slot) + 1
		index = (index + 1) % len(slots)
		scan := state.slots[slot]
		if scan == nil {
			scan = &replaySlotScan{}
			state.slots[slot] = scan
		}
		q := meta.MQTTRead{Kind: meta.MQTTReadReplaySources, Limit: w.opts.PageSize, After: meta.MQTTReadCursor{SourceOwner: scan.after}}
		out.Pages++
		call, done = context.WithTimeout(ctx, w.opts.ReadTimeout)
		r, e := w.opts.Source.ReadMQTTRecovery(call, slot, q)
		if e == nil {
			e = call.Err()
		}
		done()
		if e != nil || !validReplaySourcePage(q, r) {
			out.Failures++
			continue
		}
		if scan.pending != (meta.MQTTBindingOwner{}) && (len(r.SourceOwners) == 0 || r.SourceOwners[0] != scan.pending) {
			scan.pending = meta.MQTTBindingOwner{}
			scan.cursor = contract.ReplayCursor{}
		}
		complete := true
		for _, source := range r.SourceOwners {
			if ctx.Err() != nil || out.Visited >= w.opts.MaxVisitsPerTurn {
				complete = false
				break
			}
			cursor := contract.ReplayCursor{Pass: scan.pass}
			if scan.pending == source {
				cursor = scan.cursor
			}
			out.Visited++
			out.Attempts++
			call, done = context.WithTimeout(ctx, w.opts.StepTimeout)
			step, e := w.opts.Stepper.Step(call, source, cursor)
			if e == nil {
				e = call.Err()
			}
			done()
			if e != nil {
				out.Failures++
			} else {
				if step.ContinueScan || step.ContinueRetirement {
					next, ok := continueReplayScan(source, cursor, step)
					if step.ContinueRetirement {
						next, ok = continueRetirementScan(source, cursor, step)
					}
					if ok {
						scan.pending, scan.cursor = source, next
						out.Continuations++
						complete = false
						break
					}
					out.Failures++
				} else {
					if step.Anchored {
						out.Anchored++
					}
					if step.Repaired {
						out.Repaired++
					}
					if step.TargetComplete {
						out.Completed++
					}
					if step.RetirementCommitted {
						out.RetirementCommits++
					}
				}
			}
			// Successful durable work, failed donor rounds and errors all yield. The
			// next pass rotates cold target/donor hints and replans durable progress.
			scan.after = source
			scan.pending = meta.MQTTBindingOwner{}
			scan.cursor = contract.ReplayCursor{}
		}
		if complete && r.Done {
			scan.after = meta.MQTTBindingOwner{}
			scan.pass++
		}
	}
	return out
}

func validReplaySourcePage(q meta.MQTTRead, r meta.MQTTReadResult) bool {
	if meta.ValidateMQTTRead(q) != nil || r.Session != nil || len(r.Sessions)+len(r.Subscriptions)+len(r.DeliveryCursors)+len(r.Inflight)+len(r.Bindings)+len(r.Wills) != 0 || len(r.SourceOwners) > q.Limit || (!r.Done && len(r.SourceOwners) != q.Limit) {
		return false
	}
	check := q
	check.After = r.After
	if meta.ValidateMQTTRead(check) != nil {
		return false
	}
	previous := q.After.SourceOwner
	for _, source := range r.SourceOwners {
		probe := meta.MQTTRead{Kind: meta.MQTTReadReplaySources, Limit: 1, After: meta.MQTTReadCursor{SourceOwner: source}}
		if source == (meta.MQTTBindingOwner{}) || meta.ValidateMQTTRead(probe) != nil || (previous != (meta.MQTTBindingOwner{}) && meta.CompareMQTTBindingOwners(previous, source) >= 0) {
			return false
		}
		previous = source
	}
	return r.After.SourceOwner == previous
}

// continueReplayScan selects only the last attempted replica. Its target cannot
// move and its scan must advance; changed placement ends the visit instead.
func continueReplayScan(source meta.MQTTBindingOwner, previous contract.ReplayCursor, result contract.ReplayStepResult) (contract.ReplayCursor, bool) {
	next := result.Next
	if !result.ContinueScan || result.ContinueRetirement || result.RetirementCommitted || next.Retirement != (contract.ReplayRetirementCursor{}) || previous.Retirement != (contract.ReplayRetirementCursor{}) || result.Anchored || result.Repaired || result.TargetComplete || next.Source != source || next.Pass != previous.Pass || next.Authority == [32]byte{} || len(next.Targets) == 0 || len(next.Targets) > 256 || next.NextTarget < 0 || next.NextTarget >= len(next.Targets) {
		return contract.ReplayCursor{}, false
	}
	at := (next.NextTarget + len(next.Targets) - 1) % len(next.Targets)
	target := next.Targets[at]
	if target.NodeID == 0 || target.NodeID != result.Target || target.AnchorPosition == 0 || target.AfterAnchor == 0 || target.AfterAnchor >= target.AnchorPosition || target.DonorAfter != 0 {
		return contract.ReplayCursor{}, false
	}
	if previous.Authority != [32]byte{} {
		if previous.Authority != next.Authority || previous.NextTarget < 0 || previous.NextTarget >= len(previous.Targets) {
			return contract.ReplayCursor{}, false
		}
		old := previous.Targets[previous.NextTarget]
		if old.NodeID != target.NodeID || old.AnchorPosition != target.AnchorPosition || old.AfterAnchor >= target.AfterAnchor {
			return contract.ReplayCursor{}, false
		}
	}
	next.Targets = slices.Clone(next.Targets)
	next.RepairNext = true
	next.NextTarget = at
	return next, true
}

// continueRetirementScan retains only a strictly decreasing finite journal scan.
// Proofs are immutable scheduling bounds; the next usecase turn rereads permission.
func continueRetirementScan(source meta.MQTTBindingOwner, previous contract.ReplayCursor, result contract.ReplayStepResult) (contract.ReplayCursor, bool) {
	next, zero := result.Next, contract.ReplayCursor{}
	r := next.Retirement
	if !result.ContinueRetirement || result.ContinueScan || result.RetirementCommitted || result.Anchored || result.Repaired || result.TargetComplete || result.Target != 0 || next.Source != source || next.Pass != previous.Pass || next.Authority == [32]byte{} || len(next.Targets) != 0 || next.NextTarget != 0 || next.RepairNext || r.Source != source || r.Authority != next.Authority {
		return zero, false
	}
	q := ch.MQTTReplayRetirementScan{Generation: source.Generation, CapturedAnchor: r.Captured.Manifest.LastOffset, Through: r.Through, Limit: 1}
	page := ch.MQTTReplayRetirementSelection{Captured: r.Captured, BeforeAnchor: r.BeforeAnchor}
	if !page.ValidFor(q) {
		return zero, false
	}
	old := previous.Retirement
	if old == (contract.ReplayRetirementCursor{}) {
		if previous.Source != (meta.MQTTBindingOwner{}) || previous.Authority != [32]byte{} || len(previous.Targets) != 0 || previous.NextTarget != 0 || previous.RepairNext {
			return zero, false
		}
	} else if previous.Source != source || previous.Authority != next.Authority || old.Source != r.Source || old.Authority != r.Authority || old.Captured != r.Captured || old.Through != r.Through || old.BeforeAnchor <= r.BeforeAnchor || len(previous.Targets) != 0 || previous.NextTarget != 0 || previous.RepairNext {
		return zero, false
	}
	return next, true
}
