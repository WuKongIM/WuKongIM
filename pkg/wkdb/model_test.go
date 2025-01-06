package wkdb

import (
	"testing"

	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"github.com/stretchr/testify/assert"
)

func TestMessageUnmarshal(t *testing.T) {
	msg := &Message{
		RecvPacket: wkproto.RecvPacket{
			ChannelID:   "channel",
			ChannelType: 2,
			MessageSeq:  1,
			Payload:     []byte("hello"),
			MessageID:   1234,
		},
		Term: 100,
	}

	data, err := msg.Marshal()
	assert.NoError(t, err)

	newMsg := &Message{}
	err = newMsg.Unmarshal(data)
	assert.NoError(t, err)

	assert.Equal(t, msg.Payload, newMsg.Payload)
	assert.Equal(t, msg.ChannelID, newMsg.ChannelID)
	assert.Equal(t, msg.ChannelType, newMsg.ChannelType)
	assert.Equal(t, msg.MessageSeq, newMsg.MessageSeq)
	assert.Equal(t, msg.MessageID, newMsg.MessageID)
	assert.Equal(t, msg.Term, newMsg.Term)
}
