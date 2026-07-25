package backup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

const (
	// CheckpointFormat identifies one immutable continuous-backup vector cut.
	CheckpointFormat = "wukongim-backup-checkpoint"
	// CheckpointVersion is the current vector-cut schema.
	CheckpointVersion uint16 = 3
	// CatalogPageFormat identifies one signed hash-linked catalog page.
	CatalogPageFormat = "wukongim-backup-catalog-page"
	// CatalogPageVersion is the current catalog schema.
	CatalogPageVersion uint16 = 1

	maxCheckpointBytes  = 4 << 20
	maxCatalogPageBytes = 1 << 20
	maxCatalogEntries   = 256
)

// CheckpointStream is one stream head frozen into a checkpoint.
type CheckpointStream struct {
	// Sequence is the latest committed segment sequence, or zero for an empty stream.
	Sequence uint64 `json:"sequence"`
	// Head authenticates the latest payload segment, or is nil for an empty stream.
	Head *SegmentReference `json:"head,omitempty"`
	// CursorHead authenticates the latest message cursor sidecar.
	CursorHead *SegmentReference `json:"cursor_head,omitempty"`
	// SourceHighWatermark is the greatest fully reconciled source position.
	SourceHighWatermark uint64 `json:"source_high_watermark"`
	// WatermarkAtUnixMillis is the UTC source time represented by the stream.
	WatermarkAtUnixMillis int64 `json:"watermark_at_unix_millis"`
}

// CheckpointBaseline authenticates the materialized root of one Slot generation.
type CheckpointBaseline struct {
	// Partition is the materialized metadata/message snapshot manifest.
	Partition PartitionReference `json:"partition"`
	// MessageCursor is the complete Channel boundary index at the same cut.
	MessageCursor SegmentReference `json:"message_cursor"`
}

// CheckpointSlot is one complete Slot generation cut.
type CheckpointSlot struct {
	// HashSlot identifies the logical partition.
	HashSlot uint16 `json:"hash_slot"`
	// Generation identifies the independently replaceable segment graph.
	Generation string `json:"generation"`
	// Baseline is present when this generation starts from materialized state.
	Baseline *CheckpointBaseline `json:"baseline,omitempty"`
	// Metadata and Messages are the independently ordered stream cuts.
	Metadata CheckpointStream `json:"metadata"`
	Messages CheckpointStream `json:"messages"`
	// WatermarkAtUnixMillis is the older stream watermark.
	WatermarkAtUnixMillis int64 `json:"watermark_at_unix_millis"`
}

// Checkpoint is one signed vector cut covering every configured Hash Slot.
type Checkpoint struct {
	// Format and Version identify the portable checkpoint schema.
	Format  string `json:"format"`
	Version uint16 `json:"version"`
	// ID uniquely identifies this immutable recovery cut.
	ID string `json:"id"`
	// RepositoryID identifies the logical repository shared by both copies.
	RepositoryID string `json:"repository_id"`
	// SourceClusterID and SourceGeneration fence the captured source.
	SourceClusterID  string `json:"source_cluster_id"`
	SourceGeneration string `json:"source_generation"`
	// HashSlotCount is the exact required vector width.
	HashSlotCount uint16 `json:"hash_slot_count"`
	// CreatedAtUnixMillis is the UTC publication time.
	CreatedAtUnixMillis int64 `json:"created_at_unix_millis"`
	// EffectiveAtUnixMillis is the oldest included Slot watermark.
	EffectiveAtUnixMillis int64 `json:"effective_at_unix_millis"`
	// Slots contains exactly one entry per configured Hash Slot in ascending order.
	Slots []CheckpointSlot `json:"slots"`
	// ErasureHeads freezes the authenticated permanent-erasure prefix visible
	// when this checkpoint was published. Entries are unique and sorted by Hash Slot.
	ErasureHeads []ErasureStreamHead `json:"erasure_heads,omitempty"`
	// Signature authenticates the canonical unsigned checkpoint.
	Signature *ManifestSignature `json:"signature,omitempty"`
}

