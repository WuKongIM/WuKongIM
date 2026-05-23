package machine

import (
	"testing"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
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
