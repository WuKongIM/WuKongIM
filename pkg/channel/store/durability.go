package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/cockroachdb/pebble/v2"
)

// BeginEpoch durably appends an epoch boundary at the expected current log end.
func (s *ChannelStore) BeginEpoch(ctx context.Context, point channel.EpochPoint, expectedLEO uint64) error {
	if err := s.validate(); err != nil {
		return err
	}
	if err := contextError(ctx); err != nil {
		return err
	}
	if point.StartOffset != expectedLEO {
		return fmt.Errorf("%w: epoch start %d != expected leo %d", channel.ErrCorruptState, point.StartOffset, expectedLEO)
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	s.mu.Lock()
	leo, err := s.leoLocked()
	if err != nil {
		s.mu.Unlock()
		return err
	}
	if leo != expectedLEO {
		s.mu.Unlock()
		return fmt.Errorf("%w: leo %d != expected leo %d", channel.ErrCorruptState, leo, expectedLEO)
	}
	history, err := s.loadHistoryOrEmpty()
	if err != nil {
		s.mu.Unlock()
		return err
	}
	needsAppend, err := shouldAppendHistoryPoint(history, point)
	if err != nil {
		s.mu.Unlock()
		return err
	}
	s.mu.Unlock()
	if !needsAppend {
		return nil
	}

	return s.commitDurableMutation(ctx, false, leo, func(writeBatch *pebble.Batch) error {
		return s.writeHistoryPoint(writeBatch, point)
	})
}

// TruncateLogAndHistory durably truncates log rows and future epoch history in one batch.
func (s *ChannelStore) TruncateLogAndHistory(ctx context.Context, to uint64) error {
	if err := s.validate(); err != nil {
		return err
	}
	if err := contextError(ctx); err != nil {
		return err
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	s.mu.Lock()
	leo, err := s.leoLocked()
	if err != nil {
		s.mu.Unlock()
		return err
	}
	if to > leo {
		s.mu.Unlock()
		return fmt.Errorf("%w: truncate target %d > leo %d", channel.ErrCorruptState, to, leo)
	}
	s.mu.Unlock()

	nextLEO := leo
	if to < leo {
		nextLEO = to
	}
	return s.commitDurableMutation(ctx, to < leo, nextLEO, func(writeBatch *pebble.Batch) error {
		if to < leo {
			if err := s.messageTable().truncateFromSeq(writeBatch, to+1); err != nil {
				return err
			}
		}
		return s.deleteHistoryAfter(writeBatch, to)
	})
}

// StoreCheckpointMonotonic stores a checkpoint only when it is safe for the visible log state.
func (s *ChannelStore) StoreCheckpointMonotonic(ctx context.Context, checkpoint channel.Checkpoint, visibleHW uint64, leo uint64) error {
	if err := s.validate(); err != nil {
		return err
	}
	if err := contextError(ctx); err != nil {
		return err
	}
	if err := validateStoreCheckpoint(checkpoint); err != nil {
		return err
	}
	if checkpoint.HW > visibleHW {
		return fmt.Errorf("%w: checkpoint hw %d > visible hw %d", channel.ErrCorruptState, checkpoint.HW, visibleHW)
	}
	if checkpoint.HW > leo {
		return fmt.Errorf("%w: checkpoint hw %d > leo %d", channel.ErrCorruptState, checkpoint.HW, leo)
	}

	s.checkpointMu.Lock()
	defer s.checkpointMu.Unlock()
	if err := s.validateCheckpointMonotonicAgainstCurrent(checkpoint); err != nil {
		return err
	}

	return s.commitCheckpointMutation(ctx, func(writeBatch *pebble.Batch) error {
		return s.writeCheckpoint(writeBatch, checkpoint)
	})
}

// InstallSnapshotAtomically stores snapshot payload, checkpoint, and epoch history together.
func (s *ChannelStore) InstallSnapshotAtomically(ctx context.Context, snap channel.Snapshot, checkpoint channel.Checkpoint, epochPoint channel.EpochPoint) (uint64, error) {
	if err := s.validate(); err != nil {
		return 0, err
	}
	if err := contextError(ctx); err != nil {
		return 0, err
	}
	if checkpoint.LogStartOffset != snap.EndOffset || checkpoint.HW != snap.EndOffset {
		return 0, channel.ErrCorruptState
	}
	if checkpoint.Epoch != snap.Epoch || epochPoint.Epoch != snap.Epoch || epochPoint.StartOffset > snap.EndOffset {
		return 0, channel.ErrCorruptState
	}
	if err := validateStoreCheckpoint(checkpoint); err != nil {
		return 0, err
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	s.mu.Lock()
	leo, err := s.leoLocked()
	if err != nil {
		s.mu.Unlock()
		return 0, err
	}
	if snap.EndOffset > leo {
		s.mu.Unlock()
		return 0, fmt.Errorf("%w: snapshot end %d > leo %d", channel.ErrCorruptState, snap.EndOffset, leo)
	}
	history, err := s.loadHistoryOrEmpty()
	if err != nil {
		s.mu.Unlock()
		return 0, err
	}
	if err := validateStoreEpochHistory(history); err != nil {
		s.mu.Unlock()
		return 0, err
	}
	trimmedHistory := trimStoreHistoryAfter(history, snap.EndOffset)
	var appendPoint *channel.EpochPoint
	if !storeHistoryCoversEpochAtOffset(trimmedHistory, snap.Epoch, snap.EndOffset) {
		if epochPoint.StartOffset != snap.EndOffset {
			s.mu.Unlock()
			return 0, channel.ErrCorruptState
		}
		needsAppend, err := shouldAppendHistoryPoint(trimmedHistory, epochPoint)
		if err != nil {
			s.mu.Unlock()
			return 0, err
		}
		if needsAppend {
			point := epochPoint
			appendPoint = &point
			trimmedHistory = append(trimmedHistory, point)
		}
	}
	if !storeHistoryCoversEpochAtOffset(trimmedHistory, snap.Epoch, snap.EndOffset) {
		s.mu.Unlock()
		return 0, channel.ErrCorruptState
	}
	s.mu.Unlock()

	s.checkpointMu.Lock()
	defer s.checkpointMu.Unlock()
	if err := s.validateCheckpointMonotonicAgainstCurrent(checkpoint); err != nil {
		return 0, err
	}

	err = s.commitDurableMutation(ctx, false, leo, func(writeBatch *pebble.Batch) error {
		if err := writeBatch.Set(encodeSnapshotKey(s.key), append([]byte(nil), snap.Payload...), pebble.NoSync); err != nil {
			return err
		}
		if err := s.writeCheckpoint(writeBatch, checkpoint); err != nil {
			return err
		}
		if err := s.deleteHistoryAfter(writeBatch, snap.EndOffset); err != nil {
			return err
		}
		if appendPoint != nil {
			if err := s.writeHistoryPoint(writeBatch, *appendPoint); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return leo, nil
}

func (s *ChannelStore) commitDurableMutation(ctx context.Context, publishLEO bool, nextLEO uint64, build func(*pebble.Batch) error) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	if build == nil {
		return channel.ErrInvalidArgument
	}

	if publishLEO {
		s.writeInProgress.Store(true)
		defer s.failPendingWrite()
	}
	if coordinator := s.commitCoordinator(); coordinator != nil {
		return coordinator.submit(commitRequest{
			channelKey: s.key,
			build:      build,
			publish: func() error {
				if publishLEO {
					s.publishDurableWrite(nextLEO)
					return nil
				}
				s.recordDurableCommit()
				return nil
			},
		})
	}

	batch := s.engine.db.NewBatch()
	defer batch.Close()
	if err := build(batch); err != nil {
		return err
	}
	if err := batch.Commit(pebble.Sync); err != nil {
		return err
	}
	if publishLEO {
		s.publishDurableWrite(nextLEO)
		return nil
	}
	s.recordDurableCommit()
	return nil
}

func (s *ChannelStore) commitCheckpointMutation(ctx context.Context, build func(*pebble.Batch) error) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	if build == nil {
		return channel.ErrInvalidArgument
	}

	if coordinator := s.checkpointCoordinator(); coordinator != nil {
		return coordinator.submit(commitRequest{
			channelKey: s.key,
			build:      build,
			publish: func() error {
				s.recordDurableCommit()
				return nil
			},
		})
	}

	batch := s.engine.db.NewBatch()
	defer batch.Close()
	if err := build(batch); err != nil {
		return err
	}
	if err := batch.Commit(pebble.Sync); err != nil {
		return err
	}
	s.recordDurableCommit()
	return nil
}

func (s *ChannelStore) deleteHistoryAfter(writeBatch *pebble.Batch, leo uint64) error {
	if writeBatch == nil {
		return channel.ErrInvalidArgument
	}
	if leo == ^uint64(0) {
		return nil
	}
	prefix := encodeHistoryPrefix(s.key)
	return writeBatch.DeleteRange(encodeHistoryOffsetKey(s.key, leo+1), keyUpperBound(prefix), pebble.NoSync)
}

func validateStoreCheckpoint(checkpoint channel.Checkpoint) error {
	if checkpoint.LogStartOffset > checkpoint.HW {
		return fmt.Errorf("%w: log start %d > checkpoint hw %d", channel.ErrCorruptState, checkpoint.LogStartOffset, checkpoint.HW)
	}
	return nil
}

func (s *ChannelStore) validateCheckpointMonotonicAgainstCurrent(checkpoint channel.Checkpoint) error {
	current, err := s.LoadCheckpoint()
	if err != nil {
		if errors.Is(err, channel.ErrEmptyState) {
			return nil
		}
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

func validateStoreEpochHistory(points []channel.EpochPoint) error {
	var previous channel.EpochPoint
	for i, point := range points {
		if point.Epoch == 0 {
			return channel.ErrCorruptState
		}
		if i == 0 {
			previous = point
			continue
		}
		if point.Epoch < previous.Epoch {
			return channel.ErrCorruptState
		}
		if point.Epoch == previous.Epoch && point.StartOffset != previous.StartOffset {
			return channel.ErrCorruptState
		}
		if point.StartOffset < previous.StartOffset {
			return channel.ErrCorruptState
		}
		previous = point
	}
	return nil
}

func trimStoreHistoryAfter(points []channel.EpochPoint, leo uint64) []channel.EpochPoint {
	if len(points) == 0 {
		return nil
	}
	trimmed := points[:0]
	for _, point := range points {
		if point.StartOffset <= leo {
			trimmed = append(trimmed, point)
		}
	}
	return append([]channel.EpochPoint(nil), trimmed...)
}

func storeHistoryCoversEpochAtOffset(points []channel.EpochPoint, epoch uint64, offset uint64) bool {
	if epoch == 0 {
		return false
	}
	return storeOffsetEpochForLEO(points, offset) == epoch
}

func storeOffsetEpochForLEO(points []channel.EpochPoint, leo uint64) uint64 {
	var epoch uint64
	for _, point := range points {
		if point.StartOffset > leo {
			break
		}
		epoch = point.Epoch
	}
	return epoch
}

func contextError(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}
