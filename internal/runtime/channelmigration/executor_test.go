package channelmigration

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	slotmeta "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

func TestExecutorClaimsOnlyRunnableTask(t *testing.T) {
	now := time.UnixMilli(2000)
	store := newFakeExecutorStore(
		executorTestTask("runnable", "ch-runnable", slotmeta.ChannelMigrationStatusPending, now.Add(-time.Second)),
		withNextRun(executorTestTask("future", "ch-future", slotmeta.ChannelMigrationStatusPending, now.Add(-time.Second)), now.Add(time.Second)),
		withOwner(executorTestTask("owned", "ch-owned", slotmeta.ChannelMigrationStatusRunning, now.Add(-time.Second)), 11, now.Add(time.Second)),
		withBlocker(executorTestTask("blocked", "ch-blocked", slotmeta.ChannelMigrationStatusBlocked, now.Add(-time.Second))),
		withCompleted(executorTestTask("done", "ch-done", slotmeta.ChannelMigrationStatusCompleted, now.Add(-time.Second)), now.Add(-500*time.Millisecond)),
	)
	executor := newExecutorTestHarness(store, newFakeSlotLeadership().withDefault(true), now, 9)

	err := executor.Tick(context.Background())

	require.NoError(t, err)
	require.Equal(t, []string{"runnable"}, store.claimedTaskIDs())
	require.Equal(t, []string{"runnable"}, store.advancedTaskIDs())
}

func TestExecutorListsOnlyTasksForLocallyLedSlots(t *testing.T) {
	now := time.UnixMilli(3000)
	store := newFakeExecutorStore(
		executorTestTask("local", "ch-local", slotmeta.ChannelMigrationStatusPending, now.Add(-time.Second)),
		executorTestTask("remote", "ch-remote", slotmeta.ChannelMigrationStatusPending, now.Add(-time.Second)),
	)
	slots := newFakeSlotLeadership().
		withChannelSlot("ch-local", 1).
		withChannelSlot("ch-remote", 2).
		withLeader(1, true).
		withLeader(2, false)
	executor := newExecutorTestHarness(store, slots, now, 9)

	err := executor.Tick(context.Background())

	require.NoError(t, err)
	require.Equal(t, []string{"local"}, store.claimedTaskIDs())
	require.Equal(t, []string{"local"}, store.advancedTaskIDs())
	require.Equal(t, []uint32{1, 1, 2}, slots.checkedSlots())
}

func TestExecutorRejectsClaimWhenLocalNodeIsNotSlotLeader(t *testing.T) {
	now := time.UnixMilli(4000)
	store := newFakeExecutorStore(executorTestTask("task", "ch", slotmeta.ChannelMigrationStatusPending, now.Add(-time.Second)))
	executor := newExecutorTestHarness(store, newFakeSlotLeadership().withDefault(false), now, 9)

	err := executor.Tick(context.Background())

	require.NoError(t, err)
	require.Empty(t, store.claimedTaskIDs())
	require.Empty(t, store.advancedTaskIDs())
}

func TestExecutorStopsBeforeSideEffectsAfterSlotLeadershipLost(t *testing.T) {
	now := time.UnixMilli(5000)
	store := newFakeExecutorStore(executorTestTask("task", "ch", slotmeta.ChannelMigrationStatusPending, now.Add(-time.Second)))
	slots := newFakeSlotLeadership().withChannelSlot("ch", 3).withLeaderSequence(3, true, false)
	executor := newExecutorTestHarness(store, slots, now, 9)

	err := executor.Tick(context.Background())

	require.NoError(t, err)
	require.Equal(t, []string{"task"}, store.claimedTaskIDs())
	require.Empty(t, store.advancedTaskIDs())
	require.Equal(t, []uint32{3, 3}, slots.checkedSlots())
}

func TestExecutorOwnerLeaseHandoffAfterExpiry(t *testing.T) {
	now := time.UnixMilli(6000)
	expired := withOwner(
		executorTestTask("task", "ch", slotmeta.ChannelMigrationStatusRunning, now.Add(-time.Second)),
		11,
		now.Add(-time.Millisecond),
	)
	store := newFakeExecutorStore(expired)
	executor := newExecutorTestHarness(store, newFakeSlotLeadership().withDefault(true), now, 9)

	err := executor.Tick(context.Background())

	require.NoError(t, err)
	require.Len(t, store.claims, 1)
	require.Equal(t, uint64(11), store.claims[0].Guard.ExpectedOwnerNodeID)
	require.Equal(t, expired.OwnerLeaseUntilMS, store.claims[0].Guard.ExpectedOwnerLeaseUntilMS)
	require.Equal(t, uint64(9), store.claims[0].OwnerNodeID)
	require.Greater(t, store.claims[0].OwnerLeaseUntilMS, now.UnixMilli())
}

