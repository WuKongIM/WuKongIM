package clusterstore_test

import (
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wkstore"
	"github.com/stretchr/testify/assert"
)

func TestAddOrUpdateConversations(t *testing.T) {
	s1, t1, s2, t2, s3, t3 := newTestClusterServerGroupThree()
	defer s1.Close()
	defer s2.Close()
	defer t1.Stop()
	defer t2.Stop()

	defer s3.Close()
	defer t3.Stop()

	t1.server.MustWaitAllSlotLeaderReady(time.Second * 20)
	t2.server.MustWaitAllSlotLeaderReady(time.Second * 20)
	t3.server.MustWaitAllSlotLeaderReady(time.Second * 20)

	uid := "test"
	// var channelType uint8 = 2

	// 节点1添加
	conversation := &wkstore.Conversation{
		ChannelID:   "test",
		ChannelType: 2,
		UnreadCount: 1,
		Timestamp:   time.Now().Unix(),
		LastMsgSeq:  1,
	}
	err := s1.AddOrUpdateConversations(uid, []*wkstore.Conversation{conversation})
	assert.NoError(t, err)

	time.Sleep(time.Millisecond * 200)

	// 节点2获取
	conversations, err := s2.GetConversations(uid)
	assert.NoError(t, err)
	assert.Equal(t, 1, len(conversations))

	assert.Equal(t, conversation.ChannelID, conversations[0].ChannelID)
	assert.Equal(t, conversation.ChannelType, conversations[0].ChannelType)
	assert.Equal(t, conversation.UnreadCount, conversations[0].UnreadCount)
	assert.Equal(t, conversation.Timestamp, conversations[0].Timestamp)
	assert.Equal(t, conversation.LastMsgSeq, conversations[0].LastMsgSeq)

	conversations, err = s1.GetConversations(uid)
	assert.NoError(t, err)
	assert.Equal(t, 1, len(conversations))
	assert.Equal(t, conversation.ChannelID, conversations[0].ChannelID)
	assert.Equal(t, conversation.ChannelType, conversations[0].ChannelType)
	assert.Equal(t, conversation.UnreadCount, conversations[0].UnreadCount)
	assert.Equal(t, conversation.Timestamp, conversations[0].Timestamp)
	assert.Equal(t, conversation.LastMsgSeq, conversations[0].LastMsgSeq)

	// 节点3获取
	conversations, err = s3.GetConversations(uid)
	assert.NoError(t, err)
	assert.Equal(t, 1, len(conversations))
	assert.Equal(t, conversation.ChannelID, conversations[0].ChannelID)
	assert.Equal(t, conversation.ChannelType, conversations[0].ChannelType)
	assert.Equal(t, conversation.UnreadCount, conversations[0].UnreadCount)
	assert.Equal(t, conversation.Timestamp, conversations[0].Timestamp)
	assert.Equal(t, conversation.LastMsgSeq, conversations[0].LastMsgSeq)
}

func TestDeleteConversation(t *testing.T) {
	s1, t1, s2, t2, s3, t3 := newTestClusterServerGroupThree()
	defer s1.Close()
	defer s2.Close()
	defer t1.Stop()
	defer t2.Stop()

	defer s3.Close()
	defer t3.Stop()

	t1.server.MustWaitAllSlotLeaderReady(time.Second * 20)
	t2.server.MustWaitAllSlotLeaderReady(time.Second * 20)
	t3.server.MustWaitAllSlotLeaderReady(time.Second * 20)

	uid := "test"
	// var channelType uint8 = 2

	// 节点1添加消息
	conversation := &wkstore.Conversation{
		ChannelID:   "test",
		ChannelType: 2,
		UnreadCount: 1,
		Timestamp:   time.Now().Unix(),
		LastMsgSeq:  1,
	}
	err := s1.AddOrUpdateConversations(uid, []*wkstore.Conversation{conversation})
	assert.NoError(t, err)

	time.Sleep(time.Millisecond * 200)

	// 节点2获取
	conversations, err := s2.GetConversations(uid)
	assert.NoError(t, err)
	assert.Equal(t, 1, len(conversations))

	conversations, err = s1.GetConversations(uid)
	assert.NoError(t, err)
	assert.Equal(t, 1, len(conversations))

	// 节点1删除
	err = s1.DeleteConversation(uid, conversation.ChannelID, conversation.ChannelType)
	assert.NoError(t, err)

	time.Sleep(time.Millisecond * 200)

	// 节点2获取
	conversations, err = s2.GetConversations(uid)
	assert.NoError(t, err)
	assert.Equal(t, 0, len(conversations))

	conversations, err = s1.GetConversations(uid)
	assert.NoError(t, err)
	assert.Equal(t, 0, len(conversations))
}
