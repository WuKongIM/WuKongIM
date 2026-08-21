package persondirectory

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	runtimechannelid "github.com/WuKongIM/WuKongIM/pkg/protocol/channelid"
)

func TestProjectorReplaysBothMembershipsUntilAlignedSuccessThenCompletesTask(t *testing.T) {
	t.Parallel()

	task := metadb.PersonDirectoryTask{
		ChannelID:     runtimechannelid.EncodePersonChannel("u1", "u2"),
		ChannelType:   1,
		CommittedTail: 9,
		CreatedAt:     123,
		Generation:    7,
	}
	source := newProjectorTaskSource(7, task)
	writer := &partialMembershipWriter{
		calls: make(chan []metadb.UserChannelMembership, 4),
		fail:  true,
	}
	projector, err := New(Options{Source: source, Memberships: writer})
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	if err := projector.Start(context.Background()); err != nil {
		t.Fatalf("Start(): %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := projector.Stop(ctx); err != nil {
			t.Fatalf("Stop(): %v", err)
		}
	})

	first := waitMembershipCall(t, writer.calls)
	assertProjectedMemberships(t, first, task)
	if source.completeCount() != 0 {
		t.Fatal("task completed after one aligned item failed")
	}

	writer.setFail(false)
	projector.Wake()
	second := waitMembershipCall(t, writer.calls)
	assertProjectedMemberships(t, second, task)

	select {
	case <-source.completed:
	case <-time.After(2 * time.Second):
		t.Fatal("task was not completed after both membership ensures succeeded")
	}
	if source.completeCount() != 1 {
		t.Fatalf("complete calls = %d, want 1", source.completeCount())
	}
}

func TestProjectorPressureRetainsDurablePendingTaskAcrossRetry(t *testing.T) {
	t.Parallel()

	task := metadb.PersonDirectoryTask{
		ChannelID: runtimechannelid.EncodePersonChannel("u1", "u2"), ChannelType: 1,
		CommittedTail: 9, CreatedAt: 123,
		Generation: 1,
	}
	source := newProjectorTaskSource(7, task)
	writer := &partialMembershipWriter{calls: make(chan []metadb.UserChannelMembership, 4), fail: true}
	observer := &recordingPressureObserver{observations: make(chan PressureObservation, 16)}
	projector, err := New(Options{Source: source, Memberships: writer, Observer: observer})
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	if err := projector.Start(context.Background()); err != nil {
		t.Fatalf("Start(): %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := projector.Stop(ctx); err != nil {
			t.Fatalf("Stop(): %v", err)
		}
	})

	waitMembershipCall(t, writer.calls)
	waitPressureObservation(t, observer.observations, func(got PressureObservation) bool {
		return got.Pending == 1 && got.Inflight == 0
	})

	writer.setFail(false)
	projector.Wake()
	waitMembershipCall(t, writer.calls)
	select {
	case <-source.completed:
	case <-time.After(time.Second):
		t.Fatal("task was not completed after retry")
	}
	waitPressureObservation(t, observer.observations, func(got PressureObservation) bool {
		return got.Pending == 0 && got.Inflight == 0
	})
}

func TestProjectorAttemptTimeoutReleasesWorkerAndInflightCapacityForRetry(t *testing.T) {
	t.Parallel()

	task := metadb.PersonDirectoryTask{
		ChannelID: runtimechannelid.EncodePersonChannel("u1", "u2"), ChannelType: 1,
		CreatedAt:  123,
		Generation: 1,
	}
	source := newProjectorTaskSource(7, task)
	writer := &contextBlockingMembershipWriter{calls: make(chan struct{}, 2)}
	observer := &recordingPressureObserver{observations: make(chan PressureObservation, 16)}
	projector, err := New(Options{Source: source, Memberships: writer, Observer: observer})
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	projector.attemptTimeout = 10 * time.Millisecond
	if err := projector.Start(context.Background()); err != nil {
		t.Fatalf("Start(): %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := projector.Stop(ctx); err != nil {
			t.Fatalf("Stop(): %v", err)
		}
	})

	waitBlockingMembershipCall(t, writer.calls)
	waitPressureObservation(t, observer.observations, func(got PressureObservation) bool {
		return got.Pending == 1 && got.Inflight == 0
	})

	projector.Wake()
	waitBlockingMembershipCall(t, writer.calls)
}