func TestDuplicateExecutorRaceSingleOwner(t *testing.T) {
	now := time.UnixMilli(7000)
	store := newFakeExecutorStore(executorTestTask("task", "ch", slotmeta.ChannelMigrationStatusPending, now.Add(-time.Second)))
	releaseLists := make(chan struct{})
	var listed sync.WaitGroup
	listed.Add(2)
	store.onList = func() {
		listed.Done()
		<-releaseLists
	}
	slots := newFakeSlotLeadership().withDefault(true)
	left := newExecutorTestHarness(store, slots, now, 9)
	right := newExecutorTestHarness(store, slots, now, 10)

	errs := make(chan error, 2)
	go func() { errs <- left.Tick(context.Background()) }()
	go func() { errs <- right.Tick(context.Background()) }()
	listed.Wait()
	close(releaseLists)

	require.NoError(t, <-errs)
	require.NoError(t, <-errs)
	require.Len(t, store.claims, 2)
	require.Len(t, store.successfulClaims(), 1)
	require.Equal(t, 1, store.staleClaimCount())
	require.Len(t, store.advancedTaskIDs(), 1)
	require.Contains(t, []uint64{9, 10}, store.task("task").OwnerNodeID)
}

func TestExecutorDoesNotClearFenceOnOwnerLeaseExpiry(t *testing.T) {
	now := time.UnixMilli(8000)
	task := withOwner(
		executorTestTask("task", "ch", slotmeta.ChannelMigrationStatusRunning, now.Add(-time.Second)),
		11,
		now.Add(-time.Millisecond),
	)
	task.Phase = slotmeta.ChannelMigrationPhaseDrainLeader
	task.FenceToken = "fence-token"
	task.FenceVersion = 7
	task.FenceUntilMS = now.Add(time.Minute).UnixMilli()
	store := newFakeExecutorStore(task)
	executor := newExecutorTestHarness(store, newFakeSlotLeadership().withDefault(true), now, 9)

	err := executor.Tick(context.Background())

	require.NoError(t, err)
	after := store.task("task")
	require.Equal(t, "fence-token", after.FenceToken)
	require.Equal(t, uint64(7), after.FenceVersion)
	require.Equal(t, task.FenceUntilMS, after.FenceUntilMS)
}

func TestExecutorHonorsGlobalSourceAndTargetConcurrencyLimits(t *testing.T) {
	now := time.UnixMilli(9000)
	store := newFakeExecutorStore(
		withNodes(executorTestTask("first", "ch-first", slotmeta.ChannelMigrationStatusPending, now.Add(-time.Second)), 1, 2),
		withNodes(executorTestTask("same-source", "ch-same-source", slotmeta.ChannelMigrationStatusPending, now.Add(-time.Second)), 1, 3),
		withNodes(executorTestTask("same-target", "ch-same-target", slotmeta.ChannelMigrationStatusPending, now.Add(-time.Second)), 4, 2),
		withNodes(executorTestTask("independent", "ch-independent", slotmeta.ChannelMigrationStatusPending, now.Add(-time.Second)), 5, 6),
	)
	executor := newExecutorTestHarness(store, newFakeSlotLeadership().withDefault(true), now, 9)
	executor.cfg.MaxConcurrentSources = 1
	executor.cfg.MaxConcurrentTargets = 1

	err := executor.Tick(context.Background())

	require.NoError(t, err)
	require.Equal(t, []string{"first", "independent"}, store.claimedTaskIDs())
	require.Equal(t, []string{"first", "independent"}, store.advancedTaskIDs())
}

func TestExecutorHonorsGlobalConcurrencyLimit(t *testing.T) {
	now := time.UnixMilli(9100)
	store := newFakeExecutorStore(
		withNodes(executorTestTask("first", "ch-first", slotmeta.ChannelMigrationStatusPending, now.Add(-time.Second)), 1, 2),
		withNodes(executorTestTask("second", "ch-second", slotmeta.ChannelMigrationStatusPending, now.Add(-time.Second)), 3, 4),
		withNodes(executorTestTask("third", "ch-third", slotmeta.ChannelMigrationStatusPending, now.Add(-time.Second)), 5, 6),
	)
	executor := newExecutorTestHarness(store, newFakeSlotLeadership().withDefault(true), now, 9)
	executor.cfg.MaxConcurrent = 2

	err := executor.Tick(context.Background())

	require.NoError(t, err)
	require.Equal(t, []string{"first", "second"}, store.claimedTaskIDs())
	require.Equal(t, []string{"first", "second"}, store.advancedTaskIDs())
}

func TestExecutorUsesFreshTimePerTaskAfterSlowPriorTask(t *testing.T) {
	now := time.UnixMilli(9500)
	clock := &leaderTransferClock{now: now}
	first := leaderTransferTask("first", "ch-first", 1, 2, now)
	first.Status = slotmeta.ChannelMigrationStatusRunning
	first.Phase = slotmeta.ChannelMigrationPhaseWriteFence
	second := withOwner(
		executorTestTask("second", "ch-second", slotmeta.ChannelMigrationStatusRunning, now.Add(-time.Second)),
		9,
		now.Add(5*time.Millisecond),
	)
	store := newFakeExecutorStore(first, second)
	store.putRuntimeMeta(leaderTransferRuntimeMeta(first.ChannelID, 1, 2, now))
	store.onGetRuntimeMeta = func() {
		clock.advance(10 * time.Millisecond)
	}
	executor := NewExecutor(ExecutorOptions{
		Store:     store,
		Slots:     newFakeSlotLeadership().withDefault(true),
		Metrics:   noopMetrics{},
		LocalNode: 9,
		Now:       clock.Now,
		Config: Config{
			ScanLimit:    16,
			OwnerLease:   5 * time.Millisecond,
			RetryBackoff: time.Second,
			FenceLease:   time.Minute,
			LeaderLease:  time.Minute,
		},
	})

	err := executor.Tick(context.Background())

	require.NoError(t, err)
	require.Equal(t, []string{"first", "second"}, store.claimedTaskIDs())
	require.Greater(t, store.task("second").OwnerLeaseUntilMS, clock.Now().UnixMilli())
}

