package wkprotoenc_test

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"

	gatewaytypes "github.com/WuKongIM/WuKongIM/internal/gateway/types"
	"github.com/WuKongIM/WuKongIM/internal/gateway/wkprotoenc"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"golang.org/x/crypto/curve25519"
)

func TestGatewayEncryptionNegotiatesSameSessionKeysForClientAndServer(t *testing.T) {
	clientPriv, clientPub, err := wkprotoenc.GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair() error = %v", err)
	}

	serverKeys, serverPublicKey, err := wkprotoenc.NegotiateServerSession(wkprotoenc.EncodePublicKey(clientPub))
	if err != nil {
		t.Fatalf("NegotiateServerSession() error = %v", err)
	}

	clientKeys, err := wkprotoenc.DeriveClientSession(clientPriv, serverPublicKey, string(serverKeys.AESIV))
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

func TestGatewayEncryptionPayloadRoundTrip(t *testing.T) {
	clientPriv, clientPub, err := wkprotoenc.GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair() error = %v", err)
	}

	serverKeys, serverPublicKey, err := wkprotoenc.NegotiateServerSession(wkprotoenc.EncodePublicKey(clientPub))
	if err != nil {
		t.Fatalf("NegotiateServerSession() error = %v", err)
	}
	clientKeys, err := wkprotoenc.DeriveClientSession(clientPriv, serverPublicKey, string(serverKeys.AESIV))
	if err != nil {
		t.Fatalf("DeriveClientSession() error = %v", err)
	}

	encrypted, err := wkprotoenc.EncryptPayload([]byte("hello"), clientKeys)
	if err != nil {
		t.Fatalf("EncryptPayload() error = %v", err)
	}
	decrypted, err := wkprotoenc.DecryptPayload(encrypted, serverKeys)
	if err != nil {
		t.Fatalf("DecryptPayload() error = %v", err)
	}

	if got, want := string(decrypted), "hello"; got != want {
		t.Fatalf("payload = %q, want %q", got, want)
	}
}

func TestGatewayEncryptionValidatesSendMsgKey(t *testing.T) {
	keys := wkprotoenc.SessionKeys{
		AESKey: []byte("1234567890abcdef"),
		AESIV:  []byte("abcdef1234567890"),
	}
	packet := &frame.SendPacket{
		ClientSeq:   7,
		ClientMsgNo: "m1",
		ChannelID:   "u2",
		ChannelType: frame.ChannelTypePerson,
		Payload:     []byte("ciphertext"),
	}

	msgKey, err := wkprotoenc.SendMsgKey(packet, keys)
	if err != nil {
		t.Fatalf("SendMsgKey() error = %v", err)
	}
	packet.MsgKey = msgKey

	if err := wkprotoenc.ValidateSendPacket(packet, keys); err != nil {
		t.Fatalf("ValidateSendPacket() error = %v", err)
	}

	packet.MsgKey = "bad-key"
	if err := wkprotoenc.ValidateSendPacket(packet, keys); err != wkprotoenc.ErrMsgKeyMismatch {
		t.Fatalf("ValidateSendPacket() error = %v, want %v", err, wkprotoenc.ErrMsgKeyMismatch)
	}
}

func TestGatewayEncryptionSealsRecvPacketWithEncryptedPayloadAndMsgKey(t *testing.T) {
	clientPriv, clientPub, err := wkprotoenc.GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair() error = %v", err)
	}

	serverKeys, serverPublicKey, err := wkprotoenc.NegotiateServerSession(wkprotoenc.EncodePublicKey(clientPub))
	if err != nil {
		t.Fatalf("NegotiateServerSession() error = %v", err)
	}
	clientKeys, err := wkprotoenc.DeriveClientSession(clientPriv, serverPublicKey, string(serverKeys.AESIV))
	if err != nil {
		t.Fatalf("DeriveClientSession() error = %v", err)
	}

	packet := &frame.RecvPacket{
		MessageID:   99,
		MessageSeq:  8,
		ClientMsgNo: "m1",
		Timestamp:   123,
		FromUID:     "u1",
		ChannelID:   "u2",
		ChannelType: frame.ChannelTypePerson,
		Payload:     []byte("hello"),
	}

	sealed, err := wkprotoenc.SealRecvPacket(packet, serverKeys)
	if err != nil {
		t.Fatalf("SealRecvPacket() error = %v", err)
	}
	if got := string(packet.Payload); got != "hello" {
		t.Fatalf("original payload mutated to %q", got)
	}
	if got := string(sealed.Payload); got == "hello" {
		t.Fatalf("sealed payload should be encrypted, got %q", got)
	}
	if sealed.MsgKey == "" {
		t.Fatal("sealed MsgKey is empty")
	}

	decrypted, err := wkprotoenc.DecryptPayload(sealed.Payload, clientKeys)
	if err != nil {
		t.Fatalf("DecryptPayload() error = %v", err)
	}
	if got, want := string(decrypted), "hello"; got != want {
		t.Fatalf("payload = %q, want %q", got, want)
	}
}