func TestProjectorWakeDoesNotExceedDeclaredInflightCapacity(t *testing.T) {
	t.Parallel()

	tasks := make([]metadb.PersonDirectoryTask, 600)
	for i := range tasks {
		tasks[i] = metadb.PersonDirectoryTask{
			ChannelID:   runtimechannelid.EncodePersonChannel(fmt.Sprintf("u%04d-a", i), fmt.Sprintf("u%04d-b", i)),
			ChannelType: 1,
			CreatedAt:   int64(i + 1),
			Generation:  1,
		}
	}
	source := newProjectorTaskSource(7, tasks...)
	observer := &recordingPressureObserver{observations: make(chan PressureObservation, 64)}
	projector, err := New(Options{Source: source, Memberships: blockingMembershipWriter{}, Observer: observer})
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	if err := projector.Start(context.Background()); err != nil {
		t.Fatalf("Start(): %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := projector.Stop(ctx); err != nil {
			t.Fatalf("Stop(): %v", err)
		}
	})

	waitPressureObservation(t, observer.observations, func(got PressureObservation) bool {
		return got.Inflight == projectorQueueSize
	})
	projector.Wake()
	got := waitPressureObservation(t, observer.observations, func(got PressureObservation) bool {
		return got.Inflight >= projectorQueueSize
	})
	if got.Inflight > got.Capacity || got.Pending > got.Capacity {
		t.Fatalf("projector pressure = %+v, want pending and inflight bounded by declared capacity", got)
	}
}

func TestProjectorScanRotatesPastFailingEarlyHashSlot(t *testing.T) {
	t.Parallel()

	early := make([]metadb.PersonDirectoryTask, projectorQueueSize)
	for i := range early {
		early[i] = metadb.PersonDirectoryTask{
			ChannelID:   runtimechannelid.EncodePersonChannel(fmt.Sprintf("early-%04d-a", i), fmt.Sprintf("early-%04d-b", i)),
			ChannelType: 1, CreatedAt: int64(i + 1), Generation: 1,
		}
	}
	target := metadb.PersonDirectoryTask{
		ChannelID: runtimechannelid.EncodePersonChannel("later-a", "later-b"), ChannelType: 1, CreatedAt: 999, Generation: 1,
	}
	source := newMultiSlotProjectorTaskSource(map[metadb.HashSlot][]metadb.PersonDirectoryTask{7: early, 9: {target}})
	writer := &failingTargetMembershipWriter{targetUID: "later-a", observed: make(chan struct{})}
	observer := &recordingPressureObserver{observations: make(chan PressureObservation, 128)}
	projector, err := New(Options{Source: source, Memberships: writer, Observer: observer})
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	if err := projector.Start(context.Background()); err != nil {
		t.Fatalf("Start(): %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := projector.Stop(ctx); err != nil {
			t.Fatalf("Stop(): %v", err)
		}
	})

	waitPressureObservation(t, observer.observations, func(got PressureObservation) bool {
		return got.Pending == projectorQueueSize && got.Inflight == 0
	})
	projector.Wake()
	select {
	case <-writer.observed:
	case <-time.After(time.Second):
		t.Fatal("later hash-slot task was starved by failing tasks in the first hash slot")
	}
}

func TestProjectorProjectsOneSourcePageAsOneMembershipAndCompletionBatch(t *testing.T) {
	t.Parallel()

	tasks := []metadb.PersonDirectoryTask{
		{ChannelID: runtimechannelid.EncodePersonChannel("u1", "u2"), ChannelType: 1, CommittedTail: 9, CreatedAt: 123, Generation: 1},
		{ChannelID: runtimechannelid.EncodePersonChannel("u3", "u4"), ChannelType: 1, CommittedTail: 19, CreatedAt: 456, Generation: 1},
	}
	source := newProjectorTaskSource(7, tasks...)
	writer := &partialMembershipWriter{calls: make(chan []metadb.UserChannelMembership, 1)}
	projector, err := New(Options{Source: source, Memberships: writer})
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	if err := projector.Start(context.Background()); err != nil {
		t.Fatalf("Start(): %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := projector.Stop(ctx); err != nil {
			t.Fatalf("Stop(): %v", err)
		}
	})

	got := waitMembershipCall(t, writer.calls)
	if len(got) != 4 {
		t.Fatalf("membership batch size = %d, want 4 rows for two tasks", len(got))
	}
	assertProjectedMemberships(t, got[:2], tasks[0])
	if got[2].UID != "u3" || got[3].UID != "u4" || got[2].JoinSeq != 20 || got[3].JoinSeq != 20 {
		t.Fatalf("second task memberships = %#v, want u3/u4 at join seq 20", got[2:])
	}
	select {
	case <-source.completed:
	case <-time.After(time.Second):
		t.Fatal("batched tasks were not completed")
	}
	if source.completeCount() != len(tasks) {
		t.Fatalf("completed tasks = %d, want %d", source.completeCount(), len(tasks))
	}
}

