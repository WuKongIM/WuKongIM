package backup

import (
	"strings"
	"testing"
	"time"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
)

func TestGenerationGCCycleIDResumesAcrossWindowsAndLeaderChanges(
	t *testing.T,
) {
	now := time.UnixMilli(1_800_000_000_000).UTC()
	cursors := []backupcontract.GenerationGCCursor{
		{
			Repository: "primary", Revision: 3,
			CycleID:                  "gc-r5-h9-original",
			CatalogRetentionRevision: 5,
			CutoffUnixMillis:         now.Add(-7 * 24 * time.Hour).UnixMilli(),
			UpdatedAtUnixMillis:      now.UnixMilli(),
		},
		{
			Repository: "secondary", Revision: 4,
			CycleID:                  "gc-r5-h8-complete",
			CatalogRetentionRevision: 5,
			CutoffUnixMillis:         now.Add(-7 * 24 * time.Hour).UnixMilli(),
			UpdatedAtUnixMillis:      now.UnixMilli(),
			Complete:                 true,
		},
	}

	cycleID, err := generationGCCycleID(
		cursors, 5, 10, now.Add(24*time.Hour),
	)
	if err != nil {
		t.Fatal(err)
	}
	if cycleID != "gc-r5-h9-original" {
		t.Fatalf("cycle ID = %q", cycleID)
	}

	cycleID, err = generationGCCycleID(
		cursors, 6, 11, now.Add(48*time.Hour),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(cycleID, "gc-r6-h11-") {
		t.Fatalf("new retention cycle ID = %q", cycleID)
	}
}

func TestGenerationGCCycleIDRejectsDivergentActiveRepositories(
	t *testing.T,
) {
	now := time.UnixMilli(1_800_000_000_000).UTC()
	_, err := generationGCCycleID(
		[]backupcontract.GenerationGCCursor{
			{
				Repository: "primary", Revision: 1,
				CycleID:                  "gc-cycle-a",
				CatalogRetentionRevision: 2,
				CutoffUnixMillis:         1,
				UpdatedAtUnixMillis:      1,
			},
			{
				Repository: "secondary", Revision: 1,
				CycleID:                  "gc-cycle-b",
				CatalogRetentionRevision: 2,
				CutoffUnixMillis:         1,
				UpdatedAtUnixMillis:      1,
			},
		},
		2, 7, now,
	)
	if err == nil {
		t.Fatal("divergent active repository cycles were accepted")
	}
}
