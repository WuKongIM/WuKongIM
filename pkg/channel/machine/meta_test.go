package machine

import (
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestApplyMetaAssignsLeaderRole(t *testing.T) {
	state := NewChannelState(ch.ChannelKey("1:a"), ch.NodeID(1), 1)
	decision := state.ApplyMeta(ch.Meta{Key: state.Key, ID: ch.ChannelID{ID: "a", Type: 1}, Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1, 2, 3}, ISR: []ch.NodeID{1, 2, 3}, MinISR: 2, Status: ch.StatusActive})
	require.Empty(t, decision.Replies)
	require.Equal(t, ch.RoleLeader, state.Role)
	require.True(t, state.CommitReady)
	require.Equal(t, uint64(0), state.HW)
}

func TestApplyMetaAssignsFollowerRole(t *testing.T) {
	state := NewChannelState(ch.ChannelKey("1:a"), ch.NodeID(2), 1)
	state.ApplyMeta(ch.Meta{Key: state.Key, ID: ch.ChannelID{ID: "a", Type: 1}, Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1, 2, 3}, ISR: []ch.NodeID{1, 2, 3}, MinISR: 2, Status: ch.StatusActive})
	require.Equal(t, ch.RoleFollower, state.Role)
}

func TestApplyMetaRejectsInvalidMinISR(t *testing.T) {
	state := NewChannelState(ch.ChannelKey("1:a"), ch.NodeID(1), 1)
	decision := state.ApplyMeta(ch.Meta{Key: state.Key, Epoch: 1, LeaderEpoch: 1, Leader: 1, Replicas: []ch.NodeID{1}, ISR: []ch.NodeID{1}, MinISR: 2, Status: ch.StatusActive})
	require.ErrorIs(t, decision.Err, ch.ErrInvalidConfig)
}

func TestApplyMetaLeaderEpochChangeClearsAppendStateWithoutReplies(t *testing.T) {
	state := leaderState(t, 1, []ch.NodeID{1, 2, 3}, []ch.NodeID{1, 2, 3}, 2)
	proposed := state.ProposeAppendBatch(AppendBatchCommand{
		BatchOpID: 100,
		Waiters: []AppendBatchWaiter{
			{OpID: 1, Records: []ch.Record{{ID: 10, SizeBytes: 1}}},
			{OpID: 2, Records: []ch.Record{{ID: 20, SizeBytes: 1}}},
		},
	})
	require.Len(t, proposed.Tasks, 1)
	require.NotNil(t, state.InflightAppend)
	require.Len(t, state.PendingAppends, 2)
	require.Equal(t, []ch.OpID{1, 2}, state.PendingAppendOrder)

	decision := state.ApplyMeta(ch.Meta{Key: state.Key, ID: ch.ChannelID{ID: "a", Type: 1}, Epoch: 1, LeaderEpoch: 2, Leader: 1, Replicas: []ch.NodeID{1, 2, 3}, ISR: []ch.NodeID{1, 2, 3}, MinISR: 2, Status: ch.StatusActive})

	require.NoError(t, decision.Err)
	require.Empty(t, decision.Replies)
	require.Nil(t, state.InflightAppend)
	require.Empty(t, state.PendingAppends)
	require.Empty(t, state.PendingAppendOrder)

	next := state.ProposeAppendBatch(AppendBatchCommand{
		BatchOpID: 101,
		Waiters:   []AppendBatchWaiter{{OpID: 3, Records: []ch.Record{{ID: 30, SizeBytes: 1}}}},
	})
	require.NoError(t, next.Err)
	require.Len(t, next.Tasks, 1)
}

func TestApplyMetaCopiesWriteFenceWithoutClearingInflight(t *testing.T) {
	state := leaderState(t, 1, []ch.NodeID{1, 2, 3}, []ch.NodeID{1, 2, 3}, 2)
	state.PendingAppends[1] = &AppendWaiter{OpID: 1}
	state.InflightAppend = &AppendOp{OpID: 2}

	meta := ch.Meta{
		Key:         state.Key,
		ID:          state.ID,
		Epoch:       state.Epoch,
		LeaderEpoch: state.LeaderEpoch,
		Leader:      state.Leader,
		Replicas:    []ch.NodeID{1, 2, 3},
		ISR:         []ch.NodeID{1, 2, 3},
		MinISR:      2,
		Status:      ch.StatusActive,
		WriteFence: ch.WriteFence{
			Token:   "migration-1",
			Version: 7,
			Reason:  ch.WriteFenceReasonLeaderTransfer,
		},
	}

	decision := state.ApplyMeta(meta)

	require.NoError(t, decision.Err)
	require.Equal(t, meta.WriteFence, state.WriteFence)
	require.Len(t, state.PendingAppends, 1)
	require.NotNil(t, state.InflightAppend)
}

func TestApplyMetaRejectsIdentityOrFenceRegressionWithoutMutation(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ch.Meta)
	}{
		{
			name: "channel key mismatch",
			mutate: func(meta *ch.Meta) {
				meta.Key = ch.ChannelKey("1:other")
			},
		},
		{
			name: "channel id mismatch",
			mutate: func(meta *ch.Meta) {
				meta.ID = ch.ChannelID{ID: "other", Type: 1}
			},
		},
		{
			name: "lower channel epoch",
			mutate: func(meta *ch.Meta) {
				meta.Epoch = 3
				meta.LeaderEpoch = 99
			},
		},
		{
			name: "lower leader epoch",
			mutate: func(meta *ch.Meta) {
				meta.LeaderEpoch = 6
			},
		},
		{
			name: "equal fence different leader",
			mutate: func(meta *ch.Meta) {
				meta.Leader = 3
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state, baseline := loadedMetaState(t)
			state.PendingAppends[11] = &AppendWaiter{OpID: 11}
			state.InflightAppend = &AppendOp{OpID: 12}
			before := snapshotMetaState(state)
			candidate := baseline
			candidate.LeaseUntil = baseline.LeaseUntil.Add(2 * time.Hour)
			candidate.RetentionThroughSeq = baseline.RetentionThroughSeq + 10
			candidate.WriteFence = ch.WriteFence{Token: "stale", Version: 99, Reason: ch.WriteFenceReasonFailover}
			candidate.Status = ch.StatusDeleting
			tt.mutate(&candidate)

			assert.ErrorIs(t, state.ValidateMeta(candidate), ch.ErrStaleMeta)
			decision := state.ApplyMeta(candidate)

			require.ErrorIs(t, decision.Err, ch.ErrStaleMeta)
			require.Equal(t, before, snapshotMetaState(state))
		})
	}
}

