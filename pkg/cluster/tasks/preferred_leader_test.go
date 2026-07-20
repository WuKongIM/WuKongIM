package tasks

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"go.etcd.io/raft/v3/raftpb"
)

func TestPreferredLeaderReconcilerTransfersOnlyToEligiblePreferred(t *testing.T) {
	runtime := &recordingPreferredLeaderRuntime{
		statuses: map[multiraft.SlotID]multiraft.Status{
			1: preferredLeaderStatus(1, 7, 1, 2, 3),
		},
		decision: multiraft.PreferredLeaderTransferStarted,
	}
	reconciler := NewPreferredLeaderReconciler(PreferredLeaderReconcilerConfig{
		LocalNode:   1,
		Runtime:     runtime,
		Cooldown:    time.Minute,
		IntentGuard: currentPreferredLeaderIntent,
	})

	if err := reconciler.Reconcile(context.Background(), preferredLeaderSnapshot()); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	calls := runtime.Calls()
	if len(calls) != 1 {
		t.Fatalf("transfer calls = %#v, want one", calls)
	}
	if got := calls[0]; got.slotID != 1 || got.expectedLeader != 1 || got.expectedTerm != 7 || got.preferred != 2 {
		t.Fatalf("transfer call = %#v, want slot=1 source=1 term=7 preferred=2", got)
	}
	if got := calls[0].expectedVoters; len(got) != 3 || got[0] != 1 || got[1] != 2 || got[2] != 3 {
		t.Fatalf("expected voters = %v, want [1 2 3]", got)
	}
}

func TestPreferredLeaderReconcilerDefaultsStrictAttemptTimeout(t *testing.T) {
	reconciler := NewPreferredLeaderReconciler(PreferredLeaderReconcilerConfig{})
	if reconciler.cfg.AttemptTimeout != 250*time.Millisecond {
		t.Fatalf("AttemptTimeout = %v, want 250ms", reconciler.cfg.AttemptTimeout)
	}
}

func TestPreferredLeaderReconcilerRetainsActualWhenPreferredIsLagging(t *testing.T) {
	runtime := &recordingPreferredLeaderRuntime{
		statuses: map[multiraft.SlotID]multiraft.Status{
			1: preferredLeaderStatus(1, 7, 1, 2, 3),
		},
		decision: multiraft.PreferredLeaderTransferPreferredLagging,
	}
	reconciler := NewPreferredLeaderReconciler(PreferredLeaderReconcilerConfig{
		LocalNode:   1,
		Runtime:     runtime,
		Cooldown:    time.Minute,
		IntentGuard: currentPreferredLeaderIntent,
	})

	if err := reconciler.Reconcile(context.Background(), preferredLeaderSnapshot()); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	calls := runtime.Calls()
	if len(calls) != 1 || calls[0].preferred != 2 {
		t.Fatalf("transfer calls = %#v, want one strict check of lagging preferred 2 and no fallback", calls)
	}
}

func TestPreferredLeaderReconcilerReportsPhysicalSlotDecisionContext(t *testing.T) {
	runtime := &recordingPreferredLeaderRuntime{
		statuses: map[multiraft.SlotID]multiraft.Status{
			1: preferredLeaderStatus(1, 7, 1, 2, 3),
		},
		decision: multiraft.PreferredLeaderTransferPreferredLagging,
	}
	observer := &recordingPreferredLeaderObserver{}
	reconciler := NewPreferredLeaderReconciler(PreferredLeaderReconcilerConfig{
		LocalNode:   1,
		Runtime:     runtime,
		IntentGuard: currentPreferredLeaderIntent,
		Observer:    observer,
	})

	if err := reconciler.Reconcile(context.Background(), preferredLeaderSnapshot()); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	details := observer.detailSnapshot()
	if len(details) != 1 {
		t.Fatalf("detailed observations = %#v, want one", details)
	}
	got := details[0]
	if got.Decision != string(multiraft.PreferredLeaderTransferPreferredLagging) ||
		got.SlotID != 1 || got.ActualLeaderID != 1 || got.PreferredLeaderID != 2 ||
		got.RaftTerm != 7 || got.ConfigEpoch != 9 || got.StrictWaitDuration < 0 {
		t.Fatalf("detailed observation = %#v, want physical Slot and Raft decision context", got)
	}
}

