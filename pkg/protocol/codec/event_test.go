package codec

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/stretchr/testify/assert"
)

func TestEventEncodeAndDecode(t *testing.T) {
	packet := &frame.EventPacket{
		Id:        "123456",
		Type:      "test",
		Timestamp: 1234567890,
		Data:      []byte("test"),
	}
	codec := New()
	// 编码
	packetBytes, err := codec.EncodeFrame(packet, 1)
	assert.NoError(t, err)

	// 解码
	resultPacket, _, err := codec.DecodeFrame(packetBytes, 1)
	assert.NoError(t, err)
	resultEventPacket, ok := resultPacket.(*frame.EventPacket)
	assert.Equal(t, true, ok)

	// 比较
	assert.Equal(t, packet.Id, resultEventPacket.Id)
	assert.Equal(t, packet.Type, resultEventPacket.Type)
	assert.Equal(t, packet.Timestamp, resultEventPacket.Timestamp)
	assert.Equal(t, packet.Data, resultEventPacket.Data)
}
