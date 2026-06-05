package reactor

import (
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/machine"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/worker"
)

func (r *Reactor) handleAppend(event Event) {
	rc, err := r.lookupLoadedChannel(event.Key)
	if err != nil {
		event.Future.Complete(Result{Err: err})
		return
	}
	if err := validateAppendEvent(rc, event); err != nil {
		event.Future.Complete(Result{Err: err})
		return
	}
	admittedAt := time.Now()
	req := newAppendRequest(event, admittedAt)
	if err := r.enqueueAppendRequest(rc, req); err != nil {
		event.Future.Complete(Result{Err: err})
		return
	}
	r.cancelLeaderEvictionForAppend(rc, admittedAt)
	r.trackAppendTiming(rc, req)
	r.registerAppendCancelContext(rc, event.OpID, event.Context)
	r.tryFlushAppend(rc, admittedAt)
}

func validateAppendEvent(rc *runtimeChannel, event Event) error {
	if event.Context != nil {
		if err := event.Context.Err(); err != nil {
			return err
		}
	}
	if _, ok := rc.waiters[event.OpID]; ok {
		return ch.ErrInvalidConfig
	}
	if rc.state.Status == ch.StatusDeleted || rc.state.Status == ch.StatusDeleting {
		return ch.ErrChannelNotFound
	}
	if rc.state.Role != ch.RoleLeader {
		return ch.ErrNotLeader
	}
	if !rc.state.CommitReady {
		return ch.ErrNotReady
	}
	if event.Append.ExpectedChannelEpoch != 0 && event.Append.ExpectedChannelEpoch != rc.state.Epoch {
		return ch.ErrStaleMeta
	}
	if event.Append.ExpectedLeaderEpoch != 0 && event.Append.ExpectedLeaderEpoch != rc.state.LeaderEpoch {
		return ch.ErrStaleMeta
	}
	return nil
}

func newAppendRequest(event Event, admittedAt time.Time) appendRequest {
	return appendRequest{
		opID:       event.OpID,
		req:        event.Append,
		future:     event.Future,
		ctx:        event.Context,
		enqueuedAt: admittedAt,
		records:    appendRecordsFromMessages(event.Append.Messages),
		commitMode: normalizedCommitMode(event.Append.CommitMode),
	}
}

func appendRecordsFromMessages(messages []ch.Message) []ch.Record {
	records := make([]ch.Record, len(messages))
	for i, msg := range messages {
		records[i] = ch.Record{ID: msg.MessageID, Payload: append([]byte(nil), msg.Payload...), SizeBytes: len(msg.Payload)}
	}
	return records
}

func normalizedCommitMode(mode ch.CommitMode) ch.CommitMode {
	if mode == 0 {
		return ch.CommitModeQuorum
	}
	return mode
}

func (r *Reactor) enqueueAppendRequest(rc *runtimeChannel, req appendRequest) error {
	if err := rc.appendQ.push(req); err != nil {
		r.observeAppendQueuePressure(rc)
		return err
	}
	r.observeAppendQueuePressure(rc)
	if err := rc.addWaiter(req.opID, req.future); err != nil {
		rc.appendQ.remove(req.opID)
		r.observeAppendQueuePressure(rc)
		return err
	}
	return nil
}

func (r *Reactor) trackAppendTiming(rc *runtimeChannel, req appendRequest) {
	if rc.appendTimings == nil {
		rc.appendTimings = make(map[ch.OpID]appendTiming)
	}
	rc.appendTimings[req.opID] = appendTiming{mode: req.commitMode, enqueuedAt: req.enqueuedAt}
}

func (r *Reactor) handleStoreAppendResult(result worker.Result) {
	rc, err := r.lookupLoadedChannel(result.Fence.ChannelKey)
	if err != nil {
		return
	}
	batch := rc.appendInflight
	current := batch != nil && batch.batchOpID == result.Fence.OpID
	stored := appendStoredResultFromWorker(result)
	oldLEO := rc.state.LEO
	oldHW := rc.state.HW
	now := time.Now()
	if current {
		r.observeAppendStoreCompleted(rc, *batch, now, stored.BaseOffset, stored.Err)
	}
	decision := rc.state.ApplyAppendStored(stored)
	if stored.Err == nil {
		r.markAppendHWAdvanced(rc, oldHW, rc.state.HW, now)
	}
	if stored.Err == nil && rc.state.Role == ch.RoleLeader && rc.state.LEO > oldLEO {
		r.afterSuccessfulLeaderAppendStored(rc, result.Fence.OpID, now)
	}
	r.completeReplies(rc, decision.Replies, nil)
	if current {
		r.finishAppendInflightBatch(rc, stored.Err, now)
	}
}

func appendStoredResultFromWorker(result worker.Result) machine.AppendStoredResult {
	stored := machine.AppendStoredResult{Fence: result.Fence, Err: result.Err}
	if result.StoreAppend == nil {
		if stored.Err == nil {
			stored.Err = ch.ErrInvalidConfig
		}
		return stored
	}
	stored.BaseOffset = result.StoreAppend.BaseOffset
	stored.LastOffset = result.StoreAppend.LastOffset
	return stored
}

func (r *Reactor) afterSuccessfulLeaderAppendStored(rc *runtimeChannel, batchOpID ch.OpID, now time.Time) {
	rc.recentRecords.append(rc.appendInflightRecords(batchOpID))
	r.markAppendActivity(rc, now)
	rc.lifecycle.version = rc.state.LEO
	r.syncFollowerMatches(rc)
	for _, follower := range rc.lifecycle.followers {
		if follower == nil || follower.match >= rc.state.LEO {
			continue
		}
		if follower.stoppedVersion != 0 || follower.stopOfferedVersion != 0 || follower.stopOfferedZero {
			follower.lastPullAt = time.Time{}
		}
		follower.resetStop()
	}
	r.sendPullHintsForAppend(rc, now)
	r.scheduleLaggingFollowerResumeHints(rc, now)
}

func (r *Reactor) finishAppendInflightBatch(rc *runtimeChannel, appendErr error, now time.Time) {
	if appendErr != nil {
		for _, req := range rc.appendInflight.requests {
			if future := rc.waiters[req.opID]; future != nil {
				delete(rc.waiters, req.opID)
				r.unregisterAppendCancelContext(rc, req.opID)
				r.completeAppendFuture(rc, req.opID, future, Result{Err: appendErr})
			}
		}
	}
	rc.appendInflight = nil
	rc.appendQ.storeBlocked = false
	r.observeAppendQueuePressure(rc)
	r.tryFlushAppend(rc, now)
}

func (rc *runtimeChannel) appendInflightRecords(batchOpID ch.OpID) []ch.Record {
	if rc == nil || rc.appendInflight == nil || rc.appendInflight.batchOpID != batchOpID {
		return nil
	}
	return rc.appendInflight.records
}
