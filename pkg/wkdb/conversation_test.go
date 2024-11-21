package wkdb_test

import (
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
