package replica

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
)

// durableView is the validated durable state loaded from the split stores.
type durableView struct {
	Checkpoint   channel.Checkpoint
	EpochHistory []channel.EpochPoint
	LEO          uint64
}

// durableReplicaStore groups all durable mutations needed by a channel replica.
//
// The current implementation wraps split stores for compatibility. Those stores
// cannot provide true multi-object atomicity, so recovery validates obviously
// unsafe partial states instead of silently accepting them.
type durableReplicaStore interface {
	Recover(ctx context.Context) (durableView, error)
	BeginEpoch(ctx context.Context, point channel.EpochPoint, expectedLEO uint64) error
	AppendLeaderBatch(ctx context.Context, records []channel.Record) (oldLEO uint64, newLEO uint64, err error)
	ApplyFollowerBatch(ctx context.Context, req channel.ApplyFetchStoreRequest, epochPoint *channel.EpochPoint) (newLEO uint64, err error)
	TruncateLogAndHistory(ctx context.Context, to uint64) error
	StoreCheckpointMonotonic(ctx context.Context, checkpoint channel.Checkpoint, visibleHW uint64, leo uint64) error
	InstallSnapshotAtomically(ctx context.Context, snap channel.Snapshot, checkpoint channel.Checkpoint, epochPoint channel.EpochPoint) (leo uint64, err error)
}

type splitDurableReplicaStore struct {
	log        LogStore
	checkpoint CheckpointStore
	applyFetch ApplyFetchStore
	history    EpochHistoryStore
	snapshots  SnapshotApplier
}

// contextSyncLogStore is an optional LogStore extension for cancellable fsync.
type contextSyncLogStore interface {
	SyncContext(ctx context.Context) error
}

// combinedApplyFetchStore is an optional private extension for stores that can
// persist fetched records, checkpoint, and epoch history in one durable step.
type combinedApplyFetchStore interface {
	StoreApplyFetchWithEpoch(req channel.ApplyFetchStoreRequest, epochPoint *channel.EpochPoint) (leo uint64, err error)
}

// combinedBeginEpochStore is implemented by production stores that fence and
// publish epoch boundaries against the current durable log end.
type combinedBeginEpochStore interface {
	BeginEpoch(ctx context.Context, point channel.EpochPoint, expectedLEO uint64) error
}

// combinedTruncateLogHistoryStore is implemented by stores that can truncate
// log rows and epoch history in one durable mutation.
type combinedTruncateLogHistoryStore interface {
	TruncateLogAndHistory(ctx context.Context, to uint64) error
}

// combinedCheckpointStore is implemented by stores that validate checkpoint
// monotonicity at the durable write boundary.
type combinedCheckpointStore interface {
	StoreCheckpointMonotonic(ctx context.Context, checkpoint channel.Checkpoint, visibleHW uint64, leo uint64) error
}

// combinedSnapshotStore is implemented by stores that publish snapshot payload,
// checkpoint, and epoch history in one durable mutation.
type combinedSnapshotStore interface {
	InstallSnapshotAtomically(ctx context.Context, snap channel.Snapshot, checkpoint channel.Checkpoint, epochPoint channel.EpochPoint) (leo uint64, err error)
}

// snapshotLoader is an optional private extension used to validate that a
// snapshot checkpoint has a matching durable payload during recovery.
type snapshotLoader interface {
	LoadSnapshot(ctx context.Context) (channel.Snapshot, error)
}

// snapshotPayloadLoader is implemented by stores that can at least report
// whether a durable snapshot payload exists during recovery.
type snapshotPayloadLoader interface {
	LoadSnapshotPayload(ctx context.Context) ([]byte, error)
}

// newDurableReplicaStore adapts the existing split store interfaces into the
// durable replica contract without changing ReplicaConfig's public shape.
func newDurableReplicaStore(log LogStore, checkpoint CheckpointStore, applyFetch ApplyFetchStore, history EpochHistoryStore, snapshots SnapshotApplier) durableReplicaStore {
	return &splitDurableReplicaStore{
		log:        log,
		checkpoint: checkpoint,
		applyFetch: applyFetch,
		history:    history,
		snapshots:  snapshots,
	}
}

