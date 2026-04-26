package replica

import (
	"context"
	"errors"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/observability/sendtrace"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

const checkpointRetryDelay = 10 * time.Millisecond

type hwAdvanceOutcome struct {
	// advanced is true when the loop raised runtime HW.
	advanced bool
	// candidate is the quorum-visible offset considered by the loop.
	candidate uint64
	// oldHW and newHW are captured for diagnostics outside the loop lock.
	oldHW uint64
	newHW uint64
	// leo is the leader LEO snapshot paired with the HW decision.
	leo uint64
	// channelKey and progress are captured for diagnostics outside the loop lock.
	channelKey channel.ChannelKey
	progress   map[uint64]uint64
	// notify is the non-blocking runtime wakeup to invoke after releasing the lock.
	notify func()
	// err reports corrupt quorum progress without applying a HW mutation.
	err error
}

func (r *replica) applyCursorCommand(cmd machineCursorCommand) machineResult {
	var (
		outcome     hwAdvanceOutcome
		channelKey  channel.ChannelKey
		oldProgress uint64
		hw          uint64
		leo         uint64
		progress    map[uint64]uint64
	)

	r.mu.Lock()
	if r.state.Role == channel.ReplicaRoleTombstoned {
		r.mu.Unlock()
		return machineResult{Err: channel.ErrTombstoned}
	}
	if r.state.Role != channel.ReplicaRoleLeader && r.state.Role != channel.ReplicaRoleFencedLeader {
		r.mu.Unlock()
		return machineResult{Err: channel.ErrNotLeader}
	}
	if cmd.ChannelKey != "" && cmd.ChannelKey != r.state.ChannelKey {
		r.mu.Unlock()
		return machineResult{Err: channel.ErrStaleMeta}
	}
	if cmd.Epoch != r.state.Epoch {
		r.mu.Unlock()
		return machineResult{Err: channel.ErrStaleMeta}
	}
	if cmd.ReplicaID == 0 {
		r.mu.Unlock()
		return machineResult{Err: channel.ErrInvalidMeta}
	}
	matchOffset, err := r.cursorMatchOffsetLocked(cmd.MatchOffset, cmd.OffsetEpoch)
	if err != nil {
		r.mu.Unlock()
		return machineResult{Err: err}
	}
	if matchOffset > r.state.LEO {
		r.mu.Unlock()
		return machineResult{Err: channel.ErrCorruptState}
	}

	channelKey = r.state.ChannelKey
	oldProgress = r.progress[cmd.ReplicaID]
	if matchOffset <= oldProgress {
		hw = r.state.HW
		leo = r.state.LEO
		r.mu.Unlock()
		r.appendLogger().Debug("follower cursor stale, skipped",
			wklog.Event("repl.diag.cursor_stale"),
			wklog.String("channelKey", string(channelKey)),
			wklog.Uint64("replicaID", uint64(cmd.ReplicaID)),
			wklog.Uint64("matchOffset", matchOffset),
			wklog.Uint64("currentProgress", oldProgress),
		)
		return machineResult{}
	}

	r.setReplicaProgressLocked(cmd.ReplicaID, matchOffset)
	r.publishStateLocked()
	outcome = r.advanceHWLocked()
	progress = r.snapshotProgressLocked()
	hw = r.state.HW
	leo = r.state.LEO
	r.mu.Unlock()

	r.appendLogger().Debug("follower cursor applied",
		wklog.Event("repl.diag.cursor_applied"),
		wklog.String("channelKey", string(channelKey)),
		wklog.Uint64("replicaID", uint64(cmd.ReplicaID)),
		wklog.Uint64("matchOffset", matchOffset),
		wklog.Uint64("oldProgress", oldProgress),
		wklog.Uint64("hw", hw),
		wklog.Uint64("leo", leo),
		wklog.Any("progress", progress),
	)
	r.finishHWAdvanceOutcome(outcome)
	return machineResult{Err: outcome.err}
}

func (r *replica) applyFetchProgressCommand(cmd machineFetchProgressCommand) machineResult {
	req := cmd.Request
	var outcome hwAdvanceOutcome

	r.mu.Lock()
	if r.state.Role == channel.ReplicaRoleTombstoned {
		r.mu.Unlock()
		return machineResult{Err: channel.ErrTombstoned}
	}
	if req.MaxBytes <= 0 {
		r.mu.Unlock()
		return machineResult{Err: channel.ErrInvalidFetchBudget}
	}
	if req.ReplicaID == 0 {
		r.mu.Unlock()
		return machineResult{Err: channel.ErrInvalidMeta}
	}
	if r.state.Role != channel.ReplicaRoleLeader && r.state.Role != channel.ReplicaRoleFencedLeader {
		r.mu.Unlock()
		return machineResult{Err: channel.ErrNotLeader}
	}
	if r.state.ChannelKey != "" && req.ChannelKey != r.state.ChannelKey {
		r.mu.Unlock()
		return machineResult{Err: channel.ErrStaleMeta}
	}
	if req.Epoch != r.state.Epoch {
		r.mu.Unlock()
		return machineResult{Err: channel.ErrStaleMeta}
	}
	if req.FetchOffset < r.state.LogStartOffset {
		r.mu.Unlock()
		return machineResult{Err: channel.ErrSnapshotRequired}
	}

	leaderLEO := r.state.LEO
	matchOffset, truncateTo, err := r.divergenceStateLocked(req.FetchOffset, req.OffsetEpoch, leaderLEO)
	if err != nil {
		r.mu.Unlock()
		return machineResult{Err: err}
	}

	r.state.OffsetEpoch = offsetEpochForLEO(r.epochHistory, leaderLEO)
	needsAdvance := r.progress[r.localNode] != leaderLEO
	r.setReplicaProgressLocked(r.localNode, leaderLEO)

	oldProgress := r.progress[req.ReplicaID]
	if matchOffset > oldProgress {
		needsAdvance = true
		r.setReplicaProgressLocked(req.ReplicaID, matchOffset)
	}
	r.publishStateLocked()
	if needsAdvance {
		outcome = r.advanceHWLocked()
	}
	fetch := &machineFetchProgressResult{
		Result: channel.ReplicaFetchResult{
			Epoch:      r.state.Epoch,
			HW:         visibleCommittedHW(r.state),
			TruncateTo: truncateTo,
		},
		LeaderLEO:    leaderLEO,
		MatchOffset:  matchOffset,
		OldProgress:  oldProgress,
		NeedsAdvance: needsAdvance,
		ChannelKey:   r.state.ChannelKey,
		ReplicaID:    req.ReplicaID,
		FetchOffset:  req.FetchOffset,
	}
	if truncateTo == nil && req.FetchOffset < leaderLEO {
		fetch.ReadLog = &readLogEffect{
			EffectID:       r.nextLoopEffectID(),
			ChannelKey:     r.state.ChannelKey,
			Epoch:          r.state.Epoch,
			RoleGeneration: r.roleGeneration,
			LeaderLEO:      leaderLEO,
			FetchOffset:    req.FetchOffset,
			MaxBytes:       req.MaxBytes,
			Result:         fetch.Result,
		}
	}
	r.mu.Unlock()

	r.finishHWAdvanceOutcome(outcome)
	return machineResult{Fetch: fetch, Err: outcome.err}
}

func (r *replica) applyLeaderAppendCommittedEvent(ev machineLeaderAppendCommittedEvent) machineResult {
	if len(ev.RequestIDs) == 0 {
		return machineResult{}
	}
	durableStartedAt := ev.DurableStartedAt
	if durableStartedAt.IsZero() {
		durableStartedAt = ev.DoneAt
	}
	durableDoneAt := ev.DoneAt
	if durableDoneAt.IsZero() {
		durableDoneAt = r.now()
	}

	var (
		outcome                 hwAdvanceOutcome
		notifyLeaderLocalAppend func()
		channelKey              channel.ChannelKey
		nextLEO                 uint64
		hw                      uint64
		progress                map[uint64]uint64
	)

	r.mu.Lock()
	requests, matched, err := r.takeAppendInFlightResultLocked(ev)
	if !matched {
		r.mu.Unlock()
		return machineResult{}
	}
	if err != nil {
		r.maybeFlushAppendLocked()
		r.mu.Unlock()
		return machineResult{Err: err}
	}
	if ev.Err != nil {
		if errors.Is(ev.Err, channel.ErrLeaseExpired) {
			_ = r.appendableLocked()
		}
		r.failDurableAppendRequestsLocked(requests, ev.Err)
		r.maybeFlushAppendLocked()
		r.mu.Unlock()
		return machineResult{}
	}
	if ev.ChannelKey != r.state.ChannelKey || ev.Epoch != r.state.Epoch ||
		ev.LeaderEpoch != r.meta.LeaderEpoch || ev.RoleGeneration != r.roleGeneration {
		r.failDurableAppendRequestsLocked(requests, channel.ErrNotLeader)
		r.maybeFlushAppendLocked()
		r.mu.Unlock()
		return machineResult{}
	}
	if err := r.appendableLocked(); err != nil {
		err = appendFailureForState(err)
		r.failDurableAppendRequestsLocked(requests, err)
		r.maybeFlushAppendLocked()
		r.mu.Unlock()
		return machineResult{}
	}
	if ev.BaseOffset != r.state.LEO {
		r.failDurableAppendRequestsLocked(requests, channel.ErrCorruptState)
		r.maybeFlushAppendLocked()
		r.mu.Unlock()
		return machineResult{Err: channel.ErrCorruptState}
	}

	nextLEO = ev.BaseOffset
	recordCount := 0
	for _, req := range requests {
		recordCount += len(req.batch)
		reqCtx := req.ctx
		if reqCtx == nil {
			reqCtx = context.Background()
		}
		target := nextLEO + uint64(len(req.batch))
		rangeStart := nextLEO + 1
		if req.completed {
			delete(r.appendRequests, req.requestID)
			nextLEO = target
			continue
		}
		if reqCtx.Err() != nil {
			r.completeAppendRequestLocked(req, channel.CommitResult{}, reqCtx.Err())
			delete(r.appendRequests, req.requestID)
			nextLEO = target
			continue
		}

		req.waiter.target = target
		req.waiter.rangeStart = rangeStart
		req.waiter.rangeEnd = target
		req.waiter.durableDoneAt = durableDoneAt
		req.waiter.result.BaseOffset = nextLEO
		req.waiter.result.RecordCount = len(req.batch)
		req.waiter.result.NextCommitHW = r.state.HW
		sendtrace.Record(sendtrace.Event{
			Stage:      sendtrace.StageReplicaLeaderQueueWait,
			At:         req.waiter.enqueuedAt,
			Duration:   sendtrace.Elapsed(req.waiter.enqueuedAt, durableStartedAt),
			NodeID:     uint64(r.localNode),
			ChannelKey: string(r.state.ChannelKey),
			RangeStart: rangeStart,
			RangeEnd:   target,
		})
		sendtrace.Record(sendtrace.Event{
			Stage:      sendtrace.StageReplicaLeaderLocalDurable,
			At:         durableStartedAt,
			Duration:   sendtrace.Elapsed(durableStartedAt, durableDoneAt),
			NodeID:     uint64(r.localNode),
			ChannelKey: string(r.state.ChannelKey),
			RangeStart: rangeStart,
			RangeEnd:   target,
		})
		if req.commitMode == channel.CommitModeLocal {
			r.completeAppendRequestLocked(req, req.waiter.result, nil)
			delete(r.appendRequests, req.requestID)
			nextLEO = target
			continue
		}
		req.stage = appendRequestWaitingQuorum
		r.waiters = append(r.waiters, req.waiter)
		nextLEO = target
	}
	r.state.LEO = nextLEO
	r.state.OffsetEpoch = offsetEpochForLEO(r.epochHistory, nextLEO)
	r.setReplicaProgressLocked(r.localNode, nextLEO)
	r.publishStateLocked()
	notifyLeaderLocalAppend = r.onLeaderLocalAppend
	channelKey = r.state.ChannelKey
	hw = r.state.HW
	progress = r.snapshotProgressLocked()
	outcome = r.advanceHWLocked()
	r.mu.Unlock()

	r.appendLogger().Debug("leader local append flushed",
		wklog.Event("repl.diag.leader_append_flushed"),
		wklog.String("channelKey", string(channelKey)),
		wklog.Uint64("leo", nextLEO),
		wklog.Uint64("hw", hw),
		wklog.Int("records", recordCount),
		wklog.Bool("callbackExists", notifyLeaderLocalAppend != nil),
		wklog.Any("progress", progress),
	)
	if notifyLeaderLocalAppend != nil {
		notifyLeaderLocalAppend()
	}
	r.finishHWAdvanceOutcome(outcome)
	r.mu.Lock()
	r.maybeFlushAppendLocked()
	r.mu.Unlock()
	return machineResult{Err: outcome.err}
}

func (r *replica) takeAppendInFlightResultLocked(ev machineLeaderAppendCommittedEvent) ([]*appendRequest, bool, error) {
	if ev.EffectID == 0 || ev.EffectID != r.appendInFlightEffectID {
		return nil, false, nil
	}
	if !appendRequestIDsEqual(r.appendInFlightIDs, ev.RequestIDs) {
		requests := r.appendRequestsByIDsLocked(r.appendInFlightIDs)
		r.appendInFlightIDs = nil
		r.appendInFlightEffectID = 0
		r.failDurableAppendRequestsLocked(requests, channel.ErrCorruptState)
		return nil, true, channel.ErrCorruptState
	}

	requests := make([]*appendRequest, 0, len(ev.RequestIDs))
	for _, requestID := range ev.RequestIDs {
		req := r.appendRequests[requestID]
		if req == nil {
			current := r.appendRequestsByIDsLocked(r.appendInFlightIDs)
			r.appendInFlightIDs = nil
			r.appendInFlightEffectID = 0
			r.failDurableAppendRequestsLocked(current, channel.ErrCorruptState)
			return nil, true, channel.ErrCorruptState
		}
		requests = append(requests, req)
	}
	r.appendInFlightIDs = nil
	r.appendInFlightEffectID = 0
	return requests, true, nil
}

func (r *replica) appendRequestsByIDsLocked(requestIDs []uint64) []*appendRequest {
	requests := make([]*appendRequest, 0, len(requestIDs))
	for _, requestID := range requestIDs {
		if req := r.appendRequests[requestID]; req != nil {
			requests = append(requests, req)
		}
	}
	return requests
}

func (r *replica) failDurableAppendRequestsLocked(requests []*appendRequest, err error) {
	for _, req := range requests {
		if req == nil {
			continue
		}
		r.completeAppendRequestLocked(req, channel.CommitResult{}, err)
		if req.requestID != 0 {
			delete(r.appendRequests, req.requestID)
		}
	}
}

func appendRequestIDsEqual(left, right []uint64) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func (r *replica) applyAdvanceHWEvent() machineResult {
	r.mu.Lock()
	outcome := r.advanceHWLocked()
	r.mu.Unlock()

	r.finishHWAdvanceOutcome(outcome)
	return machineResult{Err: outcome.err}
}

func (r *replica) advanceHWLocked() hwAdvanceOutcome {
	outcome := hwAdvanceOutcome{
		channelKey: r.state.ChannelKey,
		oldHW:      r.state.HW,
		leo:        r.state.LEO,
	}
	if r.closed || r.state.Role == channel.ReplicaRoleTombstoned {
		return outcome
	}
	checkpoint, candidate, err := r.nextHWCheckpointLocked()
	outcome.candidate = candidate
	if err != nil || checkpoint == nil {
		outcome.err = err
		if err != nil {
			outcome.progress = r.snapshotProgressLocked()
		}
		return outcome
	}
	if candidate <= r.state.HW {
		return outcome
	}
	if candidate > r.state.LEO {
		outcome.err = channel.ErrCorruptState
		outcome.progress = r.snapshotProgressLocked()
		return outcome
	}

	r.state.HW = candidate
	r.scheduleCheckpointLocked(*checkpoint)
	r.publishStateLocked()
	r.notifyReadyWaitersLocked()
	outcome.advanced = true
	outcome.newHW = candidate
	outcome.leo = r.state.LEO
	outcome.notify = r.onLeaderHWAdvance
	outcome.progress = r.snapshotProgressLocked()
	return outcome
}

func (r *replica) finishHWAdvanceOutcome(outcome hwAdvanceOutcome) {
	if outcome.err != nil {
		r.appendLogger().Warn("advance HW failed",
			wklog.Event("repl.diag.advance_hw_error"),
			wklog.String("channelKey", string(outcome.channelKey)),
			wklog.Uint64("hw", outcome.oldHW),
			wklog.Uint64("leo", outcome.leo),
			wklog.Any("progress", outcome.progress),
			wklog.Error(outcome.err),
		)
		return
	}
	if !outcome.advanced {
		return
	}
	r.appendLogger().Debug("HW advanced",
		wklog.Event("repl.diag.hw_advanced"),
		wklog.String("channelKey", string(outcome.channelKey)),
		wklog.Uint64("oldHW", outcome.oldHW),
		wklog.Uint64("newHW", outcome.newHW),
		wklog.Uint64("leo", outcome.leo),
		wklog.Any("progress", outcome.progress),
	)
	if outcome.notify != nil {
		outcome.notify()
	}
}

func (r *replica) applyReconcileProofCommand(cmd machineReconcileProofCommand) machineResult {
	proof := cmd.Proof

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.state.Role == channel.ReplicaRoleTombstoned {
		return machineResult{Err: channel.ErrTombstoned}
	}
	if r.state.Role != channel.ReplicaRoleLeader && r.state.Role != channel.ReplicaRoleFencedLeader {
		return machineResult{Err: channel.ErrNotLeader}
	}
	if proof.ChannelKey != r.state.ChannelKey {
		return machineResult{Err: channel.ErrStaleMeta}
	}
	if proof.Epoch != r.state.Epoch {
		return machineResult{Err: channel.ErrStaleMeta}
	}
	if proof.LeaderEpoch != r.meta.LeaderEpoch {
		return machineResult{Err: channel.ErrStaleMeta}
	}
	if proof.ReplicaID == 0 || proof.ReplicaID == r.localNode {
		return machineResult{Err: channel.ErrInvalidMeta}
	}
	if err := r.ensureReconcileLeaseLocked(); err != nil {
		return machineResult{Err: err}
	}
	matchOffset, err := r.reconcileMatchOffsetLocked(proof)
	if err != nil {
		return machineResult{Err: err}
	}

	progressAdvanced := false
	if current := r.progress[proof.ReplicaID]; matchOffset > current {
		r.setReplicaProgressLocked(proof.ReplicaID, matchOffset)
		progressAdvanced = true
	}
	if len(r.reconcilePending) == 0 {
		if progressAdvanced {
			r.publishStateLocked()
		}
		return machineResult{}
	}
	delete(r.reconcilePending, proof.ReplicaID)
	if len(r.reconcilePending) != 0 && !r.localTailFullyProvenLocked() {
		r.publishStateLocked()
		return machineResult{}
	}

	return r.completeLeaderReconcileLocked()
}

func (r *replica) applyCompleteReconcileCommand(cmd machineCompleteReconcileCommand) machineResult {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.state.Role == channel.ReplicaRoleTombstoned {
		return machineResult{Err: channel.ErrTombstoned}
	}
	if r.state.Role != channel.ReplicaRoleLeader && r.state.Role != channel.ReplicaRoleFencedLeader {
		return machineResult{Err: channel.ErrNotLeader}
	}
	if cmd.Meta.Key != r.state.ChannelKey {
		return machineResult{Err: channel.ErrStaleMeta}
	}
	if cmd.Meta.Epoch != r.state.Epoch {
		return machineResult{Err: channel.ErrStaleMeta}
	}
	if cmd.Meta.LeaderEpoch != r.meta.LeaderEpoch {
		return machineResult{Err: channel.ErrStaleMeta}
	}
	if err := r.ensureReconcileLeaseLocked(); err != nil {
		return machineResult{Err: err}
	}
	return r.completeLeaderReconcileLocked()
}

func (r *replica) seedLeaderProgressLocked(isr []channel.NodeID, leaderLEO, committedHW uint64) {
	r.progress = make(map[channel.NodeID]uint64, len(isr))
	for _, id := range isr {
		if id == r.localNode {
			r.progress[id] = leaderLEO
			continue
		}
		r.progress[id] = committedHW
	}
}

func (r *replica) setReplicaProgressLocked(replicaID channel.NodeID, matchOffset uint64) {
	if r.progress == nil {
		r.progress = make(map[channel.NodeID]uint64)
	}
	r.progress[replicaID] = matchOffset
}

func (r *replica) snapshotProgressLocked() map[uint64]uint64 {
	out := make(map[uint64]uint64, len(r.progress))
	for id, offset := range r.progress {
		out[uint64(id)] = offset
	}
	return out
}

func (r *replica) nextHWCheckpointLocked() (*channel.Checkpoint, uint64, error) {
	if r.state.Role != channel.ReplicaRoleLeader && r.state.Role != channel.ReplicaRoleFencedLeader {
		return nil, 0, nil
	}
	candidate, ok, err := quorumProgressCandidate(r.meta.ISR, r.progress, r.meta.MinISR, r.state.HW, r.state.LEO)
	if err != nil || !ok {
		return nil, 0, err
	}

	checkpoint := channel.Checkpoint{
		Epoch:          r.state.Epoch,
		LogStartOffset: r.state.LogStartOffset,
		HW:             candidate,
	}
	return &checkpoint, candidate, nil
}

func (r *replica) notifyReadyWaitersLocked() {
	if len(r.waiters) == 0 {
		return
	}

	remaining := r.waiters[:0]
	now := r.now()
	for _, waiter := range r.waiters {
		if r.state.HW >= waiter.target {
			waiter.result.NextCommitHW = r.state.HW
			sendtrace.Record(sendtrace.Event{
				Stage:      sendtrace.StageReplicaLeaderQuorumWait,
				At:         waiter.durableDoneAt,
				Duration:   sendtrace.Elapsed(waiter.durableDoneAt, now),
				NodeID:     uint64(r.localNode),
				ChannelKey: string(r.state.ChannelKey),
				RangeStart: waiter.rangeStart,
				RangeEnd:   waiter.rangeEnd,
			})
			if waiter.request != nil {
				r.completeAppendRequestLocked(waiter.request, waiter.result, nil)
			} else {
				r.completeAppendWaiter(waiter, waiter.result, nil)
			}
			continue
		}
		remaining = append(remaining, waiter)
	}
	r.waiters = remaining
}

func (r *replica) scheduleCheckpointLocked(checkpoint channel.Checkpoint) {
	if checkpoint.HW <= r.state.CheckpointHW {
		return
	}
	if (r.checkpointQueued || r.checkpointInFlight) && checkpoint.HW <= r.pendingCheckpoint.HW {
		return
	}
	r.pendingCheckpoint = checkpoint
	r.checkpointQueued = true
	r.emitCheckpointEffectLocked()
}

func (r *replica) scheduleCheckpointRetry() {
	go func() {
		timer := time.NewTimer(checkpointRetryDelay)
		defer timer.Stop()
		select {
		case <-timer.C:
			_ = r.submitLoopResult(context.Background(), machineCheckpointRetryEvent{})
		case <-r.stopCh:
		}
	}()
}

func (r *replica) divergenceStateLocked(fetchOffset, offsetEpoch, leaderLEO uint64) (uint64, *uint64, error) {
	decision := decideLineage(r.epochHistory, r.state.LogStartOffset, r.state.HW, leaderLEO, fetchOffset, offsetEpoch)
	return decision.matchOffset, decision.truncateTo, decision.err
}

func (r *replica) cursorMatchOffsetLocked(matchOffset, offsetEpoch uint64) (uint64, error) {
	decision := decideLineage(r.epochHistory, r.state.LogStartOffset, r.state.HW, r.state.LEO, matchOffset, offsetEpoch)
	return decision.matchOffset, decision.err
}
