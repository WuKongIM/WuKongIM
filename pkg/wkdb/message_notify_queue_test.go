package wkdb_test

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"github.com/stretchr/testify/assert"
)

func TestAppendMessageOfNotifyQueue(t *testing.T) {
	d := newTestDB(t)
	err := d.Open()
	assert.NoError(t, err)

	defer func() {
		err := d.Close()
		assert.NoError(t, err)
	}()

	messages := []wkdb.Message{
		{
			RecvPacket: wkproto.RecvPacket{
				MessageID:   1,
				ChannelID:   "channel1",
				ChannelType: 1,
				FromUID:     "from1",
				ClientMsgNo: "clientMsgNo1",
				Timestamp:   100,
				Payload:     []byte("content1"),
			},
		},
		{
			RecvPacket: wkproto.RecvPacket{
				MessageID:   2,
				ChannelID:   "channel2",
				ChannelType: 2,
				FromUID:     "from2",
				ClientMsgNo: "clientMsgNo2",
				Timestamp:   200,
				Payload:     []byte("content2"),
			},
		},
	}

	err = d.AppendMessageOfNotifyQueue(messages)
	assert.NoError(t, err)

}

func TestGetMessagesOfNotifyQueue(t *testing.T) {
	d := newTestDB(t)
	err := d.Open()
	assert.NoError(t, err)

	defer func() {
		err := d.Close()
		assert.NoError(t, err)
	}()

	messages := []wkdb.Message{
		{
			RecvPacket: wkproto.RecvPacket{
				MessageID:   1,
				ChannelID:   "channel1",
				ChannelType: 1,
				FromUID:     "from1",
				ClientMsgNo: "clientMsgNo1",
				Timestamp:   100,
				Payload:     []byte("content1"),
			},
		},
		{
			RecvPacket: wkproto.RecvPacket{
				MessageID:   2,
				ChannelID:   "channel2",
				ChannelType: 2,
				FromUID:     "from2",
				ClientMsgNo: "clientMsgNo2",
				Timestamp:   200,
				Payload:     []byte("content2"),
			},
		},
	}

	err = d.AppendMessageOfNotifyQueue(messages)
	assert.NoError(t, err)

	msgs, err := d.GetMessagesOfNotifyQueue(2)
	assert.NoError(t, err)
	assert.Len(t, msgs, 2)

	assert.Equal(t, messages[0].MessageID, msgs[0].MessageID)
	assert.Equal(t, messages[1].MessageID, msgs[1].MessageID)
	assert.Equal(t, messages[0].ChannelID, msgs[0].ChannelID)
	assert.Equal(t, messages[1].ChannelID, msgs[1].ChannelID)
	assert.Equal(t, messages[0].ChannelType, msgs[0].ChannelType)
	assert.Equal(t, messages[1].ChannelType, msgs[1].ChannelType)
	assert.Equal(t, messages[0].FromUID, msgs[0].FromUID)
	assert.Equal(t, messages[1].FromUID, msgs[1].FromUID)
	assert.Equal(t, messages[0].ClientMsgNo, msgs[0].ClientMsgNo)
	assert.Equal(t, messages[1].ClientMsgNo, msgs[1].ClientMsgNo)
	assert.Equal(t, messages[0].Timestamp, msgs[0].Timestamp)
	assert.Equal(t, messages[1].Timestamp, msgs[1].Timestamp)
	assert.Equal(t, messages[0].Payload, msgs[0].Payload)
	assert.Equal(t, messages[1].Payload, msgs[1].Payload)

}

func TestRemoveMessagesOfNotifyQueue(t *testing.T) {
	d := newTestDB(t)
	err := d.Open()
	assert.NoError(t, err)

	defer func() {
		err := d.Close()
		assert.NoError(t, err)
	}()

	messages := []wkdb.Message{
		{
			RecvPacket: wkproto.RecvPacket{
				MessageID:   1,
				ChannelID:   "channel1",
				ChannelType: 1,
				FromUID:     "from1",
				ClientMsgNo: "clientMsgNo1",
				Timestamp:   100,
				Payload:     []byte("content1"),
			},
		},
		{
			RecvPacket: wkproto.RecvPacket{
				MessageID:   2,
				ChannelID:   "channel2",
				ChannelType: 2,
				FromUID:     "from2",
				ClientMsgNo: "clientMsgNo2",
				Timestamp:   200,
				Payload:     []byte("content2"),
			},
		},
	}

	err = d.AppendMessageOfNotifyQueue(messages)
	assert.NoError(t, err)

	err = d.RemoveMessagesOfNotifyQueue([]int64{2})
	assert.NoError(t, err)

	msgs, err := d.GetMessagesOfNotifyQueue(2)
	assert.NoError(t, err)
	assert.Len(t, msgs, 1)
	assert.Equal(t, messages[0].MessageID, msgs[0].MessageID)
	assert.Equal(t, messages[0].ChannelID, msgs[0].ChannelID)
	assert.Equal(t, messages[0].ChannelType, msgs[0].ChannelType)
	assert.Equal(t, messages[0].FromUID, msgs[0].FromUID)
	assert.Equal(t, messages[0].ClientMsgNo, msgs[0].ClientMsgNo)
	assert.Equal(t, messages[0].Timestamp, msgs[0].Timestamp)
	assert.Equal(t, messages[0].Payload, msgs[0].Payload)

}
