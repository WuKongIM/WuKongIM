package backup

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"strings"
)

const (
	// ArchiveFormat identifies the scheduled self-contained full-backup format.
	ArchiveFormat = "wukongim-full-backup"
	// ArchiveVersion is the only archive format version supported by this binary.
	ArchiveVersion uint32 = 1
	// CompleteMarkerFormat identifies the publication marker for one archive.
	CompleteMarkerFormat = "wukongim-full-backup-complete"
	// CompleteMarkerVersion is the publication-marker schema version.
	CompleteMarkerVersion uint32 = 1
	// DefaultHashSlotCount is the required logical partition count.
	DefaultHashSlotCount = 256
)

// Trigger identifies why a full-backup job was admitted.
type Trigger string

const (
	// TriggerInitial is the first backup created when a plan is enabled.
	TriggerInitial Trigger = "initial"
	// TriggerScheduled is a Cron-created backup.
	TriggerScheduled Trigger = "scheduled"
	// TriggerManual is an administrator-created backup.
	TriggerManual Trigger = "manual"
)

// Checksum identifies the digest applied to stored archive bytes.
type Checksum string

const (
	// ChecksumSHA256 selects SHA-256.
	ChecksumSHA256 Checksum = "sha256"
)

// ArchiveManifest is the bounded top-level description of one independent
// cluster backup. Per-channel details stay in Slot artifacts.
type ArchiveManifest struct {
	Format                string          `json:"format"`
	Version               uint32          `json:"version"`
	ID                    string          `json:"id"`
	Trigger               Trigger         `json:"trigger"`
	SourceClusterID       string          `json:"source_cluster_id"`
	SourceApplication     string          `json:"source_application"`
	HashSlotCount         int             `json:"hash_slot_count"`
	StartedAtUnixMillis   int64           `json:"started_at_unix_ms"`
	CompletedAtUnixMillis int64           `json:"completed_at_unix_ms"`
	CutStartedUnixMillis  int64           `json:"cut_started_at_unix_ms"`
	CutEndedUnixMillis    int64           `json:"cut_ended_at_unix_ms"`
	Compression           Compression     `json:"compression"`
	Checksum              Checksum        `json:"checksum"`
	LogicalBytes          uint64          `json:"logical_bytes"`
	StoredBytes           uint64          `json:"stored_bytes"`
	Records               uint64          `json:"records"`
	MaxMessageID          uint64          `json:"max_message_id"`
	Slots                 []SlotReference `json:"slots"`
}

// SlotReference binds one logical Hash Slot to its immutable manifest.
type SlotReference struct {
	HashSlot       uint16 `json:"hash_slot"`
	ManifestKey    string `json:"manifest_key"`
	ManifestSHA256 string `json:"manifest_sha256"`
	LogicalBytes   uint64 `json:"logical_bytes"`
	StoredBytes    uint64 `json:"stored_bytes"`
	Records        uint64 `json:"records"`
	MaxMessageID   uint64 `json:"max_message_id"`
}

// CompleteMarker makes exactly one manifest visible for restore.
type CompleteMarker struct {
	Format         string `json:"format"`
	Version        uint32 `json:"version"`
	ManifestSHA256 string `json:"manifest_sha256"`
	ManifestBytes  uint64 `json:"manifest_bytes"`
}

// MarshalArchiveManifest validates and encodes canonical v1 JSON.
func MarshalArchiveManifest(manifest ArchiveManifest) ([]byte, error) {
	if err := validateArchiveManifest(manifest); err != nil {
		return nil, err
	}
	body, err := json.Marshal(manifest)
	if err != nil {
		return nil, fmt.Errorf("%w: encode archive manifest: %v", ErrInvalidManifest, err)
	}
	return body, nil
}

