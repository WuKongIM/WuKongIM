package backup_test

import (
	"context"
	"strings"
	"testing"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	backupinfra "github.com/WuKongIM/WuKongIM/internal/infra/backup"
	backupruntime "github.com/WuKongIM/WuKongIM/internal/runtime/backup"
	backupusecase "github.com/WuKongIM/WuKongIM/internal/usecase/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	"github.com/WuKongIM/WuKongIM/pkg/controller"
	"github.com/stretchr/testify/require"
)

func TestControllerStateStoreLoadsBoundedCoordinationState(t *testing.T) {
	runtime := &fakeBackupController{state: controller.ClusterState{
		Revision: 7,
		Backup: &controller.BackupCoordinationState{
			LastEpoch:             3,
			ErasureLedgerBoundary: 4,
			CatalogHead: &controller.BackupCatalogPageReference{
				Sequence: 4, Key: "catalog/pages/00000000000000000004-checkpoint-4.json",
				SHA256: strings.Repeat("f", 64), Bytes: 456, LatestCheckpointID: "checkpoint-4",
			},
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
	require.Equal(t, uint64(4), state.CatalogHead.Sequence)

	state.Active.Partitions[0].ManifestKey = "mutated"
	require.Equal(t, "jobs/backup-3/partitions/2.json", runtime.state.Backup.Active.Partitions[0].ManifestKey)
}

func TestControllerStateStorePersistsCheckpointCatalogHead(t *testing.T) {
	runtime := &fakeBackupController{state: controller.ClusterState{Revision: 9}}
	store, err := backupinfra.NewControllerStateStore(runtime)
	require.NoError(t, err)
	head := &backupartifact.CatalogPageReference{
		Sequence: 7, Key: "catalog/pages/00000000000000000007-checkpoint-7.json",
		SHA256: strings.Repeat("a", 64), Bytes: 700, LatestCheckpointID: "checkpoint-7",
	}

	require.NoError(t, store.CompareAndSwap(context.Background(), 9, backupusecase.State{CatalogHead: head}))
	require.Equal(t, uint64(7), runtime.replacement.CatalogHead.Sequence)
	head.Sequence = 8
	require.Equal(t, uint64(7), runtime.replacement.CatalogHead.Sequence)
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
	frontiers, err := backupinfra.NewControllerSlotFrontierStore(coordination, staticCaptureAuthority{
		authority: backupruntime.SlotCaptureAuthority{
			SlotID: 2, LeaderTerm: 7, ConfigEpoch: 4, HolderNodeID: 1,
		},
	})
	require.NoError(t, err)
	snapshot, err := frontiers.AcquireLease(context.Background(), 17, "slot-generation-1", 1_753_400_100_000)
	require.NoError(t, err)
	runtime.onConflict = func() {
		runtime.state.Revision++
		runtime.state.Backup.LastEpoch = 4
	}
	next := backupcontract.CloneSlotFrontier(snapshot.Frontier)
	next.Revision++
	next.Metadata = backupcontract.StreamFrontier{SourceHighWatermark: 3, WatermarkAtUnixMillis: 1_753_400_100_000}
	next.Messages = backupcontract.StreamFrontier{SourceHighWatermark: 5, WatermarkAtUnixMillis: 1_753_400_090_000}
	next.WatermarkAtUnixMillis = 1_753_400_090_000
	next.UpdatedAtUnixMillis = 1_753_400_110_000

	require.NoError(t, frontiers.CompareAndSwap(context.Background(), snapshot.Frontier.Revision, snapshot.Frontier.Lease, next))
	require.Equal(t, 3, runtime.replaceCalls)
	require.Equal(t, uint64(4), runtime.replacement.LastEpoch)
	require.Len(t, runtime.replacement.SlotFrontiers, 1)

	snapshot, err = frontiers.Load(context.Background(), 17)
	require.NoError(t, err)
	require.True(t, snapshot.Found)
	require.Equal(t, uint64(2), snapshot.Frontier.Revision)
	require.Equal(t, uint64(5), snapshot.Frontier.Messages.SourceHighWatermark)
}

func TestControllerSlotFrontierStoreTakeoverPreservesFrontierAndFencesOldLease(t *testing.T) {
	runtime := &fakeBackupController{state: controller.ClusterState{
		Revision: 5,
		Backup: &controller.BackupCoordinationState{
			RestorePoints: []controller.BackupRestorePoint{},
		},
	}}
	coordination, err := backupinfra.NewControllerStateStore(runtime)
	require.NoError(t, err)
	authority := &mutableCaptureAuthority{authority: backupruntime.SlotCaptureAuthority{
		SlotID: 2, LeaderTerm: 7, ConfigEpoch: 4, HolderNodeID: 1,
	}}
	frontiers, err := backupinfra.NewControllerSlotFrontierStore(coordination, authority)
	require.NoError(t, err)

	first, err := frontiers.AcquireLease(context.Background(), 17, "slot-generation-1", 1_753_400_100_000)
	require.NoError(t, err)
	require.False(t, first.LeaseTakenOver)
	require.Equal(t, uint32(2), first.Frontier.SourceSlotID)
	require.Equal(t, int64(1_753_400_100_000), first.Frontier.SourcePinStartedAtUnixMillis)
	next := backupcontract.CloneSlotFrontier(first.Frontier)
	next.Revision++
	next.Metadata.SourceCursor = "metadata/3"
	next.Metadata.SourceHighWatermark = 3
	next.Metadata.WatermarkAtUnixMillis = 1_753_400_100_000
	next.WatermarkAtUnixMillis = 1_753_400_100_000
	next.UpdatedAtUnixMillis = 1_753_400_110_000
	require.NoError(t, frontiers.CompareAndSwap(context.Background(), first.Frontier.Revision, first.Frontier.Lease, next))

	authority.authority = backupruntime.SlotCaptureAuthority{
		SlotID: 2, LeaderTerm: 8, ConfigEpoch: 4, HolderNodeID: 2,
	}
	takeover, err := frontiers.AcquireLease(context.Background(), 17, "ignored-new-generation", 1_753_400_120_000)
	require.NoError(t, err)
	require.True(t, takeover.LeaseTakenOver)
	require.Equal(t, uint64(2), takeover.Frontier.Lease.Sequence)
	require.Equal(t, uint64(2), takeover.Frontier.Lease.HolderNodeID)
	require.Equal(t, "slot-generation-1", takeover.Frontier.Generation)
	require.Equal(t, uint64(3), takeover.Frontier.Metadata.SourceHighWatermark)
	require.Equal(t, uint32(2), takeover.Frontier.SourceSlotID)
	require.Equal(t, first.Frontier.SourcePinStartedAtUnixMillis, takeover.Frontier.SourcePinStartedAtUnixMillis)

	stale := backupcontract.CloneSlotFrontier(next)
	stale.Revision++
	err = frontiers.CompareAndSwap(context.Background(), next.Revision, next.Lease, stale)
	require.ErrorIs(t, err, backupruntime.ErrCaptureLeaseFenced)
}

func TestControllerSlotFrontierStorePhysicalRemapPreservesSourceIndexSpace(t *testing.T) {
	runtime := &fakeBackupController{state: controller.ClusterState{
		Revision: 5,
		Backup: &controller.BackupCoordinationState{
			RestorePoints: []controller.BackupRestorePoint{},
		},
	}}
	coordination, err := backupinfra.NewControllerStateStore(runtime)
	require.NoError(t, err)
	authority := &mutableCaptureAuthority{authority: backupruntime.SlotCaptureAuthority{
		SlotID: 2, LeaderTerm: 7, ConfigEpoch: 4, HolderNodeID: 1,
	}}
	frontiers, err := backupinfra.NewControllerSlotFrontierStore(coordination, authority)
	require.NoError(t, err)
	first, err := frontiers.AcquireLease(context.Background(), 17, "slot-generation-1", 1_753_400_100_000)
	require.NoError(t, err)

	authority.authority = backupruntime.SlotCaptureAuthority{
		SlotID: 3, LeaderTerm: 8, ConfigEpoch: 5, HolderNodeID: 2,
	}
	remapped, err := frontiers.AcquireLease(context.Background(), 17, "ignored-new-generation", 1_753_400_120_000)
	require.NoError(t, err)
	require.True(t, remapped.LeaseTakenOver)
	require.Equal(t, uint32(3), remapped.Frontier.Lease.SlotID)
	require.Equal(t, uint32(2), remapped.Frontier.SourceSlotID)
	require.Equal(t, first.Frontier.Generation, remapped.Frontier.Generation)
}

func TestControllerSlotFrontierStoreControllerLeaderRestartReusesLease(t *testing.T) {
	runtime := &fakeBackupController{state: controller.ClusterState{
		Revision: 5,
		Backup: &controller.BackupCoordinationState{
			RestorePoints: []controller.BackupRestorePoint{},
		},
	}}
	coordination, err := backupinfra.NewControllerStateStore(runtime)
	require.NoError(t, err)
	authority := &mutableCaptureAuthority{authority: backupruntime.SlotCaptureAuthority{
		SlotID: 2, LeaderTerm: 7, ConfigEpoch: 4, HolderNodeID: 1,
	}}
	firstStore, err := backupinfra.NewControllerSlotFrontierStore(coordination, authority)
	require.NoError(t, err)
	first, err := firstStore.AcquireLease(context.Background(), 17, "slot-generation-1", 1_753_400_100_000)
	require.NoError(t, err)
	writes := runtime.replaceCalls

	restartedStore, err := backupinfra.NewControllerSlotFrontierStore(coordination, authority)
	require.NoError(t, err)
	reloaded, err := restartedStore.AcquireLease(context.Background(), 17, "slot-generation-1", 1_753_400_200_000)
	require.NoError(t, err)
	require.Equal(t, writes, runtime.replaceCalls)
	require.Equal(t, first.Frontier.Revision, reloaded.Frontier.Revision)
	require.Equal(t, first.Frontier.Lease, reloaded.Frontier.Lease)
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
		applied := c.replacement.Clone()
		c.state.Backup = &applied
	}
	return c.replaceErr
}

var _ backupruntime.SlotFrontierStore = (*backupinfra.ControllerSlotFrontierStore)(nil)

type staticCaptureAuthority struct {
	authority backupruntime.SlotCaptureAuthority
	err       error
}

func (a staticCaptureAuthority) CurrentCaptureAuthority(context.Context, uint16) (backupruntime.SlotCaptureAuthority, error) {
	return a.authority, a.err
}

type mutableCaptureAuthority struct {
	authority backupruntime.SlotCaptureAuthority
	err       error
}

func (a *mutableCaptureAuthority) CurrentCaptureAuthority(context.Context, uint16) (backupruntime.SlotCaptureAuthority, error) {
	return a.authority, a.err
}
