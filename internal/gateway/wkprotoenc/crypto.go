package wkprotoenc

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"crypto/rand"
	"encoding/base64"
	"errors"

	gatewaytypes "github.com/WuKongIM/WuKongIM/internal/gateway/types"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"golang.org/x/crypto/curve25519"
)

const sessionIVSize = 16

var (
	ErrInvalidPublicKey = errors.New("gateway/wkprotoenc: invalid public key")
	ErrMissingSessionKey = errors.New("gateway/wkprotoenc: missing session key")
	ErrMsgKeyMismatch = errors.New("gateway/wkprotoenc: msg key mismatch")
)

type SessionKeys struct {
	AESKey []byte
	AESIV  []byte
}

type ValueReader interface {
	Value(string) any
}

func GenerateKeyPair() (private, public [32]byte, err error) {
	if _, err = rand.Read(private[:]); err != nil {
		return private, public, err
	}
	pub, err := curve25519.X25519(private[:], curve25519.Basepoint)
	if err != nil {
		return private, public, err
	}
	copy(public[:], pub)
	return private, public, nil
}

func EncodePublicKey(public [32]byte) string {
	return base64.StdEncoding.EncodeToString(public[:])
}

func DecodePublicKey(encoded string) ([32]byte, error) {
	var public [32]byte

	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return public, err
	}
	if len(decoded) != len(public) {
		return public, ErrInvalidPublicKey
	}
	copy(public[:], decoded)
	return public, nil
}

func NegotiateServerSession(clientKey string) (SessionKeys, string, error) {
	clientPublic, err := DecodePublicKey(clientKey)
	if err != nil {
		return SessionKeys{}, "", err
	}
	serverPrivate, serverPublic, err := GenerateKeyPair()
	if err != nil {
		return SessionKeys{}, "", err
	}
	secret, err := sharedSecret(serverPrivate, clientPublic)
	if err != nil {
		return SessionKeys{}, "", err
	}
	iv, err := randomIV()
	if err != nil {
		return SessionKeys{}, "", err
	}
	return SessionKeys{
		AESKey: deriveAESKey(secret),
		AESIV:  iv,
	}, EncodePublicKey(serverPublic), nil
}

func DeriveClientSession(private [32]byte, serverKey string, iv string) (SessionKeys, error) {
	serverPublic, err := DecodePublicKey(serverKey)
	if err != nil {
		return SessionKeys{}, err
	}
	secret, err := sharedSecret(private, serverPublic)
	if err != nil {
		return SessionKeys{}, err
	}
	return SessionKeys{
		AESKey: deriveAESKey(secret),
		AESIV:  []byte(iv),
	}, nil
}