// LoadArchiveManifest strictly decodes and validates canonical v1 JSON.
func LoadArchiveManifest(body []byte) (ArchiveManifest, error) {
	var manifest ArchiveManifest
	if err := decodeStrictJSON(body, &manifest); err != nil {
		return ArchiveManifest{}, fmt.Errorf("%w: decode archive manifest: %v", ErrInvalidManifest, err)
	}
	if err := validateArchiveManifest(manifest); err != nil {
		return ArchiveManifest{}, err
	}
	canonical, err := json.Marshal(manifest)
	if err != nil || !bytes.Equal(canonical, body) {
		return ArchiveManifest{}, fmt.Errorf("%w: archive manifest is not canonical", ErrInvalidManifest)
	}
	return manifest, nil
}

// NewCompleteMarker binds publication to the exact canonical manifest bytes.
func NewCompleteMarker(manifestBody []byte) (CompleteMarker, error) {
	if _, err := LoadArchiveManifest(manifestBody); err != nil {
		return CompleteMarker{}, err
	}
	sum := sha256.Sum256(manifestBody)
	return CompleteMarker{
		Format:         CompleteMarkerFormat,
		Version:        CompleteMarkerVersion,
		ManifestSHA256: hex.EncodeToString(sum[:]),
		ManifestBytes:  uint64(len(manifestBody)),
	}, nil
}

// MarshalCompleteMarker validates and encodes one publication marker.
func MarshalCompleteMarker(marker CompleteMarker) ([]byte, error) {
	if err := validateCompleteMarker(marker); err != nil {
		return nil, err
	}
	body, err := json.Marshal(marker)
	if err != nil {
		return nil, fmt.Errorf("%w: encode complete marker: %v", ErrInvalidManifest, err)
	}
	return body, nil
}

// LoadCompleteMarker verifies that markerBody publishes manifestBody exactly.
func LoadCompleteMarker(markerBody, manifestBody []byte) (CompleteMarker, error) {
	var marker CompleteMarker
	if err := decodeStrictJSON(markerBody, &marker); err != nil {
		return CompleteMarker{}, fmt.Errorf("%w: decode complete marker: %v", ErrInvalidManifest, err)
	}
	if err := validateCompleteMarker(marker); err != nil {
		return CompleteMarker{}, err
	}
	canonical, err := json.Marshal(marker)
	if err != nil || !bytes.Equal(canonical, markerBody) {
		return CompleteMarker{}, fmt.Errorf("%w: complete marker is not canonical", ErrInvalidManifest)
	}
	if marker.ManifestBytes != uint64(len(manifestBody)) {
		return CompleteMarker{}, fmt.Errorf("%w: complete marker manifest size", ErrObjectCorrupt)
	}
	sum := sha256.Sum256(manifestBody)
	if marker.ManifestSHA256 != hex.EncodeToString(sum[:]) {
		return CompleteMarker{}, fmt.Errorf("%w: complete marker manifest digest", ErrObjectCorrupt)
	}
	if _, err := LoadArchiveManifest(manifestBody); err != nil {
		return CompleteMarker{}, err
	}
	return marker, nil
}

