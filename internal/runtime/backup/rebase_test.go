package backup_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	backupruntime "github.com/WuKongIM/WuKongIM/internal/runtime/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
)

func TestCaptureEngineRebasesOneSlotAfterPinAgeWithoutReplacingHealthyGenerationEarly(t *testing.T) {
	store := &fakeSlotFrontierStore{}
	pins := &recordingSourcePins{
		observation: backupruntime.SourcePinObservation{Age: 2 * time.Hour, PinnedBytes: 32 << 20, NodePinnedBytes: 64 << 20},
	}
	baselines := &recordingBaselineCapturer{}
	baselines.beforeCapture = func(hashSlot uint16, generation string, lease backupcontract.SlotCaptureLease) {
		if hashSlot != 17 || generation == lease.Generation ||
			store.frontier.Generation != "slot-generation-1" ||
			store.frontier.Rebase == nil ||
			store.frontier.Baseline != nil {
			t.Fatalf("baseline started after early generation replacement: frontier=%#v", store.frontier)
		}
		if pins.releases != 1 {
			t.Fatalf("pin releases = %d, want release before baseline capture", pins.releases)
		}
	}
	engine := newRebaseTestEngine(t, store, &fakeContinuousSource{}, pins, baselines)

	frontier, err := engine.ReconcileSlot(context.Background(), 17)
	if err != nil {
		t.Fatalf("ReconcileSlot() error = %v", err)
	}
	if frontier.Generation == "slot-generation-1" || frontier.Rebase != nil ||
		frontier.Baseline == nil || frontier.Messages.BaselineCursorHead == nil ||
		frontier.Metadata.SourceHighWatermark != 11 {
		t.Fatalf("promoted frontier = %#v", frontier)
	}
	if pins.observes != 1 || pins.releases != 1 || baselines.calls != 1 || store.commits != 2 {
		t.Fatalf("rebase calls pin=%d/%d baseline=%d commits=%d", pins.observes, pins.releases, baselines.calls, store.commits)
	}
}

func TestCaptureEngineRetriesPendingRebaseAfterProcessRestart(t *testing.T) {
	store := &fakeSlotFrontierStore{}
	pins := &recordingSourcePins{
		observation: backupruntime.SourcePinObservation{Age: 2 * time.Hour, PinnedBytes: 32 << 20, NodePinnedBytes: 64 << 20},
	}
	firstBaselines := &recordingBaselineCapturer{err: errors.New("repository unavailable")}
	first := newRebaseTestEngine(t, store, &fakeContinuousSource{}, pins, firstBaselines)
	if _, err := first.ReconcileSlot(context.Background(), 17); err == nil {
		t.Fatal("ReconcileSlot(first) error = nil")
	}
	pendingGeneration := store.frontier.Rebase.TargetGeneration
	if store.frontier.Generation != "slot-generation-1" || store.frontier.Baseline != nil {
		t.Fatalf("failed rebase corrupted healthy frontier = %#v", store.frontier)
	}

	secondBaselines := &recordingBaselineCapturer{}
	restarted := newRebaseTestEngine(t, store, &fakeContinuousSource{}, pins, secondBaselines)
	frontier, err := restarted.ReconcileSlot(context.Background(), 17)
	if err != nil {
		t.Fatalf("ReconcileSlot(restarted) error = %v", err)
	}
	if frontier.Generation != pendingGeneration || frontier.Rebase != nil ||
		secondBaselines.calls != 1 || pins.observes != 1 {
		t.Fatalf("restart frontier=%#v baseline_calls=%d pin_observes=%d", frontier, secondBaselines.calls, pins.observes)
	}
}

