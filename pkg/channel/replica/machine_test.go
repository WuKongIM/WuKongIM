package replica

import (
	"errors"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/stretchr/testify/require"
)

func TestMachineAppendValidation(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	tests := []struct {
		name        string
		m           replicaMachine
		want        error
		wantEffects int
	}{
		{
			name: "not leader",
			m: newTestMachine(channel.ReplicaState{
				Role:        channel.ReplicaRoleFollower,
				CommitReady: true,
				LEO:         4,
			}, activeMetaWithMinISR(7, 1, 2), now),
			want: channel.ErrNotLeader,
		},
		{
			name: "not commit ready",
			m: newTestMachine(channel.ReplicaState{
				Role:        channel.ReplicaRoleLeader,
				CommitReady: false,
				LEO:         4,
			}, activeMetaWithMinISR(7, 1, 2), now),
			want: channel.ErrNotReady,
		},
		{
			name: "lease expired",
			m: newTestMachine(channel.ReplicaState{
				Role:        channel.ReplicaRoleLeader,
				CommitReady: true,
				LEO:         4,
			}, activeMetaWithMinISR(7, 1, 2), now.Add(-time.Second)),
			want:        channel.ErrLeaseExpired,
			wantEffects: 1,
		},
		{
			name: "insufficient ISR",
			m: newTestMachine(channel.ReplicaState{
				Role:        channel.ReplicaRoleLeader,
				CommitReady: true,
				LEO:         4,
			}, activeMetaWithMinISR(7, 1, 4), now.Add(time.Minute)),
			want: channel.ErrInsufficientISR,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.m.Apply(machineAppendCommand{
				Records: []channel.Record{{Payload: []byte("a"), SizeBytes: 1}},
				Now:     now,
			})

			require.ErrorIs(t, result.Err, tt.want)
			require.Len(t, result.Effects, tt.wantEffects)
			require.Len(t, result.Completions, 1)
			require.ErrorIs(t, result.Completions[0].Err, tt.want)
		})
	}
}

func TestMachineLeaseExpiryPublishesFencedState(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	m := newTestMachine(channel.ReplicaState{
		Role:        channel.ReplicaRoleLeader,
		CommitReady: true,
		LEO:         4,
		OffsetEpoch: 7,
	}, activeMetaWithMinISR(7, 1, 2), now.Add(-time.Second))

	result := m.Apply(machineAppendCommand{
		RequestID: 10,
		Records:   []channel.Record{{Payload: []byte("a"), SizeBytes: 1}},
		Now:       now,
	})

	require.ErrorIs(t, result.Err, channel.ErrLeaseExpired)
	require.Equal(t, channel.ReplicaRoleFencedLeader, m.state.replica.Role)
	requireEffectTypes(t, result.Effects, publishStateEffect{})
	require.Len(t, result.Completions, 1)
	require.ErrorIs(t, result.Completions[0].Err, channel.ErrLeaseExpired)
}

func TestMachineAppendOnAlreadyFencedLeaderDoesNotRepublishState(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	m := newTestMachine(channel.ReplicaState{
		Role:        channel.ReplicaRoleFencedLeader,
		CommitReady: true,
		LEO:         4,
		OffsetEpoch: 7,
	}, activeMetaWithMinISR(7, 1, 2), now.Add(-time.Second))

	result := m.Apply(machineAppendCommand{
		RequestID: 10,
		Records:   []channel.Record{{Payload: []byte("a"), SizeBytes: 1}},
		Now:       now,
	})

	require.ErrorIs(t, result.Err, channel.ErrLeaseExpired)
	require.Empty(t, result.Effects)
	require.Len(t, result.Completions, 1)
	require.ErrorIs(t, result.Completions[0].Err, channel.ErrLeaseExpired)
}

