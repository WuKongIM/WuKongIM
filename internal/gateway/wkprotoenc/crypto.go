package wkprotoenc

import (
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	protocolenc "github.com/WuKongIM/WuKongIM/pkg/protocol/wkprotoenc"
)

type SessionKeys = protocolenc.SessionKeys
type SessionCrypto = protocolenc.SessionCrypto
type ValueReader = protocolenc.ValueReader

var (
	ErrInvalidPublicKey  = protocolenc.ErrInvalidPublicKey
	ErrMissingSessionKey = protocolenc.ErrMissingSessionKey
	ErrMsgKeyMismatch    = protocolenc.ErrMsgKeyMismatch
)

func GenerateKeyPair() (private, public [32]byte, err error) {
	return protocolenc.GenerateKeyPair()
}

func EncodePublicKey(public [32]byte) string {
	return protocolenc.EncodePublicKey(public)
}

func DecodePublicKey(encoded string) ([32]byte, error) {
	return protocolenc.DecodePublicKey(encoded)
}

func NegotiateServerSession(clientKey string) (SessionKeys, string, error) {
	return protocolenc.NegotiateServerSession(clientKey)
}

func DeriveClientSession(private [32]byte, serverKey string, iv string) (SessionKeys, error) {
	return protocolenc.DeriveClientSession(private, serverKey, iv)
}

func EncryptPayload(payload []byte, keys SessionKeys) ([]byte, error) {
	return protocolenc.EncryptPayload(payload, keys)
}

func NewSessionCrypto(keys SessionKeys) (*SessionCrypto, error) {
	return protocolenc.NewSessionCrypto(keys)
}

func SessionCryptoFromSession(reader ValueReader) (*SessionCrypto, bool) {
	return protocolenc.SessionCryptoFromSession(reader)
}

func EncryptPayloadWithCrypto(payload []byte, sessionCrypto *SessionCrypto) ([]byte, error) {
	return protocolenc.EncryptPayloadWithCrypto(payload, sessionCrypto)
}

func DecryptPayload(payload []byte, keys SessionKeys) ([]byte, error) {
	return protocolenc.DecryptPayload(payload, keys)
}

func DecryptPayloadWithCrypto(payload []byte, sessionCrypto *SessionCrypto) ([]byte, error) {
	return protocolenc.DecryptPayloadWithCrypto(payload, sessionCrypto)
}

func SendMsgKey(packet *frame.SendPacket, keys SessionKeys) (string, error) {
	return protocolenc.SendMsgKey(packet, keys)
}

func SendMsgKeyWithCrypto(packet *frame.SendPacket, sessionCrypto *SessionCrypto) (string, error) {
	return protocolenc.SendMsgKeyWithCrypto(packet, sessionCrypto)
}

func ValidateSendPacket(packet *frame.SendPacket, keys SessionKeys) error {
	return protocolenc.ValidateSendPacket(packet, keys)
}

func ValidateSendPacketWithCrypto(packet *frame.SendPacket, sessionCrypto *SessionCrypto) error {
	return protocolenc.ValidateSendPacketWithCrypto(packet, sessionCrypto)
}

func SealRecvPacket(packet *frame.RecvPacket, keys SessionKeys) (*frame.RecvPacket, error) {
	return protocolenc.SealRecvPacket(packet, keys)
}

func SealRecvPacketWithCrypto(packet *frame.RecvPacket, sessionCrypto *SessionCrypto) (*frame.RecvPacket, error) {
	return protocolenc.SealRecvPacketWithCrypto(packet, sessionCrypto)
}

func SessionEncryptionEnabled(reader ValueReader) bool {
	return protocolenc.SessionEncryptionEnabled(reader)
}

func SessionKeysFromSession(reader ValueReader) (SessionKeys, bool) {
	return protocolenc.SessionKeysFromSession(reader)
}
