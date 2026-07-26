package backup_test

import (
	"fmt"
	"testing"
	"time"

	backupusecase "github.com/WuKongIM/WuKongIM/internal/usecase/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
)

func TestDecideCheckpointRetentionProtectsNewestHoldAndActiveRestore(t *testing.T) {
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	checkpoints := []backupartifact.CatalogCheckpointReference{
		retentionCheckpoint("newest", now.Add(-time.Hour)),
		retentionCheckpoint("expired", now.Add(-90*24*time.Hour)),
		retentionCheckpoint("held", now.Add(-91*24*time.Hour)),
		retentionCheckpoint("restoring", now.Add(-92*24*time.Hour)),
	}
	checkpoints[2].Held = true
	decision, err := backupusecase.DecideCheckpointRetention(
		now, checkpoints, backupusecase.CheckpointRetentionPolicy{}, "restoring",
	)
	if err != nil {
		t.Fatalf("DecideCheckpointRetention() error = %v", err)
	}
	if len(decision.Retain) != 3 || decision.Retain[0].ID != "newest" ||
		decision.Retain[1].ID != "held" || decision.Retain[2].ID != "restoring" {
		t.Fatalf("retained checkpoints = %+v", decision.Retain)
	}
	if len(decision.Collect) != 1 || decision.Collect[0].ID != "expired" {
		t.Fatalf("collect checkpoints = %+v", decision.Collect)
	}
}

func TestDecideCheckpointRetentionReleasesExplicitHold(t *testing.T) {
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	checkpoints := []backupartifact.CatalogCheckpointReference{
		retentionCheckpoint("newest", now.Add(-time.Hour)),
		retentionCheckpoint("old-held", now.Add(-90*24*time.Hour)),
	}
	checkpoints[1].Held = true
	held, err := backupusecase.DecideCheckpointRetention(
		now, checkpoints, backupusecase.CheckpointRetentionPolicy{}, "",
	)
	if err != nil {
		t.Fatalf("DecideCheckpointRetention(held) error = %v", err)
	}
	if len(held.Retain) != 2 || len(held.Collect) != 0 {
		t.Fatalf("held decision = %+v", held)
	}
	checkpoints[1].Held = false
	released, err := backupusecase.DecideCheckpointRetention(
		now, checkpoints, backupusecase.CheckpointRetentionPolicy{}, "",
	)
	if err != nil {
		t.Fatalf("DecideCheckpointRetention(released) error = %v", err)
	}
	if len(released.Retain) != 1 || released.Retain[0].ID != "newest" ||
		len(released.Collect) != 1 || released.Collect[0].ID != "old-held" {
		t.Fatalf("released decision = %+v", released)
	}
}

func TestDecideCheckpointRetentionUsesSharedHourlyDailyAndMonthlyBuckets(t *testing.T) {
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	checkpoints := []backupartifact.CatalogCheckpointReference{
		retentionCheckpoint("newest", now.Add(-time.Hour)),
		retentionCheckpoint("hour-newer", now.Add(-25*time.Hour-10*time.Minute)),
		retentionCheckpoint("hour-older", now.Add(-25*time.Hour-40*time.Minute)),
		retentionCheckpoint("day-newer", now.Add(-8*24*time.Hour-10*time.Minute)),
		retentionCheckpoint("day-older", now.Add(-8*24*time.Hour-2*time.Hour)),
		retentionCheckpoint("month-newer", time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)),
		retentionCheckpoint("month-older", time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)),
	}
	decision, err := backupusecase.DecideCheckpointRetention(
		now, checkpoints, backupusecase.CheckpointRetentionPolicy{MonthlyMonths: 2}, "",
	)
	if err != nil {
		t.Fatalf("DecideCheckpointRetention() error = %v", err)
	}
	retained := make(map[string]bool, len(decision.Retain))
	for _, checkpoint := range decision.Retain {
		retained[checkpoint.ID] = true
	}
	for _, id := range []string{"newest", "hour-newer", "day-newer", "month-newer"} {
		if !retained[id] {
			t.Fatalf("checkpoint %q was not retained: %+v", id, decision)
		}
	}
	for _, id := range []string{"hour-older", "day-older", "month-older"} {
		if retained[id] {
			t.Fatalf("checkpoint %q unexpectedly retained: %+v", id, decision)
		}
	}
}

func retentionCheckpoint(id string, created time.Time) backupartifact.CatalogCheckpointReference {
	vectorID := fmt.Sprintf("%064x", len(id)+1000)
	return backupartifact.CatalogCheckpointReference{
		ID: id, Key: backupartifact.CheckpointObjectKey(id),
		SHA256: fmt.Sprintf("%064x", len(id)),
		Bytes:  128, CreatedAtUnixMillis: created.UnixMilli(),
		EffectiveAtUnixMillis: created.Add(-time.Minute).UnixMilli(),
		GenerationVector: backupartifact.GenerationVectorReference{
			ID: vectorID, Key: backupartifact.GenerationVectorObjectKey(vectorID),
			SHA256: fmt.Sprintf("%064x", len(id)+2000),
			Bytes:  64, HashSlotCount: 256,
		},
	}
}
