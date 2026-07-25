package backup

import (
	"fmt"
	"sort"
	"time"

	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
)

// CheckpointRetentionDecision separates catalog references whose Generations
// remain protected from those eligible for Generation-level collection.
type CheckpointRetentionDecision struct {
	// Retain contains references whose Generations remain reachable.
	Retain []backupartifact.CatalogCheckpointReference
	// Collect contains references no longer contributing GC protection.
	Collect []backupartifact.CatalogCheckpointReference
}

// DecideCheckpointRetention applies the existing UTC recovery tiers to the
// immutable checkpoint catalog. Held and active-restore checkpoints, plus the
// newest checkpoint, are always retained.
func DecideCheckpointRetention(
	now time.Time,
	checkpoints []backupartifact.CatalogCheckpointReference,
	policy RetentionPolicy,
	activeRestoreCheckpointID string,
) (CheckpointRetentionDecision, error) {
	ordered := append([]backupartifact.CatalogCheckpointReference(nil), checkpoints...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].CreatedAtUnixMillis != ordered[j].CreatedAtUnixMillis {
			return ordered[i].CreatedAtUnixMillis > ordered[j].CreatedAtUnixMillis
		}
		return ordered[i].ID < ordered[j].ID
	})
	protected := make(map[string]struct{}, 2)
	if activeRestoreCheckpointID != "" {
		protected[activeRestoreCheckpointID] = struct{}{}
	}
	if len(ordered) > 0 {
		protected[ordered[0].ID] = struct{}{}
	}

	seen := make(map[string]struct{}, len(ordered))
	candidates := make([]retentionTierCandidate, len(ordered))
	for index, checkpoint := range ordered {
		if !validCheckpointRetentionReference(checkpoint) {
			return CheckpointRetentionDecision{}, fmt.Errorf("%w: checkpoint retention reference is invalid", ErrInvalidRequest)
		}
		if _, duplicate := seen[checkpoint.ID]; duplicate {
			return CheckpointRetentionDecision{}, fmt.Errorf("%w: duplicate checkpoint retention reference", ErrInvalidRequest)
		}
		seen[checkpoint.ID] = struct{}{}
		candidates[index] = retentionTierCandidate{
			id: checkpoint.ID, createdAtUnixMillis: checkpoint.CreatedAtUnixMillis,
			held: checkpoint.Held, monthlyEligible: true,
		}
	}
	retained, err := selectRetentionTiers(now, candidates, policy, protected)
	if err != nil {
		return CheckpointRetentionDecision{}, err
	}
	decision := CheckpointRetentionDecision{
		Retain:  make([]backupartifact.CatalogCheckpointReference, 0, len(ordered)),
		Collect: make([]backupartifact.CatalogCheckpointReference, 0, len(ordered)),
	}
	for index, checkpoint := range ordered {
		if retained[index] {
			decision.Retain = append(decision.Retain, checkpoint)
		} else {
			decision.Collect = append(decision.Collect, checkpoint)
		}
	}
	return decision, nil
}

func validCheckpointRetentionReference(reference backupartifact.CatalogCheckpointReference) bool {
	return reference.ID != "" &&
		reference.Key == backupartifact.CheckpointObjectKey(reference.ID) &&
		validRestoreDigest(reference.SHA256) &&
		reference.Bytes > 0 &&
		reference.CreatedAtUnixMillis > 0 &&
		reference.EffectiveAtUnixMillis > 0 &&
		reference.EffectiveAtUnixMillis <= reference.CreatedAtUnixMillis &&
		validRestoreDigest(reference.GenerationVector.ID) &&
		reference.GenerationVector.Key ==
			backupartifact.GenerationVectorObjectKey(reference.GenerationVector.ID) &&
		validRestoreDigest(reference.GenerationVector.SHA256) &&
		reference.GenerationVector.Bytes > 0 &&
		reference.GenerationVector.HashSlotCount > 0
}