// CatalogCheckpointReference authenticates one immutable checkpoint object.
type CatalogCheckpointReference struct {
	// ID identifies the checkpoint.
	ID string `json:"id"`
	// Key locates the immutable signed checkpoint.
	Key string `json:"key"`
	// SHA256 and Bytes authenticate the exact stored bytes.
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
	// CreatedAtUnixMillis and EffectiveAtUnixMillis support bounded catalog queries.
	CreatedAtUnixMillis   int64 `json:"created_at_unix_millis"`
	EffectiveAtUnixMillis int64 `json:"effective_at_unix_millis"`
	// Held is the latest immutable catalog decision for operator retention.
	Held bool `json:"held"`
	// StateOnly distinguishes a hold/release append from the first publication
	// of this immutable checkpoint. It prevents delta consumers from treating
	// retention decisions as new backup content.
	StateOnly bool `json:"state_only,omitempty"`
	// GenerationVector authenticates the content-addressed complete Slot map.
	GenerationVector GenerationVectorReference `json:"generation_vector"`
}

// CatalogPageReference authenticates one catalog head without loading history.
type CatalogPageReference struct {
	// Sequence is the monotonically increasing page position.
	Sequence uint64 `json:"sequence"`
	// Key locates the immutable signed page.
	Key string `json:"key"`
	// SHA256 and Bytes authenticate the exact stored bytes.
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
	// LatestCheckpointID identifies the checkpoint state appended on this page.
	LatestCheckpointID string `json:"latest_checkpoint_id"`
}

// CheckpointCatalogCommit contains one newly replicated checkpoint and the
// immutable catalog page that makes it reachable.
type CheckpointCatalogCommit struct {
	// Checkpoint authenticates the newly replicated checkpoint object.
	Checkpoint CatalogCheckpointReference
	// Head authenticates the newly replicated catalog page.
	Head CatalogPageReference
}

// CatalogPage is one bounded signed append in the immutable catalog.
type CatalogPage struct {
	// Format and Version identify the portable catalog-page schema.
	Format  string `json:"format"`
	Version uint16 `json:"version"`
	// Sequence is the monotonically increasing page position.
	Sequence uint64 `json:"sequence"`
	// CreatedAtUnixMillis is the UTC page creation time.
	CreatedAtUnixMillis int64 `json:"created_at_unix_millis"`
	// Previous authenticates the preceding page; nil identifies the first page.
	Previous *CatalogPageReference `json:"previous,omitempty"`
	// Entries are ordered newest first and bounded per page.
	Entries []CatalogCheckpointReference `json:"entries"`
	// Signature authenticates the canonical unsigned page including its hash link.
	Signature *ManifestSignature `json:"signature,omitempty"`
}

// SignCheckpoint validates and signs one detached checkpoint.
func SignCheckpoint(ctx context.Context, checkpoint Checkpoint, signer ManifestSigner, keyID string) (Checkpoint, error) {
	checkpoint.Signature = nil
	body, err := canonicalCheckpoint(checkpoint)
	if err != nil || signer == nil || strings.TrimSpace(keyID) == "" {
		if err != nil {
			return Checkpoint{}, err
		}
		return Checkpoint{}, fmt.Errorf("%w: checkpoint signer is required", ErrInvalidSignature)
	}
	signature, err := signer.Sign(ctx, keyID, body)
	if err != nil {
		return Checkpoint{}, fmt.Errorf("%w: sign checkpoint: %v", ErrInvalidSignature, err)
	}
	if signature.KeyID != keyID || strings.TrimSpace(signature.Algorithm) == "" || len(signature.Value) == 0 {
		return Checkpoint{}, fmt.Errorf("%w: checkpoint signer metadata mismatch", ErrInvalidSignature)
	}
	checkpoint.Signature = &signature
	return checkpoint, validateCheckpoint(checkpoint, true)
}

