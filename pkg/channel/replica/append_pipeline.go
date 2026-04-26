package replica

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
)

func (r *replica) applyAppendRequestCommand(cmd machineAppendRequestCommand) machineResult {
	req := cmd.Request
	if req == nil || req.waiter == nil {
		return machineResult{Err: channel.ErrInvalidArgument}
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if err := r.appendableLocked(); err != nil {
		return machineResult{Err: err}
	}
	if len(req.batch) == 0 {
		r.completeAppendRequestLocked(req, channel.CommitResult{
			BaseOffset:   r.state.LEO,
			NextCommitHW: r.state.HW,
		}, nil)
		return machineResult{}
	}

	r.nextEffectID++
	req.requestID = r.nextEffectID
	req.stage = appendRequestQueued
	req.waiter.request = req
	req.waiter.result = channel.CommitResult{RecordCount: len(req.batch)}
	if r.appendRequests == nil {
		r.appendRequests = make(map[uint64]*appendRequest)
	}
	r.appendRequests[req.requestID] = req
	r.appendPending = append(r.appendPending, req)
	r.maybeFlushAppendLocked()
	return machineResult{}
}

func (r *replica) applyAppendCancelCommand(cmd machineAppendCancelCommand) machineResult {
	req := cmd.Request
	if req == nil {
		return machineResult{}
	}
	err := cmd.Err
	if err == nil {
		err = context.Canceled
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.cancelAppendRequestLocked(req, err)
	return machineResult{}
}

func (r *replica) applyAppendFlushEvent() machineResult {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.appendFlushScheduled = false
	r.emitAppendBatchLocked()
	return machineResult{}
}

func (r *replica) maybeFlushAppendLocked() {
	if len(r.appendInFlightIDs) != 0 {
		return
	}
	if len(r.appendPending) == 0 {
		return
	}
	if r.shouldFlushAppendLocked() {
		r.appendFlushScheduled = false
		r.emitAppendBatchLocked()
		return
	}
	if !r.appendFlushScheduled {
		r.appendFlushScheduled = true
		r.scheduleAppendFlush()
	}
}

func (r *replica) shouldFlushAppendLocked() bool {
	if r.appendGroupCommit.maxWait <= 0 {
		return true
	}
	_, recordCount, byteCount := r.selectAppendBatchLocked()
	return recordCount >= r.appendGroupCommit.maxRecords || byteCount >= r.appendGroupCommit.maxBytes
}

func (r *replica) scheduleAppendFlush() {
	delay := r.appendGroupCommit.maxWait
	if delay <= 0 {
		delay = time.Millisecond
	}
	go func() {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-timer.C:
			_ = r.submitLoopResult(context.Background(), machineAppendFlushEvent{})
		case <-r.stopCh:
		}
	}()
}

func (r *replica) emitAppendBatchLocked() {
	if len(r.appendInFlightIDs) != 0 || len(r.appendPending) == 0 {
		return
	}
	if err := r.appendableLocked(); err != nil {
		r.failPendingAppendRequestsLocked(appendFailureForState(err))
		return
	}
	count, recordCount, _ := r.selectAppendBatchLocked()
	if count == 0 || recordCount == 0 {
		return
	}

	batch := make([]*appendRequest, count)
	copy(batch, r.appendPending[:count])
	copy(r.appendPending, r.appendPending[count:])
	r.appendPending = r.appendPending[:len(r.appendPending)-count]

	active := make([]*appendRequest, 0, len(batch))
	activeIDs := make([]uint64, 0, len(batch))
	records := make([]channel.Record, 0, recordCount)
	for _, req := range batch {
		if req.completed {
			continue
		}
		if err := req.ctx.Err(); err != nil {
			r.completeAppendRequestLocked(req, channel.CommitResult{}, err)
			continue
		}
		req.stage = appendRequestDurable
		active = append(active, req)
		activeIDs = append(activeIDs, req.requestID)
		records = append(records, req.batch...)
	}
	if len(active) == 0 {
		r.maybeFlushAppendLocked()
		return
	}

	r.nextEffectID++
	effectID := r.nextEffectID
	r.appendInFlightIDs = append([]uint64(nil), activeIDs...)
	r.appendInFlightEffectID = effectID
	effect := appendLeaderBatchEffect{
		EffectID:       effectID,
		RequestIDs:     append([]uint64(nil), activeIDs...),
		ChannelKey:     r.state.ChannelKey,
		Epoch:          r.state.Epoch,
		LeaderEpoch:    r.meta.LeaderEpoch,
		RoleGeneration: r.roleGeneration,
		LeaseUntil:     r.meta.LeaseUntil,
		Records:        cloneRecords(records),
		StartedAt:      r.now(),
	}
	select {
	case r.appendEffects <- effect:
	default:
		for _, req := range active {
			req.stage = appendRequestQueued
		}
		r.appendInFlightIDs = nil
		r.appendInFlightEffectID = 0
		r.appendPending = append(active, r.appendPending...)
		r.scheduleAppendFlush()
	}
}

func (r *replica) runAppendEffect(ctx context.Context, effect appendLeaderBatchEffect) {
	if ctx == nil {
		ctx = context.Background()
	}
	startedAt := effect.StartedAt
	if startedAt.IsZero() {
		startedAt = r.now()
	}
	base, newLEO, err := uint64(0), uint64(0), r.lockDurableMu(ctx)
	if err == nil {
		r.mu.Lock()
		err = r.validateAppendEffectFenceLocked(effect)
		r.mu.Unlock()
		if err == nil {
			base, newLEO, err = r.durable.AppendLeaderBatch(ctx, effect.Records)
		}
		r.durableMu.Unlock()
	}
	if err == nil {
		expectedLEO := base + uint64(len(effect.Records))
		if newLEO != expectedLEO {
			err = fmt.Errorf("%w: durable append new leo %d != expected %d", channel.ErrCorruptState, newLEO, expectedLEO)
		}
	}
	doneAt := r.now()
	_ = r.submitLoopResult(context.Background(), machineLeaderAppendCommittedEvent{
		EffectID:         effect.EffectID,
		ChannelKey:       effect.ChannelKey,
		Epoch:            effect.Epoch,
		LeaderEpoch:      effect.LeaderEpoch,
		RoleGeneration:   effect.RoleGeneration,
		LeaseUntil:       effect.LeaseUntil,
		RequestIDs:       append([]uint64(nil), effect.RequestIDs...),
		BaseOffset:       base,
		DurableStartedAt: startedAt,
		DoneAt:           doneAt,
		Err:              err,
	})
}

func (r *replica) validateAppendEffectFenceLocked(effect appendLeaderBatchEffect) error {
	if effect.EffectID == 0 || effect.EffectID != r.appendInFlightEffectID {
		return channel.ErrStaleMeta
	}
	if r.closed {
		return channel.ErrNotLeader
	}
	if r.state.Role == channel.ReplicaRoleTombstoned {
		return channel.ErrTombstoned
	}
	if effect.ChannelKey != r.state.ChannelKey || effect.Epoch != r.state.Epoch ||
		effect.LeaderEpoch != r.meta.LeaderEpoch || effect.RoleGeneration != r.roleGeneration {
		return channel.ErrStaleMeta
	}
	if r.state.Role == channel.ReplicaRoleFencedLeader {
		return channel.ErrLeaseExpired
	}
	if r.state.Role != channel.ReplicaRoleLeader {
		return channel.ErrNotLeader
	}
	if !r.state.CommitReady {
		return channel.ErrNotReady
	}
	if !r.now().Before(r.meta.LeaseUntil) || !r.now().Before(effect.LeaseUntil) {
		return channel.ErrLeaseExpired
	}
	if len(r.meta.ISR) < r.meta.MinISR {
		return channel.ErrInsufficientISR
	}
	return nil
}

func (r *replica) cancelAppendRequestLocked(req *appendRequest, err error) {
	if req.completed {
		return
	}
	if r.removePendingAppendLocked(req) {
		r.completeAppendRequestLocked(req, channel.CommitResult{}, err)
		return
	}
	if r.removeWaiterLocked(req.waiter) {
		r.completeAppendRequestLocked(req, channel.CommitResult{}, err)
		return
	}
	r.completeAppendRequestLocked(req, channel.CommitResult{}, err)
}

func (r *replica) failPendingAppendRequestsLocked(err error) {
	pending := r.appendPending
	r.appendPending = nil
	for _, req := range pending {
		r.completeAppendRequestLocked(req, channel.CommitResult{}, err)
		if req.requestID != 0 {
			delete(r.appendRequests, req.requestID)
		}
	}
}

func (r *replica) completeAppendRequestLocked(req *appendRequest, result channel.CommitResult, err error) {
	if req == nil || req.completed {
		return
	}
	req.completed = true
	previousStage := req.stage
	req.stage = appendRequestCompleted
	if previousStage != appendRequestDurable && req.requestID != 0 {
		delete(r.appendRequests, req.requestID)
	}
	r.completeAppendWaiter(req.waiter, result, err)
}

func appendFailureForState(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, channel.ErrTombstoned) {
		return channel.ErrTombstoned
	}
	if errors.Is(err, channel.ErrLeaseExpired) {
		return channel.ErrLeaseExpired
	}
	return err
}