func (s *splitDurableReplicaStore) Recover(ctx context.Context) (durableView, error) {
	if err := contextError(ctx); err != nil {
		return durableView{}, err
	}

	checkpoint, err := s.checkpoint.Load()
	if errors.Is(err, channel.ErrEmptyState) {
		checkpoint = channel.Checkpoint{}
	} else if err != nil {
		return durableView{}, err
	}

	history, err := s.history.Load()
	if errors.Is(err, channel.ErrEmptyState) {
		history = nil
	} else if err != nil {
		return durableView{}, err
	}

	var (
		snapshot       *channel.Snapshot
		snapshotKnown  bool
		snapshotExists bool
	)
	if loader, ok := s.snapshots.(snapshotLoader); ok {
		snapshotKnown = true
		loaded, err := loader.LoadSnapshot(ctx)
		if errors.Is(err, channel.ErrEmptyState) {
			snapshot = nil
		} else if err != nil {
			return durableView{}, err
		} else {
			snapshot = &loaded
			snapshotExists = true
		}
	} else if loader, ok := s.snapshots.(snapshotPayloadLoader); ok {
		snapshotKnown = true
		payload, err := loader.LoadSnapshotPayload(ctx)
		if errors.Is(err, channel.ErrEmptyState) {
			snapshotExists = false
		} else if err != nil {
			return durableView{}, err
		} else {
			snapshotExists = payload != nil
		}
	}

	leo := s.log.LEO()
	if err := validateDurableView(checkpoint, history, leo, snapshot, snapshotKnown, snapshotExists); err != nil {
		return durableView{}, err
	}

	return durableView{
		Checkpoint:   checkpoint,
		EpochHistory: append([]channel.EpochPoint(nil), history...),
		LEO:          leo,
	}, nil
}

func (s *splitDurableReplicaStore) BeginEpoch(ctx context.Context, point channel.EpochPoint, expectedLEO uint64) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	if combined, ok := s.log.(combinedBeginEpochStore); ok {
		return combined.BeginEpoch(ctx, point, expectedLEO)
	}
	if point.StartOffset != expectedLEO {
		return fmt.Errorf("%w: epoch start %d != expected leo %d", channel.ErrCorruptState, point.StartOffset, expectedLEO)
	}
	if leo := s.log.LEO(); leo != expectedLEO {
		return fmt.Errorf("%w: leo %d != expected leo %d", channel.ErrCorruptState, leo, expectedLEO)
	}

	history, err := s.history.Load()
	if errors.Is(err, channel.ErrEmptyState) {
		history = nil
	} else if err != nil {
		return err
	}
	if err := validateEpochHistory(history); err != nil {
		return err
	}
	if len(history) > 0 {
		last := history[len(history)-1]
		if last.Epoch == point.Epoch && last.StartOffset == point.StartOffset {
			return nil
		}
	}
	next := append(append([]channel.EpochPoint(nil), history...), point)
	if err := validateEpochHistory(next); err != nil {
		return err
	}
	return s.history.Append(point)
}

func (s *splitDurableReplicaStore) AppendLeaderBatch(ctx context.Context, records []channel.Record) (uint64, uint64, error) {
	if err := contextError(ctx); err != nil {
		return 0, 0, err
	}

	oldLEO := s.log.LEO()
	if len(records) == 0 {
		return oldLEO, oldLEO, nil
	}
	base, err := s.log.Append(records)
	if err != nil {
		return oldLEO, oldLEO, err
	}
	if base != oldLEO {
		return oldLEO, oldLEO, fmt.Errorf("%w: append base %d != old leo %d", channel.ErrCorruptState, base, oldLEO)
	}
	if err := syncLog(ctx, s.log); err != nil {
		return oldLEO, oldLEO, err
	}
	newLEO := s.log.LEO()
	expectedLEO := oldLEO + uint64(len(records))
	if newLEO != expectedLEO {
		return oldLEO, newLEO, fmt.Errorf("%w: leo %d != expected leo %d", channel.ErrCorruptState, newLEO, expectedLEO)
	}
	return oldLEO, newLEO, nil
}

