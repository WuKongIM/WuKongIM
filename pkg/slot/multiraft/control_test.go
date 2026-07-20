package multiraft

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"go.etcd.io/raft/v3"
	"go.etcd.io/raft/v3/quorum"
	"go.etcd.io/raft/v3/raftpb"
	"go.etcd.io/raft/v3/tracker"
)

func TestEnsurePendingConfigCapacityPreservesTrackedFuture(t *testing.T) {
	fut := newFuture(nil)
	g := &slot{
		pendingConfigs: map[uint64]trackedFuture{
			9: {future: fut, term: 4},
		},
	}

	g.ensurePendingConfigCapacity(3)

	tracked, ok := g.pendingConfigs[9]
	if !ok || tracked.future != fut || tracked.term != 4 {
		t.Fatalf("tracked future was lost: %+v ok=%v", tracked, ok)
	}
}

func TestChangeConfigAppliesAddLearner(t *testing.T) {
	rt := newStartedRuntime(t)
	slotID := openSingleNodeLeader(t, rt, 30)

	fut, err := rt.ChangeConfig(context.Background(), slotID, ConfigChange{
		Type:   AddLearner,
		NodeID: 2,
	})
	if err != nil {
		t.Fatalf("ChangeConfig() error = %v", err)
	}
	if _, err := fut.Wait(context.Background()); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
}

func TestTransferLeadershipRejectsUnknownSlot(t *testing.T) {
	rt := newStartedRuntime(t)
	err := rt.TransferLeadership(context.Background(), 999, 2)
	if !errors.Is(err, ErrSlotNotFound) {
		t.Fatalf("expected ErrSlotNotFound, got %v", err)
	}
}

func TestTryTransferLeadershipToPreferredRejectsUnknownSlot(t *testing.T) {
	rt := newStartedRuntime(t)
	decision, err := rt.TryTransferLeadershipToPreferred(context.Background(), 999, 1, 7, []NodeID{1, 2, 3}, 2, alwaysCurrentPreferredLeaderGuard{})
	if decision != "" || !errors.Is(err, ErrSlotNotFound) {
		t.Fatalf("TryTransferLeadershipToPreferred() = (%q, %v), want (empty, %v)", decision, err, ErrSlotNotFound)
	}
}

type alwaysCurrentPreferredLeaderGuard struct{}

func (alwaysCurrentPreferredLeaderGuard) Context() context.Context { return context.Background() }

func (alwaysCurrentPreferredLeaderGuard) ExecuteIfCurrent(action func()) bool {
	if action != nil {
		action()
	}
	return true
}

func TestClosingSlotCancelsQueuedStrictLeaderTransfer(t *testing.T) {
	g := newLeaderTestSlotForDrain()
	request := &strictLeaderTransferRequest{
		ctx:            context.Background(),
		expectedLeader: 1,
		expectedTerm:   7,
		expectedVoters: []NodeID{1, 2, 3},
		target:         2,
		resp:           make(chan strictLeaderTransferResponse, 1),
	}
	if err := g.enqueueControl(controlAction{kind: controlTransferLeader, strictTransfer: request}); err != nil {
		t.Fatalf("enqueueControl() error = %v", err)
	}

	g.mu.Lock()
	g.closed = true
	g.failPendingLocked(ErrSlotClosed)
	g.mu.Unlock()

	select {
	case response := <-request.resp:
		if response.decision != "" || !errors.Is(response.err, ErrSlotClosed) {
			t.Fatalf("strict response = %+v, want canceled with %v", response, ErrSlotClosed)
		}
	case <-time.After(time.Second):
		t.Fatal("queued strict transfer was not canceled when Slot closed")
	}
	if request.claim() {
		t.Fatal("canceled strict transfer remained claimable")
	}
}

func TestStrictLeaderTransferTimeoutCancelsClaimedRequestBeforeExecution(t *testing.T) {
	request := &strictLeaderTransferRequest{
		resp: make(chan strictLeaderTransferResponse, 1),
	}
	if !request.claim() {
		t.Fatal("request was not claimed")
	}
	if !request.cancel(context.DeadlineExceeded) {
		t.Fatal("processing request was not canceled before execution")
	}
	if request.beginExecution() {
		t.Fatal("timed-out request crossed into execution")
	}
	response := <-request.resp
	if !errors.Is(response.err, context.DeadlineExceeded) {
		t.Fatalf("response error = %v, want deadline exceeded", response.err)
	}
}

