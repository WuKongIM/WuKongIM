package wkdb

import (
	"context"
	"fmt"
	"hash/fnv"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/trace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeleteConversationsBatchRemovesLocalUserRelations(t *testing.T) {
	d := newShardedInternalTestDB(t, 8)
	err := d.Open()
	require.NoError(t, err)

	defer func() {
		err := d.Close()
		assert.NoError(t, err)
	}()

	uid := "batch-delete-user"
	channels := channelsOutsideUserShard(t, d, uid, 2)
	conversations := []Conversation{
		{
			Uid:          uid,
			ChannelId:    channels[0].ChannelId,
			ChannelType:  channels[0].ChannelType,
			UnreadCount:  1,
			ReadToMsgSeq: 1,
		},
		{
			Uid:          uid,
			ChannelId:    channels[1].ChannelId,
			ChannelType:  channels[1].ChannelType,
			UnreadCount:  2,
			ReadToMsgSeq: 2,
		},
	}

	require.NoError(t, d.AddOrUpdateConversationsWithUser(uid, conversations))

	for _, channel := range channels {
		localUsers, err := d.GetChannelConversationLocalUsers(channel.ChannelId, channel.ChannelType)
		require.NoError(t, err)
		require.Equal(t, []string{uid}, localUsers)
	}

	err = d.DeleteConversations(uid, channels)
	require.NoError(t, err)

	conversationsAfterDelete, err := d.GetConversations(uid)
	require.NoError(t, err)
	require.Len(t, conversationsAfterDelete, 0)

	for _, channel := range channels {
		localUsers, err := d.GetChannelConversationLocalUsers(channel.ChannelId, channel.ChannelType)
		require.NoError(t, err)
		assert.NotContains(t, localUsers, uid)
	}
}

func newShardedInternalTestDB(t testing.TB, shardNum int) *wukongDB {
	t.Helper()

	dr := t.TempDir()
	traceObj := trace.New(
		context.Background(),
		trace.NewOptions(
			trace.WithServiceName("test"),
			trace.WithServiceHostName("host"),
		),
	)
	trace.SetGlobalTrace(traceObj)

	return NewWukongDB(NewOptions(WithDir(dr), WithShardNum(shardNum))).(*wukongDB)
}

func channelsOutsideUserShard(t *testing.T, d *wukongDB, uid string, count int) []Channel {
	t.Helper()

	userShard := testShardID(uid, d.GetShardNum())
	channels := make([]Channel, 0, count)
	usedShards := make(map[uint32]struct{})

	for i := 0; len(channels) < count && i < 256; i++ {
		channel := Channel{ChannelId: fmt.Sprintf("batch-delete-channel-%d", i), ChannelType: 1}
		channelShard := d.GetChannelShardIndex(channel.ChannelId, channel.ChannelType)
		if channelShard == userShard {
			continue
		}
		if _, exists := usedShards[channelShard]; exists {
			continue
		}
		usedShards[channelShard] = struct{}{}
		channels = append(channels, channel)
	}

	require.Len(t, channels, count)
	return channels
}

func testShardID(value string, shardNum int) uint32 {
	h := fnv.New32()
	_, _ = h.Write([]byte(value))
	return h.Sum32() % uint32(shardNum)
}
