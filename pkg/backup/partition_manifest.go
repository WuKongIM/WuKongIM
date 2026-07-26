package backup

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

const (
	// PartitionManifestFormat identifies one logical hash-slot backup manifest.
	PartitionManifestFormat = "wukongim-cluster-backup-partition"
	// PartitionManifestVersion is the current partition manifest schema version.
	PartitionManifestVersion  uint32 = 3
	maxPartitionManifestBytes        = 16 << 20
)

// PartitionManifest is the immutable bounded index produced by one logical partition worker.
type PartitionManifest struct {
	// Format must equal PartitionManifestFormat.
	Format string `json:"format"`
	// Version selects the partition manifest compatibility contract.
	Version uint32 `json:"version"`
	// Generation identifies the continuous-capture generation this materialized
	// baseline starts.
	Generation string `json:"generation"`
	// RebaseEpoch fences retries from older generation-rebase attempts.
	RebaseEpoch uint64 `json:"rebase_epoch"`
	// Cut is the committed logical boundary represented by all objects.
	Cut PartitionCut `json:"cut"`
	// BaselineCursor authenticates the complete continuous message cursor
	// captured with this materialized rebase.
	BaselineCursor *SegmentReference `json:"baseline_cursor,omitempty"`
	// Evidence carries cumulative signed counts and the message-ID fence.
	Evidence PartitionEvidence `json:"evidence"`
	// Objects contains encrypted immutable chunks in repository-key order.
	Objects []ObjectEntry `json:"objects"`
}

// MarshalPartitionManifest validates and serializes one partition manifest.
func MarshalPartitionManifest(manifest PartitionManifest) ([]byte, error) {
	if err := validatePartitionManifest(manifest); err != nil {
		return nil, err
	}
	body, err := json.Marshal(manifest)
	if err != nil {
		return nil, fmt.Errorf("marshal partition manifest: %w", err)
	}
	if len(body) > maxPartitionManifestBytes {
		return nil, fmt.Errorf("%w: partition manifest exceeds size limit", ErrInvalidManifest)
	}
	return body, nil
}

// LoadPartitionManifest strictly decodes and validates one partition manifest.
func LoadPartitionManifest(body []byte) (PartitionManifest, error) {
	if len(body) == 0 || len(body) > maxPartitionManifestBytes {
		return PartitionManifest{}, fmt.Errorf("%w: partition manifest size is invalid", ErrInvalidManifest)
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var manifest PartitionManifest
	if err := decoder.Decode(&manifest); err != nil {
		return PartitionManifest{}, fmt.Errorf("%w: decode partition manifest: %v", ErrInvalidManifest, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return PartitionManifest{}, fmt.Errorf("%w: trailing partition manifest data", ErrInvalidManifest)
	}
	if err := validatePartitionManifest(manifest); err != nil {
		return PartitionManifest{}, err
	}
	return manifest, nil
}

func validatePartitionManifest(manifest PartitionManifest) error {
	if manifest.Format != PartitionManifestFormat {
		return fmt.Errorf("%w: partition format %q", ErrInvalidManifest, manifest.Format)
	}
	if manifest.Version != PartitionManifestVersion {
		return fmt.Errorf("%w: partition version %d: %w", ErrInvalidManifest, manifest.Version, ErrUnsupportedVersion)
	}
	if err := validateBackupIdentity(manifest.Generation); err != nil {
		return fmt.Errorf("%w: partition generation: %v", ErrInvalidManifest, err)
	}
	if manifest.RebaseEpoch == 0 || manifest.Cut.RaftIndex == 0 ||
		manifest.Cut.CommittedAtMillis <= 0 {
		return fmt.Errorf(
			"%w: partition rebase epoch and cut must be positive",
			ErrInvalidManifest,
		)
	}
	if err := validatePartitionEvidence(manifest.Evidence); err != nil {
		return err
	}
	if manifest.BaselineCursor != nil {
		if manifest.Cut.PhysicalSlotID == 0 ||
			validateSegmentReference(*manifest.BaselineCursor) != nil {
			return fmt.Errorf("%w: partition baseline cursor is invalid", ErrInvalidManifest)
		}
	}
	if len(manifest.Objects) == 0 {
		return fmt.Errorf("%w: partition objects are required", ErrInvalidManifest)
	}
	metadataPresent := false
	previousKey := ""
	for index, object := range manifest.Objects {
		if object.HashSlot != manifest.Cut.HashSlot {
			return fmt.Errorf("%w: partition object[%d] hash slot mismatch", ErrInvalidManifest, index)
		}
		if previousKey != "" && object.Key <= previousKey {
			return fmt.Errorf("%w: partition object keys must be unique and sorted", ErrInvalidManifest)
		}
		previousKey = object.Key
		if err := validateObjectEntry(object, index); err != nil {
			return err
		}
		if object.Kind == ObjectKindMetadata {
			metadataPresent = true
		}
	}
	if !metadataPresent {
		return fmt.Errorf("%w: partition metadata object is required", ErrInvalidManifest)
	}
	return nil
}

func validatePartitionEvidence(evidence PartitionEvidence) error {
	if evidence.Version != PartitionEvidenceVersion {
		return fmt.Errorf("%w: partition evidence version %d is unsupported", ErrInvalidManifest, evidence.Version)
	}
	if (evidence.MessageRecords == 0) != (evidence.MaxMessageID == 0) {
		return fmt.Errorf("%w: message record evidence and allocator fence disagree", ErrInvalidManifest)
	}
	return nil
}
