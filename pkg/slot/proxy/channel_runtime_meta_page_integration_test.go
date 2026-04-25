//go:build integration
// +build integration

package proxy

import (
	"context"
	"fmt"
	"sort"
	"testing"

	raftcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
	metadb "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/stretchr/testify/require"
)

func TestStoreScanChannelRuntimeMetaSlotPageReadsAuthoritativeSlot(t *testing.T) {
	ctx := context.Background()
	nodes := startTwoNodeHashSlotStores(t, 8)

	channelID := findChannelIDForSlot(t, nodes[0].cluster, 2, "remote-slot-page")
	meta := metadb.ChannelRuntimeMeta{
		ChannelID:    channelID,
		ChannelType:  1,
		ChannelEpoch: 21,
		LeaderEpoch:  8,
		Replicas:     []uint64{2},
		ISR:          []uint64{2},
		Leader:       2,
		MinISR:       1,
		Status:       2,
		Features:     7,
		LeaseUntilMS: 1700000000999,
	}
	require.NoError(t, nodes[1].db.ForHashSlot(mustHashSlotForKey(t, nodes[1].cluster, channelID)).UpsertChannelRuntimeMeta(ctx, meta))

	store, ok := any(nodes[0].store).(interface {
		ScanChannelRuntimeMetaSlotPage(ctx context.Context, slotID multiraft.SlotID, after metadb.ChannelRuntimeMetaCursor, limit int) ([]metadb.ChannelRuntimeMeta, metadb.ChannelRuntimeMetaCursor, bool, error)
	})
	require.True(t, ok, "channel runtime meta slot page methods missing")

	page, cursor, done, err := store.ScanChannelRuntimeMetaSlotPage(ctx, 2, metadb.ChannelRuntimeMetaCursor{}, 10)
	require.NoError(t, err)
	require.True(t, done)
	require.Equal(t, []metadb.ChannelRuntimeMeta{meta}, page)
	require.Equal(t, metadb.ChannelRuntimeMetaCursor{ChannelID: channelID, ChannelType: 1}, cursor)
}

func TestStoreScanChannelRuntimeMetaSlotPageMergesHashSlotsInChannelOrder(t *testing.T) {
	ctx := context.Background()
	nodes := startTwoNodeHashSlotStores(t, 8)

	channelIDs := findChannelIDsForSlotAcrossHashSlots(t, nodes[0].cluster, 2, 2, "slot-page-merge")
	firstID := channelIDs[0]
	secondID := channelIDs[1]

	metas := []metadb.ChannelRuntimeMeta{
		{
			ChannelID:    secondID,
			ChannelType:  2,
			ChannelEpoch: 33,
			LeaderEpoch:  13,
			Replicas:     []uint64{2},
			ISR:          []uint64{2},
			Leader:       2,
			MinISR:       1,
			Status:       2,
			Features:     3,
			LeaseUntilMS: 1700000003003,
		},
		{
			ChannelID:    firstID,
			ChannelType:  1,
			ChannelEpoch: 31,
			LeaderEpoch:  11,
			Replicas:     []uint64{2},
			ISR:          []uint64{2},
			Leader:       2,
			MinISR:       1,
			Status:       2,
			Features:     1,
			LeaseUntilMS: 1700000001001,
		},
		{
			ChannelID:    secondID,
			ChannelType:  1,
			ChannelEpoch: 32,
			LeaderEpoch:  12,
			Replicas:     []uint64{2},
			ISR:          []uint64{2},
			Leader:       2,
			MinISR:       1,
			Status:       2,
			Features:     2,
			LeaseUntilMS: 1700000002002,
		},
	}
	for _, meta := range metas {
		require.NoError(t, nodes[1].db.ForHashSlot(mustHashSlotForKey(t, nodes[1].cluster, meta.ChannelID)).UpsertChannelRuntimeMeta(ctx, meta))
	}

	store, ok := any(nodes[0].store).(interface {
		ScanChannelRuntimeMetaSlotPage(ctx context.Context, slotID multiraft.SlotID, after metadb.ChannelRuntimeMetaCursor, limit int) ([]metadb.ChannelRuntimeMeta, metadb.ChannelRuntimeMetaCursor, bool, error)
	})
	require.True(t, ok, "channel runtime meta slot page methods missing")

	page1, cursor, done, err := store.ScanChannelRuntimeMetaSlotPage(ctx, 2, metadb.ChannelRuntimeMetaCursor{}, 2)
	require.NoError(t, err)
	require.False(t, done)
	require.Equal(t, []metadb.ChannelRuntimeMeta{metas[1], metas[2]}, page1)
	require.Equal(t, metadb.ChannelRuntimeMetaCursor{ChannelID: secondID, ChannelType: 1}, cursor)

	page2, cursor, done, err := store.ScanChannelRuntimeMetaSlotPage(ctx, 2, cursor, 2)
	require.NoError(t, err)
	require.True(t, done)
	require.Equal(t, []metadb.ChannelRuntimeMeta{metas[0]}, page2)
	require.Equal(t, metadb.ChannelRuntimeMetaCursor{ChannelID: secondID, ChannelType: 2}, cursor)
}

func findChannelIDsForSlotAcrossHashSlots(t testing.TB, cluster *raftcluster.Cluster, slot uint64, count int, prefix string) []string {
	t.Helper()

	idsByHashSlot := make(map[uint16]string, count)
	for i := 0; i < 100_000 && len(idsByHashSlot) < count; i++ {
		channelID := fmt.Sprintf("%s-%06d", prefix, i)
		if uint64(cluster.SlotForKey(channelID)) != slot {
			continue
		}
		hashSlot := cluster.HashSlotForKey(channelID)
		if _, exists := idsByHashSlot[hashSlot]; exists {
			continue
		}
		idsByHashSlot[hashSlot] = channelID
	}
	if len(idsByHashSlot) < count {
		t.Fatalf("no channel ids found for slot %d across %d hash slots", slot, count)
	}

	channelIDs := make([]string, 0, len(idsByHashSlot))
	for _, channelID := range idsByHashSlot {
		channelIDs = append(channelIDs, channelID)
	}
	sort.Strings(channelIDs)
	return channelIDs
}
