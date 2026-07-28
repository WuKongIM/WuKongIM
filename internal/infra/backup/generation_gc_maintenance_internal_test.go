package backup

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	backupusecase "github.com/WuKongIM/WuKongIM/internal/usecase/backup"
)

func TestGenerationGCDebtReloadsDurableCursorsAfterCollection(
	t *testing.T,
) {
	observer := &generationGCDebtObserverStub{}
	state := &generationGCDebtStateStore{
		state: backupusecase.State{
			GenerationGCCursors: []backupcontract.GenerationGCCursor{
				{Repository: "primary", Complete: true},
				{Repository: "secondary", Complete: true},
			},
		},
	}
	maintenance := &GenerationGCMaintenance{
		state: state, observer: observer,
	}

	// A per-repository result can be emitted before cursor creation. Durable
	// completed cursors remain authoritative, so this must not invent debt.
	maintenance.observeCollectedDebt(context.Background())
	if observer.debt != 0 {
		t.Fatalf(
			"debt after pre-cursor failure = %d, want 0",
			observer.debt,
		)
	}

	state.state.GenerationGCCursors[1].Complete = false
	maintenance.observeCollectedDebt(context.Background())
	if observer.debt != 1 {
		t.Fatalf(
			"debt after durable partial failure = %d, want 1",
			observer.debt,
		)
	}

	state.err = errors.New("Controller state unavailable")
	observer.debt = 2
	maintenance.observeCollectedDebt(context.Background())
	if observer.debt != 2 {
		t.Fatalf(
			"debt after failed durable reload = %d, want 2",
			observer.debt,
		)
	}
}

type generationGCDebtObserverStub struct {
	debt int
}

func (o *generationGCDebtObserverStub) SetBackupGCDebt(debt int) {
	o.debt = debt
}

type generationGCDebtStateStore struct {
	state backupusecase.State
	err   error
}

func (s *generationGCDebtStateStore) Load(
	context.Context,
) (backupusecase.State, error) {
	return s.state.Clone(), s.err
}

func (*generationGCDebtStateStore) CompareAndSwap(
	context.Context,
	uint64,
	backupusecase.State,
) error {
	return nil
}

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