func TestExecutorGarbageCollectsTerminalTasksAfterRetention(t *testing.T) {
	now := time.UnixMilli(10000)
	store := newFakeExecutorStore()
	executor := newExecutorTestHarness(store, newFakeSlotLeadership().withDefault(true), now, 9)
	executor.cfg.GCRetention = time.Hour
	executor.cfg.GCLimit = 17
	store.gcDeleted = 3

	err := executor.Tick(context.Background())

	require.NoError(t, err)
	require.Equal(t, []fakeGCCall{{beforeMS: now.Add(-time.Hour).UnixMilli(), limit: 17}}, store.gcCalls)
}

func TestExecutorRecordsPhaseTransitionMetrics(t *testing.T) {
	now := time.UnixMilli(11000)
	store := newFakeExecutorStore(executorTestTask("task", "ch", slotmeta.ChannelMigrationStatusPending, now.Add(-time.Second)))
	metrics := &recordingExecutorMetrics{}
	executor := newExecutorTestHarness(store, newFakeSlotLeadership().withDefault(true), now, 9)
	executor.metrics = metrics

	err := executor.Tick(context.Background())

	require.NoError(t, err)
	require.Equal(t, []int{1}, metrics.activeTasks)
	require.Len(t, metrics.phaseTransitions, 1)
	require.Equal(t, "task", metrics.phaseTransitions[0].TaskID)
	require.Equal(t, slotmeta.ChannelMigrationPhaseValidate, metrics.phaseTransitions[0].FromPhase)
	require.Equal(t, slotmeta.ChannelMigrationPhaseValidate, metrics.phaseTransitions[0].ToPhase)
	require.Len(t, metrics.retries, 1)
	require.ErrorIs(t, metrics.retries[0].err, ErrPhaseNotImplemented)
}

func TestExecutorReturnsAdvancePersistenceError(t *testing.T) {
	now := time.UnixMilli(12000)
	store := newFakeExecutorStore(executorTestTask("task", "ch", slotmeta.ChannelMigrationStatusPending, now.Add(-time.Second)))
	store.advanceErr = errors.New("advance raft failed")
	metrics := &recordingExecutorMetrics{}
	executor := newExecutorTestHarness(store, newFakeSlotLeadership().withDefault(true), now, 9)
	executor.metrics = metrics

	err := executor.Tick(context.Background())

	require.ErrorIs(t, err, store.advanceErr)
	require.Len(t, metrics.retries, 1)
	require.ErrorIs(t, metrics.retries[0].err, store.advanceErr)
}

func TestExecutorTickReportsMissingDependencies(t *testing.T) {
	executor := NewExecutor(ExecutorOptions{LocalNode: 9})

	var err error
	require.NotPanics(t, func() {
		err = executor.Tick(context.Background())
	})
	require.ErrorIs(t, err, ErrMissingDependency)
}

func TestExecutorDefaultRetryBackoffAvoidsHotLoop(t *testing.T) {
	now := time.UnixMilli(13000)
	store := newFakeExecutorStore(executorTestTask("task", "ch", slotmeta.ChannelMigrationStatusPending, now.Add(-time.Second)))
	executor := NewExecutor(ExecutorOptions{
		Store:     store,
		Slots:     newFakeSlotLeadership().withDefault(true),
		LocalNode: 9,
		Now:       func() time.Time { return now },
	})

	err := executor.Tick(context.Background())

	require.NoError(t, err)
	require.Len(t, store.advances, 1)
	require.GreaterOrEqual(t, store.advances[0].NextRunAtMS, now.Add(time.Minute).UnixMilli())
}

func TestExecutorNormalizesSubMillisecondDurations(t *testing.T) {
	now := time.UnixMilli(14000)
	store := newFakeExecutorStore(executorTestTask("task", "ch", slotmeta.ChannelMigrationStatusPending, now.Add(-time.Second)))
	executor := NewExecutor(ExecutorOptions{
		Store:     store,
		Slots:     newFakeSlotLeadership().withDefault(true),
		LocalNode: 9,
		Now:       func() time.Time { return now },
		Config: Config{
			OwnerLease:   time.Nanosecond,
			RetryBackoff: time.Nanosecond,
		},
	})

	err := executor.Tick(context.Background())

	require.NoError(t, err)
	require.Len(t, store.claims, 1)
	require.Greater(t, store.claims[0].OwnerLeaseUntilMS, now.UnixMilli())
	require.Len(t, store.advances, 1)
	require.Greater(t, store.advances[0].NextRunAtMS, now.UnixMilli())
}

