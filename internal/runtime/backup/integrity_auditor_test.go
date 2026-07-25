package backup_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	backupruntime "github.com/WuKongIM/WuKongIM/internal/runtime/backup"
	"github.com/stretchr/testify/require"
)

func TestIntegrityAuditorPersistsDegradedBeforeRepairAndResumesAfterRestart(t *testing.T) {
	store := &memoryIntegrityAuditStore{}
	backend := &repairingIntegrityAuditBackend{}
	observer := &recordingIntegrityAuditObserver{}
	now := advancingAuditClock(1_753_400_000_000)
	auditor := newIntegrityAuditor(t, store, backend, &recordingIntegrityAuditRecovery{}, observer, now)

	state, err := auditor.RunStep(context.Background())
	require.NoError(t, err)
	require.Equal(t, backupcontract.IntegrityAuditPhaseInspect, state.Cursor.Phase)

	state, err = auditor.RunStep(context.Background())
	require.NoError(t, err)
	require.Equal(t, backupcontract.IntegrityAuditPhaseRepair, state.Cursor.Phase)
	slot, found := backupcontract.FindSlotAuditState(state, 7)
	require.True(t, found)
	require.Equal(t, backupcontract.SlotAuditDegraded, slot.Health)
	require.Equal(t, "secondary", slot.Repository)
	require.Equal(t, []string{"missing:secondary"}, observer.corruptions)
	require.Zero(t, backend.repairCalls)

	// A new Controller Leader/process observes the durable repair cursor.
	restarted := newIntegrityAuditor(
		t, store, backend, &recordingIntegrityAuditRecovery{}, observer, now,
	)
	state, err = restarted.RunStep(context.Background())
	require.NoError(t, err)
	require.Equal(t, backupcontract.IntegrityAuditPhaseRevalidate, state.Cursor.Phase)
	require.Equal(t, 1, backend.repairCalls)
	require.Equal(t, int64(42), observer.repairBytes)

	state, err = restarted.RunStep(context.Background())
	require.NoError(t, err)
	require.Equal(t, "slot-8-object-1", state.Cursor.Position)
	slot, found = backupcontract.FindSlotAuditState(state, 7)
	require.True(t, found)
	require.Equal(t, backupcontract.SlotAuditHealthy, slot.Health)
	require.NotZero(t, state.LastSuccessAtUnixMillis)
	require.Equal(t, state.LastSuccessAtUnixMillis, observer.lastSuccess)
}

func TestIntegrityAuditorRequestsRebaseForDualLossAndContinuesNextSlot(t *testing.T) {
	store := &memoryIntegrityAuditStore{}
	backend := &dualLossIntegrityAuditBackend{}
	recovery := &recordingIntegrityAuditRecovery{
		available: true,
		result: backupruntime.IntegrityAuditRebaseResult{
			Complete: true, Generation: "slot-generation-2",
		},
	}
	auditor := newIntegrityAuditor(
		t, store, backend, recovery, &recordingIntegrityAuditObserver{},
		advancingAuditClock(1_753_400_100_000),
	)

	_, err := auditor.RunStep(context.Background())
	require.NoError(t, err)
	state, err := auditor.RunStep(context.Background())
	require.NoError(t, err)
	require.Equal(t, backupcontract.IntegrityAuditPhaseRebase, state.Cursor.Phase)
	slot, found := backupcontract.FindSlotAuditState(state, 7)
	require.True(t, found)
	require.Equal(t, backupcontract.SlotAuditRebaseRequired, slot.Health)
	require.Zero(t, recovery.rebaseCalls)

	restarted := newIntegrityAuditor(
		t, store, backend, recovery, &recordingIntegrityAuditObserver{},
		advancingAuditClock(1_753_400_200_000),
	)
	state, err = restarted.RunStep(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, recovery.rebaseCalls)
	require.Equal(t, uint16(8), state.Cursor.HashSlot)
	require.Equal(t, "slot-8-object-1", state.Cursor.Position)
	slot, found = backupcontract.FindSlotAuditState(state, 7)
	require.True(t, found)
	require.Equal(t, backupcontract.SlotAuditHealthy, slot.Health)
	require.Equal(t, "slot-generation-2", slot.Generation)

	state, err = restarted.RunStep(context.Background())
	require.NoError(t, err)
	nextSlot, found := backupcontract.FindSlotAuditState(state, 8)
	require.True(t, found)
	require.Equal(t, backupcontract.SlotAuditHealthy, nextSlot.Health)
}

