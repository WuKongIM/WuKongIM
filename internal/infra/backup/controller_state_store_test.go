package backup_test

import (
	"context"
	"strings"
	"testing"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	backupinfra "github.com/WuKongIM/WuKongIM/internal/infra/backup"
	backupruntime "github.com/WuKongIM/WuKongIM/internal/runtime/backup"
	backupusecase "github.com/WuKongIM/WuKongIM/internal/usecase/backup"
	"github.com/WuKongIM/WuKongIM/pkg/controller"
	"github.com/stretchr/testify/require"
)

func TestControllerStateStoreLoadsBoundedCoordinationState(t *testing.T) {
	runtime := &fakeBackupController{state: controller.ClusterState{
		Revision: 7,
		Backup: &controller.BackupCoordinationState{
			LastEpoch:             3,
			ErasureLedgerBoundary: 4,
			PendingErasureLedger: &controller.BackupErasureLedgerReference{
				Sequence: 5, EventID: strings.Repeat("d", 64), RecordKey: "erasure-ledger/events/0002/" + strings.Repeat("d", 64) + ".json", RecordSHA256: strings.Repeat("e", 64),
			},
			Active: &controller.BackupJob{
				ID:                  "backup-3",
				Epoch:               3,
				Kind:                "incremental",
				Status:              "capturing",
				HashSlotCount:       16,
				ConfigFingerprint:   strings.Repeat("a", 64),
				RestorePointID:      "restore-job-3",
				StartedAtUnixMillis: 1710000000000,
				UpdatedAtUnixMillis: 1710000001000,
				Partitions: []controller.BackupPartitionReport{
					{
						JobID:                 "backup-3",
						BackupEpoch:           3,
						HashSlot:              2,
						RaftIndex:             11,
						CommittedAtUnixMillis: 1710000000000,
						ManifestKey:           "jobs/backup-3/partitions/2.json",
						ManifestSHA256:        strings.Repeat("b", 64),
						ObjectCount:           2,
						CiphertextBytes:       256,
					},
				},
			},
			RestorePoints: []controller.BackupRestorePoint{},
			PendingGarbage: []controller.BackupRestorePoint{{
				ID: "restore-expired", JobID: "backup-1", BackupEpoch: 1, Kind: "materialized_full",
				EffectiveAtUnixMillis: 1700000000000, CreatedAtUnixMillis: 1700000001000,
				ManifestSHA256: strings.Repeat("c", 64), PrimaryVerified: true, SecondaryVerified: true,
			}},
		},
	}}
	store, err := backupinfra.NewControllerStateStore(runtime)
	require.NoError(t, err)

	state, err := store.Load(context.Background())
	require.NoError(t, err)
	require.Equal(t, uint64(7), state.Revision)
	require.Equal(t, uint64(3), state.LastEpoch)
	require.NotNil(t, state.Active)
	require.Equal(t, backupusecase.JobStatusCapturing, state.Active.Status)
	require.Equal(t, uint16(2), state.Active.Partitions[0].HashSlot)
	require.Equal(t, "restore-expired", state.PendingGarbage[0].ID)
	require.Equal(t, uint64(4), state.ErasureLedgerBoundary)
	require.Equal(t, uint64(5), state.PendingErasureLedger.Sequence)

	state.Active.Partitions[0].ManifestKey = "mutated"
	require.Equal(t, "jobs/backup-3/partitions/2.json", runtime.state.Backup.Active.Partitions[0].ManifestKey)
}

func TestControllerStateStorePersistsErasureLedgerCoordination(t *testing.T) {
	runtime := &fakeBackupController{state: controller.ClusterState{Revision: 9}}
	store, err := backupinfra.NewControllerStateStore(runtime)
	require.NoError(t, err)
	pending := &backupusecase.ErasureLedgerRecordReference{
		Sequence: 3, EventID: strings.Repeat("a", 64), RecordKey: "erasure-ledger/events/0001/" + strings.Repeat("a", 64) + ".json", RecordSHA256: strings.Repeat("b", 64),
	}

	err = store.CompareAndSwap(context.Background(), 9, backupusecase.State{ErasureLedgerBoundary: 2, PendingErasureLedger: pending})
	require.NoError(t, err)
	require.Equal(t, uint64(2), runtime.replacement.ErasureLedgerBoundary)
	require.Equal(t, uint64(3), runtime.replacement.PendingErasureLedger.Sequence)

	pending.EventID = "mutated"
	require.Equal(t, strings.Repeat("a", 64), runtime.replacement.PendingErasureLedger.EventID)
}