func newExecutorTestHarness(store *fakeExecutorStore, slots *fakeSlotLeadership, now time.Time, localNode channel.NodeID) *Executor {
	return NewExecutor(ExecutorOptions{
		Store:     store,
		Slots:     slots,
		Metrics:   noopMetrics{},
		LocalNode: localNode,
		Now:       func() time.Time { return now },
		Config: Config{
			ScanLimit:            32,
			OwnerLease:           time.Minute,
			RetryBackoff:         time.Second,
			GCRetention:          0,
			GCLimit:              100,
			MaxConcurrentSources: 0,
			MaxConcurrentTargets: 0,
		},
	})
}

func executorTestTask(taskID, channelID string, status slotmeta.ChannelMigrationStatus, updatedAt time.Time) Task {
	return Task{
		TaskID:           taskID,
		Kind:             slotmeta.ChannelMigrationKind(255),
		Status:           status,
		Phase:            slotmeta.ChannelMigrationPhaseValidate,
		ChannelID:        channelID,
		ChannelType:      1,
		SourceNode:       1,
		TargetNode:       2,
		BaseChannelEpoch: 3,
		BaseLeaderEpoch:  4,
		CreatedAtMS:      updatedAt.Add(-time.Second).UnixMilli(),
		UpdatedAtMS:      updatedAt.UnixMilli(),
	}
}

func withNextRun(task Task, nextRun time.Time) Task {
	task.NextRunAtMS = nextRun.UnixMilli()
	return task
}

func withOwner(task Task, owner uint64, leaseUntil time.Time) Task {
	task.OwnerNodeID = owner
	task.OwnerLeaseUntilMS = leaseUntil.UnixMilli()
	return task
}

func withBlocker(task Task) Task {
	task.Status = slotmeta.ChannelMigrationStatusBlocked
	task.Phase = slotmeta.ChannelMigrationPhaseFinalTargetCatchUp
	task.BlockerCode = slotmeta.ChannelMigrationBlockerNeedsSnapshotBootstrap
	task.BlockerMessage = "snapshot bootstrap required"
	return task
}

func withCompleted(task Task, completedAt time.Time) Task {
	task.Status = slotmeta.ChannelMigrationStatusCompleted
	task.Phase = slotmeta.ChannelMigrationPhaseClearFence
	task.CompletedAtMS = completedAt.UnixMilli()
	task.UpdatedAtMS = completedAt.UnixMilli()
	return task
}

func withNodes(task Task, source, target uint64) Task {
	task.SourceNode = source
	task.TargetNode = target
	task.DesiredLeader = target
	return task
}

type fakeExecutorStore struct {
	mu                     sync.Mutex
	tasks                  []Task
	runtimeMetas           map[string]slotmeta.ChannelRuntimeMeta
	claims                 []ClaimRequest
	claimErrs              []error
	advances               []AdvanceRequest
	setFenceRequests       []slotmeta.ChannelMigrationFenceRequest
	resetFenceRequests     []slotmeta.ChannelMigrationResetFenceRequest
	leaderTransferCommits  []slotmeta.ChannelMigrationLeaderTransferRequest
	addLearnerRequests     []slotmeta.ChannelMigrationAddLearnerRequest
	promoteLearnerRequests []slotmeta.ChannelMigrationPromoteLearnerRequest
	clearFenceRequests     []slotmeta.ChannelMigrationClearFenceRequest
	gcCalls                []fakeGCCall
	gcDeleted              int
	advanceErr             error
	onList                 func()
	onGetRuntimeMeta       func()
}

type fakeGCCall struct {
	beforeMS int64
	limit    int
}

func newFakeExecutorStore(tasks ...Task) *fakeExecutorStore {
	return &fakeExecutorStore{
		tasks:        append([]Task(nil), tasks...),
		runtimeMetas: make(map[string]slotmeta.ChannelRuntimeMeta),
	}
}

func (s *fakeExecutorStore) GetChannelRuntimeMeta(ctx context.Context, channelID string, channelType int64) (slotmeta.ChannelRuntimeMeta, error) {
	s.mu.Lock()
	meta, ok := s.runtimeMetas[runtimeMetaFakeKey(channelID, channelType)]
	s.mu.Unlock()
	if s.onGetRuntimeMeta != nil {
		s.onGetRuntimeMeta()
	}
	if !ok {
		return slotmeta.ChannelRuntimeMeta{}, slotmeta.ErrNotFound
	}
	return meta, nil
}

func (s *fakeExecutorStore) ListRunnableTasksForLocalLeaderSlots(ctx context.Context, nowMS int64, limit int) ([]Task, error) {
	s.mu.Lock()
	tasks := append([]Task(nil), s.tasks...)
	s.mu.Unlock()

	if limit > 0 && len(tasks) > limit {
		tasks = tasks[:limit]
	}
	if s.onList != nil {
		s.onList()
	}
	return tasks, nil
}

