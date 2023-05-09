package wkproto

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSendEncodeAndDecode(t *testing.T) {
	var setting Setting
	setting.Set(SettingNoEncrypt)
	packet := &SendPacket{
		Framer: Framer{
			RedDot: true,
		},
		Setting:     setting,
		ClientSeq:   2,
		ChannelID:   "34341",
		ChannelType: 2,
		Payload:     []byte("dsdsdsd"),
	}
	packet.RedDot = true

	codec := New()
	// 编码
	packetBytes, err := codec.EncodeFrame(packet, LatestVersion)
	assert.NoError(t, err)

	// 解码
	resultPacket, _, err := codec.DecodeFrame(packetBytes, LatestVersion)
	assert.NoError(t, err)
	resultSendPacket, ok := resultPacket.(*SendPacket)
	assert.Equal(t, true, ok)

	// 比较
	assert.Equal(t, packet.ClientSeq, resultSendPacket.ClientSeq)
	assert.Equal(t, packet.ChannelID, resultSendPacket.ChannelID)
	assert.Equal(t, packet.ChannelType, resultSendPacket.ChannelType)
	assert.Equal(t, packet.RedDot, resultSendPacket.RedDot)
	assert.Equal(t, packet.Payload, resultSendPacket.Payload)
	assert.Equal(t, packet.Setting, resultSendPacket.Setting)
}