func TestExtractedStrictLeaderTransferRejectsTerminalSlotBeforeExecution(t *testing.T) {
	fatalErr := errors.New("slot fatal")
	tests := []struct {
		name    string
		prepare func(*slot)
		wantErr error
	}{
		{name: "close slot", prepare: func(g *slot) { g.closed = true }, wantErr: ErrSlotClosed},
		{name: "fatal", prepare: func(g *slot) { g.fatalErr = fatalErr }, wantErr: fatalErr},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := newLeaderTestSlotForDrain()
			request := newClaimedStrictLeaderTransferRequest(alwaysCurrentPreferredLeaderGuard{})
			g.mu.Lock()
			tt.prepare(g)
			g.mu.Unlock()

			// The action has already been extracted from g.controls and claimed by
			// the worker. The final g.mu-fenced admission check must still win.
			g.executeStrictLeaderTransfer(request, 2)
			response := <-request.resp
			if response.decision != "" || !errors.Is(response.err, tt.wantErr) {
				t.Fatalf("strict response = %+v, want terminal error %v", response, tt.wantErr)
			}
		})
	}
}

func TestRuntimeCloseCancelsQueuedStrictLeaderTransfer(t *testing.T) {
	rt := newStartedRuntime(t)
	const slotID SlotID = 77
	if err := rt.OpenSlot(context.Background(), newInternalSlotOptions(slotID)); err != nil {
		t.Fatalf("OpenSlot() error = %v", err)
	}
	rt.mu.RLock()
	g := rt.slots[slotID]
	rt.mu.RUnlock()
	if g == nil {
		t.Fatal("opened Slot is missing from Runtime")
	}

	// Keep the real worker from extracting the request so Runtime.Close must
	// cancel it through failPendingLocked before waiting for Slot idleness.
	g.mu.Lock()
	g.processing = true
	g.mu.Unlock()

	type result struct {
		decision PreferredLeaderTransferDecision
		err      error
	}
	resultCh := make(chan result, 1)
	go func() {
		decision, err := rt.TryTransferLeadershipToPreferred(
			context.Background(), slotID, 1, 7, []NodeID{1, 2, 3}, 2,
			alwaysCurrentPreferredLeaderGuard{},
		)
		resultCh <- result{decision: decision, err: err}
	}()

	deadline := time.Now().Add(time.Second)
	for {
		g.mu.Lock()
		queued := len(g.controls) == 1 && g.controls[0].strictTransfer != nil
		g.mu.Unlock()
		if queued {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("strict transfer was not queued before Runtime.Close")
		}
		time.Sleep(time.Millisecond)
	}

	closeDone := make(chan error, 1)
	go func() { closeDone <- rt.Close() }()
	select {
	case got := <-resultCh:
		if got.decision != "" || !errors.Is(got.err, ErrRuntimeClosed) {
			t.Fatalf("strict result = (%q, %v), want (empty, %v)", got.decision, got.err, ErrRuntimeClosed)
		}
	case <-time.After(time.Second):
		t.Fatal("Runtime.Close did not cancel the queued strict transfer")
	}

	g.mu.Lock()
	g.processing = false
	if g.cond != nil {
		g.cond.Broadcast()
	}
	g.mu.Unlock()
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("Runtime.Close() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Runtime.Close did not finish after Slot became idle")
	}
}

func TestExtractedStrictLeaderTransferRejectsInvalidatedIntentAtExecutionGap(t *testing.T) {
	g := newLeaderTestSlotForDrain()
	guard := newInvalidatablePreferredLeaderGuard()
	request := newClaimedStrictLeaderTransferRequest(guard)
	guard.invalidate()

	// Fresh Raft selection has already happened. The shared execution guard
	// must prevent the final TransferLeader issue after Controller invalidation.
	g.executeStrictLeaderTransfer(request, 2)
	response := <-request.resp
	if response.err != nil || response.decision != PreferredLeaderTransferStaleIntent {
		t.Fatalf("strict response = %+v, want stale_intent no-op", response)
	}
}

