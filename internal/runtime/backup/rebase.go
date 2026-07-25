package backup

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
)

// reconcileRebase either leaves a healthy pinned Slot on the continuous path
// or owns the complete retryable materialized-rebase path for that Slot.
func (e *CaptureEngine) reconcileRebase(
	ctx context.Context,
	current backupcontract.SlotFrontier,
) (backupcontract.SlotFrontier, bool, error) {
	if e.options.Rebase == nil {
		return current, false, nil
	}
	if current.Rebase != nil {
		next, err := e.runRebase(ctx, current)
		if errors.Is(err, ErrCaptureSourceCompacted) || errors.Is(err, ErrStaleCapture) {
			reason := current.Rebase.Reason
			if current.SourceSlotID != current.Lease.SlotID {
				reason = backupcontract.RebaseReasonSourceRemapped
			}
			rotated, rotateErr := e.rotatePendingRebase(ctx, current, reason)
			if rotateErr != nil {
				return backupcontract.SlotFrontier{}, true, rotateErr
			}
			next, err = e.runRebase(ctx, rotated)
		}
		return next, true, err
	}
	if current.SourceSlotID != current.Lease.SlotID {
		next, err := e.beginRebase(ctx, current, backupcontract.RebaseReasonSourceRemapped)
		if err != nil {
			return backupcontract.SlotFrontier{}, true, err
		}
		next, err = e.runRebase(ctx, next)
		return next, true, err
	}
	if reason := e.generationCompactionReason(current); reason != "" {
		next, err := e.beginRebase(ctx, current, reason)
		if err != nil {
			return backupcontract.SlotFrontier{}, true, err
		}
		next, err = e.runRebase(ctx, next)
		return next, true, err
	}
	observation, err := e.options.Rebase.Pins.Observe(
		ctx, current.HashSlot, current.Lease, current,
	)
	if observer, ok := e.options.Observer.(RebaseObserver); ok && err == nil {
		observer.SetBackupSourcePin(
			current.HashSlot, observation.Age, observation.PinnedBytes, observation.NodePinnedBytes,
		)
	}
	if err != nil {
		if errors.Is(err, ErrCaptureSourceCompacted) {
			next, beginErr := e.beginRebase(ctx, current, backupcontract.RebaseReasonSourceCompacted)
			if beginErr != nil {
				return backupcontract.SlotFrontier{}, true, beginErr
			}
			next, runErr := e.runRebase(ctx, next)
			return next, true, runErr
		}
		e.recordStatus(current.HashSlot, backupcontract.CaptureStateFailed, current, SourceWatermarks{}, "pin_observe")
		return backupcontract.SlotFrontier{}, true, err
	}
	if observation.Age < 0 {
		e.recordStatus(current.HashSlot, backupcontract.CaptureStateFailed, current, SourceWatermarks{}, "pin_observe")
		return backupcontract.SlotFrontier{}, true, fmt.Errorf("%w: negative source pin age", ErrInvalidCapture)
	}
	reason := ""
	switch {
	case observation.Age >= e.options.Rebase.Policy.MaxAge:
		reason = backupcontract.RebaseReasonPinAge
	case observation.NodePinnedBytes > e.options.Rebase.Policy.MaxNodeBytes && observation.NodeBudgetVictim:
		reason = backupcontract.RebaseReasonNodeByteBudget
	}
	if reason == "" {
		return current, false, nil
	}
	next, err := e.beginRebase(ctx, current, reason)
	if err != nil {
		return backupcontract.SlotFrontier{}, true, err
	}
	next, err = e.runRebase(ctx, next)
	return next, true, err
}