func TestCaptureEngineFencesOldLeaderPromotionAndNewLeaderResumesRebase(t *testing.T) {
	store := &fakeSlotFrontierStore{}
	pins := &recordingSourcePins{
		observation: backupruntime.SourcePinObservation{Age: 2 * time.Hour, PinnedBytes: 32 << 20, NodePinnedBytes: 64 << 20},
	}
	baselines := &recordingBaselineCapturer{}
	baselines.beforeCapture = func(uint16, string, backupcontract.SlotCaptureLease) {
		if baselines.calls == 1 {
			store.authority = backupruntime.SlotCaptureAuthority{
				SlotID: 1, LeaderTerm: 8, ConfigEpoch: 3, HolderNodeID: 2,
			}
		}
	}
	engine := newRebaseTestEngine(t, store, &fakeContinuousSource{}, pins, baselines)
	if _, err := engine.ReconcileSlot(context.Background(), 17); !errors.Is(err, backupruntime.ErrCaptureLeaseFenced) {
		t.Fatalf("ReconcileSlot(old leader) error = %v", err)
	}
	oldGeneration := store.frontier.Generation
	targetGeneration := store.frontier.Rebase.TargetGeneration
	if oldGeneration != "slot-generation-1" || store.frontier.Baseline != nil {
		t.Fatalf("old leader published baseline = %#v", store.frontier)
	}

	frontier, err := engine.ReconcileSlot(context.Background(), 17)
	if err != nil {
		t.Fatalf("ReconcileSlot(new leader) error = %v", err)
	}
	if frontier.Generation == targetGeneration || frontier.Lease.HolderNodeID != 2 ||
		frontier.Lease.LeaderTerm != 8 || frontier.Lease.Sequence != 2 {
		t.Fatalf("new leader frontier = %#v", frontier)
	}
}

func TestCaptureEngineRotatesPublishedBaselineThatLostItsSourceCut(t *testing.T) {
	store := &fakeSlotFrontierStore{}
	pins := &recordingSourcePins{
		observation: backupruntime.SourcePinObservation{Age: 2 * time.Hour},
	}
	baselines := &recordingBaselineCapturer{
		err: errors.New("promotion interrupted after immutable publish"),
	}
	engine := newRebaseTestEngine(t, store, &fakeContinuousSource{}, pins, baselines)
	if _, err := engine.ReconcileSlot(context.Background(), 17); err == nil {
		t.Fatal("ReconcileSlot(first) error = nil")
	}
	oldTarget := store.frontier.Rebase.TargetGeneration
	baselines.err = nil
	baselines.errs = []error{backupruntime.ErrCaptureSourceCompacted, nil}

	frontier, err := engine.ReconcileSlot(context.Background(), 17)
	if err != nil {
		t.Fatalf("ReconcileSlot(retry) error = %v", err)
	}
	if frontier.Generation == oldTarget || frontier.Rebase != nil ||
		baselines.lastGeneration == oldTarget {
		t.Fatalf("retry reused stale immutable target: old=%q frontier=%#v last=%q", oldTarget, frontier, baselines.lastGeneration)
	}
}

func TestCaptureEngineRebasesDurableCursorAfterPhysicalSlotRemap(t *testing.T) {
	store := &fakeSlotFrontierStore{}
	pins := &recordingSourcePins{}
	source := &fakeContinuousSource{watermarks: backupruntime.SourceWatermarks{
		Metadata: backupruntime.SourceWatermark{CommittedAtUnixMillis: 1_753_400_100_000},
		Messages: backupruntime.SourceWatermark{CommittedAtUnixMillis: 1_753_400_100_000},
	}}
	baselines := &recordingBaselineCapturer{}
	engine := newRebaseTestEngine(t, store, source, pins, baselines)
	first, err := engine.ReconcileSlot(context.Background(), 17)
	if err != nil {
		t.Fatalf("ReconcileSlot(initial) error = %v", err)
	}
	if first.SourceSlotID != 1 {
		t.Fatalf("initial source Slot = %d, want 1", first.SourceSlotID)
	}
	store.authority = backupruntime.SlotCaptureAuthority{
		SlotID: 2, LeaderTerm: 8, ConfigEpoch: 4, HolderNodeID: 2,
	}

	remapped, err := engine.ReconcileSlot(context.Background(), 17)
	if err != nil {
		t.Fatalf("ReconcileSlot(remap) error = %v", err)
	}
	if remapped.SourceSlotID != 2 || remapped.Lease.SlotID != 2 ||
		remapped.Baseline == nil || remapped.Rebase != nil ||
		baselines.calls != 1 || pins.adopts != 1 {
		t.Fatalf(
			"remapped frontier=%#v baseline_calls=%d lease_adoptions=%d",
			remapped, baselines.calls, pins.adopts,
		)
	}
}

