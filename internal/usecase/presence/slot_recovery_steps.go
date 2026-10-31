package presence

import (
	"context"
	"time"

	authority "github.com/WuKongIM/WuKongIM/internal/runtime/presence"
	"github.com/WuKongIM/WuKongIM/pkg/goroutine"
	"github.com/WuKongIM/WuKongIM/pkg/observability/rpctrace"
)

// A cooperative turn bounds useful work without waiting for a batch to fill.
// Pages have a hard count cut; rows/bytes may cross their cut on the final
// already-received page. These are fairness cuts, not new payload/RSS limits.
const (
	slotTurnMaxPages = 4
	slotTurnMaxRows  = 4096
	slotTurnMaxBytes = uint64(4 << 20)
)

// A turn ends at its work cut, including terminal End for exhausted pages.
// Paused scan goroutines own prepaid staging, not an active execution slot.
// Their count is bounded by admitted keys and the shared staging byte ledger.
type slotTurnResult struct {
	pending bool
	rows    int
	err     error
}
type slotRecoveryGroup struct {
	group                    EndpointLookupGroup
	flight                   *slotRecoveryFlight
	live, attachedLive, done bool
	rows                     int
	err                      error
}
type slotRecoveryCall struct {
	groups []slotRecoveryGroup
	next   int
}
type slotRecoveryTurn struct {
	call     *slotRecoveryCall
	index    int
	flight   *slotRecoveryFlight
	finished bool
}

func newSlotRecoveryCall(groups []EndpointLookupGroup) *slotRecoveryCall {
	c := &slotRecoveryCall{groups: make([]slotRecoveryGroup, len(groups))}
	for i, g := range groups {
		c.groups[i] = slotRecoveryGroup{group: g, live: true}
	}
	return c
}
func (c *slotRecoveryCall) owned() bool {
	for _, g := range c.groups {
		if g.flight != nil {
			return true
		}
	}
	return false
}
func (c *slotRecoveryCall) complete() bool {
	for _, g := range c.groups {
		if !g.done {
			return false
		}
	}
	return true
}

// All scheduler calls acquire AuthorityRecovery.mu before SlotRecovery.mu.
// Scan/provider code never holds SlotRecovery.mu while entering the scheduler.
func (r *SlotRecovery) setSlotGroupLive(c *slotRecoveryCall, index int, live bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	g := &c.groups[index]
	g.live = live
	if !live && g.attachedLive {
		g.attachedLive = false
		g.flight.users--
		if g.flight.users == 0 {
			g.flight.cancel()
		}
	}
}

func (r *SlotRecovery) reserveSlotTurn(c *slotRecoveryCall) (*slotRecoveryTurn, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for offset := 0; offset < len(c.groups); offset++ {
		i := (c.next + offset) % len(c.groups)
		g := &c.groups[i]
		if g.done {
			continue
		}
		if r.closed {
			g.live = false
			if g.attachedLive {
				g.attachedLive = false
				g.flight.users--
				if g.flight.users == 0 {
					g.flight.cancel()
				}
			}
		}
		if g.flight == nil {
			if !g.live {
				g.done = true
				g.err = authority.ErrRouteNotReady
				continue
			}
			key := generationKey(g.group.Target)
			f := r.flights[key]
			if f == nil {
				if len(r.flights) >= recoveryMaxKeys {
					g.done = true
					g.err = authority.ErrRouteNotReady
					continue
				}
				physical, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				f = &slotRecoveryFlight{target: g.group.Target, ctx: physical, cancel: cancel, done: make(chan struct{}), cooperative: true, permit: make(chan struct{}, 1), turn: make(chan slotTurnResult, 1)}
				r.flights[key] = f
				goroutine.SafeGo(nil, goroutine.TaskPresenceRecoveryScheduler, func() { r.run(key, f) })
			}
			g.flight = f
			g.attachedLive = true
			f.users++
		}
		f := g.flight
		// A canceled follower need not consume the surviving owner's next page.
		if !g.live && (f.finished || f.users > 0) {
			g.done = true
			g.err = authority.ErrRouteNotReady
			continue
		}
		if f.busy {
			continue
		}
		f.busy = true
		c.next = (i + 1) % len(c.groups)
		return &slotRecoveryTurn{call: c, index: i, flight: f, finished: f.finished}, true
	}
	return nil, false
}

func (r *SlotRecovery) advanceSlotTurn(ctx context.Context, t *slotRecoveryTurn) slotTurnResult {
	f := t.flight
	result := slotTurnResult{}
	if t.finished {
		result.err = f.err
	} else {
		f.permit <- struct{}{}
		result = <-f.turn
	}
	if result.pending || result.err != nil {
		return result
	}
	if err := ctx.Err(); err != nil {
		return slotTurnResult{err: err}
	}
	g := t.call.groups[t.index].group
	claim, err := r.current(g.Target)
	if err != nil {
		return slotTurnResult{err: err}
	}
	missing, err := r.directory.RecoveryUIDs(g.Target, g.UIDs)
	if err != nil {
		return slotTurnResult{err: err}
	}
	if len(missing) > 0 {
		if err = r.reconcile(ctx, claim, missing); err != nil {
			return slotTurnResult{err: err}
		}
	}
	rows, err := r.directory.CountGenerationRoutes(claim, g.UIDs)
	return slotTurnResult{rows: rows, err: err}
}