func TestIntegrityAuditorKeepsRebaseFrozenUntilReplacementIsValidated(t *testing.T) {
	store := &memoryIntegrityAuditStore{}
	backend := &dualLossIntegrityAuditBackend{}
	recovery := &recordingIntegrityAuditRecovery{available: true}
	auditor := newIntegrityAuditor(
		t, store, backend, recovery, &recordingIntegrityAuditObserver{},
		advancingAuditClock(1_753_400_250_000),
	)

	_, err := auditor.RunStep(context.Background())
	require.NoError(t, err)
	state, err := auditor.RunStep(context.Background())
	require.NoError(t, err)
	require.Equal(t, backupcontract.IntegrityAuditPhaseRebase, state.Cursor.Phase)
	rebaseRevision := state.Revision

	state, err = auditor.RunStep(context.Background())
	require.NoError(t, err)
	require.Equal(t, rebaseRevision, state.Revision)
	require.Equal(t, backupcontract.IntegrityAuditPhaseRebase, state.Cursor.Phase)
	slot, found := backupcontract.FindSlotAuditState(state, 7)
	require.True(t, found)
	require.Equal(t, backupcontract.SlotAuditRebaseRequired, slot.Health)

	recovery.result = backupruntime.IntegrityAuditRebaseResult{
		Complete: true, Generation: "slot-generation-2",
	}
	state, err = auditor.RunStep(context.Background())
	require.NoError(t, err)
	require.Greater(t, state.Revision, rebaseRevision)
	require.Equal(t, uint16(8), state.Cursor.HashSlot)
	slot, found = backupcontract.FindSlotAuditState(state, 7)
	require.True(t, found)
	require.Equal(t, backupcontract.SlotAuditHealthy, slot.Health)
	require.Equal(t, "slot-generation-2", slot.Generation)
}

func TestIntegrityAuditorMarksDualLossFailedWithoutBlockingLaterSlots(t *testing.T) {
	store := &memoryIntegrityAuditStore{}
	backend := &dualLossIntegrityAuditBackend{}
	recovery := &recordingIntegrityAuditRecovery{}
	observer := &recordingIntegrityAuditObserver{}
	auditor := newIntegrityAuditor(
		t, store, backend, recovery, observer,
		advancingAuditClock(1_753_400_300_000),
	)

	_, err := auditor.RunStep(context.Background())
	require.NoError(t, err)
	state, err := auditor.RunStep(context.Background())
	require.NoError(t, err)
	require.Equal(t, backupcontract.IntegrityAuditPhaseRebase, state.Cursor.Phase)
	state, err = auditor.RunStep(context.Background())
	require.ErrorIs(t, err, backupruntime.ErrIntegrityAuditUnrecoverable)
	slot, found := backupcontract.FindSlotAuditState(state, 7)
	require.True(t, found)
	require.Equal(t, backupcontract.SlotAuditFailed, slot.Health)
	require.Equal(t, uint16(8), state.Cursor.HashSlot)
	require.Equal(t, 1, observer.unrecoverable)

	state, err = auditor.RunStep(context.Background())
	require.NoError(t, err)
	slot, found = backupcontract.FindSlotAuditState(state, 8)
	require.True(t, found)
	require.Equal(t, backupcontract.SlotAuditHealthy, slot.Health)
}

