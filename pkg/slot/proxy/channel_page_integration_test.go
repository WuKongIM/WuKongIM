//go:build integration
// +build integration

package proxy

import (
	"context"
	"testing"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/stretchr/testify/require"
)

func TestStoreScanChannelsSlotPageReadsAuthoritativeLocalSlot(t *testing.T) {
	nodes := startTwoNodeHashSlotStores(t, 8)
	ctx := context.Background()
	slotID := multiraft.SlotID(1)
	channelA := findChannelIDForSlot(t, nodes[0].cluster, uint64(slotID), "local-a")
	hashSlotA := mustHashSlotForKey(t, nodes[0].cluster, channelA)
	channelB := findChannelIDForSlotWithDifferentHashSlot(t, nodes[0].cluster, uint64(slotID), hashSlotA, "local-b")
	hashSlotB := mustHashSlotForKey(t, nodes[0].cluster, channelB)
	require.NoError(t, nodes[0].db.ForHashSlot(hashSlotA).CreateChannel(ctx, metadb.Channel{ChannelID: channelA, ChannelType: 2}))
	require.NoError(t, nodes[0].db.ForHashSlot(hashSlotB).CreateChannel(ctx, metadb.Channel{ChannelID: channelB, ChannelType: 2}))

	page, cursor, done, err := nodes[0].store.ScanChannelsSlotPage(ctx, slotID, metadb.ChannelCursor{}, 1)
	require.NoError(t, err)
	require.Len(t, page, 1)
	require.False(t, done)

	page2, _, done, err := nodes[0].store.ScanChannelsSlotPage(ctx, slotID, cursor, 10)
	require.NoError(t, err)
	require.True(t, done)
	allIDs := channelIDsFromMeta(append(page, page2...))
	require.Contains(t, allIDs, channelA)
	require.Contains(t, allIDs, channelB)
}

func TestStoreScanChannelsSlotPageReadsAuthoritativeRemoteSlot(t *testing.T) {
	nodes := startTwoNodeHashSlotStores(t, 8)
	ctx := context.Background()
	slotID := multiraft.SlotID(2)
	channelID := findChannelIDForSlot(t, nodes[1].cluster, uint64(slotID), "remote-scan")
	hashSlot := mustHashSlotForKey(t, nodes[1].cluster, channelID)
	require.NoError(t, nodes[1].db.ForHashSlot(hashSlot).CreateChannel(ctx, metadb.Channel{ChannelID: channelID, ChannelType: 2}))

	page, _, done, err := nodes[0].store.ScanChannelsSlotPage(ctx, slotID, metadb.ChannelCursor{}, 10)
	require.NoError(t, err)
	require.True(t, done)
	require.Contains(t, channelIDsFromMeta(page), channelID)
}

func channelIDsFromMeta(channels []metadb.Channel) []string {
	ids := make([]string, 0, len(channels))
	for _, ch := range channels {
		ids = append(ids, ch.ChannelID)
	}
	return ids
}
