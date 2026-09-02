package client

import (
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/wkprotoenc"
)

func TestBuildSendPacketPreservesWireFieldsAndOwnsPayload(t *testing.T) {
	payload := []byte("payload")
	pkt, err := buildSendPacket(Message{
		Setting:     frame.SettingNoEncrypt,
		Expire:      30,
		ClientMsgNo: "client-message",
		ChannelID:   "group-1",
		ChannelType: frame.ChannelTypeGroup,
		Topic:       "thread-1",
		Payload:     payload,
	}, 42)
	if err != nil {
		t.Fatalf("buildSendPacket() error = %v", err)
	}
	if pkt.ClientSeq != 42 || pkt.ClientMsgNo != "client-message" {
		t.Fatalf("packet identity = (%d, %q), want (42, client-message)", pkt.ClientSeq, pkt.ClientMsgNo)
	}
	if pkt.Setting != frame.SettingNoEncrypt || pkt.Expire != 30 || pkt.ChannelID != "group-1" || pkt.ChannelType != frame.ChannelTypeGroup || pkt.Topic != "thread-1" {
		t.Fatalf("packet fields = %#v, want outbound message fields preserved", pkt)
	}
	payload[0] = 'X'
	if got := string(pkt.Payload); got != "payload" {
		t.Fatalf("packet payload = %q after caller mutation, want owned copy", got)
	}
}