// MarshalCheckpoint serializes one signed checkpoint.
func MarshalCheckpoint(checkpoint Checkpoint) ([]byte, error) {
	if err := validateCheckpoint(checkpoint, true); err != nil {
		return nil, err
	}
	body, err := json.Marshal(checkpoint)
	if err != nil || len(body) > maxCheckpointBytes {
		return nil, fmt.Errorf("%w: checkpoint exceeds encoding limit", ErrInvalidObject)
	}
	return body, nil
}

// LoadCheckpoint strictly decodes and verifies one checkpoint.
func LoadCheckpoint(ctx context.Context, body []byte, signer ManifestSigner) (Checkpoint, error) {
	var checkpoint Checkpoint
	if signer == nil || strictCheckpointJSON(body, maxCheckpointBytes, &checkpoint) != nil {
		return Checkpoint{}, fmt.Errorf("%w: checkpoint encoding is invalid", ErrInvalidObject)
	}
	if err := validateCheckpoint(checkpoint, true); err != nil {
		return Checkpoint{}, err
	}
	signature := *checkpoint.Signature
	canonical, err := canonicalCheckpoint(checkpoint)
	if err != nil {
		return Checkpoint{}, err
	}
	if err := signer.Verify(ctx, signature, canonical); err != nil {
		return Checkpoint{}, fmt.Errorf("%w: verify checkpoint: %v", ErrInvalidSignature, err)
	}
	return checkpoint, nil
}

// SignCatalogPage validates and signs one detached catalog append.
func SignCatalogPage(ctx context.Context, page CatalogPage, signer ManifestSigner, keyID string) (CatalogPage, error) {
	page.Signature = nil
	body, err := canonicalCatalogPage(page)
	if err != nil || signer == nil || strings.TrimSpace(keyID) == "" {
		if err != nil {
			return CatalogPage{}, err
		}
		return CatalogPage{}, fmt.Errorf("%w: catalog signer is required", ErrInvalidSignature)
	}
	signature, err := signer.Sign(ctx, keyID, body)
	if err != nil {
		return CatalogPage{}, fmt.Errorf("%w: sign catalog page: %v", ErrInvalidSignature, err)
	}
	if signature.KeyID != keyID || strings.TrimSpace(signature.Algorithm) == "" || len(signature.Value) == 0 {
		return CatalogPage{}, fmt.Errorf("%w: catalog signer metadata mismatch", ErrInvalidSignature)
	}
	page.Signature = &signature
	return page, validateCatalogPage(page, true)
}

// MarshalCatalogPage serializes one signed catalog page.
func MarshalCatalogPage(page CatalogPage) ([]byte, error) {
	if err := validateCatalogPage(page, true); err != nil {
		return nil, err
	}
	body, err := json.Marshal(page)
	if err != nil || len(body) > maxCatalogPageBytes {
		return nil, fmt.Errorf("%w: catalog page exceeds encoding limit", ErrInvalidObject)
	}
	return body, nil
}

// LoadCatalogPage strictly decodes and verifies one catalog page.
func LoadCatalogPage(ctx context.Context, body []byte, signer ManifestSigner) (CatalogPage, error) {
	var page CatalogPage
	if signer == nil || strictCheckpointJSON(body, maxCatalogPageBytes, &page) != nil {
		return CatalogPage{}, fmt.Errorf("%w: catalog page encoding is invalid", ErrInvalidObject)
	}
	if err := validateCatalogPage(page, true); err != nil {
		return CatalogPage{}, err
	}
	signature := *page.Signature
	canonical, err := canonicalCatalogPage(page)
	if err != nil {
		return CatalogPage{}, err
	}
	if err := signer.Verify(ctx, signature, canonical); err != nil {
		return CatalogPage{}, fmt.Errorf("%w: verify catalog page: %v", ErrInvalidSignature, err)
	}
	return page, nil
}

func canonicalCheckpoint(checkpoint Checkpoint) ([]byte, error) {
	checkpoint.Signature = nil
	if err := validateCheckpoint(checkpoint, false); err != nil {
		return nil, err
	}
	return json.Marshal(checkpoint)
}

