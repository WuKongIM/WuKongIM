package backup_test

import (
	"context"
	"strings"
	"testing"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
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
	proof := restoreStateStoreCatalogProof()
	runtime := &fakeRestoreController{state: controller.ClusterState{Revision: 8, Restore: &controller.RestoreCoordinationState{Plan: &controller.RestorePlan{
		ID: "plan-1", CheckpointID: "restore-1", CheckpointSHA256: strings.Repeat("a", 64), Repository: "secondary",
		CatalogProof: &proof, CheckpointVersion: backupartifact.CheckpointVersion,
		CheckpointCreatedAtUnixMillis:   proof.Checkpoint.CreatedAtUnixMillis,
		CheckpointEffectiveAtUnixMillis: proof.Checkpoint.EffectiveAtUnixMillis,
		SourceClusterID:                 "cluster-a", SourceGeneration: "generation-a", TargetClusterID: "cluster-b", TargetGeneration: "generation-b",
		HashSlotCount: 1, EstimatedPlainBytes: &plain, EstimatedCipherBytes: &cipher,
		ErasureLedgerVersion: backupartifact.ErasureLedgerSnapshotVersion, ErasureEventCount: 3,
		ErasureHeads: []backupartifact.ErasureStreamHead{{
			HashSlot: 0, Sequence: 3, CommitKey: backupartifact.ErasureLedgerCommitKey(strings.Repeat("e", 64), 0, 3), CommitSHA256: strings.Repeat("f", 64),
		}},
		ErasureLedgerSHA256: strings.Repeat("e", 64),
		Status:              controllerstate.RestoreStatusInstalling, CreatedAtUnixMillis: 1, UpdatedAtUnixMillis: 2,
		Partitions: []controller.RestorePartition{{
			HashSlot: 0, Status: controllerstate.RestorePartitionConverged,
			TargetSlotID: 7, LeaderNodeID: 2, LeaderTerm: 9, ConfigEpoch: 4,
			InstallAttempt: 1, EvidenceVersion: backupartifact.RestoreEvidenceVersion,
			Installed: true, MetadataSHA256: strings.Repeat("b", 64),
			ContentSHA256:       strings.Repeat("b", 64),
			MessageMerkleSHA256: strings.Repeat("c", 64),
			ReplicaCount:        3, ConvergedReplicas: 3,
			StartedAtUnixMillis: 1, InstalledAtUnixMillis: 2,
		}},
	}}}}
	store, err := backupinfra.NewControllerRestoreStateStore(runtime)
	require.NoError(t, err)
	loaded, err := store.Load(context.Background())
	require.NoError(t, err)
	require.Equal(t, uint64(8), loaded.Revision)
	require.Equal(t, backupusecase.RestoreStatusInstalling, loaded.Plan.Status)
	require.Equal(t, backupartifact.RestoreEvidenceVersion, loaded.Plan.Partitions[0].EvidenceVersion)
	require.Equal(t, strings.Repeat("b", 64), loaded.Plan.Partitions[0].MetadataSHA256)
	require.Equal(t, backupcontract.RestorePartitionConverged, loaded.Plan.Partitions[0].Status)
	require.Equal(t, proof, *loaded.Plan.CatalogProof)
	require.Equal(t, uint64(11), *loaded.Plan.EstimatedPlainBytes)
	require.Equal(t, uint64(3), loaded.Plan.ErasureEventCount)
	require.Equal(t, uint64(3), loaded.Plan.ErasureHeads[0].Sequence)
	require.Equal(t, strings.Repeat("e", 64), loaded.Plan.ErasureLedgerSHA256)
	*loaded.Plan.EstimatedPlainBytes = 99
	loaded.Plan.ErasureHeads[0].Sequence = 9
	loaded.Plan.CatalogProof.Head.Sequence = 9
	require.Equal(t, uint64(11), *runtime.state.Restore.Plan.EstimatedPlainBytes)
	require.Equal(t, uint64(3), runtime.state.Restore.Plan.ErasureHeads[0].Sequence)
	require.Equal(t, uint64(1), runtime.state.Restore.Plan.CatalogProof.Head.Sequence)

	runtime.replaceErr = controller.ErrExpectedRevisionMismatch
	err = store.CompareAndSwap(context.Background(), 8, loaded)
	require.ErrorIs(t, err, backupusecase.ErrStateConflict)
}

func restoreStateStoreCatalogProof() backupartifact.CheckpointCatalogProof {
	vectorID := strings.Repeat("d", 64)
	entry := backupartifact.CatalogCheckpointReference{
		ID: "restore-1", Key: backupartifact.CheckpointObjectKey("restore-1"),
		SHA256: strings.Repeat("a", 64), Bytes: 100,
		CreatedAtUnixMillis: 2, EffectiveAtUnixMillis: 1,
		GenerationVector: backupartifact.GenerationVectorReference{
			ID: vectorID, Key: backupartifact.GenerationVectorObjectKey(vectorID),
			SHA256: strings.Repeat("e", 64), Bytes: 100, HashSlotCount: 1,
		},
	}
	page := backupartifact.CatalogPageReference{
		Sequence: 1, Key: backupartifact.CatalogPageObjectKey(1, "restore-1"),
		SHA256: strings.Repeat("f", 64), Bytes: 100,
		LatestCheckpointID: "restore-1",
	}
	return backupartifact.CheckpointCatalogProof{
		Head: page, EntryPage: page, Checkpoint: entry,
	}
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
