package machine

import (
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/stretchr/testify/require"
)

func TestAdvanceHWUsesISRMinISR(t *testing.T) {
	state := leaderState(t, 1, []ch.NodeID{1, 2, 3}, []ch.NodeID{1, 2, 3}, 2)
	state.Progress[1] = ReplicaProgress{Match: 105}
	state.Progress[2] = ReplicaProgress{Match: 104}
	state.Progress[3] = ReplicaProgress{Match: 80}
	advanced := state.AdvanceHW()
	require.True(t, advanced)
	require.Equal(t, uint64(104), state.HW)
}

func TestAdvanceHWSingleNodeCluster(t *testing.T) {
	state := leaderState(t, 1, []ch.NodeID{1}, []ch.NodeID{1}, 1)
	state.Progress[1] = ReplicaProgress{Match: 7}
	require.True(t, state.AdvanceHW())
	require.Equal(t, uint64(7), state.HW)
}

func leaderState(t *testing.T, local ch.NodeID, replicas, isr []ch.NodeID, minISR int) *ChannelState {
	t.Helper()
	state := NewChannelState(ch.ChannelKey("1:a"), local, 1)
	decision := state.ApplyMeta(ch.Meta{Key: state.Key, ID: ch.ChannelID{ID: "a", Type: 1}, Epoch: 1, LeaderEpoch: 1, Leader: local, Replicas: replicas, ISR: isr, MinISR: minISR, Status: ch.StatusActive})
	require.NoError(t, decision.Err)
	return state
}

func followerState(t *testing.T, local, leader ch.NodeID) *ChannelState {
	t.Helper()
	state := NewChannelState(ch.ChannelKey("1:a"), local, 1)
	decision := state.ApplyMeta(ch.Meta{Key: state.Key, ID: ch.ChannelID{ID: "a", Type: 1}, Epoch: 1, LeaderEpoch: 1, Leader: leader, Replicas: []ch.NodeID{leader, local}, ISR: []ch.NodeID{leader, local}, MinISR: 2, Status: ch.StatusActive})
	require.NoError(t, decision.Err)
	return state
}