func TestApplyMetaEqualFenceSameLeaderRefreshesAuthorityFields(t *testing.T) {
	state, baseline := loadedMetaState(t)
	refresh := baseline
	refresh.Replicas = []ch.NodeID{2, 1, 3}
	refresh.ISR = []ch.NodeID{2, 1}
	refresh.MinISR = 2
	refresh.LeaseUntil = baseline.LeaseUntil.Add(time.Minute)
	refresh.RetentionThroughSeq = baseline.RetentionThroughSeq + 4
	refresh.WriteFence = ch.WriteFence{Token: "migration-2", Version: 8, Reason: ch.WriteFenceReasonReplicaReplace}
	refresh.Status = ch.StatusDeleting

	decision := state.ApplyMeta(refresh)

	require.NoError(t, decision.Err)
	require.Equal(t, baseline.ID, state.ID)
	require.Equal(t, baseline.Epoch, state.Epoch)
	require.Equal(t, baseline.LeaderEpoch, state.LeaderEpoch)
	require.Equal(t, baseline.Leader, state.Leader)
	require.Equal(t, refresh.Replicas, state.Replicas)
	require.Equal(t, refresh.ISR, state.ISR)
	require.Equal(t, refresh.MinISR, state.MinISR)
	require.Equal(t, refresh.LeaseUntil, state.LeaseUntil)
	require.Equal(t, refresh.RetentionThroughSeq, state.RetentionThroughSeq)
	require.Equal(t, refresh.WriteFence, state.WriteFence)
	require.Equal(t, refresh.Status, state.Status)
}

func TestApplyMetaHigherFenceMayChangeLeaderAndRole(t *testing.T) {
	tests := []struct {
		name        string
		epoch       uint64
		leaderEpoch uint64
	}{
		{name: "higher leader epoch", epoch: 4, leaderEpoch: 8},
		{name: "higher channel epoch resets leader epoch", epoch: 5, leaderEpoch: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state, baseline := loadedMetaState(t)
			next := baseline
			next.Epoch = tt.epoch
			next.LeaderEpoch = tt.leaderEpoch
			next.Leader = state.LocalNode

			decision := state.ApplyMeta(next)

			require.NoError(t, decision.Err)
			require.Equal(t, tt.epoch, state.Epoch)
			require.Equal(t, tt.leaderEpoch, state.LeaderEpoch)
			require.Equal(t, state.LocalNode, state.Leader)
			require.Equal(t, ch.RoleLeader, state.Role)
		})
	}
}

type metaStateSnapshot struct {
	ID                  ch.ChannelID
	Epoch               uint64
	LeaderEpoch         uint64
	Leader              ch.NodeID
	Role                ch.Role
	Status              ch.Status
	Replicas            []ch.NodeID
	ISR                 []ch.NodeID
	MinISR              int
	LeaseUntil          time.Time
	RetentionThroughSeq uint64
	WriteFence          ch.WriteFence
	CommitReady         bool
	PendingAppends      int
	InflightAppendOpID  ch.OpID
}

func loadedMetaState(t *testing.T) (*ChannelState, ch.Meta) {
	t.Helper()
	state := NewChannelState(ch.ChannelKey("1:a"), ch.NodeID(1), 1)
	meta := ch.Meta{
		Key:                 state.Key,
		ID:                  ch.ChannelID{ID: "a", Type: 1},
		Epoch:               4,
		LeaderEpoch:         7,
		Leader:              2,
		Replicas:            []ch.NodeID{1, 2, 3},
		ISR:                 []ch.NodeID{1, 2, 3},
		MinISR:              2,
		LeaseUntil:          time.Unix(1700000000, 0).UTC(),
		RetentionThroughSeq: 5,
		WriteFence:          ch.WriteFence{Token: "migration-1", Version: 7, Reason: ch.WriteFenceReasonLeaderTransfer},
		Status:              ch.StatusActive,
	}
	require.NoError(t, state.ApplyMeta(meta).Err)
	return state, meta
}

func snapshotMetaState(state *ChannelState) metaStateSnapshot {
	snapshot := metaStateSnapshot{
		ID:                  state.ID,
		Epoch:               state.Epoch,
		LeaderEpoch:         state.LeaderEpoch,
		Leader:              state.Leader,
		Role:                state.Role,
		Status:              state.Status,
		Replicas:            append([]ch.NodeID(nil), state.Replicas...),
		ISR:                 append([]ch.NodeID(nil), state.ISR...),
		MinISR:              state.MinISR,
		LeaseUntil:          state.LeaseUntil,
		RetentionThroughSeq: state.RetentionThroughSeq,
		WriteFence:          state.WriteFence,
		CommitReady:         state.CommitReady,
		PendingAppends:      len(state.PendingAppends),
	}
	if state.InflightAppend != nil {
		snapshot.InflightAppendOpID = state.InflightAppend.OpID
	}
	return snapshot
}
