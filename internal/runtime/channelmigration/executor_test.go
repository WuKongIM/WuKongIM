package channelmigration

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	slotmeta "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
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
		Kind:             slotmeta.ChannelMigrationKindLeaderTransfer,
		Status:           status,
		Phase:            slotmeta.ChannelMigrationPhaseValidate,
		ChannelID:        channelID,
		ChannelType:      1,
		SourceNode:       1,
		TargetNode:       2,
		DesiredLeader:    2,
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
	mu         sync.Mutex
	tasks      []Task
	claims     []ClaimRequest
	claimErrs  []error
	advances   []AdvanceRequest
	gcCalls    []fakeGCCall
	gcDeleted  int
	advanceErr error
	onList     func()
}

type fakeGCCall struct {
	beforeMS int64
	limit    int
}

func newFakeExecutorStore(tasks ...Task) *fakeExecutorStore {
	return &fakeExecutorStore{tasks: append([]Task(nil), tasks...)}
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
