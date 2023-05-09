package wkproto

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPongEncodeAndDecode(t *testing.T) {
	packet := &PongPacket{}

	codec := New()
	// 编码
	packetBytes, err := codec.EncodeFrame(packet, 1)
	assert.NoError(t, err)

	// 解码
	resultPacket, _, err := codec.DecodeFrame(packetBytes, 1)
	assert.NoError(t, err)
	_, ok := resultPacket.(*PongPacket)
	assert.Equal(t, true, ok)
}