func validateCheckpoint(checkpoint Checkpoint, requireSignature bool) error {
	if checkpoint.Format != CheckpointFormat || checkpoint.Version != CheckpointVersion ||
		validateRestorePointID(checkpoint.ID) != nil ||
		validateRestorePointID(checkpoint.RepositoryID) != nil ||
		validateRestorePointID(checkpoint.SourceClusterID) != nil ||
		validateRestorePointID(checkpoint.SourceGeneration) != nil ||
		checkpoint.HashSlotCount == 0 || checkpoint.CreatedAtUnixMillis <= 0 ||
		checkpoint.EffectiveAtUnixMillis <= 0 ||
		len(checkpoint.Slots) != int(checkpoint.HashSlotCount) {
		return fmt.Errorf("%w: checkpoint identity or coverage is invalid", ErrInvalidObject)
	}
	var erasureEventCount uint64
	for index, head := range checkpoint.ErasureHeads {
		if head.HashSlot >= checkpoint.HashSlotCount ||
			(index > 0 && checkpoint.ErasureHeads[index-1].HashSlot >= head.HashSlot) ||
			ValidateErasureStreamHead(head) != nil ||
			head.Sequence > uint64(MaxErasureLedgerEvents)-erasureEventCount {
			return fmt.Errorf("%w: checkpoint erasure stream head is invalid", ErrInvalidObject)
		}
		erasureEventCount += head.Sequence
	}
	var effective int64
	for index, slot := range checkpoint.Slots {
		if slot.HashSlot != uint16(index) || validateRestorePointID(slot.Generation) != nil ||
			validateCheckpointBaseline(slot.Baseline, slot.HashSlot) != nil ||
			slot.WatermarkAtUnixMillis <= 0 ||
			slot.WatermarkAtUnixMillis != olderCheckpointTime(
				slot.Metadata.WatermarkAtUnixMillis,
				slot.Messages.WatermarkAtUnixMillis,
			) ||
			validateCheckpointStream(slot.Metadata, false) != nil ||
			validateCheckpointStream(slot.Messages, true) != nil {
			return fmt.Errorf("%w: checkpoint Slot[%d] is invalid", ErrInvalidObject, index)
		}
		effective = olderCheckpointTime(effective, slot.WatermarkAtUnixMillis)
	}
	if effective != checkpoint.EffectiveAtUnixMillis {
		return fmt.Errorf("%w: checkpoint effective time is invalid", ErrInvalidObject)
	}
	return validateCheckpointSignature(checkpoint.Signature, requireSignature)
}

func validateCheckpointBaseline(baseline *CheckpointBaseline, hashSlot uint16) error {
	if baseline == nil {
		return nil
	}
	partition := baseline.Partition
	if partition.HashSlot != hashSlot || partition.Bytes <= 0 ||
		partition.ObjectCount == 0 || partition.CiphertextBytes == 0 ||
		validatePartitionManifestKey(partition.Key) != nil ||
		validateSHA256(partition.SHA256) != nil ||
		validatePartitionEvidence(partition.Evidence) != nil ||
		validateSegmentReference(baseline.MessageCursor) != nil {
		return ErrInvalidObject
	}
	return nil
}

func validateCheckpointStream(stream CheckpointStream, message bool) error {
	if stream.WatermarkAtUnixMillis <= 0 {
		return ErrInvalidObject
	}
	if stream.Sequence == 0 {
		if stream.Head != nil || stream.CursorHead != nil {
			return ErrInvalidObject
		}
		return nil
	}
	if stream.Head == nil || stream.SourceHighWatermark == 0 ||
		validateSegmentReference(*stream.Head) != nil {
		return ErrInvalidObject
	}
	if message {
		if stream.CursorHead == nil || validateSegmentReference(*stream.CursorHead) != nil {
			return ErrInvalidObject
		}
	} else if stream.CursorHead != nil {
		return ErrInvalidObject
	}
	return nil
}

