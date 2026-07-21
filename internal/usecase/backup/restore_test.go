package backup_test

import (
	"context"
	"strings"
	"testing"
	"time"

	backupusecase "github.com/WuKongIM/WuKongIM/internal/usecase/backup"
)

func TestRestoreLifecycleRequiresEmptyFreshGenerationAndFence(t *testing.T) {
	store := &memoryRestoreStore{}
	now := time.Unix(1_800_000_000, 0)
	app, err := backupusecase.NewRestoreApp(backupusecase.RestoreOptions{
		Enabled: true, Store: store,
		Inspector: fakeRestoreInspector{inspection: backupusecase.RestoreInspection{
			RestorePointID: "restore-7", ManifestSHA256: strings.Repeat("a", 64),
			SourceClusterID: "old", SourceGeneration: "old-gen", TargetClusterID: "new", TargetGeneration: "new-gen",
			HashSlotCount: 2, TargetEmpty: true,
		}},
		Verifier: fakeRestoreVerifier{}, Now: func() time.Time { return now }, NewPlanID: func() string { return "plan-7" },
	})
	if err != nil {
		t.Fatalf("NewRestoreApp(): %v", err)
	}
	plan, err := app.Plan(context.Background(), backupusecase.RestorePlanRequest{RestorePointID: "restore-7", Repository: "primary"})
	if err != nil || plan.Status != backupusecase.RestoreStatusPlanned {
		t.Fatalf("Plan() plan=%+v err=%v", plan, err)
	}
	if _, err := app.Plan(context.Background(), backupusecase.RestorePlanRequest{RestorePointID: "restore-7", Repository: "primary"}); err == nil {
		t.Fatal("second Plan() error = nil")
	}
	plan, err = app.Start(context.Background(), plan.ID)
	if err != nil || plan.Status != backupusecase.RestoreStatusInstalling {
		t.Fatalf("Start() plan=%+v err=%v", plan, err)
	}
	plan, err = app.Start(context.Background(), plan.ID)
	if err != nil || plan.Status != backupusecase.RestoreStatusInstalling {
		t.Fatalf("idempotent Start() plan=%+v err=%v", plan, err)
	}
	for hashSlot := uint16(0); hashSlot < 2; hashSlot++ {
		report := backupusecase.RestorePartition{HashSlot: hashSlot, Installed: true, MetadataSHA256: strings.Repeat("b", 64)}
		plan, err = app.ReportPartition(context.Background(), plan.ID, report)
		if err != nil {
			t.Fatalf("ReportPartition(%d): %v", hashSlot, err)
		}
		if hashSlot == 0 {
			if _, err := app.ReportPartition(context.Background(), plan.ID, report); err != nil {
				t.Fatalf("idempotent ReportPartition(%d): %v", hashSlot, err)
			}
		}
	}
	if plan.Status != backupusecase.RestoreStatusInstalled {
		t.Fatalf("installed status = %q", plan.Status)
	}
	plan, err = app.Verify(context.Background(), plan.ID)
	if err != nil || plan.Status != backupusecase.RestoreStatusVerified {
		t.Fatalf("Verify() plan=%+v err=%v", plan, err)
	}
	if _, err := app.Activate(context.Background(), plan.ID, "dns-changed"); err == nil {
		t.Fatal("Activate() without cryptographic fence digest error = nil")
	}
	plan, err = app.Activate(context.Background(), plan.ID, strings.Repeat("f", 64))
	if err != nil || plan.Status != backupusecase.RestoreStatusActivated {
		t.Fatalf("Activate() plan=%+v err=%v", plan, err)
	}
	if _, err := app.Activate(context.Background(), plan.ID, strings.Repeat("F", 64)); err == nil {
		t.Fatal("Activate() uppercase digest error = nil")
	}
	plan, err = app.Activate(context.Background(), plan.ID, strings.Repeat("f", 64))
	if err != nil || plan.Status != backupusecase.RestoreStatusActivated {
		t.Fatalf("idempotent Activate() plan=%+v err=%v", plan, err)
	}
}

type memoryRestoreStore struct{ state backupusecase.RestoreState }

func (s *memoryRestoreStore) Load(context.Context) (backupusecase.RestoreState, error) {
	return s.state, nil
}
func (s *memoryRestoreStore) CompareAndSwap(_ context.Context, revision uint64, next backupusecase.RestoreState) error {
	if revision != s.state.Revision {
		return backupusecase.ErrStateConflict
	}
	next.Revision = revision + 1
	s.state = next
	return nil
}

type fakeRestoreInspector struct {
	inspection backupusecase.RestoreInspection
}

func (f fakeRestoreInspector) Inspect(context.Context, backupusecase.RestorePlanRequest) (backupusecase.RestoreInspection, error) {
	return f.inspection, nil
}

type fakeRestoreVerifier struct{}

func (fakeRestoreVerifier) VerifyRestore(_ context.Context, plan backupusecase.RestorePlan) ([]backupusecase.RestorePartition, error) {
	result := append([]backupusecase.RestorePartition(nil), plan.Partitions...)
	for index := range result {
		result[index].Verified = true
	}
	return result, nil
}
