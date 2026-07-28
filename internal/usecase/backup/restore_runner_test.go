package backup_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	backupusecase "github.com/WuKongIM/WuKongIM/internal/usecase/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
)

func TestRestoreRunnerStagesAndVerifiesAllReplicasBeforeSwitch(t *testing.T) {
	now := time.Date(2026, 7, 29, 3, 0, 0, 0, time.UTC)
	slots := make([]backupcontract.RestoreSlotProgress, backupcontract.HashSlotCount)
	for hashSlot := range slots {
		slots[hashSlot] = backupcontract.RestoreSlotProgress{
			HashSlot: uint16(hashSlot),
			Status:   backupcontract.RestoreSlotStatusPending,
		}
	}
	stateStore := &memoryScheduledStateStore{
		state: backupcontract.SystemState{
			Revision: 1,
			Plan: &backupcontract.Plan{
				Revision: 1,
				Store: backupcontract.StoreConfig{
					Kind: backupcontract.StoreKindFile,
				},
			},
			ActiveRestore: &backupcontract.RestoreJob{
				ID:                 "restore-1",
				BackupID:           "backup-1",
				Status:             backupcontract.RestoreStatusPreparing,
				StartedUnixMillis:  now.UnixMilli(),
				DeadlineUnixMillis: now.Add(48 * time.Hour).UnixMilli(),
				UpdatedUnixMillis:  now.UnixMilli(),
				TargetActivation:   "activation-new",
				Slots:              slots,
			},
		},
	}
	scheduled, err := backupusecase.NewScheduledService(
		backupusecase.ScheduledOptions{
			StateStore: stateStore,
			Now:        func() time.Time { return now },
			NewID:      func() string { return "unused" },
		},
	)
	if err != nil {
		t.Fatalf("NewScheduledService(): %v", err)
	}
	restore, err := backupusecase.NewRestoreService(
		backupusecase.RestoreServiceOptions{
			StateStore:    stateStore,
			Repository:    fixedRepositoryProvider{},
			Preflight:     noopRestorePreflight{},
			Now:           func() time.Time { return now },
			NewID:         func() string { return "unused" },
			NewActivation: func() string { return "unused" },
		},
	)
	if err != nil {
		t.Fatalf("NewRestoreService(): %v", err)
	}
	executor := &recordingRestoreExecutor{}
	executor.beforeExit = func() error {
		state, err := scheduled.State(context.Background())
		if err != nil {
			return err
		}
		if state.ActiveRestore == nil ||
			state.ActiveRestore.Status != backupcontract.RestoreStatusFinalizing {
			return fmt.Errorf("cleanup began before durable finalizing phase")
		}
		return nil
	}
	runner, err := backupusecase.NewRestoreRunner(
		scheduled, restore, executor, func() time.Time { return now },
	)
	if err != nil {
		t.Fatalf("NewRestoreRunner(): %v", err)
	}
	for step := 0; step < 600; step++ {
		advanced, err := runner.RunOnce(context.Background())
		if err != nil {
			t.Fatalf("RunOnce(%d): %v", step, err)
		}
		state, err := scheduled.State(context.Background())
		if err != nil {
			t.Fatalf("State(%d): %v", step, err)
		}
		if state.ActiveRestore == nil {
			if !advanced {
				t.Fatalf("terminal RunOnce(%d) advanced = false", step)
			}
			break
		}
		if step == 599 {
			t.Fatal("restore did not finish")
		}
	}
	state, err := scheduled.State(context.Background())
	if err != nil {
		t.Fatalf("State(): %v", err)
	}
	if state.ActiveRestore != nil ||
		state.ManagerSessionEpoch != 1 ||
		len(state.History) != 1 ||
		state.History[0].Status != string(backupcontract.RestoreStatusSucceeded) {
		t.Fatalf("state = %#v", state)
	}
	if len(executor.staged) != backupcontract.HashSlotCount ||
		len(executor.verified) != backupcontract.HashSlotCount ||
		executor.archives != 1 ||
		executor.switched != 1 || executor.entered != 1 ||
		executor.exited != 1 {
		t.Fatalf("executor = %#v", executor)
	}
}