func TestIntegrityAuditorPreservesCompleteResumeAfterLastObjectDualLoss(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		recovery   *recordingIntegrityAuditRecovery
		wantHealth backupcontract.SlotAuditHealth
		wantErr    error
	}{
		{
			name: "replacement",
			recovery: &recordingIntegrityAuditRecovery{
				available: true,
				result: backupruntime.IntegrityAuditRebaseResult{
					Complete: true, Generation: "slot-generation-2",
				},
			},
			wantHealth: backupcontract.SlotAuditHealthy,
		},
		{
			name:       "unavailable",
			recovery:   &recordingIntegrityAuditRecovery{},
			wantHealth: backupcontract.SlotAuditFailed,
			wantErr:    backupruntime.ErrIntegrityAuditUnrecoverable,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			store := &memoryIntegrityAuditStore{}
			auditor := newIntegrityAuditor(
				t, store, &lastObjectDualLossIntegrityAuditBackend{},
				testCase.recovery, &recordingIntegrityAuditObserver{},
				advancingAuditClock(1_753_400_350_000),
			)
			_, err := auditor.RunStep(context.Background())
			require.NoError(t, err)
			state, err := auditor.RunStep(context.Background())
			require.NoError(t, err)
			require.Equal(
				t, backupcontract.IntegrityAuditPhaseComplete,
				state.Cursor.ResumePhase,
			)

			state, err = auditor.RunStep(context.Background())
			if testCase.wantErr == nil {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, testCase.wantErr)
			}
			require.Equal(
				t, backupcontract.IntegrityAuditPhaseComplete,
				state.Cursor.Phase,
			)
			slot, found := backupcontract.FindSlotAuditState(state, 7)
			require.True(t, found)
			require.Equal(t, testCase.wantHealth, slot.Health)
		})
	}
}

func TestIntegrityAuditorPassesCompletedCatalogCursorToNextCycle(t *testing.T) {
	store := &memoryIntegrityAuditStore{}
	backend := &cycleResumeIntegrityAuditBackend{}
	auditor := newIntegrityAuditor(
		t, store, backend, &recordingIntegrityAuditRecovery{},
		&recordingIntegrityAuditObserver{},
		advancingAuditClock(1_753_400_400_000),
	)

	_, err := auditor.RunStep(context.Background())
	require.NoError(t, err)
	state, err := auditor.RunStep(context.Background())
	require.NoError(t, err)
	require.Equal(t, backupcontract.IntegrityAuditPhaseComplete, state.Cursor.Phase)
	completedRevision := state.Revision
	state, err = auditor.RunStep(context.Background())
	require.NoError(t, err)
	require.Equal(t, backupcontract.IntegrityAuditPhaseComplete, state.Cursor.Phase)
	require.Equal(t, completedRevision, state.Revision)
	require.Equal(t, []uint64{0, 9}, backend.previousSequences)
}

func TestIntegrityAuditorProjectsDurableMetricsAfterLeaderRestart(t *testing.T) {
	cursor := auditCursor(7, "damaged-segment")
	cursor.Phase = backupcontract.IntegrityAuditPhaseRebase
	cursor.ResumeHashSlot = 8
	cursor.ResumeGeneration = "slot-generation-1"
	cursor.ResumePosition = "next-segment"
	cursor.ResumePhase = backupcontract.IntegrityAuditPhaseInspect
	store := &memoryIntegrityAuditStore{state: backupcontract.IntegrityAuditState{
		Revision: 4, DebtObjects: 17,
		LastSuccessAtUnixMillis: 1_753_400_399_000,
		Cursor:                  &cursor,
	}}
	observer := &recordingIntegrityAuditObserver{}
	auditor := newIntegrityAuditor(
		t, store, &dualLossIntegrityAuditBackend{},
		&recordingIntegrityAuditRecovery{available: true}, observer,
		advancingAuditClock(1_753_400_400_000),
	)

	state, err := auditor.RunStep(context.Background())
	require.NoError(t, err)
	require.Equal(t, uint64(4), state.Revision)
	require.Equal(t, uint64(17), observer.debt)
	require.Equal(t, int64(1_753_400_399_000), observer.lastSuccess)
}