func TestPreferredLeaderReconcilerReportsFreshWorkerLeaderAndTerm(t *testing.T) {
	runtime := &advancingPreferredLeaderRuntime{
		precheck: preferredLeaderStatus(1, 7, 1, 2, 3),
		worker:   preferredLeaderStatus(3, 8, 1, 2, 3),
	}
	observer := &recordingPreferredLeaderObserver{}
	reconciler := NewPreferredLeaderReconciler(PreferredLeaderReconcilerConfig{
		LocalNode:   1,
		Runtime:     runtime,
		IntentGuard: currentPreferredLeaderIntent,
		Observer:    observer,
	})

	if err := reconciler.Reconcile(context.Background(), preferredLeaderSnapshot()); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	details := observer.detailSnapshot()
	if len(details) != 1 {
		t.Fatalf("detailed observations = %#v, want one", details)
	}
	if got := details[0]; got.Decision != string(multiraft.PreferredLeaderTransferStaleIntent) ||
		got.ActualLeaderID != 3 || got.RaftTerm != 8 {
		t.Fatalf("detailed observation = %#v, want fresh worker leader=3 term=8", got)
	}
}

func TestPreferredLeaderReconcilerReportsRemotePreferredMatchAfterTransfer(t *testing.T) {
	runtime := &recordingPreferredLeaderRuntime{
		statuses: map[multiraft.SlotID]multiraft.Status{
			1: preferredLeaderStatus(1, 7, 1, 2, 3),
		},
		decision: multiraft.PreferredLeaderTransferStarted,
	}
	observer := &recordingPreferredLeaderObserver{}
	reconciler := NewPreferredLeaderReconciler(PreferredLeaderReconcilerConfig{
		LocalNode:   1,
		Runtime:     runtime,
		IntentGuard: currentPreferredLeaderIntent,
		Observer:    observer,
	})

	if err := reconciler.Reconcile(context.Background(), preferredLeaderSnapshot()); err != nil {
		t.Fatalf("first Reconcile() error = %v", err)
	}
	runtime.mu.Lock()
	runtime.statuses[1] = preferredLeaderStatus(2, 8, 1, 2, 3)
	runtime.mu.Unlock()
	if err := reconciler.Reconcile(context.Background(), preferredLeaderSnapshot()); err != nil {
		t.Fatalf("second Reconcile() error = %v", err)
	}

	details := observer.detailSnapshot()
	if len(details) != 2 {
		t.Fatalf("detailed observations = %#v, want transfer and remote match", details)
	}
	if got := details[1]; got.Decision != preferredLeaderDecisionMatch || got.ActualLeaderID != 2 || got.RaftTerm != 8 {
		t.Fatalf("recovery observation = %#v, want remote preferred match leader=2 term=8", got)
	}
	if observer.hasDecision(preferredLeaderDecisionMatch) {
		t.Fatalf("aggregate observations = %#v, remote follower must not inflate match rate", observer.snapshot())
	}
}

func TestPreferredLeaderReconcilerSkipsIneligiblePreferred(t *testing.T) {
	runtime := &recordingPreferredLeaderRuntime{
		statuses: map[multiraft.SlotID]multiraft.Status{
			1: preferredLeaderStatus(1, 7, 1, 3),
		},
		decision: multiraft.PreferredLeaderTransferStarted,
	}
	reconciler := NewPreferredLeaderReconciler(PreferredLeaderReconcilerConfig{
		LocalNode:   1,
		Runtime:     runtime,
		Cooldown:    time.Minute,
		IntentGuard: currentPreferredLeaderIntent,
	})

	if err := reconciler.Reconcile(context.Background(), preferredLeaderSnapshot()); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if calls := runtime.Calls(); len(calls) != 0 {
		t.Fatalf("transfer calls = %#v, want none when preferred is not a current voter", calls)
	}
}