func EncryptPayload(payload []byte, keys SessionKeys) ([]byte, error) {
	key, iv, err := normalizedKeyAndIV(keys)
	if err != nil {
		return nil, err
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	padded := pkcs7Pad(payload, aes.BlockSize)
	encrypted := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(encrypted, padded)

	out := make([]byte, base64.StdEncoding.EncodedLen(len(encrypted)))
	base64.StdEncoding.Encode(out, encrypted)
	return out, nil
}

func DecryptPayload(payload []byte, keys SessionKeys) ([]byte, error) {
	key, iv, err := normalizedKeyAndIV(keys)
	if err != nil {
		return nil, err
	}

	decoded := make([]byte, base64.StdEncoding.DecodedLen(len(payload)))
	n, err := base64.StdEncoding.Decode(decoded, payload)
	if err != nil {
		return nil, err
	}
	decoded = decoded[:n]
	if len(decoded) == 0 || len(decoded)%aes.BlockSize != 0 {
		return nil, ErrInvalidPublicKey
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	plain := make([]byte, len(decoded))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(plain, decoded)
	return pkcs7Unpad(plain, aes.BlockSize)
}

func SendMsgKey(packet *frame.SendPacket, keys SessionKeys) (string, error) {
	if packet == nil {
		return "", ErrMsgKeyMismatch
	}
	return msgKey([]byte(packet.VerityString()), keys)
}

func ValidateSendPacket(packet *frame.SendPacket, keys SessionKeys) error {
	expected, err := SendMsgKey(packet, keys)
	if err != nil {
		return err
	}
	if packet.MsgKey != expected {
		return ErrMsgKeyMismatch
	}
	return nil
}

func SealRecvPacket(packet *frame.RecvPacket, keys SessionKeys) (*frame.RecvPacket, error) {
	if packet == nil {
		return nil, ErrMsgKeyMismatch
	}

	sealed := *packet
	sealed.Payload = append([]byte(nil), packet.Payload...)

	encrypted, err := EncryptPayload(sealed.Payload, keys)
	if err != nil {
		return nil, err
	}
	sealed.Payload = encrypted
	sealed.MsgKey, err = recvMsgKey(&sealed, keys)
	if err != nil {
		return nil, err
	}
	return &sealed, nil
}

func SessionEncryptionEnabled(reader ValueReader) bool {
	if reader == nil {
		return false
	}
	enabled, _ := reader.Value(gatewaytypes.SessionValueEncryptionEnabled).(bool)
	return enabled
}

func SessionKeysFromSession(reader ValueReader) (SessionKeys, bool) {
	if reader == nil {
		return SessionKeys{}, false
	}
	key, ok := bytesValue(reader.Value(gatewaytypes.SessionValueAESKey))
	if !ok {
		return SessionKeys{}, false
	}
	iv, ok := bytesValue(reader.Value(gatewaytypes.SessionValueAESIV))
	if !ok {
		return SessionKeys{}, false
	}
	return SessionKeys{AESKey: key, AESIV: iv}, true
}

func sharedSecret(private, public [32]byte) ([]byte, error) {
	secret, err := curve25519.X25519(private[:], public[:])
	if err != nil {
		return nil, err
	}
	return secret, nil
}

func deriveAESKey(secret []byte) []byte {
	sum := md5.Sum([]byte(base64.StdEncoding.EncodeToString(secret)))
	return []byte(string(hexLower(sum[:])[:16]))
}

func randomIV() ([]byte, error) {
	const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

	buf := make([]byte, sessionIVSize)
	raw := make([]byte, sessionIVSize)
	if _, err := rand.Read(raw); err != nil {
		return nil, err
	}
	for i := range buf {
		buf[i] = alphabet[int(raw[i])%len(alphabet)]
	}
	return buf, nil
}

func normalizedKeyAndIV(keys SessionKeys) ([]byte, []byte, error) {
	if len(keys.AESKey) < aes.BlockSize || len(keys.AESIV) < aes.BlockSize {
		return nil, nil, ErrMissingSessionKey
	}
	key := append([]byte(nil), keys.AESKey[:aes.BlockSize]...)
	iv := append([]byte(nil), keys.AESIV[:aes.BlockSize]...)
	return key, iv, nil
}

func msgKey(sign []byte, keys SessionKeys) (string, error) {
	encrypted, err := EncryptPayload(sign, keys)
	if err != nil {
		return "", err
	}
	sum := md5.Sum(encrypted)
	return string(hexLower(sum[:])), nil
}

func recvMsgKey(packet *frame.RecvPacket, keys SessionKeys) (string, error) {
	if packet == nil {
		return "", ErrMsgKeyMismatch
	}
	return msgKey([]byte(packet.VerityString()), keys)
}

func pkcs7Pad(payload []byte, blockSize int) []byte {
	padding := blockSize - len(payload)%blockSize
	if padding == 0 {
		padding = blockSize
	}
	out := make([]byte, len(payload)+padding)
	copy(out, payload)
	for i := len(payload); i < len(out); i++ {
		out[i] = byte(padding)
	}
	return out
}

func pkcs7Unpad(payload []byte, blockSize int) ([]byte, error) {
	if len(payload) == 0 || len(payload)%blockSize != 0 {
		return nil, ErrMissingSessionKey
	}
	padding := int(payload[len(payload)-1])
	if padding == 0 || padding > blockSize || padding > len(payload) {
		return nil, ErrMissingSessionKey
	}
	for i := len(payload) - padding; i < len(payload); i++ {
		if int(payload[i]) != padding {
			return nil, ErrMissingSessionKey
		}
	}
	return append([]byte(nil), payload[:len(payload)-padding]...), nil
}

func bytesValue(value any) ([]byte, bool) {
	switch v := value.(type) {
	case []byte:
		return append([]byte(nil), v...), len(v) > 0
	case string:
		if v == "" {
			return nil, false
		}
		return []byte(v), true
	default:
		return nil, false
	}
}

func hexLower(src []byte) []byte {
	const digits = "0123456789abcdef"

	out := make([]byte, len(src)*2)
	for i, b := range src {
		out[i*2] = digits[b>>4]
		out[i*2+1] = digits[b&0x0f]
	}
	return out
}