func TestCaptureEngineFreezesOnlyAuditorOwnedSlot(t *testing.T) {
	source := &fakeContinuousSource{
		watermarks: backupruntime.SourceWatermarks{
			Metadata: backupruntime.SourceWatermark{CommittedAtUnixMillis: 1_753_400_100_000},
			Messages: backupruntime.SourceWatermark{CommittedAtUnixMillis: 1_753_400_100_000},
		},
	}
	frontiers := &fakeSlotFrontierStore{authority: testCaptureAuthority()}
	segments := &recordingSegmentCommitter{}
	gate := staticIntegrityAuditGate{slots: map[uint16]backupcontract.SlotIntegrityAuditState{
		7: {
			HashSlot: 7, Generation: "slot-generation-1",
			Health: backupcontract.SlotAuditDegraded,
		},
	}}
	engine, err := backupruntime.NewCaptureEngine(backupruntime.CaptureEngineOptions{
		RepositoryID: "backup-prod", SourceClusterID: "cluster-source",
		SourceGeneration: "source-generation-1", KMSKeyID: "kms-backup",
		InitialGeneration: "slot-generation-1", HashSlotCount: 256,
		Source: source, Frontiers: frontiers, Segments: segments,
		Clock: newAdvancingCaptureClock(), AuditGate: gate,
		Policy: backupruntime.RollingPolicy{
			TargetSegmentBytes: 64 << 20, MaxSegmentBytes: 256 << 20,
			MaxOpenDuration: 30 * time.Second, PageRecords: 1024,
		},
	})
	require.NoError(t, err)

	_, err = engine.ReconcileSlot(context.Background(), 7)
	require.ErrorIs(t, err, backupruntime.ErrIntegrityAuditFrozen)
	require.Equal(t, backupcontract.CaptureStateDegraded, engine.Status()[0].State)

	_, err = engine.ReconcileSlot(context.Background(), 8)
	require.NoError(t, err)
	statuses := engine.Status()
	require.Len(t, statuses, 2)
	require.Equal(t, uint16(8), statuses[1].HashSlot)
	require.NotEqual(t, backupcontract.CaptureStateDegraded, statuses[1].State)
}

func TestCaptureEngineRebasesAuditCorruptionAndWaitsForAuditorConfirmation(t *testing.T) {
	store := &fakeSlotFrontierStore{}
	pins := &recordingSourcePins{}
	baselines := &recordingBaselineCapturer{}
	baselines.beforeCapture = func(
		_ uint16,
		_ string,
		_ backupcontract.SlotCaptureLease,
	) {
		require.NotNil(t, store.frontier.Rebase)
		require.Equal(
			t, backupcontract.RebaseReasonAuditCorruption,
			store.frontier.Rebase.Reason,
		)
	}
	gate := staticIntegrityAuditGate{slots: map[uint16]backupcontract.SlotIntegrityAuditState{
		17: {
			HashSlot: 17, Generation: "slot-generation-1",
			Health: backupcontract.SlotAuditRebaseRequired,
		},
	}}
	engine, err := backupruntime.NewCaptureEngine(backupruntime.CaptureEngineOptions{
		RepositoryID: "backup-prod", SourceClusterID: "cluster-source",
		SourceGeneration: "source-generation-1", KMSKeyID: "kms-backup",
		InitialGeneration: "slot-generation-1", HashSlotCount: 256,
		Source: &fakeContinuousSource{}, Frontiers: store,
		Segments:  &recordingSegmentCommitter{},
		Clock:     &fakeCaptureClock{now: time.UnixMilli(1_753_400_500_000)},
		AuditGate: gate,
		Policy: backupruntime.RollingPolicy{
			TargetSegmentBytes: 64 << 20, MaxSegmentBytes: 256 << 20,
			MaxOpenDuration: 30 * time.Second, PageRecords: 1024,
		},
		Rebase: &backupruntime.RebaseOptions{
			Policy: backupruntime.SourcePinPolicy{
				MaxAge: time.Hour, MaxNodeBytes: 128 << 20,
			},
			Pins: pins, Baselines: baselines,
			Validator: &recordingGenerationValidator{},
			CostPlanner: &recordingGenerationCostPlanner{
				cost: backupruntime.GenerationCompactionCost{
					IOBytes: 96 << 30, NetworkBytes: 192 << 30,
				},
			},
		},
	})
	require.NoError(t, err)

	replacement, err := engine.ReconcileSlot(context.Background(), 17)
	require.NoError(t, err)
	require.NotEqual(t, "slot-generation-1", replacement.Generation)
	require.Nil(t, replacement.Rebase)
	require.Equal(t, 1, baselines.calls)

	frontier, err := engine.ReconcileSlot(context.Background(), 17)
	require.ErrorIs(t, err, backupruntime.ErrIntegrityAuditFrozen)
	require.Equal(t, replacement.Generation, frontier.Generation)
	require.Equal(t, 1, baselines.calls)
}