func TestCaptureEngineRebasesWhenRestartFindsCleanupPastCursor(t *testing.T) {
	store := &fakeSlotFrontierStore{}
	pins := &recordingSourcePins{err: backupruntime.ErrCaptureSourceCompacted}
	baselines := &recordingBaselineCapturer{}
	engine := newRebaseTestEngine(t, store, &fakeContinuousSource{}, pins, baselines)

	frontier, err := engine.ReconcileSlot(context.Background(), 17)
	if err != nil {
		t.Fatalf("ReconcileSlot() error = %v", err)
	}
	if frontier.Baseline == nil || baselines.lastGeneration == "slot-generation-1" ||
		pins.releases != 1 {
		t.Fatalf("cleanup recovery frontier=%#v pins=%#v baseline=%#v", frontier, pins, baselines)
	}
}

func TestCaptureEngineReleasesFormerLeaderLocalPin(t *testing.T) {
	store := &fakeSlotFrontierStore{}
	pins := &recordingSourcePins{}
	source := &fakeContinuousSource{watermarks: backupruntime.SourceWatermarks{
		Metadata: backupruntime.SourceWatermark{CommittedAtUnixMillis: 1_753_400_100_000},
		Messages: backupruntime.SourceWatermark{CommittedAtUnixMillis: 1_753_400_100_000},
	}}
	engine := newRebaseTestEngine(t, store, source, pins, &recordingBaselineCapturer{})
	if _, err := engine.ReconcileSlot(context.Background(), 17); err != nil {
		t.Fatalf("ReconcileSlot(initial) error = %v", err)
	}
	store.acquireErr = backupruntime.ErrCaptureNotLeader

	if _, err := engine.ReconcileSlot(context.Background(), 17); !errors.Is(err, backupruntime.ErrCaptureNotLeader) {
		t.Fatalf("ReconcileSlot(former leader) error = %v", err)
	}
	if pins.obsoleteReleases != 1 {
		t.Fatalf("obsolete pin releases = %d, want 1", pins.obsoleteReleases)
	}
}

func TestCaptureEngineCompactsOneSlotAtIndependentGenerationThresholds(t *testing.T) {
	now := time.UnixMilli(1_753_400_200_000)
	tests := []struct {
		name   string
		reason string
		mutate func(*backupcontract.SlotFrontier)
	}{
		{
			name: "delta bytes", reason: backupcontract.RebaseReasonGenerationBytes,
			mutate: func(frontier *backupcontract.SlotFrontier) {
				frontier.Metadata.CapturedPlaintextBytes = backupruntime.DefaultGenerationMaxDeltaBytes
			},
		},
		{
			name: "delta reaches smaller baseline", reason: backupcontract.RebaseReasonGenerationBytes,
			mutate: func(frontier *backupcontract.SlotFrontier) {
				cursor := validRuntimeSegmentReference("f")
				frontier.Baseline = &backupcontract.SlotBaselineReference{
					PlaintextBytes: 100,
					Partition: backupartifact.PartitionReference{
						HashSlot: 17, Key: "partition-manifests/slot-generation-1/00017.json",
						SHA256: strings.Repeat("9", 64), Bytes: 512,
						ObjectCount: 2, CiphertextBytes: 256,
						Evidence: backupartifact.PartitionEvidence{Version: backupartifact.PartitionEvidenceVersion},
					},
				}
				frontier.Messages.BaselineCursorHead = &cursor
				frontier.Messages.CapturedPlaintextBytes = 100
			},
		},
		{
			name: "segment count", reason: backupcontract.RebaseReasonGenerationSegments,
			mutate: func(frontier *backupcontract.SlotFrontier) {
				frontier.Metadata.Sequence = backupruntime.DefaultGenerationMaxSegments
				head := validRuntimeSegmentReference("a")
				frontier.Metadata.Head = &head
			},
		},
		{
			name: "generation age", reason: backupcontract.RebaseReasonGenerationAge,
			mutate: func(frontier *backupcontract.SlotFrontier) {
				frontier.GenerationStartedAtUnixMillis = now.Add(-backupruntime.DefaultGenerationMaxAge).UnixMilli()
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := &fakeSlotFrontierStore{}
			_, err := store.AcquireLease(context.Background(), 17, "slot-generation-1", now.UnixMilli())
			if err != nil {
				t.Fatalf("AcquireLease() error = %v", err)
			}
			test.mutate(&store.frontier)
			pins := &recordingSourcePins{}
			baselines := &recordingBaselineCapturer{}
			baselines.beforeCapture = func(_ uint16, _ string, _ backupcontract.SlotCaptureLease) {
				if store.frontier.Rebase == nil || store.frontier.Rebase.Reason != test.reason {
					t.Fatalf("pending rebase = %#v, want reason %q", store.frontier.Rebase, test.reason)
				}
			}
			engine := newRebaseTestEngine(t, store, &fakeContinuousSource{}, pins, baselines)

			frontier, err := engine.ReconcileSlot(context.Background(), 17)
			if err != nil {
				t.Fatalf("ReconcileSlot() error = %v", err)
			}
			if frontier.Generation == "slot-generation-1" || frontier.Rebase != nil ||
				frontier.GenerationStartedAtUnixMillis != frontier.UpdatedAtUnixMillis ||
				frontier.Metadata.CapturedPlaintextBytes != 0 ||
				frontier.Messages.CapturedPlaintextBytes != 0 {
				t.Fatalf("compacted frontier = %#v", frontier)
			}
			if pins.observes != 0 || pins.releases != 1 || baselines.calls != 1 {
				t.Fatalf("compaction calls observe=%d release=%d baseline=%d", pins.observes, pins.releases, baselines.calls)
			}
		})
	}
}

