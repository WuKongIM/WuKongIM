package wkdb_test

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/stretchr/testify/assert"
)

func TestAddOrUpdateConversations(t *testing.T) {
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
			Uid:            uid,
			UnreadCount:    10,
			ChannelId:      "1234",
			ChannelType:    1,
			ReadedToMsgSeq: 1,
		},
		{
			Uid:            uid,
			UnreadCount:    21,
			ChannelId:      "567",
			ChannelType:    2,
			ReadedToMsgSeq: 2,
		},
	}

	err = d.AddOrUpdateConversations(uid, conversations)
	assert.NoError(t, err)

}

func TestGetConversations(t *testing.T) {
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
			Uid:            uid,
			ChannelId:      "1234",
			ChannelType:    1,
			UnreadCount:    20,
			ReadedToMsgSeq: 2,
		},
		{
			Uid:            uid,
			ChannelId:      "5678",
			ChannelType:    1,
			UnreadCount:    22,
			ReadedToMsgSeq: 10,
		},
	}

	err = d.AddOrUpdateConversations(uid, conversations)
	assert.NoError(t, err)

	conversations2, err := d.GetConversations(uid)
	assert.NoError(t, err)

	assert.Equal(t, len(conversations), len(conversations2))

	assert.Equal(t, conversations[0].Uid, conversations2[0].Uid)
	assert.Equal(t, conversations[0].ChannelId, conversations2[0].ChannelId)
	assert.Equal(t, conversations[0].ChannelType, conversations2[0].ChannelType)
	assert.Equal(t, conversations[0].UnreadCount, conversations2[0].UnreadCount)
	assert.Equal(t, conversations[0].ReadedToMsgSeq, conversations2[0].ReadedToMsgSeq)

	assert.Equal(t, conversations[1].Uid, conversations2[1].Uid)
	assert.Equal(t, conversations[1].ChannelId, conversations2[1].ChannelId)
	assert.Equal(t, conversations[1].ChannelType, conversations2[1].ChannelType)
	assert.Equal(t, conversations[1].UnreadCount, conversations2[1].UnreadCount)
	assert.Equal(t, conversations[1].ReadedToMsgSeq, conversations2[1].ReadedToMsgSeq)

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
			Uid:            uid,
			ChannelId:      "1234",
			ChannelType:    1,
			UnreadCount:    20,
			ReadedToMsgSeq: 2,
		},
		{
			Uid:            uid,
			ChannelId:      "4567",
			ChannelType:    1,
			UnreadCount:    30,
			ReadedToMsgSeq: 10,
		},
	}

	err = d.AddOrUpdateConversations(uid, conversations)
	assert.NoError(t, err)

	err = d.DeleteConversation(uid, "ch123", 1)
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
// 			ReadedToMsgSeq: 2,
// 		},
// 		{
// 			Uid:            uid,
// 			ChannelId:      "5678",
// 			ChannelType:    1,
// 			UnreadCount:    30,
// 			ReadedToMsgSeq: 10,
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
