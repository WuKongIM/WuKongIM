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
	policy CheckpointRetentionPolicy,
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

const (
	fiveMinuteRetentionWindow = 24 * time.Hour
	hourlyRetentionWindow     = 7 * 24 * time.Hour
	dailyRetentionWindow      = 30 * 24 * time.Hour
)

type retentionTierCandidate struct {
	id                  string
	createdAtUnixMillis int64
	held                bool
	monthlyEligible     bool
}

func selectRetentionTiers(
	now time.Time,
	candidates []retentionTierCandidate,
	policy CheckpointRetentionPolicy,
	protected map[string]struct{},
) ([]bool, error) {
	if policy.MonthlyMonths < 0 || policy.MonthlyMonths > 120 {
		return nil, fmt.Errorf(
			"%w: monthly retention months must be between 0 and 120",
			ErrInvalidRequest,
		)
	}
	hourBuckets := make(map[string]struct{})
	dayBuckets := make(map[string]struct{})
	monthBuckets := make(map[string]struct{})
	retained := make([]bool, len(candidates))
	now = now.UTC()
	monthlyCutoff := now.AddDate(0, -policy.MonthlyMonths, 0)
	for index, candidate := range candidates {
		created := time.UnixMilli(candidate.createdAtUnixMillis).UTC()
		age := now.Sub(created)
		_, explicitlyProtected := protected[candidate.id]
		retain := candidate.held || explicitlyProtected || age < 0
		switch {
		case retain:
		case age <= fiveMinuteRetentionWindow:
			retain = true
		case age <= hourlyRetentionWindow:
			bucket := created.Format("2006-01-02T15")
			if _, exists := hourBuckets[bucket]; !exists {
				hourBuckets[bucket] = struct{}{}
				retain = true
			}
		case age <= dailyRetentionWindow:
			bucket := created.Format("2006-01-02")
			if _, exists := dayBuckets[bucket]; !exists {
				dayBuckets[bucket] = struct{}{}
				retain = true
			}
		case policy.MonthlyMonths > 0 &&
			candidate.monthlyEligible &&
			!created.Before(monthlyCutoff):
			bucket := created.Format("2006-01")
			if _, exists := monthBuckets[bucket]; !exists {
				monthBuckets[bucket] = struct{}{}
				retain = true
			}
		}
		retained[index] = retain
	}
	return retained, nil
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
