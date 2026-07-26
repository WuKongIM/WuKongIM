package backup_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

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
			CatalogHead: &controller.BackupCatalogPageReference{
				Sequence: 4, Key: "catalog/pages/00000000000000000004-checkpoint-4.json",
				SHA256: strings.Repeat("f", 64), Bytes: 456, LatestCheckpointID: "checkpoint-4",
			},
			CatalogRetentionRevision: 1,
			CatalogAuditRootSequence: 2,
			ErasureStreams: []controller.BackupErasureStreamState{{
				HashSlot: 2,
				Head: &backupartifact.ErasureStreamHead{
					HashSlot: 2, Sequence: 4, CommitKey: backupartifact.ErasureLedgerCommitKey(strings.Repeat("e", 64), 2, 4), CommitSHA256: strings.Repeat("a", 64),
				},
				Pending: &controller.BackupErasureLedgerReference{
					HashSlot: 2, Sequence: 5, EventID: strings.Repeat("d", 64), RecordKey: "erasure-ledger/events/0002/" + strings.Repeat("d", 64) + ".json", RecordSHA256: strings.Repeat("e", 64),
				},
			}},
			GenerationGCCursors: []controller.BackupGenerationGCCursor{{
				Repository: "primary", Revision: 2, CycleID: "gc-cycle-1",
				CatalogRetentionRevision: 1,
				AfterKey:                 "objects/generation-old/00002/object.bin",
				CutoffUnixMillis:         1710000000000, UpdatedAtUnixMillis: 1710000001000,
			}},
		},
	}}
	store, err := backupinfra.NewControllerStateStore(runtime)
	require.NoError(t, err)

	state, err := store.Load(context.Background())
	require.NoError(t, err)
	require.Equal(t, uint64(7), state.Revision)
	require.Equal(t, uint64(4), state.ErasureStreams[0].Head.Sequence)
	require.Equal(t, uint64(5), state.ErasureStreams[0].Pending.Sequence)
	require.Equal(t, uint64(4), state.CatalogHead.Sequence)
	require.Equal(t, uint64(1), state.CatalogRetentionRevision)
	require.Equal(t, uint64(2), state.CatalogAuditRootSequence)
	require.Equal(t, uint64(2), state.GenerationGCCursors[0].Revision)
	require.Equal(t, "objects/generation-old/00002/object.bin", state.GenerationGCCursors[0].AfterKey)

	state.GenerationGCCursors[0].AfterKey = "mutated"
	require.Equal(t, "objects/generation-old/00002/object.bin", runtime.state.Backup.GenerationGCCursors[0].AfterKey)
}

func TestControllerStateStorePersistsCheckpointCatalogHead(t *testing.T) {
	runtime := &fakeBackupController{state: controller.ClusterState{Revision: 9}}
	store, err := backupinfra.NewControllerStateStore(runtime)
	require.NoError(t, err)
	head := &backupartifact.CatalogPageReference{
		Sequence: 7, Key: "catalog/pages/00000000000000000007-checkpoint-7.json",
		SHA256: strings.Repeat("a", 64), Bytes: 700, LatestCheckpointID: "checkpoint-7",
	}

	require.NoError(t, store.CompareAndSwap(
		context.Background(), 9,
		backupusecase.State{
			CatalogHead: head, CatalogRetentionRevision: 1,
			CatalogAuditRootSequence: 7,
		},
	))
	require.Equal(t, uint64(7), runtime.replacement.CatalogHead.Sequence)
	require.Equal(t, uint64(1), runtime.replacement.CatalogRetentionRevision)
	require.Equal(t, uint64(7), runtime.replacement.CatalogAuditRootSequence)
	head.Sequence = 8
	require.Equal(t, uint64(7), runtime.replacement.CatalogHead.Sequence)
}