func (s *fakeExecutorStore) ClaimChannelMigrationTask(ctx context.Context, req ClaimRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.claims = append(s.claims, req)
	err := error(nil)
	defer func() {
		s.claimErrs = append(s.claimErrs, err)
	}()
	idx := s.taskIndex(req.Guard.TaskID)
	if idx < 0 {
		err = slotmeta.ErrNotFound
		return err
	}
	if !guardMatches(req.Guard, s.tasks[idx]) {
		err = slotmeta.ErrStaleMeta
		return err
	}
	s.tasks[idx].Status = req.Status
	s.tasks[idx].Phase = req.Phase
	s.tasks[idx].OwnerNodeID = req.OwnerNodeID
	s.tasks[idx].OwnerLeaseUntilMS = req.OwnerLeaseUntilMS
	s.tasks[idx].UpdatedAtMS = req.UpdatedAtMS
	return nil
}

func (s *fakeExecutorStore) AdvanceChannelMigrationTask(ctx context.Context, req AdvanceRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.advanceErr != nil {
		return s.advanceErr
	}
	idx := s.taskIndex(req.Guard.TaskID)
	if idx < 0 {
		return slotmeta.ErrNotFound
	}
	if !guardMatches(req.Guard, s.tasks[idx]) {
		return slotmeta.ErrStaleMeta
	}
	s.advances = append(s.advances, req)
	s.tasks[idx].Status = req.Status
	s.tasks[idx].Phase = req.Phase
	s.tasks[idx].Attempt = req.Attempt
	s.tasks[idx].NextRunAtMS = req.NextRunAtMS
	s.tasks[idx].BlockerCode = req.BlockerCode
	s.tasks[idx].BlockerMessage = req.BlockerMessage
	s.tasks[idx].LastError = req.LastError
	s.tasks[idx].UpdatedAtMS = req.UpdatedAtMS
	s.tasks[idx].CompletedAtMS = req.CompletedAtMS
	s.tasks[idx].Progress = req.Progress
	if req.EmbeddedDesiredLeader != 0 {
		s.tasks[idx].EmbeddedLeaderTransfer = true
		s.tasks[idx].EmbeddedDesiredLeader = req.EmbeddedDesiredLeader
	}
	if req.CutoverProof.CutoverLEO != 0 ||
		req.CutoverProof.CutoverHW != 0 ||
		req.CutoverProof.DrainedLeaderNode != 0 ||
		req.CutoverProof.DrainedRuntimeGeneration != 0 ||
		req.CutoverProof.DrainedChannelEpoch != 0 ||
		req.CutoverProof.DrainedLeaderEpoch != 0 ||
		req.CutoverProof.DrainedFenceVersion != 0 {
		s.tasks[idx].CutoverLEO = req.CutoverProof.CutoverLEO
		s.tasks[idx].CutoverHW = req.CutoverProof.CutoverHW
		s.tasks[idx].DrainedLeaderNode = req.CutoverProof.DrainedLeaderNode
		s.tasks[idx].DrainedRuntimeGeneration = req.CutoverProof.DrainedRuntimeGeneration
		s.tasks[idx].DrainedChannelEpoch = req.CutoverProof.DrainedChannelEpoch
		s.tasks[idx].DrainedLeaderEpoch = req.CutoverProof.DrainedLeaderEpoch
		s.tasks[idx].DrainedFenceVersion = req.CutoverProof.DrainedFenceVersion
	}
	return nil
}

func (s *fakeExecutorStore) SetChannelWriteFence(ctx context.Context, req slotmeta.ChannelMigrationFenceRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	idx, meta, err := s.guardedTaskAndMetaLocked(req.Guard, req.RuntimeGuard)
	if err != nil {
		return err
	}
	s.setFenceRequests = append(s.setFenceRequests, req)
	nextVersion := meta.WriteFenceVersion + 1
	s.tasks[idx].Status = req.Status
	s.tasks[idx].Phase = req.Phase
	s.tasks[idx].FenceToken = req.Guard.TaskID
	s.tasks[idx].FenceVersion = nextVersion
	s.tasks[idx].FenceUntilMS = req.FenceUntilMS
	s.tasks[idx].UpdatedAtMS = req.UpdatedAtMS

	meta.WriteFenceToken = req.Guard.TaskID
	meta.WriteFenceVersion = nextVersion
	meta.WriteFenceReason = req.FenceReason
	meta.WriteFenceUntilMS = req.FenceUntilMS
	s.runtimeMetas[runtimeMetaFakeKey(meta.ChannelID, meta.ChannelType)] = meta
	return nil
}