func TestGatewayEncryptionAcceptsLegacyClientDerivedKeysForSendValidation(t *testing.T) {
	clientPriv, clientPub, err := wkprotoenc.GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair() error = %v", err)
	}

	serverKeys, serverPublicKey, err := wkprotoenc.NegotiateServerSession(wkprotoenc.EncodePublicKey(clientPub))
	if err != nil {
		t.Fatalf("NegotiateServerSession() error = %v", err)
	}

	legacyClientKeys, err := legacyDeriveClientSession(clientPriv, serverPublicKey, string(serverKeys.AESIV))
	if err != nil {
		t.Fatalf("legacyDeriveClientSession() error = %v", err)
	}

	packet := &frame.SendPacket{
		ClientSeq:   7,
		ClientMsgNo: "legacy-client",
		ChannelID:   "u2",
		ChannelType: frame.ChannelTypePerson,
		Payload:     []byte("hello"),
	}
	packet.Payload, err = wkprotoenc.EncryptPayload(packet.Payload, legacyClientKeys)
	if err != nil {
		t.Fatalf("EncryptPayload() error = %v", err)
	}
	packet.MsgKey, err = wkprotoenc.SendMsgKey(packet, legacyClientKeys)
	if err != nil {
		t.Fatalf("SendMsgKey() error = %v", err)
	}

	if err := wkprotoenc.ValidateSendPacket(packet, serverKeys); err != nil {
		t.Fatalf("ValidateSendPacket() error = %v", err)
	}

	decrypted, err := wkprotoenc.DecryptPayload(packet.Payload, serverKeys)
	if err != nil {
		t.Fatalf("DecryptPayload() error = %v", err)
	}
	if got, want := string(decrypted), "hello"; got != want {
		t.Fatalf("payload = %q, want %q", got, want)
	}
}

func TestGatewayEncryptionReadsCachedSessionCrypto(t *testing.T) {
	keys := wkprotoenc.SessionKeys{
		AESKey: []byte("1234567890abcdef"),
		AESIV:  []byte("abcdef1234567890"),
	}
	sessionCrypto, err := wkprotoenc.NewSessionCrypto(keys)
	if err != nil {
		t.Fatalf("NewSessionCrypto() error = %v", err)
	}
	reader := valueReader{
		gatewaytypes.SessionValueCrypto: sessionCrypto,
	}

	got, ok := wkprotoenc.SessionCryptoFromSession(reader)
	if !ok {
		t.Fatal("SessionCryptoFromSession() did not read cached session crypto")
	}
	encrypted, err := wkprotoenc.EncryptPayloadWithCrypto([]byte("hello"), got)
	if err != nil {
		t.Fatalf("EncryptPayloadWithCrypto() error = %v", err)
	}
	decrypted, err := wkprotoenc.DecryptPayload(encrypted, keys)
	if err != nil {
		t.Fatalf("DecryptPayload() error = %v", err)
	}
	if string(decrypted) != "hello" {
		t.Fatalf("decrypted payload = %q, want hello", decrypted)
	}
}

func TestGatewayEncryptionSessionCryptoMatchesKeyAPIs(t *testing.T) {
	keys := wkprotoenc.SessionKeys{
		AESKey: []byte("1234567890abcdef"),
		AESIV:  []byte("abcdef1234567890"),
	}
	sessionCrypto, err := wkprotoenc.NewSessionCrypto(keys)
	if err != nil {
		t.Fatalf("NewSessionCrypto() error = %v", err)
	}

	payload := []byte("hello")
	encryptedWithKeys, err := wkprotoenc.EncryptPayload(payload, keys)
	if err != nil {
		t.Fatalf("EncryptPayload() error = %v", err)
	}
	encryptedWithCrypto, err := wkprotoenc.EncryptPayloadWithCrypto(payload, sessionCrypto)
	if err != nil {
		t.Fatalf("EncryptPayloadWithCrypto() error = %v", err)
	}
	if string(encryptedWithCrypto) != string(encryptedWithKeys) {
		t.Fatalf("cached encrypted payload = %q, want %q", encryptedWithCrypto, encryptedWithKeys)
	}

	decrypted, err := wkprotoenc.DecryptPayloadWithCrypto(encryptedWithKeys, sessionCrypto)
	if err != nil {
		t.Fatalf("DecryptPayloadWithCrypto() error = %v", err)
	}
	if string(decrypted) != "hello" {
		t.Fatalf("decrypted payload = %q, want hello", decrypted)
	}

	packet := &frame.RecvPacket{
		MessageID:   99,
		MessageSeq:  8,
		ClientMsgNo: "m1",
		Timestamp:   123,
		FromUID:     "u1",
		ChannelID:   "u2",
		ChannelType: frame.ChannelTypePerson,
		Payload:     []byte("hello"),
	}
	sealedWithKeys, err := wkprotoenc.SealRecvPacket(packet, keys)
	if err != nil {
		t.Fatalf("SealRecvPacket() error = %v", err)
	}
	sealedWithCrypto, err := wkprotoenc.SealRecvPacketWithCrypto(packet, sessionCrypto)
	if err != nil {
		t.Fatalf("SealRecvPacketWithCrypto() error = %v", err)
	}
	if string(sealedWithCrypto.Payload) != string(sealedWithKeys.Payload) || sealedWithCrypto.MsgKey != sealedWithKeys.MsgKey {
		t.Fatalf("cached sealed packet differs from key API")
	}
}

