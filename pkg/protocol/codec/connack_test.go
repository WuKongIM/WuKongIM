package codec

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/stretchr/testify/assert"
)

func TestConnackEncodeAndDecode(t *testing.T) {
	packet := &frame.ConnackPacket{
		TimeDiff:      12345,
		ReasonCode:    frame.ReasonSuccess,
		ServerKey:     "ServerKey",
		Salt:          "Salt",
		ServerVersion: 100,
	}
	packet.HasServerVersion = true
	codec := New()
	// 编码
	packetBytes, err := codec.EncodeFrame(packet, 4)
	assert.NoError(t, err)
	// 解码
	resultPacket, _, err := codec.DecodeFrame(packetBytes, 4)
	assert.NoError(t, err)
	resultConnackPacket, ok := resultPacket.(*frame.ConnackPacket)
	assert.Equal(t, true, ok)

	// 正确与否比较
	assert.Equal(t, packet.TimeDiff, resultConnackPacket.TimeDiff)
	assert.Equal(t, packet.ReasonCode, resultConnackPacket.ReasonCode)
	assert.Equal(t, packet.ServerKey, resultConnackPacket.ServerKey)
	assert.Equal(t, packet.Salt, resultConnackPacket.Salt)
	assert.Equal(t, packet.ServerVersion, resultConnackPacket.ServerVersion)
}
