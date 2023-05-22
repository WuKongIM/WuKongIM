package wkproto

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestRecvEncodeAndDecode(t *testing.T) {

	packet := &RecvPacket{
		MessageID:   1223,
		MessageSeq:  9238934,
		Timestamp:   int32(time.Now().Unix()),
		ChannelID:   "3434",
		ChannelType: 2,
		FromUID:     "123",
		Payload:     []byte("中文测试"),
	}
	packet.Framer = Framer{
		NoPersist: true,
		SyncOnce:  true,
	}

	codec := New()
	// 编码
	packetBytes, err := codec.EncodeFrame(packet, 1)
	assert.NoError(t, err)

	// 解码
	resultPacket, _, err := codec.DecodeFrame(packetBytes, 1)
	assert.NoError(t, err)
	resultRecvPacket, ok := resultPacket.(*RecvPacket)
	fmt.Println("resultRecvPacket--->", resultRecvPacket)
	assert.Equal(t, true, ok)

	// 比较
	assert.Equal(t, packet.MessageID, resultRecvPacket.MessageID)
	assert.Equal(t, packet.MessageSeq, resultRecvPacket.MessageSeq)
	assert.Equal(t, packet.Timestamp, resultRecvPacket.Timestamp)
	assert.Equal(t, packet.ChannelID, resultRecvPacket.ChannelID)
	assert.Equal(t, packet.ChannelType, resultRecvPacket.ChannelType)
	assert.Equal(t, packet.Payload, resultRecvPacket.Payload)

	assert.Equal(t, packet.Framer.GetNoPersist(), resultRecvPacket.Framer.GetNoPersist())
	assert.Equal(t, packet.Framer.GetRedDot(), resultRecvPacket.Framer.GetRedDot())
	assert.Equal(t, packet.Framer.GetsyncOnce(), resultRecvPacket.Framer.GetsyncOnce())
}