func TestGatewayEncryptionSessionCryptoRoundTripsVariedPayloadSizes(t *testing.T) {
	keys := wkprotoenc.SessionKeys{
		AESKey: []byte("1234567890abcdef"),
		AESIV:  []byte("abcdef1234567890"),
	}
	sessionCrypto, err := wkprotoenc.NewSessionCrypto(keys)
	if err != nil {
		t.Fatalf("NewSessionCrypto() error = %v", err)
	}

	for _, size := range []int{0, 1, 15, 16, 17, 31, 32, 33, 64, 127} {
		payload := []byte(strings.Repeat("x", size))
		encryptedWithKeys, err := wkprotoenc.EncryptPayload(payload, keys)
		if err != nil {
			t.Fatalf("EncryptPayload(size=%d) error = %v", size, err)
		}
		encryptedWithCrypto, err := wkprotoenc.EncryptPayloadWithCrypto(payload, sessionCrypto)
		if err != nil {
			t.Fatalf("EncryptPayloadWithCrypto(size=%d) error = %v", size, err)
		}
		if string(encryptedWithCrypto) != string(encryptedWithKeys) {
			t.Fatalf("cached encrypted payload for size %d differs from key API", size)
		}

		decrypted, err := wkprotoenc.DecryptPayloadWithCrypto(encryptedWithCrypto, sessionCrypto)
		if err != nil {
			t.Fatalf("DecryptPayloadWithCrypto(size=%d) error = %v", size, err)
		}
		if string(decrypted) != string(payload) {
			t.Fatalf("decrypted payload size %d = %q, want %q", size, decrypted, payload)
		}
	}
}

func TestGatewayEncryptionSessionCryptoMatchesStandardCBC(t *testing.T) {
	keys := wkprotoenc.SessionKeys{
		AESKey: []byte("1234567890abcdef"),
		AESIV:  []byte("abcdef1234567890"),
	}
	sessionCrypto, err := wkprotoenc.NewSessionCrypto(keys)
	if err != nil {
		t.Fatalf("NewSessionCrypto() error = %v", err)
	}

	for _, size := range []int{0, 1, 15, 16, 17, 31, 32, 33, 64, 127} {
		payload := []byte(strings.Repeat("x", size))
		want, err := referenceEncryptPayload(payload, keys)
		if err != nil {
			t.Fatalf("referenceEncryptPayload(size=%d) error = %v", size, err)
		}

		got, err := wkprotoenc.EncryptPayloadWithCrypto(payload, sessionCrypto)
		if err != nil {
			t.Fatalf("EncryptPayloadWithCrypto(size=%d) error = %v", size, err)
		}
		if string(got) != string(want) {
			t.Fatalf("encrypted payload for size %d differs from standard CBC", size)
		}

		decrypted, err := wkprotoenc.DecryptPayloadWithCrypto(want, sessionCrypto)
		if err != nil {
			t.Fatalf("DecryptPayloadWithCrypto(size=%d) error = %v", size, err)
		}
		if string(decrypted) != string(payload) {
			t.Fatalf("decrypted standard CBC payload size %d = %q, want %q", size, decrypted, payload)
		}
	}
}

func BenchmarkEncryptPayload(b *testing.B) {
	keys := benchmarkNegotiatedSessionKeys(b)
	payload := []byte("benchmark payload for wkproto encryption")

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		encrypted, err := wkprotoenc.EncryptPayload(payload, keys)
		if err != nil {
			b.Fatalf("EncryptPayload() error = %v", err)
		}
		if len(encrypted) == 0 {
			b.Fatal("empty encrypted payload")
		}
	}
}

