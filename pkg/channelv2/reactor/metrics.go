package reactor

import (
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/transport"
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

// AppendWaitStageObserver receives optional append future wait sub-stage metrics.
type AppendWaitStageObserver interface {
	ObserveAppendWaitStage(stage string, mode ch.CommitMode, result string, d time.Duration)
}

// RuntimeObserver receives low-cardinality active-runtime metrics.
type RuntimeObserver interface {
	SetChannelRuntimeCount(reactorID int, role ch.Role, count int)
	ObserveChannelActivationRejected(reason string)
}

// ReplicationObserver receives follower replication scheduling metrics.
type ReplicationObserver interface {
	SetFollowerParkedCount(reactorID int, count int)
	ObserveFollowerRecoveryProbe(result string)
	ObservePull(result string, empty bool)
}

// LifecycleObserver receives optional leader runtime lifecycle events.
type LifecycleObserver interface {
	ObserveChannelRuntimeLoaded(key ch.ChannelKey)
	ObserveChannelRuntimeEvicted(key ch.ChannelKey, role ch.Role)
	ObservePullHintSent(key ch.ChannelKey, follower ch.NodeID, reason transport.PullHintReason)
	ObservePullHintDropped(key ch.ChannelKey, follower ch.NodeID, err error)
}

// FollowerLifecycleObserver receives optional follower lifecycle events without
// requiring existing lifecycle observers to grow new methods.
type FollowerLifecycleObserver interface {
	ObserveFollowerStopped(key ch.ChannelKey, follower ch.NodeID, activityVersion uint64)
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

func (r *Reactor) observeAppendComplete(rc *runtimeChannel, opID ch.OpID, err error) {
	timing, ok := rc.appendTimings[opID]
	if !ok {
		return
	}
	delete(rc.appendTimings, opID)
	completedAt := time.Now()
	if !timing.storeCompletedAt.IsZero() {
		r.observeAppendWaitStage("post_store_commit_wait", timing.mode, appendWaitResult(err), completedAt.Sub(timing.storeCompletedAt))
	}
	wait := completedAt.Sub(timing.enqueuedAt)
	if wait < 0 {
		wait = 0
	}
	r.cfg.Observer.ObserveAppendLatency(timing.mode, wait)
}

func (r *Reactor) markAppendStoreSubmitted(rc *runtimeChannel, batch appendBatch, submittedAt time.Time) {
	if r == nil || rc == nil {
		return
	}
	for _, req := range batch.requests {
		timing, ok := rc.appendTimings[req.opID]
		if !ok {
			continue
		}
		timing.storeSubmittedAt = submittedAt
		rc.appendTimings[req.opID] = timing
	}
}

func (r *Reactor) observeAppendStoreCompleted(rc *runtimeChannel, batch appendBatch, completedAt time.Time, err error) {
	if r == nil || rc == nil {
		return
	}
	for _, req := range batch.requests {
		timing, ok := rc.appendTimings[req.opID]
		if !ok || timing.storeSubmittedAt.IsZero() {
			continue
		}
		r.observeAppendWaitStage("store_append_wait", timing.mode, appendWaitResult(err), completedAt.Sub(timing.storeSubmittedAt))
		if err == nil {
			timing.storeCompletedAt = completedAt
			rc.appendTimings[req.opID] = timing
		}
	}
}

func (r *Reactor) observeAppendWaitStage(stage string, mode ch.CommitMode, result string, d time.Duration) {
	if d < 0 {
		d = 0
	}
	if observer, ok := r.cfg.Observer.(AppendWaitStageObserver); ok {
		observer.ObserveAppendWaitStage(stage, mode, result, d)
	}
}

func appendWaitResult(err error) string {
	if err != nil {
		return "err"
	}
	return "ok"
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

func (r *Reactor) observeRuntimeCount(role ch.Role, count int) {
	if observer, ok := r.cfg.Observer.(RuntimeObserver); ok {
		observer.SetChannelRuntimeCount(r.cfg.ID, role, count)
	}
}

func (r *Reactor) observeActivationRejected(reason string) {
	if r == nil {
		return
	}
	r.activationRejectedTotal++
	if observer, ok := r.cfg.Observer.(RuntimeObserver); ok {
		observer.ObserveChannelActivationRejected(reason)
	}
}

func (r *Reactor) observeFollowerParkedCount(count int) {
	if observer, ok := r.cfg.Observer.(ReplicationObserver); ok {
		observer.SetFollowerParkedCount(r.cfg.ID, count)
	}
}

func (r *Reactor) observeRecoveryProbe(result string) {
	if observer, ok := r.cfg.Observer.(ReplicationObserver); ok {
		observer.ObserveFollowerRecoveryProbe(result)
	}
}

func (r *Reactor) observePull(result string, empty bool) {
	if observer, ok := r.cfg.Observer.(ReplicationObserver); ok {
		observer.ObservePull(result, empty)
	}
}

func (r *Reactor) observeChannelRuntimeLoaded(key ch.ChannelKey) {
	if observer, ok := r.cfg.Observer.(LifecycleObserver); ok {
		observer.ObserveChannelRuntimeLoaded(key)
	}
}

func (r *Reactor) observeChannelRuntimeEvicted(key ch.ChannelKey, role ch.Role) {
	if observer, ok := r.cfg.Observer.(LifecycleObserver); ok {
		observer.ObserveChannelRuntimeEvicted(key, role)
	}
}

func (r *Reactor) observePullHintSent(key ch.ChannelKey, follower ch.NodeID, reason transport.PullHintReason) {
	if observer, ok := r.cfg.Observer.(LifecycleObserver); ok {
		observer.ObservePullHintSent(key, follower, reason)
	}
}

func (r *Reactor) observePullHintDropped(key ch.ChannelKey, follower ch.NodeID, err error) {
	if observer, ok := r.cfg.Observer.(LifecycleObserver); ok {
		observer.ObservePullHintDropped(key, follower, err)
	}
}

func (r *Reactor) observeFollowerStopped(key ch.ChannelKey, follower ch.NodeID, activityVersion uint64) {
	if observer, ok := r.cfg.Observer.(FollowerLifecycleObserver); ok {
		observer.ObserveFollowerStopped(key, follower, activityVersion)
	}
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
