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
			SessionId:      1,
			ReadedToMsgSeq: 1,
		},
		{
			Uid:            uid,
			UnreadCount:    21,
			SessionId:      2,
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
			SessionId:      1,
			UnreadCount:    20,
			ReadedToMsgSeq: 2,
		},
		{
			Uid:            uid,
			SessionId:      2,
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
	assert.Equal(t, conversations[0].SessionId, conversations2[0].SessionId)
	assert.Equal(t, conversations[0].UnreadCount, conversations2[0].UnreadCount)
	assert.Equal(t, conversations[0].ReadedToMsgSeq, conversations2[0].ReadedToMsgSeq)

	assert.Equal(t, conversations[1].Uid, conversations2[1].Uid)
	assert.Equal(t, conversations[1].SessionId, conversations2[1].SessionId)
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
			SessionId:      1,
			UnreadCount:    20,
			ReadedToMsgSeq: 2,
		},
		{
			Uid:            uid,
			SessionId:      2,
			UnreadCount:    30,
			ReadedToMsgSeq: 10,
		},
	}

	err = d.AddOrUpdateConversations(uid, conversations)
	assert.NoError(t, err)

	err = d.DeleteConversation(uid, 1)
	assert.NoError(t, err)

	conversations2, err := d.GetConversations(uid)
	assert.NoError(t, err)

	assert.Len(t, conversations2, 1)
	conversations[1].Id = conversations2[0].Id
	assert.Equal(t, conversations[1], conversations2[0])
}

func TestGetConversationBySessionIds(t *testing.T) {
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
			SessionId:      1,
			UnreadCount:    20,
			ReadedToMsgSeq: 2,
		},
		{
			Uid:            uid,
			SessionId:      2,
			UnreadCount:    30,
			ReadedToMsgSeq: 10,
		},
	}

	err = d.AddOrUpdateConversations(uid, conversations)
	assert.NoError(t, err)

	conversations2, err := d.GetConversationBySessionIds(uid, []uint64{1, 2})
	assert.NoError(t, err)

	assert.Len(t, conversations2, 2)
	conversations[0].Id = conversations2[0].Id
	conversations[1].Id = conversations2[1].Id
	assert.Equal(t, conversations[0], conversations2[0])
	assert.Equal(t, conversations[1], conversations2[1])
}
