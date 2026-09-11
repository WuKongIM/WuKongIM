package cluster

import (
	"context"
	"errors"
	"fmt"
	"math"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/usecase/message"
	pluginusecase "github.com/WuKongIM/WuKongIM/internal/usecase/plugin"
	channelruntime "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

func TestPluginChannelOwnerBatchUsesCurrentAuthorityAndBoundsRequests(t *testing.T) {
	node := &batchPluginOwnerNode{metas: make(map[metadb.ChannelKey]metadb.ChannelRuntimeMeta)}
	ids := make([]message.ChannelID, 655)
	for i := range ids {
		ids[i] = message.ChannelID{ID: fmt.Sprintf("channel-%d", i), Type: 2}
		key := metadb.ChannelKey{ChannelID: ids[i].ID, ChannelType: 2}
		node.metas[key] = metadb.ChannelRuntimeMeta{ChannelID: key.ChannelID, ChannelType: 2, Leader: 2}
	}
	node.cached = channelruntime.Meta{Leader: 3}
	reader := NewPluginChannelOwnerReader(node)
	owners, err := reader.ChannelOwnerNodes(context.Background(), ids)
	require.NoError(t, err)
	require.Len(t, owners, len(ids))
	for _, owner := range owners {
		require.Equal(t, uint64(2), owner)
	}
	require.Equal(t, []int{512, 143}, node.batchSizes)
	require.Zero(t, node.resolveCalls, "existing rows cannot use stale append cache")
	key := metadb.ChannelKey{ChannelID: ids[0].ID, ChannelType: 2}
	node.metas[key] = metadb.ChannelRuntimeMeta{ChannelID: key.ChannelID, ChannelType: 2, Leader: 1}
	owners, err = reader.ChannelOwnerNodes(context.Background(), ids[:1])
	require.NoError(t, err)
	require.Equal(t, []uint64{1}, owners)
	node.batchErr = context.DeadlineExceeded
	_, err = reader.ChannelOwnerNodes(context.Background(), ids[:1])
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Zero(t, node.resolveCalls)
	node.batchErr = nil
	delete(node.metas, key)
	owners, err = reader.ChannelOwnerNodes(context.Background(), ids[:1])
	require.NoError(t, err)
	require.Equal(t, []uint64{3}, owners)
	require.Equal(t, 1, node.resolveCalls)
}

type batchPluginOwnerNode struct {
	recordingPluginChannelOwnerNode
	metas      map[metadb.ChannelKey]metadb.ChannelRuntimeMeta
	batchSizes []int
	batchErr   error
}

func (n *batchPluginOwnerNode) BatchGetChannelRuntimeMetas(_ context.Context, keys []metadb.ChannelKey) (map[metadb.ChannelKey]metadb.ChannelRuntimeMeta, error) {
	n.batchSizes = append(n.batchSizes, len(keys))
	return n.metas, n.batchErr
}

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

func TestPluginChannelOwnerReaderFollowsDurableLeaderAfterFailover(t *testing.T) {
	node := &recordingPluginChannelOwnerNode{
		meta:   metadb.ChannelRuntimeMeta{ChannelID: "g1", ChannelType: 2, Leader: 3},
		cached: channelruntime.Meta{Leader: 3},
	}
	reader := NewPluginChannelOwnerReader(node)

	owner, err := reader.ChannelOwnerNode(context.Background(), message.ChannelID{ID: "g1", Type: 2})

	require.NoError(t, err)
	require.Equal(t, uint64(3), owner)
	require.Equal(t, channelruntime.ChannelID{ID: "g1", Type: 2}, node.last)
	node.meta.Leader = 1
	owner, err = reader.ChannelOwnerNode(context.Background(), message.ChannelID{ID: "g1", Type: 2})
	require.NoError(t, err)
	require.Equal(t, uint64(1), owner)
	require.Zero(t, node.resolveCalls, "existing ownership must not reuse the stale append cache")
}

func TestPluginChannelOwnerReaderDoesNotHideAuthorityReadFailure(t *testing.T) {
	node := &recordingPluginChannelOwnerNode{err: context.DeadlineExceeded, cached: channelruntime.Meta{Leader: 3}}
	owner, err := NewPluginChannelOwnerReader(node).ChannelOwnerNode(context.Background(), message.ChannelID{ID: "g1", Type: 2})
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Zero(t, owner)
	require.Zero(t, node.resolveCalls)
}

func TestPluginChannelOwnerReaderInitializesOnlyMissingRuntimeMeta(t *testing.T) {
	node := &recordingPluginChannelOwnerNode{err: metadb.ErrNotFound, cached: channelruntime.Meta{Leader: 3}}
	reader := NewPluginChannelOwnerReader(node)
	owner, err := reader.ChannelOwnerNode(context.Background(), message.ChannelID{ID: "g1", Type: 2})
	require.NoError(t, err)
	require.Equal(t, uint64(3), owner)
	require.Equal(t, 1, node.resolveCalls)
	node.resolveErr = errors.New("placement unavailable")
	_, err = reader.ChannelOwnerNode(context.Background(), message.ChannelID{ID: "g1", Type: 2})
	require.ErrorIs(t, err, node.resolveErr)
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
	last         channelruntime.ChannelID
	meta         metadb.ChannelRuntimeMeta
	cached       channelruntime.Meta
	err          error
	resolveErr   error
	resolveCalls int
}

func (n *recordingPluginChannelOwnerNode) GetChannelRuntimeMeta(_ context.Context, id string, typ int64) (metadb.ChannelRuntimeMeta, error) {
	n.last = channelruntime.ChannelID{ID: id, Type: uint8(typ)}
	return n.meta, n.err
}

func (n *recordingPluginChannelOwnerNode) ResolveChannelAppendAuthority(_ context.Context, id channelruntime.ChannelID) (channelruntime.Meta, error) {
	n.last = id
	n.resolveCalls++
	return n.cached, n.resolveErr
}
