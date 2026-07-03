package cluster

import (
	"context"
	"math"
	"testing"

	"github.com/WuKongIM/WuKongIM/internalv2/usecase/message"
	pluginusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/plugin"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
	"github.com/stretchr/testify/require"
)

func TestPluginClusterReaderMapsControlSnapshot(t *testing.T) {
	node := &recordingPluginClusterNode{snapshot: control.Snapshot{
		Nodes: []control.Node{
			{NodeID: 2, Addr: "127.0.0.1:7002", Status: control.NodeDown},
			{NodeID: 1, Addr: "127.0.0.1:7001", Status: control.NodeAlive},
		},
		Slots: []control.SlotAssignment{{
			SlotID:          7,
			DesiredPeers:    []uint64{1, 2},
			ConfigEpoch:     uint64(math.MaxUint32) + 1,
			PreferredLeader: 2,
		}},
	}}
	reader := NewPluginClusterReader(node)

	got, err := reader.ClusterSnapshot(context.Background())

	require.NoError(t, err)
	require.Equal(t, 1, node.calls)
	require.Equal(t, []pluginusecase.ClusterNode{
		{ID: 2, ClusterAddr: "127.0.0.1:7002", Online: false},
		{ID: 1, ClusterAddr: "127.0.0.1:7001", Online: true},
	}, got.Nodes)
	require.Len(t, got.Slots, 1)
	require.Equal(t, uint32(7), got.Slots[0].ID)
	require.Equal(t, uint64(2), got.Slots[0].Leader)
	require.Equal(t, uint32(math.MaxUint32), got.Slots[0].Term)
	require.Equal(t, []uint64{1, 2}, got.Slots[0].Replicas)
	node.snapshot.Slots[0].DesiredPeers[0] = 99
	require.Equal(t, []uint64{1, 2}, got.Slots[0].Replicas)
}

func TestPluginChannelOwnerReaderUsesAppendAuthorityLeader(t *testing.T) {
	node := &recordingPluginChannelOwnerNode{
		meta: channelv2.Meta{
			ID:     channelv2.ChannelID{ID: "g1", Type: 2},
			Leader: 3,
		},
	}
	reader := NewPluginChannelOwnerReader(node)

	owner, err := reader.ChannelOwnerNode(context.Background(), message.ChannelID{ID: "g1", Type: 2})

	require.NoError(t, err)
	require.Equal(t, uint64(3), owner)
	require.Equal(t, channelv2.ChannelID{ID: "g1", Type: 2}, node.last)
}

type recordingPluginClusterNode struct {
	calls    int
	snapshot control.Snapshot
	err      error
}

func (n *recordingPluginClusterNode) LocalControlSnapshot(context.Context) (control.Snapshot, error) {
	n.calls++
	if n.err != nil {
		return control.Snapshot{}, n.err
	}
	return n.snapshot.Clone(), nil
}

type recordingPluginChannelOwnerNode struct {
	last channelv2.ChannelID
	meta channelv2.Meta
	err  error
}

func (n *recordingPluginChannelOwnerNode) ResolveChannelAppendAuthority(_ context.Context, id channelv2.ChannelID) (channelv2.Meta, error) {
	n.last = id
	if n.err != nil {
		return channelv2.Meta{}, n.err
	}
	return n.meta, nil
}
