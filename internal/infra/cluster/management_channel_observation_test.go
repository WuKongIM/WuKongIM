package cluster

import (
	"context"
	"errors"
	"sync"
	"testing"

	accessnode "github.com/WuKongIM/WuKongIM/internal/access/node"
	management "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/channels"
	"github.com/stretchr/testify/require"
)

// Failure inventory: per-channel RPC fanout; wrong target/order; old/unreachable
// peers reported inactive; local and remote implementations disagree; nil tail
// observations become fabricated zero values.
func TestManagementChannelObserverGroupsLeadersAndPreservesFailures(t *testing.T) {
	node := &channelObservationNode{calls: make(map[uint64]int)}
	observer := NewManagementChannelObserver(node)
	local := accessnode.New(accessnode.Options{ManagerChannelObservations: NewManagementChannelObserver(&channelObservationNode{localID: 2})})
	node.handler = local.HandleManagerChannelRPC
	targets := []management.ChannelObservationTarget{
		{ChannelID: "a", ChannelType: 2, LeaderID: 1},
		{ChannelID: "b", ChannelType: 2, LeaderID: 2},
		{ChannelID: "c", ChannelType: 2, LeaderID: 2},
		{ChannelID: "d", ChannelType: 2, LeaderID: 3},
	}
	got, err := observer.ObserveChannels(context.Background(), targets, false)
	require.NoError(t, err)
	require.Len(t, got, 4)
	for i := range targets {
		require.Equal(t, targets[i], got[i].Target)
	}
	require.True(t, got[0].Known)
	require.True(t, got[1].Known)
	require.True(t, got[2].Known)
	require.False(t, got[3].Known)
	require.False(t, got[3].Active)
	require.Nil(t, got[3].MaxMessageSeq)
	require.Equal(t, map[uint64]int{2: 1, 3: 1}, node.calls)
}

func TestManagementChannelObserverRejectsMalformedLocalResults(t *testing.T) {
	node := &channelObservationNode{malformed: true}
	target := management.ChannelObservationTarget{ChannelID: "a", ChannelType: 2, LeaderID: 1}
	_, err := NewManagementChannelObserver(node).ObserveLocalChannels(context.Background(), []management.ChannelObservationTarget{target}, false)
	require.Error(t, err)
}

type channelObservationNode struct {
	mu        sync.Mutex
	calls     map[uint64]int
	handler   func(context.Context, []byte) ([]byte, error)
	malformed bool
	localID   uint64
}

func (n *channelObservationNode) NodeID() uint64 {
	if n.localID != 0 {
		return n.localID
	}
	return 1
}
func (n *channelObservationNode) CallRPC(ctx context.Context, id uint64, _ uint8, payload []byte) ([]byte, error) {
	n.mu.Lock()
	n.calls[id]++
	n.mu.Unlock()
	if id == 3 {
		return nil, errors.New("unreachable")
	}
	return n.handler(ctx, payload)
}
func (n *channelObservationNode) ObserveLocalChannels(_ context.Context, targets []channels.RuntimeObservationTarget, _ bool) ([]channels.RuntimeObservation, error) {
	if n.malformed {
		return nil, nil
	}
	out := make([]channels.RuntimeObservation, len(targets))
	for i, target := range targets {
		out[i] = channels.RuntimeObservation{Target: target, Known: true}
	}
	return out, nil
}
