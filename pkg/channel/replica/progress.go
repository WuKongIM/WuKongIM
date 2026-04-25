package replica

import (
	"context"
	"slices"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/observability/sendtrace"
	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

const smallISRProgressBufferSize = 8
const checkpointRetryDelay = 10 * time.Millisecond

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

func (r *replica) startAdvancePublisher() {
	go func() {
		defer close(r.advanceDone)
		for {
			select {
			case <-r.advanceSignal:
				for {
					advanced, candidate, err := r.advanceHWOnce()
					if err != nil {
						r.failWaitersUpTo(candidate, err)
						break
					}
					if !advanced {
						break
					}
				}
			case <-r.stopCh:
				return
			}
		}
	}()
}

func (r *replica) signalAdvanceHW() {
	select {
	case r.advanceSignal <- struct{}{}:
	default:
	}
}

func (r *replica) advanceHWOnce() (bool, uint64, error) {
	r.advanceMu.Lock()
	defer r.advanceMu.Unlock()

	var notifyLeaderHWAdvance func()
	r.mu.Lock()
	checkpoint, candidate, err := r.nextHWCheckpointLocked()
	if err != nil || checkpoint == nil {
		progress := r.snapshotProgressLocked()
		r.mu.Unlock()
		if err != nil {
			r.appendLogger().Warn("advance HW failed",
				wklog.Event("repl.diag.advance_hw_error"),
				wklog.String("channelKey", string(r.state.ChannelKey)),
				wklog.Uint64("hw", r.state.HW),
				wklog.Uint64("leo", r.state.LEO),
				wklog.Any("progress", progress),
				wklog.Error(err),
			)
		}
		return checkpoint != nil, candidate, err
	}
	if candidate <= r.state.HW {
		r.mu.Unlock()
		return true, candidate, nil
	}
	if candidate > r.state.LEO {
		r.mu.Unlock()
		return true, candidate, channel.ErrCorruptState
	}

	oldHW := r.state.HW
	r.state.HW = candidate
	r.notifyReadyWaitersLocked()
	r.scheduleCheckpointLocked(*checkpoint)
	r.publishStateLocked()
	notifyLeaderHWAdvance = r.onLeaderHWAdvance
	progress := r.snapshotProgressLocked()
	r.mu.Unlock()
	r.appendLogger().Debug("HW advanced",
		wklog.Event("repl.diag.hw_advanced"),
		wklog.String("channelKey", string(r.state.ChannelKey)),
		wklog.Uint64("oldHW", oldHW),
		wklog.Uint64("newHW", candidate),
		wklog.Uint64("leo", r.state.LEO),
		wklog.Any("progress", progress),
	)
	if notifyLeaderHWAdvance != nil {
		notifyLeaderHWAdvance()
	}
	return true, candidate, nil
}

func (r *replica) nextHWCheckpointLocked() (*channel.Checkpoint, uint64, error) {
	if r.state.Role != channel.ReplicaRoleLeader && r.state.Role != channel.ReplicaRoleFencedLeader {
		return nil, 0, nil
	}
	if len(r.meta.ISR) == 0 || r.meta.MinISR == 0 {
		return nil, 0, nil
	}

	var smallMatches [smallISRProgressBufferSize]uint64
	matches := smallMatches[:0]
	if len(r.meta.ISR) > smallISRProgressBufferSize {
		matches = make([]uint64, 0, len(r.meta.ISR))
	}
	for _, id := range r.meta.ISR {
		matches = append(matches, r.progress[id])
	}
	if len(matches) < r.meta.MinISR {
		return nil, 0, nil
	}

	slices.Sort(matches)
	candidate := matches[len(matches)-r.meta.MinISR]
	if candidate <= r.state.HW {
		return nil, 0, nil
	}
	if candidate > r.state.LEO {
		return nil, 0, channel.ErrCorruptState
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
			r.completeAppendWaiter(waiter, waiter.result, nil)
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
	r.signalCheckpoint()
}

func (r *replica) startCheckpointPublisher() {
	go func() {
		defer close(r.checkpointDone)
		for {
			select {
			case <-r.checkpointSignal:
				for {
					checkpoint, ok := r.nextPendingCheckpoint()
					if !ok {
						break
					}
					err := r.checkpoints.Store(checkpoint)
					if r.finishPendingCheckpoint(checkpoint, err) {
						r.scheduleCheckpointRetry()
						break
					}
				}
			case <-r.stopCh:
				return
			}
		}
	}()
}

func (r *replica) signalCheckpoint() {
	select {
	case r.checkpointSignal <- struct{}{}:
	default:
	}
}

func (r *replica) scheduleCheckpointRetry() {
	go func() {
		timer := time.NewTimer(checkpointRetryDelay)
		defer timer.Stop()
		select {
		case <-timer.C:
			r.signalCheckpoint()
		case <-r.stopCh:
		}
	}()
}

func (r *replica) nextPendingCheckpoint() (channel.Checkpoint, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.checkpointQueued || r.checkpointInFlight {
		return channel.Checkpoint{}, false
	}
	checkpoint := r.pendingCheckpoint
	r.checkpointInFlight = true
	return checkpoint, true
}

func (r *replica) finishPendingCheckpoint(checkpoint channel.Checkpoint, err error) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.checkpointInFlight = false
	if err != nil {
		r.checkpointQueued = r.pendingCheckpoint.HW > r.state.CheckpointHW
		r.state.CommitReady = false
		r.publishStateLocked()
		return r.checkpointQueued
	}

	if checkpoint.HW > r.state.CheckpointHW {
		r.state.CheckpointHW = checkpoint.HW
	}
	r.checkpointQueued = r.pendingCheckpoint.HW > r.state.CheckpointHW
	if !r.state.CommitReady && len(r.reconcilePending) == 0 && !r.checkpointQueued && r.state.CheckpointHW >= r.state.HW {
		r.state.CommitReady = true
	}
	r.publishStateLocked()
	return false
}

func (r *replica) failWaitersUpTo(target uint64, err error) {
	if target == 0 || err == nil {
		return
	}

	r.mu.Lock()
	if len(r.waiters) == 0 {
		r.mu.Unlock()
		return
	}

	ready := make([]*appendWaiter, 0, len(r.waiters))
	remaining := r.waiters[:0]
	for _, waiter := range r.waiters {
		if waiter.target <= target {
			ready = append(ready, waiter)
			continue
		}
		remaining = append(remaining, waiter)
	}
	r.waiters = remaining
	r.mu.Unlock()

	r.completeAppendWaiters(ready, err)
}

func (r *replica) divergenceStateLocked(fetchOffset, offsetEpoch, leaderLEO uint64) (uint64, *uint64) {
	if len(r.epochHistory) == 0 || offsetEpoch == 0 {
		return fetchOffset, nil
	}

	matchOffset := uint64(0)
	index := -1
	for i, point := range r.epochHistory {
		if point.Epoch <= offsetEpoch {
			index = i
			continue
		}
		break
	}
	if index >= 0 {
		matchOffset = leaderLEO
		if index+1 < len(r.epochHistory) {
			matchOffset = r.epochHistory[index+1].StartOffset
		}
	}
	if fetchOffset > matchOffset {
		truncateTo := matchOffset
		return matchOffset, &truncateTo
	}
	return fetchOffset, nil
}

func (r *replica) ApplyFollowerCursor(_ context.Context, req channel.ReplicaFollowerCursorUpdate) error {
	r.mu.Lock()

	if r.state.Role == channel.ReplicaRoleTombstoned {
		r.mu.Unlock()
		return channel.ErrTombstoned
	}
	if r.state.Role != channel.ReplicaRoleLeader && r.state.Role != channel.ReplicaRoleFencedLeader {
		r.mu.Unlock()
		return channel.ErrNotLeader
	}
	if req.ChannelKey != "" && req.ChannelKey != r.state.ChannelKey {
		r.mu.Unlock()
		return channel.ErrStaleMeta
	}
	if req.Epoch != r.state.Epoch {
		r.mu.Unlock()
		return channel.ErrStaleMeta
	}
	if req.ReplicaID == 0 {
		r.mu.Unlock()
		return channel.ErrInvalidMeta
	}
	current := r.progress[req.ReplicaID]
	if req.MatchOffset <= current {
		r.mu.Unlock()
		r.appendLogger().Debug("follower cursor stale, skipped",
			wklog.Event("repl.diag.cursor_stale"),
			wklog.String("channelKey", string(r.state.ChannelKey)),
			wklog.Uint64("replicaID", uint64(req.ReplicaID)),
			wklog.Uint64("matchOffset", req.MatchOffset),
			wklog.Uint64("currentProgress", current),
		)
		return nil
	}
	if req.MatchOffset > r.state.LEO {
		r.mu.Unlock()
		return channel.ErrCorruptState
	}
	r.setReplicaProgressLocked(req.ReplicaID, req.MatchOffset)
	progress := r.snapshotProgressLocked()
	r.publishStateLocked()
	r.mu.Unlock()

	r.appendLogger().Debug("follower cursor applied",
		wklog.Event("repl.diag.cursor_applied"),
		wklog.String("channelKey", string(r.state.ChannelKey)),
		wklog.Uint64("replicaID", uint64(req.ReplicaID)),
		wklog.Uint64("matchOffset", req.MatchOffset),
		wklog.Uint64("oldProgress", current),
		wklog.Uint64("hw", r.state.HW),
		wklog.Uint64("leo", r.state.LEO),
		wklog.Any("progress", progress),
	)
	r.signalAdvanceHW()
	return nil
}

func (r *replica) ApplyProgressAck(ctx context.Context, req channel.ReplicaProgressAckRequest) error {
	return r.ApplyFollowerCursor(ctx, channel.ReplicaFollowerCursorUpdate{
		ChannelKey:  req.ChannelKey,
		Epoch:       req.Epoch,
		ReplicaID:   req.ReplicaID,
		MatchOffset: req.MatchOffset,
	})
}
