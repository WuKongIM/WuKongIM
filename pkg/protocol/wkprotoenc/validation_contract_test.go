package wkprotoenc_test

import (
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	protocolenc "github.com/WuKongIM/WuKongIM/pkg/protocol/wkprotoenc"
)

func TestValidateSendPacketWithCryptoRejectsTamperingAndMissingState(t *testing.T) {
	keys := protocolenc.SessionKeys{
		AESKey: []byte("1234567890abcdef"),
		AESIV:  []byte("abcdef1234567890"),
	}
	sessionCrypto, err := protocolenc.NewSessionCrypto(keys)
	if err != nil {
		t.Fatalf("NewSessionCrypto() error = %v", err)
	}
	packet := &frame.SendPacket{
		ClientSeq:   7,
		ClientMsgNo: "m1",
		ChannelID:   "u2",
		ChannelType: frame.ChannelTypePerson,
		Payload:     []byte("ciphertext"),
	}
	packet.MsgKey, err = protocolenc.SendMsgKeyWithCrypto(packet, sessionCrypto)
	if err != nil {
		t.Fatalf("SendMsgKeyWithCrypto() error = %v", err)
	}
	if err := protocolenc.ValidateSendPacketWithCrypto(packet, sessionCrypto); err != nil {
		t.Fatalf("ValidateSendPacketWithCrypto() error = %v", err)
	}

	packet.Payload[0] ^= 1
	if err := protocolenc.ValidateSendPacketWithCrypto(packet, sessionCrypto); !errors.Is(err, protocolenc.ErrMsgKeyMismatch) {
		t.Fatalf("tampered payload error = %v, want ErrMsgKeyMismatch", err)
	}
	if err := protocolenc.ValidateSendPacketWithCrypto(nil, sessionCrypto); !errors.Is(err, protocolenc.ErrMsgKeyMismatch) {
		t.Fatalf("nil packet error = %v, want ErrMsgKeyMismatch", err)
	}
	if err := protocolenc.ValidateSendPacketWithCrypto(packet, nil); !errors.Is(err, protocolenc.ErrMissingSessionKey) {
		t.Fatalf("nil crypto error = %v, want ErrMissingSessionKey", err)
	}
}
