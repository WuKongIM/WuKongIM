//go:build integration

package proxy

import (
	"context"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/stretchr/testify/require"
	"sync/atomic"
	"testing"
)

func TestConversationMetadataReadsLifecycleAndRuntimeAtSameOwner(t *testing.T) {
	ctx := context.Background()
	nodes := startTwoNodeHashSlotStores(t, 256)
	id := findChannelIDForSlot(t, nodes[0].cluster, 2, "combined-conversation")
	shard := nodes[1].db.ForHashSlot(mustHashSlotForKey(t, nodes[1].cluster, id))
	require.NoError(t, shard.UpsertChannel(ctx, metadb.Channel{ChannelID: id, ChannelType: 2}))
	meta := metadb.ChannelRuntimeMeta{ChannelID: id, ChannelType: 2, Leader: 2, ChannelEpoch: 3, LeaderEpoch: 5, RouteGeneration: 7, Replicas: []uint64{1, 2}, ISR: []uint64{1, 2}, MinISR: 2, Status: 2, RetentionThroughSeq: 9}
	require.NoError(t, shard.UpsertChannelRuntimeMeta(ctx, meta))
	counter := &conversationRPCCount{proxyTestCluster: nodes[0].cluster}
	nodes[0].store.cluster = counter
	results := nodes[0].store.ReadPermissionMetadataBatch(ctx, []PermissionMetadataRead{{Kind: PermissionMetadataReadConversation, ChannelID: id, ChannelType: 2}})
	require.Equal(t, int32(1), counter.calls.Load(), "one combined RPC per remote Slot")
	require.Len(t, results, 1)
	require.NoError(t, results[0].Err)
	require.True(t, results[0].Found)
	require.NotNil(t, results[0].Runtime)
	require.Equal(t, metadb.NormalizeChannelRuntimeMeta(meta), *results[0].Runtime)
	// Local and remote ingress must agree on the same owner records.
	local := nodes[1].store.ReadPermissionMetadataBatch(ctx, []PermissionMetadataRead{{Kind: PermissionMetadataReadConversation, ChannelID: id, ChannelType: 2}})
	require.Equal(t, local, results)
	results[0].Runtime.Replicas[0] = 99
	again := nodes[0].store.ReadPermissionMetadataBatch(ctx, []PermissionMetadataRead{{Kind: PermissionMetadataReadConversation, ChannelID: id, ChannelType: 2}})
	require.Equal(t, uint64(1), again[0].Runtime.Replicas[0])
	// Absent business rows remain compatible with legacy runtime-only Channels.
	missing := findChannelIDForSlot(t, nodes[0].cluster, 2, "missing-runtime")
	missingResults := nodes[0].store.ReadPermissionMetadataBatch(ctx, []PermissionMetadataRead{{Kind: PermissionMetadataReadConversation, ChannelID: missing, ChannelType: 2}})
	require.NoError(t, missingResults[0].Err)
	require.False(t, missingResults[0].Found)
	require.Nil(t, missingResults[0].Runtime)
	require.NoError(t, shard.UpsertChannel(ctx, metadb.Channel{ChannelID: id, ChannelType: 2, Disband: 1}))
	disbanded := nodes[0].store.ReadPermissionMetadataBatch(ctx, []PermissionMetadataRead{{Kind: PermissionMetadataReadConversation, ChannelID: id, ChannelType: 2}})
	require.NoError(t, disbanded[0].Err)
	require.Equal(t, int64(1), disbanded[0].Channel.Disband)
	require.Nil(t, disbanded[0].Runtime)
	// A former owner cannot return the compound tuple after authority changes.
	unstable := &conversationOwnerTransition{Cluster: nodes[1].cluster}
	guarded := &Store{cluster: unstable, db: nodes[1].db}
	_, err := guarded.readPermissionMetadataLocal(ctx, 2, []PermissionMetadataRead{{Kind: PermissionMetadataReadConversation, ChannelID: id, ChannelType: 2}})
	require.ErrorIs(t, err, ErrReadStaleRoute)

}

type conversationRPCCount struct {
	*proxyTestCluster
	calls atomic.Int32
}

func (c *conversationRPCCount) RPCService(ctx context.Context, node multiraft.NodeID, slot multiraft.SlotID, service uint8, payload []byte) ([]byte, error) {
	c.calls.Add(1)
	return c.proxyTestCluster.RPCService(ctx, node, slot, service, payload)
}

type conversationOwnerTransition struct {
	Cluster
	calls int
}

func (c *conversationOwnerTransition) LeaderOf(slot multiraft.SlotID) (multiraft.NodeID, error) {
	c.calls++
	if c.calls > 1 {
		return 1, nil
	}
	return c.Cluster.LeaderOf(slot)
}