func TestControllerStateStoreRoundTripsVerificationEvidence(t *testing.T) {
	runtime := &fakeBackupController{state: controller.ClusterState{Revision: 9}}
	store, err := backupinfra.NewControllerStateStore(runtime)
	require.NoError(t, err)
	evidence := &backupusecase.VerificationEvidence{
		Status: backupusecase.VerificationTaskSucceeded, StartedAtUnixMillis: 100,
		CompletedAtUnixMillis: 200, PrimaryVerified: true, SecondaryVerified: true,
		ManifestSHA256: strings.Repeat("a", 64),
	}
	task := &backupusecase.VerificationTask{
		ID: "verification-1", RestorePointID: "restore-1", VerificationEvidence: *evidence,
	}

	err = store.CompareAndSwap(context.Background(), 9, backupusecase.State{
		Verification: task,
		RestorePoints: []backupusecase.RestorePoint{{
			ID: "restore-1", JobID: "backup-1", BackupEpoch: 1, Kind: "materialized_full",
			EffectiveAtUnixMillis: 50, CreatedAtUnixMillis: 60, ManifestSHA256: strings.Repeat("a", 64),
			PrimaryVerified: true, SecondaryVerified: true, LastVerification: evidence,
		}},
	})
	require.NoError(t, err)
	require.Equal(t, controller.BackupVerificationTaskStatus("succeeded"), runtime.replacement.Verification.Status)
	require.Equal(t, strings.Repeat("a", 64), runtime.replacement.RestorePoints[0].LastVerification.ManifestSHA256)

	runtime.state.Backup = &runtime.replacement
	loaded, err := store.Load(context.Background())
	require.NoError(t, err)
	require.Equal(t, backupusecase.VerificationTaskSucceeded, loaded.Verification.Status)
	require.Equal(t, int64(200), loaded.RestorePoints[0].LastVerification.CompletedAtUnixMillis)
}

func TestControllerStateStoreMapsRevisionConflict(t *testing.T) {
	runtime := &fakeBackupController{state: controller.ClusterState{Revision: 8}, replaceErr: controller.ErrExpectedRevisionMismatch}
	store, err := backupinfra.NewControllerStateStore(runtime)
	require.NoError(t, err)

	err = store.CompareAndSwap(context.Background(), 7, backupusecase.State{LastEpoch: 1})
	require.ErrorIs(t, err, backupusecase.ErrStateConflict)
}

func TestControllerSlotFrontierStoreRetriesGlobalConflictWithoutLosingCoordinationState(t *testing.T) {
	runtime := &fakeBackupController{state: controller.ClusterState{
		Revision: 8,
		Backup: &controller.BackupCoordinationState{
			LastEpoch:      3,
			RestorePoints:  []controller.BackupRestorePoint{},
			PendingGarbage: []controller.BackupRestorePoint{},
		},
	}}
	coordination, err := backupinfra.NewControllerStateStore(runtime)
	require.NoError(t, err)
	frontiers, err := backupinfra.NewControllerSlotFrontierStore(coordination)
	require.NoError(t, err)
	runtime.onConflict = func() {
		runtime.state.Revision = 9
		runtime.state.Backup.LastEpoch = 4
	}
	next := backupcontract.SlotFrontier{
		Revision: 1, HashSlot: 17, Generation: "slot-generation-1",
		Metadata:              backupcontract.StreamFrontier{SourceHighWatermark: 3, WatermarkAtUnixMillis: 1_753_400_100_000},
		Messages:              backupcontract.StreamFrontier{SourceHighWatermark: 5, WatermarkAtUnixMillis: 1_753_400_090_000},
		WatermarkAtUnixMillis: 1_753_400_090_000,
		UpdatedAtUnixMillis:   1_753_400_110_000,
	}

	require.NoError(t, frontiers.CompareAndSwap(context.Background(), 0, next))
	require.Equal(t, 2, runtime.replaceCalls)
	require.Equal(t, uint64(4), runtime.replacement.LastEpoch)
	require.Len(t, runtime.replacement.SlotFrontiers, 1)

	snapshot, err := frontiers.Load(context.Background(), 17)
	require.NoError(t, err)
	require.True(t, snapshot.Found)
	require.Equal(t, uint64(1), snapshot.Frontier.Revision)
	require.Equal(t, uint64(5), snapshot.Frontier.Messages.SourceHighWatermark)
}

type fakeBackupController struct {
	state            controller.ClusterState
	expectedRevision uint64
	replacement      controller.BackupCoordinationState
	replaceErr       error
	replaceCalls     int
	onConflict       func()
}

func (c *fakeBackupController) LoadBackupCoordinationState(context.Context) (controller.ClusterState, error) {
	return c.state.Clone(), nil
}

func (c *fakeBackupController) ReplaceBackupCoordinationState(_ context.Context, expectedRevision uint64, replacement controller.BackupCoordinationState) error {
	c.replaceCalls++
	c.expectedRevision = expectedRevision
	c.replacement = replacement.Clone()
	if c.onConflict != nil {
		onConflict := c.onConflict
		c.onConflict = nil
		onConflict()
		return controller.ErrExpectedRevisionMismatch
	}
	if c.replaceErr == nil {
		c.state.Revision = expectedRevision + 1
		c.state.Backup = &c.replacement
	}
	return c.replaceErr
}

var _ backupruntime.SlotFrontierStore = (*backupinfra.ControllerSlotFrontierStore)(nil)