// rotatePendingRebase abandons one immutable target that cannot be resumed by
// the current lease. The last healthy generation and stream frontiers remain
// untouched while a fresh target key and epoch are durably fenced.
func (e *CaptureEngine) rotatePendingRebase(
	ctx context.Context,
	current backupcontract.SlotFrontier,
	reason string,
) (backupcontract.SlotFrontier, error) {
	if current.Rebase == nil || current.Revision == math.MaxUint64 {
		return backupcontract.SlotFrontier{}, ErrInvalidCapture
	}
	now := e.options.Clock.Now().UnixMilli()
	next := backupcontract.CloneSlotFrontier(current)
	next.Revision++
	next.Rebase = &backupcontract.SlotRebase{
		TargetGeneration:    fmt.Sprintf("rebase-%05d-%020d", current.HashSlot, next.Revision),
		Epoch:               next.Revision,
		Reason:              reason,
		StartedAtUnixMillis: now,
	}
	next.UpdatedAtUnixMillis = now
	if err := e.options.Frontiers.CompareAndSwap(
		ctx, current.Revision, current.Lease, next,
	); err != nil {
		e.recordRebaseFailure(current, reason, rebaseFailureCategory("rebase_rotate", err), err)
		return backupcontract.SlotFrontier{}, err
	}
	e.discardPendingSlot(current.HashSlot)
	if invalidator, ok := e.options.Source.(SourceStateInvalidator); ok {
		invalidator.InvalidateSourceState(current.HashSlot)
	}
	e.recordStatus(
		current.HashSlot, backupcontract.CaptureStateRebasing, next,
		SourceWatermarks{}, "",
	)
	return next, nil
}

func (e *CaptureEngine) beginRebase(
	ctx context.Context,
	current backupcontract.SlotFrontier,
	reason string,
) (backupcontract.SlotFrontier, error) {
	if current.Rebase != nil {
		return current, nil
	}
	if current.Revision == math.MaxUint64 {
		return backupcontract.SlotFrontier{}, fmt.Errorf("%w: frontier revision overflow", ErrInvalidCapture)
	}
	now := e.options.Clock.Now().UnixMilli()
	next := backupcontract.CloneSlotFrontier(current)
	next.Revision++
	next.Rebase = &backupcontract.SlotRebase{
		TargetGeneration: fmt.Sprintf("rebase-%05d-%020d", current.HashSlot, next.Revision),
		Epoch:            next.Revision,
		Reason:           reason, StartedAtUnixMillis: now,
	}
	next.UpdatedAtUnixMillis = now
	if err := e.options.Frontiers.CompareAndSwap(ctx, current.Revision, current.Lease, next); err != nil {
		e.recordRebaseFailure(current, reason, "rebase_begin", err)
		return backupcontract.SlotFrontier{}, err
	}
	e.discardPendingSlot(current.HashSlot)
	if invalidator, ok := e.options.Source.(SourceStateInvalidator); ok {
		invalidator.InvalidateSourceState(current.HashSlot)
	}
	e.recordStatus(current.HashSlot, backupcontract.CaptureStateRebasing, next, SourceWatermarks{}, "")
	return next, nil
}

