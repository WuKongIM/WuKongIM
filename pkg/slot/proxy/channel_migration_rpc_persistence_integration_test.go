//go:build integration

package proxy

import (
	"context"
	"fmt"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/stretchr/testify/require"
)

func TestChannelMigrationListActiveTasksForNodeRPCClampsHugeLimitAndReportsHasMore(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	hashSlot := uint16(1)
	store := &Store{
		cluster: &proxyTestMigrationCluster{
			nodeID:      1,
			localNodeID: 1,
			slotIDs:     []multiraft.SlotID{1},
			hashSlots:   map[multiraft.SlotID][]uint16{1: {hashSlot}},
			leaders:     map[multiraft.SlotID]multiraft.NodeID{1: 1},
		},
		db: db,
	}
	const wantLimit = 1024
	batch := db.NewWriteBatch()
	t.Cleanup(func() {
		require.NoError(t, batch.Close())
	})
	for i := 0; i < wantLimit+1; i++ {
		task := proxyTestChannelMigrationTask(fmt.Sprintf("task-rpc-huge-limit-%04d", i), fmt.Sprintf("channel-rpc-huge-limit-%04d", i))
		task.SourceNode = 3
		task.TargetNode = 4
		require.NoError(t, batch.CreateChannelMigrationTask(hashSlot, task))
	}
	require.NoError(t, batch.Commit())
	body, err := encodeChannelMigrationRPCRequestBinary(channelMigrationRPCRequest{
		Op:     channelMigrationRPCListActiveForNode,
		SlotID: 1,
		NodeID: 3,
		Limit:  int(^uint(0) >> 1),
	})
	require.NoError(t, err)

	var resp channelMigrationRPCResponse
	require.NotPanics(t, func() {
		respBody, err := store.handleChannelMigrationRPC(ctx, body)
		require.NoError(t, err)
		resp, err = decodeChannelMigrationRPCResponse(respBody)
		require.NoError(t, err)
	})
	require.Equal(t, rpcStatusOK, resp.Status)
	require.Len(t, resp.Tasks, wantLimit)
	require.True(t, resp.HasMore)
}
