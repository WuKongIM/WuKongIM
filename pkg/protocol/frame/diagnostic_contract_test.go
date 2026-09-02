package frame

import (
	"strings"
	"testing"

	"github.com/valyala/bytebufferpool"
)

func TestWireEnumsHaveStableDiagnosticNames(t *testing.T) {
	frameTypes := []struct {
		value FrameType
		name  string
	}{
		{CONNECT, "CONNECT"},
		{CONNACK, "CONNACK"},
		{SEND, "SEND"},
		{SENDACK, "SENDACK"},
		{RECV, "RECV"},
		{RECVACK, "RECVACK"},
		{PING, "PING"},
		{PONG, "PONG"},
		{DISCONNECT, "DISCONNECT"},
		{SUB, "SUB"},
		{SUBACK, "SUBACK"},
		{EVENT, "EVENT"},
	}
	for _, tt := range frameTypes {
		if got := tt.value.String(); got != tt.name {
			t.Errorf("FrameType(%d).String() = %q, want %q", tt.value, got, tt.name)
		}
	}
	if got := FrameType(255).String(); got != "UNKNOWN[255]" {
		t.Fatalf("unknown FrameType.String() = %q, want %q", got, "UNKNOWN[255]")
	}

	reasons := []struct {
		value ReasonCode
		name  string
	}{
		{ReasonUnknown, "ReasonUnknown"},
		{ReasonSuccess, "ReasonSuccess"},
		{ReasonAuthFail, "ReasonAuthFail"},
		{ReasonSubscriberNotExist, "ReasonSubscriberNotExist"},
		{ReasonInBlacklist, "ReasonInBlacklist"},
		{ReasonChannelNotExist, "ReasonChannelNotExist"},
		{ReasonUserNotOnNode, "ReasonUserNotOnNode"},
		{ReasonSenderOffline, "ReasonSenderOffline"},
		{ReasonMsgKeyError, "ReasonMsgKeyError"},
		{ReasonPayloadDecodeError, "ReasonPayloadDecodeError"},
		{ReasonForwardSendPacketError, "ReasonForwardSendPacketError"},
		{ReasonNotAllowSend, "ReasonNotAllowSend"},
		{ReasonConnectKick, "ReasonConnectKick"},
		{ReasonNotInWhitelist, "ReasonNotInWhitelist"},
		{ReasonQueryTokenError, "ReasonQueryTokenError"},
		{ReasonSystemError, "ReasonSystemError"},
		{ReasonChannelIDError, "ReasonChannelIDError"},
		{ReasonNodeMatchError, "ReasonNodeMatchError"},
		{ReasonNodeNotMatch, "ReasonNodeNotMatch"},
		{ReasonBan, "ReasonBan"},
		{ReasonNotSupportHeader, "ReasonNotSupportHeader"},
		{ReasonClientKeyIsEmpty, "ReasonClientKeyIsEmpty"},
		{ReasonRateLimit, "ReasonRateLimit"},
		{ReasonNotSupportChannelType, "ReasonNotSupportChannelType"},
		{ReasonDisband, "ReasonDisband"},
		{ReasonSendBan, "ReasonSendBan"},
		{ReasonChannelDeleting, "ReasonChannelDeleting"},
		{ReasonProtocolUpgradeRequired, "ReasonProtocolUpgradeRequired"},
		{ReasonIdempotencyConflict, "ReasonIdempotencyConflict"},
		{ReasonMessageSeqExhausted, "ReasonMessageSeqExhausted"},
	}
	for _, tt := range reasons {
		if got := tt.value.String(); got != tt.name {
			t.Errorf("ReasonCode(%d).String() = %q, want %q", tt.value, got, tt.name)
		}
		if got := tt.value.Byte(); got != byte(tt.value) {
			t.Errorf("ReasonCode(%d).Byte() = %d, want %d", tt.value, got, tt.value)
		}
	}
	if got := ReasonCode(255).String(); got != "UNKNOWN[255]" {
		t.Fatalf("unknown ReasonCode.String() = %q, want %q", got, "UNKNOWN[255]")
	}

	if got := DeviceLevelSlave.String(); got != "Slave" {
		t.Errorf("DeviceLevelSlave.String() = %q, want Slave", got)
	}
	if got := DeviceLevelMaster.String(); got != "Master" {
		t.Errorf("DeviceLevelMaster.String() = %q, want Master", got)
	}
	if got := DeviceLevel(9).String(); got != "Unknown[9]" {
		t.Errorf("unknown DeviceLevel.String() = %q, want Unknown[9]", got)
	}
	deviceFlags := []struct {
		value DeviceFlag
		name  string
	}{{APP, "APP"}, {WEB, "WEB"}, {PC, "2"}, {SYSTEM, "SYSTEM"}}
	for _, tt := range deviceFlags {
		if got := tt.value.String(); got != tt.name {
			t.Errorf("DeviceFlag(%d).String() = %q, want %q", tt.value, got, tt.name)
		}
		if got := tt.value.ToUint8(); got != uint8(tt.value) {
			t.Errorf("DeviceFlag(%d).ToUint8() = %d", tt.value, got)
		}
	}
}

