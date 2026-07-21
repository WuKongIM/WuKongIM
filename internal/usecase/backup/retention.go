package backup

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
)

const (
	fiveMinuteRetentionWindow = 24 * time.Hour
	hourlyRetentionWindow     = 7 * 24 * time.Hour
	dailyRetentionWindow      = 30 * 24 * time.Hour
)

// DecideRetention applies the fixed UTC recovery-point tiers without mutating
// coordination state. Held points, the newest point, and explicitly protected
// base points always remain retained.
func DecideRetention(now time.Time, points []RestorePoint, policy RetentionPolicy, protectedIDs []string) (RetentionDecision, error) {
	if policy.MonthlyMonths < 0 || policy.MonthlyMonths > 120 {
		return RetentionDecision{}, fmt.Errorf("%w: monthly retention months must be between 0 and 120", ErrInvalidRequest)
	}
	ordered := append([]RestorePoint(nil), points...)
	sort.Slice(ordered, func(left, right int) bool {
		if ordered[left].EffectiveAtUnixMillis != ordered[right].EffectiveAtUnixMillis {
			return ordered[left].EffectiveAtUnixMillis > ordered[right].EffectiveAtUnixMillis
		}
		if ordered[left].CreatedAtUnixMillis != ordered[right].CreatedAtUnixMillis {
			return ordered[left].CreatedAtUnixMillis > ordered[right].CreatedAtUnixMillis
		}
		return ordered[left].ID < ordered[right].ID
	})
	protected := make(map[string]struct{}, len(protectedIDs)+1)
	for _, id := range protectedIDs {
		if id != "" {
			protected[id] = struct{}{}
		}
	}
	if len(ordered) > 0 {
		protected[ordered[0].ID] = struct{}{}
	}

	hourBuckets := make(map[string]struct{})
	dayBuckets := make(map[string]struct{})
	monthBuckets := make(map[string]struct{})
	decision := RetentionDecision{Retain: make([]RestorePoint, 0, len(ordered)), Collect: make([]RestorePoint, 0, len(ordered))}
	now = now.UTC()
	monthlyCutoff := now.AddDate(0, -policy.MonthlyMonths, 0)
	for _, point := range ordered {
		created := time.UnixMilli(point.CreatedAtUnixMillis).UTC()
		age := now.Sub(created)
		retain := point.Held || age < 0
		if _, ok := protected[point.ID]; ok {
			retain = true
		}
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
		case policy.MonthlyMonths > 0 && !created.Before(monthlyCutoff) && point.Kind == backupartifact.RestorePointMaterializedFull:
			bucket := created.Format("2006-01")
			if _, exists := monthBuckets[bucket]; !exists {
				monthBuckets[bucket] = struct{}{}
				retain = true
			}
		}
		if retain {
			decision.Retain = append(decision.Retain, point)
		} else {
			decision.Collect = append(decision.Collect, point)
		}
	}
	return decision, nil
}

// ApplyRetention atomically moves expired restore-point references into the
// durable garbage queue before repository objects may be deleted.
func (a *App) ApplyRetention(ctx context.Context, policy RetentionPolicy) (RetentionDecision, error) {
	if a == nil || !a.enabled {
		return RetentionDecision{}, ErrDisabled
	}
	for attempt := 0; attempt < maxStateRetries; attempt++ {
		state, err := a.store.Load(ctx)
		if err != nil {
			return RetentionDecision{}, err
		}
		protected := make([]string, 0, 1)
		if state.Active != nil && state.Active.BaseRestorePointID != "" {
			protected = append(protected, state.Active.BaseRestorePointID)
		}
		decision, err := DecideRetention(a.now(), state.RestorePoints, policy, protected)
		if err != nil {
			return RetentionDecision{}, err
		}
		if len(decision.Collect) == 0 {
			return decision, nil
		}
		next := state.Clone()
		next.RestorePoints = append([]RestorePoint(nil), decision.Retain...)
		next.PendingGarbage = append(next.PendingGarbage, decision.Collect...)
		if err := a.store.CompareAndSwap(ctx, state.Revision, next); err != nil {
			if errors.Is(err, ErrStateConflict) {
				continue
			}
			return RetentionDecision{}, err
		}
		return decision, nil
	}
	return RetentionDecision{}, ErrStateConflict
}

// CompleteGarbage removes one durable queue entry after both repository copies
// have completed reference-safe collection. Repeated completion is idempotent.
func (a *App) CompleteGarbage(ctx context.Context, restorePointID string) error {
	if a == nil || !a.enabled {
		return ErrDisabled
	}
	if restorePointID == "" {
		return ErrInvalidRequest
	}
	return a.mutate(ctx, func(state *State) error {
		for index, point := range state.PendingGarbage {
			if point.ID != restorePointID {
				continue
			}
			state.PendingGarbage = append(state.PendingGarbage[:index], state.PendingGarbage[index+1:]...)
			return nil
		}
		return nil
	})
}