func TestCaptureIntegrityAuditRecoveryReportsOnlyPromotedReplacement(t *testing.T) {
	probe := staticIntegrityAuditSourceProbe{available: true}
	frontiers := &recordingIntegrityAuditFrontiers{
		results: []backupcontract.SlotFrontier{
			{
				HashSlot: 7, Generation: "slot-generation-1",
				Rebase: &backupcontract.SlotRebase{
					TargetGeneration: "slot-generation-2",
				},
			},
			{
				HashSlot: 7, Generation: "slot-generation-2",
				GenerationStartedAtUnixMillis: 200,
				LastPromotion: &backupcontract.SlotGenerationPromotion{
					PreviousGeneration:   "slot-generation-1",
					Reason:               backupcontract.RebaseReasonAuditCorruption,
					PromotedAtUnixMillis: 200,
				},
			},
		},
	}
	recovery, err := backupruntime.NewCaptureIntegrityAuditRecovery(probe, frontiers)
	require.NoError(t, err)
	available, err := recovery.SourceAvailable(
		context.Background(), 7, "slot-generation-1",
	)
	require.NoError(t, err)
	require.True(t, available)

	result, err := recovery.RequestRebase(
		context.Background(), 7, "slot-generation-1",
	)
	require.NoError(t, err)
	require.False(t, result.Complete)
	result, err = recovery.RequestRebase(
		context.Background(), 7, "slot-generation-1",
	)
	require.NoError(t, err)
	require.True(t, result.Complete)
	require.Equal(t, "slot-generation-2", result.Generation)
}

func TestCaptureIntegrityAuditRecoveryRejectsUnrelatedGenerationChange(t *testing.T) {
	probe := staticIntegrityAuditSourceProbe{available: true}
	frontiers := &recordingIntegrityAuditFrontiers{
		results: []backupcontract.SlotFrontier{{
			HashSlot: 7, Generation: "slot-generation-3",
			GenerationStartedAtUnixMillis: 300,
			LastPromotion: &backupcontract.SlotGenerationPromotion{
				PreviousGeneration:   "slot-generation-2",
				Reason:               backupcontract.RebaseReasonGenerationAge,
				PromotedAtUnixMillis: 300,
			},
		}},
	}
	recovery, err := backupruntime.NewCaptureIntegrityAuditRecovery(probe, frontiers)
	require.NoError(t, err)
	result, err := recovery.RequestRebase(
		context.Background(), 7, "slot-generation-1",
	)
	require.NoError(t, err)
	require.False(t, result.Complete)
}