func TestPreferredLeaderReconcilerCooldownPreventsDuplicateTransfer(t *testing.T) {
	now := time.Unix(100, 0)
	runtime := &recordingPreferredLeaderRuntime{
		statuses: map[multiraft.SlotID]multiraft.Status{
			1: preferredLeaderStatus(1, 7, 1, 2, 3),
		},
		decision: multiraft.PreferredLeaderTransferStarted,
	}
	reconciler := NewPreferredLeaderReconciler(PreferredLeaderReconcilerConfig{
		LocalNode:   1,
		Runtime:     runtime,
		Cooldown:    time.Minute,
		Now:         func() time.Time { return now },
		IntentGuard: currentPreferredLeaderIntent,
	})
	snapshot := preferredLeaderSnapshot()

	for i := 0; i < 2; i++ {
		if err := reconciler.Reconcile(context.Background(), snapshot); err != nil {
			t.Fatalf("Reconcile(%d) error = %v", i, err)
		}
	}
	if calls := runtime.Calls(); len(calls) != 1 {
		t.Fatalf("transfer calls = %#v, want one within cooldown", calls)
	}

	now = now.Add(time.Minute)
	if err := reconciler.Reconcile(context.Background(), snapshot); err != nil {
		t.Fatalf("Reconcile(after cooldown) error = %v", err)
	}
	if calls := runtime.Calls(); len(calls) != 2 {
		t.Fatalf("transfer calls = %#v, want retry after cooldown", calls)
	}
}

func TestPreferredLeaderReconcilerForgetsCooldownWhenSlotIsRemovedAndReAdded(t *testing.T) {
	now := time.Unix(100, 0)
	runtime := &recordingPreferredLeaderRuntime{
		statuses: map[multiraft.SlotID]multiraft.Status{
			1: preferredLeaderStatus(1, 7, 1, 2, 3),
		},
		decision: multiraft.PreferredLeaderTransferStarted,
	}
	reconciler := NewPreferredLeaderReconciler(PreferredLeaderReconcilerConfig{
		LocalNode:   1,
		Runtime:     runtime,
		Cooldown:    time.Minute,
		Now:         func() time.Time { return now },
		IntentGuard: currentPreferredLeaderIntent,
	})

	if err := reconciler.Reconcile(context.Background(), preferredLeaderSnapshot()); err != nil {
		t.Fatalf("Reconcile(initial) error = %v", err)
	}
	if err := reconciler.Reconcile(context.Background(), control.Snapshot{Revision: 2}); err != nil {
		t.Fatalf("Reconcile(removed) error = %v", err)
	}
	readded := preferredLeaderSnapshot()
	readded.Revision = 3
	if err := reconciler.Reconcile(context.Background(), readded); err != nil {
		t.Fatalf("Reconcile(re-added) error = %v", err)
	}
	if calls := runtime.Calls(); len(calls) != 2 {
		t.Fatalf("transfer calls = %#v, want fresh attempt after Slot removal and re-add", calls)
	}
}

func TestPreferredLeaderReconcilerConcurrentCallsReserveOneTransfer(t *testing.T) {
	now := time.Unix(100, 0)
	runtime := &recordingPreferredLeaderRuntime{
		statuses: map[multiraft.SlotID]multiraft.Status{
			1: preferredLeaderStatus(1, 7, 1, 2, 3),
		},
		decision: multiraft.PreferredLeaderTransferStarted,
	}
	reconciler := NewPreferredLeaderReconciler(PreferredLeaderReconcilerConfig{
		LocalNode:   1,
		Runtime:     runtime,
		Cooldown:    time.Minute,
		Now:         func() time.Time { return now },
		IntentGuard: currentPreferredLeaderIntent,
	})

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := reconciler.Reconcile(context.Background(), preferredLeaderSnapshot()); err != nil {
				t.Errorf("Reconcile() error = %v", err)
			}
		}()
	}
	wg.Wait()
	if calls := runtime.Calls(); len(calls) != 1 {
		t.Fatalf("transfer calls = %#v, want one concurrent reservation", calls)
	}
}

