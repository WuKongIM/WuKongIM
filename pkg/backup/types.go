package backup

import (
	"context"
	"errors"
)

const (
	// PartitionEvidenceVersion is the signed restore evidence schema version.
	PartitionEvidenceVersion uint32 = 1
)

var (
	// ErrInvalidManifest reports a structurally or semantically invalid manifest.
	ErrInvalidManifest = errors.New("backup: invalid manifest")
	// ErrInvalidSignature reports a manifest authenticity verification failure.
	ErrInvalidSignature = errors.New("backup: invalid manifest signature")
	// ErrUnsupportedVersion reports a backup format version this binary cannot read.
	ErrUnsupportedVersion = errors.New("backup: unsupported manifest version")
	// ErrInvalidObject reports invalid backup object metadata or codec input.
	ErrInvalidObject = errors.New("backup: invalid object")
	// ErrObjectCorrupt reports a backup object authenticity or integrity failure.
	ErrObjectCorrupt = errors.New("backup: corrupt object")
	// ErrObjectExists reports an immutable repository key that already exists.
	ErrObjectExists = errors.New("backup: repository object exists")
	// ErrObjectNotFound reports a repository key that does not exist.
	ErrObjectNotFound = errors.New("backup: repository object not found")
	// ErrRepositoryIncomplete reports that not every required repository copy verified.
	ErrRepositoryIncomplete = errors.New("backup: repository copies incomplete")
	// ErrObjectLocked reports that immutable retention still forbids deletion.
	ErrObjectLocked = errors.New("backup: repository object is locked")
)

// ObjectKind identifies one logical payload in a cluster backup.
type ObjectKind string

const (
	// ObjectKindMetadata contains business and recovery-critical metadata for one hash slot.
	ObjectKindMetadata ObjectKind = "metadata"
	// ObjectKindMessages contains committed channel message segments for one hash slot.
	ObjectKindMessages ObjectKind = "messages"
	// ObjectKindErasureLedger contains permanent-erasure ledger entries.
	ObjectKindErasureLedger ObjectKind = "erasure_ledger"
	// ObjectKindChannelIndex contains per-channel committed cuts used to start
	// one continuous-capture generation without Controller per-channel state.
	ObjectKindChannelIndex ObjectKind = "channel_index"
)

// Compression identifies the compression applied before object encryption.
type Compression string

const (
	// CompressionZstd selects Zstandard compression.
	CompressionZstd Compression = "zstd"
)

// Encryption identifies the authenticated encryption used for an object.
type Encryption string

const (
	// EncryptionAES256GCM selects AES-256-GCM object encryption.
	EncryptionAES256GCM Encryption = "aes-256-gcm"
)

// PartitionCut identifies the committed logical boundary for one hash slot.
type PartitionCut struct {
	// HashSlot is the logical hash slot covered by this cut.
	HashSlot uint16 `json:"hash_slot"`
	// PhysicalSlotID identifies the Raft index space used by RaftIndex. It is
	// required for materialized continuous-capture baselines.
	PhysicalSlotID uint32 `json:"physical_slot_id,omitempty"`
	// RaftIndex is the committed Slot Raft index included by the backup.
	RaftIndex uint64 `json:"raft_index"`
	// CommittedAtMillis is the UTC commit watermark represented by this cut.
	CommittedAtMillis int64 `json:"committed_at_ms"`
}

// ObjectEntry describes one immutable encrypted backup object.
type ObjectEntry struct {
	// Key is the safe repository-relative immutable object key.
	Key string `json:"key"`
	// Kind identifies the logical payload carried by the object.
	Kind ObjectKind `json:"kind"`
	// HashSlot identifies the logical partition carried by the object.
	HashSlot uint16 `json:"hash_slot"`
	// PlaintextSHA256 authenticates the decompressed logical payload.
	PlaintextSHA256 string `json:"plaintext_sha256"`
	// CiphertextSHA256 authenticates the bytes stored in the repository.
	CiphertextSHA256 string `json:"ciphertext_sha256"`
	// PlaintextBytes is the decompressed payload size.
	PlaintextBytes int64 `json:"plaintext_bytes"`
	// CiphertextBytes is the stored encrypted object size.
	CiphertextBytes int64 `json:"ciphertext_bytes"`
	// Compression identifies the compression applied before encryption.
	Compression Compression `json:"compression"`
	// Encryption identifies the authenticated encryption algorithm.
	Encryption Encryption `json:"encryption"`
	// DataKey is the authenticated provider-neutral wrapped object key.
	DataKey DataKeyEnvelope `json:"data_key"`
	// Nonce is the base64-encoded AEAD nonce.
	Nonce string `json:"nonce"`
}