func (s *splitDurableReplicaStore) ApplyFollowerBatch(ctx context.Context, req channel.ApplyFetchStoreRequest, epochPoint *channel.EpochPoint) (uint64, error) {
	if err := contextError(ctx); err != nil {
		return 0, err
	}
	baseLEO := s.log.LEO()
	expectedLEO := baseLEO + uint64(len(req.Records))
	if req.Checkpoint != nil && req.Checkpoint.HW < req.PreviousCommittedHW {
		return 0, channel.ErrCorruptState
	}
	if req.Checkpoint != nil {
		if err := validateCheckpoint(*req.Checkpoint); err != nil {
			return 0, err
		}
		if req.Checkpoint.HW > expectedLEO {
			return 0, fmt.Errorf("%w: checkpoint hw %d > expected leo %d", channel.ErrCorruptState, req.Checkpoint.HW, expectedLEO)
		}
		if err := s.validateCheckpointMonotonic(req.Checkpoint); err != nil {
			return 0, err
		}
	}

	if epochPoint != nil {
		if epochPoint.StartOffset != baseLEO {
			return 0, channel.ErrCorruptState
		}
		if combined, ok := s.applyFetch.(combinedApplyFetchStore); ok {
			leo, err := combined.StoreApplyFetchWithEpoch(req, epochPoint)
			if err != nil {
				return 0, err
			}
			if req.Checkpoint != nil && req.Checkpoint.HW > leo {
				return 0, channel.ErrCorruptState
			}
			return leo, nil
		}
		// Split-store compatibility path: publish the epoch boundary first so
		// any later records are recoverable with lineage even if checkpoint
		// publication fails. Production ChannelStore uses the combined path.
		if err := s.BeginEpoch(ctx, *epochPoint, baseLEO); err != nil {
			return 0, err
		}
	}

	if s.applyFetch != nil {
		leo, err := s.applyFetch.StoreApplyFetch(req)
		if err != nil {
			return 0, err
		}
		if req.Checkpoint != nil && req.Checkpoint.HW > leo {
			return 0, channel.ErrCorruptState
		}
		return leo, nil
	}

	// Compatibility-only fallback for stores that have not implemented
	// ApplyFetchStore. This is not atomic across log and checkpoint stores; unsafe
	// partial states are rejected by Recover.
	oldLEO, newLEO, err := s.AppendLeaderBatch(ctx, req.Records)
	if err != nil {
		return 0, err
	}
	if len(req.Records) > 0 && oldLEO+uint64(len(req.Records)) != newLEO {
		return 0, channel.ErrCorruptState
	}
	if req.Checkpoint != nil {
		if err := s.StoreCheckpointMonotonic(ctx, *req.Checkpoint, req.Checkpoint.HW, newLEO); err != nil {
			return 0, err
		}
	}
	return newLEO, nil
}

func (s *splitDurableReplicaStore) TruncateLogAndHistory(ctx context.Context, to uint64) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	if combined, ok := s.log.(combinedTruncateLogHistoryStore); ok {
		return combined.TruncateLogAndHistory(ctx, to)
	}
	if err := s.log.Truncate(to); err != nil {
		return err
	}
	if err := syncLog(ctx, s.log); err != nil {
		return err
	}
	if leo := s.log.LEO(); leo != to {
		return fmt.Errorf("%w: leo %d != truncate target %d", channel.ErrCorruptState, leo, to)
	}
	return s.history.TruncateTo(to)
}

