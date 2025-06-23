package wkdb_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/stretchr/testify/assert"
)

func TestConversation(t *testing.T) {

	d := newTestDB(t)
	err := d.Open()
	assert.NoError(t, err)

	defer func() {
		err := d.Close()
		assert.NoError(t, err)
	}()

	uid := "test1"
	createdAt := time.Now()
	updatedAt := time.Now()
	channelId := "1234"
	channelType := uint8(1)
	conversations := []wkdb.Conversation{
		{
			Id:           1,
			Uid:          uid,
			UnreadCount:  10,
			ChannelId:    channelId,
			ChannelType:  channelType,
			ReadToMsgSeq: 1,
			CreatedAt:    &createdAt,
			UpdatedAt:    &updatedAt,
		},
		{
			Id:           2,
			Uid:          uid,
			UnreadCount:  21,
			ChannelId:    "567",
			ChannelType:  2,
			ReadToMsgSeq: 2,
			CreatedAt:    &createdAt,
			UpdatedAt:    &updatedAt,
		},
	}

	t.Run("AddOrUpdateConversations", func(t *testing.T) {
		err = d.AddOrUpdateConversationsWithUser(uid, conversations)
		assert.NoError(t, err)

		uids, err := d.GetChannelConversationLocalUsers(channelId, 1)
		assert.NoError(t, err)
		assert.Len(t, uids, 1)
		assert.Equal(t, uid, uids[0])
	})

	t.Run("GetConversations", func(t *testing.T) {
		conversations2, err := d.GetConversations(uid)
		assert.NoError(t, err)

		assert.Len(t, conversations, len(conversations2))

		assert.Equal(t, conversations[0].Uid, conversations2[0].Uid)
		assert.Equal(t, conversations[0].ChannelId, conversations2[0].ChannelId)
		assert.Equal(t, conversations[0].ChannelType, conversations2[0].ChannelType)
		assert.Equal(t, conversations[0].UnreadCount, conversations2[0].UnreadCount)
		assert.Equal(t, conversations[0].ReadToMsgSeq, conversations2[0].ReadToMsgSeq)
		assert.Equal(t, conversations[0].CreatedAt.Unix(), conversations2[0].CreatedAt.Unix())
		assert.Equal(t, conversations[0].UpdatedAt.Unix(), conversations2[0].UpdatedAt.Unix())

		assert.Equal(t, conversations[1].Uid, conversations2[1].Uid)
		assert.Equal(t, conversations[1].ChannelId, conversations2[1].ChannelId)
		assert.Equal(t, conversations[1].ChannelType, conversations2[1].ChannelType)
		assert.Equal(t, conversations[1].UnreadCount, conversations2[1].UnreadCount)
		assert.Equal(t, conversations[1].ReadToMsgSeq, conversations2[1].ReadToMsgSeq)
		assert.Equal(t, conversations[1].CreatedAt.Unix(), conversations2[1].CreatedAt.Unix())
		assert.Equal(t, conversations[1].UpdatedAt.Unix(), conversations2[1].UpdatedAt.Unix())
	})

	t.Run("DeleteConversation", func(t *testing.T) {
		err = d.DeleteConversation(uid, channelId, channelType)
		assert.NoError(t, err)

		conversations2, err := d.GetConversations(uid)
		assert.NoError(t, err)

		assert.Len(t, conversations2, 1)
		conversations[1].UpdatedAt = nil
		conversations2[0].UpdatedAt = nil
		assert.Equal(t, conversations[1].Uid, conversations2[0].Uid)
		assert.Equal(t, conversations[1].ChannelId, conversations2[0].ChannelId)
		assert.Equal(t, conversations[1].ChannelType, conversations2[0].ChannelType)
		assert.Equal(t, conversations[1].UnreadCount, conversations2[0].UnreadCount)
		assert.Equal(t, conversations[1].ReadToMsgSeq, conversations2[0].ReadToMsgSeq)
	})

}

func TestDeleteConversation(t *testing.T) {
	d := newTestDB(t)
	err := d.Open()
	assert.NoError(t, err)

	defer func() {
		err := d.Close()
		assert.NoError(t, err)
	}()

	uid := "test1"
	conversations := []wkdb.Conversation{
		{
			Id:           1,
			Uid:          uid,
			ChannelId:    "1234",
			ChannelType:  1,
			UnreadCount:  20,
			ReadToMsgSeq: 2,
		},
		{
			Id:           2,
			Uid:          uid,
			ChannelId:    "4567",
			ChannelType:  1,
			UnreadCount:  30,
			ReadToMsgSeq: 10,
		},
	}

	err = d.AddOrUpdateConversationsWithUser(uid, conversations)
	assert.NoError(t, err)

	err = d.DeleteConversation(uid, "1234", 1)
	assert.NoError(t, err)

	conversations2, err := d.GetConversations(uid)
	assert.NoError(t, err)

	assert.Len(t, conversations2, 1)
	conversations[1].Id = conversations2[0].Id
	assert.Equal(t, conversations[1], conversations2[0])
}

// func TestGetConversationBySessionIds(t *testing.T) {
// 	d := newTestDB(t)
// 	err := d.Open()
// 	assert.NoError(t, err)