func newClaimedStrictLeaderTransferRequest(guard PreferredLeaderTransferGuard) *strictLeaderTransferRequest {
	request := &strictLeaderTransferRequest{
		ctx:            context.Background(),
		expectedLeader: 1,
		expectedTerm:   7,
		expectedVoters: []NodeID{1, 2, 3},
		target:         2,
		guard:          guard,
		resp:           make(chan strictLeaderTransferResponse, 1),
	}
	request.claim()
	return request
}

type invalidatablePreferredLeaderGuard struct {
	mu      sync.Mutex
	ctx     context.Context
	cancel  context.CancelFunc
	current bool
}

func newInvalidatablePreferredLeaderGuard() *invalidatablePreferredLeaderGuard {
	ctx, cancel := context.WithCancel(context.Background())
	return &invalidatablePreferredLeaderGuard{ctx: ctx, cancel: cancel, current: true}
}

func (g *invalidatablePreferredLeaderGuard) Context() context.Context { return g.ctx }

func (g *invalidatablePreferredLeaderGuard) ExecuteIfCurrent(action func()) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.current {
		return false
	}
	if action != nil {
		action()
	}
	return true
}

func (g *invalidatablePreferredLeaderGuard) invalidate() {
	g.mu.Lock()
	g.current = false
	g.cancel()
	g.mu.Unlock()
}

func TestExpectLeaderTransferRejectsUnknownSlot(t *testing.T) {
	rt := newStartedRuntime(t)
	err := rt.ExpectLeaderTransfer(context.Background(), 999, 2)
	if !errors.Is(err, ErrSlotNotFound) {
		t.Fatalf("expected ErrSlotNotFound, got %v", err)
	}
}

