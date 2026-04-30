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
	ErrInvalidPublicKey  = errors.New("gateway/wkprotoenc: invalid public key")
	ErrMissingSessionKey = errors.New("gateway/wkprotoenc: missing session key")
	ErrMsgKeyMismatch    = errors.New("gateway/wkprotoenc: msg key mismatch")
)

type SessionKeys struct {
	AESKey []byte
	AESIV  []byte
}

// SessionCrypto caches immutable per-session AES state for repeated packet encryption.
type SessionCrypto struct {
	block cipher.Block
	iv    [aes.BlockSize]byte
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
	block, iv, err := aesBlockAndIV(keys)
	if err != nil {
		return nil, err
	}
	return encryptPayloadWithBlock(payload, block, iv)
}

// NewSessionCrypto builds immutable AES state scoped to one negotiated session.
func NewSessionCrypto(keys SessionKeys) (*SessionCrypto, error) {
	block, iv, err := aesBlockAndIV(keys)
	if err != nil {
		return nil, err
	}
	sessionCrypto := &SessionCrypto{block: block}
	sessionCrypto.iv = iv
	return sessionCrypto, nil
}

// SessionCryptoFromSession returns the cached crypto context stored on a gateway session.
func SessionCryptoFromSession(reader ValueReader) (*SessionCrypto, bool) {
	if reader == nil {
		return nil, false
	}
	sessionCrypto, ok := reader.Value(gatewaytypes.SessionValueCrypto).(*SessionCrypto)
	return sessionCrypto, ok && sessionCrypto != nil
}

// EncryptPayloadWithCrypto encrypts a payload using cached per-session AES state.
func EncryptPayloadWithCrypto(payload []byte, sessionCrypto *SessionCrypto) ([]byte, error) {
	if sessionCrypto == nil || sessionCrypto.block == nil {
		return nil, ErrMissingSessionKey
	}
	return encryptPayloadWithBlock(payload, sessionCrypto.block, sessionCrypto.iv)
}

func encryptPayloadWithBlock(payload []byte, block cipher.Block, iv [aes.BlockSize]byte) ([]byte, error) {
	padding := pkcs7PaddingSize(len(payload), aes.BlockSize)
	encrypted := make([]byte, len(payload)+padding)
	copy(encrypted, payload)
	for i := len(payload); i < len(encrypted); i++ {
		encrypted[i] = byte(padding)
	}
	encryptCBCBlocks(block, iv, encrypted)

	out := make([]byte, base64.StdEncoding.EncodedLen(len(encrypted)))
	base64.StdEncoding.Encode(out, encrypted)
	return out, nil
}

func DecryptPayload(payload []byte, keys SessionKeys) ([]byte, error) {
	block, iv, err := aesBlockAndIV(keys)
	if err != nil {
		return nil, err
	}
	plain, err := decryptPayloadWithBlock(payload, block, iv)
	if err != nil {
		return nil, err
	}
	return append([]byte(nil), plain...), nil
}

// DecryptPayloadWithCrypto decrypts a payload using cached per-session AES state.
func DecryptPayloadWithCrypto(payload []byte, sessionCrypto *SessionCrypto) ([]byte, error) {
	if sessionCrypto == nil || sessionCrypto.block == nil {
		return nil, ErrMissingSessionKey
	}
	return decryptPayloadWithBlock(payload, sessionCrypto.block, sessionCrypto.iv)
}

// decryptPayloadWithBlock decrypts in place on the decoded ciphertext buffer.
func decryptPayloadWithBlock(payload []byte, block cipher.Block, iv [aes.BlockSize]byte) ([]byte, error) {
	decoded := make([]byte, base64.StdEncoding.DecodedLen(len(payload)))
	n, err := base64.StdEncoding.Decode(decoded, payload)
	if err != nil {
		return nil, err
	}
	decoded = decoded[:n]
	if len(decoded) == 0 || len(decoded)%aes.BlockSize != 0 {
		return nil, ErrInvalidPublicKey
	}

	decryptCBCBlocks(block, iv, decoded)
	return pkcs7UnpadView(decoded, aes.BlockSize)
}

func SendMsgKey(packet *frame.SendPacket, keys SessionKeys) (string, error) {
	sessionCrypto, err := NewSessionCrypto(keys)
	if err != nil {
		return "", err
	}
	return SendMsgKeyWithCrypto(packet, sessionCrypto)
}

// SendMsgKeyWithCrypto calculates the send packet verification key using cached session crypto.
func SendMsgKeyWithCrypto(packet *frame.SendPacket, sessionCrypto *SessionCrypto) (string, error) {
	if packet == nil {
		return "", ErrMsgKeyMismatch
	}
	return msgKeyWithCrypto([]byte(packet.VerityString()), sessionCrypto)
}

