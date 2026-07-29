package backup

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

const (
	// SlotManifestFormat identifies one logical Hash Slot archive manifest.
	SlotManifestFormat = "wukongim-full-backup-slot"
	// SlotManifestVersion is the supported Slot manifest schema.
	SlotManifestVersion uint32 = 1
)

// ChunkKind identifies one logical stream within a Hash Slot archive.
type ChunkKind string

const (
	// ChunkKindMetadata contains Slot-owned business metadata.
	ChunkKindMetadata ChunkKind = "metadata"
	// ChunkKindMessages contains committed Slot-owned message state.
	ChunkKindMessages ChunkKind = "messages"
)

// SlotCut identifies the stable authority point captured for one Hash Slot.
type SlotCut struct {
	PhysicalSlotID       uint32 `json:"physical_slot_id"`
	LeaderTerm           uint64 `json:"leader_term"`
	AppliedTerm          uint64 `json:"applied_term"`
	ConfigurationVersion uint64 `json:"configuration_version"`
	AppliedIndex         uint64 `json:"applied_index"`
	CapturedAtUnixMillis int64  `json:"captured_at_unix_ms"`
}

// ChunkReference binds one ordered logical stream part to its digest and
// semantic record summary.
type ChunkReference struct {
	Kind         ChunkKind       `json:"kind"`
	Sequence     uint32          `json:"sequence"`
	Stream       uint32          `json:"stream"`
	Part         uint32          `json:"part"`
	Final        bool            `json:"final"`
	Key          string          `json:"key"`
	Descriptor   ChunkDescriptor `json:"descriptor"`
	Records      uint64          `json:"records"`
	MaxMessageID uint64          `json:"max_message_id"`
}

// SlotManifest is the complete portable description of one Hash Slot.
type SlotManifest struct {
	Format       string           `json:"format"`
	Version      uint32           `json:"version"`
	HashSlot     uint16           `json:"hash_slot"`
	Cut          SlotCut          `json:"cut"`
	Chunks       []ChunkReference `json:"chunks"`
	LogicalBytes uint64           `json:"logical_bytes"`
	StoredBytes  uint64           `json:"stored_bytes"`
	Records      uint64           `json:"records"`
	MaxMessageID uint64           `json:"max_message_id"`
}

// MarshalSlotManifest validates and encodes canonical Slot manifest JSON.
func MarshalSlotManifest(manifest SlotManifest) ([]byte, error) {
	if err := validateSlotManifest(manifest); err != nil {
		return nil, err
	}
	body, err := json.Marshal(manifest)
	if err != nil {
		return nil, fmt.Errorf("%w: encode Slot manifest: %v", ErrInvalidManifest, err)
	}
	if uint64(len(body)) > MaxSlotManifestBytes {
		return nil, fmt.Errorf("%w: Slot manifest exceeds limit", ErrInvalidManifest)
	}
	return body, nil
}

// LoadSlotManifest strictly decodes and validates canonical Slot manifest JSON.
func LoadSlotManifest(body []byte) (SlotManifest, error) {
	var manifest SlotManifest
	if err := decodeStrictJSON(body, &manifest); err != nil {
		return SlotManifest{}, fmt.Errorf("%w: decode Slot manifest: %v", ErrInvalidManifest, err)
	}
	if err := validateSlotManifest(manifest); err != nil {
		return SlotManifest{}, err
	}
	canonical, err := json.Marshal(manifest)
	if err != nil || !bytes.Equal(canonical, body) {
		return SlotManifest{}, fmt.Errorf("%w: Slot manifest is not canonical", ErrInvalidManifest)
	}
	return manifest, nil
}

