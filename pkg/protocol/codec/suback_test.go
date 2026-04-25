package codec

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/stretchr/testify/assert"
)

func TestSuback(t *testing.T) {
	packet := &frame.SubackPacket{
		ChannelID:   "123456",
		ChannelType: 1,
		Action:      frame.Subscribe,
		ReasonCode:  1,
	}

	codec := New()
	// 编码
	packetBytes, err := codec.EncodeFrame(packet, 1)
	assert.NoError(t, err)

	// 解码
	resultPacket, _, err := codec.DecodeFrame(packetBytes, 1)
	assert.NoError(t, err)
	resultSubackPacket, ok := resultPacket.(*frame.SubackPacket)
	assert.Equal(t, true, ok)

	// 比较
	assert.Equal(t, packet.ChannelID, resultSubackPacket.ChannelID)
	assert.Equal(t, packet.ChannelType, resultSubackPacket.ChannelType)
	assert.Equal(t, packet.Action, resultSubackPacket.Action)
	assert.Equal(t, packet.ReasonCode, resultSubackPacket.ReasonCode)

}