func TestCaptureEngineKeepsOldGenerationWhileCompactionBudgetIsUnavailable(t *testing.T) {
	now := time.UnixMilli(1_753_400_200_000)
	store := &fakeSlotFrontierStore{}
	_, err := store.AcquireLease(context.Background(), 17, "slot-generation-1", now.UnixMilli())
	if err != nil {
		t.Fatalf("AcquireLease() error = %v", err)
	}
	store.frontier.Metadata.CapturedPlaintextBytes = backupruntime.DefaultGenerationMaxDeltaBytes
	pins := &recordingSourcePins{}
	baselines := &recordingBaselineCapturer{}
	budget := &recordingCompactionBudget{}
	engine := newRebaseTestEngineWithBudget(t, store, &fakeContinuousSource{}, pins, baselines, budget)

	if _, err := engine.ReconcileSlot(context.Background(), 17); !errors.Is(err, backupruntime.ErrCompactionBudget) {
		t.Fatalf("ReconcileSlot(blocked) error = %v", err)
	}
	if store.frontier.Generation != "slot-generation-1" || store.frontier.Rebase == nil ||
		pins.releases != 0 || baselines.calls != 0 ||
		budget.lastCost.IOBytes != 96<<30 ||
		budget.lastCost.NetworkBytes != 192<<30 {
		t.Fatalf(
			"budget failure changed authority or underestimated work: frontier=%#v releases=%d baselines=%d cost=%#v",
			store.frontier, pins.releases, baselines.calls, budget.lastCost,
		)
	}

	budget.allow = true
	frontier, err := engine.ReconcileSlot(context.Background(), 17)
	if err != nil {
		t.Fatalf("ReconcileSlot(retry) error = %v", err)
	}
	if frontier.Generation == "slot-generation-1" || budget.acquires != 2 ||
		budget.releases != 1 || pins.releases != 1 || baselines.calls != 1 {
		t.Fatalf(
			"retry frontier=%#v budget=%d/%d pins=%d baselines=%d",
			frontier, budget.acquires, budget.releases, pins.releases, baselines.calls,
		)
	}
}

