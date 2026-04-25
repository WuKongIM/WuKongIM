package codec

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSendEncodeAndDecode(t *testing.T) {
	var setting frame.Setting
	setting.Set(frame.SettingNoEncrypt)
	packet := &frame.SendPacket{
		Framer: frame.Framer{
			RedDot: true,
		},
		Expire:      100,
		Setting:     setting,
		ClientSeq:   2,
		ChannelID:   "34341",
		ChannelType: 2,
		Payload:     []byte("dsdsdsd1"),
	}
	packet.RedDot = true

	codec := New()
	// 编码
	packetBytes, err := codec.EncodeFrame(packet, frame.LatestVersion)
	assert.NoError(t, err)

	// 解码
	resultPacket, _, err := codec.DecodeFrame(packetBytes, frame.LatestVersion)
	assert.NoError(t, err)
	resultSendPacket, ok := resultPacket.(*frame.SendPacket)
	assert.Equal(t, true, ok)

	// 比较
	assert.Equal(t, packet.ClientSeq, resultSendPacket.ClientSeq)
	assert.Equal(t, packet.ChannelID, resultSendPacket.ChannelID)
	assert.Equal(t, packet.ChannelType, resultSendPacket.ChannelType)
	assert.Equal(t, packet.Expire, resultSendPacket.Expire)
	assert.Equal(t, packet.RedDot, resultSendPacket.RedDot)
	assert.Equal(t, packet.Payload, resultSendPacket.Payload)
	assert.Equal(t, packet.Setting, resultSendPacket.Setting)
}

func TestSendEncodeAndDecodeV5WithStreamDoesNotMiscalculateRemainingLength(t *testing.T) {
	packet := &frame.SendPacket{
		Framer: frame.Framer{
			RedDot: true,
		},
		Setting:     frame.SettingStream,
		ClientSeq:   7,
		ClientMsgNo: "client-msg-no",
		StreamNo:    "stream-no",
		ChannelID:   "channel-1",
		ChannelType: 2,
		MsgKey:      "msg-key",
		Payload:     []byte("payload"),
	}

	codec := New()
	packetBytes, err := codec.EncodeFrame(packet, frame.LatestVersion)
	assert.NoError(t, err)

	resultPacket, consumed, err := codec.DecodeFrame(packetBytes, frame.LatestVersion)
	assert.NoError(t, err)
	assert.Equal(t, len(packetBytes), consumed)

	resultSendPacket, ok := resultPacket.(*frame.SendPacket)
	require.True(t, ok, "expected *frame.SendPacket, got %T", resultPacket)
	require.NotNil(t, resultSendPacket)
	assert.Equal(t, packet.ClientSeq, resultSendPacket.ClientSeq)
	assert.Equal(t, packet.ClientMsgNo, resultSendPacket.ClientMsgNo)
	assert.Empty(t, resultSendPacket.StreamNo)
	assert.Equal(t, packet.ChannelID, resultSendPacket.ChannelID)
	assert.Equal(t, packet.ChannelType, resultSendPacket.ChannelType)
	assert.Equal(t, packet.MsgKey, resultSendPacket.MsgKey)
	assert.Equal(t, packet.Payload, resultSendPacket.Payload)
}
