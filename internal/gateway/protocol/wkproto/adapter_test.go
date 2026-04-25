package wkproto_test

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/gateway"
	adapterpkg "github.com/WuKongIM/WuKongIM/internal/gateway/protocol/wkproto"
	"github.com/WuKongIM/WuKongIM/internal/gateway/session"
	"github.com/WuKongIM/WuKongIM/internal/gateway/testkit"
	"github.com/WuKongIM/WuKongIM/internal/gateway/wkprotoenc"
	codec "github.com/WuKongIM/WuKongIM/pkg/protocol/codec"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

func TestAdapterDecodeReturnsZeroUntilFrameIsComplete(t *testing.T) {
	adapter := adapterpkg.New()
	sess := testkit.NewProtocolSession()

	wire, err := codec.New().EncodeFrame(&frame.PingPacket{}, frame.LatestVersion)
	if err != nil {
		t.Fatalf("encode frame failed: %v", err)
	}

	frames, consumed, err := adapter.Decode(sess, wire[:0])
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(frames) != 0 || consumed != 0 {
		t.Fatalf("expected no progress for incomplete frame, got frames=%d consumed=%d", len(frames), consumed)
	}
}

func TestAdapterEncodeRoundTrip(t *testing.T) {
	adapter := adapterpkg.New()
	sess := testkit.NewProtocolSession()

	encoded, err := adapter.Encode(sess, &frame.PingPacket{}, session.OutboundMeta{})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	frames, consumed, err := adapter.Decode(sess, encoded)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if consumed != len(encoded) {
		t.Fatalf("expected consumed=%d, got %d", len(encoded), consumed)
	}
	if len(frames) != 1 {
		t.Fatalf("expected one frame, got %d", len(frames))
	}
	if _, ok := frames[0].(*frame.PingPacket); !ok {
		t.Fatalf("expected ping packet, got %T", frames[0])
	}
}

func TestAdapterDecodeStickyFrames(t *testing.T) {
	adapter := adapterpkg.New()
	sess := testkit.NewProtocolSession()

	codec := codec.New()
	first, err := codec.EncodeFrame(&frame.PingPacket{}, frame.LatestVersion)
	if err != nil {
		t.Fatalf("encode first frame failed: %v", err)
	}
	second, err := codec.EncodeFrame(&frame.PongPacket{}, frame.LatestVersion)
	if err != nil {
		t.Fatalf("encode second frame failed: %v", err)
	}

	wire := append(append([]byte(nil), first...), second...)
	frames, consumed, err := adapter.Decode(sess, wire)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if consumed != len(wire) {
		t.Fatalf("expected consumed=%d, got %d", len(wire), consumed)
	}
	if len(frames) != 2 {
		t.Fatalf("expected two frames, got %d", len(frames))
	}
}

func TestAdapterUsesSessionVersionForOutboundFrames(t *testing.T) {
	adapter := adapterpkg.New()
	sess := testkit.NewProtocolSession()
	sess.SetValue(gateway.SessionValueProtocolVersion, uint8(5))

	encoded, err := adapter.Encode(sess, &frame.SendackPacket{
		MessageID:   9,
		MessageSeq:  42,
		ClientSeq:   7,
		ClientMsgNo: "m1",
		ReasonCode:  frame.ReasonSuccess,
	}, session.OutboundMeta{})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	f, _, err := codec.New().DecodeFrame(encoded, 5)
	if err != nil {
		t.Fatalf("DecodeFrame: %v", err)
	}
	ack, ok := f.(*frame.SendackPacket)
	if !ok {
		t.Fatalf("expected sendack, got %T", f)
	}
	if ack.MessageSeq != 42 {
		t.Fatalf("MessageSeq = %d, want 42", ack.MessageSeq)
	}
}