func TestRestoreRunnerCanceledBeforeMaintenanceDoesNotEnterMaintenance(t *testing.T) {
	now := time.Date(2026, 7, 29, 3, 0, 0, 0, time.UTC)
	runner, scheduled, executor := newPreparingRestoreRunnerForTest(
		t, now, true, now.Add(48*time.Hour), nil,
	)

	active, err := runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce(): %v", err)
	}
	if !active || executor.archives != 0 || executor.entered != 0 {
		t.Fatalf("active=%v executor=%#v, want direct pre-maintenance cancellation", active, executor)
	}
	state, err := scheduled.State(context.Background())
	if err != nil {
		t.Fatalf("State(): %v", err)
	}
	if state.ActiveRestore != nil || len(state.History) != 1 ||
		state.History[0].Status != string(backupcontract.RestoreStatusCanceled) {
		t.Fatalf("state = %#v", state)
	}
}

func TestRestoreRunnerExpiredBeforeMaintenanceDoesNotEnterMaintenance(t *testing.T) {
	now := time.Date(2026, 7, 29, 3, 0, 0, 0, time.UTC)
	runner, scheduled, executor := newPreparingRestoreRunnerForTest(
		t, now, false, now, nil,
	)

	active, err := runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce(): %v", err)
	}
	if !active || executor.archives != 0 || executor.entered != 0 {
		t.Fatalf("active=%v executor=%#v, want direct pre-maintenance timeout", active, executor)
	}
	state, err := scheduled.State(context.Background())
	if err != nil {
		t.Fatalf("State(): %v", err)
	}
	if state.ActiveRestore != nil || len(state.History) != 1 ||
		state.History[0].Status != string(backupcontract.RestoreStatusFailed) ||
		state.History[0].ErrorCode != "deadline_exceeded" {
		t.Fatalf("state = %#v", state)
	}
}

func TestRestoreRunnerArchiveVerificationFailureDoesNotEnterMaintenance(t *testing.T) {
	now := time.Date(2026, 7, 29, 3, 0, 0, 0, time.UTC)
	verifyErr := errors.New("corrupt archive")
	runner, scheduled, executor := newPreparingRestoreRunnerForTest(
		t, now, false, now.Add(48*time.Hour), verifyErr,
	)

	active, err := runner.RunOnce(context.Background())
	if !errors.Is(err, verifyErr) {
		t.Fatalf("RunOnce() error = %v, want verification failure", err)
	}
	if !active || executor.archives != 1 || executor.entered != 0 {
		t.Fatalf("active=%v executor=%#v, want verification only", active, executor)
	}
	state, stateErr := scheduled.State(context.Background())
	if stateErr != nil {
		t.Fatalf("State(): %v", stateErr)
	}
	if state.ActiveRestore != nil || len(state.History) != 1 ||
		state.History[0].Status != string(backupcontract.RestoreStatusFailed) ||
		state.History[0].ErrorCode != "archive_verification_failed" {
		t.Fatalf("state = %#v", state)
	}
}