func (r *replica) startAppendEffectWorker() {
	go func() {
		defer close(r.appendWorkerDone)
		for {
			select {
			case effect := <-r.appendEffects:
				ctx, cancel := context.WithCancel(context.Background())
				done := make(chan struct{})
				go func() {
					defer close(done)
					r.runAppendEffect(ctx, effect)
				}()
				select {
				case <-done:
					cancel()
				case <-r.stopCh:
					cancel()
					return
				}
			case <-r.stopCh:
				return
			}
		}
	}()
}

func (r *replica) appendableLocked() error {
	if r.closed {
		return channel.ErrNotLeader
	}
	if r.state.Role == channel.ReplicaRoleTombstoned {
		return channel.ErrTombstoned
	}
	if r.state.Role == channel.ReplicaRoleFencedLeader {
		return channel.ErrLeaseExpired
	}
	if r.state.Role != channel.ReplicaRoleLeader {
		return channel.ErrNotLeader
	}
	if !r.state.CommitReady {
		return channel.ErrNotReady
	}
	if !r.now().Before(r.meta.LeaseUntil) {
		r.state.Role = channel.ReplicaRoleFencedLeader
		r.roleGeneration++
		r.publishStateLocked()
		return channel.ErrLeaseExpired
	}
	if len(r.meta.ISR) < r.meta.MinISR {
		return channel.ErrInsufficientISR
	}
	return nil
}

