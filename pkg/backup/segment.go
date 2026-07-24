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
)

const (
	// SegmentFormat identifies one content-addressed backup segment.
	SegmentFormat = "wukongim-backup-segment"
	// SegmentVersion is the current content-addressed segment schema version.
	SegmentVersion uint32 = 1

	maxSegmentCiphertextBytes = maxObjectPlaintextBytes + (16 << 20)
)

// SegmentStream identifies the logical stream carried by a segment.
type SegmentStream string

const (
	// SegmentStreamMetadata contains committed metadata changes for one Hash Slot.
	SegmentStreamMetadata SegmentStream = "metadata"
	// SegmentStreamMessages contains committed Channel message changes for one Hash Slot.
	SegmentStreamMessages SegmentStream = "messages"
	// SegmentStreamErasure contains permanent-erasure records for one Hash Slot.
	SegmentStreamErasure SegmentStream = "erasure"
)

// SegmentLogicalDescriptor identifies one ordered position in a Slot stream.
type SegmentLogicalDescriptor struct {
	// RepositoryID is the stable logical identity shared by both repositories.
	RepositoryID string `json:"repository_id"`
	// SourceClusterID identifies the cluster that committed the source data.
	SourceClusterID string `json:"source_cluster_id"`
	// SourceGeneration fences the source cluster disaster-recovery generation.
	SourceGeneration string `json:"source_generation"`
	// Generation identifies the independently compacted Slot generation.
	Generation string `json:"generation"`
	// HashSlot identifies the logical Hash Slot carried by the segment.
	HashSlot uint16 `json:"hash_slot"`
	// Stream identifies metadata, messages, or permanent erasure.
	Stream SegmentStream `json:"stream"`
	// Sequence orders segments within one Slot generation and stream.
	Sequence uint64 `json:"sequence"`
	// RecordCount is the number of logical records encoded in the plaintext.
	RecordCount uint64 `json:"record_count"`
}

// SegmentDescriptor combines stable logical identity with one sealing key.
type SegmentDescriptor struct {
	// Logical contains every field that contributes to the Segment ID.
	Logical SegmentLogicalDescriptor
	// KMSKeyID identifies the key-encryption key used for this sealing attempt.
	KMSKeyID string
}

// SegmentHeader is the canonical logical identity hashed into a Segment ID.
type SegmentHeader struct {
	// Format must equal SegmentFormat.
	Format string `json:"format"`
	// Version selects the segment schema.
	Version uint32 `json:"version"`
	// Logical contains the stable ordered Slot-stream position.
	Logical SegmentLogicalDescriptor `json:"logical"`
	// PlaintextSHA256 authenticates the decompressed logical payload.
	PlaintextSHA256 string `json:"plaintext_sha256"`
	// PlaintextBytes is the decompressed payload size.
	PlaintextBytes int64 `json:"plaintext_bytes"`
}

// SegmentPayload describes one encrypted representation of a logical segment.
type SegmentPayload struct {
	// Key is the immutable repository-relative ciphertext key.
	Key string `json:"key"`
	// CiphertextSHA256 authenticates the stored bytes.
	CiphertextSHA256 string `json:"ciphertext_sha256"`
	// CiphertextBytes is the stored encrypted payload size.
	CiphertextBytes int64 `json:"ciphertext_bytes"`
	// Compression identifies the compression applied before encryption.
	Compression Compression `json:"compression"`
	// Encryption identifies the authenticated encryption algorithm.
	Encryption Encryption `json:"encryption"`
	// KMSKeyID identifies the key-encryption key used to wrap the data key.
	KMSKeyID string `json:"kms_key_id"`
	// WrappedKey is the base64-encoded KMS-wrapped data key.
	WrappedKey string `json:"wrapped_key"`
	// Nonce is the base64-encoded AEAD nonce.
	Nonce string `json:"nonce"`
}