func (s *splitDurableReplicaStore) StoreCheckpointMonotonic(ctx context.Context, checkpoint channel.Checkpoint, visibleHW uint64, leo uint64) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	if combined, ok := s.log.(combinedCheckpointStore); ok {
		return combined.StoreCheckpointMonotonic(ctx, checkpoint, visibleHW, leo)
	}
	if err := validateCheckpoint(checkpoint); err != nil {
		return err
	}
	if checkpoint.HW > visibleHW {
		return fmt.Errorf("%w: checkpoint hw %d > visible hw %d", channel.ErrCorruptState, checkpoint.HW, visibleHW)
	}
	if checkpoint.HW > leo {
		return fmt.Errorf("%w: checkpoint hw %d > leo %d", channel.ErrCorruptState, checkpoint.HW, leo)
	}

	current, err := s.checkpoint.Load()
	if errors.Is(err, channel.ErrEmptyState) {
		current = channel.Checkpoint{}
	} else if err != nil {
		return err
	}
	if checkpoint.HW < current.HW {
		return fmt.Errorf("%w: checkpoint hw regression %d < %d", channel.ErrCorruptState, checkpoint.HW, current.HW)
	}
	if checkpoint.LogStartOffset < current.LogStartOffset {
		return fmt.Errorf("%w: log start regression %d < %d", channel.ErrCorruptState, checkpoint.LogStartOffset, current.LogStartOffset)
	}
	if checkpoint.Epoch < current.Epoch {
		return fmt.Errorf("%w: checkpoint epoch regression %d < %d", channel.ErrCorruptState, checkpoint.Epoch, current.Epoch)
	}
	return s.checkpoint.Store(checkpoint)
}

func (s *splitDurableReplicaStore) InstallSnapshotAtomically(ctx context.Context, snap channel.Snapshot, checkpoint channel.Checkpoint, epochPoint channel.EpochPoint) (uint64, error) {
	if err := contextError(ctx); err != nil {
		return 0, err
	}
	if combined, ok := s.log.(combinedSnapshotStore); ok {
		return combined.InstallSnapshotAtomically(ctx, snap, checkpoint, epochPoint)
	}
	leo := s.log.LEO()
	if snap.EndOffset > leo {
		return 0, fmt.Errorf("%w: snapshot end %d > leo %d", channel.ErrCorruptState, snap.EndOffset, leo)
	}
	if checkpoint.LogStartOffset != snap.EndOffset || checkpoint.HW != snap.EndOffset {
		return 0, channel.ErrCorruptState
	}
	if checkpoint.Epoch != snap.Epoch || epochPoint.Epoch != snap.Epoch || epochPoint.StartOffset > snap.EndOffset {
		return 0, channel.ErrCorruptState
	}
	if err := s.validateCheckpointMonotonic(&checkpoint); err != nil {
		return 0, err
	}
	if err := s.snapshots.InstallSnapshot(ctx, snap); err != nil {
		return 0, err
	}
	if loader, ok := s.snapshots.(snapshotLoader); ok {
		loaded, err := loader.LoadSnapshot(ctx)
		if err != nil {
			return 0, err
		}
		if loaded.Epoch != snap.Epoch || loaded.EndOffset != snap.EndOffset || !bytes.Equal(loaded.Payload, snap.Payload) {
			return 0, channel.ErrCorruptState
		}
	}
	if err := s.history.TruncateTo(snap.EndOffset); err != nil {
		return 0, err
	}
	history, err := s.history.Load()
	if errors.Is(err, channel.ErrEmptyState) {
		history = nil
	} else if err != nil {
		return 0, err
	}
	if !historyCoversEpochAtOffset(history, snap.Epoch, snap.EndOffset) {
		if epochPoint.StartOffset != snap.EndOffset {
			return 0, channel.ErrCorruptState
		}
		if err := s.BeginEpoch(ctx, epochPoint, snap.EndOffset); err != nil {
			return 0, err
		}
	}
	if err := s.StoreCheckpointMonotonic(ctx, checkpoint, snap.EndOffset, leo); err != nil {
		return 0, err
	}
	return leo, nil
}