func (s *fakeExecutorStore) ResetChannelWriteFenceToPreCutover(ctx context.Context, req slotmeta.ChannelMigrationResetFenceRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	idx, meta, err := s.guardedTaskAndMetaLocked(req.Guard, req.RuntimeGuard)
	if err != nil {
		return err
	}
	if req.NowMS <= meta.WriteFenceUntilMS {
		return slotmeta.ErrStaleMeta
	}
	s.resetFenceRequests = append(s.resetFenceRequests, req)
	s.tasks[idx].Status = req.Status
	s.tasks[idx].Phase = req.Phase
	s.tasks[idx].FenceToken = ""
	s.tasks[idx].FenceVersion = 0
	s.tasks[idx].FenceUntilMS = 0
	s.tasks[idx].CutoverLEO = 0
	s.tasks[idx].CutoverHW = 0
	s.tasks[idx].DrainedLeaderNode = 0
	s.tasks[idx].DrainedRuntimeGeneration = 0
	s.tasks[idx].DrainedChannelEpoch = 0
	s.tasks[idx].DrainedLeaderEpoch = 0
	s.tasks[idx].DrainedFenceVersion = 0
	s.tasks[idx].UpdatedAtMS = req.UpdatedAtMS

	meta.WriteFenceToken = ""
	meta.WriteFenceVersion++
	meta.WriteFenceReason = 0
	meta.WriteFenceUntilMS = 0
	s.runtimeMetas[runtimeMetaFakeKey(meta.ChannelID, meta.ChannelType)] = meta
	return nil
}

func (s *fakeExecutorStore) CommitChannelLeaderTransfer(ctx context.Context, req slotmeta.ChannelMigrationLeaderTransferRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	idx, meta, err := s.guardedTaskAndMetaLocked(req.Guard, req.RuntimeGuard)
	if err != nil {
		return err
	}
	task := s.tasks[idx]
	if task.FenceToken != meta.WriteFenceToken ||
		task.FenceVersion != meta.WriteFenceVersion ||
		task.DrainedFenceVersion != meta.WriteFenceVersion ||
		req.NowMS > meta.WriteFenceUntilMS {
		return slotmeta.ErrStaleMeta
	}
	s.leaderTransferCommits = append(s.leaderTransferCommits, req)
	s.tasks[idx].Status = req.Status
	s.tasks[idx].Phase = req.Phase
	s.tasks[idx].UpdatedAtMS = req.UpdatedAtMS

	meta.Leader = req.DesiredLeader
	meta.LeaderEpoch = req.NextLeaderEpoch
	meta.LeaseUntilMS = req.LeaseUntilMS
	s.runtimeMetas[runtimeMetaFakeKey(meta.ChannelID, meta.ChannelType)] = meta
	return nil
}

func (s *fakeExecutorStore) AddChannelLearner(ctx context.Context, req slotmeta.ChannelMigrationAddLearnerRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	idx, meta, err := s.guardedTaskAndMetaLocked(req.Guard, req.RuntimeGuard)
	if err != nil {
		return err
	}
	task := s.tasks[idx]
	if req.TargetNode != task.TargetNode ||
		meta.Leader == task.SourceNode ||
		!containsUint64(meta.Replicas, task.SourceNode) ||
		!containsUint64(meta.ISR, task.SourceNode) ||
		containsUint64(meta.Replicas, req.TargetNode) ||
		containsUint64(meta.ISR, req.TargetNode) {
		return slotmeta.ErrStaleMeta
	}
	s.addLearnerRequests = append(s.addLearnerRequests, req)
	s.tasks[idx].Status = req.Status
	s.tasks[idx].Phase = req.Phase
	s.tasks[idx].UpdatedAtMS = req.UpdatedAtMS

	meta.Replicas = append(meta.Replicas, req.TargetNode)
	meta.ChannelEpoch++
	s.runtimeMetas[runtimeMetaFakeKey(meta.ChannelID, meta.ChannelType)] = meta
	return nil
}

func (s *fakeExecutorStore) PromoteLearnerAndRemoveReplica(ctx context.Context, req slotmeta.ChannelMigrationPromoteLearnerRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	idx, meta, err := s.guardedTaskAndMetaLocked(req.Guard, req.RuntimeGuard)
	if err != nil {
		return err
	}
	task := s.tasks[idx]
	if task.FenceToken != meta.WriteFenceToken ||
		task.FenceVersion != meta.WriteFenceVersion ||
		task.DrainedFenceVersion != meta.WriteFenceVersion ||
		task.DrainedChannelEpoch != meta.ChannelEpoch ||
		task.DrainedLeaderEpoch != meta.LeaderEpoch ||
		task.DrainedLeaderNode != meta.Leader ||
		req.NowMS > meta.WriteFenceUntilMS ||
		req.SourceNode != task.SourceNode ||
		req.TargetNode != task.TargetNode ||
		meta.Leader == req.SourceNode ||
		!containsUint64(meta.Replicas, req.SourceNode) ||
		!containsUint64(meta.Replicas, req.TargetNode) ||
		!containsUint64(meta.ISR, req.SourceNode) ||
		containsUint64(meta.ISR, req.TargetNode) {
		return slotmeta.ErrStaleMeta
	}
	s.promoteLearnerRequests = append(s.promoteLearnerRequests, req)
	s.tasks[idx].Status = req.Status
	s.tasks[idx].Phase = req.Phase
	s.tasks[idx].UpdatedAtMS = req.UpdatedAtMS

	meta.Replicas = replaceFakeUint64Member(meta.Replicas, req.SourceNode, req.TargetNode)
	meta.ISR = replaceFakeUint64Member(meta.ISR, req.SourceNode, req.TargetNode)
	meta.ChannelEpoch++
	s.runtimeMetas[runtimeMetaFakeKey(meta.ChannelID, meta.ChannelType)] = meta
	return nil
}

