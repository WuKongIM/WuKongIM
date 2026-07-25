package backup_test

import (
	"context"
	"sync"
	"testing"
	"time"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	backupinfra "github.com/WuKongIM/WuKongIM/internal/infra/backup"
	backupusecase "github.com/WuKongIM/WuKongIM/internal/usecase/backup"
	"github.com/stretchr/testify/require"
)

func TestControllerIntegrityAuditStateStorePersistsCursorAndPreservesOtherCoordination(t *testing.T) {
	coordination := &erasureLedgerStateStore{state: backupusecase.State{
		Revision: 7, LastEpoch: 3,
	}}
	store, err := backupinfra.NewControllerIntegrityAuditStateStore(coordination)
	require.NoError(t, err)
	next := backupcontract.IntegrityAuditState{
		Revision: 1, DebtObjects: 4,
		UpdatedAtUnixMillis: 1_753_400_100_000,
		Cursor: &backupcontract.IntegrityAuditCursor{
			CycleID: "audit-cycle-1", ScrubEpoch: 5, HashSlot: 7,
			Generation: "slot-generation-1", Position: "segment-1",
			Phase:               backupcontract.IntegrityAuditPhaseInspect,
			UpdatedAtUnixMillis: 1_753_400_100_000,
		},
		Slots: []backupcontract.SlotIntegrityAuditState{{
			HashSlot: 7, Generation: "slot-generation-1",
			Health:     backupcontract.SlotAuditDegraded,
			Repository: "secondary", Category: backupcontract.IntegrityCorruptionMissing,
			UpdatedAtUnixMillis: 1_753_400_100_000,
		}},
	}
	require.NoError(t, store.CompareAndSwapIntegrityAudit(
		context.Background(), 0, next,
	))
	require.Equal(t, uint64(3), coordination.state.LastEpoch)

	loaded, err := store.LoadIntegrityAudit(context.Background())
	require.NoError(t, err)
	require.Equal(t, uint64(1), loaded.Revision)
	require.Equal(t, uint64(5), loaded.Cursor.ScrubEpoch)
	require.Equal(t, "segment-1", loaded.Cursor.Position)
	slot, found, err := store.AuditSlotState(context.Background(), 7)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, backupcontract.SlotAuditDegraded, slot.Health)
}

func TestControllerIntegrityAuditStateStoreRejectsStaleAuditRevision(t *testing.T) {
	coordination := &erasureLedgerStateStore{}
	store, err := backupinfra.NewControllerIntegrityAuditStateStore(coordination)
	require.NoError(t, err)
	first := backupcontract.IntegrityAuditState{
		Revision: 1, UpdatedAtUnixMillis: 1,
	}
	require.NoError(t, store.CompareAndSwapIntegrityAudit(
		context.Background(), 0, first,
	))
	err = store.CompareAndSwapIntegrityAudit(context.Background(), 0, first)
	require.ErrorIs(t, err, backupusecase.ErrStateConflict)
}

func TestControllerIntegrityAuditStateStoreRejectsCursorBelowRetainedRoot(t *testing.T) {
	coordination := &erasureLedgerStateStore{state: backupusecase.State{
		Revision:                 7,
		CatalogAuditRootSequence: 3,
	}}
	store, err := backupinfra.NewControllerIntegrityAuditStateStore(coordination)
	require.NoError(t, err)
	next := backupcontract.IntegrityAuditState{
		Revision: 1, UpdatedAtUnixMillis: 1,
		Cursor: &backupcontract.IntegrityAuditCursor{
			CycleID:    "catalog-segments-stale",
			ScrubEpoch: 1, CatalogSequence: 5,
			CatalogRootSequence: 1,
			Generation:          "catalog-navigation",
			Position:            "stale", Phase: backupcontract.IntegrityAuditPhaseInspect,
		},
	}

	err = store.CompareAndSwapIntegrityAudit(context.Background(), 0, next)
	require.ErrorIs(t, err, backupusecase.ErrStateConflict)
	require.Zero(t, coordination.state.IntegrityAudit.Revision)
}

