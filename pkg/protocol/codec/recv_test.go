package codec

import (
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/stretchr/testify/assert"
)

func TestRecvEncodeAndDecode(t *testing.T) {

	packet := &frame.RecvPacket{
		MessageID:   1223,
		Expire:      10,
		MessageSeq:  9238934,
		Timestamp:   int32(time.Now().Unix()),
		ChannelID:   "3434",
		ChannelType: 2,
		FromUID:     "123",
		Payload:     []byte("中文测试"),
	}
	packet.Framer = frame.Framer{
		NoPersist: true,
		SyncOnce:  true,
	}

	codec := New()
	// 编码
	packetBytes, err := codec.EncodeFrame(packet, 3)
	assert.NoError(t, err)

	// 解码
	resultPacket, _, err := codec.DecodeFrame(packetBytes, 3)
	assert.NoError(t, err)
	resultRecvPacket, ok := resultPacket.(*frame.RecvPacket)
	assert.Equal(t, true, ok)

	// 比较
	assert.Equal(t, packet.MessageID, resultRecvPacket.MessageID)
	assert.Equal(t, packet.MessageSeq, resultRecvPacket.MessageSeq)
	assert.Equal(t, packet.Timestamp, resultRecvPacket.Timestamp)
	assert.Equal(t, packet.ChannelID, resultRecvPacket.ChannelID)
	assert.Equal(t, packet.ChannelType, resultRecvPacket.ChannelType)
	assert.Equal(t, packet.Payload, resultRecvPacket.Payload)
	assert.Equal(t, packet.Expire, resultRecvPacket.Expire)

	assert.Equal(t, packet.Framer.GetNoPersist(), resultRecvPacket.Framer.GetNoPersist())
	assert.Equal(t, packet.Framer.GetRedDot(), resultRecvPacket.Framer.GetRedDot())
	assert.Equal(t, packet.Framer.GetsyncOnce(), resultRecvPacket.Framer.GetsyncOnce())
}

func TestRecvEncodeAndDecodeSupportsUint64MessageSeqOnLatestVersion(t *testing.T) {
	packet := &frame.RecvPacket{
		MessageID:   1223,
		Expire:      10,
		MessageSeq:  uint64(^uint32(0)) + 7,
		Timestamp:   int32(time.Now().Unix()),
		ChannelID:   "3434",
		ChannelType: 2,
		FromUID:     "123",
		Payload:     []byte("u64"),
	}

	codec := New()
	packetBytes, err := codec.EncodeFrame(packet, frame.LatestVersion)
	assert.NoError(t, err)

	resultPacket, _, err := codec.DecodeFrame(packetBytes, frame.LatestVersion)
	assert.NoError(t, err)
	resultRecvPacket, ok := resultPacket.(*frame.RecvPacket)
	assert.Equal(t, true, ok)
	assert.Equal(t, packet.MessageSeq, resultRecvPacket.MessageSeq)
}