func BenchmarkSessionCryptoEncryptPayload(b *testing.B) {
	keys := benchmarkNegotiatedSessionKeys(b)
	sessionCrypto, err := wkprotoenc.NewSessionCrypto(keys)
	if err != nil {
		b.Fatalf("NewSessionCrypto() error = %v", err)
	}
	payload := []byte("benchmark payload for wkproto encryption")

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		encrypted, err := wkprotoenc.EncryptPayloadWithCrypto(payload, sessionCrypto)
		if err != nil {
			b.Fatalf("EncryptPayloadWithCrypto() error = %v", err)
		}
		if len(encrypted) == 0 {
			b.Fatal("empty encrypted payload")
		}
	}
}

func BenchmarkSealRecvPacket(b *testing.B) {
	keys := benchmarkNegotiatedSessionKeys(b)
	payload := []byte(strings.Repeat("recv-payload-", 4))
	packet := &frame.RecvPacket{
		MessageID:   99,
		MessageSeq:  8,
		ClientMsgNo: "m1",
		Timestamp:   123,
		FromUID:     "u1",
		ChannelID:   "u2",
		ChannelType: frame.ChannelTypePerson,
		Payload:     payload,
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sealed, err := wkprotoenc.SealRecvPacket(packet, keys)
		if err != nil {
			b.Fatalf("SealRecvPacket() error = %v", err)
		}
		if len(sealed.Payload) == 0 || sealed.MsgKey == "" {
			b.Fatal("sealed packet is incomplete")
		}
	}
}

func BenchmarkSessionCryptoSealRecvPacket(b *testing.B) {
	keys := benchmarkNegotiatedSessionKeys(b)
	sessionCrypto, err := wkprotoenc.NewSessionCrypto(keys)
	if err != nil {
		b.Fatalf("NewSessionCrypto() error = %v", err)
	}
	payload := []byte(strings.Repeat("recv-payload-", 4))
	packet := &frame.RecvPacket{
		MessageID:   99,
		MessageSeq:  8,
		ClientMsgNo: "m1",
		Timestamp:   123,
		FromUID:     "u1",
		ChannelID:   "u2",
		ChannelType: frame.ChannelTypePerson,
		Payload:     payload,
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sealed, err := wkprotoenc.SealRecvPacketWithCrypto(packet, sessionCrypto)
		if err != nil {
			b.Fatalf("SealRecvPacketWithCrypto() error = %v", err)
		}
		if len(sealed.Payload) == 0 || sealed.MsgKey == "" {
			b.Fatal("sealed packet is incomplete")
		}
	}
}

func benchmarkNegotiatedSessionKeys(b *testing.B) wkprotoenc.SessionKeys {
	b.Helper()
	_, clientPub, err := wkprotoenc.GenerateKeyPair()
	if err != nil {
		b.Fatalf("GenerateKeyPair() error = %v", err)
	}
	keys, _, err := wkprotoenc.NegotiateServerSession(wkprotoenc.EncodePublicKey(clientPub))
	if err != nil {
		b.Fatalf("NegotiateServerSession() error = %v", err)
	}
	return keys
}

type valueReader map[string]any

func (r valueReader) Value(key string) any {
	return r[key]
}

func referenceEncryptPayload(payload []byte, keys wkprotoenc.SessionKeys) ([]byte, error) {
	block, err := aes.NewCipher(keys.AESKey[:aes.BlockSize])
	if err != nil {
		return nil, err
	}
	padding := aes.BlockSize - len(payload)%aes.BlockSize
	if padding == 0 {
		padding = aes.BlockSize
	}
	padded := make([]byte, len(payload)+padding)
	copy(padded, payload)
	for i := len(payload); i < len(padded); i++ {
		padded[i] = byte(padding)
	}
	cipher.NewCBCEncrypter(block, keys.AESIV[:aes.BlockSize]).CryptBlocks(padded, padded)
	out := make([]byte, base64.StdEncoding.EncodedLen(len(padded)))
	base64.StdEncoding.Encode(out, padded)
	return out, nil
}

func legacyDeriveClientSession(private [32]byte, serverKey string, iv string) (wkprotoenc.SessionKeys, error) {
	serverPublic, err := wkprotoenc.DecodePublicKey(serverKey)
	if err != nil {
		return wkprotoenc.SessionKeys{}, err
	}
	secret, err := curve25519.X25519(private[:], serverPublic[:])
	if err != nil {
		return wkprotoenc.SessionKeys{}, err
	}
	return wkprotoenc.SessionKeys{
		AESKey: legacyDeriveAESKey(secret),
		AESIV:  []byte(iv),
	}, nil
}

func legacyDeriveAESKey(secret []byte) []byte {
	sum := md5.Sum([]byte(base64.StdEncoding.EncodeToString(secret)))
	return []byte(hex.EncodeToString(sum[:])[:16])
}
