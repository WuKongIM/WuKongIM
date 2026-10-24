package multiraft

import "time"

// setStorageCountPaused is owner-only and preserves the existing scheduling
// decision. Enabled observation records the actual marker transition under mu
// so registration cannot lose an already-open pause. Resume includes time until
// this owner consumes completions and observes a returned whole lease; physical
// Save completion alone does not close it. Disabled tracing adds no clock/lock.
func (g *slot) setStorageCountPaused(paused bool) {
	if g.storageCountPaused == paused {
		return
	}
	if !g.readBarrierTracing {
		g.storageCountPaused = paused
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	at := time.Now()
	g.storageCountPaused = paused
	if paused {
		g.readCountPausedStarted = at
	} else {
		g.readCountPausedStarted = time.Time{}
	}
	for _, o := range g.readTimings {
		if o == nil {
			continue
		}
		if paused {
			o.openCountPaused(at)
		} else {
			o.closeCountPaused(at, false)
		}
	}
}

// openCountPaused clips an older Slot pause to this caller's observation start.
// The fixed third cumulative track creates no detailed span or observer entry.
func (o *readBarrierTiming) openCountPaused(at time.Time) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.terminal.IsZero() {
		return
	}
	if at.Before(o.points[0]) {
		at = o.points[0]
	}
	if !o.open[2].IsZero() {
		o.coverageInvalid = true
		return
	}
	o.open[2] = at
}

func (o *readBarrierTiming) closeCountPaused(at time.Time, unfinished bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.terminal.IsZero() {
		o.accumulateOverlay(2, at, unfinished)
	}
}
