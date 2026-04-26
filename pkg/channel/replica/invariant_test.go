package replica

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/stretchr/testify/require"
)

func TestReplicaInvariantRejectsInvalidWatermarks(t *testing.T) {
	tests := []struct {
		name  string
		state channel.ReplicaState
	}{
		{
			name: "log start above checkpoint high watermark",
			state: channel.ReplicaState{
				LogStartOffset: 5,
				CheckpointHW:   4,
				HW:             4,
				LEO:            4,
			},
		},
		{
			name: "checkpoint high watermark above runtime high watermark",
			state: channel.ReplicaState{
				LogStartOffset: 0,
				CheckpointHW:   5,
				HW:             4,
				LEO:            5,
			},
		},
		{
			name: "runtime high watermark above log end offset",
			state: channel.ReplicaState{
				LogStartOffset: 0,
				CheckpointHW:   4,
				HW:             6,
				LEO:            5,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkReplicaInvariant(replicaInvariantCheck{State: tt.state})
			require.ErrorIs(t, err, channel.ErrCorruptState)
		})
	}
}

func TestReplicaInvariantRejectsOffsetEpochMismatch(t *testing.T) {
	err := checkReplicaInvariant(replicaInvariantCheck{
		State: channel.ReplicaState{
			LogStartOffset: 0,
			CheckpointHW:   3,
			HW:             3,
			LEO:            4,
			OffsetEpoch:    7,
		},
		EpochHistory: []channel.EpochPoint{
			{Epoch: 7, StartOffset: 0},
			{Epoch: 8, StartOffset: 4},
		},
	})

	require.ErrorIs(t, err, channel.ErrCorruptState)
}

func TestReplicaInvariantRejectsOffsetEpochWithoutHistory(t *testing.T) {
	err := checkReplicaInvariant(replicaInvariantCheck{
		State: channel.ReplicaState{
			LogStartOffset: 0,
			CheckpointHW:   3,
			HW:             3,
			LEO:            4,
			OffsetEpoch:    7,
		},
	})

	require.ErrorIs(t, err, channel.ErrCorruptState)
}

func TestReplicaInvariantRejectsEpochHistoryBeyondLEO(t *testing.T) {
	err := checkReplicaInvariant(replicaInvariantCheck{
		State: channel.ReplicaState{
			LogStartOffset: 0,
			CheckpointHW:   3,
			HW:             3,
			LEO:            5,
			OffsetEpoch:    7,
		},
		EpochHistory: []channel.EpochPoint{
			{Epoch: 7, StartOffset: 0},
			{Epoch: 8, StartOffset: 10},
		},
	})

	require.ErrorIs(t, err, channel.ErrCorruptState)
}

func TestReplicaInvariantRejectsTailTruncationBelowHW(t *testing.T) {
	previous := channel.ReplicaState{
		LogStartOffset: 0,
		CheckpointHW:   5,
		HW:             5,
		LEO:            7,
		OffsetEpoch:    7,
	}
	truncateTo := uint64(4)
	err := checkReplicaInvariant(replicaInvariantCheck{
		PreviousState: &previous,
		State: channel.ReplicaState{
			LogStartOffset: 0,
			CheckpointHW:   5,
			HW:             5,
			LEO:            truncateTo,
			OffsetEpoch:    7,
		},
		EpochHistory:   []channel.EpochPoint{{Epoch: 7, StartOffset: 0}},
		TailTruncateTo: &truncateTo,
	})

	require.ErrorIs(t, err, channel.ErrCorruptState)
	require.Contains(t, err.Error(), "truncate target")
}

func TestReplicaInvariantRejectsTailTruncationLEOMismatch(t *testing.T) {
	previous := channel.ReplicaState{
		LogStartOffset: 0,
		CheckpointHW:   4,
		HW:             4,
		LEO:            7,
		OffsetEpoch:    7,
	}
	truncateTo := uint64(5)
	err := checkReplicaInvariant(replicaInvariantCheck{
		PreviousState: &previous,
		State: channel.ReplicaState{
			LogStartOffset: 0,
			CheckpointHW:   4,
			HW:             4,
			LEO:            7,
			OffsetEpoch:    7,
		},
		EpochHistory:   []channel.EpochPoint{{Epoch: 7, StartOffset: 0}},
		TailTruncateTo: &truncateTo,
	})

	require.ErrorIs(t, err, channel.ErrCorruptState)
}