func TestControllerIntegrityAuditStateStoreUsesNarrowCachedSlotProjection(t *testing.T) {
	coordination := &countingIntegrityCoordinationStore{state: backupusecase.State{
		IntegrityAudit: backupcontract.IntegrityAuditState{
			Revision: 1,
			Slots: []backupcontract.SlotIntegrityAuditState{{
				HashSlot: 7, Generation: "slot-generation-1",
				Health: backupcontract.SlotAuditHealthy,
			}},
		},
	}}
	store, err := backupinfra.NewControllerIntegrityAuditStateStore(coordination)
	require.NoError(t, err)

	for hashSlot := uint16(0); hashSlot < 256; hashSlot++ {
		_, _, err := store.AuditSlotState(context.Background(), hashSlot)
		require.NoError(t, err)
	}
	require.Equal(t, 1, coordination.loadCount())

	degraded := backupcontract.IntegrityAuditState{
		Revision: 2,
		Slots: []backupcontract.SlotIntegrityAuditState{{
			HashSlot: 7, Generation: "slot-generation-1",
			Health: backupcontract.SlotAuditDegraded,
		}},
	}
	coordination.mu.Lock()
	coordination.state.IntegrityAudit = degraded
	coordination.mu.Unlock()
	store.PublishIntegrityAuditProjection(degraded)
	slot, found, err := store.AuditSlotState(context.Background(), 7)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, backupcontract.SlotAuditDegraded, slot.Health)
	require.Equal(t, 2, coordination.loadCount())
}

func TestControllerIntegrityAuditStateStoreLinearizesFreezeWithGCDelete(t *testing.T) {
	coordination := &countingIntegrityCoordinationStore{}
	store, err := backupinfra.NewControllerIntegrityAuditStateStore(coordination)
	require.NoError(t, err)
	remoteStore, err := backupinfra.NewControllerIntegrityAuditStateStore(coordination)
	require.NoError(t, err)
	_, err = store.LoadIntegrityAudit(context.Background())
	require.NoError(t, err)

	deleteStarted := make(chan struct{})
	releaseDelete := make(chan struct{})
	deleteDone := make(chan error, 1)
	go func() {
		allowed, _, guardErr := store.WithGenerationGCDelete(
			context.Background(), 7, "",
			func(context.Context) (int, error) {
				close(deleteStarted)
				<-releaseDelete
				return 1, nil
			},
		)
		if !allowed && guardErr == nil {
			guardErr = context.Canceled
		}
		deleteDone <- guardErr
	}()
	<-deleteStarted

	guarded, err := remoteStore.LoadIntegrityAudit(context.Background())
	require.NoError(t, err)
	require.Len(t, guarded.GCGuards, 1)
	require.GreaterOrEqual(
		t,
		guarded.GCGuards[0].ExpiresAtUnixMillis-
			guarded.GCGuards[0].AcquiredAtUnixMillis,
		(10 * time.Minute).Milliseconds(),
	)
	frozen := backupcontract.CloneIntegrityAuditState(guarded)
	frozen.Revision++
	frozen.Slots = []backupcontract.SlotIntegrityAuditState{{
		HashSlot: 7, Generation: "slot-generation-1",
		Health: backupcontract.SlotAuditDegraded,
	}}
	require.ErrorIs(
		t,
		remoteStore.CompareAndSwapIntegrityAudit(
			context.Background(), guarded.Revision, frozen,
		),
		backupusecase.ErrStateConflict,
	)
	close(releaseDelete)
	require.NoError(t, <-deleteDone)

	released, err := remoteStore.LoadIntegrityAudit(context.Background())
	require.NoError(t, err)
	frozen = backupcontract.CloneIntegrityAuditState(released)
	frozen.Revision++
	frozen.Slots = []backupcontract.SlotIntegrityAuditState{{
		HashSlot: 7, Generation: "slot-generation-1",
		Health: backupcontract.SlotAuditDegraded,
	}}
	require.NoError(t, remoteStore.CompareAndSwapIntegrityAudit(
		context.Background(), released.Revision, frozen,
	))

	called := false
	allowed, used, err := store.WithGenerationGCDelete(
		context.Background(), 7, "",
		func(context.Context) (int, error) {
			called = true
			return 1, nil
		},
	)
	require.NoError(t, err)
	require.False(t, allowed)
	require.Zero(t, used)
	require.False(t, called)
}

