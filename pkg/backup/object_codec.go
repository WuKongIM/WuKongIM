package backup

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/klauspost/compress/zstd"
)

const maxObjectPlaintextBytes = 256 << 20

// ObjectCodec compresses and encrypts bounded immutable backup objects.
type ObjectCodec struct {
	keys DataKeyManager
	rand io.Reader
}

// NewObjectCodec creates an object codec backed by keys. random may be nil to use crypto/rand.
func NewObjectCodec(keys DataKeyManager, random io.Reader) *ObjectCodec {
	if random == nil {
		random = rand.Reader
	}
	return &ObjectCodec{keys: keys, rand: random}
}

// Seal compresses plaintext, encrypts it with a fresh data key, and returns manifest metadata.
func (c *ObjectCodec) Seal(ctx context.Context, descriptor ObjectDescriptor, plaintext []byte) (SealedObject, error) {
	if c == nil || c.keys == nil || c.rand == nil {
		return SealedObject{}, fmt.Errorf("%w: codec dependencies are required", ErrInvalidObject)
	}
	if err := validateDescriptor(descriptor); err != nil {
		return SealedObject{}, err
	}
	if len(plaintext) > maxObjectPlaintextBytes {
		return SealedObject{}, fmt.Errorf("%w: plaintext bytes %d exceed %d", ErrInvalidObject, len(plaintext), maxObjectPlaintextBytes)
	}
	dataKey, err := c.keys.GenerateDataKey(ctx, descriptor.KMSKeyID)
	if err != nil {
		return SealedObject{}, fmt.Errorf("generate data key: %w", err)
	}
	defer zeroBytes(dataKey.Plaintext)
	if len(dataKey.Plaintext) != 32 || len(dataKey.Wrapped) == 0 {
		return SealedObject{}, fmt.Errorf("%w: KMS returned an invalid AES-256 data key", ErrInvalidObject)
	}

	compressed, err := compressObject(plaintext)
	if err != nil {
		return SealedObject{}, err
	}
	block, err := aes.NewCipher(dataKey.Plaintext)
	if err != nil {
		return SealedObject{}, fmt.Errorf("create object cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return SealedObject{}, fmt.Errorf("create object AEAD: %w", err)
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(c.rand, nonce); err != nil {
		return SealedObject{}, fmt.Errorf("generate object nonce: %w", err)
	}
	plainHash := sha256.Sum256(plaintext)
	entry := ObjectEntry{
		Key:             descriptor.Key,
		Kind:            descriptor.Kind,
		HashSlot:        descriptor.HashSlot,
		PlaintextSHA256: hex.EncodeToString(plainHash[:]),
		PlaintextBytes:  int64(len(plaintext)),
		Compression:     CompressionZstd,
		Encryption:      EncryptionAES256GCM,
		KMSKeyID:        descriptor.KMSKeyID,
		WrappedKey:      base64.StdEncoding.EncodeToString(dataKey.Wrapped),
		Nonce:           base64.StdEncoding.EncodeToString(nonce),
	}
	aad, err := objectAssociatedData(entry)
	if err != nil {
		return SealedObject{}, err
	}
	ciphertext := aead.Seal(nil, nonce, compressed, aad)
	cipherHash := sha256.Sum256(ciphertext)
	entry.CiphertextSHA256 = hex.EncodeToString(cipherHash[:])
	entry.CiphertextBytes = int64(len(ciphertext))
	return SealedObject{Entry: entry, Ciphertext: ciphertext}, nil
}

// Open verifies, decrypts, decompresses, and verifies one immutable object.
func (c *ObjectCodec) Open(ctx context.Context, entry ObjectEntry, ciphertext []byte) ([]byte, error) {
	if c == nil || c.keys == nil {
		return nil, fmt.Errorf("%w: codec dependencies are required", ErrInvalidObject)
	}
	if entry.PlaintextBytes < 0 || entry.PlaintextBytes > maxObjectPlaintextBytes {
		return nil, fmt.Errorf("%w: plaintext bytes %d exceed bounds", ErrInvalidObject, entry.PlaintextBytes)
	}
	if int64(len(ciphertext)) != entry.CiphertextBytes {
		return nil, fmt.Errorf("%w: ciphertext size mismatch", ErrObjectCorrupt)
	}
	cipherHash := sha256.Sum256(ciphertext)
	if hex.EncodeToString(cipherHash[:]) != entry.CiphertextSHA256 {
		return nil, fmt.Errorf("%w: ciphertext checksum mismatch", ErrObjectCorrupt)
	}
	if entry.Compression != CompressionZstd || entry.Encryption != EncryptionAES256GCM {
		return nil, fmt.Errorf("%w: unsupported codec %q/%q", ErrInvalidObject, entry.Compression, entry.Encryption)
	}
	wrapped, err := base64.StdEncoding.DecodeString(entry.WrappedKey)
	if err != nil || len(wrapped) == 0 {
		return nil, fmt.Errorf("%w: invalid wrapped key", ErrInvalidObject)
	}
	nonce, err := base64.StdEncoding.DecodeString(entry.Nonce)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid nonce", ErrInvalidObject)
	}
	plaintextKey, err := c.keys.UnwrapDataKey(ctx, entry.KMSKeyID, wrapped)
	if err != nil {
		return nil, fmt.Errorf("unwrap data key: %w", err)
	}
	defer zeroBytes(plaintextKey)
	if len(plaintextKey) != 32 {
		return nil, fmt.Errorf("%w: unwrapped key is not AES-256", ErrInvalidObject)
	}
	block, err := aes.NewCipher(plaintextKey)
	if err != nil {
		return nil, fmt.Errorf("create object cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create object AEAD: %w", err)
	}
	if len(nonce) != aead.NonceSize() {
		return nil, fmt.Errorf("%w: nonce size %d", ErrInvalidObject, len(nonce))
	}
	aad, err := objectAssociatedData(entry)
	if err != nil {
		return nil, err
	}
	compressed, err := aead.Open(nil, nonce, ciphertext, aad)
	if err != nil {
		return nil, fmt.Errorf("%w: AEAD authentication failed", ErrObjectCorrupt)
	}
	plaintext, err := decompressObject(compressed, entry.PlaintextBytes)
	if err != nil {
		return nil, err
	}
	plainHash := sha256.Sum256(plaintext)
	if hex.EncodeToString(plainHash[:]) != entry.PlaintextSHA256 {
		return nil, fmt.Errorf("%w: plaintext checksum mismatch", ErrObjectCorrupt)
	}
	return plaintext, nil
}

func validateDescriptor(descriptor ObjectDescriptor) error {
	if err := validateObjectKey(descriptor.Key); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidObject, err)
	}
	switch descriptor.Kind {
	case ObjectKindMetadata, ObjectKindMessages, ObjectKindErasureLedger, ObjectKindChannelIndex:
	default:
		return fmt.Errorf("%w: object kind %q", ErrInvalidObject, descriptor.Kind)
	}
	if strings.TrimSpace(descriptor.KMSKeyID) == "" {
		return fmt.Errorf("%w: KMS key id is required", ErrInvalidObject)
	}
	return nil
}