func TestReplicaInvariantRejectsTailTruncationWithoutPreviousState(t *testing.T) {
	truncateTo := uint64(5)
	err := checkReplicaInvariant(replicaInvariantCheck{
		State: channel.ReplicaState{
			LogStartOffset: 0,
			CheckpointHW:   4,
			HW:             4,
			LEO:            truncateTo,
			OffsetEpoch:    7,
		},
		EpochHistory:   []channel.EpochPoint{{Epoch: 7, StartOffset: 0}},
		TailTruncateTo: &truncateTo,
	})

	require.ErrorIs(t, err, channel.ErrCorruptState)
	require.Contains(t, err.Error(), "previous state")
}

func TestReplicaInvariantRejectsLEORegressionWithoutTruncation(t *testing.T) {
	previous := channel.ReplicaState{
		LogStartOffset: 0,
		CheckpointHW:   4,
		HW:             4,
		LEO:            7,
		OffsetEpoch:    7,
	}

	err := checkReplicaInvariant(replicaInvariantCheck{
		PreviousState: &previous,
		State: channel.ReplicaState{
			LogStartOffset: 0,
			CheckpointHW:   4,
			HW:             4,
			LEO:            6,
			OffsetEpoch:    7,
		},
		EpochHistory: []channel.EpochPoint{{Epoch: 7, StartOffset: 0}},
	})

	require.ErrorIs(t, err, channel.ErrCorruptState)
}

func TestReplicaInvariantRejectsSnapshotRegression(t *testing.T) {
	previous := channel.ReplicaState{
		LogStartOffset: 5,
		CheckpointHW:   5,
		HW:             5,
		LEO:            8,
		OffsetEpoch:    7,
	}
	snapshotEnd := uint64(4)

	err := checkReplicaInvariant(replicaInvariantCheck{
		PreviousState:     &previous,
		SnapshotEndOffset: &snapshotEnd,
		State: channel.ReplicaState{
			LogStartOffset: 4,
			CheckpointHW:   4,
			HW:             4,
			LEO:            8,
			OffsetEpoch:    7,
		},
		EpochHistory: []channel.EpochPoint{{Epoch: 7, StartOffset: 0}},
	})

	require.ErrorIs(t, err, channel.ErrCorruptState)
}

func TestReplicaInvariantRejectsSnapshotContextWithoutPreviousState(t *testing.T) {
	snapshotEnd := uint64(5)
	err := checkReplicaInvariant(replicaInvariantCheck{
		SnapshotEndOffset: &snapshotEnd,
		State: channel.ReplicaState{
			LogStartOffset: snapshotEnd,
			CheckpointHW:   snapshotEnd,
			HW:             snapshotEnd,
			LEO:            8,
			OffsetEpoch:    7,
		},
		EpochHistory: []channel.EpochPoint{{Epoch: 7, StartOffset: 0}},
	})

	require.ErrorIs(t, err, channel.ErrCorruptState)
	require.Contains(t, err.Error(), "previous state")
}

func TestReplicaInvariantAcceptsValidTransition(t *testing.T) {
	previous := channel.ReplicaState{
		LogStartOffset: 0,
		CheckpointHW:   3,
		HW:             3,
		LEO:            5,
		OffsetEpoch:    7,
	}

	err := checkReplicaInvariant(replicaInvariantCheck{
		PreviousState: &previous,
		State: channel.ReplicaState{
			LogStartOffset: 0,
			CheckpointHW:   4,
			HW:             5,
			LEO:            6,
			OffsetEpoch:    8,
		},
		EpochHistory: []channel.EpochPoint{
			{Epoch: 7, StartOffset: 0},
			{Epoch: 8, StartOffset: 6},
		},
	})

	require.NoError(t, err)
}
