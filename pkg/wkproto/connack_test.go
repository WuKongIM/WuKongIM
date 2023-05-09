package wkproto

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestConnackEncodeAndDecode(t *testing.T) {
	packet := &ConnackPacket{
		TimeDiff:   12345,
		ReasonCode: ReasonSuccess,
		ServerKey:  "ServerKey",
		Salt:       "Salt",
	}
	codec := New()
	// 编码
	packetBytes, err := codec.EncodeFrame(packet, 4)
	assert.NoError(t, err)
	// 解码
	resultPacket, _, err := codec.DecodeFrame(packetBytes, 4)
	assert.NoError(t, err)
	resultConnackPacket, ok := resultPacket.(*ConnackPacket)
	assert.Equal(t, true, ok)

	// 正确与否比较
	assert.Equal(t, packet.TimeDiff, resultConnackPacket.TimeDiff)
	assert.Equal(t, packet.ReasonCode, resultConnackPacket.ReasonCode)
	assert.Equal(t, packet.ServerKey, resultConnackPacket.ServerKey)
	assert.Equal(t, packet.Salt, resultConnackPacket.Salt)
}