func TestCaptureEngineKeepsOldGenerationUntilReplacementAuditPasses(t *testing.T) {
	now := time.UnixMilli(1_753_400_200_000)
	store := &fakeSlotFrontierStore{}
	_, err := store.AcquireLease(context.Background(), 17, "slot-generation-1", now.UnixMilli())
	if err != nil {
		t.Fatalf("AcquireLease() error = %v", err)
	}
	store.frontier.Metadata.CapturedPlaintextBytes = backupruntime.DefaultGenerationMaxDeltaBytes
	pins := &recordingSourcePins{}
	baselines := &recordingBaselineCapturer{}
	auditErr := errors.New("replacement audit pending")
	validator := &recordingGenerationValidator{err: auditErr}
	engine := newRebaseTestEngineWithOptions(
		t, store, &fakeContinuousSource{}, pins, baselines, nil, validator,
	)

	if _, err := engine.ReconcileSlot(context.Background(), 17); !errors.Is(err, auditErr) {
		t.Fatalf("ReconcileSlot(audit pending) error = %v", err)
	}
	if store.frontier.Generation != "slot-generation-1" || store.frontier.Rebase == nil ||
		store.frontier.Baseline != nil || baselines.calls != 1 ||
		baselines.materializations != 1 || validator.calls != 1 {
		t.Fatalf(
			"audit failure changed authority: frontier=%#v baselines=%d validations=%d",
			store.frontier, baselines.calls, validator.calls,
		)
	}

	validator.err = nil
	frontier, err := engine.ReconcileSlot(context.Background(), 17)
	if err != nil {
		t.Fatalf("ReconcileSlot(audited retry) error = %v", err)
	}
	if frontier.Generation == "slot-generation-1" || frontier.Rebase != nil ||
		validator.calls != 2 || baselines.materializations != 1 {
		t.Fatalf(
			"audited frontier=%#v validations=%d materializations=%d",
			frontier, validator.calls, baselines.materializations,
		)
	}
}

func newRebaseTestEngine(
	t *testing.T,
	store *fakeSlotFrontierStore,
	source backupruntime.ContinuousSource,
	pins *recordingSourcePins,
	baselines *recordingBaselineCapturer,
) *backupruntime.CaptureEngine {
	return newRebaseTestEngineWithOptions(t, store, source, pins, baselines, nil, nil)
}

func newRebaseTestEngineWithBudget(
	t *testing.T,
	store *fakeSlotFrontierStore,
	source backupruntime.ContinuousSource,
	pins *recordingSourcePins,
	baselines *recordingBaselineCapturer,
	budget backupruntime.GenerationCompactionBudget,
) *backupruntime.CaptureEngine {
	return newRebaseTestEngineWithOptions(t, store, source, pins, baselines, budget, nil)
}

func newRebaseTestEngineWithOptions(
	t *testing.T,
	store *fakeSlotFrontierStore,
	source backupruntime.ContinuousSource,
	pins *recordingSourcePins,
	baselines *recordingBaselineCapturer,
	budget backupruntime.GenerationCompactionBudget,
	validator backupruntime.GenerationPromotionValidator,
) *backupruntime.CaptureEngine {
	t.Helper()
	if validator == nil {
		validator = &recordingGenerationValidator{}
	}
	costPlanner := &recordingGenerationCostPlanner{
		cost: backupruntime.GenerationCompactionCost{
			IOBytes: 96 << 30, NetworkBytes: 192 << 30,
		},
	}
	engine, err := backupruntime.NewCaptureEngine(backupruntime.CaptureEngineOptions{
		RepositoryID: "backup-prod", SourceClusterID: "cluster-source",
		SourceGeneration: "source-generation-1", KMSKeyID: "kms-backup",
		InitialGeneration: "slot-generation-1", HashSlotCount: 256,
		Source: source, Frontiers: store, Segments: &recordingSegmentCommitter{},
		Clock: &fakeCaptureClock{now: time.UnixMilli(1_753_400_200_000)},
		Policy: backupruntime.RollingPolicy{
			TargetSegmentBytes: 64 << 20, MaxSegmentBytes: 256 << 20,
			MaxOpenDuration: 30 * time.Second, PageRecords: 1024,
		},
		Rebase: &backupruntime.RebaseOptions{
			Policy: backupruntime.SourcePinPolicy{MaxAge: time.Hour, MaxNodeBytes: 128 << 20},
			Pins:   pins, Baselines: baselines, Validator: validator,
			CostPlanner: costPlanner, Budget: budget,
		},
	})
	if err != nil {
		t.Fatalf("NewCaptureEngine() error = %v", err)
	}
	return engine
}

type recordingGenerationValidator struct {
	err   error
	calls int
}

func (v *recordingGenerationValidator) ValidateGenerationReplacement(
	context.Context,
	backupcontract.SlotFrontier,
	backupruntime.MaterializedBaseline,
) error {
	v.calls++
	return v.err
}

type recordingGenerationCostPlanner struct {
	cost backupruntime.GenerationCompactionCost
	err  error
}

func (p *recordingGenerationCostPlanner) PlanGenerationCompaction(
	context.Context,
	backupcontract.SlotFrontier,
) (backupruntime.GenerationCompactionCost, error) {
	return p.cost, p.err
}