func TestBootstrapSlotCampaignsAfterInitialMembershipIsReady(t *testing.T) {
	transport := &recordingTransport{}
	rt, err := New(Options{
		NodeID:       1,
		TickInterval: time.Hour,
		Workers:      1,
		Transport:    transport,
		Raft: RaftOptions{
			ElectionTick:  10,
			HeartbeatTick: 1,
		},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() {
		if err := rt.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})

	if err := rt.BootstrapSlot(context.Background(), BootstrapSlotRequest{
		Slot:     newInternalSlotOptions(100),
		Voters:   []NodeID{1, 2, 3},
		Campaign: true,
	}); err != nil {
		t.Fatalf("BootstrapSlot() error = %v", err)
	}

	waitForCondition(t, func() bool {
		return transport.countMessagesOfType(raftpb.MsgVote) > 0
	})
}

func TestBootstrapSlotRejectsCampaignFromNonVoter(t *testing.T) {
	rt := newStartedRuntimeWithTick(t, time.Hour)
	err := rt.BootstrapSlot(context.Background(), BootstrapSlotRequest{
		Slot:     newInternalSlotOptions(101),
		Voters:   []NodeID{2, 3},
		Campaign: true,
	})
	if !errors.Is(err, ErrInvalidOptions) {
		t.Fatalf("BootstrapSlot() error = %v, want %v", err, ErrInvalidOptions)
	}
}

func TestSelectLeaderTransferTransfereePrefersEligiblePreferred(t *testing.T) {
	st := raft.Status{
		BasicStatus: raft.BasicStatus{
			SoftState: raft.SoftState{Lead: 1},
			HardState: raftpb.HardState{Commit: 10},
		},
		Config: tracker.Config{Voters: quorum.JointConfig{
			quorum.MajorityConfig{1: {}, 2: {}, 3: {}},
		}},
		Progress: map[uint64]tracker.Progress{
			1: {Match: 10},
			2: {Match: 10},
			3: {Match: 10},
		},
	}

	if got := selectLeaderTransferTransferee(st, 2); got != 2 {
		t.Fatalf("selectLeaderTransferTransferee() = %d, want preferred 2", got)
	}
}

func TestSelectStrictLeaderTransferTargetAcceptsEligiblePreferred(t *testing.T) {
	st := raft.Status{
		BasicStatus: raft.BasicStatus{
			ID:        1,
			SoftState: raft.SoftState{Lead: 1, RaftState: raft.StateLeader},
			HardState: raftpb.HardState{Term: 7, Commit: 10},
		},
		Config: tracker.Config{Voters: quorum.JointConfig{
			quorum.MajorityConfig{1: {}, 2: {}, 3: {}},
		}},
		Progress: map[uint64]tracker.Progress{
			1: {Match: 12, RecentActive: true},
			2: {Match: 10, RecentActive: true},
			3: {Match: 9, RecentActive: true},
		},
	}

	if got, decision := selectStrictLeaderTransferTarget(st, 1, 7, []NodeID{1, 2, 3}, 2); got != 2 || decision != PreferredLeaderTransferStarted {
		t.Fatalf("selectStrictLeaderTransferTarget() = (%d, %q), want eligible preferred 2 and transfer_started", got, decision)
	}
}

func TestSelectStrictLeaderTransferTargetRejectsUnsafeOrStaleRequest(t *testing.T) {
	base := raft.Status{
		BasicStatus: raft.BasicStatus{
			ID:        1,
			SoftState: raft.SoftState{Lead: 1, RaftState: raft.StateLeader},
			HardState: raftpb.HardState{Term: 7, Commit: 10},
		},
		Config: tracker.Config{Voters: quorum.JointConfig{
			quorum.MajorityConfig{1: {}, 2: {}, 3: {}},
		}},
		Progress: map[uint64]tracker.Progress{
			1: {Match: 12, RecentActive: true},
			2: {Match: 9, RecentActive: true},
			3: {Match: 10, RecentActive: true},
		},
	}
	inactive := base
	inactive.Progress = map[uint64]tracker.Progress{
		1: {Match: 12, RecentActive: true},
		2: {Match: 9, RecentActive: true},
		3: {Match: 10},
	}
	transferring := base
	transferring.LeadTransferee = 3

	tests := []struct {
		name           string
		status         raft.Status
		expectedLeader NodeID
		expectedTerm   uint64
		expectedVoters []NodeID
		preferred      NodeID
		wantDecision   PreferredLeaderTransferDecision
	}{
		{name: "preferred behind commit", status: base, expectedLeader: 1, expectedTerm: 7, expectedVoters: []NodeID{1, 2, 3}, preferred: 2, wantDecision: PreferredLeaderTransferPreferredLagging},
		{name: "preferred not voter", status: base, expectedLeader: 1, expectedTerm: 7, expectedVoters: []NodeID{1, 2, 3}, preferred: 4, wantDecision: PreferredLeaderTransferVoterMismatch},
		{name: "stale leader", status: base, expectedLeader: 2, expectedTerm: 7, expectedVoters: []NodeID{1, 2, 3}, preferred: 3, wantDecision: PreferredLeaderTransferStaleIntent},
		{name: "stale term", status: base, expectedLeader: 1, expectedTerm: 6, expectedVoters: []NodeID{1, 2, 3}, preferred: 3, wantDecision: PreferredLeaderTransferStaleIntent},
		{name: "stale voter set", status: base, expectedLeader: 1, expectedTerm: 7, expectedVoters: []NodeID{1, 2, 4}, preferred: 3, wantDecision: PreferredLeaderTransferVoterMismatch},
		{name: "duplicate expected voter", status: base, expectedLeader: 1, expectedTerm: 7, expectedVoters: []NodeID{1, 2, 2}, preferred: 3, wantDecision: PreferredLeaderTransferVoterMismatch},
		{name: "zero expected voter", status: base, expectedLeader: 1, expectedTerm: 7, expectedVoters: []NodeID{0, 2, 3}, preferred: 3, wantDecision: PreferredLeaderTransferVoterMismatch},
		{name: "preferred inactive", status: inactive, expectedLeader: 1, expectedTerm: 7, expectedVoters: []NodeID{1, 2, 3}, preferred: 3, wantDecision: PreferredLeaderTransferPreferredInactive},
		{name: "transfer already in progress", status: transferring, expectedLeader: 1, expectedTerm: 7, expectedVoters: []NodeID{1, 2, 3}, preferred: 3, wantDecision: PreferredLeaderTransferInProgress},
		{name: "preferred already leader", status: base, expectedLeader: 1, expectedTerm: 7, expectedVoters: []NodeID{1, 2, 3}, preferred: 1, wantDecision: PreferredLeaderTransferStaleIntent},
	}
	joint := base
	joint.Config.Voters[1] = quorum.MajorityConfig{1: {}, 2: {}, 3: {}}
	tests = append(tests, struct {
		name           string
		status         raft.Status
		expectedLeader NodeID
		expectedTerm   uint64
		expectedVoters []NodeID
		preferred      NodeID
		wantDecision   PreferredLeaderTransferDecision
	}{name: "joint membership", status: joint, expectedLeader: 1, expectedTerm: 7, expectedVoters: []NodeID{1, 2, 3}, preferred: 3, wantDecision: PreferredLeaderTransferJointConfig})
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, decision := selectStrictLeaderTransferTarget(tt.status, tt.expectedLeader, tt.expectedTerm, tt.expectedVoters, tt.preferred); got != 0 || decision != tt.wantDecision {
				t.Fatalf("selectStrictLeaderTransferTarget() = (%d, %q), want (0, %q)", got, decision, tt.wantDecision)
			}
		})
	}
}