func TestFrontierIntegrityAuditSourceProbeRejectsUnrelatedHistoricalGeneration(t *testing.T) {
	source := &fakeContinuousSource{}
	frontiers := &recordingIntegrityAuditFrontiers{
		results: []backupcontract.SlotFrontier{{
			HashSlot: 7, Generation: "slot-generation-3",
			GenerationStartedAtUnixMillis: 300,
			LastPromotion: &backupcontract.SlotGenerationPromotion{
				PreviousGeneration:   "slot-generation-2",
				Reason:               backupcontract.RebaseReasonGenerationAge,
				PromotedAtUnixMillis: 300,
			},
		}},
	}
	probe, err := backupruntime.NewFrontierIntegrityAuditSourceProbe(
		frontiers, source,
	)
	require.NoError(t, err)
	available, err := probe.SourceAvailable(
		context.Background(), 7, "slot-generation-1",
	)
	require.NoError(t, err)
	require.False(t, available)
	require.Zero(t, source.watermarkCalls)
}

func TestIntegrityAuditorRunIfLeaderDoesNoFollowerWork(t *testing.T) {
	backend := &cycleResumeIntegrityAuditBackend{}
	auditor := newIntegrityAuditor(
		t, &memoryIntegrityAuditStore{}, backend,
		&recordingIntegrityAuditRecovery{},
		&recordingIntegrityAuditObserver{},
		advancingAuditClock(1_753_400_600_000),
	)
	leadership := &integrityAuditTestLeadership{local: 1, leader: 2}

	ran, err := auditor.RunIfLeader(context.Background(), leadership)
	require.NoError(t, err)
	require.False(t, ran)
	require.Empty(t, backend.previousSequences)

	leadership.leader = 1
	ran, err = auditor.RunIfLeader(context.Background(), leadership)
	require.NoError(t, err)
	require.True(t, ran)
	require.Equal(t, []uint64{0}, backend.previousSequences)
}

type memoryIntegrityAuditStore struct {
	mu    sync.Mutex
	state backupcontract.IntegrityAuditState
}

func (s *memoryIntegrityAuditStore) LoadIntegrityAudit(context.Context) (backupcontract.IntegrityAuditState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return backupcontract.CloneIntegrityAuditState(s.state), nil
}

func (s *memoryIntegrityAuditStore) CompareAndSwapIntegrityAudit(
	_ context.Context,
	revision uint64,
	next backupcontract.IntegrityAuditState,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state.Revision != revision {
		return backupcontract.ErrStateConflict
	}
	s.state = backupcontract.CloneIntegrityAuditState(next)
	return nil
}

type staticIntegrityAuditSourceProbe struct {
	available bool
}

func (p staticIntegrityAuditSourceProbe) SourceAvailable(
	context.Context,
	uint16,
	string,
) (bool, error) {
	return p.available, nil
}

type recordingIntegrityAuditFrontiers struct {
	results []backupcontract.SlotFrontier
	calls   int
}

func (c *recordingIntegrityAuditFrontiers) Load(
	context.Context,
	uint16,
) (backupruntime.FrontierSnapshot, error) {
	index := c.calls
	c.calls++
	return backupruntime.FrontierSnapshot{
		Frontier: c.results[index], Found: true,
	}, nil
}

type integrityAuditTestLeadership struct {
	local  uint64
	leader uint64
}

func (l *integrityAuditTestLeadership) NodeID() uint64 {
	return l.local
}

func (l *integrityAuditTestLeadership) BackupControllerLeaderID() uint64 {
	return l.leader
}

type repairingIntegrityAuditBackend struct {
	repaired    bool
	repairCalls int
}

func (b *repairingIntegrityAuditBackend) Start(
	_ context.Context,
	_ *backupcontract.IntegrityAuditCursor,
) (backupcontract.IntegrityAuditCursor, uint64, error) {
	return auditCursor(7, "slot-7-object-1"), 2, nil
}

func (b *repairingIntegrityAuditBackend) Inspect(
	_ context.Context,
	cursor backupcontract.IntegrityAuditCursor,
) (backupruntime.IntegrityAuditInspection, error) {
	copies := []backupruntime.IntegrityAuditCopy{
		{Repository: "primary", Healthy: true},
		{Repository: "secondary", Healthy: b.repaired},
	}
	if !b.repaired {
		copies[1].Category = backupcontract.IntegrityCorruptionMissing
	}
	return backupruntime.IntegrityAuditInspection{
		Copies: copies, ArtifactBytes: 42, DebtObjects: 1,
		Next: auditCursor(8, "slot-8-object-1"),
	}, nil
}