func TestMachineAppendValidationEmitsLeaderAppendEffect(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	meta := activeMetaWithMinISR(7, 1, 2)
	meta.LeaderEpoch = 70
	m := newTestMachine(channel.ReplicaState{
		ChannelKey:  "group-10",
		Role:        channel.ReplicaRoleLeader,
		Epoch:       7,
		Leader:      1,
		CommitReady: true,
		LEO:         4,
		OffsetEpoch: 7,
	}, meta, now.Add(time.Minute))

	result := m.Apply(machineAppendCommand{
		RequestID: 11,
		Records:   []channel.Record{{Payload: []byte("a"), SizeBytes: 1}},
		Now:       now,
	})

	require.NoError(t, result.Err)
	require.Empty(t, result.Completions)
	require.Len(t, result.Effects, 1)
	effect, ok := result.Effects[0].(appendLeaderBatchEffect)
	require.True(t, ok)
	require.Equal(t, uint64(1), effect.EffectID)
	require.Equal(t, []uint64{11}, effect.RequestIDs)
	require.Equal(t, channel.ChannelKey("group-10"), effect.ChannelKey)
	require.Equal(t, uint64(7), effect.Epoch)
	require.Equal(t, uint64(70), effect.LeaderEpoch)
	require.Equal(t, uint64(1), effect.RoleGeneration)
	require.Equal(t, now.Add(time.Minute), effect.LeaseUntil)
}

func TestMachineAppendEffectOwnsRecordPayload(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	payload := []byte("original")
	m := newTestMachine(channel.ReplicaState{
		ChannelKey:  "group-10",
		Role:        channel.ReplicaRoleLeader,
		Epoch:       7,
		Leader:      1,
		CommitReady: true,
		LEO:         4,
		OffsetEpoch: 7,
	}, activeMetaWithMinISR(7, 1, 2), now.Add(time.Minute))

	result := m.Apply(machineAppendCommand{
		RequestID: 11,
		Records:   []channel.Record{{Payload: payload, SizeBytes: len(payload)}},
		Now:       now,
	})
	payload[0] = 'X'

	require.NoError(t, result.Err)
	effect := result.Effects[0].(appendLeaderBatchEffect)
	require.Equal(t, []byte("original"), effect.Records[0].Payload)
}

func TestMachineAppendRunsInvariantBeforeEmittingEffect(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	m := newTestMachine(channel.ReplicaState{
		Role:         channel.ReplicaRoleLeader,
		CommitReady:  true,
		CheckpointHW: 5,
		HW:           4,
		LEO:          6,
		OffsetEpoch:  7,
	}, activeMetaWithMinISR(7, 1, 2), now.Add(time.Minute))

	result := m.Apply(machineAppendCommand{
		RequestID: 12,
		Records:   []channel.Record{{Payload: []byte("a"), SizeBytes: 1}},
		Now:       now,
	})

	require.ErrorIs(t, result.Err, channel.ErrCorruptState)
	require.Empty(t, result.Effects)
}

func TestMachineCursorProgressUpdate(t *testing.T) {
	m := newTestMachine(channel.ReplicaState{
		Role:        channel.ReplicaRoleLeader,
		HW:          4,
		LEO:         6,
		OffsetEpoch: 7,
	}, activeMetaWithMinISR(7, 1, 2), time.Now().Add(time.Minute))
	m.state.progress = map[channel.NodeID]uint64{1: 6, 2: 4, 3: 4}
	m.state.epochHistory = []channel.EpochPoint{{Epoch: 7, StartOffset: 0}}

	result := m.Apply(machineCursorCommand{
		ChannelKey:  "group-10",
		Epoch:       7,
		ReplicaID:   2,
		MatchOffset: 6,
		OffsetEpoch: 7,
	})

	require.NoError(t, result.Err)
	require.Equal(t, uint64(6), m.state.progress[2])
	requireEffectTypes(t, result.Effects, publishStateEffect{})
}