func TestSelectStrictLeaderTransferTargetPreservesManualTransferTarget(t *testing.T) {
	st := raft.Status{
		BasicStatus: raft.BasicStatus{
			ID:             1,
			SoftState:      raft.SoftState{Lead: 1, RaftState: raft.StateLeader},
			HardState:      raftpb.HardState{Term: 7, Commit: 10},
			LeadTransferee: 3,
		},
		Config: tracker.Config{Voters: quorum.JointConfig{
			quorum.MajorityConfig{1: {}, 2: {}, 3: {}},
		}},
		Progress: map[uint64]tracker.Progress{
			1: {Match: 10, RecentActive: true},
			2: {Match: 10, RecentActive: true},
			3: {Match: 10, RecentActive: true},
		},
	}

	target, decision := selectStrictLeaderTransferTarget(st, 1, 7, []NodeID{1, 2, 3}, 2)
	if target != 0 || decision != PreferredLeaderTransferInProgress || st.LeadTransferee != 3 {
		t.Fatalf("strict selection = (%d, %q), manual target=%d; want no-op preserving target 3", target, decision, st.LeadTransferee)
	}
}

func TestSelectLeaderTransferTransfereeFallsBackWhenPreferredBehind(t *testing.T) {
	st := raft.Status{
		BasicStatus: raft.BasicStatus{
			SoftState: raft.SoftState{Lead: 1},
			HardState: raftpb.HardState{Commit: 10},
		},
		Config: tracker.Config{Voters: quorum.JointConfig{
			quorum.MajorityConfig{1: {}, 2: {}, 3: {}, 4: {}},
		}},
		Progress: map[uint64]tracker.Progress{
			1: {Match: 10},
			2: {Match: 9},
			3: {Match: 10},
			4: {Match: 10},
		},
	}

	if got := selectLeaderTransferTransferee(st, 2); got != 3 {
		t.Fatalf("selectLeaderTransferTransferee() = %d, want eligible voter 3", got)
	}
}

func TestSelectLeaderTransferTransfereePrefersCommittedPreferredWithActiveLeaderTail(t *testing.T) {
	st := raft.Status{
		BasicStatus: raft.BasicStatus{
			SoftState: raft.SoftState{Lead: 1},
			HardState: raftpb.HardState{Commit: 10},
		},
		Config: tracker.Config{Voters: quorum.JointConfig{
			quorum.MajorityConfig{1: {}, 2: {}, 3: {}},
		}},
		Progress: map[uint64]tracker.Progress{
			1: {Match: 12},
			2: {Match: 10},
			3: {Match: 12},
		},
	}

	if got := selectLeaderTransferTransferee(st, 2); got != 2 {
		t.Fatalf("selectLeaderTransferTransferee() = %d, want committed preferred voter 2", got)
	}
}