func TestProjectorDoesNotProjectAfterSourceLeadershipIsLost(t *testing.T) {
	t.Parallel()

	task := metadb.PersonDirectoryTask{
		ChannelID: runtimechannelid.EncodePersonChannel("u1", "u2"), ChannelType: 1,
		CommittedTail: 9, CreatedAt: 123,
		Generation: 1,
	}
	writer := &countingMembershipWriter{}
	projector, err := New(Options{Source: nonLeaderTaskSource{}, Memberships: writer})
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	_, err = projector.projectBatch(context.Background(), ownedTaskBatch{tasks: []ownedTask{{hashSlot: 7, task: task}}})
	if !errors.Is(err, errSourceLeadershipLost) {
		t.Fatalf("project() error = %v, want source leadership loss", err)
	}
	if writer.calls != 0 {
		t.Fatalf("membership writes = %d, want 0 after leadership loss", writer.calls)
	}
}

func TestProjectorDoesNotWriteMembershipsAfterSourceTaskGenerationChanges(t *testing.T) {
	t.Parallel()

	task := metadb.PersonDirectoryTask{
		ChannelID: runtimechannelid.EncodePersonChannel("u1", "u2"), ChannelType: 1,
		CommittedTail: 9, CreatedAt: 123, Generation: 1,
	}
	source := &staleGenerationTaskSource{projectorTaskSource: *newProjectorTaskSource(7, task)}
	writer := &countingMembershipWriter{}
	projector, err := New(Options{Source: source, Memberships: writer})
	if err != nil {
		t.Fatalf("New(): %v", err)
	}

	_, err = projector.projectBatch(context.Background(), ownedTaskBatch{tasks: []ownedTask{{hashSlot: 7, task: task}}})
	if !errors.Is(err, metadb.ErrStaleMeta) {
		t.Fatalf("projectBatch() error = %v, want stale source generation", err)
	}
	if writer.calls != 0 {
		t.Fatalf("membership writes = %d, want 0 after source generation changed", writer.calls)
	}
}

func TestProjectorCombinesTasksFromDifferentSourceHashSlots(t *testing.T) {
	t.Parallel()

	tasks := []ownedTask{
		{hashSlot: 7, task: metadb.PersonDirectoryTask{ChannelID: runtimechannelid.EncodePersonChannel("u1", "u2"), ChannelType: 1, CreatedAt: 1, Generation: 1}},
		{hashSlot: 9, task: metadb.PersonDirectoryTask{ChannelID: runtimechannelid.EncodePersonChannel("u3", "u4"), ChannelType: 1, CreatedAt: 2, Generation: 1}},
	}
	source := &multiHashTaskSource{}
	writer := &partialMembershipWriter{calls: make(chan []metadb.UserChannelMembership, 1)}
	projector, err := New(Options{Source: source, Memberships: writer})
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	if _, err := projector.projectBatch(context.Background(), ownedTaskBatch{tasks: tasks}); err != nil {
		t.Fatalf("projectBatch(): %v", err)
	}
	if got := <-writer.calls; len(got) != 4 {
		t.Fatalf("membership rows = %d, want one four-row cross-source batch", len(got))
	}
	if len(source.completed) != 2 || source.completed[0].HashSlot != 7 || source.completed[1].HashSlot != 9 {
		t.Fatalf("completed task locations = %#v, want source hash slots 7 and 9", source.completed)
	}
}

func waitMembershipCall(t *testing.T, calls <-chan []metadb.UserChannelMembership) []metadb.UserChannelMembership {
	t.Helper()
	select {
	case call := <-calls:
		return call
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for membership projection")
		return nil
	}
}

func waitBlockingMembershipCall(t *testing.T, calls <-chan struct{}) {
	t.Helper()
	select {
	case <-calls:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for blocking membership projection")
	}
}

func waitPressureObservation(t *testing.T, observations <-chan PressureObservation, match func(PressureObservation) bool) PressureObservation {
	t.Helper()
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	for {
		select {
		case got := <-observations:
			if match(got) {
				return got
			}
		case <-timer.C:
			t.Fatal("timed out waiting for projector pressure observation")
			return PressureObservation{}
		}
	}
}

