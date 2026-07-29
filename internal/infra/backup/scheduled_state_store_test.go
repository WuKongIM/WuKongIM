package backup_test

import (
	"context"
	"errors"
	"testing"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	backupinfra "github.com/WuKongIM/WuKongIM/internal/infra/backup"
	backupusecase "github.com/WuKongIM/WuKongIM/internal/usecase/backup"
	"github.com/WuKongIM/WuKongIM/pkg/controller"
)

func TestScheduledControllerStateStoreRoundTripsDetachedState(t *testing.T) {
	runtime := &fakeScheduledBackupController{
		state: controller.ClusterState{Revision: 12},
	}
	store, err := backupinfra.NewScheduledControllerStateStore(runtime)
	if err != nil {
		t.Fatalf("NewScheduledControllerStateStore(): %v", err)
	}
	next := scheduledSystemState()

	if err := store.CompareAndSwap(context.Background(), 12, next); err != nil {
		t.Fatalf("CompareAndSwap(): %v", err)
	}
	loaded, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	if loaded.Revision != 13 || loaded.Plan == nil || loaded.ActiveBackup == nil {
		t.Fatalf("loaded = %#v", loaded)
	}
	if loaded.Plan.Store.Kind != backupcontract.StoreKindS3 ||
		string(loaded.Plan.Store.CredentialCiphertext) != "ciphertext" ||
		loaded.Plan.RepositoryVerification == nil ||
		loaded.Plan.RepositoryVerification.Status !=
			backupcontract.RepositoryVerificationVerified ||
		loaded.Plan.RepositoryVerification.VerifiedAtUnixMillis !=
			1_800_000_000_500 ||
		len(loaded.ActiveBackup.Slots) != backupcontract.HashSlotCount {
		t.Fatalf("round trip lost state: %#v", loaded)
	}

	loaded.Plan.Store.CredentialCiphertext[0] = 'X'
	loaded.Plan.RepositoryVerification.Status =
		backupcontract.RepositoryVerificationUnverified
	loaded.ActiveBackup.Slots[0].Status = backupcontract.SlotStatusComplete
	reloaded, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("Load(second): %v", err)
	}
	if string(reloaded.Plan.Store.CredentialCiphertext) != "ciphertext" ||
		reloaded.Plan.RepositoryVerification == nil ||
		reloaded.Plan.RepositoryVerification.Status !=
			backupcontract.RepositoryVerificationVerified ||
		reloaded.ActiveBackup.Slots[0].Status != backupcontract.SlotStatusPending {
		t.Fatal("Load returned state aliased to Controller storage")
	}
}

func TestScheduledControllerStateStoreMapsRevisionConflict(t *testing.T) {
	runtime := &fakeScheduledBackupController{
		state:      controller.ClusterState{Revision: 7},
		replaceErr: controller.ErrExpectedRevisionMismatch,
	}
	store, err := backupinfra.NewScheduledControllerStateStore(runtime)
	if err != nil {
		t.Fatalf("NewScheduledControllerStateStore(): %v", err)
	}
	err = store.CompareAndSwap(context.Background(), 6, scheduledSystemState())
	if !errors.Is(err, backupusecase.ErrStateConflict) {
		t.Fatalf("CompareAndSwap() error = %v", err)
	}
}

func TestScheduledControllerStateStoreRejectsStaleCoordinatorFence(t *testing.T) {
	scheduled := controller.ScheduledBackupState{Revision: 7}
	runtime := &fakeScheduledBackupController{
		state: controller.ClusterState{
			Revision:        7,
			ScheduledBackup: &scheduled,
		},
		leaderID: 2, leaderTerm: 11,
	}
	store, err := backupinfra.NewScheduledControllerStateStore(runtime)
	if err != nil {
		t.Fatalf("NewScheduledControllerStateStore(): %v", err)
	}
	current, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	ctx := backupcontract.WithCoordinatorFence(
		context.Background(), 1, 10,
	)
	if err := store.CompareAndSwap(ctx, current.Revision, current); !errors.Is(
		err, backupusecase.ErrStateConflict,
	) {
		t.Fatalf("CompareAndSwap(stale coordinator) error = %v", err)
	}
	if runtime.state.Revision != 7 {
		t.Fatalf("revision changed to %d", runtime.state.Revision)
	}
}

func scheduledSystemState() backupcontract.SystemState {
	slots := make([]backupcontract.SlotProgress, backupcontract.HashSlotCount)
	for hashSlot := range slots {
		slots[hashSlot] = backupcontract.SlotProgress{
			HashSlot: uint16(hashSlot),
			Status:   backupcontract.SlotStatusPending,
		}
	}
	return backupcontract.SystemState{
		Revision: 9,
		Plan: &backupcontract.Plan{
			Revision: 1,
			Enabled:  true,
			Store: backupcontract.StoreConfig{
				Kind:                 backupcontract.StoreKindS3,
				Endpoint:             "https://s3.example.com",
				Region:               "test",
				Bucket:               "backup",
				Prefix:               "cluster",
				PathStyle:            true,
				CredentialCiphertext: []byte("ciphertext"),
				CredentialRevision:   2,
			},
			Cron:                     "0 1 * * *",
			TimeZone:                 "Asia/Shanghai",
			RetentionCount:           7,
			RateBytesPerSec:          50 << 20,
			WorkersPerNode:           1,
			MaxDurationMillis:        12 * 60 * 60 * 1000,
			ScheduleCursorUnixMillis: 1_800_000_000_000,
			CreatedUnixMillis:        1_800_000_000_000,
			UpdatedUnixMillis:        1_800_000_000_000,
			RepositoryVerification: &backupcontract.RepositoryVerification{
				Status:               backupcontract.RepositoryVerificationVerified,
				VerifiedAtUnixMillis: 1_800_000_000_500,
			},
		},
		ActiveBackup: &backupcontract.BackupJob{
			ID:                  "backup-1",
			Trigger:             backupcontract.TriggerInitial,
			Status:              backupcontract.JobStatusPreparing,
			PlanRevision:        1,
			StartedAtUnixMillis: 1_800_000_000_000,
			DeadlineUnixMillis:  1_800_043_200_000,
			UpdatedUnixMillis:   1_800_000_000_000,
			Slots:               slots,
		},
		History: []backupcontract.TaskRecord{},
	}
}

type fakeScheduledBackupController struct {
	state      controller.ClusterState
	replaceErr error
	leaderID   uint64
	leaderTerm uint64
}

func (f *fakeScheduledBackupController) BackupControllerFence(
	context.Context,
) (uint64, uint64, error) {
	if f.leaderID == 0 {
		return 1, 1, nil
	}
	return f.leaderID, f.leaderTerm, nil
}

func (f *fakeScheduledBackupController) LocalState(context.Context) (controller.ClusterState, error) {
	return f.state.Clone(), nil
}

func (f *fakeScheduledBackupController) ReplaceScheduledBackupState(
	_ context.Context,
	expectedRevision uint64,
	replacement controller.ScheduledBackupState,
) error {
	if f.replaceErr != nil {
		return f.replaceErr
	}
	if f.state.Revision != expectedRevision {
		return controller.ErrExpectedRevisionMismatch
	}
	f.state.Revision++
	applied := replacement.Clone()
	f.state.ScheduledBackup = &applied
	return nil
}