func TestControllerStateStorePersistsErasureLedgerCoordination(t *testing.T) {
	runtime := &fakeBackupController{state: controller.ClusterState{Revision: 9}}
	store, err := backupinfra.NewControllerStateStore(runtime)
	require.NoError(t, err)
	pending := &backupusecase.ErasureLedgerRecordReference{
		HashSlot: 1, Sequence: 3, EventID: strings.Repeat("a", 64), RecordKey: "erasure-ledger/events/0001/" + strings.Repeat("a", 64) + ".json", RecordSHA256: strings.Repeat("b", 64),
	}
	head := &backupartifact.ErasureStreamHead{
		HashSlot: 1, Sequence: 2, CommitKey: backupartifact.ErasureLedgerCommitKey(strings.Repeat("e", 64), 1, 2), CommitSHA256: strings.Repeat("c", 64),
	}

	err = store.CompareAndSwap(context.Background(), 9, backupusecase.State{ErasureStreams: []backupusecase.ErasureStreamState{{
		HashSlot: 1, Head: head, Pending: pending,
	}}})
	require.NoError(t, err)
	require.Equal(t, uint64(2), runtime.replacement.ErasureStreams[0].Head.Sequence)
	require.Equal(t, uint64(3), runtime.replacement.ErasureStreams[0].Pending.Sequence)

	pending.EventID = "mutated"
	head.Sequence = 9
	require.Equal(t, strings.Repeat("a", 64), runtime.replacement.ErasureStreams[0].Pending.EventID)
	require.Equal(t, uint64(2), runtime.replacement.ErasureStreams[0].Head.Sequence)
}

func TestControllerStateStoreMapsRevisionConflict(t *testing.T) {
	runtime := &fakeBackupController{state: controller.ClusterState{Revision: 8}, replaceErr: controller.ErrExpectedRevisionMismatch}
	store, err := backupinfra.NewControllerStateStore(runtime)
	require.NoError(t, err)

	err = store.CompareAndSwap(context.Background(), 7, backupusecase.State{})
	require.ErrorIs(t, err, backupusecase.ErrStateConflict)
}