func TestBuildSendPacketRejectsInvalidProtocolIdentity(t *testing.T) {
	valid := Message{ChannelID: "group-1", ChannelType: frame.ChannelTypeGroup}
	tests := []struct {
		name string
		msg  Message
		seq  uint64
		want error
	}{
		{name: "missing channel", msg: Message{ChannelType: frame.ChannelTypeGroup}, seq: 1, want: ErrInvalidMessage},
		{name: "missing channel type", msg: Message{ChannelID: "group-1"}, seq: 1, want: ErrInvalidMessage},
		{name: "missing sequence", msg: valid, seq: 0, want: ErrInvalidMessage},
		{name: "sequence exceeds wire width", msg: valid, seq: uint64(math.MaxUint32) + 1, want: ErrClientSeqExhausted},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := buildSendPacket(tt.msg, tt.seq); !errors.Is(err, tt.want) {
				t.Fatalf("buildSendPacket() error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestCryptoStateSealsSendWithoutMutatingCallerPacket(t *testing.T) {
	state := newNegotiatedCryptoStateOrFatal(t)
	original := &frame.SendPacket{
		ClientSeq:   9,
		ClientMsgNo: "message-9",
		ChannelID:   "group-1",
		ChannelType: frame.ChannelTypeGroup,
		Payload:     []byte("secret payload"),
	}

	sealed, err := state.sealSend(original)
	if err != nil {
		t.Fatalf("sealSend() error = %v", err)
	}
	if sealed == original {
		t.Fatal("sealSend() returned caller packet instead of an encrypted copy")
	}
	if string(original.Payload) != "secret payload" || original.MsgKey != "" {
		t.Fatalf("original packet was mutated: payload=%q msg_key=%q", original.Payload, original.MsgKey)
	}
	if string(sealed.Payload) == "secret payload" || sealed.MsgKey == "" {
		t.Fatalf("sealed packet lacks ciphertext evidence: payload=%q msg_key=%q", sealed.Payload, sealed.MsgKey)
	}
	plain, err := wkprotoenc.DecryptPayloadWithCrypto(sealed.Payload, state.currentSession())
	if err != nil {
		t.Fatalf("DecryptPayloadWithCrypto() error = %v", err)
	}
	if string(plain) != "secret payload" {
		t.Fatalf("decrypted payload = %q, want secret payload", plain)
	}
}

func TestCryptoStateEncryptionBypassAndSessionReset(t *testing.T) {
	var nilState *cryptoState
	if nilState.currentSession() != nil {
		t.Fatal("nil crypto state exposed a session")
	}
	if got, err := nilState.sealSend(nil); err != nil || got != nil {
		t.Fatalf("nilState.sealSend(nil) = %#v, %v", got, err)
	}

	state := newNegotiatedCryptoStateOrFatal(t)
	noEncrypt := &frame.SendPacket{Setting: frame.SettingNoEncrypt, Payload: []byte("plain")}
	if got, err := state.sealSend(noEncrypt); err != nil || got != noEncrypt {
		t.Fatalf("sealSend(no-encrypt) = %#v, %v; want original packet", got, err)
	}
	if err := state.applyConnack(&frame.ConnackPacket{}); err != nil {
		t.Fatalf("applyConnack(clear session) error = %v", err)
	}
	if state.currentSession() != nil {
		t.Fatal("unencrypted CONNACK did not clear the previous crypto session")
	}
	plain := &frame.SendPacket{Payload: []byte("plain")}
	if got, err := state.sealSend(plain); err != nil || got != plain {
		t.Fatalf("sealSend(without session) = %#v, %v; want original packet", got, err)
	}
	if err := state.applyConnack(&frame.ConnackPacket{ServerKey: "invalid-key", Salt: "invalid-salt"}); err == nil {
		t.Fatal("applyConnack() error = nil, want invalid peer key rejection")
	}
}

func TestRecvDecryptErrorCarriesBoundedRoutingEvidence(t *testing.T) {
	cause := errors.New("ciphertext rejected")
	if err := recvDecryptError(nil, cause); !errors.Is(err, cause) || !strings.Contains(err.Error(), "packet is nil") {
		t.Fatalf("recvDecryptError(nil) = %v, want wrapped cause and nil-packet evidence", err)
	}

	payload := []byte("abcdefghijklmnopqrstuvwxyz0123456789-sensitive-tail")
	pkt := &frame.RecvPacket{
		ChannelID:   "group-1",
		ChannelType: frame.ChannelTypeGroup,
		FromUID:     "sender",
		ClientMsgNo: "client-message",
		MessageID:   71,
		MessageSeq:  72,
		Payload:     payload,
	}
	err := recvDecryptError(pkt, cause)
	if !errors.Is(err, cause) {
		t.Fatalf("recvDecryptError() = %v, want wrapped cause", err)
	}
	text := err.Error()
	for _, want := range []string{"group-1", "sender", "client-message", "message_id=71", "message_seq=72", "payload_len=51"} {
		if !strings.Contains(text, want) {
			t.Fatalf("recvDecryptError() = %q, missing %q", text, want)
		}
	}
	if strings.Contains(text, "sensitive-tail") {
		t.Fatalf("recvDecryptError() leaked payload tail: %q", text)
	}
}

func TestDetachPayloadOwnsDecodedBufferSlices(t *testing.T) {
	sendBacking := []byte("send")
	send := &frame.SendPacket{Payload: sendBacking}
	detachPayload(send)
	sendBacking[0] = 'X'
	if string(send.Payload) != "send" {
		t.Fatalf("detached SEND payload = %q", send.Payload)
	}

	recvBacking := []byte("recv")
	recv := &frame.RecvPacket{Payload: recvBacking}
	detachPayload(recv)
	recvBacking[0] = 'X'
	if string(recv.Payload) != "recv" {
		t.Fatalf("detached RECV payload = %q", recv.Payload)
	}

	eventBacking := []byte("event")
	event := &frame.EventPacket{Data: eventBacking}
	detachPayload(event)
	eventBacking[0] = 'X'
	if string(event.Data) != "event" {
		t.Fatalf("detached EVENT data = %q", event.Data)
	}

	detachPayload(&frame.PongPacket{})
}

func TestClientErrorTypesPreserveMachineReadableCauses(t *testing.T) {
	cause := errors.New("reader failed")
	wrapped := wrapSessionReadError(cause)
	if wrapped.Error() != cause.Error() || !errors.Is(wrapped, cause) || !IsSessionReadError(wrapped) {
		t.Fatalf("wrapped session error = %v, want discoverable %v", wrapped, cause)
	}
	if got := wrapSessionReadError(nil); !errors.Is(got, ErrClosed) || !IsSessionReadError(got) {
		t.Fatalf("wrapSessionReadError(nil) = %v, want session-wrapped ErrClosed", got)
	}

	sendErr := SendError{ClientSeq: 7, ClientMsgNo: "m-7", ReasonCode: frame.ReasonAuthFail}
	for _, want := range []string{"reason=", "client_seq=7", `client_msg_no="m-7"`} {
		if !strings.Contains(sendErr.Error(), want) {
			t.Fatalf("SendError.Error() = %q, missing %q", sendErr.Error(), want)
		}
	}
}

func newNegotiatedCryptoStateOrFatal(t *testing.T) *cryptoState {
	t.Helper()
	state, err := newCryptoState()
	if err != nil {
		t.Fatalf("newCryptoState() error = %v", err)
	}
	serverKeys, serverKey, err := wkprotoenc.NegotiateServerSession(wkprotoenc.EncodePublicKey(state.public))
	if err != nil {
		t.Fatalf("NegotiateServerSession() error = %v", err)
	}
	if err := state.applyConnack(&frame.ConnackPacket{ServerKey: serverKey, Salt: string(serverKeys.AESIV)}); err != nil {
		t.Fatalf("applyConnack() error = %v", err)
	}
	return state
}