func TestAdapterEncryptsOutboundRecvPacketWhenSessionIsEncrypted(t *testing.T) {
	adapter := adapterpkg.New()
	sess := testkit.NewProtocolSession()
	sess.SetValue(gateway.SessionValueProtocolVersion, uint8(frame.LatestVersion))
	sess.SetValue(gateway.SessionValueEncryptionEnabled, true)
	sess.SetValue(gateway.SessionValueAESKey, []byte("1234567890abcdef"))
	sess.SetValue(gateway.SessionValueAESIV, []byte("abcdef1234567890"))

	original := &frame.RecvPacket{
		MessageID:   1,
		MessageSeq:  2,
		ClientMsgNo: "m1",
		Timestamp:   3,
		FromUID:     "u1",
		ChannelID:   "u2",
		ChannelType: frame.ChannelTypePerson,
		Payload:     []byte("hello"),
	}

	encoded, err := adapter.Encode(sess, original, session.OutboundMeta{})
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}

	decodedFrame, _, err := codec.New().DecodeFrame(encoded, frame.LatestVersion)
	if err != nil {
		t.Fatalf("DecodeFrame() error = %v", err)
	}
	decoded, ok := decodedFrame.(*frame.RecvPacket)
	if !ok {
		t.Fatalf("decoded frame = %T, want *frame.RecvPacket", decodedFrame)
	}
	if got := string(original.Payload); got != "hello" {
		t.Fatalf("original payload mutated to %q", got)
	}
	if got := string(decoded.Payload); got == "hello" {
		t.Fatalf("decoded payload should be encrypted, got %q", got)
	}
	if decoded.MsgKey == "" {
		t.Fatal("decoded MsgKey is empty")
	}

	plain, err := wkprotoenc.DecryptPayload(decoded.Payload, wkprotoenc.SessionKeys{
		AESKey: []byte("1234567890abcdef"),
		AESIV:  []byte("abcdef1234567890"),
	})
	if err != nil {
		t.Fatalf("DecryptPayload() error = %v", err)
	}
	if got, want := string(plain), "hello"; got != want {
		t.Fatalf("payload = %q, want %q", got, want)
	}
}

func TestAdapterBypassesEncryptionWhenRecvPacketDisablesIt(t *testing.T) {
	adapter := adapterpkg.New()
	sess := testkit.NewProtocolSession()
	sess.SetValue(gateway.SessionValueProtocolVersion, uint8(frame.LatestVersion))
	sess.SetValue(gateway.SessionValueEncryptionEnabled, true)
	sess.SetValue(gateway.SessionValueAESKey, []byte("1234567890abcdef"))
	sess.SetValue(gateway.SessionValueAESIV, []byte("abcdef1234567890"))

	encoded, err := adapter.Encode(sess, &frame.RecvPacket{
		Setting:     frame.SettingNoEncrypt,
		MessageID:   1,
		MessageSeq:  2,
		ClientMsgNo: "m1",
		Timestamp:   3,
		FromUID:     "u1",
		ChannelID:   "u2",
		ChannelType: frame.ChannelTypePerson,
		Payload:     []byte("hello"),
	}, session.OutboundMeta{})
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}

	decodedFrame, _, err := codec.New().DecodeFrame(encoded, frame.LatestVersion)
	if err != nil {
		t.Fatalf("DecodeFrame() error = %v", err)
	}
	decoded, ok := decodedFrame.(*frame.RecvPacket)
	if !ok {
		t.Fatalf("decoded frame = %T, want *frame.RecvPacket", decodedFrame)
	}
	if got, want := string(decoded.Payload), "hello"; got != want {
		t.Fatalf("payload = %q, want %q", got, want)
	}
	if decoded.MsgKey != "" {
		t.Fatalf("MsgKey = %q, want empty", decoded.MsgKey)
	}
}

func TestAdapterReturnsErrorWhenEncryptedSessionIsMissingKeys(t *testing.T) {
	adapter := adapterpkg.New()
	sess := testkit.NewProtocolSession()
	sess.SetValue(gateway.SessionValueProtocolVersion, uint8(frame.LatestVersion))
	sess.SetValue(gateway.SessionValueEncryptionEnabled, true)

	_, err := adapter.Encode(sess, &frame.RecvPacket{
		MessageID:   1,
		MessageSeq:  2,
		ClientMsgNo: "m1",
		Timestamp:   3,
		FromUID:     "u1",
		ChannelID:   "u2",
		ChannelType: frame.ChannelTypePerson,
		Payload:     []byte("hello"),
	}, session.OutboundMeta{})
	if err == nil {
		t.Fatal("expected error for missing session keys")
	}
}