func (s *fakeExecutorStore) ClearChannelWriteFence(ctx context.Context, req slotmeta.ChannelMigrationClearFenceRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	idx, meta, err := s.guardedTaskAndMetaLocked(req.Guard, req.RuntimeGuard)
	if err != nil {
		return err
	}
	s.clearFenceRequests = append(s.clearFenceRequests, req)
	s.tasks[idx].Status = req.Status
	s.tasks[idx].Phase = req.Phase
	s.tasks[idx].FenceToken = ""
	s.tasks[idx].FenceVersion = 0
	s.tasks[idx].FenceUntilMS = 0
	s.tasks[idx].CutoverLEO = 0
	s.tasks[idx].CutoverHW = 0
	s.tasks[idx].DrainedLeaderNode = 0
	s.tasks[idx].DrainedRuntimeGeneration = 0
	s.tasks[idx].DrainedChannelEpoch = 0
	s.tasks[idx].DrainedLeaderEpoch = 0
	s.tasks[idx].DrainedFenceVersion = 0
	s.tasks[idx].UpdatedAtMS = req.UpdatedAtMS
	s.tasks[idx].CompletedAtMS = req.CompletedAtMS
	if req.Status == slotmeta.ChannelMigrationStatusRunning &&
		req.Phase == slotmeta.ChannelMigrationPhaseAddLearner &&
		s.tasks[idx].Kind == slotmeta.ChannelMigrationKindReplicaReplace &&
		s.tasks[idx].EmbeddedLeaderTransfer {
		s.tasks[idx].EmbeddedLeaderTransfer = false
		s.tasks[idx].EmbeddedDesiredLeader = 0
	}

	meta.WriteFenceToken = ""
	meta.WriteFenceVersion++
	meta.WriteFenceReason = 0
	meta.WriteFenceUntilMS = 0
	s.runtimeMetas[runtimeMetaFakeKey(meta.ChannelID, meta.ChannelType)] = meta
	return nil
}

func (s *fakeExecutorStore) GarbageCollectTerminalTasks(ctx context.Context, beforeMS int64, limit int) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.gcCalls = append(s.gcCalls, fakeGCCall{beforeMS: beforeMS, limit: limit})
	return s.gcDeleted, nil
}

func (s *fakeExecutorStore) claimedTaskIDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	ids := make([]string, 0, len(s.claims))
	for _, claim := range s.claims {
		ids = append(ids, claim.Guard.TaskID)
	}
	return ids
}

func (s *fakeExecutorStore) successfulClaims() []ClaimRequest {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]ClaimRequest, 0, len(s.claims))
	for _, claim := range s.claims {
		task := s.taskLocked(claim.Guard.TaskID)
		if task.OwnerNodeID == claim.OwnerNodeID {
			out = append(out, claim)
		}
	}
	return out
}

func (s *fakeExecutorStore) staleClaimCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	count := 0
	for _, err := range s.claimErrs {
		if errors.Is(err, slotmeta.ErrStaleMeta) {
			count++
		}
	}
	return count
}

func (s *fakeExecutorStore) advancedTaskIDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	ids := make([]string, 0, len(s.advances))
	for _, advance := range s.advances {
		ids = append(ids, advance.Guard.TaskID)
	}
	return ids
}

func (s *fakeExecutorStore) task(taskID string) Task {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.taskLocked(taskID)
}

func (s *fakeExecutorStore) taskLocked(taskID string) Task {
	idx := s.taskIndex(taskID)
	if idx < 0 {
		return Task{}
	}
	return s.tasks[idx]
}

func (s *fakeExecutorStore) taskIndex(taskID string) int {
	for i, task := range s.tasks {
		if task.TaskID == taskID {
			return i
		}
	}
	return -1
}

func (s *fakeExecutorStore) guardedTaskAndMetaLocked(guard slotmeta.ChannelMigrationTaskGuard, runtimeGuard slotmeta.ChannelMigrationRuntimeGuard) (int, slotmeta.ChannelRuntimeMeta, error) {
	idx := s.taskIndex(guard.TaskID)
	if idx < 0 {
		return -1, slotmeta.ChannelRuntimeMeta{}, slotmeta.ErrNotFound
	}
	if !guardMatches(guard, s.tasks[idx]) {
		return -1, slotmeta.ChannelRuntimeMeta{}, slotmeta.ErrStaleMeta
	}
	meta, ok := s.runtimeMetas[runtimeMetaFakeKey(runtimeGuard.ChannelID, runtimeGuard.ChannelType)]
	if !ok {
		return -1, slotmeta.ChannelRuntimeMeta{}, slotmeta.ErrNotFound
	}
	if !runtimeGuardMatches(runtimeGuard, meta) {
		return -1, slotmeta.ChannelRuntimeMeta{}, slotmeta.ErrStaleMeta
	}
	return idx, meta, nil
}