func TestPreferredLeaderReconcilerSkipsWhileControllerTaskIsActive(t *testing.T) {
	runtime := &recordingPreferredLeaderRuntime{
		statuses: map[multiraft.SlotID]multiraft.Status{
			1: preferredLeaderStatus(1, 7, 1, 2, 3),
		},
		decision: multiraft.PreferredLeaderTransferStarted,
	}
	reconciler := NewPreferredLeaderReconciler(PreferredLeaderReconcilerConfig{LocalNode: 1, Runtime: runtime, IntentGuard: currentPreferredLeaderIntent})
	snapshot := preferredLeaderSnapshot()
	snapshot.Tasks = []control.ReconcileTask{{TaskID: "active"}}

	if err := reconciler.Reconcile(context.Background(), snapshot); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if calls := runtime.Calls(); len(calls) != 0 {
		t.Fatalf("transfer calls = %#v, want none while Controller task is active", calls)
	}
}

func TestPreferredLeaderReconcilerTimesOutStrictAttemptAsNoOp(t *testing.T) {
	runtime := &blockingPreferredLeaderRuntime{
		status: preferredLeaderStatus(1, 7, 1, 2, 3),
	}
	observer := &recordingPreferredLeaderObserver{}
	reconciler := NewPreferredLeaderReconciler(PreferredLeaderReconcilerConfig{
		LocalNode:      1,
		Runtime:        runtime,
		AttemptTimeout: 20 * time.Millisecond,
		IntentGuard:    currentPreferredLeaderIntent,
		Observer:       observer,
	})
	startedAt := time.Now()
	if err := reconciler.Reconcile(context.Background(), preferredLeaderSnapshot()); err != nil {
		t.Fatalf("Reconcile() error = %v, want bounded no-op", err)
	}
	if elapsed := time.Since(startedAt); elapsed > 200*time.Millisecond {
		t.Fatalf("Reconcile() elapsed = %v, want bounded strict attempt", elapsed)
	}
	if !observer.hasDecision(preferredLeaderDecisionTimeout) || !observer.hasWait(preferredLeaderDecisionTimeout) {
		t.Fatalf("observer = %+v, want timeout decision and wait", observer.snapshot())
	}
	details := observer.detailSnapshot()
	if len(details) != 1 || details[0].ActualLeaderID != 0 || details[0].RaftTerm != 0 {
		t.Fatalf("detailed observations = %#v, want unknown worker leader and term after pre-worker timeout", details)
	}
}

func TestPreferredLeaderReconcilerDoesNotReusePrecheckAfterStrictError(t *testing.T) {
	strictErr := errors.New("strict worker unavailable")
	runtime := &recordingPreferredLeaderRuntime{
		statuses: map[multiraft.SlotID]multiraft.Status{
			1: preferredLeaderStatus(1, 7, 1, 2, 3),
		},
		transferErr: strictErr,
	}
	observer := &recordingPreferredLeaderObserver{}
	reconciler := NewPreferredLeaderReconciler(PreferredLeaderReconcilerConfig{
		LocalNode:   1,
		Runtime:     runtime,
		IntentGuard: currentPreferredLeaderIntent,
		Observer:    observer,
	})

	if err := reconciler.Reconcile(context.Background(), preferredLeaderSnapshot()); !errors.Is(err, strictErr) {
		t.Fatalf("Reconcile() error = %v, want %v", err, strictErr)
	}
	details := observer.detailSnapshot()
	if len(details) != 1 || details[0].Decision != preferredLeaderDecisionError ||
		details[0].ActualLeaderID != 0 || details[0].RaftTerm != 0 {
		t.Fatalf("detailed observations = %#v, want error with unknown worker leader and term", details)
	}
}

