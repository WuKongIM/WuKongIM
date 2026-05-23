package reactor

import (
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/worker"
)

// Observer receives lightweight runtime metrics from the reactor hot path.
type Observer interface {
	SetReactorMailboxDepth(reactorID int, priority string, depth int)
	SetWorkerQueueDepth(pool string, depth int)
	ObserveAppendBatch(records int, bytes int, wait time.Duration)
	ObserveAppendLatency(mode ch.CommitMode, d time.Duration)
	ObserveWorkerResult(kind worker.TaskKind, err error, d time.Duration)
}

type noopObserver struct{}

func (noopObserver) SetReactorMailboxDepth(reactorID int, priority string, depth int) {}
func (noopObserver) SetWorkerQueueDepth(pool string, depth int)                       {}
func (noopObserver) ObserveAppendBatch(records int, bytes int, wait time.Duration)    {}
func (noopObserver) ObserveAppendLatency(mode ch.CommitMode, d time.Duration)         {}
func (noopObserver) ObserveWorkerResult(kind worker.TaskKind, err error, d time.Duration) {
}

func defaultObserver(observer Observer) Observer {
	if observer == nil {
		return noopObserver{}
	}
	return observer
}

func (r *Reactor) observeAppendComplete(rc *runtimeChannel, opID ch.OpID) {
	timing, ok := rc.appendTimings[opID]
	if !ok {
		return
	}
	delete(rc.appendTimings, opID)
	wait := time.Since(timing.enqueuedAt)
	if wait < 0 {
		wait = 0
	}
	r.cfg.Observer.ObserveAppendLatency(timing.mode, wait)
}

func (r *Reactor) observeAppendBatch(batch appendBatch, now time.Time) {
	if len(batch.requests) == 0 {
		return
	}
	wait := now.Sub(batch.requests[0].enqueuedAt)
	if wait < 0 {
		wait = 0
	}
	r.cfg.Observer.ObserveAppendBatch(len(batch.records), recordsBytes(batch.records), wait)
}

func (r *Reactor) observeMailboxDepth(priority Priority) {
	r.cfg.Observer.SetReactorMailboxDepth(r.cfg.ID, priorityName(priority), r.mailbox.Depth(priority))
}

func (r *Reactor) observeAllMailboxDepths() {
	r.observeMailboxDepth(PriorityHigh)
	r.observeMailboxDepth(PriorityNormal)
	r.observeMailboxDepth(PriorityLow)
}

func priorityName(priority Priority) string {
	switch priority {
	case PriorityHigh:
		return "high"
	case PriorityLow:
		return "low"
	default:
		return "normal"
	}
}