func guardMatches(guard slotmeta.ChannelMigrationTaskGuard, task Task) bool {
	return task.ChannelID == guard.ChannelID &&
		task.ChannelType == guard.ChannelType &&
		task.TaskID == guard.TaskID &&
		task.Status == guard.ExpectedStatus &&
		task.Phase == guard.ExpectedPhase &&
		task.OwnerNodeID == guard.ExpectedOwnerNodeID &&
		task.OwnerLeaseUntilMS == guard.ExpectedOwnerLeaseUntilMS &&
		task.UpdatedAtMS == guard.ExpectedUpdatedAtMS
}

func runtimeGuardMatches(guard slotmeta.ChannelMigrationRuntimeGuard, meta slotmeta.ChannelRuntimeMeta) bool {
	return meta.ChannelID == guard.ChannelID &&
		meta.ChannelType == guard.ChannelType &&
		meta.ChannelEpoch == guard.ExpectedChannelEpoch &&
		meta.LeaderEpoch == guard.ExpectedLeaderEpoch &&
		meta.Leader == guard.ExpectedLeader &&
		meta.WriteFenceToken == guard.ExpectedFenceToken &&
		meta.WriteFenceVersion == guard.ExpectedFenceVersion
}

func runtimeMetaFakeKey(channelID string, channelType int64) string {
	return fmt.Sprintf("%s\x00%d", channelID, channelType)
}

func replaceFakeUint64Member(values []uint64, oldValue, newValue uint64) []uint64 {
	out := make([]uint64, 0, len(values))
	seen := make(map[uint64]struct{}, len(values))
	for _, value := range values {
		if value == oldValue {
			value = newValue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

type fakeSlotLeadership struct {
	mu           sync.Mutex
	defaultLocal bool
	channelSlots map[string]uint32
	leaders      map[uint32][]bool
	checks       []uint32
}

func newFakeSlotLeadership() *fakeSlotLeadership {
	return &fakeSlotLeadership{
		defaultLocal: true,
		channelSlots: make(map[string]uint32),
		leaders:      make(map[uint32][]bool),
	}
}

func (s *fakeSlotLeadership) withDefault(local bool) *fakeSlotLeadership {
	s.defaultLocal = local
	return s
}

func (s *fakeSlotLeadership) withChannelSlot(channelID string, slot uint32) *fakeSlotLeadership {
	s.channelSlots[channelID] = slot
	return s
}

func (s *fakeSlotLeadership) withLeader(slot uint32, local bool) *fakeSlotLeadership {
	s.leaders[slot] = []bool{local}
	return s
}

func (s *fakeSlotLeadership) withLeaderSequence(slot uint32, sequence ...bool) *fakeSlotLeadership {
	s.leaders[slot] = append([]bool(nil), sequence...)
	return s
}

func (s *fakeSlotLeadership) IsLocalLeader(ctx context.Context, slotID uint32) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.checks = append(s.checks, slotID)
	sequence := s.leaders[slotID]
	if len(sequence) == 0 {
		return s.defaultLocal, nil
	}
	local := sequence[0]
	if len(sequence) > 1 {
		s.leaders[slotID] = sequence[1:]
	}
	return local, nil
}

func (s *fakeSlotLeadership) SlotForChannel(channelID string) uint32 {
	s.mu.Lock()
	defer s.mu.Unlock()

	if slot, ok := s.channelSlots[channelID]; ok {
		return slot
	}
	return 1
}

func (s *fakeSlotLeadership) checkedSlots() []uint32 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]uint32(nil), s.checks...)
}

type recordingExecutorMetrics struct {
	activeTasks      []int
	phaseTransitions []PhaseTransition
	retries          []recordedRetry
	blockers         []string
	fenceDurations   []time.Duration
	taskDurations    []time.Duration
	gcDeleted        []int
}

type recordedRetry struct {
	taskID string
	err    error
}

func (m *recordingExecutorMetrics) RecordActiveTasks(active int) {
	m.activeTasks = append(m.activeTasks, active)
}

func (m *recordingExecutorMetrics) RecordPhaseTransition(transition PhaseTransition) {
	m.phaseTransitions = append(m.phaseTransitions, transition)
}

func (m *recordingExecutorMetrics) RecordRetry(task Task, err error) {
	m.retries = append(m.retries, recordedRetry{taskID: task.TaskID, err: err})
}

func (m *recordingExecutorMetrics) RecordBlocker(task Task, code string) {
	m.blockers = append(m.blockers, code)
}

func (m *recordingExecutorMetrics) RecordFenceDuration(task Task, duration time.Duration) {
	m.fenceDurations = append(m.fenceDurations, duration)
}

func (m *recordingExecutorMetrics) RecordTaskDuration(task Task, duration time.Duration) {
	m.taskDurations = append(m.taskDurations, duration)
}

func (m *recordingExecutorMetrics) RecordGarbageCollection(deleted int) {
	m.gcDeleted = append(m.gcDeleted, deleted)
}

var _ Store = (*fakeExecutorStore)(nil)
var _ SlotLeadership = (*fakeSlotLeadership)(nil)
var _ Metrics = (*recordingExecutorMetrics)(nil)