func TestPreferredLeaderReconcilerRejectsStaleAppliedIntentAfterStatus(t *testing.T) {
	runtime := &recordingPreferredLeaderRuntime{
		statuses: map[multiraft.SlotID]multiraft.Status{1: preferredLeaderStatus(1, 7, 1, 2, 3)},
		decision: multiraft.PreferredLeaderTransferStarted,
	}
	checked := false
	reconciler := NewPreferredLeaderReconciler(PreferredLeaderReconcilerConfig{
		LocalNode: 1,
		Runtime:   runtime,
		IntentGuard: func(intent PreferredLeaderIntent) (multiraft.PreferredLeaderTransferGuard, bool) {
			if runtime.StatusCalls() == 0 {
				t.Error("latest intent callback ran before Runtime.Status")
			}
			checked = true
			return nil, false
		},
	})
	if err := reconciler.Reconcile(context.Background(), preferredLeaderSnapshot()); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if !checked {
		t.Fatal("latest applied intent was not checked")
	}
	if calls := runtime.Calls(); len(calls) != 0 {
		t.Fatalf("transfer calls = %#v, want none for stale applied intent", calls)
	}
}

func TestPreferredLeaderReconcilerRotatesStrictCheckCursor(t *testing.T) {
	statuses := make(map[multiraft.SlotID]multiraft.Status)
	snapshot := control.Snapshot{Revision: 1}
	for slotID := uint32(1); slotID <= 6; slotID++ {
		status := preferredLeaderStatus(1, 7, 1, 2, 3)
		status.SlotID = multiraft.SlotID(slotID)
		statuses[multiraft.SlotID(slotID)] = status
		snapshot.Slots = append(snapshot.Slots, control.SlotAssignment{
			SlotID: slotID, DesiredPeers: []uint64{1, 2, 3}, ConfigEpoch: 9, PreferredLeader: 2,
		})
	}
	now := time.Unix(100, 0)
	runtime := &recordingPreferredLeaderRuntime{statuses: statuses, decision: multiraft.PreferredLeaderTransferPreferredLagging}
	reconciler := NewPreferredLeaderReconciler(PreferredLeaderReconcilerConfig{
		LocalNode: 1, Runtime: runtime, Cooldown: time.Millisecond,
		Now:         func() time.Time { now = now.Add(time.Second); return now },
		IntentGuard: currentPreferredLeaderIntent,
	})
	for pass := 0; pass < 2; pass++ {
		if err := reconciler.Reconcile(context.Background(), snapshot); err != nil {
			t.Fatalf("Reconcile(%d) error = %v", pass, err)
		}
	}
	calls := runtime.Calls()
	if len(calls) != 8 {
		t.Fatalf("transfer calls = %#v, want four per pass", calls)
	}
	if calls[4].slotID != 5 || calls[5].slotID != 6 {
		t.Fatalf("second pass starts with slots %d,%d, want 5,6 after cursor rotation", calls[4].slotID, calls[5].slotID)
	}
}