func validateArchiveManifest(manifest ArchiveManifest) error {
	if manifest.Format != ArchiveFormat {
		return fmt.Errorf("%w: archive format %q", ErrInvalidManifest, manifest.Format)
	}
	if manifest.Version != ArchiveVersion {
		return fmt.Errorf("%w: archive version %d", ErrUnsupportedVersion, manifest.Version)
	}
	if err := validateBackupIdentity(manifest.ID); err != nil {
		return fmt.Errorf("%w: archive id: %v", ErrInvalidManifest, err)
	}
	if err := validateBackupIdentity(manifest.SourceClusterID); err != nil {
		return fmt.Errorf("%w: source cluster id: %v", ErrInvalidManifest, err)
	}
	if manifest.SourceApplication == "" || len(manifest.SourceApplication) > 128 {
		return fmt.Errorf("%w: source application", ErrInvalidManifest)
	}
	switch manifest.Trigger {
	case TriggerInitial, TriggerScheduled, TriggerManual:
	default:
		return fmt.Errorf("%w: archive trigger %q", ErrInvalidManifest, manifest.Trigger)
	}
	if manifest.HashSlotCount != DefaultHashSlotCount ||
		len(manifest.Slots) != DefaultHashSlotCount {
		return fmt.Errorf("%w: archive requires %d Hash Slots", ErrInvalidManifest, DefaultHashSlotCount)
	}
	if manifest.StartedAtUnixMillis <= 0 ||
		manifest.CompletedAtUnixMillis < manifest.StartedAtUnixMillis ||
		manifest.CutStartedUnixMillis < manifest.StartedAtUnixMillis ||
		manifest.CutEndedUnixMillis < manifest.CutStartedUnixMillis ||
		manifest.CutEndedUnixMillis > manifest.CompletedAtUnixMillis {
		return fmt.Errorf("%w: archive timestamps", ErrInvalidManifest)
	}
	if manifest.Compression != CompressionZstd || manifest.Checksum != ChecksumSHA256 {
		return fmt.Errorf("%w: archive codec", ErrInvalidManifest)
	}
	seen := make([]bool, DefaultHashSlotCount)
	var logicalBytes, storedBytes, records, maxMessageID uint64
	for index, slot := range manifest.Slots {
		if int(slot.HashSlot) >= DefaultHashSlotCount || seen[slot.HashSlot] {
			return fmt.Errorf("%w: slot reference[%d]", ErrInvalidManifest, index)
		}
		seen[slot.HashSlot] = true
		if err := validateSlotManifestKey(slot.HashSlot, slot.ManifestKey); err != nil {
			return fmt.Errorf("%w: slot reference[%d]: %v", ErrInvalidManifest, index, err)
		}
		if err := validateSHA256(slot.ManifestSHA256); err != nil {
			return fmt.Errorf("%w: slot reference[%d] digest: %v", ErrInvalidManifest, index, err)
		}
		logicalBytes += slot.LogicalBytes
		storedBytes += slot.StoredBytes
		records += slot.Records
		if slot.MaxMessageID > maxMessageID {
			maxMessageID = slot.MaxMessageID
		}
	}
	for slot, present := range seen {
		if !present {
			return fmt.Errorf("%w: missing Hash Slot %d", ErrInvalidManifest, slot)
		}
	}
	if manifest.LogicalBytes != 0 && manifest.LogicalBytes != logicalBytes {
		return fmt.Errorf("%w: logical byte total", ErrInvalidManifest)
	}
	if manifest.StoredBytes != 0 && manifest.StoredBytes != storedBytes {
		return fmt.Errorf("%w: stored byte total", ErrInvalidManifest)
	}
	if manifest.Records != 0 && manifest.Records != records {
		return fmt.Errorf("%w: record total", ErrInvalidManifest)
	}
	if manifest.MaxMessageID != 0 && manifest.MaxMessageID != maxMessageID {
		return fmt.Errorf("%w: message id high-water mark", ErrInvalidManifest)
	}
	return nil
}

func validateSlotManifestKey(hashSlot uint16, key string) error {
	if err := validateRepositoryKey(key); err != nil {
		return err
	}
	wantPrefix := fmt.Sprintf("slots/%03d/", hashSlot)
	if !strings.HasPrefix(key, wantPrefix) || path.Base(key) != "manifest.json" {
		return fmt.Errorf("key %q does not identify Hash Slot %d manifest", key, hashSlot)
	}
	return nil
}

func validateCompleteMarker(marker CompleteMarker) error {
	if marker.Format != CompleteMarkerFormat || marker.Version != CompleteMarkerVersion ||
		marker.ManifestBytes == 0 {
		return fmt.Errorf("%w: complete marker", ErrInvalidManifest)
	}
	if err := validateSHA256(marker.ManifestSHA256); err != nil {
		return fmt.Errorf("%w: complete marker digest: %v", ErrInvalidManifest, err)
	}
	return nil
}

func decodeStrictJSON(body []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return fmt.Errorf("multiple JSON values")
		}
		return err
	}
	return nil
}