type recordingCompactionBudget struct {
	allow    bool
	acquires int
	releases int
	lastCost backupruntime.GenerationCompactionCost
}

func (b *recordingCompactionBudget) TryAcquire(cost backupruntime.GenerationCompactionCost) bool {
	b.acquires++
	b.lastCost = cost
	return b.allow
}

func (b *recordingCompactionBudget) Release(backupruntime.GenerationCompactionCost) {
	b.releases++
}

type recordingSourcePins struct {
	observation      backupruntime.SourcePinObservation
	err              error
	observes         int
	releases         int
	adopts           int
	obsoleteReleases int
}

func (p *recordingSourcePins) Observe(_ context.Context, _ uint16, _ backupcontract.SlotCaptureLease, frontier backupcontract.SlotFrontier) (backupruntime.SourcePinObservation, error) {
	if frontier.Metadata.SourceCursor == "11" {
		return backupruntime.SourcePinObservation{}, nil
	}
	p.observes++
	return p.observation, p.err
}

func (p *recordingSourcePins) Release(context.Context, uint16, backupcontract.SlotCaptureLease) (backupruntime.SourcePinObservation, error) {
	p.releases++
	return backupruntime.SourcePinObservation{}, nil
}

func (p *recordingSourcePins) AdoptLease(context.Context, uint16, backupcontract.SlotCaptureLease) (backupruntime.SourcePinObservation, error) {
	p.adopts++
	return backupruntime.SourcePinObservation{}, nil
}

func (p *recordingSourcePins) ReleaseObsolete(context.Context, uint16) (backupruntime.SourcePinObservation, error) {
	p.obsoleteReleases++
	return backupruntime.SourcePinObservation{}, nil
}

type recordingBaselineCapturer struct {
	err              error
	errs             []error
	calls            int
	materializations int
	lastGeneration   string
	beforeCapture    func(uint16, string, backupcontract.SlotCaptureLease)
	cached           map[string]backupruntime.MaterializedBaseline
}

func (c *recordingBaselineCapturer) CaptureBaseline(
	ctx context.Context,
	hashSlot uint16,
	generation string,
	_ uint64,
	lease backupcontract.SlotCaptureLease,
	pinCut func(context.Context, uint64) error,
) (backupruntime.MaterializedBaseline, error) {
	c.calls++
	c.lastGeneration = generation
	if c.beforeCapture != nil {
		c.beforeCapture(hashSlot, generation, lease)
	}
	if c.err != nil {
		return backupruntime.MaterializedBaseline{}, c.err
	}
	if len(c.errs) > 0 {
		err := c.errs[0]
		c.errs = c.errs[1:]
		if err != nil {
			return backupruntime.MaterializedBaseline{}, err
		}
	}
	if baseline, found := c.cached[generation]; found {
		if err := pinCut(ctx, baseline.Metadata.SourceHighWatermark); err != nil {
			return backupruntime.MaterializedBaseline{}, err
		}
		return baseline, nil
	}
	if err := pinCut(ctx, 11); err != nil {
		return backupruntime.MaterializedBaseline{}, err
	}
	cursor := validRuntimeSegmentReference("e")
	baseline := backupruntime.MaterializedBaseline{
		Generation: generation,
		Reference: backupcontract.SlotBaselineReference{
			PlaintextBytes: 2048,
			Partition: backupartifact.PartitionReference{
				HashSlot: hashSlot, Key: "partition-manifests/" + generation + "/00017.json",
				SHA256: cursor.SegmentID, Bytes: 512, ObjectCount: 3, CiphertextBytes: 1024,
				Evidence: backupartifact.PartitionEvidence{Version: backupartifact.PartitionEvidenceVersion},
			},
		},
		Metadata: backupcontract.StreamFrontier{
			SourceCursor: "11", SourceHighWatermark: 11,
			WatermarkAtUnixMillis: 1_753_400_190_000,
		},
		Messages: backupcontract.StreamFrontier{
			BaselineCursorHead:    &cursor,
			WatermarkAtUnixMillis: 1_753_400_180_000,
		},
		WatermarkAtUnixMillis: 1_753_400_180_000,
	}
	if c.cached == nil {
		c.cached = make(map[string]backupruntime.MaterializedBaseline)
	}
	c.cached[generation] = baseline
	c.materializations++
	return baseline, nil
}
