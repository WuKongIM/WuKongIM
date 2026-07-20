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
) (multiraft.PreferredLeaderTransferDecision, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, preferredLeaderTransferCall{
		slotID:         slotID,
		expectedLeader: expectedLeader,
		expectedTerm:   expectedTerm,
		expectedVoters: append([]multiraft.NodeID(nil), expectedVoters...),
		preferred:      preferred,
	})
	return r.decision, nil
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
) (multiraft.PreferredLeaderTransferDecision, error) {
	<-ctx.Done()
	return "", ctx.Err()
}

type preferredLeaderObservation struct {
	decision string
	wait     bool
}

type recordingPreferredLeaderObserver struct {
	mu           sync.Mutex
	observations []preferredLeaderObservation
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

func (r *recordingPreferredLeaderRuntime) Calls() []preferredLeaderTransferCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]preferredLeaderTransferCall(nil), r.calls...)
}
