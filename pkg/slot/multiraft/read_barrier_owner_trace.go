package multiraft

import "time"

// Owner stages describe time inside existing synchronization/admission calls,
// not a claim that a goroutine parked. Children partition only the budget scope.
type ownerStorageStage uint8

const (
	ownerStageNone ownerStorageStage = iota
	ownerStageDrain
	ownerStagePermit
	ownerStageWork
)
const (
	ownerOtherOversize uint8 = 1 << iota
	ownerOtherSnapshot
	ownerOtherConfig
	ownerOtherCompaction
	ownerOtherCapture
	ownerOtherEmbedding
)

// ownerStorageToken is passed explicitly down an owner-only synchronous call
// chain. Async storage never receives it. Fields and Slot registration use g.mu;
// only the owner changes stages, while callers may register/cancel observations.
type ownerStorageToken struct {
	g       *slot
	budget  bool
	mask    uint8
	started time.Time
	stage   ownerStorageStage
	stageAt time.Time
}

// beginOwnerStorage returns before any clock, lock or allocation when disabled.
// There is at most one synchronous scope on the unique Slot owner.
func (g *slot) beginOwnerStorage(reject, other uint8) *ownerStorageToken {
	if !g.readBarrierTracing {
		return nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	at := time.Now()
	token := &ownerStorageToken{g: g, budget: reject != 0, mask: other, started: at}
	if reject != 0 {
		token.mask = reject
	}
	g.readOwnerStorage = token
	for _, o := range g.readTimings {
		if o != nil {
			o.openOwner(token.budget, token.mask, at, ownerStageNone, time.Time{})
		}
	}
	return token
}
func (t *ownerStorageToken) setStage(stage ownerStorageStage) ownerStorageStage {
	if t == nil {
		return ownerStageNone
	}
	g := t.g
	g.mu.Lock()
	defer g.mu.Unlock()
	old := t.stage
	if g.readOwnerStorage != t || !t.budget || old == stage {
		return old
	}
	at := time.Now()
	t.stage = stage
	t.stageAt = at
	for _, o := range g.readTimings {
		if o != nil {
			o.switchOwnerStage(at, stage)
		}
	}
	return old
}
func (t *ownerStorageToken) end() {
	if t == nil {
		return
	}
	g := t.g
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.readOwnerStorage != t {
		return
	}
	at := time.Now()
	for _, o := range g.readTimings {
		if o != nil {
			o.closeOwner(at, false)
		}
	}
	g.readOwnerStorage = nil
}

func (o *readBarrierTiming) openOwner(budget bool, mask uint8, at time.Time, stage ownerStorageStage, stageAt time.Time) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.terminal.IsZero() {
		return
	}
	if !o.ownerOpen.IsZero() {
		o.coverageInvalid = true
		return
	}
	if at.Before(o.points[0]) {
		at = o.points[0]
	}
	o.ownerOpen = at
	o.ownerBudget = budget
	o.ownerMask = mask
	o.ownerStage = stage
	if stage != ownerStageNone {
		if stageAt.Before(at) {
			stageAt = at
		}
		o.ownerStageAt = stageAt
	}
}
func (o *readBarrierTiming) switchOwnerStage(at time.Time, stage ownerStorageStage) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.terminal.IsZero() || o.ownerOpen.IsZero() || !o.ownerBudget {
		return
	}
	o.accumulateOwnerChild(at)
	o.ownerStage = stage
	o.ownerStageAt = at
}
func (o *readBarrierTiming) closeOwner(at time.Time, unfinished bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.terminal.IsZero() {
		return
	}
	o.closeOwnerLocked(at, unfinished)
}

// closeOwnerLocked clips scope and active child at the same caller terminal.
// Fixed five-phase storage is independent of detailed overlay retention.
func (o *readBarrierTiming) closeOwnerLocked(end time.Time, unfinished bool) {
	start := o.ownerOpen
	if start.IsZero() {
		return
	}
	o.accumulateOwnerChild(end)
	o.ownerOpen = time.Time{}
	o.ownerIntersections(start, end, func(i int, d time.Duration) {
		p := &o.ownerCoverage.Phases[i]
		if o.ownerBudget {
			p.Budget.Duration += d
			p.Budget.Count++
			p.Budget.Unfinished = p.Budget.Unfinished || unfinished
			p.Budget.RejectMask |= o.ownerMask
		} else {
			p.Other.Duration += d
			p.Other.Count++
			p.Other.Unfinished = p.Other.Unfinished || unfinished
			p.Other.CauseMask |= o.ownerMask
		}
	})
}
func (o *readBarrierTiming) accumulateOwnerChild(end time.Time) {
	start, stage := o.ownerStageAt, o.ownerStage
	o.ownerStageAt = time.Time{}
	o.ownerStage = ownerStageNone
	if start.IsZero() || stage == ownerStageNone || !o.ownerBudget {
		return
	}
	o.ownerIntersections(start, end, func(i int, d time.Duration) {
		p := &o.ownerCoverage.Phases[i].Budget
		switch stage {
		case ownerStageDrain:
			p.DrainWait += d
		case ownerStagePermit:
			p.SavePermit += d
		case ownerStageWork:
			p.SyncWork += d
		}
	})
}
func (o *readBarrierTiming) ownerIntersections(start, end time.Time, add func(int, time.Duration)) {
	if end.Before(start) {
		o.coverageInvalid = true
		return
	}
	for i := range o.ownerCoverage.Phases {
		if o.points[i].IsZero() {
			break
		}
		a, b := start, o.points[i+1]
		if o.points[i].After(a) {
			a = o.points[i]
		}
		if b.IsZero() || b.After(end) {
			b = end
		}
		if b.After(a) {
			add(i, b.Sub(a))
		}
		if o.points[i+1].IsZero() {
			break
		}
	}
}