// SealedSegment contains one logical header and one encrypted representation.
type SealedSegment struct {
	// ID is the lowercase SHA-256 of the canonical SegmentHeader.
	ID string
	// Header contains the stable logical segment identity.
	Header SegmentHeader
	// Payload describes Ciphertext without affecting the logical ID.
	Payload SegmentPayload
	// Ciphertext is the compressed then encrypted segment body.
	Ciphertext []byte
}

// SegmentCodec seals and opens bounded content-addressed backup segments.
type SegmentCodec struct {
	// keys creates and unwraps the one envelope key used by each segment.
	keys DataKeyManager
	// rand supplies a fresh AES-GCM nonce for each sealing attempt.
	rand io.Reader
}

// NewSegmentCodec creates a codec backed by keys. random may be nil to use crypto/rand.
func NewSegmentCodec(keys DataKeyManager, random io.Reader) *SegmentCodec {
	if random == nil {
		random = rand.Reader
	}
	return &SegmentCodec{keys: keys, rand: random}
}

// Seal derives the logical ID before applying fresh compression and encryption.
func (c *SegmentCodec) Seal(ctx context.Context, descriptor SegmentDescriptor, plaintext []byte) (SealedSegment, error) {
	if c == nil || c.keys == nil || c.rand == nil {
		return SealedSegment{}, fmt.Errorf("%w: segment codec dependencies are required", ErrInvalidObject)
	}
	header, segmentID, canonical, err := buildSegmentIdentity(descriptor, plaintext)
	if err != nil {
		return SealedSegment{}, err
	}

	dataKey, err := c.keys.GenerateDataKey(ctx, descriptor.KMSKeyID)
	if err != nil {
		return SealedSegment{}, fmt.Errorf("generate segment data key: %w", err)
	}
	defer zeroBytes(dataKey.Plaintext)
	if len(dataKey.Plaintext) != 32 || len(dataKey.Wrapped) == 0 {
		return SealedSegment{}, fmt.Errorf("%w: KMS returned an invalid segment AES-256 data key", ErrInvalidObject)
	}
	compressed, err := compressObject(plaintext)
	if err != nil {
		return SealedSegment{}, err
	}
	block, err := aes.NewCipher(dataKey.Plaintext)
	if err != nil {
		return SealedSegment{}, fmt.Errorf("create segment cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return SealedSegment{}, fmt.Errorf("create segment AEAD: %w", err)
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(c.rand, nonce); err != nil {
		return SealedSegment{}, fmt.Errorf("generate segment nonce: %w", err)
	}
	ciphertext := aead.Seal(compressed[:0], nonce, compressed, canonical)
	ciphertextHash := sha256.Sum256(ciphertext)
	ciphertextSHA256 := hex.EncodeToString(ciphertextHash[:])
	payload := SegmentPayload{
		Key:              segmentPayloadKey(segmentID, ciphertextSHA256),
		CiphertextSHA256: ciphertextSHA256,
		CiphertextBytes:  int64(len(ciphertext)),
		Compression:      CompressionZstd,
		Encryption:       EncryptionAES256GCM,
		KMSKeyID:         descriptor.KMSKeyID,
		WrappedKey:       base64.StdEncoding.EncodeToString(dataKey.Wrapped),
		Nonce:            base64.StdEncoding.EncodeToString(nonce),
	}
	return SealedSegment{ID: segmentID, Header: header, Payload: payload, Ciphertext: ciphertext}, nil
}

// Open authenticates, decrypts, decompresses, and verifies one sealed segment.
// It may reuse ciphertext storage to keep the bounded segment memory peak low.
func (c *SegmentCodec) Open(ctx context.Context, header SegmentHeader, payload SegmentPayload, ciphertext []byte) ([]byte, error) {
	if c == nil || c.keys == nil {
		return nil, fmt.Errorf("%w: segment codec dependencies are required", ErrInvalidObject)
	}
	canonical, err := canonicalSegmentHeader(header)
	if err != nil {
		return nil, err
	}
	identityHash := sha256.Sum256(canonical)
	segmentID := hex.EncodeToString(identityHash[:])
	if err := validateSegmentPayload(segmentID, payload); err != nil {
		return nil, err
	}
	if int64(len(ciphertext)) != payload.CiphertextBytes {
		return nil, fmt.Errorf("%w: segment ciphertext size mismatch", ErrObjectCorrupt)
	}
	ciphertextHash := sha256.Sum256(ciphertext)
	if hex.EncodeToString(ciphertextHash[:]) != payload.CiphertextSHA256 {
		return nil, fmt.Errorf("%w: segment ciphertext checksum mismatch", ErrObjectCorrupt)
	}
	wrapped, err := base64.StdEncoding.DecodeString(payload.WrappedKey)
	if err != nil || len(wrapped) == 0 {
		return nil, fmt.Errorf("%w: invalid segment wrapped key", ErrInvalidObject)
	}
	nonce, err := base64.StdEncoding.DecodeString(payload.Nonce)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid segment nonce", ErrInvalidObject)
	}
	plaintextKey, err := c.keys.UnwrapDataKey(ctx, payload.KMSKeyID, wrapped)
	if err != nil {
		return nil, fmt.Errorf("unwrap segment data key: %w", err)
	}
	defer zeroBytes(plaintextKey)
	if len(plaintextKey) != 32 {
		return nil, fmt.Errorf("%w: unwrapped segment key is not AES-256", ErrInvalidObject)
	}
	block, err := aes.NewCipher(plaintextKey)
	if err != nil {
		return nil, fmt.Errorf("create segment cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create segment AEAD: %w", err)
	}
	if len(nonce) != aead.NonceSize() {
		return nil, fmt.Errorf("%w: segment nonce size %d", ErrInvalidObject, len(nonce))
	}
	compressed, err := aead.Open(ciphertext[:0], nonce, ciphertext, canonical)
	if err != nil {
		return nil, fmt.Errorf("%w: segment AEAD authentication failed", ErrObjectCorrupt)
	}
	plaintext, err := decompressObject(compressed, header.PlaintextBytes)
	if err != nil {
		return nil, err
	}
	plaintextHash := sha256.Sum256(plaintext)
	if hex.EncodeToString(plaintextHash[:]) != header.PlaintextSHA256 {
		return nil, fmt.Errorf("%w: segment plaintext checksum mismatch", ErrObjectCorrupt)
	}
	return plaintext, nil
}

func canonicalSegmentHeader(header SegmentHeader) ([]byte, error) {
	if err := validateSegmentHeader(header); err != nil {
		return nil, err
	}
	body, err := json.Marshal(header)
	if err != nil {
		return nil, fmt.Errorf("canonical segment header: %w", err)
	}
	return body, nil
}

func validateSegmentDescriptor(descriptor SegmentDescriptor) error {
	if err := validateSegmentLogicalDescriptor(descriptor.Logical); err != nil {
		return err
	}
	if strings.TrimSpace(descriptor.KMSKeyID) == "" || len(descriptor.KMSKeyID) > 512 {
		return fmt.Errorf("%w: segment KMS key id is invalid", ErrInvalidObject)
	}
	return nil
}

func validateSegmentLogicalDescriptor(descriptor SegmentLogicalDescriptor) error {
	if err := validateSegmentIdentity(descriptor.RepositoryID, "repository id"); err != nil {
		return err
	}
	if err := validateSegmentIdentity(descriptor.SourceClusterID, "source cluster id"); err != nil {
		return err
	}
	if err := validateSegmentIdentity(descriptor.SourceGeneration, "source generation"); err != nil {
		return err
	}
	if err := validateSegmentIdentity(descriptor.Generation, "generation"); err != nil {
		return err
	}
	switch descriptor.Stream {
	case SegmentStreamMetadata, SegmentStreamMessages, SegmentStreamErasure:
	default:
		return fmt.Errorf("%w: segment stream %q is invalid", ErrInvalidObject, descriptor.Stream)
	}
	if descriptor.Sequence == 0 || descriptor.RecordCount == 0 {
		return fmt.Errorf("%w: segment sequence and record count must be positive", ErrInvalidObject)
	}
	return nil
}

func validateSegmentHeader(header SegmentHeader) error {
	if header.Format != SegmentFormat || header.Version != SegmentVersion {
		return fmt.Errorf("%w: segment format or version is unsupported", ErrInvalidObject)
	}
	if err := validateSegmentLogicalDescriptor(header.Logical); err != nil {
		return err
	}
	if err := validateSHA256(header.PlaintextSHA256); err != nil {
		return fmt.Errorf("%w: segment plaintext checksum: %v", ErrInvalidObject, err)
	}
	if header.PlaintextBytes <= 0 || header.PlaintextBytes > maxObjectPlaintextBytes {
		return fmt.Errorf("%w: segment plaintext bytes are outside bounds", ErrInvalidObject)
	}
	return nil
}

func validateSegmentPayload(segmentID string, payload SegmentPayload) error {
	if err := validateSHA256(segmentID); err != nil {
		return fmt.Errorf("%w: segment id: %v", ErrInvalidObject, err)
	}
	if err := validateSHA256(payload.CiphertextSHA256); err != nil {
		return fmt.Errorf("%w: segment ciphertext checksum: %v", ErrInvalidObject, err)
	}
	if payload.Key != segmentPayloadKey(segmentID, payload.CiphertextSHA256) {
		return fmt.Errorf("%w: segment payload key mismatch", ErrInvalidObject)
	}
	if payload.CiphertextBytes <= 0 || payload.CiphertextBytes > maxSegmentCiphertextBytes {
		return fmt.Errorf("%w: segment ciphertext bytes are outside bounds", ErrInvalidObject)
	}
	if payload.Compression != CompressionZstd || payload.Encryption != EncryptionAES256GCM {
		return fmt.Errorf("%w: segment codec is unsupported", ErrInvalidObject)
	}
	if strings.TrimSpace(payload.KMSKeyID) == "" || len(payload.KMSKeyID) > 512 {
		return fmt.Errorf("%w: segment KMS key id is invalid", ErrInvalidObject)
	}
	if wrapped, err := base64.StdEncoding.DecodeString(payload.WrappedKey); err != nil || len(wrapped) == 0 {
		return fmt.Errorf("%w: segment wrapped key is invalid", ErrInvalidObject)
	}
	if nonce, err := base64.StdEncoding.DecodeString(payload.Nonce); err != nil || len(nonce) == 0 {
		return fmt.Errorf("%w: segment nonce is invalid", ErrInvalidObject)
	}
	return nil
}

func validateSegmentIdentity(value, name string) error {
	if err := validateRestorePointID(value); err != nil {
		return fmt.Errorf("%w: segment %s: %v", ErrInvalidObject, name, err)
	}
	return nil
}

func segmentPayloadKey(segmentID, ciphertextSHA256 string) string {
	return "segments/" + segmentID + "/payloads/" + ciphertextSHA256 + ".bin"
}

func buildSegmentIdentity(descriptor SegmentDescriptor, plaintext []byte) (SegmentHeader, string, []byte, error) {
	if err := validateSegmentDescriptor(descriptor); err != nil {
		return SegmentHeader{}, "", nil, err
	}
	if len(plaintext) == 0 || len(plaintext) > maxObjectPlaintextBytes {
		return SegmentHeader{}, "", nil, fmt.Errorf("%w: segment plaintext size %d is outside bounds", ErrInvalidObject, len(plaintext))
	}
	plaintextHash := sha256.Sum256(plaintext)
	header := SegmentHeader{
		Format:          SegmentFormat,
		Version:         SegmentVersion,
		Logical:         descriptor.Logical,
		PlaintextSHA256: hex.EncodeToString(plaintextHash[:]),
		PlaintextBytes:  int64(len(plaintext)),
	}
	canonical, err := canonicalSegmentHeader(header)
	if err != nil {
		return SegmentHeader{}, "", nil, err
	}
	identityHash := sha256.Sum256(canonical)
	return header, hex.EncodeToString(identityHash[:]), canonical, nil
}