func ValidateSendPacket(packet *frame.SendPacket, keys SessionKeys) error {
	sessionCrypto, err := NewSessionCrypto(keys)
	if err != nil {
		return err
	}
	return ValidateSendPacketWithCrypto(packet, sessionCrypto)
}

// ValidateSendPacketWithCrypto validates a send packet verification key using cached session crypto.
func ValidateSendPacketWithCrypto(packet *frame.SendPacket, sessionCrypto *SessionCrypto) error {
	expected, err := SendMsgKeyWithCrypto(packet, sessionCrypto)
	if err != nil {
		return err
	}
	if packet.MsgKey != expected {
		return ErrMsgKeyMismatch
	}
	return nil
}

func SealRecvPacket(packet *frame.RecvPacket, keys SessionKeys) (*frame.RecvPacket, error) {
	sessionCrypto, err := NewSessionCrypto(keys)
	if err != nil {
		return nil, err
	}
	return SealRecvPacketWithCrypto(packet, sessionCrypto)
}

// SealRecvPacketWithCrypto encrypts a recv packet payload and msg key using cached session crypto.
func SealRecvPacketWithCrypto(packet *frame.RecvPacket, sessionCrypto *SessionCrypto) (*frame.RecvPacket, error) {
	if packet == nil {
		return nil, ErrMsgKeyMismatch
	}

	sealed := *packet

	encrypted, err := EncryptPayloadWithCrypto(packet.Payload, sessionCrypto)
	if err != nil {
		return nil, err
	}
	sealed.Payload = encrypted
	sealed.MsgKey, err = recvMsgKeyWithCrypto(&sealed, sessionCrypto)
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

func aesBlockAndIV(keys SessionKeys) (cipher.Block, [aes.BlockSize]byte, error) {
	if len(keys.AESKey) < aes.BlockSize || len(keys.AESIV) < aes.BlockSize {
		return nil, [aes.BlockSize]byte{}, ErrMissingSessionKey
	}

	var key [aes.BlockSize]byte
	var iv [aes.BlockSize]byte
	copy(key[:], keys.AESKey[:aes.BlockSize])
	copy(iv[:], keys.AESIV[:aes.BlockSize])

	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, [aes.BlockSize]byte{}, err
	}
	return block, iv, nil
}

func msgKeyWithCrypto(sign []byte, sessionCrypto *SessionCrypto) (string, error) {
	encrypted, err := EncryptPayloadWithCrypto(sign, sessionCrypto)
	if err != nil {
		return "", err
	}
	sum := md5.Sum(encrypted)
	return string(hexLower(sum[:])), nil
}

func recvMsgKeyWithCrypto(packet *frame.RecvPacket, sessionCrypto *SessionCrypto) (string, error) {
	if packet == nil {
		return "", ErrMsgKeyMismatch
	}
	return msgKeyWithCrypto([]byte(packet.VerityString()), sessionCrypto)
}

// encryptCBCBlocks applies CBC without allocating a cipher.BlockMode per message.
func encryptCBCBlocks(block cipher.Block, iv [aes.BlockSize]byte, data []byte) {
	previous := iv
	for offset := 0; offset < len(data); offset += aes.BlockSize {
		chunk := data[offset : offset+aes.BlockSize]
		xorBlock(chunk, previous[:])
		block.Encrypt(chunk, chunk)
		copy(previous[:], chunk)
	}
}

// decryptCBCBlocks applies CBC without allocating a cipher.BlockMode per message.
func decryptCBCBlocks(block cipher.Block, iv [aes.BlockSize]byte, data []byte) {
	previous := iv
	var ciphertext [aes.BlockSize]byte
	for offset := 0; offset < len(data); offset += aes.BlockSize {
		chunk := data[offset : offset+aes.BlockSize]
		copy(ciphertext[:], chunk)
		block.Decrypt(chunk, chunk)
		xorBlock(chunk, previous[:])
		previous = ciphertext
	}
}

func xorBlock(dst []byte, mask []byte) {
	for i := 0; i < aes.BlockSize; i++ {
		dst[i] ^= mask[i]
	}
}

func pkcs7PaddingSize(payloadLen int, blockSize int) int {
	padding := blockSize - payloadLen%blockSize
	if padding == 0 {
		padding = blockSize
	}
	return padding
}

func pkcs7UnpadView(payload []byte, blockSize int) ([]byte, error) {
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
	return payload[:len(payload)-padding], nil
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
