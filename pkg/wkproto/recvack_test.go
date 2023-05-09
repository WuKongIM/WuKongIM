package wkproto

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRecvackEncodeAndDecode(t *testing.T) {

	packet := &RecvackPacket{
		Framer: Framer{
			RedDot: true,
		},
		MessageID:  1234,
		MessageSeq: 2334,
	}

	codec := New()
	// 编码
	packetBytes, err := codec.EncodeFrame(packet, 1)
	assert.NoError(t, err)

	// 解码
	resultPacket, _, err := codec.DecodeFrame(packetBytes, 1)
	assert.NoError(t, err)
	resultRecvackPacket, ok := resultPacket.(*RecvackPacket)
	assert.Equal(t, true, ok)

	// 比较
	assert.Equal(t, packet.MessageID, resultRecvackPacket.MessageID)
	assert.Equal(t, packet.MessageSeq, resultRecvackPacket.MessageSeq)
	assert.Equal(t, packet.RedDot, resultRecvackPacket.RedDot)
}
