package wkdb_test

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/stretchr/testify/assert"
)

func TestAddOrUpdateConversations(t *testing.T) {
	d := wkdb.NewWukongDB(wkdb.NewOptions(wkdb.WithDir(t.TempDir())))
	err := d.Open()
	assert.NoError(t, err)

	defer func() {
		err := d.Close()
		assert.NoError(t, err)
	}()

	uid := "test1"
	conversations := []wkdb.Conversation{
		{
			UID:             uid,
			ChannelId:       "channel1",
			ChannelType:     1,
			UnreadCount:     10,
			Timestamp:       100,
			LastMsgSeq:      1,
			LastClientMsgNo: "clientMsgNo1",
			LastMsgID:       123,
			Version:         1,
		},
		{
			UID:             uid,
			ChannelId:       "channel2",
			ChannelType:     2,
			UnreadCount:     20,
			Timestamp:       200,
			LastMsgSeq:      2,
			LastClientMsgNo: "clientMsgNo2",
			LastMsgID:       435,
			Version:         2,
		},
	}

	err = d.AddOrUpdateConversations(uid, conversations)
	assert.NoError(t, err)

}

func TestGetConversations(t *testing.T) {
	d := wkdb.NewWukongDB(wkdb.NewOptions(wkdb.WithDir(t.TempDir())))
	err := d.Open()
	assert.NoError(t, err)

	defer func() {
		err := d.Close()
		assert.NoError(t, err)
	}()

	uid := "test1"
	conversations := []wkdb.Conversation{
		{
			UID:             uid,
			ChannelId:       "channel2",
			ChannelType:     2,
			UnreadCount:     20,
			Timestamp:       200,
			LastMsgSeq:      2,
			LastClientMsgNo: "clientMsgNo2",
			LastMsgID:       435,
			Version:         2,
		},
		{
			UID:             uid,
			ChannelId:       "channel1",
			ChannelType:     1,
			UnreadCount:     10,
			Timestamp:       100,
			LastMsgSeq:      1,
			LastClientMsgNo: "clientMsgNo1",
			LastMsgID:       123,
			Version:         1,
		},
	}

	err = d.AddOrUpdateConversations(uid, conversations)
	assert.NoError(t, err)

	conversations2, err := d.GetConversations(uid)
	assert.NoError(t, err)

	assert.ElementsMatch(t, conversations, conversations2)
}

func TestDeleteConversation(t *testing.T) {
	d := wkdb.NewWukongDB(wkdb.NewOptions(wkdb.WithDir(t.TempDir())))
	err := d.Open()
	assert.NoError(t, err)

	defer func() {
		err := d.Close()
		assert.NoError(t, err)
	}()

	uid := "test1"
	conversations := []wkdb.Conversation{
		{
			UID:             uid,
			ChannelId:       "channel2",
			ChannelType:     2,
			UnreadCount:     20,
			Timestamp:       200,
			LastMsgSeq:      2,
			LastClientMsgNo: "clientMsgNo2",
			LastMsgID:       435,
			Version:         2,
		},
		{
			UID:             uid,
			ChannelId:       "channel1",
			ChannelType:     1,
			UnreadCount:     10,
			Timestamp:       100,
			LastMsgSeq:      1,
			LastClientMsgNo: "clientMsgNo1",
			LastMsgID:       123,
			Version:         1,
		},
	}

	err = d.AddOrUpdateConversations(uid, conversations)
	assert.NoError(t, err)

	err = d.DeleteConversation(uid, "channel1", 1)
	assert.NoError(t, err)

	conversations2, err := d.GetConversations(uid)
	assert.NoError(t, err)

	assert.Len(t, conversations2, 1)
	assert.Equal(t, conversations[0], conversations2[0])
}