func (b *repairingIntegrityAuditBackend) Repair(
	_ context.Context,
	_ backupcontract.IntegrityAuditCursor,
	repository string,
) (int64, error) {
	if repository != "secondary" {
		return 0, errors.New("wrong repair target")
	}
	b.repairCalls++
	b.repaired = true
	return 42, nil
}

type dualLossIntegrityAuditBackend struct{}

func (*dualLossIntegrityAuditBackend) Start(
	context.Context,
	*backupcontract.IntegrityAuditCursor,
) (backupcontract.IntegrityAuditCursor, uint64, error) {
	return auditCursor(7, "slot-7-object-1"), 2, nil
}

type lastObjectDualLossIntegrityAuditBackend struct{}

func (*lastObjectDualLossIntegrityAuditBackend) Start(
	_ context.Context,
	previous *backupcontract.IntegrityAuditCursor,
) (backupcontract.IntegrityAuditCursor, uint64, error) {
	if previous != nil {
		return *previous, 0, nil
	}
	return auditCursor(7, "last-object"), 1, nil
}

func (*lastObjectDualLossIntegrityAuditBackend) Inspect(
	_ context.Context,
	cursor backupcontract.IntegrityAuditCursor,
) (backupruntime.IntegrityAuditInspection, error) {
	complete := cursor
	complete.Position = "complete"
	complete.Generation = "catalog-segments-complete"
	complete.Phase = backupcontract.IntegrityAuditPhaseComplete
	return backupruntime.IntegrityAuditInspection{
		Copies: []backupruntime.IntegrityAuditCopy{
			{Repository: "primary", Category: backupcontract.IntegrityCorruptionMissing},
			{Repository: "secondary", Category: backupcontract.IntegrityCorruptionMissing},
		},
		Next: complete, ArtifactBytes: 19,
	}, nil
}

func (*lastObjectDualLossIntegrityAuditBackend) Repair(
	context.Context,
	backupcontract.IntegrityAuditCursor,
	string,
) (int64, error) {
	return 0, errors.New("repair must not run")
}

type cycleResumeIntegrityAuditBackend struct {
	previousSequences []uint64
}

func (b *cycleResumeIntegrityAuditBackend) Start(
	_ context.Context,
	previous *backupcontract.IntegrityAuditCursor,
) (backupcontract.IntegrityAuditCursor, uint64, error) {
	var sequence uint64
	if previous != nil {
		sequence = previous.CatalogSequence
	}
	b.previousSequences = append(b.previousSequences, sequence)
	if previous != nil {
		return *previous, 0, nil
	}
	return auditCursor(7, "slot-7-object-1"), 1, nil
}

func (*cycleResumeIntegrityAuditBackend) Inspect(
	_ context.Context,
	cursor backupcontract.IntegrityAuditCursor,
) (backupruntime.IntegrityAuditInspection, error) {
	complete := cursor
	complete.Position = "complete"
	complete.Phase = backupcontract.IntegrityAuditPhaseComplete
	return backupruntime.IntegrityAuditInspection{
		Copies: []backupruntime.IntegrityAuditCopy{
			{Repository: "primary", Healthy: true},
			{Repository: "secondary", Healthy: true},
		},
		Next: complete, ArtifactBytes: 1,
	}, nil
}

func (*cycleResumeIntegrityAuditBackend) Repair(
	context.Context,
	backupcontract.IntegrityAuditCursor,
	string,
) (int64, error) {
	return 0, errors.New("repair must not run")
}