func TestMachineCursorRollsBackProgressOnInvariantFailure(t *testing.T) {
	m := newTestMachine(channel.ReplicaState{
		Role:         channel.ReplicaRoleLeader,
		CheckpointHW: 5,
		HW:           4,
		LEO:          6,
		OffsetEpoch:  7,
	}, activeMetaWithMinISR(7, 1, 2), time.Now().Add(time.Minute))
	m.state.progress = map[channel.NodeID]uint64{1: 6, 2: 4, 3: 4}
	m.state.epochHistory = []channel.EpochPoint{{Epoch: 7, StartOffset: 0}}

	result := m.Apply(machineCursorCommand{
		ChannelKey:  "group-10",
		Epoch:       7,
		ReplicaID:   2,
		MatchOffset: 6,
		OffsetEpoch: 7,
	})

	require.ErrorIs(t, result.Err, channel.ErrCorruptState)
	require.Equal(t, uint64(4), m.state.progress[2])
	require.Empty(t, result.Effects)
}

func TestMachineHWAdvanceCompletion(t *testing.T) {
	m := newTestMachine(channel.ReplicaState{
		ChannelKey:     "group-10",
		Role:           channel.ReplicaRoleLeader,
		Epoch:          7,
		LogStartOffset: 0,
		HW:             4,
		CheckpointHW:   4,
		LEO:            6,
		OffsetEpoch:    7,
	}, activeMetaWithMinISR(7, 1, 2), time.Now().Add(time.Minute))
	m.state.progress = map[channel.NodeID]uint64{1: 6, 2: 6, 3: 4}

	result := m.Apply(machineAdvanceHWEvent{})

	require.NoError(t, result.Err)
	require.Equal(t, uint64(6), m.state.replica.HW)
	requireEffectTypes(t, result.Effects, completeAppendEffect{}, storeCheckpointEffect{}, publishStateEffect{})
	checkpointEffect := result.Effects[1].(storeCheckpointEffect)
	require.Equal(t, channel.Checkpoint{Epoch: 7, LogStartOffset: 0, HW: 6}, checkpointEffect.Checkpoint)
}

func TestMachineFencedLeaderCanAdvanceHW(t *testing.T) {
	m := newTestMachine(channel.ReplicaState{
		ChannelKey:     "group-10",
		Role:           channel.ReplicaRoleFencedLeader,
		Epoch:          7,
		LogStartOffset: 0,
		HW:             4,
		CheckpointHW:   4,
		LEO:            6,
		OffsetEpoch:    7,
	}, activeMetaWithMinISR(7, 1, 2), time.Now().Add(time.Minute))
	m.state.progress = map[channel.NodeID]uint64{1: 6, 2: 6, 3: 4}

	result := m.Apply(machineAdvanceHWEvent{})

	require.NoError(t, result.Err)
	require.Equal(t, uint64(6), m.state.replica.HW)
	requireEffectTypes(t, result.Effects, completeAppendEffect{}, storeCheckpointEffect{}, publishStateEffect{})
}

func TestMachineFencedLeaderCanApplyCursorAndAdvanceHW(t *testing.T) {
	m := newTestMachine(channel.ReplicaState{
		ChannelKey:     "group-10",
		Role:           channel.ReplicaRoleFencedLeader,
		Epoch:          7,
		LogStartOffset: 0,
		HW:             4,
		CheckpointHW:   4,
		LEO:            6,
		OffsetEpoch:    7,
	}, activeMetaWithMinISR(7, 1, 2), time.Now().Add(time.Minute))
	m.state.progress = map[channel.NodeID]uint64{1: 6, 2: 4, 3: 4}

	cursor := m.Apply(machineCursorCommand{
		ChannelKey:  "group-10",
		Epoch:       7,
		ReplicaID:   2,
		MatchOffset: 6,
		OffsetEpoch: 7,
	})
	require.NoError(t, cursor.Err)
	require.Equal(t, uint64(6), m.state.progress[2])

	advance := m.Apply(machineAdvanceHWEvent{})
	require.NoError(t, advance.Err)
	require.Equal(t, uint64(6), m.state.replica.HW)
	requireEffectTypes(t, advance.Effects, completeAppendEffect{}, storeCheckpointEffect{}, publishStateEffect{})
}