func TestPreferredLeaderReconcilerAdvancesCursorAfterStatusError(t *testing.T) {
	statusErr := errors.New("slot status unavailable")
	runtime := &recordingPreferredLeaderRuntime{
		statuses: map[multiraft.SlotID]multiraft.Status{
			2: preferredLeaderStatus(1, 7, 1, 2, 3),
		},
		statusErrors: map[multiraft.SlotID]error{1: statusErr},
		decision:     multiraft.PreferredLeaderTransferStarted,
	}
	snapshot := control.Snapshot{
		Revision: 1,
		Slots: []control.SlotAssignment{
			{SlotID: 1, DesiredPeers: []uint64{1, 2, 3}, ConfigEpoch: 9, PreferredLeader: 2},
			{SlotID: 2, DesiredPeers: []uint64{1, 2, 3}, ConfigEpoch: 9, PreferredLeader: 2},
		},
	}
	reconciler := NewPreferredLeaderReconciler(PreferredLeaderReconcilerConfig{
		LocalNode:   1,
		Runtime:     runtime,
		IntentGuard: currentPreferredLeaderIntent,
	})

	if err := reconciler.Reconcile(context.Background(), snapshot); !errors.Is(err, statusErr) {
		t.Fatalf("first Reconcile() error = %v, want %v", err, statusErr)
	}
	if err := reconciler.Reconcile(context.Background(), snapshot); err != nil {
		t.Fatalf("second Reconcile() error = %v", err)
	}
	if calls := runtime.Calls(); len(calls) != 1 || calls[0].slotID != 2 {
		t.Fatalf("transfer calls = %#v, want next pass to start with healthy slot 2", calls)
	}
}

func preferredLeaderSnapshot() control.Snapshot {
	return control.Snapshot{
		Revision: 1,
		Slots: []control.SlotAssignment{{
			SlotID:          1,
			DesiredPeers:    []uint64{1, 2, 3},
			ConfigEpoch:     9,
			PreferredLeader: 2,
		}},
	}
}

func currentPreferredLeaderIntent(PreferredLeaderIntent) (multiraft.PreferredLeaderTransferGuard, bool) {
	return testPreferredLeaderGuard{ctx: context.Background()}, true
}

type testPreferredLeaderGuard struct {
	ctx context.Context
}

func (g testPreferredLeaderGuard) Context() context.Context { return g.ctx }

func (testPreferredLeaderGuard) ExecuteIfCurrent(action func()) bool {
	if action != nil {
		action()
	}
	return true
}

func preferredLeaderStatus(leader multiraft.NodeID, term uint64, voters ...multiraft.NodeID) multiraft.Status {
	ids := make([]uint64, 0, len(voters))
	for _, voter := range voters {
		ids = append(ids, uint64(voter))
	}
	return multiraft.Status{
		SlotID:        1,
		NodeID:        1,
		LeaderID:      leader,
		CurrentVoters: append([]multiraft.NodeID(nil), voters...),
		ConfState:     raftpb.ConfState{Voters: ids},
		Term:          term,
	}
}

type preferredLeaderTransferCall struct {
	slotID         multiraft.SlotID
	expectedLeader multiraft.NodeID
	expectedTerm   uint64
	expectedVoters []multiraft.NodeID
	preferred      multiraft.NodeID
}

type recordingPreferredLeaderRuntime struct {
	mu           sync.Mutex
	statuses     map[multiraft.SlotID]multiraft.Status
	statusErrors map[multiraft.SlotID]error
	decision     multiraft.PreferredLeaderTransferDecision
	transferErr  error
	calls        []preferredLeaderTransferCall
	statusCalls  int
}

func (r *recordingPreferredLeaderRuntime) Status(slotID multiraft.SlotID) (multiraft.Status, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.statusCalls++
	if err := r.statusErrors[slotID]; err != nil {
		return multiraft.Status{}, err
	}
	return r.statuses[slotID], nil
}

func (r *recordingPreferredLeaderRuntime) StatusCalls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.statusCalls
}

func (r *recordingPreferredLeaderRuntime) TryTransferLeadershipToPreferred(
	_ context.Context,
	slotID multiraft.SlotID,
	expectedLeader multiraft.NodeID,
	expectedTerm uint64,
	expectedVoters []multiraft.NodeID,
	preferred multiraft.NodeID,
	_ multiraft.PreferredLeaderTransferGuard,
) (multiraft.PreferredLeaderTransferResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, preferredLeaderTransferCall{
		slotID:         slotID,
		expectedLeader: expectedLeader,
		expectedTerm:   expectedTerm,
		expectedVoters: append([]multiraft.NodeID(nil), expectedVoters...),
		preferred:      preferred,
	})
	result := multiraft.PreferredLeaderTransferResult{Decision: r.decision}
	if r.transferErr == nil {
		workerStatus := r.statuses[slotID]
		result.ObservedLeaderID = workerStatus.LeaderID
		result.ObservedTerm = workerStatus.Term
		result.RaftObserved = true
	}
	return result, r.transferErr
}