func newPreparingRestoreRunnerForTest(
	t *testing.T,
	now time.Time,
	cancelRequested bool,
	deadline time.Time,
	verifyErr error,
) (*backupusecase.RestoreRunner, *backupusecase.ScheduledService, *recordingRestoreExecutor) {
	t.Helper()
	slots := make([]backupcontract.RestoreSlotProgress, backupcontract.HashSlotCount)
	for hashSlot := range slots {
		slots[hashSlot] = backupcontract.RestoreSlotProgress{
			HashSlot: uint16(hashSlot),
			Status:   backupcontract.RestoreSlotStatusPending,
		}
	}
	stateStore := &memoryScheduledStateStore{
		state: backupcontract.SystemState{
			Revision: 1,
			Plan: &backupcontract.Plan{
				Revision: 1,
				Store:    backupcontract.StoreConfig{Kind: backupcontract.StoreKindFile},
			},
			ActiveRestore: &backupcontract.RestoreJob{
				ID: "restore-pre-maintenance", BackupID: "backup-1",
				Status:             backupcontract.RestoreStatusPreparing,
				StartedUnixMillis:  now.UnixMilli(),
				DeadlineUnixMillis: deadline.UnixMilli(),
				UpdatedUnixMillis:  now.UnixMilli(),
				CancelRequested:    cancelRequested,
				TargetActivation:   "activation-new",
				Slots:              slots,
			},
		},
	}
	scheduled, err := backupusecase.NewScheduledService(
		backupusecase.ScheduledOptions{
			StateStore: stateStore, Now: func() time.Time { return now },
			NewID: func() string { return "unused" },
		},
	)
	if err != nil {
		t.Fatalf("NewScheduledService(): %v", err)
	}
	restore, err := backupusecase.NewRestoreService(
		backupusecase.RestoreServiceOptions{
			StateStore: stateStore, Repository: fixedRepositoryProvider{},
			Preflight: noopRestorePreflight{}, Now: func() time.Time { return now },
			NewID:         func() string { return "unused" },
			NewActivation: func() string { return "unused" },
		},
	)
	if err != nil {
		t.Fatalf("NewRestoreService(): %v", err)
	}
	executor := &recordingRestoreExecutor{archiveErr: verifyErr}
	runner, err := backupusecase.NewRestoreRunner(
		scheduled, restore, executor, func() time.Time { return now },
	)
	if err != nil {
		t.Fatalf("NewRestoreRunner(): %v", err)
	}
	return runner, scheduled, executor
}

type noopRestorePreflight struct{}

func (noopRestorePreflight) Check(
	context.Context,
	backupcontract.RestoreJob,
	backupcontract.Plan,
	backupartifact.ArchiveManifest,
) error {
	return nil
}

type recordingRestoreExecutor struct {
	archives   int
	entered    int
	staged     []uint16
	verified   []uint16
	switched   int
	exited     int
	beforeExit func() error
	archiveErr error
}

func (e *recordingRestoreExecutor) VerifyArchive(
	context.Context,
	backupcontract.RestoreJob,
) error {
	e.archives++
	return e.archiveErr
}

func (e *recordingRestoreExecutor) EnterMaintenance(
	context.Context,
	backupcontract.RestoreJob,
) (string, error) {
	e.entered++
	return "activation-old", nil
}

func (e *recordingRestoreExecutor) StageSlot(
	_ context.Context,
	_ backupcontract.RestoreJob,
	hashSlot uint16,
	_ uint32,
) (backupusecase.RestoreStageResult, error) {
	e.staged = append(e.staged, hashSlot)
	return backupusecase.RestoreStageResult{
		ReplicaNodeIDs: []uint64{1, 2, 3},
		LogicalBytes:   uint64(hashSlot) + 1,
	}, nil
}

func (e *recordingRestoreExecutor) VerifySlot(
	_ context.Context,
	_ backupcontract.RestoreJob,
	hashSlot uint16,
	_ uint32,
) error {
	if len(e.staged) != backupcontract.HashSlotCount {
		return fmt.Errorf("verification began before all Slots were staged")
	}
	e.verified = append(e.verified, hashSlot)
	return nil
}

func (e *recordingRestoreExecutor) ActivateRestore(
	context.Context,
	backupcontract.RestoreJob,
) error {
	if len(e.verified) != backupcontract.HashSlotCount {
		return fmt.Errorf("switch began before all Slots were verified")
	}
	e.switched++
	return nil
}

func (e *recordingRestoreExecutor) Rollback(
	context.Context,
	backupcontract.RestoreJob,
) error {
	return nil
}

func (e *recordingRestoreExecutor) ExitMaintenance(
	_ context.Context,
	_ backupcontract.RestoreJob,
	success bool,
) error {
	if !success {
		return fmt.Errorf("unexpected rollback")
	}
	if e.beforeExit != nil {
		if err := e.beforeExit(); err != nil {
			return err
		}
	}
	e.exited++
	return nil
}