func (r *replica) selectAppendBatchLocked() (int, int, int) {
	var (
		count       int
		recordCount int
		byteCount   int
	)
	for i, req := range r.appendPending {
		nextRecordCount := recordCount + len(req.batch)
		nextByteCount := byteCount + req.byteCount
		if i > 0 && (nextRecordCount > r.appendGroupCommit.maxRecords || nextByteCount > r.appendGroupCommit.maxBytes) {
			break
		}
		count = i + 1
		recordCount = nextRecordCount
		byteCount = nextByteCount
	}
	if count == 0 && len(r.appendPending) > 0 {
		return 1, len(r.appendPending[0].batch), r.appendPending[0].byteCount
	}
	return count, recordCount, byteCount
}

func (r *replica) removePendingAppendLocked(target *appendRequest) bool {
	for i, req := range r.appendPending {
		if req != target {
			continue
		}
		r.appendPending = append(r.appendPending[:i], r.appendPending[i+1:]...)
		return true
	}
	return false
}

func (r *replica) completeAppendWaiter(waiter *appendWaiter, result channel.CommitResult, err error) {
	if waiter == nil || waiter.ch == nil {
		return
	}
	select {
	case waiter.ch <- appendCompletion{result: result, err: err}:
	default:
	}
}

func (r *replica) removeWaiterLocked(target *appendWaiter) bool {
	for i, waiter := range r.waiters {
		if waiter != target {
			continue
		}
		r.waiters = append(r.waiters[:i], r.waiters[i+1:]...)
		return true
	}
	return false
}

func (r *replica) failOutstandingAppendWorkLocked(err error) {
	pending := r.appendPending
	r.appendPending = nil
	inFlightIDs := append([]uint64(nil), r.appendInFlightIDs...)
	r.appendInFlightIDs = nil
	r.appendInFlightEffectID = 0
	waiters := r.waiters
	r.waiters = nil

	for _, req := range pending {
		r.completeAppendRequestLocked(req, channel.CommitResult{}, err)
	}
	for _, requestID := range inFlightIDs {
		req := r.appendRequests[requestID]
		if req == nil {
			continue
		}
		r.completeAppendRequestLocked(req, channel.CommitResult{}, err)
		delete(r.appendRequests, requestID)
	}
	for _, waiter := range waiters {
		if waiter.request != nil {
			r.completeAppendRequestLocked(waiter.request, channel.CommitResult{}, err)
			if waiter.request.requestID != 0 {
				delete(r.appendRequests, waiter.request.requestID)
			}
			continue
		}
		r.completeAppendWaiter(waiter, channel.CommitResult{}, err)
	}
}
