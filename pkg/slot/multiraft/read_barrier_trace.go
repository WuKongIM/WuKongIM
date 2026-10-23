package multiraft

import (
	"errors"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/observability/rpctrace"
	raft "go.etcd.io/raft/v3"
)

// The observer registry is independent of Raft admission. Overflow loses
// observation only; it cannot reject, retry or delay a read through a new queue.
const maxObservedReadBarriers = 32

// readBarrierTiming contains only bounded local-clock data. Slot code may
// update this data while holding mu, but never calls an arbitrary observer or
// the enclosing Trace under that lock. The returning caller publishes once.
type readBarrierTiming struct {
	mu            sync.Mutex
	points        [6]time.Time
	terminal      time.Time
	terminalErr   error
	overlays      [4]rpctrace.HistoricalSpan
	overlayErrors [4]error
	saves, sends  int
	dropped       uint64
	// Active operations and per-phase totals are independent of detail retention.
	open            [2]time.Time
	coverage        [rpctrace.BarrierPhaseCount][2]rpctrace.BarrierOverlayCoverage
	coverageInvalid bool
}

func (o *readBarrierTiming) mark(phase int, at time.Time) {
	if o == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.terminal.IsZero() && o.points[phase].IsZero() {
		o.points[phase] = at
	}
}

func (o *readBarrierTiming) finish(err error) {
	if o == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.terminal.IsZero() {
		return
	}
	o.terminal = time.Now()
	o.terminalErr = err
	if err == nil && o.points[5].IsZero() {
		o.points[5] = o.terminal
	}
	for kind := range o.open {
		o.accumulateOverlay(kind, o.terminal, true)
	}
}

// openOverlay clips prior Ready work to the sampled caller's own window.
func (o *readBarrierTiming) openOverlay(stage rpctrace.Stage, at time.Time, send bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.terminal.IsZero() {
		return
	}
	kind := 0
	if send {
		kind = 1
	}
	if at.Before(o.points[0]) {
		at = o.points[0]
	}
	if !o.open[kind].IsZero() {
		o.coverageInvalid = true
	}
	o.open[kind] = at
	i := o.saves
	if send {
		i = o.sends + 2
	}
	if (!send && o.saves == 2) || (send && o.sends == 2) {
		o.dropped++
		return
	}
	o.overlays[i] = rpctrace.HistoricalSpan{Stage: stage, Start: at, Result: rpctrace.ResultUnfinished, Incomplete: true}
	if send {
		o.sends++
	} else {
		o.saves++
	}
}

func (o *readBarrierTiming) closeOverlay(at time.Time, err error, send bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.terminal.IsZero() {
		return
	}
	kind := 0
	if send {
		kind = 1
	}
	o.accumulateOverlay(kind, at, false)
	start, end := 0, o.saves
	if send {
		start, end = 2, 2+o.sends
	}
	for i := start; i < end; i++ {
		if o.overlays[i].Incomplete {
			o.overlays[i].End = at
			o.overlays[i].Incomplete = false
			o.overlayErrors[i] = err
		}
	}
}

// accumulateOverlay runs under the existing timing lock. Each kind is serial
// on a Slot, but Save and Send can overlap. Each operation contributes once to
// each positive intersection within its own kind.
// Boundaries already observed at close split the operation exactly; an unreached
// phase receives no measurement. Finish clips any open operation once.
func (o *readBarrierTiming) accumulateOverlay(kind int, end time.Time, unfinished bool) {
	start := o.open[kind]
	if start.IsZero() {
		return
	}
	o.open[kind] = time.Time{}
	if end.Before(start) {
		o.coverageInvalid = true
		return
	}
	for i := 0; i < rpctrace.BarrierPhaseCount; i++ {
		if o.points[i].IsZero() {
			break
		}
		phaseEnd := o.points[i+1]
		if phaseEnd.IsZero() || phaseEnd.After(end) {
			phaseEnd = end
		}
		intersectionStart := start
		if o.points[i].After(intersectionStart) {
			intersectionStart = o.points[i]
		}
		if phaseEnd.After(intersectionStart) {
			total := &o.coverage[i][kind]
			total.Duration += phaseEnd.Sub(intersectionStart)
			total.Count++
			total.Unfinished = total.Unfinished || unfinished
		}
		if o.points[i+1].IsZero() {
			break
		}
	}
}