func TestMachineCursorRejectsStaleOrInvalidRequest(t *testing.T) {
	tests := []struct {
		name string
		cmd  machineCursorCommand
		want error
	}{
		{
			name: "stale channel key",
			cmd:  machineCursorCommand{ChannelKey: "other", Epoch: 7, ReplicaID: 2, MatchOffset: 6, OffsetEpoch: 7},
			want: channel.ErrStaleMeta,
		},
		{
			name: "stale epoch",
			cmd:  machineCursorCommand{ChannelKey: "group-10", Epoch: 6, ReplicaID: 2, MatchOffset: 6, OffsetEpoch: 7},
			want: channel.ErrStaleMeta,
		},
		{
			name: "invalid replica id",
			cmd:  machineCursorCommand{ChannelKey: "group-10", Epoch: 7, MatchOffset: 6, OffsetEpoch: 7},
			want: channel.ErrInvalidMeta,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newTestMachine(channel.ReplicaState{
				Role:        channel.ReplicaRoleLeader,
				HW:          4,
				LEO:         6,
				OffsetEpoch: 7,
			}, activeMetaWithMinISR(7, 1, 2), time.Now().Add(time.Minute))
			m.state.progress = map[channel.NodeID]uint64{1: 6, 2: 4, 3: 4}

			result := m.Apply(tt.cmd)

			require.ErrorIs(t, result.Err, tt.want)
			require.Equal(t, uint64(4), m.state.progress[2])
			require.Empty(t, result.Effects)
		})
	}
}

func TestMachineCheckpointResultSurvivesLaterUnrelatedEffect(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	m := newTestMachine(channel.ReplicaState{
		ChannelKey:     "group-10",
		Role:           channel.ReplicaRoleLeader,
		Epoch:          7,
		LogStartOffset: 0,
		HW:             4,
		CheckpointHW:   4,
		LEO:            6,
		CommitReady:    true,
		OffsetEpoch:    7,
	}, activeMetaWithMinISR(7, 1, 2), now.Add(time.Minute))
	m.state.progress = map[channel.NodeID]uint64{1: 6, 2: 6, 3: 4}
	advance := m.Apply(machineAdvanceHWEvent{})
	require.NoError(t, advance.Err)
	checkpointEffect := advance.Effects[1].(storeCheckpointEffect)

	appendResult := m.Apply(machineAppendCommand{
		RequestID: 12,
		Records:   []channel.Record{{Payload: []byte("a"), SizeBytes: 1}},
		Now:       now,
	})
	require.NoError(t, appendResult.Err)
	require.Len(t, appendResult.Effects, 1)

	stored := m.Apply(machineCheckpointStoredEvent{
		EffectID:       checkpointEffect.EffectID,
		ChannelKey:     checkpointEffect.ChannelKey,
		Epoch:          checkpointEffect.Epoch,
		RoleGeneration: checkpointEffect.RoleGeneration,
		Checkpoint:     checkpointEffect.Checkpoint,
	})

	require.NoError(t, stored.Err)
	require.Equal(t, uint64(6), m.state.replica.CheckpointHW)
	requireEffectTypes(t, stored.Effects, publishStateEffect{})
}

func TestMachineCheckpointResultHandling(t *testing.T) {
	meta := activeMetaWithMinISR(7, 1, 2)
	meta.LeaderEpoch = 70
	m := newTestMachine(channel.ReplicaState{
		Role:           channel.ReplicaRoleLeader,
		HW:             6,
		CheckpointHW:   4,
		LEO:            6,
		OffsetEpoch:    7,
		LogStartOffset: 0,
	}, meta, time.Now().Add(time.Minute))
	effectID := m.nextEffectID()
	m.state.checkpointEffectID = effectID

	result := m.Apply(machineCheckpointStoredEvent{
		EffectID:       effectID,
		ChannelKey:     "group-10",
		Epoch:          7,
		LeaderEpoch:    70,
		RoleGeneration: 1,
		Checkpoint: channel.Checkpoint{
			Epoch:          7,
			LogStartOffset: 0,
			HW:             6,
		},
	})

	require.NoError(t, result.Err)
	require.Equal(t, uint64(6), m.state.replica.CheckpointHW)
	requireEffectTypes(t, result.Effects, publishStateEffect{})
}

