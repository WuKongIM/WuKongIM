package replica

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/legacy/channel"
	"github.com/WuKongIM/WuKongIM/pkg/observability/sendtrace"
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
	if r.loop != nil {
		r.loop.scheduleResult(delay, machineAppendFlushEvent{})
	}
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

	batch := r.appendPending[:count]
	copy(r.appendPending, r.appendPending[count:])
	r.appendPending = r.appendPending[:len(r.appendPending)-count]

	active := r.appendInFlightRequests[:0]
	activeIDs := r.appendInFlightIDs[:0]
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
	r.appendInFlightRequests = active
	r.appendInFlightIDs = activeIDs
	r.appendInFlightEffectID = effectID
	effect := appendLeaderBatchEffect{
		EffectID:       effectID,
		RequestIDs:     activeIDs,
		ChannelKey:     r.state.ChannelKey,
		Epoch:          r.state.Epoch,
		LeaderEpoch:    r.meta.LeaderEpoch,
		RoleGeneration: r.roleGeneration,
		LeaseUntil:     r.meta.LeaseUntil,
		// Records reuses loop-owned payloads from appendRequest.batch; the facade
		// already deep-copied caller records before they entered the loop.
		Records:   records,
		StartedAt: r.now(),
	}
	if r.executionPool != nil {
		if err := r.executionPool.submitAppendEffect(context.Background(), r, effect); err == nil {
			return
		}
		for _, req := range active {
			req.stage = appendRequestQueued
		}
		r.appendInFlightRequests = active[:0]
		r.appendInFlightIDs = r.appendInFlightIDs[:0]
		r.appendInFlightEffectID = 0
		r.appendPending = append(active, r.appendPending...)
		r.scheduleAppendFlush()
		return
	}
	select {
	case r.appendEffects <- effect:
	default:
		for _, req := range active {
			req.stage = appendRequestQueued
		}
		r.appendInFlightRequests = active[:0]
		r.appendInFlightIDs = r.appendInFlightIDs[:0]
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
	recordCount, byteCount := appendEffectStats(effect.Records)
	lockStartedAt := r.now()
	base, newLEO, err := uint64(0), uint64(0), r.lockDurableMu(ctx)
	lockDoneAt := r.now()
	sendtrace.Record(sendtrace.Event{
		Stage:       sendtrace.StageReplicaLeaderDurableMuWait,
		At:          lockStartedAt,
		Duration:    sendtrace.Elapsed(lockStartedAt, lockDoneAt),
		NodeID:      uint64(r.localNode),
		ChannelKey:  string(effect.ChannelKey),
		RecordCount: recordCount,
		ByteCount:   byteCount,
		Result:      sendTraceResultFromError(err),
		ErrorCode:   sendTraceErrorCode(err),
		Error:       shortTraceError(err),
	})
	if err == nil {
		r.mu.Lock()
		err = r.validateAppendEffectFenceLocked(effect)
		r.mu.Unlock()
		if err == nil {
			appendStartedAt := r.now()
			base, newLEO, err = r.durable.AppendLeaderBatch(ctx, effect.Records)
			sendtrace.Record(sendtrace.Event{
				Stage:       sendtrace.StageReplicaLeaderDurableAppend,
				At:          appendStartedAt,
				Duration:    sendtrace.Elapsed(appendStartedAt, r.now()),
				NodeID:      uint64(r.localNode),
				ChannelKey:  string(effect.ChannelKey),
				RecordCount: recordCount,
				ByteCount:   byteCount,
				Result:      sendTraceResultFromError(err),
				ErrorCode:   sendTraceErrorCode(err),
				Error:       shortTraceError(err),
			})
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
		RequestIDs:       effect.RequestIDs,
		BaseOffset:       base,
		DurableStartedAt: startedAt,
		DoneAt:           doneAt,
		Err:              err,
	})
}

func appendEffectStats(records []channel.Record) (recordCount int, byteCount int) {
	recordCount = len(records)
	for _, record := range records {
		if record.SizeBytes > 0 {
			byteCount += record.SizeBytes
			continue
		}
		byteCount += len(record.Payload)
	}
	return recordCount, byteCount
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
	if r.pendingLeaderEpochEffectID != 0 {
		return channel.ErrNotReady
	}
	if !r.state.CommitReady {
		return channel.ErrNotReady
	}
	if !r.now().Before(r.meta.LeaseUntil) {
		return channel.ErrLeaseExpired
	}
	if r.meta.WriteFence.BlocksAppend() {
		return channel.ErrWriteFenced
	}
	if r.drainedFenceBlocksAppendLocked() {
		return channel.ErrWriteFenced
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
		r.completeAndDeleteAppendRequestLocked(req, channel.CommitResult{}, err)
	}
}

func (r *replica) completeAppendRequestLocked(req *appendRequest, result channel.CommitResult, err error) {
	if req == nil || req.completed {
		return
	}
	req.completed = true
	previousStage := req.stage
	req.stage = appendRequestCompleted
	if req.requestID != 0 && (previousStage != appendRequestDurable || err == nil) {
		delete(r.appendRequests, req.requestID)
	}
	r.completeAppendWaiter(req.waiter, result, err)
}

// completeAndDeleteAppendRequestLocked captures the request ID before
// publishing completion because successful callers may recycle req immediately.
func (r *replica) completeAndDeleteAppendRequestLocked(req *appendRequest, result channel.CommitResult, err error) {
	if req == nil {
		return
	}
	requestID := req.requestID
	r.completeAppendRequestLocked(req, result, err)
	if requestID != 0 {
		delete(r.appendRequests, requestID)
	}
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
	if r.pendingLeaderEpochEffectID != 0 {
		return channel.ErrNotReady
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
	if r.meta.WriteFence.BlocksAppend() {
		return channel.ErrWriteFenced
	}
	if r.drainedFenceBlocksAppendLocked() {
		return channel.ErrWriteFenced
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
	inFlightRequests := r.appendInFlightRequests
	if len(inFlightRequests) == 0 && len(r.appendInFlightIDs) > 0 {
		inFlightRequests = r.appendRequestsByIDsLocked(r.appendInFlightIDs)
	}
	r.appendInFlightRequests = nil
	r.appendInFlightIDs = nil
	r.appendInFlightEffectID = 0
	waiters := r.waiters
	r.waiters = nil

	for _, req := range pending {
		r.completeAndDeleteAppendRequestLocked(req, channel.CommitResult{}, err)
	}
	for _, req := range inFlightRequests {
		if req == nil {
			continue
		}
		r.completeAndDeleteAppendRequestLocked(req, channel.CommitResult{}, err)
	}
	for _, waiter := range waiters {
		if req := waiter.request; req != nil {
			r.completeAndDeleteAppendRequestLocked(req, channel.CommitResult{}, err)
			continue
		}
		r.completeAppendWaiter(waiter, channel.CommitResult{}, err)
	}
}