// ManifestSignature records a portable signature over canonical artifact bytes.
type ManifestSignature struct {
	// Algorithm identifies the signature algorithm used by the signer.
	Algorithm string `json:"algorithm"`
	// KeyID identifies one immutable signing-key version trusted by the authority.
	KeyID string `json:"key_id"`
	// Value contains the raw signature bytes and is base64 encoded by JSON.
	Value []byte `json:"value"`
}

// PartitionReference authenticates one immutable logical partition manifest.
type PartitionReference struct {
	// HashSlot identifies the logical partition.
	HashSlot uint16 `json:"hash_slot"`
	// Key is the immutable repository key of the partition manifest.
	Key string `json:"key"`
	// SHA256 authenticates the exact partition manifest bytes.
	SHA256 string `json:"sha256"`
	// Bytes is the partition manifest size.
	Bytes int64 `json:"bytes"`
	// ObjectCount is the number of encrypted payload objects directly referenced.
	ObjectCount uint64 `json:"object_count"`
	// CiphertextBytes is the direct encrypted payload size for this layer.
	CiphertextBytes uint64 `json:"ciphertext_bytes"`
	// Evidence carries signed cumulative restore counts and allocator fences.
	Evidence PartitionEvidence `json:"evidence"`
}

// PartitionEvidence is the cumulative semantic evidence required to restore
// one logical partition fail-closed. Channel sequence fences remain in the
// authenticated Channel index because they are keyed per Channel.
type PartitionEvidence struct {
	// Version distinguishes explicit empty evidence from a missing field.
	Version uint32 `json:"version"`
	// MetadataRecords is the entry count in the latest full metadata snapshot.
	MetadataRecords uint64 `json:"metadata_records"`
	// MessageRecords is the cumulative message-row count installed base to tip.
	MessageRecords uint64 `json:"message_records"`
	// MaxMessageID is the greatest durable message ID installed base to tip.
	MaxMessageID uint64 `json:"max_message_id"`
}

// ManifestSigner signs and verifies canonical artifact bytes through one key authority.
type ManifestSigner interface {
	// Sign signs message with the authority's active signing key.
	Sign(ctx context.Context, message []byte) (ManifestSignature, error)
	// Verify verifies signature against the exact canonical message bytes.
	Verify(ctx context.Context, signature ManifestSignature, message []byte) error
}

// DataKey contains one plaintext object key and its portable wrapped envelope.
type DataKey struct {
	// Plaintext is the short-lived AES-256 key used only during object codec work.
	Plaintext []byte
	// Envelope is the authenticated wrapped key stored with object metadata.
	Envelope DataKeyEnvelope
}

// DataKeyEnvelope carries one wrapped object key and the information required
// to select and authenticate its immutable wrapping-key version.
type DataKeyEnvelope struct {
	// Version identifies the strict portable envelope schema.
	Version uint32 `json:"version"`
	// Algorithm identifies the authenticated key-wrapping algorithm.
	Algorithm string `json:"algorithm"`
	// KeyID selects one immutable wrapping-key version.
	KeyID string `json:"key_id"`
	// Nonce is the key-wrapping AEAD nonce.
	Nonce []byte `json:"nonce"`
	// Value is the authenticated wrapped AES-256 data key.
	Value []byte `json:"value"`
}

// DataKeyManager generates and unwraps per-object data keys through one key authority.
type DataKeyManager interface {
	// NewDataKey returns a new plaintext and wrapped data-key pair.
	NewDataKey(ctx context.Context) (DataKey, error)
	// OpenDataKey authenticates and unwraps one portable data-key envelope.
	OpenDataKey(ctx context.Context, envelope DataKeyEnvelope) ([]byte, error)
}

// ObjectDescriptor identifies the logical object to compress and encrypt.
type ObjectDescriptor struct {
	// Key is the immutable repository-relative object key.
	Key string
	// Kind identifies the logical object payload.
	Kind ObjectKind
	// HashSlot identifies the logical partition carried by the object.
	HashSlot uint16
}

// StreamDescriptor identifies one logical plaintext stream before bounded chunking.
type StreamDescriptor struct {
	// Generation is the safe immutable object namespace.
	Generation string
	// HashSlot identifies the logical partition.
	HashSlot uint16
	// Kind identifies metadata, messages, or the erasure ledger.
	Kind ObjectKind
	// ShardID disambiguates independently uploaded fragments of the same
	// logical stream. Empty identifies the only fragment.
	ShardID string
}

// SealedObject contains encrypted bytes and their signed-manifest metadata.
type SealedObject struct {
	// Entry describes and authenticates Ciphertext.
	Entry ObjectEntry
	// Ciphertext is the compressed then encrypted repository payload.
	Ciphertext []byte
}