func TestMachineStaleCheckpointResultDiscard(t *testing.T) {
	m := newTestMachine(channel.ReplicaState{
		ChannelKey:   "group-10",
		Epoch:        7,
		Role:         channel.ReplicaRoleLeader,
		HW:           6,
		CheckpointHW: 4,
		LEO:          6,
		OffsetEpoch:  7,
	}, activeMetaWithMinISR(7, 1, 2), time.Now().Add(time.Minute))
	m.nextEffectID()
	m.state.checkpointEffectID = m.state.nextEffectID

	result := m.Apply(machineCheckpointStoredEvent{
		EffectID:       99,
		ChannelKey:     "group-10",
		Epoch:          7,
		RoleGeneration: 1,
		Checkpoint: channel.Checkpoint{
			Epoch: 7,
			HW:    6,
		},
	})

	require.NoError(t, result.Err)
	require.Equal(t, uint64(4), m.state.replica.CheckpointHW)
	require.Empty(t, result.Effects)
}

func TestMachineCheckpointResultWithoutPendingEffectIsDiscarded(t *testing.T) {
	m := newTestMachine(channel.ReplicaState{
		ChannelKey:   "group-10",
		Epoch:        7,
		Role:         channel.ReplicaRoleLeader,
		HW:           6,
		CheckpointHW: 4,
		LEO:          6,
		OffsetEpoch:  7,
	}, activeMetaWithMinISR(7, 1, 2), time.Now().Add(time.Minute))

	result := m.Apply(machineCheckpointStoredEvent{
		EffectID:       0,
		ChannelKey:     "group-10",
		Epoch:          7,
		RoleGeneration: 1,
		Checkpoint:     channel.Checkpoint{Epoch: 7, HW: 6},
	})

	require.NoError(t, result.Err)
	require.Equal(t, uint64(4), m.state.replica.CheckpointHW)
	require.Empty(t, result.Effects)
}

func TestMachineStaleCheckpointResultFencing(t *testing.T) {
	tests := []struct {
		name  string
		event machineCheckpointStoredEvent
	}{
		{
			name: "stale channel key",
			event: machineCheckpointStoredEvent{
				EffectID:       1,
				ChannelKey:     "other",
				Epoch:          7,
				RoleGeneration: 1,
				Checkpoint:     channel.Checkpoint{Epoch: 7, HW: 6},
			},
		},
		{
			name: "stale epoch",
			event: machineCheckpointStoredEvent{
				EffectID:       1,
				ChannelKey:     "group-10",
				Epoch:          6,
				RoleGeneration: 1,
				Checkpoint:     channel.Checkpoint{Epoch: 7, HW: 6},
			},
		},
		{
			name: "stale role generation",
			event: machineCheckpointStoredEvent{
				EffectID:       1,
				ChannelKey:     "group-10",
				Epoch:          7,
				RoleGeneration: 0,
				Checkpoint:     channel.Checkpoint{Epoch: 7, HW: 6},
			},
		},
		{
			name: "stale leader epoch",
			event: machineCheckpointStoredEvent{
				EffectID:       1,
				ChannelKey:     "group-10",
				Epoch:          7,
				LeaderEpoch:    69,
				RoleGeneration: 1,
				Checkpoint:     channel.Checkpoint{Epoch: 7, HW: 6},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			meta := activeMetaWithMinISR(7, 1, 2)
			meta.LeaderEpoch = 70
			m := newTestMachine(channel.ReplicaState{
				ChannelKey:   "group-10",
				Epoch:        7,
				Role:         channel.ReplicaRoleLeader,
				HW:           6,
				CheckpointHW: 4,
				LEO:          6,
				OffsetEpoch:  7,
			}, meta, time.Now().Add(time.Minute))
			m.nextEffectID()
			m.state.checkpointEffectID = m.state.nextEffectID

			result := m.Apply(tt.event)

			require.NoError(t, result.Err)
			require.Equal(t, uint64(4), m.state.replica.CheckpointHW)
			require.Empty(t, result.Effects)
		})
	}
}

