package cluster

import (
	"context"
	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	channelstore "github.com/WuKongIM/WuKongIM/pkg/channel/store"
	channeltransport "github.com/WuKongIM/WuKongIM/pkg/channel/transport"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/channels"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	slotproxy "github.com/WuKongIM/WuKongIM/pkg/slot/proxy"
	"github.com/stretchr/testify/require"
	"testing"
)

type combinedHeadProbe struct {
	stageHeadReader
	ids   []ch.ChannelID
	metas []ch.Meta
}

func (r *combinedHeadProbe) ReadPersistedConversationHeadsResolved(_ context.Context, ids []ch.ChannelID, _ string, metas []ch.Meta, _ ...channels.ConversationBadgeQuery) ([]channels.ConversationHeadResult, error) {
	r.ids = ids
	r.metas = metas
	return make([]channels.ConversationHeadResult, len(ids)), nil
}
func TestNodeConversationCombinesMetadataBeforeHeadRead(t *testing.T) {
	ctx := context.Background()
	n, db := newLocalMetadataScanNode(t)
	n.defaultSlotProxy = slotproxy.NewChannelMetadataStore(n, db)
	id := keyForNodeHashSlot(t, 4, 0)
	ids := []ch.ChannelID{{ID: id, Type: 2}, {ID: id, Type: 3}, {ID: id, Type: 4}}
	shard := db.ForHashSlot(n.HashSlotForKey(id))
	require.NoError(t, shard.UpsertChannel(ctx, metadb.Channel{ChannelID: id, ChannelType: 2}))
	require.NoError(t, shard.UpsertChannelRuntimeMeta(ctx, metadb.ChannelRuntimeMeta{ChannelID: id, ChannelType: 2, Leader: 1, ChannelEpoch: 5, LeaderEpoch: 7, RouteGeneration: 9, Replicas: []uint64{1}, ISR: []uint64{1}, MinISR: 1, Status: 2, RetentionThroughSeq: 3}))
	require.NoError(t, shard.UpsertChannel(ctx, metadb.Channel{ChannelID: id, ChannelType: 4, Disband: 1}))
	probe := &combinedHeadProbe{}
	n.channels = probe
	n.defaultChannels = true
	out, err := n.ReadChannelPersistedConversationHeads(ctx, ids, "u")
	require.NoError(t, err)
	require.Len(t, out, 3)
	require.NoError(t, out[0].Err)
	require.ErrorIs(t, out[1].Err, ch.ErrChannelNotFound)
	require.ErrorIs(t, out[2].Err, ch.ErrChannelNotFound)
	require.Equal(t, ids[:1], probe.ids)
	require.Len(t, probe.metas, 1)
	require.Equal(t, uint64(5), probe.metas[0].Epoch)
	require.Equal(t, uint64(7), probe.metas[0].LeaderEpoch)
	require.Equal(t, uint64(9), probe.metas[0].RouteGeneration)
	require.Equal(t, uint64(3), probe.metas[0].RetentionThroughSeq)
}

type unusedConversationRuntime struct {
	ch.Cluster
	channeltransport.Server
}

func TestCustomChannelServiceKeepsItsMetadataSource(t *testing.T) {
	ctx := context.Background()
	n, db := newLocalMetadataScanNode(t)
	n.defaultSlotProxy = slotproxy.NewChannelMetadataStore(n, db)
	id := ch.ChannelID{ID: keyForNodeHashSlot(t, 4, 0), Type: 2}
	custom, err := channels.NewService(channels.Config{Runtime: unusedConversationRuntime{}, LocalNode: 1, Store: channelstore.NewMemoryFactory(), MetaSource: channels.NewStaticMetaSource([]ch.Meta{{ID: id, Leader: 1, Epoch: 1, LeaderEpoch: 1, Replicas: []ch.NodeID{1}, ISR: []ch.NodeID{1}, MinISR: 1, Status: ch.StatusActive}})})
	require.NoError(t, err)
	WithChannels(custom)(n)
	out, err := n.ReadChannelPersistedConversationHeads(ctx, []ch.ChannelID{id}, "u")
	require.NoError(t, err)
	require.Len(t, out, 1)
	require.NoError(t, out[0].Err)
}