func TestControllerIntegrityAuditStateStoreLinearizesAuditSelectionWithGCDelete(
	t *testing.T,
) {
	ctx := context.Background()
	coordination := &countingIntegrityCoordinationStore{}
	store, err := backupinfra.NewControllerIntegrityAuditStateStore(
		coordination,
	)
	require.NoError(t, err)
	deleteStarted := make(chan struct{})
	releaseDelete := make(chan struct{})
	deleteDone := make(chan error, 1)
	go func() {
		allowed, _, guardErr := store.WithGenerationGCDelete(
			ctx, 7, "",
			func(context.Context) (int, error) {
				close(deleteStarted)
				<-releaseDelete
				return 1, nil
			},
		)
		if !allowed && guardErr == nil {
			guardErr = context.Canceled
		}
		deleteDone <- guardErr
	}()
	<-deleteStarted

	guarded, err := store.LoadIntegrityAudit(ctx)
	require.NoError(t, err)
	started := backupcontract.CloneIntegrityAuditState(guarded)
	started.Revision++
	started.UpdatedAtUnixMillis = time.Now().UTC().UnixMilli()
	started.Cursor = &backupcontract.IntegrityAuditCursor{
		CycleID:    "catalog-segments-selection-a",
		Generation: "catalog-navigation", Position: "selection-a",
		Phase:               backupcontract.IntegrityAuditPhaseInspect,
		UpdatedAtUnixMillis: started.UpdatedAtUnixMillis,
	}
	require.ErrorIs(
		t,
		store.CompareAndSwapIntegrityAudit(
			ctx, guarded.Revision, started,
		),
		backupcontract.ErrStateConflict,
	)
	close(releaseDelete)
	require.NoError(t, <-deleteDone)

	released, err := store.LoadIntegrityAudit(ctx)
	require.NoError(t, err)
	started = backupcontract.CloneIntegrityAuditState(released)
	started.Revision++
	started.UpdatedAtUnixMillis = time.Now().UTC().UnixMilli()
	started.Cursor = &backupcontract.IntegrityAuditCursor{
		CycleID:    "catalog-segments-selection-a",
		Generation: "catalog-navigation", Position: "selection-a",
		Phase:               backupcontract.IntegrityAuditPhaseInspect,
		UpdatedAtUnixMillis: started.UpdatedAtUnixMillis,
	}
	require.NoError(t, store.CompareAndSwapIntegrityAudit(
		ctx, released.Revision, started,
	))

	called := false
	allowed, _, err := store.WithGenerationGCDelete(
		ctx, 7, "",
		func(context.Context) (int, error) {
			called = true
			return 1, nil
		},
	)
	require.NoError(t, err)
	require.False(t, allowed)
	require.False(t, called)
	allowed, _, err = store.WithGenerationGCDelete(
		ctx, 7, "catalog-segments-selection-a",
		func(context.Context) (int, error) {
			called = true
			return 1, nil
		},
	)
	require.NoError(t, err)
	require.True(t, allowed)
	require.True(t, called)
}

func TestControllerIntegrityAuditStateStoreReclaimsExpiredGCGuard(t *testing.T) {
	now := time.Now().UTC()
	coordination := &countingIntegrityCoordinationStore{
		state: backupusecase.State{
			IntegrityAudit: backupcontract.IntegrityAuditState{
				Revision:            1,
				UpdatedAtUnixMillis: now.Add(-2 * time.Minute).UnixMilli(),
				GCGuards: []backupcontract.IntegrityAuditGCGuard{{
					HashSlot: 7, Token: "gc-expired-guard",
					AcquiredAtUnixMillis: now.Add(-3 * time.Minute).UnixMilli(),
					ExpiresAtUnixMillis:  now.Add(-2 * time.Minute).UnixMilli(),
				}},
			},
		},
	}
	store, err := backupinfra.NewControllerIntegrityAuditStateStore(coordination)
	require.NoError(t, err)
	current, err := store.LoadIntegrityAudit(context.Background())
	require.NoError(t, err)
	next := backupcontract.CloneIntegrityAuditState(current)
	next.Revision++
	next.UpdatedAtUnixMillis = now.UnixMilli()
	next.Slots = []backupcontract.SlotIntegrityAuditState{{
		HashSlot: 7, Generation: "slot-generation-1",
		Health:     backupcontract.SlotAuditDegraded,
		Repository: "secondary", Category: backupcontract.IntegrityCorruptionMissing,
		UpdatedAtUnixMillis: now.UnixMilli(),
	}}

	require.NoError(t, store.CompareAndSwapIntegrityAudit(
		context.Background(), current.Revision, next,
	))
	loaded, err := store.LoadIntegrityAudit(context.Background())
	require.NoError(t, err)
	require.Empty(t, loaded.GCGuards)
	slot, found := backupcontract.FindSlotAuditState(loaded, 7)
	require.True(t, found)
	require.Equal(t, backupcontract.SlotAuditDegraded, slot.Health)
}

type countingIntegrityCoordinationStore struct {
	mu    sync.Mutex
	state backupusecase.State
	loads int
}

func (s *countingIntegrityCoordinationStore) Load(
	context.Context,
) (backupusecase.State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loads++
	return s.state.Clone(), nil
}

func (s *countingIntegrityCoordinationStore) CompareAndSwap(
	_ context.Context,
	revision uint64,
	next backupusecase.State,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state.Revision != revision {
		return backupusecase.ErrStateConflict
	}
	next.Revision = revision + 1
	s.state = next.Clone()
	return nil
}

func (s *countingIntegrityCoordinationStore) loadCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loads
}