func (r *SlotRecovery) finishSlotTurn(t *slotRecoveryTurn, result slotTurnResult) {
	r.mu.Lock()
	defer r.mu.Unlock()
	t.flight.busy = false
	if !result.pending {
		g := &t.call.groups[t.index]
		g.done = true
		g.rows = result.rows
		g.err = result.err
	}
}

func (r *SlotRecovery) releaseSlotCall(c *slotRecoveryCall) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range c.groups {
		g := &c.groups[i]
		if g.attachedLive {
			g.flight.users--
			if g.flight.users == 0 {
				g.flight.cancel()
			}
		}
		*g = slotRecoveryGroup{}
	}
	c.groups = nil
}

func markRecoveryStarted(w *recoveryWork) {
	select {
	case <-w.started:
	default:
		close(w.started)
	}
}

func (r *AuthorityRecovery) updateSlotCallLiveLocked(w *recoveryWork, states map[*recoveryMember]bool) {
	if w.slotCall == nil {
		return
	}
	for i, g := range w.groups {
		live := false
		if !r.closed {
			if w.preparation != nil {
				live = recoveryMemberLive(w.creator, states)
			}
			for _, uid := range g.UIDs {
				f := r.inFlight[recoveryKey(g.Target, uid)]
				if f == nil || f.work != w {
					continue
				}
				for m := range f.users {
					if recoveryMemberLive(m, states) {
						live = true
						break
					}
				}
				if live {
					break
				}
			}
		}
		r.fullSlots.setSlotGroupLive(w.slotCall, i, live)
	}
}

// dispatchSlotStepsLocked keeps the original FIFO and all caller/key metadata.
// Busy same-slot followers stay queued; they create no spinning executors.
// Cancellation cleanup uses the same sixteen physical execution permits.
func (r *AuthorityRecovery) dispatchSlotStepsLocked() {
	for len(r.queue) > 0 && len(r.active) < recoveryMaxCohorts {
		selected := -1
		var turn *slotRecoveryTurn
		for i := 0; i < len(r.queue); i++ {
			w := r.queue[i]
			if w.finished {
				continue
			}
			r.updateSlotCallLiveLocked(w, make(map[*recoveryMember]bool))
			var ready bool
			turn, ready = r.fullSlots.reserveSlotTurn(w.slotCall)
			if w.slotCall.complete() {
				r.finishSlotWorkLocked(w)
				i--
				continue
			}
			if ready {
				selected = i
				break
			}
		}
		if selected < 0 {
			return
		}
		w := r.queue[selected]
		copy(r.queue[selected:], r.queue[selected+1:])
		r.queue[len(r.queue)-1] = nil
		r.queue = r.queue[:len(r.queue)-1]
		ctx, cancel := context.WithCancel(context.Background())
		ctx = rpctrace.Copy(ctx, w.creator.ctx)
		e := &recoveryExecution{ctx: ctx, cancel: cancel, works: []*recoveryWork{w}, slotTurn: turn}
		w.execution = e
		markRecoveryStarted(w)
		r.active[e] = struct{}{}
		r.resultCalls++
		r.census.BatchesTotal++
		r.census.BatchCallsTotal++
		r.census.BatchCallsHighWater = max(r.census.BatchCallsHighWater, 1)
		goroutine.SafeGo(nil, goroutine.TaskPresenceRecoveryScheduler, func() { r.executeSlotStep(e) })
	}
}

func (r *AuthorityRecovery) executeSlotStep(e *recoveryExecution) {
	result := r.fullSlots.advanceSlotTurn(e.ctx, e.slotTurn)
	r.mu.Lock()
	r.fullSlots.finishSlotTurn(e.slotTurn, result)
	w := e.works[0]
	w.execution = nil
	e.works = nil
	e.slotTurn = nil
	e.cancel()
	delete(r.active, e)
	r.resultCalls--
	if w.slotCall.complete() {
		r.finishSlotWorkLocked(w)
	} else {
		r.queue = append(r.queue, w)
	}
	r.kickLocked()
	r.maybeDrainedLocked()
	r.mu.Unlock()
}

func (r *AuthorityRecovery) finishSlotWorkLocked(w *recoveryWork) {
	if w.preparation != nil {
		w.preparation.err = recoveryExecutionFailure(ActivationPhaseRecoveryProvider, w.slotCall.groups[0].err)
		r.finishWorkLocked(w, map[recoveryUIDKey]error{})
		return
	}
	outcomes := make(map[recoveryUIDKey]error, len(w.flights))
	total := 0
	for _, g := range w.slotCall.groups {
		err := recoveryExecutionFailure(ActivationPhaseRecoveryProvider, g.err)
		// The full snapshot is shared, but each original caller still owns its
		// 4096-route provider boundary. Final Directory/caller snapshots remain
		// outside the decoded provider ledger, just as in the legacy path.
		total += g.rows
		if err == nil && total > 4096 {
			err = WrapActivationPhase(ActivationPhaseRecoveryProvider, authority.ErrRouteNotReady)
		}
		for _, uid := range g.group.UIDs {
			outcomes[recoveryKey(g.group.Target, uid)] = err
		}
	}
	r.finishWorkLocked(w, outcomes)
}
