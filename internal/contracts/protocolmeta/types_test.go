package protocolmeta

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func TestProtocolValuesStayWireCompatible(t *testing.T) {
	if uint8(DeviceFlagApp) != uint8(frame.APP) ||
		uint8(DeviceFlagWeb) != uint8(frame.WEB) ||
		uint8(DeviceFlagPC) != uint8(frame.PC) ||
		uint8(DeviceFlagSystem) != uint8(frame.SYSTEM) {
		t.Fatal("device flag contract drifted from the protocol wire values")
	}
	if uint8(DeviceLevelSlave) != uint8(frame.DeviceLevelSlave) ||
		uint8(DeviceLevelMaster) != uint8(frame.DeviceLevelMaster) {
		t.Fatal("device level contract drifted from the protocol wire values")
	}
	if uint8(ChannelTypePerson) != frame.ChannelTypePerson ||
		uint8(ChannelTypeTemp) != frame.ChannelTypeTemp ||
		uint8(ChannelTypeSystemUIDRegistry) != uint8(frame.SYSTEM) {
		t.Fatal("channel type contract drifted from the protocol compatibility values")
	}
}