func TestPacketTypesAndVerificationMaterialMatchWireContract(t *testing.T) {
	packets := []struct {
		packet Frame
		want   FrameType
	}{
		{ConnectPacket{}, CONNECT},
		{ConnackPacket{}, CONNACK},
		{&SendPacket{}, SEND},
		{&SendackPacket{}, SENDACK},
		{&RecvPacket{}, RECV},
		{&RecvackPacket{}, RECVACK},
		{&PingPacket{}, PING},
		{&PongPacket{}, PONG},
		{DisconnectPacket{}, DISCONNECT},
		{&SubPacket{}, SUB},
		{&SubackPacket{}, SUBACK},
		{&EventPacket{}, EVENT},
	}
	for _, tt := range packets {
		if got := tt.packet.GetFrameType(); got != tt.want {
			t.Errorf("%T.GetFrameType() = %v, want %v", tt.packet, got, tt.want)
		}
	}

	send := &SendPacket{
		ClientSeq:   7,
		ClientMsgNo: "m1",
		ChannelID:   "channel",
		ChannelType: ChannelTypePerson,
		Payload:     []byte("body"),
	}
	if got, want := send.VerityString(), "7m1channel1body"; got != want {
		t.Fatalf("SendPacket.VerityString() = %q, want %q", got, want)
	}

	recv := &RecvPacket{
		MessageID:   -7,
		MessageSeq:  8,
		ClientMsgNo: "m1",
		Timestamp:   9,
		FromUID:     "sender",
		ChannelID:   "channel",
		ChannelType: ChannelTypeGroup,
		Payload:     []byte("body"),
	}
	const wantVerification = "-78m19senderchannel2body"
	if got := recv.VerityString(); got != wantVerification {
		t.Fatalf("RecvPacket.VerityString() = %q, want %q", got, wantVerification)
	}
	buf := bytebufferpool.Get()
	defer bytebufferpool.Put(buf)
	buf.Reset()
	recv.VerityBytes(buf)
	if got := string(buf.B); got != wantVerification {
		t.Fatalf("RecvPacket.VerityBytes() = %q, want %q", got, wantVerification)
	}

	setting := SettingReceiptEnabled | SettingTopic
	if got := setting.Uint8(); got != uint8(setting) {
		t.Fatalf("Setting.Uint8() = %d, want %d", got, setting)
	}
	if got := UnSubscribe.Uint8(); got != 1 {
		t.Fatalf("UnSubscribe.Uint8() = %d, want 1", got)
	}

	framer := Framer{FrameType: SEND, RemainingLength: 12, NoPersist: true, RedDot: true, SyncOnce: true, DUP: true}
	for _, fragment := range []string{"packetType: SEND", "remainingLength:12", "NoPersist:true", "redDot:true", "syncOnce:true", "DUP:true"} {
		if got := framer.String(); !strings.Contains(got, fragment) {
			t.Errorf("Framer.String() = %q, want fragment %q", got, fragment)
		}
	}
}