func TestMachineClosedOrTombstonedRejectsMutatingEvents(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*replicaMachine)
		want  error
	}{
		{
			name: "tombstoned",
			setup: func(m *replicaMachine) {
				m.state.replica.Role = channel.ReplicaRoleTombstoned
			},
			want: channel.ErrTombstoned,
		},
		{
			name: "closed",
			setup: func(m *replicaMachine) {
				m.state.closed = true
			},
			want: channel.ErrNotLeader,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newTestMachine(channel.ReplicaState{
				Role:         channel.ReplicaRoleLeader,
				HW:           4,
				CheckpointHW: 4,
				LEO:          6,
				OffsetEpoch:  7,
			}, activeMetaWithMinISR(7, 1, 2), time.Now().Add(time.Minute))
			m.state.progress = map[channel.NodeID]uint64{1: 6, 2: 4, 3: 4}
			tt.setup(&m)

			cursor := m.Apply(machineCursorCommand{ReplicaID: 2, MatchOffset: 6, OffsetEpoch: 7})
			require.ErrorIs(t, cursor.Err, tt.want)
			require.Equal(t, uint64(4), m.state.progress[2])

			advance := m.Apply(machineAdvanceHWEvent{})
			require.ErrorIs(t, advance.Err, tt.want)
			require.Equal(t, uint64(4), m.state.replica.HW)

			m.nextEffectID()
			m.state.checkpointEffectID = m.state.nextEffectID
			checkpoint := m.Apply(machineCheckpointStoredEvent{
				EffectID:       m.state.nextEffectID,
				ChannelKey:     "group-10",
				Epoch:          7,
				RoleGeneration: m.state.roleGeneration,
				Checkpoint:     channel.Checkpoint{Epoch: 7, HW: 6},
			})
			require.NoError(t, checkpoint.Err)
			require.Equal(t, uint64(4), m.state.replica.CheckpointHW)
		})
	}
}

func TestMachineRoleTransitionsRunInvariantChecks(t *testing.T) {
	tests := []struct {
		name  string
		event machineEvent
	}{
		{name: "lease expiry append", event: machineAppendCommand{Records: []channel.Record{{Payload: []byte("a"), SizeBytes: 1}}, Now: time.Unix(1_700_000_000, 0)}},
		{name: "tombstone", event: machineTombstoneCommand{}},
		{name: "close", event: machineCloseCommand{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			now := time.Unix(1_700_000_000, 0)
			m := newTestMachine(channel.ReplicaState{
				Role:         channel.ReplicaRoleLeader,
				CommitReady:  true,
				CheckpointHW: 5,
				HW:           4,
				LEO:          6,
				OffsetEpoch:  7,
			}, activeMetaWithMinISR(7, 1, 2), now.Add(-time.Second))

			result := m.Apply(tt.event)

			require.ErrorIs(t, result.Err, channel.ErrCorruptState)
		})
	}
}

func TestMachineTerminalTransitionsCompleteAppendEvenWhenInvariantFails(t *testing.T) {
	tests := []struct {
		name      string
		event     machineEvent
		wantRole  channel.ReplicaRole
		wantClose bool
		wantErr   error
	}{
		{
			name:     "tombstone",
			event:    machineTombstoneCommand{},
			wantRole: channel.ReplicaRoleTombstoned,
			wantErr:  channel.ErrTombstoned,
		},
		{
			name:      "close",
			event:     machineCloseCommand{},
			wantRole:  channel.ReplicaRoleLeader,
			wantClose: true,
			wantErr:   channel.ErrNotLeader,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newTestMachine(channel.ReplicaState{
				Role:         channel.ReplicaRoleLeader,
				CommitReady:  true,
				CheckpointHW: 5,
				HW:           4,
				LEO:          6,
				OffsetEpoch:  7,
			}, activeMetaWithMinISR(7, 1, 2), time.Now().Add(time.Minute))

			result := m.Apply(tt.event)

			require.ErrorIs(t, result.Err, channel.ErrCorruptState)
			require.Equal(t, tt.wantRole, m.state.replica.Role)
			require.Equal(t, tt.wantClose, m.state.closed)
			requireEffectTypes(t, result.Effects, publishStateEffect{}, completeAppendEffect{})
			complete := result.Effects[1].(completeAppendEffect)
			require.ErrorIs(t, complete.Err, tt.wantErr)
		})
	}
}