func assertProjectedMemberships(t *testing.T, got []metadb.UserChannelMembership, task metadb.PersonDirectoryTask) {
	t.Helper()
	if len(got) != 2 {
		t.Fatalf("memberships = %#v, want 2", got)
	}
	uids := map[string]bool{got[0].UID: true, got[1].UID: true}
	if !uids["u1"] || !uids["u2"] {
		t.Fatalf("membership UIDs = %q/%q, want canonical pair u1/u2", got[0].UID, got[1].UID)
	}
	for _, membership := range got {
		if membership.ChannelID != task.ChannelID || membership.ChannelType != task.ChannelType ||
			membership.JoinSeq != 10 || membership.ReadSeq != 9 || membership.DeletedToSeq != 9 ||
			membership.SourceVersion != task.Generation || membership.UpdatedAt != task.CreatedAt {
			t.Fatalf("membership = %#v, want task-bound initial state", membership)
		}
	}
}

type projectorTaskSource struct {
	mu          sync.Mutex
	hashSlot    metadb.HashSlot
	tasks       []metadb.PersonDirectoryTask
	completions int
	completed   chan struct{}
}

func newProjectorTaskSource(hashSlot metadb.HashSlot, tasks ...metadb.PersonDirectoryTask) *projectorTaskSource {
	return &projectorTaskSource{hashSlot: hashSlot, tasks: append([]metadb.PersonDirectoryTask(nil), tasks...), completed: make(chan struct{}, 1)}
}

func (s *projectorTaskSource) LocalLeaderHashSlots(context.Context) ([]metadb.HashSlot, error) {
	return []metadb.HashSlot{s.hashSlot}, nil
}

func (s *projectorTaskSource) IsLocalLeaderHashSlot(_ context.Context, hashSlot metadb.HashSlot) (bool, error) {
	return hashSlot == s.hashSlot, nil
}

func (s *projectorTaskSource) ListPersonDirectoryTaskPage(_ context.Context, hashSlot metadb.HashSlot, after metadb.PersonDirectoryTaskCursor, limit int) ([]metadb.PersonDirectoryTask, metadb.PersonDirectoryTaskCursor, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if hashSlot != s.hashSlot || len(s.tasks) == 0 {
		return nil, metadb.PersonDirectoryTaskCursor{}, true, nil
	}
	start := 0
	if after != (metadb.PersonDirectoryTaskCursor{}) {
		for start < len(s.tasks) {
			task := s.tasks[start]
			start++
			if task.ChannelID == after.ChannelID && task.ChannelType == after.ChannelType {
				break
			}
		}
	}
	if start == len(s.tasks) {
		return nil, after, true, nil
	}
	end := min(start+limit, len(s.tasks))
	rows := append([]metadb.PersonDirectoryTask(nil), s.tasks[start:end]...)
	last := rows[len(rows)-1]
	return rows, metadb.PersonDirectoryTaskCursor{ChannelID: last.ChannelID, ChannelType: last.ChannelType}, end == len(s.tasks), nil
}

func (s *projectorTaskSource) ValidatePersonDirectoryTasks(_ context.Context, locations []metadb.PersonDirectoryTaskLocation) []error {
	s.mu.Lock()
	defer s.mu.Unlock()
	results := make([]error, len(locations))
	for i, location := range locations {
		valid := false
		for _, task := range s.tasks {
			if location.HashSlot == s.hashSlot && location.ChannelID == task.ChannelID &&
				location.ChannelType == task.ChannelType && location.Generation == task.Generation {
				valid = true
				break
			}
		}
		if !valid {
			results[i] = metadb.ErrStaleMeta
		}
	}
	return results
}

func (s *projectorTaskSource) CompletePersonDirectoryTasks(_ context.Context, tasks []metadb.PersonDirectoryTaskLocation) []error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(tasks) == 0 {
		return []error{errors.New("unexpected completion")}
	}
	completed := make(map[metadb.ChannelKey]uint64, len(tasks))
	for _, task := range tasks {
		if task.HashSlot != s.hashSlot {
			return []error{errors.New("unexpected completion")}
		}
		completed[metadb.ChannelKey{ChannelID: task.ChannelID, ChannelType: task.ChannelType}] = task.Generation
	}
	remaining := s.tasks[:0]
	for _, task := range s.tasks {
		key := metadb.ChannelKey{ChannelID: task.ChannelID, ChannelType: task.ChannelType}
		if generation, ok := completed[key]; ok {
			if generation != task.Generation {
				return []error{errors.New("unexpected completion generation")}
			}
			s.completions++
			delete(completed, key)
			continue
		}
		remaining = append(remaining, task)
	}
	if len(completed) != 0 {
		return []error{errors.New("unexpected completion")}
	}
	s.tasks = remaining
	select {
	case s.completed <- struct{}{}:
	default:
	}
	return make([]error, len(tasks))
}