func TestControllerSlotFrontierStoreRetriesGlobalConflictWithoutLosingCoordinationState(t *testing.T) {
	runtime := &fakeBackupController{state: controller.ClusterState{
		Revision: 8,
		Backup: &controller.BackupCoordinationState{
			GenerationGCCursors: []controller.BackupGenerationGCCursor{{
				Repository: "primary", Revision: 3, CycleID: "gc-cycle-1",
				CatalogRetentionRevision: 1,
				CutoffUnixMillis:         1710000000000,
				UpdatedAtUnixMillis:      1710000001000,
			}},
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
		runtime.state.Backup.GenerationGCCursors[0].Revision = 4
	}
	next := backupcontract.CloneSlotFrontier(snapshot.Frontier)
	next.Revision++
	next.Metadata = backupcontract.StreamFrontier{SourceHighWatermark: 3, WatermarkAtUnixMillis: 1_753_400_100_000}
	next.Messages = backupcontract.StreamFrontier{SourceHighWatermark: 5, WatermarkAtUnixMillis: 1_753_400_090_000}
	next.WatermarkAtUnixMillis = 1_753_400_090_000
	next.UpdatedAtUnixMillis = 1_753_400_110_000

	require.NoError(t, frontiers.CompareAndSwap(context.Background(), snapshot.Frontier.Revision, snapshot.Frontier.Lease, next))
	require.Equal(t, 3, runtime.replaceCalls)
	require.Equal(t, uint64(4), runtime.replacement.GenerationGCCursors[0].Revision)
	require.Len(t, runtime.replacement.SlotFrontiers, 1)

	snapshot, err = frontiers.Load(context.Background(), 17)
	require.NoError(t, err)
	require.True(t, snapshot.Found)
	require.Equal(t, uint64(2), snapshot.Frontier.Revision)
	require.Equal(t, uint64(5), snapshot.Frontier.Messages.SourceHighWatermark)
}

func TestControllerSlotFrontierStoreInitializes256SlotsAcrossConcurrentNodes(t *testing.T) {
	runtime := &concurrentBackupController{
		state: controller.ClusterState{
			Revision: 1,
			Backup:   &controller.BackupCoordinationState{},
		},
	}
	const nodeCount = 3
	stores := make([]*backupinfra.ControllerSlotFrontierStore, nodeCount)
	for index := range stores {
		coordination, err := backupinfra.NewControllerStateStore(runtime)
		require.NoError(t, err)
		stores[index], err = backupinfra.NewControllerSlotFrontierStore(
			coordination,
			staticCaptureAuthority{authority: backupruntime.SlotCaptureAuthority{
				SlotID: uint32(index + 1), LeaderTerm: 7, ConfigEpoch: 4,
				HolderNodeID: uint64(index + 1),
			}},
		)
		require.NoError(t, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	errs := make(chan error, 256)
	var workers sync.WaitGroup
	for hashSlot := uint16(0); hashSlot < 256; hashSlot++ {
		hashSlot := hashSlot
		workers.Add(1)
		go func() {
			defer workers.Done()
			_, err := stores[int(hashSlot)%nodeCount].AcquireLease(
				ctx, hashSlot, "generation-1", 1_753_400_100_000,
			)
			errs <- err
		}()
	}
	workers.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	state, err := runtime.LoadBackupCoordinationState(context.Background())
	require.NoError(t, err)
	require.NotNil(t, state.Backup)
	require.Len(t, state.Backup.SlotFrontiers, 256)
	for index, frontier := range state.Backup.SlotFrontiers {
		require.Equal(t, uint16(index), frontier.HashSlot)
		require.Equal(t, uint64(1), frontier.Revision)
	}
}

func TestControllerSlotFrontierStoreRejectsFreezeThatWinsDuringUpload(t *testing.T) {
	runtime := &fakeBackupController{state: controller.ClusterState{
		Revision: 8,
		Backup:   &controller.BackupCoordinationState{},
	}}
	coordination, err := backupinfra.NewControllerStateStore(runtime)
	require.NoError(t, err)
	frontiers, err := backupinfra.NewControllerSlotFrontierStore(
		coordination,
		staticCaptureAuthority{authority: backupruntime.SlotCaptureAuthority{
			SlotID: 2, LeaderTerm: 7, ConfigEpoch: 4, HolderNodeID: 1,
		}},
	)
	require.NoError(t, err)
	snapshot, err := frontiers.AcquireLease(
		context.Background(), 17, "slot-generation-1", 1_753_400_100_000,
	)
	require.NoError(t, err)

	// The artifact upload occurred outside Controller state; a remote auditor
	// freezes the Generation before the final frontier CAS.
	runtime.state.Revision++
	runtime.state.Backup.IntegrityAudit = controller.BackupIntegrityAuditState{
		Revision: 1, UpdatedAtUnixMillis: 1_753_400_105_000,
		Slots: []controller.BackupSlotIntegrityAuditState{{
			HashSlot: 17, Generation: "slot-generation-1",
			Health: "degraded", Repository: "secondary", Category: "missing",
			UpdatedAtUnixMillis: 1_753_400_105_000,
		}},
	}
	next := backupcontract.CloneSlotFrontier(snapshot.Frontier)
	next.Revision++
	next.Metadata.SourceHighWatermark = 3
	next.UpdatedAtUnixMillis = 1_753_400_110_000

	require.ErrorIs(
		t,
		frontiers.CompareAndSwap(
			context.Background(), snapshot.Frontier.Revision,
			snapshot.Frontier.Lease, next,
		),
		backupruntime.ErrIntegrityAuditFrozen,
	)
	reloaded, err := frontiers.Load(context.Background(), 17)
	require.NoError(t, err)
	require.Equal(
		t, snapshot.Frontier.Metadata.SourceHighWatermark,
		reloaded.Frontier.Metadata.SourceHighWatermark,
	)
}

func TestControllerSlotFrontierStoreTakeoverPreservesFrontierAndFencesOldLease(t *testing.T) {
	runtime := &fakeBackupController{state: controller.ClusterState{
		Revision: 5,
		Backup:   &controller.BackupCoordinationState{},
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
		Backup:   &controller.BackupCoordinationState{},
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
		Backup:   &controller.BackupCoordinationState{},
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

type concurrentBackupController struct {
	mu    sync.Mutex
	state controller.ClusterState
}

func (c *concurrentBackupController) LoadBackupCoordinationState(
	context.Context,
) (controller.ClusterState, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state.Clone(), nil
}

func (c *concurrentBackupController) ReplaceBackupCoordinationState(
	_ context.Context,
	expectedRevision uint64,
	replacement controller.BackupCoordinationState,
) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if expectedRevision != c.state.Revision {
		return controller.ErrExpectedRevisionMismatch
	}
	c.state.Revision++
	applied := replacement.Clone()
	c.state.Backup = &applied
	return nil
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
