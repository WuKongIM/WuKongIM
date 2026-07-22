package backup_test

import (
	"context"
	"strings"
	"testing"

	backupinfra "github.com/WuKongIM/WuKongIM/internal/infra/backup"
	backupusecase "github.com/WuKongIM/WuKongIM/internal/usecase/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	"github.com/WuKongIM/WuKongIM/pkg/controller"
	controllerstate "github.com/WuKongIM/WuKongIM/pkg/controller/state"
	"github.com/stretchr/testify/require"
)

func TestControllerRestoreStateStoreRoundTripsPointersAndMapsConflict(t *testing.T) {
	plain := uint64(11)
	cipher := uint64(12)
	runtime := &fakeRestoreController{state: controller.ClusterState{Revision: 8, Restore: &controller.RestoreCoordinationState{Plan: &controller.RestorePlan{
		ID: "plan-1", RestorePointID: "restore-1", ManifestSHA256: strings.Repeat("a", 64), Repository: "secondary",
		SourceClusterID: "cluster-a", SourceGeneration: "generation-a", TargetClusterID: "cluster-b", TargetGeneration: "generation-b",
		HashSlotCount: 1, EstimatedPlainBytes: &plain, EstimatedCipherBytes: &cipher,
		ErasureLedgerVersion: backupartifact.ErasureLedgerSnapshotVersion, ErasureLedgerBoundary: 3, ErasureLedgerSHA256: strings.Repeat("e", 64),
		Status: controllerstate.RestoreStatusInstalling, CreatedAtUnixMillis: 1, UpdatedAtUnixMillis: 2,
		Partitions: []controller.RestorePartition{{HashSlot: 0, EvidenceVersion: backupartifact.PartitionEvidenceVersion, Installed: true, MetadataSHA256: strings.Repeat("b", 64)}},
	}}}}
	store, err := backupinfra.NewControllerRestoreStateStore(runtime)
	require.NoError(t, err)
	loaded, err := store.Load(context.Background())
	require.NoError(t, err)
	require.Equal(t, uint64(8), loaded.Revision)
	require.Equal(t, backupusecase.RestoreStatusInstalling, loaded.Plan.Status)
	require.Equal(t, backupartifact.PartitionEvidenceVersion, loaded.Plan.Partitions[0].EvidenceVersion)
	require.Equal(t, strings.Repeat("b", 64), loaded.Plan.Partitions[0].MetadataSHA256)
	require.Equal(t, uint64(11), *loaded.Plan.EstimatedPlainBytes)
	require.Equal(t, uint64(3), loaded.Plan.ErasureLedgerBoundary)
	require.Equal(t, strings.Repeat("e", 64), loaded.Plan.ErasureLedgerSHA256)
	*loaded.Plan.EstimatedPlainBytes = 99
	require.Equal(t, uint64(11), *runtime.state.Restore.Plan.EstimatedPlainBytes)

	runtime.replaceErr = controller.ErrExpectedRevisionMismatch
	err = store.CompareAndSwap(context.Background(), 8, loaded)
	require.ErrorIs(t, err, backupusecase.ErrStateConflict)
}

type fakeRestoreController struct {
	state       controller.ClusterState
	replaceErr  error
	replacement controller.RestoreCoordinationState
}

func (f *fakeRestoreController) LoadRestoreCoordinationState(context.Context) (controller.ClusterState, error) {
	return f.state.Clone(), nil
}

func (f *fakeRestoreController) ReplaceRestoreCoordinationState(_ context.Context, _ uint64, replacement controller.RestoreCoordinationState) error {
	f.replacement = replacement.Clone()
	return f.replaceErr
}