func (*dualLossIntegrityAuditBackend) Inspect(
	_ context.Context,
	cursor backupcontract.IntegrityAuditCursor,
) (backupruntime.IntegrityAuditInspection, error) {
	if cursor.HashSlot == 8 {
		complete := auditCursor(8, "complete")
		complete.Phase = backupcontract.IntegrityAuditPhaseComplete
		return backupruntime.IntegrityAuditInspection{
			Copies: []backupruntime.IntegrityAuditCopy{
				{Repository: "primary", Healthy: true},
				{Repository: "secondary", Healthy: true},
			},
			Next: complete, ArtifactBytes: 11,
		}, nil
	}
	return backupruntime.IntegrityAuditInspection{
		Copies: []backupruntime.IntegrityAuditCopy{
			{Repository: "primary", Category: backupcontract.IntegrityCorruptionCiphertext},
			{Repository: "secondary", Category: backupcontract.IntegrityCorruptionMissing},
		},
		Next: auditCursor(8, "slot-8-object-1"), ArtifactBytes: 19, DebtObjects: 1,
	}, nil
}

func (*dualLossIntegrityAuditBackend) Repair(
	context.Context,
	backupcontract.IntegrityAuditCursor,
	string,
) (int64, error) {
	return 0, errors.New("repair must not run")
}

type recordingIntegrityAuditRecovery struct {
	available   bool
	rebaseCalls int
	result      backupruntime.IntegrityAuditRebaseResult
}

func (r *recordingIntegrityAuditRecovery) SourceAvailable(
	context.Context,
	uint16,
	string,
) (bool, error) {
	return r.available, nil
}

func (r *recordingIntegrityAuditRecovery) RequestRebase(
	context.Context,
	uint16,
	string,
) (backupruntime.IntegrityAuditRebaseResult, error) {
	r.rebaseCalls++
	return r.result, nil
}

type recordingIntegrityAuditObserver struct {
	debt          uint64
	lastSuccess   int64
	corruptions   []string
	repairBytes   int64
	unrecoverable int
}

func (o *recordingIntegrityAuditObserver) SetBackupAuditDebt(debt uint64) {
	o.debt = debt
}

func (o *recordingIntegrityAuditObserver) SetBackupAuditLastSuccess(at int64) {
	o.lastSuccess = at
}

func (o *recordingIntegrityAuditObserver) ObserveBackupAuditCorruption(category, repository string) {
	o.corruptions = append(o.corruptions, category+":"+repository)
}

func (o *recordingIntegrityAuditObserver) AddBackupAuditRepairBytes(_ string, bytes int64) {
	o.repairBytes += bytes
}

func (o *recordingIntegrityAuditObserver) ObserveBackupAuditUnrecoverable() {
	o.unrecoverable++
}

type staticIntegrityAuditGate struct {
	slots map[uint16]backupcontract.SlotIntegrityAuditState
}

func (g staticIntegrityAuditGate) AuditSlotState(
	_ context.Context,
	hashSlot uint16,
) (backupcontract.SlotIntegrityAuditState, bool, error) {
	state, found := g.slots[hashSlot]
	return state, found, nil
}

func auditCursor(hashSlot uint16, position string) backupcontract.IntegrityAuditCursor {
	return backupcontract.IntegrityAuditCursor{
		CycleID: "audit-cycle-1", CatalogSequence: 9,
		HashSlot: hashSlot, Generation: "slot-generation-1",
		Position: position, Phase: backupcontract.IntegrityAuditPhaseInspect,
	}
}

func advancingAuditClock(start int64) func() time.Time {
	current := start
	return func() time.Time {
		current += 1000
		return time.UnixMilli(current).UTC()
	}
}

func newIntegrityAuditor(
	t *testing.T,
	store backupruntime.IntegrityAuditStateStore,
	backend backupruntime.IntegrityAuditBackend,
	recovery backupruntime.IntegrityAuditRecovery,
	observer backupruntime.IntegrityAuditObserver,
	now func() time.Time,
) *backupruntime.IntegrityAuditor {
	t.Helper()
	auditor, err := backupruntime.NewIntegrityAuditor(backupruntime.IntegrityAuditorOptions{
		State: store, Backend: backend, Recovery: recovery,
		Observer: observer, Now: now,
	})
	require.NoError(t, err)
	return auditor
}