func TestMachineTombstoneAndClose(t *testing.T) {
	m := newTestMachine(channel.ReplicaState{
		Role:        channel.ReplicaRoleLeader,
		CommitReady: true,
		LEO:         4,
		OffsetEpoch: 7,
	}, activeMetaWithMinISR(7, 1, 2), time.Now().Add(time.Minute))

	tombstone := m.Apply(machineTombstoneCommand{})
	require.NoError(t, tombstone.Err)
	require.Equal(t, channel.ReplicaRoleTombstoned, m.state.replica.Role)
	requireEffectTypes(t, tombstone.Effects, publishStateEffect{}, completeAppendEffect{})

	appendAfterTombstone := m.Apply(machineAppendCommand{
		Records: []channel.Record{{Payload: []byte("a"), SizeBytes: 1}},
		Now:     time.Now(),
	})
	require.ErrorIs(t, appendAfterTombstone.Err, channel.ErrTombstoned)

	closeResult := m.Apply(machineCloseCommand{})
	require.NoError(t, closeResult.Err)
	require.True(t, m.state.closed)
	requireEffectTypes(t, closeResult.Effects, publishStateEffect{}, completeAppendEffect{})

	appendAfterClose := m.Apply(machineAppendCommand{
		Records: []channel.Record{{Payload: []byte("b"), SizeBytes: 1}},
		Now:     time.Now(),
	})
	require.ErrorIs(t, appendAfterClose.Err, channel.ErrNotLeader)
}

func newTestMachine(state channel.ReplicaState, meta channel.Meta, leaseUntil time.Time) replicaMachine {
	meta.LeaseUntil = leaseUntil
	state.ChannelKey = meta.Key
	state.Epoch = meta.Epoch
	state.Leader = meta.Leader
	if state.OffsetEpoch == 0 && state.LEO > 0 {
		state.OffsetEpoch = state.Epoch
	}
	return newReplicaMachine(replicaMachineState{
		meta:           meta,
		replica:        state,
		progress:       map[channel.NodeID]uint64{1: state.LEO},
		epochHistory:   []channel.EpochPoint{{Epoch: state.Epoch, StartOffset: 0}},
		roleGeneration: 1,
	})
}

func TestReplicaMachineClonesInputState(t *testing.T) {
	progress := map[channel.NodeID]uint64{1: 4}
	meta := activeMetaWithMinISR(7, 1, 2)
	m := newReplicaMachine(replicaMachineState{
		meta:     meta,
		replica:  channel.ReplicaState{Role: channel.ReplicaRoleLeader, LEO: 4},
		progress: progress,
	})

	progress[1] = 99
	meta.ISR[0] = 99

	require.Equal(t, uint64(4), m.state.progress[1])
	require.Equal(t, channel.NodeID(1), m.state.meta.ISR[0])
}

func requireEffectTypes(t *testing.T, effects []machineEffect, want ...machineEffect) {
	t.Helper()
	require.Len(t, effects, len(want))
	for i := range want {
		require.IsType(t, want[i], effects[i])
	}
}

func requireCompletionError(t *testing.T, completions []machineCompletion, want error) {
	t.Helper()
	require.Len(t, completions, 1)
	require.True(t, errors.Is(completions[0].Err, want))
}
