package wkprotoenc_test

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	protocolenc "github.com/WuKongIM/WuKongIM/pkg/protocol/wkprotoenc"
)

func TestNegotiateSessionKeysRoundTripsBetweenClientAndServer(t *testing.T) {
	clientPriv, clientPub, err := protocolenc.GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair() error = %v", err)
	}

	serverKeys, serverPublic, err := protocolenc.NegotiateServerSession(protocolenc.EncodePublicKey(clientPub))
	if err != nil {
		t.Fatalf("NegotiateServerSession() error = %v", err)
	}
	clientKeys, err := protocolenc.DeriveClientSession(clientPriv, serverPublic, string(serverKeys.AESIV))
	if err != nil {
		t.Fatalf("DeriveClientSession() error = %v", err)
	}

	if got, want := string(clientKeys.AESKey), string(serverKeys.AESKey); got != want {
		t.Fatalf("AESKey = %q, want %q", got, want)
	}
	if got, want := string(clientKeys.AESIV), string(serverKeys.AESIV); got != want {
		t.Fatalf("AESIV = %q, want %q", got, want)
	}
}

func TestPayloadEncryptionRoundTrip(t *testing.T) {
	keys := protocolenc.SessionKeys{
		AESKey: []byte("1234567890abcdef"),
		AESIV:  []byte("abcdef1234567890"),
	}
	encrypted, err := protocolenc.EncryptPayload([]byte("hello"), keys)
	if err != nil {
		t.Fatalf("EncryptPayload() error = %v", err)
	}
	decrypted, err := protocolenc.DecryptPayload(encrypted, keys)
	if err != nil {
		t.Fatalf("DecryptPayload() error = %v", err)
	}
	if got, want := string(decrypted), "hello"; got != want {
		t.Fatalf("payload = %q, want %q", got, want)
	}
}

func TestSendMsgKeyAndRecvSealUseNegotiatedCrypto(t *testing.T) {
	clientPriv, clientPub, err := protocolenc.GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair() error = %v", err)
	}
	serverKeys, serverPublic, err := protocolenc.NegotiateServerSession(protocolenc.EncodePublicKey(clientPub))
	if err != nil {
		t.Fatalf("NegotiateServerSession() error = %v", err)
	}
	clientKeys, err := protocolenc.DeriveClientSession(clientPriv, serverPublic, string(serverKeys.AESIV))
	if err != nil {
		t.Fatalf("DeriveClientSession() error = %v", err)
	}

	send := &frame.SendPacket{
		ClientSeq:   7,
		ClientMsgNo: "m1",
		ChannelID:   "u2",
		ChannelType: frame.ChannelTypePerson,
		Payload:     []byte("ciphertext"),
	}
	send.MsgKey, err = protocolenc.SendMsgKey(send, clientKeys)
	if err != nil {
		t.Fatalf("SendMsgKey() error = %v", err)
	}
	if err := protocolenc.ValidateSendPacket(send, clientKeys); err != nil {
		t.Fatalf("ValidateSendPacket() error = %v", err)
	}

	recv, err := protocolenc.SealRecvPacket(&frame.RecvPacket{
		MessageID:   99,
		MessageSeq:  8,
		ClientMsgNo: "m1",
		Timestamp:   123,
		FromUID:     "u1",
		ChannelID:   "u2",
		ChannelType: frame.ChannelTypePerson,
		Payload:     []byte("hello"),
	}, serverKeys)
	if err != nil {
		t.Fatalf("SealRecvPacket() error = %v", err)
	}
	decrypted, err := protocolenc.DecryptPayload(recv.Payload, clientKeys)
	if err != nil {
		t.Fatalf("DecryptPayload() error = %v", err)
	}
	if got, want := string(decrypted), "hello"; got != want {
		t.Fatalf("payload = %q, want %q", got, want)
	}
}

func TestSessionReadersReadCachedCryptoAndKeys(t *testing.T) {
	sessionCrypto, err := protocolenc.NewSessionCrypto(protocolenc.SessionKeys{
		AESKey: []byte("1234567890abcdef"),
		AESIV:  []byte("abcdef1234567890"),
	})
	if err != nil {
		t.Fatalf("NewSessionCrypto() error = %v", err)
	}
	reader := valueReader{
		"gateway.encryption_enabled": true,
		"gateway.aes_key":            []byte("1234567890abcdef"),
		"gateway.aes_iv":             []byte("abcdef1234567890"),
		"gateway.wkproto_crypto":     sessionCrypto,
	}

	if !protocolenc.SessionEncryptionEnabled(reader) {
		t.Fatal("SessionEncryptionEnabled() = false, want true")
	}
	keys, ok := protocolenc.SessionKeysFromSession(reader)
	if !ok {
		t.Fatal("SessionKeysFromSession() = false, want true")
	}
	if got, want := string(keys.AESKey), "1234567890abcdef"; got != want {
		t.Fatalf("AESKey = %q, want %q", got, want)
	}
	if got, want := string(keys.AESIV), "abcdef1234567890"; got != want {
		t.Fatalf("AESIV = %q, want %q", got, want)
	}
	cached, ok := protocolenc.SessionCryptoFromSession(reader)
	if !ok || cached == nil {
		t.Fatal("SessionCryptoFromSession() = false, want true")
	}
}

type valueReader map[string]any

func (v valueReader) Value(key string) any {
	return v[key]
}