func (e *CaptureEngine) runRebase(
	ctx context.Context,
	current backupcontract.SlotFrontier,
) (backupcontract.SlotFrontier, error) {
	if current.Rebase == nil {
		return backupcontract.SlotFrontier{}, ErrInvalidCapture
	}
	rebase := *current.Rebase
	cost, err := e.options.Rebase.CostPlanner.PlanGenerationCompaction(ctx, current)
	if err != nil || cost.IOBytes <= 0 || cost.NetworkBytes <= 0 {
		if err == nil {
			err = fmt.Errorf("%w: compaction cost plan is invalid", ErrInvalidCapture)
		}
		e.recordRebaseFailure(current, rebase.Reason, "compaction_plan", err)
		return backupcontract.SlotFrontier{}, err
	}
	if !e.options.Rebase.Budget.TryAcquire(cost) {
		e.recordRebaseFailure(current, rebase.Reason, "compaction_budget", ErrCompactionBudget)
		return backupcontract.SlotFrontier{}, ErrCompactionBudget
	}
	defer e.options.Rebase.Budget.Release(cost)
	released, err := e.options.Rebase.Pins.Release(ctx, current.HashSlot, current.Lease)
	if err != nil {
		e.recordRebaseFailure(current, rebase.Reason, rebaseFailureCategory("pin_release", err), err)
		return backupcontract.SlotFrontier{}, err
	}
	if observer, ok := e.options.Observer.(RebaseObserver); ok {
		observer.SetBackupSourcePin(current.HashSlot, 0, 0, released.NodePinnedBytes)
	}
	e.recordStatus(current.HashSlot, backupcontract.CaptureStateRebasing, current, SourceWatermarks{}, "")
	baseline, err := e.options.Rebase.Baselines.CaptureBaseline(
		ctx,
		current.HashSlot,
		rebase.TargetGeneration,
		rebase.Epoch,
		current.Lease,
		func(pinContext context.Context, cut uint64) error {
			pinned := backupcontract.CloneSlotFrontier(current)
			pinned.SourceSlotID = current.Lease.SlotID
			pinned.Metadata.SourceCursor = strconv.FormatUint(cut, 10)
			pinned.Metadata.SourceHighWatermark = cut
			_, pinErr := e.options.Rebase.Pins.Observe(
				pinContext, current.HashSlot, current.Lease, pinned,
			)
			return pinErr
		},
	)
	if err != nil {
		e.recordRebaseFailure(current, rebase.Reason, rebaseFailureCategory("rebase_capture", err), err)
		return backupcontract.SlotFrontier{}, err
	}
	if err := validateMaterializedBaseline(current.HashSlot, rebase.TargetGeneration, baseline); err != nil {
		e.recordRebaseFailure(current, rebase.Reason, "rebase_validate", err)
		return backupcontract.SlotFrontier{}, err
	}
	if err := e.options.Rebase.Validator.ValidateGenerationReplacement(ctx, current, baseline); err != nil {
		e.recordRebaseFailure(current, rebase.Reason, "rebase_audit", err)
		return backupcontract.SlotFrontier{}, err
	}
	if current.Revision == math.MaxUint64 {
		err := fmt.Errorf("%w: frontier revision overflow", ErrInvalidCapture)
		e.recordRebaseFailure(current, rebase.Reason, "rebase_promote", err)
		return backupcontract.SlotFrontier{}, err
	}
	next := backupcontract.CloneSlotFrontier(current)
	next.Revision++
	next.Generation = baseline.Generation
	next.GenerationStartedAtUnixMillis = e.options.Clock.Now().UnixMilli()
	next.Lease.Generation = baseline.Generation
	next.Lease.AcquiredAtUnixMillis = next.GenerationStartedAtUnixMillis
	next.SourceSlotID = next.Lease.SlotID
	next.SourcePinStartedAtUnixMillis = next.Lease.AcquiredAtUnixMillis
	next.Baseline = &baseline.Reference
	next.Rebase = nil
	next.LastPromotion = &backupcontract.SlotGenerationPromotion{
		PreviousGeneration:   current.Generation,
		Reason:               rebase.Reason,
		PromotedAtUnixMillis: next.GenerationStartedAtUnixMillis,
	}
	next.Metadata = baseline.Metadata
	next.Messages = baseline.Messages
	next.WatermarkAtUnixMillis = baseline.WatermarkAtUnixMillis
	next.UpdatedAtUnixMillis = next.Lease.AcquiredAtUnixMillis
	promoter := e.options.Frontiers.(SlotGenerationPromoter)
	if err := promoter.PromoteGeneration(ctx, current.Revision, current.Lease, next); err != nil {
		if invalidator, ok := e.options.Source.(SourceStateInvalidator); ok {
			invalidator.InvalidateSourceState(current.HashSlot)
		}
		e.recordRebaseFailure(current, rebase.Reason, rebaseFailureCategory("rebase_promote", err), err)
		return backupcontract.SlotFrontier{}, err
	}
	if invalidator, ok := e.options.Source.(SourceStateInvalidator); ok {
		invalidator.InvalidateSourceState(current.HashSlot)
	}
	if observer, ok := e.options.Observer.(RebaseObserver); ok {
		observer.ObserveBackupSlotRebase(
			current.HashSlot,
			rebase.Reason,
			time.Duration(maxInt64(0, next.UpdatedAtUnixMillis-rebase.StartedAtUnixMillis))*time.Millisecond,
			"",
		)
	}
	watermarks := SourceWatermarks{
		Metadata: SourceWatermark{Position: next.Metadata.SourceHighWatermark, CommittedAtUnixMillis: next.Metadata.WatermarkAtUnixMillis},
		Messages: SourceWatermark{Position: next.Messages.SourceHighWatermark, CommittedAtUnixMillis: next.Messages.WatermarkAtUnixMillis},
	}
	e.recordStatus(current.HashSlot, backupcontract.CaptureStateIdle, next, watermarks, "")
	return backupcontract.CloneSlotFrontier(next), nil
}

