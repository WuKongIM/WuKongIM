package replica

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
)

func (r *replica) recoverFromStores() error {
	view, err := r.durable.Recover(context.Background())
	if err != nil {
		return err
	}
	checkpoint := view.Checkpoint
	history := view.EpochHistory
	leo := view.LEO

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
	result := r.submitLoopCommand(ctx, machineInstallSnapshotCommand{Snapshot: snap})
	if result.Err != nil {
		return result.Err
	}
	for _, effect := range result.Effects {
		install, ok := effect.(installSnapshotEffect)
		if !ok {
			continue
		}
		return r.executeInstallSnapshotEffect(ctx, install)
	}
	return nil
}