func (s *projectorTaskSource) completeCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.completions
}

type partialMembershipWriter struct {
	mu    sync.Mutex
	fail  bool
	calls chan []metadb.UserChannelMembership
}

type recordingPressureObserver struct {
	observations chan PressureObservation
}

func (o *recordingPressureObserver) ObservePersonDirectoryPressure(observation PressureObservation) {
	o.observations <- observation
}

type nonLeaderTaskSource struct{}

type staleGenerationTaskSource struct {
	projectorTaskSource
}

func (*staleGenerationTaskSource) ValidatePersonDirectoryTasks(context.Context, []metadb.PersonDirectoryTaskLocation) []error {
	return []error{metadb.ErrStaleMeta}
}

func (nonLeaderTaskSource) LocalLeaderHashSlots(context.Context) ([]metadb.HashSlot, error) {
	return nil, nil
}

func (nonLeaderTaskSource) IsLocalLeaderHashSlot(context.Context, metadb.HashSlot) (bool, error) {
	return false, nil
}

func (nonLeaderTaskSource) ListPersonDirectoryTaskPage(context.Context, metadb.HashSlot, metadb.PersonDirectoryTaskCursor, int) ([]metadb.PersonDirectoryTask, metadb.PersonDirectoryTaskCursor, bool, error) {
	return nil, metadb.PersonDirectoryTaskCursor{}, true, nil
}

func (nonLeaderTaskSource) ValidatePersonDirectoryTasks(context.Context, []metadb.PersonDirectoryTaskLocation) []error {
	return []error{metadb.ErrStaleMeta}
}

func (nonLeaderTaskSource) CompletePersonDirectoryTasks(context.Context, []metadb.PersonDirectoryTaskLocation) []error {
	return []error{errors.New("unexpected task completion")}
}

type countingMembershipWriter struct{ calls int }

func (w *countingMembershipWriter) EnsureUserChannelMembershipBatch(context.Context, []metadb.UserChannelMembership) []MembershipResult {
	w.calls++
	return nil
}

type blockingMembershipWriter struct{}

func (blockingMembershipWriter) EnsureUserChannelMembershipBatch(ctx context.Context, memberships []metadb.UserChannelMembership) []MembershipResult {
	<-ctx.Done()
	results := make([]MembershipResult, len(memberships))
	for i := range results {
		results[i].Err = ctx.Err()
	}
	return results
}

type contextBlockingMembershipWriter struct {
	calls chan struct{}
}

func (w *contextBlockingMembershipWriter) EnsureUserChannelMembershipBatch(ctx context.Context, memberships []metadb.UserChannelMembership) []MembershipResult {
	w.calls <- struct{}{}
	<-ctx.Done()
	results := make([]MembershipResult, len(memberships))
	for i := range results {
		results[i].Err = ctx.Err()
	}
	return results
}

type multiHashTaskSource struct {
	completed []metadb.PersonDirectoryTaskLocation
}

func (*multiHashTaskSource) LocalLeaderHashSlots(context.Context) ([]metadb.HashSlot, error) {
	return []metadb.HashSlot{7, 9}, nil
}

func (*multiHashTaskSource) IsLocalLeaderHashSlot(context.Context, metadb.HashSlot) (bool, error) {
	return true, nil
}

func (*multiHashTaskSource) ListPersonDirectoryTaskPage(context.Context, metadb.HashSlot, metadb.PersonDirectoryTaskCursor, int) ([]metadb.PersonDirectoryTask, metadb.PersonDirectoryTaskCursor, bool, error) {
	return nil, metadb.PersonDirectoryTaskCursor{}, true, nil
}

func (*multiHashTaskSource) ValidatePersonDirectoryTasks(_ context.Context, tasks []metadb.PersonDirectoryTaskLocation) []error {
	return make([]error, len(tasks))
}

func (s *multiHashTaskSource) CompletePersonDirectoryTasks(_ context.Context, tasks []metadb.PersonDirectoryTaskLocation) []error {
	s.completed = append(s.completed, tasks...)
	return make([]error, len(tasks))
}