func canonicalCatalogPage(page CatalogPage) ([]byte, error) {
	page.Signature = nil
	if err := validateCatalogPage(page, false); err != nil {
		return nil, err
	}
	return json.Marshal(page)
}

func validateCatalogPage(page CatalogPage, requireSignature bool) error {
	if page.Format != CatalogPageFormat || page.Version != CatalogPageVersion ||
		page.Sequence == 0 || page.CreatedAtUnixMillis <= 0 ||
		len(page.Entries) == 0 || len(page.Entries) > maxCatalogEntries {
		return fmt.Errorf("%w: catalog page identity is invalid", ErrInvalidObject)
	}
	if page.Sequence == 1 {
		if page.Previous != nil {
			return fmt.Errorf("%w: first catalog page has a predecessor", ErrInvalidObject)
		}
	} else if page.Previous == nil || page.Previous.Sequence+1 != page.Sequence ||
		validateCatalogPageReference(*page.Previous) != nil {
		return fmt.Errorf("%w: catalog predecessor is invalid", ErrInvalidObject)
	}
	for index, entry := range page.Entries {
		if index > 0 && page.Entries[index-1].CreatedAtUnixMillis <= entry.CreatedAtUnixMillis {
			return fmt.Errorf("%w: catalog entries are not newest first", ErrInvalidObject)
		}
		if validateCatalogCheckpointReference(entry) != nil {
			return fmt.Errorf("%w: catalog entry[%d] is invalid", ErrInvalidObject, index)
		}
	}
	return validateCheckpointSignature(page.Signature, requireSignature)
}

func validateCatalogCheckpointReference(reference CatalogCheckpointReference) error {
	if validateRestorePointID(reference.ID) != nil ||
		reference.Key != CheckpointObjectKey(reference.ID) ||
		validateSHA256(reference.SHA256) != nil || reference.Bytes <= 0 ||
		reference.CreatedAtUnixMillis <= 0 || reference.EffectiveAtUnixMillis <= 0 ||
		reference.EffectiveAtUnixMillis > reference.CreatedAtUnixMillis ||
		validateGenerationVectorReference(reference.GenerationVector) != nil {
		return ErrInvalidObject
	}
	return nil
}

func validateCatalogPageReference(reference CatalogPageReference) error {
	if reference.Sequence == 0 || validateRestorePointID(reference.LatestCheckpointID) != nil ||
		reference.Key != CatalogPageObjectKey(reference.Sequence, reference.LatestCheckpointID) ||
		validateSHA256(reference.SHA256) != nil || reference.Bytes <= 0 {
		return ErrInvalidObject
	}
	return nil
}

func validateCheckpointSignature(signature *ManifestSignature, required bool) error {
	if !required {
		if signature != nil {
			return fmt.Errorf("%w: unsigned artifact contains signature", ErrInvalidObject)
		}
		return nil
	}
	if signature == nil || strings.TrimSpace(signature.KeyID) == "" ||
		strings.TrimSpace(signature.Algorithm) == "" || len(signature.Value) == 0 {
		return fmt.Errorf("%w: artifact signature is required", ErrInvalidSignature)
	}
	return nil
}

func strictCheckpointJSON(body []byte, maximum int64, target any) error {
	if len(body) == 0 || int64(len(body)) > maximum {
		return ErrInvalidObject
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return ErrInvalidObject
	}
	return nil
}

func olderCheckpointTime(left, right int64) int64 {
	if left <= 0 {
		return right
	}
	if right <= 0 || left < right {
		return left
	}
	return right
}

// CheckpointObjectKey returns the deterministic immutable checkpoint key.
func CheckpointObjectKey(id string) string {
	return "checkpoints/" + id + "/checkpoint.json"
}

// CatalogPageObjectKey returns the deterministic immutable page key.
func CatalogPageObjectKey(sequence uint64, latestCheckpointID string) string {
	return fmt.Sprintf("catalog/pages/%020d-%s.json", sequence, latestCheckpointID)
}