// publish runs after removal from the Slot's fixed observer registry. An
// incomplete phase ends at caller termination even if RawNode completes later.
func (o *readBarrierTiming) publish(trace *rpctrace.Trace) {
	o.mu.Lock()
	points, terminal, terminalErr, overlays, overlayErrors, dropped := o.points, o.terminal, o.terminalErr, o.overlays, o.overlayErrors, o.dropped
	totals, coverageInvalid := o.coverage, o.coverageInvalid
	o.mu.Unlock()
	// Error classification may call a custom error method, so even this work
	// happens after every Slot/observation mutex has been released.
	terminalResult := rpctrace.ResultFor(terminalErr)
	if errors.Is(terminalErr, ErrSlotBusy) {
		terminalResult = rpctrace.ResultBusy
	}
	var spans [rpctrace.MaxBarrierSpans]rpctrace.HistoricalSpan
	stages := [5]rpctrace.Stage{rpctrace.StageSlotControlWait, rpctrace.StageSlotReadEligibility, rpctrace.StageSlotRawQuorum, rpctrace.StageSlotReadyToAccept, rpctrace.StageSlotApplyWait}
	var coverage rpctrace.BarrierCoverage
	for i, stage := range stages {
		coverage.Phases[i].Stage = stage
	}
	n := 0
	for i, stage := range stages {
		if points[i].IsZero() {
			break
		}
		end := points[i+1]
		incomplete := end.IsZero()
		result := rpctrace.ResultOK
		if incomplete {
			end = terminal
			result = terminalResult
		}
		coverage.Phases[i].Observed = true
		coverage.Phases[i].Save = totals[i][0]
		coverage.Phases[i].Send = totals[i][1]
		spans[n] = rpctrace.HistoricalSpan{Stage: stage, Start: points[i], End: end, Result: result, Incomplete: incomplete}
		n++
		if incomplete {
			break
		}
	}
	for i, overlay := range overlays {
		if overlay.Start.IsZero() {
			continue
		}
		if overlay.Incomplete {
			overlay.End = terminal
			overlay.Result = terminalResult
		} else {
			overlay.Result = rpctrace.ResultFor(overlayErrors[i])
		}
		spans[n] = overlay
		n++
	}
	if coverageInvalid {
		trace.RetainLongestBarrier(points[0], terminal, spans, n, dropped)
	} else {
		trace.RetainLongestBarrierWithCoverage(points[0], terminal, spans, n, dropped, coverage)
	}
}

// registerReadTiming also captures a Save/Send that began before enqueue. The
// fixed registry is necessary for a canceled caller to report an unfinished
// prior Ready operation without waiting for that operation to complete.
func (g *slot) registerReadTiming(o *readBarrierTiming) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	for i := range g.readTimings {
		if g.readTimings[i] != nil {
			continue
		}
		g.readTimings[i] = o
		if !g.readSaveStarted.IsZero() {
			o.openOverlay(g.readSaveStage, g.readSaveStarted, false)
		}
		if !g.readSendStarted.IsZero() {
			o.openOverlay(rpctrace.StageSlotRaftSend, g.readSendStarted, true)
		}
		return true
	}
	return false
}

func (g *slot) unregisterReadTiming(o *readBarrierTiming) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for i, p := range g.readTimings {
		if p == o {
			g.readTimings[i] = nil
			return
		}
	}
}

// observeReadStatesProduced records the raw quorum result before persistence.
// It deliberately does not set request.index or complete any barrier: those
// remain exclusively in acceptReadStates after the original Ready ordering.
func (g *slot) observeReadStatesProduced(states []raft.ReadState) {
	if len(states) == 0 {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	at := time.Now()
	for _, state := range states {
		if p := g.pendingReads[string(state.RequestCtx)]; p != nil {
			p.timing.Load().mark(3, at)
			for _, key := range p.batchKeys {
				if sibling := g.pendingReads[key]; sibling != nil {
					sibling.timing.Load().mark(3, at)
				}
			}
		}
	}
}

func readySaveStage(ready raft.Ready) rpctrace.Stage {
	kinds := 0
	var stage rpctrace.Stage
	if len(ready.Entries) > 0 {
		kinds++
		stage = rpctrace.StageSlotSaveEntries
	}
	if !raft.IsEmptyHardState(ready.HardState) {
		kinds++
		stage = rpctrace.StageSlotSaveHardState
	}
	if !raft.IsEmptySnap(ready.Snapshot) {
		kinds++
		stage = rpctrace.StageSlotSaveSnapshot
	}
	if kinds > 1 {
		return rpctrace.StageSlotSaveMixed
	}
	return stage
}

// begin/end Ready overlays contain only fixed timing bookkeeping. The Save
// interval covers PersistentState preparation plus the existing Storage.Save;
// message-only Ready has no Save overlay. Send is a separate overlapping span.
func (g *slot) beginReadReadyOverlay(stage rpctrace.Stage, send bool) {
	if stage == "" {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	at := time.Now()
	if send {
		g.readSendStarted = at
	} else {
		g.readSaveStarted = at
		g.readSaveStage = stage
	}
	for _, o := range g.readTimings {
		if o != nil {
			o.openOverlay(stage, at, send)
		}
	}
}

func (g *slot) endReadReadyOverlay(stage rpctrace.Stage, err error, send bool) {
	if stage == "" {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	at := time.Now()
	for _, o := range g.readTimings {
		if o != nil {
			o.closeOverlay(at, err, send)
		}
	}
	if send {
		g.readSendStarted = time.Time{}
	} else {
		g.readSaveStarted = time.Time{}
		g.readSaveStage = ""
	}
}
