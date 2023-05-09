package wkproto

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDisConnectEncodeAndDecode(t *testing.T) {

	//clientTimestamp :=time.Now().UnixNano()/1000/1000
	packet := &DisconnectPacket{
		ReasonCode: 1,
		Reason:     "其他地方登录了！",
	}

	codec := New()
	// 编码
	packetBytes, err := codec.EncodeFrame(packet, 1)
	assert.NoError(t, err)

	// 解码
	resultPacket, _, err := codec.DecodeFrame(packetBytes, 1)
	assert.NoError(t, err)
	resultConnectPacket, ok := resultPacket.(*DisconnectPacket)
	assert.Equal(t, true, ok)

	assert.Equal(t, packet.ReasonCode, resultConnectPacket.ReasonCode)
	assert.Equal(t, packet.Reason, resultConnectPacket.Reason)
}
