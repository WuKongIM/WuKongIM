package codec

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/stretchr/testify/assert"
)

func TestSbuEncodeAndDecode(t *testing.T) {
	packet := &frame.SubPacket{
		Setting:     frame.Setting(1),
		ChannelID:   "123456",
		ChannelType: 1,
		Action:      frame.Subscribe,
	}

	codec := New()
	// 编码
	packetBytes, err := codec.EncodeFrame(packet, 1)
	assert.NoError(t, err)

	// 解码
	resultPacket, _, err := codec.DecodeFrame(packetBytes, 1)
	assert.NoError(t, err)
	resultSubPacket, ok := resultPacket.(*frame.SubPacket)
	assert.Equal(t, true, ok)

	// 比较
	assert.Equal(t, packet.Setting, resultSubPacket.Setting)
	assert.Equal(t, packet.ChannelID, resultSubPacket.ChannelID)
	assert.Equal(t, packet.ChannelType, resultSubPacket.ChannelType)
	assert.Equal(t, packet.Action, resultSubPacket.Action)

}