type blockingPreferredLeaderRuntime struct {
	status multiraft.Status
}

func (r *blockingPreferredLeaderRuntime) Status(multiraft.SlotID) (multiraft.Status, error) {
	return r.status, nil
}

func (r *blockingPreferredLeaderRuntime) TryTransferLeadershipToPreferred(
	ctx context.Context,
	_ multiraft.SlotID,
	_ multiraft.NodeID,
	_ uint64,
	_ []multiraft.NodeID,
	_ multiraft.NodeID,
	_ multiraft.PreferredLeaderTransferGuard,
) (multiraft.PreferredLeaderTransferResult, error) {
	<-ctx.Done()
	return multiraft.PreferredLeaderTransferResult{}, ctx.Err()
}

type advancingPreferredLeaderRuntime struct {
	precheck multiraft.Status
	worker   multiraft.Status
}

func (r *advancingPreferredLeaderRuntime) Status(multiraft.SlotID) (multiraft.Status, error) {
	return r.precheck, nil
}

func (r *advancingPreferredLeaderRuntime) TryTransferLeadershipToPreferred(
	context.Context,
	multiraft.SlotID,
	multiraft.NodeID,
	uint64,
	[]multiraft.NodeID,
	multiraft.NodeID,
	multiraft.PreferredLeaderTransferGuard,
) (multiraft.PreferredLeaderTransferResult, error) {
	return multiraft.PreferredLeaderTransferResult{
		Decision:         multiraft.PreferredLeaderTransferStaleIntent,
		ObservedLeaderID: r.worker.LeaderID,
		ObservedTerm:     r.worker.Term,
		RaftObserved:     true,
	}, nil
}

type preferredLeaderObservation struct {
	decision string
	wait     bool
}

type recordingPreferredLeaderObserver struct {
	mu           sync.Mutex
	observations []preferredLeaderObservation
	details      []PreferredLeaderObservation
}

func (o *recordingPreferredLeaderObserver) ObservePreferredLeaderReconcile(observation PreferredLeaderObservation) {
	o.mu.Lock()
	o.details = append(o.details, observation)
	o.mu.Unlock()
}

func (o *recordingPreferredLeaderObserver) ObservePreferredLeaderDecision(decision string) {
	o.mu.Lock()
	o.observations = append(o.observations, preferredLeaderObservation{decision: decision})
	o.mu.Unlock()
}

func (o *recordingPreferredLeaderObserver) ObservePreferredLeaderStrictWait(decision string, _ time.Duration) {
	o.mu.Lock()
	o.observations = append(o.observations, preferredLeaderObservation{decision: decision, wait: true})
	o.mu.Unlock()
}

func (o *recordingPreferredLeaderObserver) hasDecision(decision string) bool {
	for _, observation := range o.snapshot() {
		if observation.decision == decision && !observation.wait {
			return true
		}
	}
	return false
}

func (o *recordingPreferredLeaderObserver) hasWait(decision string) bool {
	for _, observation := range o.snapshot() {
		if observation.decision == decision && observation.wait {
			return true
		}
	}
	return false
}

func (o *recordingPreferredLeaderObserver) snapshot() []preferredLeaderObservation {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]preferredLeaderObservation(nil), o.observations...)
}

func (o *recordingPreferredLeaderObserver) detailSnapshot() []PreferredLeaderObservation {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]PreferredLeaderObservation(nil), o.details...)
}

func (r *recordingPreferredLeaderRuntime) Calls() []preferredLeaderTransferCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]preferredLeaderTransferCall(nil), r.calls...)
}