// 	defer func() {
// 		err := d.Close()
// 		assert.NoError(t, err)
// 	}()

// 	uid := "test1"
// 	conversations := []wkdb.Conversation{
// 		{
// 			Uid:            uid,
// 			ChannelId:      "1234",
// 			ChannelType:    1,
// 			UnreadCount:    20,
// 			ReadToMsgSeq: 2,
// 		},
// 		{
// 			Uid:            uid,
// 			ChannelId:      "5678",
// 			ChannelType:    1,
// 			UnreadCount:    30,
// 			ReadToMsgSeq: 10,
// 		},
// 	}

// 	err = d.AddOrUpdateConversations(uid, conversations)
// 	assert.NoError(t, err)

// 	conversations2, err := d.GetConversationBySessionIds(uid, []uint64{1, 2})
// 	assert.NoError(t, err)

// 	assert.Len(t, conversations2, 2)
// 	conversations[0].Id = conversations2[0].Id
// 	conversations[1].Id = conversations2[1].Id
// 	assert.Equal(t, conversations[0], conversations2[0])
// 	assert.Equal(t, conversations[1], conversations2[1])
// }

// 测试 GetLastConversations 批量查询优化
func TestGetLastConversationsBatch(t *testing.T) {
	d := newTestDB(t)
	err := d.Open()
	assert.NoError(t, err)

	defer func() {
		err := d.Close()
		assert.NoError(t, err)
	}()

	uid := "test_batch_user"
	now := time.Now()

	// 创建多个会话用于测试
	conversations := make([]wkdb.Conversation, 0, 20)
	for i := 0; i < 20; i++ {
		updatedAt := now.Add(time.Duration(i) * time.Minute)
		conversations = append(conversations, wkdb.Conversation{
			Id:           uint64(i + 1),
			Uid:          uid,
			ChannelId:    fmt.Sprintf("channel_%d", i),
			ChannelType:  1,
			Type:         wkdb.ConversationTypeChat,
			UnreadCount:  uint32(i + 1),
			ReadToMsgSeq: uint64(i + 1),
			CreatedAt:    &now,
			UpdatedAt:    &updatedAt,
		})
	}

	// 添加会话
	err = d.AddOrUpdateConversationsWithUser(uid, conversations)
	assert.NoError(t, err)

	// 测试批量查询
	result, err := d.GetLastConversations(uid, wkdb.ConversationTypeChat, 0, nil, 10)
	assert.NoError(t, err)
	assert.LessOrEqual(t, len(result), 10)

	// 验证结果按更新时间排序（最新的在前）
	for i := 1; i < len(result); i++ {
		assert.True(t, result[i-1].UpdatedAt.After(*result[i].UpdatedAt) || result[i-1].UpdatedAt.Equal(*result[i].UpdatedAt))
	}
}

// 测试 AddOrUpdateConversations 的性能
func BenchmarkAddOrUpdateConversations(b *testing.B) {
	d := newTestDB(b)
	err := d.Open()
	assert.NoError(b, err)

	defer func() {
		err := d.Close()
		assert.NoError(b, err)
	}()

	uid := "test1"
	createdAt := time.Now()
	updatedAt := time.Now()
	conversations := []wkdb.Conversation{
		{
			Id:           1,
			Uid:          uid,
			ChannelId:    "1234",
			ChannelType:  1,
			UnreadCount:  20,
			ReadToMsgSeq: 2,
			CreatedAt:    &createdAt,
			UpdatedAt:    &updatedAt,
		},
		{
			Id:           2,
			Uid:          uid,
			ChannelId:    "5678",
			ChannelType:  1,
			UnreadCount:  22,
			ReadToMsgSeq: 10,
			CreatedAt:    &createdAt,
			UpdatedAt:    &updatedAt,
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		err = d.AddOrUpdateConversationsWithUser(uid, conversations)
		assert.NoError(b, err)
	}
}

// 测试 GetLastConversations 的性能对比
func BenchmarkGetLastConversations(b *testing.B) {
	d := newTestDB(b)
	err := d.Open()
	assert.NoError(b, err)

	defer func() {
		err := d.Close()
		assert.NoError(b, err)
	}()

	uid := "bench_user"
	now := time.Now()

	// 创建大量会话数据
	conversations := make([]wkdb.Conversation, 0, 1000)
	for i := 0; i < 1000; i++ {
		updatedAt := now.Add(time.Duration(i) * time.Minute)
		conversations = append(conversations, wkdb.Conversation{
			Id:           uint64(i + 1),
			Uid:          uid,
			ChannelId:    fmt.Sprintf("channel_%d", i),
			ChannelType:  1,
			Type:         wkdb.ConversationTypeChat,
			UnreadCount:  uint32(i + 1),
			ReadToMsgSeq: uint64(i + 1),
			CreatedAt:    &now,
			UpdatedAt:    &updatedAt,
		})
	}

	err = d.AddOrUpdateConversationsWithUser(uid, conversations)
	assert.NoError(b, err)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := d.GetLastConversations(uid, wkdb.ConversationTypeChat, 0, nil, 20)
		assert.NoError(b, err)
	}
}
