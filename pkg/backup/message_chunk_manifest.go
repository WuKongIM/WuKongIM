package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

const (
	messageChunkManifestFormat  = "wukongim-full-backup-message-chunks"
	messageChunkManifestVersion = uint32(1)
	// MaxSlotManifestBytes bounds both Slot manifests and their message chunk
	// index objects while allowing very large logical Slots.
	MaxSlotManifestBytes uint64 = 64 << 20
	maxMessageChunks            = 200_000
)

// MessageChunkManifest is a repository-resident index for one Channel-leader
// message stream. RPC receipts carry only its fixed-size reference.
type MessageChunkManifest struct {
	Format       string           `json:"format"`
	Version      uint32           `json:"version"`
	HashSlot     uint16           `json:"hash_slot"`
	Chunks       []ChunkReference `json:"chunks"`
	LogicalBytes uint64           `json:"logical_bytes"`
	StoredBytes  uint64           `json:"stored_bytes"`
	Records      uint64           `json:"records"`
	MaxMessageID uint64           `json:"max_message_id"`
}

// NewMessageChunkManifest builds and validates a bounded stream index.
func NewMessageChunkManifest(hashSlot uint16, chunks []ChunkReference) (MessageChunkManifest, error) {
	manifest := MessageChunkManifest{
		Format: messageChunkManifestFormat, Version: messageChunkManifestVersion,
		HashSlot: hashSlot, Chunks: append([]ChunkReference(nil), chunks...),
	}
	for _, chunk := range manifest.Chunks {
		manifest.LogicalBytes += chunk.Descriptor.LogicalBytes
		manifest.StoredBytes += chunk.Descriptor.StoredBytes
		manifest.Records += chunk.Records
		if chunk.MaxMessageID > manifest.MaxMessageID {
			manifest.MaxMessageID = chunk.MaxMessageID
		}
	}
	if err := validateMessageChunkManifest(manifest); err != nil {
		return MessageChunkManifest{}, err
	}
	return manifest, nil
}

// MarshalMessageChunkManifest encodes one canonical repository index.
func MarshalMessageChunkManifest(manifest MessageChunkManifest) ([]byte, error) {
	if err := validateMessageChunkManifest(manifest); err != nil {
		return nil, err
	}
	body, err := json.Marshal(manifest)
	if err != nil {
		return nil, fmt.Errorf("%w: encode message chunk manifest: %v", ErrInvalidManifest, err)
	}
	if uint64(len(body)) > MaxSlotManifestBytes {
		return nil, fmt.Errorf("%w: message chunk manifest exceeds limit", ErrInvalidManifest)
	}
	return body, nil
}

// LoadMessageChunkManifest validates canonical bytes.
func LoadMessageChunkManifest(body []byte) (MessageChunkManifest, error) {
	var manifest MessageChunkManifest
	if err := decodeStrictJSON(body, &manifest); err != nil {
		return MessageChunkManifest{}, fmt.Errorf("%w: decode message chunk manifest: %v", ErrInvalidManifest, err)
	}
	if err := validateMessageChunkManifest(manifest); err != nil {
		return MessageChunkManifest{}, err
	}
	canonical, err := json.Marshal(manifest)
	if err != nil || !bytes.Equal(canonical, body) {
		return MessageChunkManifest{}, fmt.Errorf("%w: message chunk manifest is not canonical", ErrInvalidManifest)
	}
	return manifest, nil
}

// LoadStoredMessageChunkManifest loads an exact immutable stream index.
func LoadStoredMessageChunkManifest(
	ctx context.Context,
	store ArchiveStore,
	backupID string,
	key string,
	expectedSHA256 string,
) (MessageChunkManifest, error) {
	if validateSHA256(expectedSHA256) != nil ||
		ValidateRepositoryKey("backups/"+backupID+"/"+key) != nil {
		return MessageChunkManifest{}, ErrInvalidObject
	}
	body, err := ReadStoredObject(
		ctx, store, "backups/"+backupID+"/"+key, MaxSlotManifestBytes,
	)
	if err != nil {
		return MessageChunkManifest{}, err
	}
	sum := sha256.Sum256(body)
	if hex.EncodeToString(sum[:]) != expectedSHA256 {
		return MessageChunkManifest{}, fmt.Errorf("%w: message chunk manifest digest", ErrObjectCorrupt)
	}
	return LoadMessageChunkManifest(body)
}

func validateMessageChunkManifest(manifest MessageChunkManifest) error {
	if manifest.Format != messageChunkManifestFormat ||
		manifest.Version != messageChunkManifestVersion ||
		int(manifest.HashSlot) >= DefaultHashSlotCount ||
		len(manifest.Chunks) == 0 ||
		len(manifest.Chunks) > maxMessageChunks {
		return fmt.Errorf("%w: invalid message chunk manifest identity", ErrInvalidManifest)
	}
	first := manifest.Chunks[0]
	if first.Kind != ChunkKindMessages || first.Sequence == 0 ||
		first.Stream == 0 || first.Part != 1 {
		return fmt.Errorf("%w: invalid first message chunk", ErrInvalidManifest)
	}
	var logicalBytes, storedBytes, records, maxMessageID uint64
	for index, chunk := range manifest.Chunks {
		if chunk.Kind != ChunkKindMessages ||
			chunk.Sequence != first.Sequence+uint32(index) ||
			chunk.Stream != first.Stream ||
			chunk.Part != uint32(index)+1 ||
			(index == len(manifest.Chunks)-1) != chunk.Final ||
			validateChunkDescriptor(chunk.Descriptor) != nil ||
			ValidateRepositoryKey(chunk.Key) != nil {
			return fmt.Errorf("%w: invalid message chunk %d", ErrInvalidManifest, index)
		}
		logicalBytes += chunk.Descriptor.LogicalBytes
		storedBytes += chunk.Descriptor.StoredBytes
		records += chunk.Records
		if chunk.MaxMessageID > maxMessageID {
			maxMessageID = chunk.MaxMessageID
		}
	}
	if manifest.LogicalBytes != logicalBytes ||
		manifest.StoredBytes != storedBytes ||
		manifest.Records != records ||
		manifest.MaxMessageID != maxMessageID {
		return fmt.Errorf("%w: message chunk totals", ErrInvalidManifest)
	}
	return nil
}
