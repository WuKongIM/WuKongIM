package codec

import (
	"bytes"
	"strings"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func TestDecodeFrameTreatsEmptyAndPartialInputAsIncomplete(t *testing.T) {
	proto := New()
	for _, input := range [][]byte{nil, {}, {byte(frame.SEND << 4)}, {byte(frame.SEND << 4), 0x80}} {
		got, consumed, err := proto.DecodeFrame(input, frame.LatestVersion)
		if err != nil || got != nil || consumed != 0 {
			t.Fatalf("DecodeFrame(%v) = (%T, %d, %v), want incomplete frame", input, got, consumed, err)
		}
	}
}

func TestReaderAndWriterPathsRoundTripWithoutNetwork(t *testing.T) {
	proto := New()
	packet := &frame.SendPacket{
		Framer:      frame.Framer{NoPersist: true, RedDot: true, SyncOnce: true, DUP: true},
		Setting:     frame.SettingTopic,
		ClientSeq:   42,
		ClientMsgNo: "m1",
		ChannelID:   "room",
		ChannelType: frame.ChannelTypeGroup,
		Expire:      60,
		MsgKey:      "key",
		Topic:       "orders",
		Payload:     []byte("payload"),
	}

	var wire bytes.Buffer
	if err := proto.WriteFrame(&wire, packet, frame.LatestVersion); err != nil {
		t.Fatalf("WriteFrame() error = %v", err)
	}
	decoded, err := proto.DecodePacketWithConn(bytes.NewReader(wire.Bytes()), frame.LatestVersion)
	if err != nil {
		t.Fatalf("DecodePacketWithConn() error = %v", err)
	}
	got, ok := decoded.(*frame.SendPacket)
	if !ok {
		t.Fatalf("decoded type = %T, want *frame.SendPacket", decoded)
	}
	if got.ClientSeq != packet.ClientSeq || got.ClientMsgNo != packet.ClientMsgNo || got.ChannelID != packet.ChannelID || got.Topic != packet.Topic || !bytes.Equal(got.Payload, packet.Payload) {
		t.Fatalf("decoded send packet = %+v, want key fields from %+v", got, packet)
	}
	if !got.NoPersist || !got.RedDot || !got.SyncOnce || !got.DUP {
		t.Fatalf("decoded header flags = %+v, want all message flags", got.Framer)
	}

	for _, tt := range []struct {
		packet frame.Frame
		want   frame.FrameType
	}{{&frame.PingPacket{}, frame.PING}, {&frame.PongPacket{}, frame.PONG}} {
		wire, err := proto.EncodeFrame(tt.packet, frame.LatestVersion)
		if err != nil {
			t.Fatalf("EncodeFrame(%v) error = %v", tt.want, err)
		}
		decoded, err := proto.DecodePacketWithConn(bytes.NewReader(wire), frame.LatestVersion)
		if err != nil || decoded.GetFrameType() != tt.want {
			t.Fatalf("reader round-trip %v = (%T, %v)", tt.want, decoded, err)
		}
	}
}

func TestProtocolRejectsUnsupportedAndOversizedFrames(t *testing.T) {
	proto := New()
	if data, err := proto.EncodeFrame(frame.Framer{FrameType: frame.UNKNOWN}, frame.LatestVersion); err == nil || len(data) != 0 {
		t.Fatalf("EncodeFrame(UNKNOWN) = (%v, %v), want explicit rejection", data, err)
	}

	oversizedHeader := append([]byte{byte(frame.SEND << 4)}, encodeVariable(MaxRemaingLength+1)...)
	if _, err := proto.DecodePacketWithConn(bytes.NewReader(oversizedHeader), frame.LatestVersion); err == nil || !strings.Contains(err.Error(), "最大限制") {
		t.Fatalf("oversized reader frame error = %v", err)
	}
	if got, consumed, err := proto.DecodeFrame(oversizedHeader, frame.LatestVersion); err == nil || got != nil || consumed != 0 {
		t.Fatalf("oversized slice frame = (%T, %d, %v), want size rejection", got, consumed, err)
	}

	unknownHeader := []byte{byte(frame.FrameType(15) << 4), 1, 0}
	if _, err := proto.DecodePacketWithConn(bytes.NewReader(unknownHeader), frame.LatestVersion); err == nil || !strings.Contains(err.Error(), "不支持") {
		t.Fatalf("unknown reader frame error = %v", err)
	}
	if got, consumed, err := proto.DecodeFrame(unknownHeader, frame.LatestVersion); err == nil || got != nil || consumed != 0 {
		t.Fatalf("unknown slice frame = (%T, %d, %v), want type rejection", got, consumed, err)
	}

	truncatedBody := []byte{byte(frame.SEND << 4), 2, 0}
	if _, err := proto.DecodePacketWithConn(bytes.NewReader(truncatedBody), frame.LatestVersion); err == nil {
		t.Fatal("truncated reader body error = nil")
	}
}
