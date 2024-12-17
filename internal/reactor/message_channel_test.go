package reactor

import (
	"testing"

	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"github.com/stretchr/testify/assert"
)

func TestChannelMessage_EncodeDecode(t *testing.T) {
	original := &ChannelMessage{
		Conn: &Conn{
			Uid: "testUid",
		}, // Assuming Conn has a proper implementation
		SendPacket: &wkproto.SendPacket{
			ChannelID: "ch1",
			Payload:   []byte("testPayload"),
		}, // Assuming SendPacket has a proper implementation
		FakeChannelId: "testChannel",
		ChannelType:   1,
		Index:         12345,
		MsgType:       ChannelMsgSend,
		ToNode:        67890,
		MessageId:     111213,
		MessageSeq:    141516,
		ReasonCode:    wkproto.ReasonCode(0),
	}

	encoded, err := original.Encode()
	assert.NoError(t, err)

	decoded := &ChannelMessage{}
	err = decoded.Decode(encoded)
	assert.NoError(t, err)

	assert.Equal(t, original.Conn.Uid, decoded.Conn.Uid)
	assert.Equal(t, original.SendPacket.ChannelID, decoded.SendPacket.ChannelID)
	assert.Equal(t, original.SendPacket.Payload, decoded.SendPacket.Payload)
	assert.Equal(t, original.FakeChannelId, decoded.FakeChannelId)
	assert.Equal(t, original.ChannelType, decoded.ChannelType)
	assert.Equal(t, original.Index, decoded.Index)
	assert.Equal(t, original.MsgType, decoded.MsgType)
	assert.Equal(t, original.ToNode, decoded.ToNode)
	assert.Equal(t, original.MessageId, decoded.MessageId)
	assert.Equal(t, original.MessageSeq, decoded.MessageSeq)
	assert.Equal(t, original.ReasonCode, decoded.ReasonCode)
}