func validateMaterializedBaseline(hashSlot uint16, generation string, baseline MaterializedBaseline) error {
	if baseline.Generation != generation ||
		baseline.Reference.Partition.HashSlot != hashSlot ||
		baseline.Reference.Partition.Bytes <= 0 ||
		baseline.Reference.Partition.ObjectCount == 0 ||
		baseline.Reference.Partition.CiphertextBytes == 0 ||
		baseline.Reference.PlaintextBytes == 0 ||
		baseline.Metadata.Sequence != 0 || baseline.Metadata.Head != nil || baseline.Metadata.CursorHead != nil ||
		baseline.Messages.Sequence != 0 || baseline.Messages.Head != nil || baseline.Messages.CursorHead != nil ||
		baseline.Metadata.BaselineCursorHead != nil ||
		baseline.Messages.BaselineCursorHead == nil ||
		baseline.Metadata.SourceHighWatermark == 0 ||
		baseline.Metadata.WatermarkAtUnixMillis <= 0 ||
		baseline.Messages.WatermarkAtUnixMillis <= 0 ||
		baseline.WatermarkAtUnixMillis != olderPositiveTime(
			baseline.Metadata.WatermarkAtUnixMillis,
			baseline.Messages.WatermarkAtUnixMillis,
		) {
		return fmt.Errorf("%w: materialized baseline is invalid", ErrInvalidCapture)
	}
	if err := validateCommittedSegmentReference(*baseline.Messages.BaselineCursorHead); err != nil {
		return err
	}
	return nil
}

func (e *CaptureEngine) generationCompactionReason(frontier backupcontract.SlotFrontier) string {
	policy := e.options.Rebase.Policy
	deltaBytes := saturatingAddUint64(
		frontier.Metadata.CapturedPlaintextBytes,
		frontier.Messages.CapturedPlaintextBytes,
	)
	byteLimit := policy.MaxDeltaBytes
	if frontier.Baseline != nil && frontier.Baseline.PlaintextBytes > 0 &&
		frontier.Baseline.PlaintextBytes < byteLimit {
		byteLimit = frontier.Baseline.PlaintextBytes
	}
	switch {
	case deltaBytes >= byteLimit:
		return backupcontract.RebaseReasonGenerationBytes
	case generationSegmentCount(frontier) >= policy.MaxSegments:
		return backupcontract.RebaseReasonGenerationSegments
	}
	startedAt := frontier.GenerationStartedAtUnixMillis
	if startedAt <= 0 {
		startedAt = frontier.Lease.AcquiredAtUnixMillis
	}
	if startedAt > 0 &&
		e.options.Clock.Now().Sub(time.UnixMilli(startedAt)) >= policy.MaxGenerationAge {
		return backupcontract.RebaseReasonGenerationAge
	}
	return ""
}

func generationSegmentCount(frontier backupcontract.SlotFrontier) uint64 {
	if frontier.Messages.Sequence > (math.MaxUint64-frontier.Metadata.Sequence)/2 {
		return math.MaxUint64
	}
	return frontier.Metadata.Sequence + 2*frontier.Messages.Sequence
}

func saturatingAddUint64(left, right uint64) uint64 {
	if left > math.MaxUint64-right {
		return math.MaxUint64
	}
	return left + right
}

func (e *CaptureEngine) recordRebaseFailure(
	frontier backupcontract.SlotFrontier,
	reason string,
	category string,
	_ error,
) {
	e.recordStatus(frontier.HashSlot, backupcontract.CaptureStateRebasing, frontier, SourceWatermarks{}, category)
	if observer, ok := e.options.Observer.(RebaseObserver); ok {
		now := e.options.Clock.Now().UnixMilli()
		started := now
		if frontier.Rebase != nil {
			started = frontier.Rebase.StartedAtUnixMillis
		}
		observer.ObserveBackupSlotRebase(
			frontier.HashSlot,
			reason,
			time.Duration(maxInt64(0, now-started))*time.Millisecond,
			category,
		)
	}
}

func rebaseFailureCategory(category string, err error) string {
	if errors.Is(err, ErrCaptureLeaseFenced) || errors.Is(err, ErrCaptureNotLeader) {
		return "rebase_fenced"
	}
	return category
}

func (e *CaptureEngine) discardPendingSlot(hashSlot uint16) {
	e.pendingMu.Lock()
	var released int64
	for key, accumulator := range e.pending {
		if key.hashSlot != hashSlot {
			continue
		}
		delete(e.pending, key)
		if accumulator != nil {
			released += accumulator.reservedBytes
			accumulator.reservedBytes = 0
		}
	}
	e.pendingMu.Unlock()
	if released > 0 {
		e.options.MemoryBudget.Release(released)
	}
	e.notifyPendingChanged()
}

func maxInt64(left, right int64) int64 {
	if left > right {
		return left
	}
	return right
}
