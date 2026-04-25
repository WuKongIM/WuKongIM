package channelmeta

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	"github.com/stretchr/testify/require"
)

func TestLivenessCacheWarmsOnMiss(t *testing.T) {
	cache := &LivenessCache{}
	source := &livenessSourceFake{nodes: []controllermeta.ClusterNode{
		{NodeID: 2, Status: controllermeta.NodeStatusAlive},
		{NodeID: 3, Status: controllermeta.NodeStatusDead},
	}}

	cache.Warm(context.Background(), 3, source)
	status, ok := cache.Status(3)
	require.True(t, ok)
	require.Equal(t, controllermeta.NodeStatusDead, status)

	cache.Warm(context.Background(), 3, source)
	require.Equal(t, 1, source.calls)
}

func TestLivenessCacheUpdateReportsUnhealthyTransitions(t *testing.T) {
	cache := &LivenessCache{}

	require.True(t, cache.Update(7, controllermeta.NodeStatusDead))
	require.False(t, cache.Update(7, controllermeta.NodeStatusDead))
	require.False(t, cache.Update(7, controllermeta.NodeStatusAlive))
	require.True(t, cache.Update(7, controllermeta.NodeStatusDraining))
}

func TestObservedLeaderRepairReasonDetectsReplicaLeaderDrift(t *testing.T) {
	meta := channel.Meta{
		Key:      "1:drift",
		Epoch:    4,
		Leader:   3,
		ISR:      []channel.NodeID{2, 3},
		Status:   channel.StatusActive,
		Features: channel.Features{MessageSeqFormat: channel.MessageSeqFormatLegacyU32},
	}
	state := channel.ReplicaState{
		ChannelKey:  meta.Key,
		Epoch:       4,
		Leader:      2,
		Role:        channel.ReplicaRoleLeader,
		CommitReady: true,
	}

	require.Equal(t, channel.LeaderRepairReasonLeaderDrift.String(), ObservedLeaderRepairReason(meta, state))

	state.Leader = 9
	require.Empty(t, ObservedLeaderRepairReason(meta, state))
	state.Leader = 2
	state.Epoch = 5
	require.Empty(t, ObservedLeaderRepairReason(meta, state))
	state.Epoch = 4
	state.Role = channel.ReplicaRoleTombstoned
	require.Empty(t, ObservedLeaderRepairReason(meta, state))
}

type livenessSourceFake struct {
	nodes []controllermeta.ClusterNode
	err   error
	calls int
}

func (f *livenessSourceFake) ListNodesStrict(context.Context) ([]controllermeta.ClusterNode, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return append([]controllermeta.ClusterNode(nil), f.nodes...), nil
}

func TestLivenessCacheWarmIgnoresSourceErrors(t *testing.T) {
	cache := &LivenessCache{}
	cache.Warm(context.Background(), 3, &livenessSourceFake{err: errors.New("controller unavailable")})
	_, ok := cache.Status(3)
	require.False(t, ok)
}

func TestMetaLeaseNeedsRenewalDetectsExpiredLease(t *testing.T) {
	now := time.UnixMilli(1_700_000_777_000).UTC()
	require.True(t, MetaLeaseNeedsRenewal(now.Add(-time.Millisecond).UnixMilli(), now, 0))
	require.True(t, MetaLeaseNeedsRenewal(now.Add(time.Second).UnixMilli(), now, time.Second))
	require.False(t, MetaLeaseNeedsRenewal(now.Add(2*time.Second).UnixMilli(), now, time.Second))
}
