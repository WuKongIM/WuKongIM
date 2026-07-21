package backup_test

import (
	"context"
	"strings"
	"testing"

	backupinfra "github.com/WuKongIM/WuKongIM/internal/infra/backup"
	backupusecase "github.com/WuKongIM/WuKongIM/internal/usecase/backup"
	"github.com/WuKongIM/WuKongIM/pkg/controller"
	"github.com/stretchr/testify/require"
)

func TestControllerStateStoreLoadsBoundedCoordinationState(t *testing.T) {
	runtime := &fakeBackupController{state: controller.ClusterState{
		Revision: 7,
		Backup: &controller.BackupCoordinationState{
			LastEpoch: 3,
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

	state.Active.Partitions[0].ManifestKey = "mutated"
	require.Equal(t, "jobs/backup-3/partitions/2.json", runtime.state.Backup.Active.Partitions[0].ManifestKey)
}

func TestControllerStateStoreMapsRevisionConflict(t *testing.T) {
	runtime := &fakeBackupController{state: controller.ClusterState{Revision: 8}, replaceErr: controller.ErrExpectedRevisionMismatch}
	store, err := backupinfra.NewControllerStateStore(runtime)
	require.NoError(t, err)

	err = store.CompareAndSwap(context.Background(), 7, backupusecase.State{LastEpoch: 1})
	require.ErrorIs(t, err, backupusecase.ErrStateConflict)
}

type fakeBackupController struct {
	state            controller.ClusterState
	expectedRevision uint64
	replacement      controller.BackupCoordinationState
	replaceErr       error
}

func (c *fakeBackupController) LoadBackupCoordinationState(context.Context) (controller.ClusterState, error) {
	return c.state.Clone(), nil
}

func (c *fakeBackupController) ReplaceBackupCoordinationState(_ context.Context, expectedRevision uint64, replacement controller.BackupCoordinationState) error {
	c.expectedRevision = expectedRevision
	c.replacement = replacement.Clone()
	return c.replaceErr
}
