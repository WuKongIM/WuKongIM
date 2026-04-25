package replica

import (
	"context"
	"errors"
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
)

func (r *replica) recoverFromStores() error {
	checkpoint, err := r.checkpoints.Load()
	if errors.Is(err, channel.ErrEmptyState) {
		checkpoint = channel.Checkpoint{}
	} else if err != nil {
		return err
	}

	history, err := r.history.Load()
	if errors.Is(err, channel.ErrEmptyState) {
		history = nil
	} else if err != nil {
		return err
	}

	if err := validateCheckpoint(checkpoint); err != nil {
		return err
	}
	if err := validateEpochHistory(history); err != nil {
		return err
	}

	leo := r.log.LEO()
	if checkpoint.HW > leo {
		return fmt.Errorf("%w: checkpoint hw %d > leo %d", channel.ErrCorruptState, checkpoint.HW, leo)
	}
	if len(history) > 0 && history[len(history)-1].StartOffset > leo {
		return fmt.Errorf("%w: epoch history start %d > leo %d", channel.ErrCorruptState, history[len(history)-1].StartOffset, leo)
	}

	r.state.Role = channel.ReplicaRoleFollower
	r.state.Epoch = checkpoint.Epoch
	r.state.OffsetEpoch = offsetEpochForLEO(history, leo)
	r.state.LogStartOffset = checkpoint.LogStartOffset
	r.state.HW = checkpoint.HW
	r.state.CheckpointHW = checkpoint.HW
	r.state.CommitReady = leo == checkpoint.HW
	r.state.LEO = leo
	r.epochHistory = append([]channel.EpochPoint(nil), history...)
	r.recovered = true
	r.publishStateLocked()
	return nil
}

func validateCheckpoint(checkpoint channel.Checkpoint) error {
	if checkpoint.LogStartOffset > checkpoint.HW {
		return channel.ErrCorruptState
	}
	return nil
}

func validateEpochHistory(history []channel.EpochPoint) error {
	for i, point := range history {
		if point.Epoch == 0 {
			return channel.ErrCorruptState
		}
		if i == 0 {
			continue
		}
		prev := history[i-1]
		if point.Epoch < prev.Epoch {
			return channel.ErrCorruptState
		}
		if point.StartOffset < prev.StartOffset {
			return channel.ErrCorruptState
		}
		if point.Epoch == prev.Epoch && point.StartOffset != prev.StartOffset {
			return channel.ErrCorruptState
		}
	}
	return nil
}

func (r *replica) InstallSnapshot(ctx context.Context, snap channel.Snapshot) error {
	r.appendMu.Lock()
	defer r.appendMu.Unlock()
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.state.Role == channel.ReplicaRoleTombstoned {
		return channel.ErrTombstoned
	}
	if r.state.ChannelKey != "" && snap.ChannelKey != "" && snap.ChannelKey != r.state.ChannelKey {
		return channel.ErrStaleMeta
	}
	if snap.Epoch < r.state.Epoch {
		return channel.ErrStaleMeta
	}
	if snap.EndOffset < r.state.HW || snap.EndOffset < r.state.LogStartOffset {
		return channel.ErrCorruptState
	}
	leo := r.log.LEO()
	if leo < snap.EndOffset {
		return channel.ErrCorruptState
	}
	if err := r.snapshots.InstallSnapshot(ctx, snap); err != nil {
		return err
	}
	if err := r.history.TruncateTo(snap.EndOffset); err != nil {
		return err
	}
	r.epochHistory = trimEpochHistoryToLEO(r.epochHistory, snap.EndOffset)
	if len(r.epochHistory) == 0 {
		if err := r.appendEpochPointLocked(channel.EpochPoint{
			Epoch:       snap.Epoch,
			StartOffset: snap.EndOffset,
		}); err != nil {
			return err
		}
	} else {
		last := r.epochHistory[len(r.epochHistory)-1]
		switch {
		case last.Epoch == snap.Epoch:
			// Existing history already maps this epoch; no additional point needed.
		case last.Epoch < snap.Epoch:
			if err := r.appendEpochPointLocked(channel.EpochPoint{
				Epoch:       snap.Epoch,
				StartOffset: snap.EndOffset,
			}); err != nil {
				return err
			}
		default:
			return channel.ErrCorruptState
		}
	}

	checkpoint := channel.Checkpoint{
		Epoch:          snap.Epoch,
		LogStartOffset: snap.EndOffset,
		HW:             snap.EndOffset,
	}
	if err := r.checkpoints.Store(checkpoint); err != nil {
		return err
	}
	r.state.Role = channel.ReplicaRoleFollower
	r.state.Epoch = snap.Epoch
	r.state.OffsetEpoch = offsetEpochForLEO(r.epochHistory, leo)
	r.state.LogStartOffset = snap.EndOffset
	r.state.HW = snap.EndOffset
	r.state.CheckpointHW = snap.EndOffset
	r.state.CommitReady = true
	r.state.LEO = leo
	r.publishStateLocked()
	return nil
}