func TestSelectLeaderTransferTransfereeAllowsCommittedPreferredWithoutFullyCaughtUpPeer(t *testing.T) {
	st := raft.Status{
		BasicStatus: raft.BasicStatus{
			SoftState: raft.SoftState{Lead: 1},
			HardState: raftpb.HardState{Commit: 10},
		},
		Config: tracker.Config{Voters: quorum.JointConfig{
			quorum.MajorityConfig{1: {}, 2: {}, 3: {}},
		}},
		Progress: map[uint64]tracker.Progress{
			1: {Match: 12},
			2: {Match: 10},
			3: {Match: 9},
		},
	}

	if got := selectLeaderTransferTransferee(st, 2); got != 2 {
		t.Fatalf("selectLeaderTransferTransferee() = %d, want committed preferred voter 2", got)
	}
}

func TestSelectLeaderTransferTransfereeReturnsZeroWithoutEligibleVoter(t *testing.T) {
	st := raft.Status{
		BasicStatus: raft.BasicStatus{
			SoftState: raft.SoftState{Lead: 1},
			HardState: raftpb.HardState{Commit: 10},
		},
		Config: tracker.Config{Voters: quorum.JointConfig{
			quorum.MajorityConfig{1: {}, 2: {}, 3: {}},
		}},
		Progress: map[uint64]tracker.Progress{
			1: {Match: 10},
			2: {Match: 9},
			3: {Match: 8},
		},
	}

	if got := selectLeaderTransferTransferee(st, 2); got != 0 {
		t.Fatalf("selectLeaderTransferTransferee() = %d, want 0", got)
	}
}

func TestChangeConfigCorrelatesFutureByCommittedIndex(t *testing.T) {
	rt := newStartedRuntime(t)
	slotID := openSingleNodeLeader(t, rt, 31)

	fut, err := rt.ChangeConfig(context.Background(), slotID, ConfigChange{
		Type:   AddLearner,
		NodeID: 2,
	})
	if err != nil {
		t.Fatalf("ChangeConfig() error = %v", err)
	}

	res, err := fut.Wait(context.Background())
	if err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	if res.Index == 0 {
		t.Fatalf("Wait().Index = 0")
	}

	st, err := rt.Status(slotID)
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if res.Index != st.AppliedIndex {
		t.Fatalf("Wait().Index = %d, want applied index %d", res.Index, st.AppliedIndex)
	}
}

