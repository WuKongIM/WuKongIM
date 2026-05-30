package reactor

import (
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/transport"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/worker"
)

const (
	appendWaitStageStoreAppend         = "store_append_wait"
	appendWaitStagePostStoreCommit     = "post_store_commit_wait"
	appendWaitStageQuorumFollowerPull  = "quorum_follower_pull_wait"
	appendWaitStageQuorumAckOffset     = "quorum_ack_offset_wait"
	appendWaitStageQuorumHWAdvance     = "quorum_hw_advance_wait"
	appendWaitStageQuorumFinalComplete = "quorum_final_complete_wait"
	replicationStagePullHintToSubmit   = "follower_pull_hint_to_submit"
	replicationStagePullRPC            = "follower_pull_rpc"
	replicationStageStoreApply         = "follower_store_apply"
	replicationStageApplyToAckReturn   = "follower_apply_to_ack_return"
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

// ReplicationStageObserver receives optional follower replication stage metrics.
type ReplicationStageObserver interface {
	ObserveReplicationStage(stage string, result string, d time.Duration)
}

// LifecycleObserver receives optional leader runtime lifecycle events.
type LifecycleObserver interface {
	ObserveChannelRuntimeLoaded(key ch.ChannelKey)
	ObserveChannelRuntimeEvicted(key ch.ChannelKey, role ch.Role)
	ObservePullHintSent(key ch.ChannelKey, follower ch.NodeID, reason transport.PullHintReason)
	ObservePullHintDropped(key ch.ChannelKey, follower ch.NodeID, err error)
}

// PullHintResultObserver receives optional leader PullHint result counters.
type PullHintResultObserver interface {
	ObservePullHintResult(reason transport.PullHintReason, result string, err error)
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
	if !timing.hwAdvancedAt.IsZero() {
		r.observeAppendWaitStage(appendWaitStageQuorumFinalComplete, timing.mode, appendWaitResult(err), completedAt.Sub(timing.hwAdvancedAt))
	}
	if !timing.storeCompletedAt.IsZero() {
		r.observeAppendWaitStage(appendWaitStagePostStoreCommit, timing.mode, appendWaitResult(err), completedAt.Sub(timing.storeCompletedAt))
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

func (r *Reactor) observeAppendStoreCompleted(rc *runtimeChannel, batch appendBatch, completedAt time.Time, baseOffset uint64, err error) {
	if r == nil || rc == nil {
		return
	}
	nextOffset := baseOffset
	for _, req := range batch.requests {
		timing, ok := rc.appendTimings[req.opID]
		recordCount := len(req.records)
		if !ok {
			nextOffset += uint64(recordCount)
			continue
		}
		if !timing.storeSubmittedAt.IsZero() {
			r.observeAppendWaitStage(appendWaitStageStoreAppend, timing.mode, appendWaitResult(err), completedAt.Sub(timing.storeSubmittedAt))
		}
		if err == nil {
			timing.storeCompletedAt = completedAt
			if nextOffset > 0 && recordCount > 0 {
				timing.targetOffset = nextOffset + uint64(recordCount) - 1
			}
			rc.appendTimings[req.opID] = timing
		}
		nextOffset += uint64(recordCount)
	}
}

func (r *Reactor) markAppendFollowerPullServed(rc *runtimeChannel, records []ch.Record, servedAt time.Time) {
	if r == nil || rc == nil || len(records) == 0 {
		return
	}
	for opID, timing := range rc.appendTimings {
		if !timing.tracksQuorumCommitWait() || !timing.followerPullServedAt.IsZero() || !recordsCoverOffset(records, timing.targetOffset) {
			continue
		}
		timing.followerPullServedAt = servedAt
		rc.appendTimings[opID] = timing
		r.observeAppendWaitStage(appendWaitStageQuorumFollowerPull, timing.mode, "ok", servedAt.Sub(timing.storeCompletedAt))
	}
}

func (r *Reactor) markAppendAckOffsetObserved(rc *runtimeChannel, ackOffset uint64, observedAt time.Time) {
	if r == nil || rc == nil || ackOffset == 0 {
		return
	}
	for opID, timing := range rc.appendTimings {
		if !timing.tracksQuorumCommitWait() || !timing.ackOffsetObservedAt.IsZero() || ackOffset < timing.targetOffset {
			continue
		}
		timing.ackOffsetObservedAt = observedAt
		rc.appendTimings[opID] = timing
		r.observeAppendWaitStage(appendWaitStageQuorumAckOffset, timing.mode, "ok", observedAt.Sub(timing.storeCompletedAt))
	}
}

func (r *Reactor) markAppendHWAdvanced(rc *runtimeChannel, oldHW uint64, newHW uint64, advancedAt time.Time) {
	if r == nil || rc == nil || newHW == 0 {
		return
	}
	for opID, timing := range rc.appendTimings {
		if !timing.tracksQuorumCommitWait() || !timing.hwAdvancedAt.IsZero() || oldHW >= timing.targetOffset || newHW < timing.targetOffset {
			continue
		}
		timing.hwAdvancedAt = advancedAt
		rc.appendTimings[opID] = timing
		r.observeAppendWaitStage(appendWaitStageQuorumHWAdvance, timing.mode, "ok", advancedAt.Sub(timing.storeCompletedAt))
	}
}

func (timing appendTiming) tracksQuorumCommitWait() bool {
	return timing.mode == ch.CommitModeQuorum && !timing.storeCompletedAt.IsZero() && timing.targetOffset > 0
}

func recordsCoverOffset(records []ch.Record, offset uint64) bool {
	if offset == 0 {
		return false
	}
	for _, record := range records {
		if record.Index == offset {
			return true
		}
	}
	return false
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

func (r *Reactor) observeReplicationStage(stage string, result string, d time.Duration) {
	if d < 0 {
		d = 0
	}
	if observer, ok := r.cfg.Observer.(ReplicationStageObserver); ok {
		observer.ObserveReplicationStage(stage, result, d)
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

func (r *Reactor) observePullHintResult(reason transport.PullHintReason, result string, err error) {
	if observer, ok := r.cfg.Observer.(PullHintResultObserver); ok {
		observer.ObservePullHintResult(reason, result, err)
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