func compressObject(plaintext []byte) ([]byte, error) {
	encoder, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedDefault))
	if err != nil {
		return nil, fmt.Errorf("create zstd encoder: %w", err)
	}
	defer encoder.Close()
	return encoder.EncodeAll(plaintext, nil), nil
}

func decompressObject(compressed []byte, expectedBytes int64) ([]byte, error) {
	decoder, err := zstd.NewReader(nil, zstd.WithDecoderMaxMemory(maxObjectPlaintextBytes))
	if err != nil {
		return nil, fmt.Errorf("create zstd decoder: %w", err)
	}
	defer decoder.Close()
	plaintext, err := decoder.DecodeAll(compressed, make([]byte, 0, expectedBytes))
	if err != nil {
		return nil, fmt.Errorf("%w: decompress object: %v", ErrObjectCorrupt, err)
	}
	if int64(len(plaintext)) != expectedBytes {
		return nil, fmt.Errorf("%w: plaintext size mismatch", ErrObjectCorrupt)
	}
	return plaintext, nil
}

func objectAssociatedData(entry ObjectEntry) ([]byte, error) {
	value := struct {
		Key             string      `json:"key"`
		Kind            ObjectKind  `json:"kind"`
		HashSlot        uint16      `json:"hash_slot"`
		PlaintextSHA256 string      `json:"plaintext_sha256"`
		PlaintextBytes  int64       `json:"plaintext_bytes"`
		Compression     Compression `json:"compression"`
		Encryption      Encryption  `json:"encryption"`
		KMSKeyID        string      `json:"kms_key_id"`
	}{
		Key:             entry.Key,
		Kind:            entry.Kind,
		HashSlot:        entry.HashSlot,
		PlaintextSHA256: entry.PlaintextSHA256,
		PlaintextBytes:  entry.PlaintextBytes,
		Compression:     entry.Compression,
		Encryption:      entry.Encryption,
		KMSKeyID:        entry.KMSKeyID,
	}
	body, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode object associated data: %w", err)
	}
	return body, nil
}

func zeroBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