func TestChangeConfigRejectsWhileAnotherConfigChangeIsPending(t *testing.T) {
	rt := newStartedRuntime(t)
	slotID := SlotID(34)
	store := newBlockingMarkAppliedStorage()

	err := rt.BootstrapSlot(context.Background(), BootstrapSlotRequest{
		Slot: SlotOptions{
			ID:           slotID,
			Storage:      store,
			StateMachine: &internalFakeStateMachine{},
		},
		Voters: []NodeID{1},
	})
	if err != nil {
		t.Fatalf("BootstrapSlot() error = %v", err)
	}

	waitForCondition(t, func() bool {
		st, err := rt.Status(slotID)
		return err == nil && st.Role == RoleLeader
	})

	store.internalFakeStorage.mu.Lock()
	baselineApplied := store.internalFakeStorage.lastApplied
	store.internalFakeStorage.mu.Unlock()
	store.armAfter(baselineApplied + 1)

	first := mustChangeConfig(t, rt, slotID, 2)

	select {
	case <-store.started:
	case <-time.After(time.Second):
		t.Fatal("first MarkApplied() did not start")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := first.Wait(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("first Wait() error = %v, want %v", err, context.DeadlineExceeded)
	}

	second, err := rt.ChangeConfig(context.Background(), slotID, ConfigChange{
		Type:   AddLearner,
		NodeID: 3,
	})
	if !errors.Is(err, ErrConfigChangePending) {
		t.Fatalf("expected ErrConfigChangePending, got future=%v err=%v", second, err)
	}

	store.unblock()

	if _, err := first.Wait(context.Background()); err != nil {
		t.Fatalf("first Wait() after unblock error = %v", err)
	}
}

func TestChangeConfigAllowsNextConfigChangeAfterPreviousApplied(t *testing.T) {
	rt := newStartedRuntime(t)
	slotID := openSingleNodeLeader(t, rt, 35)

	first := waitForFutureResult(t, mustChangeConfig(t, rt, slotID, 2))
	second := waitForFutureResult(t, mustChangeConfig(t, rt, slotID, 3))

	if second.Index <= first.Index {
		t.Fatalf("second.Index = %d, want > first.Index %d", second.Index, first.Index)
	}
}

func TestRemoteConfigChangeDoesNotResolveLocalFuture(t *testing.T) {
	cluster := newAsyncTestCluster(t, []NodeID{1, 2, 3}, asyncNetworkConfig{
		MaxDelay: 5 * time.Millisecond,
		Seed:     14,
	})
	slotID := SlotID(32)

	cluster.bootstrapSlot(t, slotID, []NodeID{1, 2, 3})
	cluster.waitForBootstrapApplied(t, slotID, 3)

	oldLeader := cluster.waitForLeader(t, slotID)
	cluster.partitionNode(oldLeader)

	stale, err := cluster.runtime(oldLeader).ChangeConfig(context.Background(), slotID, ConfigChange{
		Type:   AddLearner,
		NodeID: 4,
	})
	if err != nil {
		t.Fatalf("ChangeConfig(stale) error = %v", err)
	}

	newLeader := cluster.waitForLeaderAmong(t, slotID, cluster.otherNodes(oldLeader))
	fresh, err := cluster.runtime(newLeader).ChangeConfig(context.Background(), slotID, ConfigChange{
		Type:   AddLearner,
		NodeID: 5,
	})
	if err != nil {
		t.Fatalf("ChangeConfig(fresh) error = %v", err)
	}

	freshRes, err := fresh.Wait(context.Background())
	if err != nil {
		t.Fatalf("fresh Wait() error = %v", err)
	}

	cluster.healNode(oldLeader)
	cluster.waitForNodeCommitIndex(t, oldLeader, slotID, freshRes.Index)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	res, err := stale.Wait(ctx)
	if err == nil {
		t.Fatalf("stale config future resolved unexpectedly: result=%+v err=%v", res, err)
	}
	if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, ErrNotLeader) {
		t.Fatalf("stale config future error = %v", err)
	}
}

func TestChangeConfigWaitBlocksUntilReadyBatchFullyCompletes(t *testing.T) {
	rt := newStartedRuntime(t)
	slotID := SlotID(33)
	store := newBlockingMarkAppliedStorage()

	err := rt.BootstrapSlot(context.Background(), BootstrapSlotRequest{
		Slot: SlotOptions{
			ID:           slotID,
			Storage:      store,
			StateMachine: &internalFakeStateMachine{},
		},
		Voters: []NodeID{1},
	})
	if err != nil {
		t.Fatalf("BootstrapSlot() error = %v", err)
	}

	waitForCondition(t, func() bool {
		st, err := rt.Status(slotID)
		return err == nil && st.Role == RoleLeader
	})
	store.internalFakeStorage.mu.Lock()
	baselineApplied := store.internalFakeStorage.lastApplied
	store.internalFakeStorage.mu.Unlock()
	store.armAfter(baselineApplied + 1)

	fut, err := rt.ChangeConfig(context.Background(), slotID, ConfigChange{
		Type:   AddLearner,
		NodeID: 2,
	})
	if err != nil {
		t.Fatalf("ChangeConfig() error = %v", err)
	}

	select {
	case <-store.started:
	case <-time.After(time.Second):
		t.Fatal("MarkApplied() did not start")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := fut.Wait(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Wait() error = %v, want %v", err, context.DeadlineExceeded)
	}

	store.unblock()

	if _, err := fut.Wait(context.Background()); err != nil {
		t.Fatalf("Wait() after unblock error = %v", err)
	}
}

func mustChangeConfig(t *testing.T, rt *Runtime, slotID SlotID, nodeID NodeID) Future {
	t.Helper()

	fut, err := rt.ChangeConfig(context.Background(), slotID, ConfigChange{
		Type:   AddLearner,
		NodeID: nodeID,
	})
	if err != nil {
		t.Fatalf("ChangeConfig(node=%d) error = %v", nodeID, err)
	}
	return fut
}
