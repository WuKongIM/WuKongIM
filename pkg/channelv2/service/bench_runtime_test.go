package service

import (
	"context"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/store"
	"github.com/stretchr/testify/require"
)

func TestServiceRuntimeSnapshotDelegatesToGroup(t *testing.T) {
	cluster, err := New(Config{LocalNode: 1, Store: store.NewMemoryFactory(), ReactorCount: 1})
	require.NoError(t, err)
	defer cluster.Close()

	meta := serviceBenchRuntimeMeta("service-runtime-snapshot", 1, 1)
	require.NoError(t, cluster.ApplyMeta(meta))

	bench, ok := cluster.(ch.RuntimeBench)
	require.True(t, ok)
	snapshot, err := bench.RuntimeSnapshot(context.Background())
	require.NoError(t, err)

	require.Equal(t, ch.NodeID(1), snapshot.NodeID)
	require.Equal(t, 1, snapshot.ActiveTotal)
	require.Equal(t, 1, snapshot.ActiveLeader)
	require.Equal(t, 0, snapshot.ActiveFollower)
	require.Len(t, snapshot.Reactors, 1)
}

func TestServiceRuntimeProbeDelegatesToGroup(t *testing.T) {
	cluster, err := New(Config{LocalNode: 1, Store: store.NewMemoryFactory(), ReactorCount: 1})
	require.NoError(t, err)
	defer cluster.Close()

	leaderMeta := serviceBenchRuntimeMeta("service-runtime-probe-leader", 1, 1)
	followerMeta := serviceBenchRuntimeMeta("service-runtime-probe-follower", 1, 2)
	require.NoError(t, cluster.ApplyMeta(leaderMeta))
	require.NoError(t, cluster.ApplyMeta(followerMeta))

	missing := ch.ChannelID{ID: "service-runtime-probe-missing", Type: 1}
	bench, ok := cluster.(ch.RuntimeBench)
	require.True(t, ok)
	result, err := bench.RuntimeProbe(context.Background(), ch.RuntimeSelector{ChannelIDs: []ch.ChannelID{leaderMeta.ID, followerMeta.ID, missing}})
	require.NoError(t, err)

	require.Equal(t, 3, result.Checked)
	require.Equal(t, 1, result.LoadedLeader)
	require.Equal(t, 1, result.LoadedFollower)
	require.Equal(t, []ch.ChannelID{missing}, result.Missing)
}

func TestServiceDrainChannelDelegatesToGroup(t *testing.T) {
	cluster, err := New(Config{LocalNode: 1, Store: store.NewMemoryFactory(), ReactorCount: 1})
	require.NoError(t, err)
	defer cluster.Close()

	meta := serviceBenchRuntimeMeta("service-runtime-drain", 1, 1)
	meta.WriteFence = ch.WriteFence{Token: "migration-1", Version: 1, Reason: ch.WriteFenceReasonLeaderTransfer}
	require.NoError(t, cluster.ApplyMeta(meta))

	drain, ok := cluster.(ch.RuntimeDrain)
	require.True(t, ok)
	result, err := drain.DrainChannel(context.Background(), ch.DrainChannelRequest{
		ChannelID:    meta.ID,
		LeaderEpoch:  meta.LeaderEpoch,
		FenceVersion: meta.WriteFence.Version,
		Timeout:      time.Millisecond,
	})
	require.NoError(t, err)
	require.True(t, result.Drained)
}

func TestServiceRuntimeEvictDelegatesToGroup(t *testing.T) {
	cluster, err := New(Config{LocalNode: 1, Store: store.NewMemoryFactory(), ReactorCount: 1})
	require.NoError(t, err)
	defer cluster.Close()

	meta := serviceBenchRuntimeMeta("service-runtime-evict", 1, 1)
	require.NoError(t, cluster.ApplyMeta(meta))

	bench, ok := cluster.(ch.RuntimeBench)
	require.True(t, ok)
	result, err := bench.RuntimeEvict(context.Background(), ch.RuntimeSelector{ChannelIDs: []ch.ChannelID{meta.ID}})
	require.NoError(t, err)

	require.Equal(t, 1, result.Requested)
	require.Equal(t, 1, result.Evicted)
	require.Equal(t, 0, result.SkippedBusy)
	require.Equal(t, 0, result.Missing)

	probe, err := bench.RuntimeProbe(context.Background(), ch.RuntimeSelector{ChannelIDs: []ch.ChannelID{meta.ID}})
	require.NoError(t, err)
	require.Equal(t, []ch.ChannelID{meta.ID}, probe.Missing)
}

func serviceBenchRuntimeMeta(id string, local ch.NodeID, leader ch.NodeID) ch.Meta {
	channelID := ch.ChannelID{ID: id, Type: 1}
	replicas := []ch.NodeID{local}
	if leader != local {
		replicas = append(replicas, leader)
	}
	return ch.Meta{
		Key:         ch.ChannelKeyForID(channelID),
		ID:          channelID,
		Epoch:       1,
		LeaderEpoch: 1,
		Leader:      leader,
		Replicas:    replicas,
		ISR:         replicas,
		MinISR:      1,
		Status:      ch.StatusActive,
	}
}