func validateDurableView(checkpoint channel.Checkpoint, history []channel.EpochPoint, leo uint64, snapshot *channel.Snapshot, snapshotKnown bool, snapshotExists bool) error {
	if err := validateCheckpoint(checkpoint); err != nil {
		return err
	}
	if err := validateEpochHistory(history); err != nil {
		return err
	}
	if checkpoint.HW > leo {
		return fmt.Errorf("%w: checkpoint hw %d > leo %d", channel.ErrCorruptState, checkpoint.HW, leo)
	}
	if len(history) > 0 && history[len(history)-1].StartOffset > leo {
		return fmt.Errorf("%w: epoch history start %d > leo %d", channel.ErrCorruptState, history[len(history)-1].StartOffset, leo)
	}
	if checkpoint.HW > 0 && !historyCoversEpochAtOffset(history, checkpoint.Epoch, checkpoint.HW) {
		return fmt.Errorf("%w: checkpoint epoch %d is not compatible with hw %d", channel.ErrCorruptState, checkpoint.Epoch, checkpoint.HW)
	}
	if snapshot == nil {
		if checkpoint.LogStartOffset > 0 {
			if snapshotKnown && !snapshotExists {
				return fmt.Errorf("%w: checkpoint log start %d has no durable snapshot payload", channel.ErrCorruptState, checkpoint.LogStartOffset)
			}
			return nil
		}
		if snapshotKnown && snapshotExists {
			return fmt.Errorf("%w: snapshot payload has no compatible checkpoint", channel.ErrCorruptState)
		}
		return nil
	}
	if snapshot.EndOffset > leo {
		return fmt.Errorf("%w: snapshot end %d > leo %d", channel.ErrCorruptState, snapshot.EndOffset, leo)
	}
	if checkpoint.LogStartOffset != snapshot.EndOffset || checkpoint.HW < snapshot.EndOffset {
		return fmt.Errorf("%w: snapshot end %d is not compatible with checkpoint", channel.ErrCorruptState, snapshot.EndOffset)
	}
	if !historyCoversEpochAtOffset(history, snapshot.Epoch, snapshot.EndOffset) {
		return fmt.Errorf("%w: snapshot epoch %d is not compatible with history", channel.ErrCorruptState, snapshot.Epoch)
	}
	return nil
}

func (s *splitDurableReplicaStore) validateCheckpointMonotonic(checkpoint *channel.Checkpoint) error {
	if checkpoint == nil {
		return nil
	}
	current, err := s.checkpoint.Load()
	if errors.Is(err, channel.ErrEmptyState) {
		return nil
	}
	if err != nil {
		return err
	}
	if checkpoint.HW < current.HW {
		return fmt.Errorf("%w: checkpoint hw regression %d < %d", channel.ErrCorruptState, checkpoint.HW, current.HW)
	}
	if checkpoint.LogStartOffset < current.LogStartOffset {
		return fmt.Errorf("%w: log start regression %d < %d", channel.ErrCorruptState, checkpoint.LogStartOffset, current.LogStartOffset)
	}
	if checkpoint.Epoch < current.Epoch {
		return fmt.Errorf("%w: checkpoint epoch regression %d < %d", channel.ErrCorruptState, checkpoint.Epoch, current.Epoch)
	}
	return nil
}

func historyCoversEpochAtOffset(history []channel.EpochPoint, epoch uint64, offset uint64) bool {
	if epoch == 0 {
		return false
	}
	return offsetEpochForLEO(history, offset) == epoch
}

func contextError(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

func syncLog(ctx context.Context, log LogStore) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	if syncer, ok := log.(contextSyncLogStore); ok {
		return syncer.SyncContext(ctx)
	}
	if err := log.Sync(); err != nil {
		return err
	}
	return contextError(ctx)
}