func validateSlotManifest(manifest SlotManifest) error {
	if manifest.Format != SlotManifestFormat {
		return fmt.Errorf("%w: Slot manifest format %q", ErrInvalidManifest, manifest.Format)
	}
	if manifest.Version != SlotManifestVersion {
		return fmt.Errorf("%w: Slot manifest version %d", ErrUnsupportedVersion, manifest.Version)
	}
	if int(manifest.HashSlot) >= DefaultHashSlotCount ||
		manifest.Cut.PhysicalSlotID == 0 ||
		manifest.Cut.LeaderTerm == 0 ||
		manifest.Cut.AppliedTerm == 0 ||
		manifest.Cut.ConfigurationVersion == 0 ||
		manifest.Cut.AppliedIndex == 0 ||
		manifest.Cut.CapturedAtUnixMillis <= 0 {
		return fmt.Errorf("%w: Slot identity or cut", ErrInvalidManifest)
	}
	if len(manifest.Chunks) == 0 {
		return fmt.Errorf("%w: Slot chunks are empty", ErrInvalidManifest)
	}
	nextSequence := map[ChunkKind]uint32{
		ChunkKindMetadata: 1,
		ChunkKindMessages: 1,
	}
	nextStream := map[ChunkKind]uint32{
		ChunkKindMetadata: 0,
		ChunkKindMessages: 1,
	}
	currentPart := map[ChunkKind]uint32{}
	streamFinal := map[ChunkKind]bool{}
	lastKindOrder := 0
	var logicalBytes, storedBytes, records, maxMessageID uint64
	for index, chunk := range manifest.Chunks {
		kindOrder := 0
		keyPrefix := ""
		switch chunk.Kind {
		case ChunkKindMetadata:
			kindOrder = 1
			keyPrefix = "meta"
		case ChunkKindMessages:
			kindOrder = 2
			keyPrefix = "messages"
		default:
			return fmt.Errorf("%w: chunk[%d] kind", ErrInvalidManifest, index)
		}
		if kindOrder < lastKindOrder {
			return fmt.Errorf("%w: chunk[%d] kind order", ErrInvalidManifest, index)
		}
		lastKindOrder = kindOrder
		if chunk.Sequence != nextSequence[chunk.Kind] {
			return fmt.Errorf("%w: chunk[%d] sequence", ErrInvalidManifest, index)
		}
		nextSequence[chunk.Kind]++
		if chunk.Stream != nextStream[chunk.Kind] {
			if chunk.Stream != nextStream[chunk.Kind]+1 ||
				!streamFinal[chunk.Kind] {
				return fmt.Errorf("%w: chunk[%d] stream", ErrInvalidManifest, index)
			}
			nextStream[chunk.Kind] = chunk.Stream
			currentPart[chunk.Kind] = 0
			streamFinal[chunk.Kind] = false
		}
		if chunk.Part != currentPart[chunk.Kind]+1 || streamFinal[chunk.Kind] {
			return fmt.Errorf("%w: chunk[%d] part", ErrInvalidManifest, index)
		}
		currentPart[chunk.Kind] = chunk.Part
		streamFinal[chunk.Kind] = chunk.Final
		wantSuffix := fmt.Sprintf(
			"/%s-%06d.zst", keyPrefix, chunk.Sequence,
		)
		wantLegacyKey := fmt.Sprintf(
			"slots/%03d%s", manifest.HashSlot, wantSuffix,
		)
		attemptPrefix := fmt.Sprintf(
			"slots/%03d/attempts/", manifest.HashSlot,
		)
		if chunk.Key != wantLegacyKey &&
			(!strings.HasPrefix(chunk.Key, attemptPrefix) ||
				!strings.HasSuffix(chunk.Key, wantSuffix)) {
			return fmt.Errorf("%w: chunk[%d] key %q", ErrInvalidManifest, index, chunk.Key)
		}
		if err := validateChunkDescriptor(chunk.Descriptor); err != nil {
			return fmt.Errorf("%w: chunk[%d]: %v", ErrInvalidManifest, index, err)
		}
		if chunk.Kind == ChunkKindMetadata && chunk.MaxMessageID != 0 {
			return fmt.Errorf("%w: metadata chunk[%d] message id", ErrInvalidManifest, index)
		}
		logicalBytes += chunk.Descriptor.LogicalBytes
		storedBytes += chunk.Descriptor.StoredBytes
		records += chunk.Records
		if chunk.MaxMessageID > maxMessageID {
			maxMessageID = chunk.MaxMessageID
		}
	}
	if !streamFinal[ChunkKindMetadata] ||
		(nextSequence[ChunkKindMessages] > 1 && !streamFinal[ChunkKindMessages]) {
		return fmt.Errorf("%w: unterminated chunk stream", ErrInvalidManifest)
	}
	if manifest.LogicalBytes != logicalBytes ||
		manifest.StoredBytes != storedBytes ||
		manifest.Records != records ||
		manifest.MaxMessageID != maxMessageID {
		return fmt.Errorf("%w: Slot totals", ErrInvalidManifest)
	}
	return nil
}
