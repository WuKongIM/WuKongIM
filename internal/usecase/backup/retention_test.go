package backup_test

import (
	"context"
	"testing"
	"time"

	backupusecase "github.com/WuKongIM/WuKongIM/internal/usecase/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	"github.com/stretchr/testify/require"
)

func TestDecideRetentionEnforcesUTCRecoveryPointTiers(t *testing.T) {
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	points := []backupusecase.RestorePoint{
		retentionPoint("latest", now.Add(-5*time.Minute), backupartifact.RestorePointIncremental),
		retentionPoint("five-minute", now.Add(-23*time.Hour), backupartifact.RestorePointIncremental),
		retentionPoint("hour-new", now.Add(-24*time.Hour-30*time.Minute), backupartifact.RestorePointIncremental),
		retentionPoint("hour-old-same-bucket", now.Add(-24*time.Hour-50*time.Minute), backupartifact.RestorePointIncremental),
		retentionPoint("day-new", now.Add(-8*24*time.Hour), backupartifact.RestorePointSyntheticFull),
		retentionPoint("day-old-same-bucket", now.Add(-8*24*time.Hour-2*time.Hour), backupartifact.RestorePointIncremental),
		retentionPoint("monthly-new", time.Date(2026, 6, 20, 4, 0, 0, 0, time.UTC), backupartifact.RestorePointMaterializedFull),
		retentionPoint("monthly-old-same-bucket", time.Date(2026, 6, 2, 4, 0, 0, 0, time.UTC), backupartifact.RestorePointMaterializedFull),
		retentionPoint("expired", time.Date(2025, 1, 2, 4, 0, 0, 0, time.UTC), backupartifact.RestorePointMaterializedFull),
		retentionPoint("held", time.Date(2024, 1, 2, 4, 0, 0, 0, time.UTC), backupartifact.RestorePointIncremental),
		retentionPoint("active-base", time.Date(2024, 2, 2, 4, 0, 0, 0, time.UTC), backupartifact.RestorePointIncremental),
	}
	points[len(points)-2].Held = true

	decision, err := backupusecase.DecideRetention(now, points, backupusecase.RetentionPolicy{MonthlyMonths: 12}, []string{"active-base"})
	require.NoError(t, err)
	require.Equal(t, []string{
		"latest", "five-minute", "hour-new", "day-new", "monthly-new", "active-base", "held",
	}, retentionIDs(decision.Retain))
	require.Equal(t, []string{
		"hour-old-same-bucket", "day-old-same-bucket", "monthly-old-same-bucket", "expired",
	}, retentionIDs(decision.Collect))
}

func TestDecideRetentionDisablesMonthlyTierButAlwaysKeepsNewestAndHeld(t *testing.T) {
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	points := []backupusecase.RestorePoint{
		retentionPoint("newest-ancient", now.AddDate(0, -3, 0), backupartifact.RestorePointMaterializedFull),
		retentionPoint("older-held", now.AddDate(0, -4, 0), backupartifact.RestorePointIncremental),
		retentionPoint("older", now.AddDate(0, -5, 0), backupartifact.RestorePointMaterializedFull),
	}
	points[1].Held = true

	decision, err := backupusecase.DecideRetention(now, points, backupusecase.RetentionPolicy{}, nil)
	require.NoError(t, err)
	require.Equal(t, []string{"newest-ancient", "older-held"}, retentionIDs(decision.Retain))
	require.Equal(t, []string{"older"}, retentionIDs(decision.Collect))
}

func TestApplyRetentionPersistsGarbageBeforeCollectionAndProtectsActiveBase(t *testing.T) {
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	store := &memoryStateStore{state: backupusecase.State{
		Active: &backupusecase.Job{BaseRestorePointID: "active-base"},
		RestorePoints: []backupusecase.RestorePoint{
			retentionPoint("newest", now.AddDate(0, -3, 0), backupartifact.RestorePointMaterializedFull),
			retentionPoint("active-base", now.AddDate(0, -4, 0), backupartifact.RestorePointIncremental),
			retentionPoint("expired", now.AddDate(0, -5, 0), backupartifact.RestorePointMaterializedFull),
		},
	}}
	app, err := backupusecase.NewApp(backupusecase.Options{
		Enabled: true, HashSlotCount: 1, Store: store, Publisher: &recordingPublisher{},
		Now: func() time.Time { return now }, NewJobID: func() string { return "job" },
	})
	require.NoError(t, err)

	decision, err := app.ApplyRetention(context.Background(), backupusecase.RetentionPolicy{})
	require.NoError(t, err)
	require.Equal(t, []string{"newest", "active-base"}, retentionIDs(decision.Retain))
	require.Equal(t, []string{"expired"}, retentionIDs(decision.Collect))
	state, err := store.Load(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{"newest", "active-base"}, retentionIDs(state.RestorePoints))
	require.Equal(t, []string{"expired"}, retentionIDs(state.PendingGarbage))

	require.NoError(t, app.CompleteGarbage(context.Background(), "expired"))
	state, err = store.Load(context.Background())
	require.NoError(t, err)
	require.Empty(t, state.PendingGarbage)
	require.NoError(t, app.CompleteGarbage(context.Background(), "expired"))
}

func retentionPoint(id string, created time.Time, kind backupartifact.RestorePointKind) backupusecase.RestorePoint {
	return backupusecase.RestorePoint{ID: id, Kind: kind, EffectiveAtUnixMillis: created.UnixMilli(), CreatedAtUnixMillis: created.UnixMilli()}
}

func retentionIDs(points []backupusecase.RestorePoint) []string {
	ids := make([]string, len(points))
	for index, point := range points {
		ids[index] = point.ID
	}
	return ids
}
