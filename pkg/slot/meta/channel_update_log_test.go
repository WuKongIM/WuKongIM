package meta

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestShardBatchGetChannelUpdateLogsReturnsLatestEntries(t *testing.T) {
	shard := openConversationTestShard(t)
	ctx := context.Background()

	require.NoError(t, shard.UpsertChannelUpdateLog(ctx, ChannelUpdateLog{
		ChannelID:       "g1",
		ChannelType:     2,
		UpdatedAt:       100,
		LastMsgSeq:      10,
		LastClientMsgNo: "c1",
		LastMsgAt:       200,
	}))
	require.NoError(t, shard.UpsertChannelUpdateLog(ctx, ChannelUpdateLog{
		ChannelID:       "g1",
		ChannelType:     2,
		UpdatedAt:       110,
		LastMsgSeq:      11,
		LastClientMsgNo: "c2",
		LastMsgAt:       210,
	}))
	require.NoError(t, shard.UpsertChannelUpdateLog(ctx, ChannelUpdateLog{
		ChannelID:       "g2",
		ChannelType:     2,
		UpdatedAt:       120,
		LastMsgSeq:      20,
		LastClientMsgNo: "c3",
		LastMsgAt:       220,
	}))

	got, err := shard.BatchGetChannelUpdateLogs(ctx, []ConversationKey{
		{ChannelID: "g1", ChannelType: 2},
		{ChannelID: "g2", ChannelType: 2},
		{ChannelID: "g3", ChannelType: 2},
	})
	require.NoError(t, err)
	require.Equal(t, map[ConversationKey]ChannelUpdateLog{
		{ChannelID: "g1", ChannelType: 2}: {
			ChannelID:       "g1",
			ChannelType:     2,
			UpdatedAt:       110,
			LastMsgSeq:      11,
			LastClientMsgNo: "c2",
			LastMsgAt:       210,
		},
		{ChannelID: "g2", ChannelType: 2}: {
			ChannelID:       "g2",
			ChannelType:     2,
			UpdatedAt:       120,
			LastMsgSeq:      20,
			LastClientMsgNo: "c3",
			LastMsgAt:       220,
		},
	}, got)
}

func TestShardDeleteChannelUpdateLogsRemovesEntries(t *testing.T) {
	shard := openConversationTestShard(t)
	ctx := context.Background()

	require.NoError(t, shard.UpsertChannelUpdateLog(ctx, ChannelUpdateLog{
		ChannelID:       "g1",
		ChannelType:     2,
		UpdatedAt:       100,
		LastMsgSeq:      10,
		LastClientMsgNo: "c1",
		LastMsgAt:       200,
	}))
	require.NoError(t, shard.UpsertChannelUpdateLog(ctx, ChannelUpdateLog{
		ChannelID:       "g2",
		ChannelType:     2,
		UpdatedAt:       120,
		LastMsgSeq:      20,
		LastClientMsgNo: "c2",
		LastMsgAt:       220,
	}))

	require.NoError(t, shard.DeleteChannelUpdateLogs(ctx, []ConversationKey{
		{ChannelID: "g1", ChannelType: 2},
	}))

	got, err := shard.BatchGetChannelUpdateLogs(ctx, []ConversationKey{
		{ChannelID: "g1", ChannelType: 2},
		{ChannelID: "g2", ChannelType: 2},
	})
	require.NoError(t, err)
	require.Equal(t, map[ConversationKey]ChannelUpdateLog{
		{ChannelID: "g2", ChannelType: 2}: {
			ChannelID:       "g2",
			ChannelType:     2,
			UpdatedAt:       120,
			LastMsgSeq:      20,
			LastClientMsgNo: "c2",
			LastMsgAt:       220,
		},
	}, got)
}