type multiSlotProjectorTaskSource struct {
	mu    sync.Mutex
	slots []metadb.HashSlot
	tasks map[metadb.HashSlot][]metadb.PersonDirectoryTask
}

func newMultiSlotProjectorTaskSource(tasks map[metadb.HashSlot][]metadb.PersonDirectoryTask) *multiSlotProjectorTaskSource {
	slots := make([]metadb.HashSlot, 0, len(tasks))
	for hashSlot := range tasks {
		slots = append(slots, hashSlot)
	}
	sort.Slice(slots, func(i, j int) bool { return slots[i] < slots[j] })
	return &multiSlotProjectorTaskSource{slots: slots, tasks: tasks}
}

func (s *multiSlotProjectorTaskSource) LocalLeaderHashSlots(context.Context) ([]metadb.HashSlot, error) {
	return append([]metadb.HashSlot(nil), s.slots...), nil
}

func (s *multiSlotProjectorTaskSource) IsLocalLeaderHashSlot(_ context.Context, hashSlot metadb.HashSlot) (bool, error) {
	for _, candidate := range s.slots {
		if candidate == hashSlot {
			return true, nil
		}
	}
	return false, nil
}

func (s *multiSlotProjectorTaskSource) ListPersonDirectoryTaskPage(_ context.Context, hashSlot metadb.HashSlot, after metadb.PersonDirectoryTaskCursor, limit int) ([]metadb.PersonDirectoryTask, metadb.PersonDirectoryTaskCursor, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tasks := s.tasks[hashSlot]
	start := 0
	if after != (metadb.PersonDirectoryTaskCursor{}) {
		for start < len(tasks) {
			task := tasks[start]
			start++
			if task.ChannelID == after.ChannelID && task.ChannelType == after.ChannelType {
				break
			}
		}
	}
	if start >= len(tasks) {
		return nil, after, true, nil
	}
	end := min(start+limit, len(tasks))
	rows := append([]metadb.PersonDirectoryTask(nil), tasks[start:end]...)
	last := rows[len(rows)-1]
	next := metadb.PersonDirectoryTaskCursor{ChannelID: last.ChannelID, ChannelType: last.ChannelType}
	return rows, next, end == len(tasks), nil
}

func (s *multiSlotProjectorTaskSource) ValidatePersonDirectoryTasks(_ context.Context, locations []metadb.PersonDirectoryTaskLocation) []error {
	s.mu.Lock()
	defer s.mu.Unlock()
	results := make([]error, len(locations))
	for i, location := range locations {
		valid := false
		for _, task := range s.tasks[location.HashSlot] {
			if task.ChannelID == location.ChannelID && task.ChannelType == location.ChannelType && task.Generation == location.Generation {
				valid = true
				break
			}
		}
		if !valid {
			results[i] = metadb.ErrStaleMeta
		}
	}
	return results
}

func (*multiSlotProjectorTaskSource) CompletePersonDirectoryTasks(_ context.Context, tasks []metadb.PersonDirectoryTaskLocation) []error {
	results := make([]error, len(tasks))
	for i := range results {
		results[i] = errors.New("unexpected completion")
	}
	return results
}

type failingTargetMembershipWriter struct {
	targetUID string
	observed  chan struct{}
	once      sync.Once
}

func (w *failingTargetMembershipWriter) EnsureUserChannelMembershipBatch(_ context.Context, memberships []metadb.UserChannelMembership) []MembershipResult {
	for _, membership := range memberships {
		if membership.UID == w.targetUID {
			w.once.Do(func() { close(w.observed) })
		}
	}
	results := make([]MembershipResult, len(memberships))
	for i := range results {
		results[i].Err = errors.New("projection unavailable")
	}
	return results
}

func (w *partialMembershipWriter) EnsureUserChannelMembershipBatch(_ context.Context, memberships []metadb.UserChannelMembership) []MembershipResult {
	w.mu.Lock()
	fail := w.fail
	w.mu.Unlock()
	cloned := append([]metadb.UserChannelMembership(nil), memberships...)
	w.calls <- cloned
	results := make([]MembershipResult, len(memberships))
	if fail {
		results[1].Err = errors.New("second UID unavailable")
	}
	return results
}

func (w *partialMembershipWriter) setFail(fail bool) {
	w.mu.Lock()
	w.fail = fail
	w.mu.Unlock()
}
