package wkproto

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestConnectEncodeAndDecode(t *testing.T) {

	//clientTimestamp :=time.Now().UnixNano()/1000/1000
	packet := &ConnectPacket{
		Version:         1,
		DeviceFlag:      1,
		DeviceID:        "deviceID",
		ClientTimestamp: 1,
		UID:             "test",
	}

	codec := New()
	// 编码
	packetBytes, err := codec.EncodeFrame(packet, 1)
	assert.NoError(t, err)
	// panic(fmt.Sprintf("%v",packetBytes))
	// 解码
	resultPacket, _, err := codec.DecodeFrame(packetBytes, 1)
	assert.NoError(t, err)
	resultConnectPacket, ok := resultPacket.(*ConnectPacket)
	assert.Equal(t, true, ok)

	assert.Equal(t, packet.Version, resultConnectPacket.Version)
	assert.Equal(t, packet.DeviceFlag, resultConnectPacket.DeviceFlag)
	assert.Equal(t, packet.DeviceID, resultConnectPacket.DeviceID)
	assert.Equal(t, packet.ClientTimestamp, resultConnectPacket.ClientTimestamp)
	assert.Equal(t, packet.UID, resultConnectPacket.UID)
	assert.Equal(t, packet.Token, resultConnectPacket.Token)
}
