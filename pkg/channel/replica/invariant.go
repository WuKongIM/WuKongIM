package replica

import (
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
)

// replicaInvariantCheck describes the durable/runtime state a transition wants
// to publish, plus optional operation context that has extra safety rules.
type replicaInvariantCheck struct {
	State             channel.ReplicaState
	EpochHistory      []channel.EpochPoint
	PreviousState     *channel.ReplicaState
	TailTruncateTo    *uint64
	SnapshotEndOffset *uint64
}

func checkReplicaInvariant(check replicaInvariantCheck) error {
	state := check.State
	if check.TailTruncateTo != nil {
		if check.PreviousState == nil {
			return fmt.Errorf("%w: truncate transition missing previous state", channel.ErrCorruptState)
		}
		if state.LEO != *check.TailTruncateTo {
			return fmt.Errorf("%w: leo %d != truncate target %d", channel.ErrCorruptState, state.LEO, *check.TailTruncateTo)
		}
		if *check.TailTruncateTo > check.PreviousState.LEO {
			return fmt.Errorf("%w: truncate target %d > previous leo %d", channel.ErrCorruptState, *check.TailTruncateTo, check.PreviousState.LEO)
		}
		if *check.TailTruncateTo < state.HW {
			return fmt.Errorf("%w: truncate target %d < hw %d", channel.ErrCorruptState, *check.TailTruncateTo, state.HW)
		}
		if *check.TailTruncateTo < state.CheckpointHW {
			return fmt.Errorf("%w: truncate target %d < checkpoint hw %d", channel.ErrCorruptState, *check.TailTruncateTo, state.CheckpointHW)
		}
	}

	if state.LogStartOffset > state.CheckpointHW {
		return fmt.Errorf("%w: log start %d > checkpoint hw %d", channel.ErrCorruptState, state.LogStartOffset, state.CheckpointHW)
	}
	if state.CheckpointHW > state.HW {
		return fmt.Errorf("%w: checkpoint hw %d > hw %d", channel.ErrCorruptState, state.CheckpointHW, state.HW)
	}
	if state.HW > state.LEO {
		return fmt.Errorf("%w: hw %d > leo %d", channel.ErrCorruptState, state.HW, state.LEO)
	}
	if err := validateEpochHistory(check.EpochHistory); err != nil {
		return err
	}
	if len(check.EpochHistory) == 0 {
		if state.OffsetEpoch != 0 {
			return fmt.Errorf("%w: offset epoch %d without epoch history", channel.ErrCorruptState, state.OffsetEpoch)
		}
	} else {
		if check.EpochHistory[len(check.EpochHistory)-1].StartOffset > state.LEO {
			return fmt.Errorf("%w: epoch history start %d > leo %d", channel.ErrCorruptState, check.EpochHistory[len(check.EpochHistory)-1].StartOffset, state.LEO)
		}
		expectedOffsetEpoch := offsetEpochForLEO(check.EpochHistory, state.LEO)
		if state.OffsetEpoch != expectedOffsetEpoch {
			return fmt.Errorf("%w: offset epoch %d != expected %d", channel.ErrCorruptState, state.OffsetEpoch, expectedOffsetEpoch)
		}
	}

	if check.PreviousState != nil {
		previous := *check.PreviousState
		if state.LogStartOffset < previous.LogStartOffset {
			return fmt.Errorf("%w: log start regression %d < %d", channel.ErrCorruptState, state.LogStartOffset, previous.LogStartOffset)
		}
		if state.HW < previous.HW {
			return fmt.Errorf("%w: hw regression %d < %d", channel.ErrCorruptState, state.HW, previous.HW)
		}
		if state.CheckpointHW < previous.CheckpointHW {
			return fmt.Errorf("%w: checkpoint hw regression %d < %d", channel.ErrCorruptState, state.CheckpointHW, previous.CheckpointHW)
		}
		if check.TailTruncateTo == nil && state.LEO < previous.LEO {
			return fmt.Errorf("%w: leo regression %d < %d", channel.ErrCorruptState, state.LEO, previous.LEO)
		}
	}

	if check.SnapshotEndOffset != nil {
		if check.PreviousState == nil {
			return fmt.Errorf("%w: snapshot transition missing previous state", channel.ErrCorruptState)
		}
		previous := *check.PreviousState
		if *check.SnapshotEndOffset < previous.LogStartOffset {
			return fmt.Errorf("%w: snapshot end %d < previous log start %d", channel.ErrCorruptState, *check.SnapshotEndOffset, previous.LogStartOffset)
		}
		if *check.SnapshotEndOffset < previous.HW {
			return fmt.Errorf("%w: snapshot end %d < previous hw %d", channel.ErrCorruptState, *check.SnapshotEndOffset, previous.HW)
		}
		if *check.SnapshotEndOffset < previous.CheckpointHW {
			return fmt.Errorf("%w: snapshot end %d < previous checkpoint hw %d", channel.ErrCorruptState, *check.SnapshotEndOffset, previous.CheckpointHW)
		}
		if state.LogStartOffset != *check.SnapshotEndOffset ||
			state.HW != *check.SnapshotEndOffset ||
			state.CheckpointHW != *check.SnapshotEndOffset {
			return fmt.Errorf("%w: snapshot end %d not published consistently", channel.ErrCorruptState, *check.SnapshotEndOffset)
		}
	}

	return nil
}
