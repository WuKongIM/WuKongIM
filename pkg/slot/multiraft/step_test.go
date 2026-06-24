package multiraft

import (
	"context"
	"encoding/binary"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	raft "go.etcd.io/raft/v3"
	"go.etcd.io/raft/v3/raftpb"
)

func TestStepRoutesMessageToCorrectSlot(t *testing.T) {
	rt := newStartedRuntime(t)
	if err := rt.OpenSlot(context.Background(), newInternalSlotOptions(100)); err != nil {
		t.Fatalf("OpenSlot() error = %v", err)
	}
	if err := rt.Step(context.Background(), Envelope{
		SlotID:  100,
		Message: raftpb.Message{Type: raftpb.MsgHeartbeat, From: 2, To: 1},
	}); err != nil {
		t.Fatalf("Step() error = %v", err)
	}

	waitForCondition(t, func() bool { return slotRequestCount(rt, 100) == 1 })
}

func TestStepUnknownSlotReturnsErrSlotNotFound(t *testing.T) {
	rt := newStartedRuntime(t)
	err := rt.Step(context.Background(), Envelope{SlotID: 404})
	if !errors.Is(err, ErrSlotNotFound) {
		t.Fatalf("expected ErrSlotNotFound, got %v", err)
	}
}

func TestRuntimeTickLoopEnqueuesOpenSlots(t *testing.T) {
	rt := newStartedRuntime(t)
	if err := rt.OpenSlot(context.Background(), newInternalSlotOptions(101)); err != nil {
		t.Fatalf("OpenSlot() error = %v", err)
	}

	waitForCondition(t, func() bool { return slotTickCount(rt, 101) > 0 })
}

func TestStatusIsRaceFree(t *testing.T) {
	rt := newStartedRuntime(t)
	slotID := openSingleNodeLeader(t, rt, 102)

	done := make(chan struct{})
	defer close(done)

	go func() {
		for {
			select {
			case <-done:
				return
			default:
				_, _ = rt.Status(slotID)
			}
		}
	}()

	for i := 0; i < 5; i++ {
		fut, err := rt.Propose(context.Background(), slotID, proposalString("status"))
		if err != nil {
			t.Fatalf("Propose() error = %v", err)
		}
		if _, err := fut.Wait(context.Background()); err != nil {
			t.Fatalf("Wait() error = %v", err)
		}
	}
}

func TestRuntimeStatusIncludesCurrentVoters(t *testing.T) {
	rt := newStartedRuntime(t)
	slotID := openSingleNodeLeader(t, rt, 106)

	st, err := rt.Status(slotID)
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if got, want := st.CurrentVoters, []NodeID{1}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Status().CurrentVoters = %v, want %v", got, want)
	}
}

func TestRuntimeStatusCurrentVotersSnapshotIsImmutable(t *testing.T) {
	rt := newStartedRuntime(t)
	slotID := openSingleNodeLeader(t, rt, 107)

	st, err := rt.Status(slotID)
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	st.CurrentVoters[0] = 99

	next, err := rt.Status(slotID)
	if err != nil {
		t.Fatalf("Status() second error = %v", err)
	}
	if got, want := next.CurrentVoters, []NodeID{1}; !reflect.DeepEqual(got, want) {
		t.Fatalf("second Status().CurrentVoters = %v, want %v", got, want)
	}
}

func TestRuntimeRefreshesBasicStatusAfterTick(t *testing.T) {
	slotID := SlotID(108)
	g, err := newSlot(context.Background(), 1, nil, RaftOptions{ElectionTick: 10, HeartbeatTick: 1}, newInternalSlotOptions(slotID), nil, nil)
	if err != nil {
		t.Fatalf("newSlot() error = %v", err)
	}
	if err := g.rawNode.Bootstrap([]raft.Peer{{ID: 1}}); err != nil {
		t.Fatalf("Bootstrap() error = %v", err)
	}
	rt := &Runtime{
		opts:  Options{Transport: &internalFakeTransport{}},
		slots: map[SlotID]*slot{slotID: g},
	}
	for i := 0; i < 8; i++ {
		if !rt.processSlot(slotID) {
			break
		}
	}
	beforeBasic, beforeFull := slotStatusRefreshCounts(g)

	g.markTickPending()
	rt.processSlot(slotID)

	afterBasic, afterFull := slotStatusRefreshCounts(g)
	if got := afterBasic - beforeBasic; got != 1 {
		t.Fatalf("basic status refresh delta = %d, want 1 (before=%d after=%d)", got, beforeBasic, afterBasic)
	}
	if afterFull != beforeFull {
		t.Fatalf("full status refresh count = %d, want %d", afterFull, beforeFull)
	}
}

func TestRuntimeRefreshesVotersAfterConfigChange(t *testing.T) {
	rt := newStartedRuntime(t)
	slotID := openSingleNodeLeader(t, rt, 109)

	fut, err := rt.ChangeConfig(context.Background(), slotID, ConfigChange{Type: AddVoter, NodeID: 2})
	if err != nil {
		t.Fatalf("ChangeConfig() error = %v", err)
	}
	if _, err := fut.Wait(context.Background()); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}

	st, err := rt.Status(slotID)
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if got, want := st.CurrentVoters, []NodeID{1, 2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Status().CurrentVoters = %v, want %v", got, want)
	}
}

func TestRuntimeStatusIncludesLearnersConfStateAndProgress(t *testing.T) {
	rt := newStartedRuntime(t)
	slotID := openSingleNodeLeader(t, rt, 112)

	fut, err := rt.ChangeConfig(context.Background(), slotID, ConfigChange{Type: AddLearner, NodeID: 2})
	if err != nil {
		t.Fatalf("ChangeConfig(AddLearner) error = %v", err)
	}
	if _, err := fut.Wait(context.Background()); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}

	st, err := rt.Status(slotID)
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if got, want := st.CurrentLearners, []NodeID{2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Status().CurrentLearners = %v, want %v", got, want)
	}
	if got, want := st.ConfState.Learners, []uint64{2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Status().ConfState.Learners = %v, want %v", got, want)
	}
	if _, ok := st.Progress[2]; !ok {
		t.Fatalf("Status().Progress missing learner 2: %#v", st.Progress)
	}
	if st.ConfigAppliedIndex == 0 {
		t.Fatal("Status().ConfigAppliedIndex = 0, want non-zero config entry index")
	}
}

func TestRuntimeStatusRefreshesLearnerProgress(t *testing.T) {
	cluster := newAsyncTestCluster(t, []NodeID{1, 2, 3}, asyncNetworkConfig{
		MaxDelay: time.Millisecond,
		Seed:     21,
	})
	slotID := SlotID(196)
	voters := []NodeID{1, 2}
	cluster.bootstrapSlot(t, slotID, voters)
	leaderID := cluster.waitForLeaderAmong(t, slotID, voters)

	learnerStore := &internalFakeStorage{}
	learnerFSM := &internalFakeStateMachine{}
	cluster.stores[3][slotID] = learnerStore
	cluster.fsms[3][slotID] = learnerFSM
	if err := cluster.runtime(3).OpenSlot(context.Background(), SlotOptions{
		ID:           slotID,
		Storage:      learnerStore,
		StateMachine: learnerFSM,
	}); err != nil {
		t.Fatalf("OpenSlot(learner) error = %v", err)
	}

	cluster.partitionNode(3)
	change, err := cluster.runtime(leaderID).ChangeConfig(context.Background(), slotID, ConfigChange{
		Type:   AddLearner,
		NodeID: 3,
	})
	if err != nil {
		t.Fatalf("ChangeConfig(AddLearner) error = %v", err)
	}
	waitForFutureResult(t, change)
	if st, err := cluster.runtime(leaderID).Status(slotID); err != nil {
		t.Fatalf("Status(leader) error = %v", err)
	} else if _, ok := st.Progress[3]; !ok {
		t.Fatalf("Status().Progress missing learner 3 after config change: %#v", st.Progress)
	}

	cluster.healNode(3)
	leaderID = cluster.waitForLeaderAmong(t, slotID, voters)
	proposal, err := cluster.runtime(leaderID).Propose(context.Background(), slotID, proposalString("refresh-learner-progress"))
	if err != nil {
		t.Fatalf("Propose() error = %v", err)
	}
	proposalResult := waitForFutureResult(t, proposal)
	cluster.waitForCondition(t, func() bool {
		st, err := cluster.runtime(3).Status(slotID)
		return err == nil && st.AppliedIndex >= proposalResult.Index
	})

	cluster.waitForCondition(t, func() bool {
		st, err := cluster.runtime(leaderID).Status(slotID)
		if err != nil {
			return false
		}
		progress, ok := st.Progress[3]
		return ok && progress.Match >= proposalResult.Index
	})
}

func TestOpenSlotRestoresConfigAppliedIndex(t *testing.T) {
	rt := newStartedRuntime(t)
	slotID := SlotID(113)
	store := &internalFakeStorage{}
	if err := rt.BootstrapSlot(context.Background(), BootstrapSlotRequest{
		Slot: SlotOptions{
			ID:           slotID,
			Storage:      store,
			StateMachine: &internalFakeStateMachine{},
		},
		Voters: []NodeID{1},
	}); err != nil {
		t.Fatalf("BootstrapSlot() error = %v", err)
	}
	waitForCondition(t, func() bool {
		st, err := rt.Status(slotID)
		return err == nil && st.Role == RoleLeader
	})

	change, err := rt.ChangeConfig(context.Background(), slotID, ConfigChange{Type: AddLearner, NodeID: 2})
	if err != nil {
		t.Fatalf("ChangeConfig(AddLearner) error = %v", err)
	}
	changeResult := waitForFutureResult(t, change)
	if err := rt.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	reopened := newStartedRuntime(t)
	if err := reopened.OpenSlot(context.Background(), SlotOptions{
		ID:           slotID,
		Storage:      store,
		StateMachine: &internalFakeStateMachine{},
	}); err != nil {
		t.Fatalf("OpenSlot(reopened) error = %v", err)
	}
	st, err := reopened.Status(slotID)
	if err != nil {
		t.Fatalf("Status(reopened) error = %v", err)
	}
	if st.ConfigAppliedIndex != changeResult.Index {
		t.Fatalf("Status().ConfigAppliedIndex = %d, want config entry index %d", st.ConfigAppliedIndex, changeResult.Index)
	}
}

func TestOpenSlotDoesNotRestoreUnappliedConfigAppliedIndex(t *testing.T) {
	store := &internalFakeStorage{
		entries: []raftpb.Entry{
			{Index: 1, Term: 1, Type: raftpb.EntryConfChange},
		},
	}
	rt := newStartedRuntime(t)
	if err := rt.OpenSlot(context.Background(), SlotOptions{
		ID:           116,
		Storage:      store,
		StateMachine: &internalFakeStateMachine{},
	}); err != nil {
		t.Fatalf("OpenSlot() error = %v", err)
	}
	st, err := rt.Status(116)
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if st.ConfigAppliedIndex != 0 {
		t.Fatalf("Status().ConfigAppliedIndex = %d, want 0 for unapplied config entry", st.ConfigAppliedIndex)
	}
}

func TestConfigAppliedIndexDoesNotAdvanceOnNormalEntry(t *testing.T) {
	rt := newStartedRuntime(t)
	slotID := SlotID(114)
	store := &internalFakeStorage{}
	if err := rt.BootstrapSlot(context.Background(), BootstrapSlotRequest{
		Slot: SlotOptions{
			ID:           slotID,
			Storage:      store,
			StateMachine: &internalFakeStateMachine{},
		},
		Voters: []NodeID{1},
	}); err != nil {
		t.Fatalf("BootstrapSlot() error = %v", err)
	}
	waitForCondition(t, func() bool {
		st, err := rt.Status(slotID)
		return err == nil && st.Role == RoleLeader
	})

	change, err := rt.ChangeConfig(context.Background(), slotID, ConfigChange{Type: AddLearner, NodeID: 2})
	if err != nil {
		t.Fatalf("ChangeConfig(AddLearner) error = %v", err)
	}
	changeResult := waitForFutureResult(t, change)
	proposal, err := rt.Propose(context.Background(), slotID, proposalString("normal-after-config"))
	if err != nil {
		t.Fatalf("Propose() error = %v", err)
	}
	proposalResult := waitForFutureResult(t, proposal)
	if proposalResult.Index <= changeResult.Index {
		t.Fatalf("normal proposal index = %d, want > config index %d", proposalResult.Index, changeResult.Index)
	}
	if err := rt.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	reopened := newStartedRuntime(t)
	if err := reopened.OpenSlot(context.Background(), SlotOptions{
		ID:           slotID,
		Storage:      store,
		StateMachine: &internalFakeStateMachine{},
	}); err != nil {
		t.Fatalf("OpenSlot(reopened) error = %v", err)
	}
	st, err := reopened.Status(slotID)
	if err != nil {
		t.Fatalf("Status(reopened) error = %v", err)
	}
	if st.ConfigAppliedIndex != changeResult.Index {
		t.Fatalf("Status().ConfigAppliedIndex = %d, want config entry index %d", st.ConfigAppliedIndex, changeResult.Index)
	}
	if st.ConfigAppliedIndex == proposalResult.Index {
		t.Fatalf("Status().ConfigAppliedIndex advanced to normal entry index %d", proposalResult.Index)
	}
}

func TestOpenSlotRestoresDurableConfigAppliedIndexAfterSnapshotCompaction(t *testing.T) {
	rt := newCompactionRuntime(t, LogCompactionConfig{
		Enabled:        true,
		EnabledSet:     true,
		TriggerEntries: 1000,
		CheckInterval:  time.Hour,
	})
	slotID := SlotID(118)
	store := &internalFakeStorage{}
	fsm := &snapshottingStateMachine{}
	if err := rt.BootstrapSlot(context.Background(), BootstrapSlotRequest{
		Slot: SlotOptions{
			ID:           slotID,
			Storage:      store,
			StateMachine: fsm,
		},
		Voters: []NodeID{1},
	}); err != nil {
		t.Fatalf("BootstrapSlot() error = %v", err)
	}
	waitForSingleNodeLeader(t, rt, slotID)

	change, err := rt.ChangeConfig(context.Background(), slotID, ConfigChange{Type: AddLearner, NodeID: 2})
	if err != nil {
		t.Fatalf("ChangeConfig(AddLearner) error = %v", err)
	}
	changeResult := waitForFutureResult(t, change)
	var normalResult Result
	for i := 0; i < 3; i++ {
		proposal, err := rt.Propose(context.Background(), slotID, proposalString("normal-after-snapshot-config"))
		if err != nil {
			t.Fatalf("Propose(%d) error = %v", i, err)
		}
		normalResult = waitForFutureResult(t, proposal)
	}
	if normalResult.Index <= changeResult.Index {
		t.Fatalf("normal proposal index = %d, want > config index %d", normalResult.Index, changeResult.Index)
	}

	result, err := rt.CompactLog(context.Background(), slotID)
	if err != nil {
		t.Fatalf("CompactLog() error = %v", err)
	}
	if !result.Compacted {
		t.Fatalf("CompactLog().Compacted = false, skipped=%q", result.SkippedReason)
	}
	if result.AfterSnapshotIndex <= changeResult.Index {
		t.Fatalf("snapshot index = %d, want > config entry index %d", result.AfterSnapshotIndex, changeResult.Index)
	}
	if err := rt.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	reopened := newStartedRuntime(t)
	if err := reopened.OpenSlot(context.Background(), SlotOptions{
		ID:           slotID,
		Storage:      store,
		StateMachine: &snapshottingStateMachine{},
	}); err != nil {
		t.Fatalf("OpenSlot(reopened) error = %v", err)
	}
	st, err := reopened.Status(slotID)
	if err != nil {
		t.Fatalf("Status(reopened) error = %v", err)
	}
	if st.ConfigAppliedIndex != changeResult.Index {
		t.Fatalf("Status().ConfigAppliedIndex = %d, want config entry index %d", st.ConfigAppliedIndex, changeResult.Index)
	}
}

func TestOpenSlotRestoresConfigAppliedIndexWithoutExternalPointScans(t *testing.T) {
	store := &countingEntriesStorage{
		internalFakeStorage: &internalFakeStorage{
			state: BootstrapState{
				HardState:    raftpb.HardState{Commit: 4},
				AppliedIndex: 4,
				ConfState:    raftpb.ConfState{Voters: []uint64{1}, Learners: []uint64{2}},
			},
			entries: []raftpb.Entry{
				{Index: 1, Term: 1, Type: raftpb.EntryNormal},
				{Index: 2, Term: 1, Type: raftpb.EntryConfChange},
				{Index: 3, Term: 1, Type: raftpb.EntryNormal},
				{Index: 4, Term: 1, Type: raftpb.EntryNormal},
			},
		},
	}
	rt := newStartedRuntime(t)
	if err := rt.OpenSlot(context.Background(), SlotOptions{
		ID:           117,
		Storage:      store,
		StateMachine: &internalFakeStateMachine{},
	}); err != nil {
		t.Fatalf("OpenSlot() error = %v", err)
	}
	st, err := rt.Status(117)
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if st.ConfigAppliedIndex != 2 {
		t.Fatalf("Status().ConfigAppliedIndex = %d, want config entry index 2", st.ConfigAppliedIndex)
	}
	if got := store.entriesCallCount(); got != 1 {
		t.Fatalf("Entries() calls = %d, want only the load-time range read", got)
	}
	if got, want := store.entriesRanges(), []entryRange{{lo: 1, hi: 5}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Entries() ranges = %#v, want %#v", got, want)
	}
}

func TestOpenSlotPrefersRetainedConfigEntryOverStaleDurableConfigAppliedIndex(t *testing.T) {
	store := &internalFakeStorage{
		state: BootstrapState{
			HardState:          raftpb.HardState{Commit: 5},
			AppliedIndex:       5,
			ConfigAppliedIndex: 2,
			ConfState:          raftpb.ConfState{Voters: []uint64{1}, Learners: []uint64{2, 3}},
		},
		entries: []raftpb.Entry{
			{Index: 1, Term: 1, Type: raftpb.EntryNormal},
			{Index: 2, Term: 1, Type: raftpb.EntryConfChange},
			{Index: 3, Term: 1, Type: raftpb.EntryNormal},
			{Index: 4, Term: 1, Type: raftpb.EntryConfChangeV2},
			{Index: 5, Term: 1, Type: raftpb.EntryNormal},
		},
	}
	rt := newStartedRuntime(t)
	if err := rt.OpenSlot(context.Background(), SlotOptions{
		ID:           119,
		Storage:      store,
		StateMachine: &internalFakeStateMachine{},
	}); err != nil {
		t.Fatalf("OpenSlot() error = %v", err)
	}
	st, err := rt.Status(119)
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if st.ConfigAppliedIndex != 4 {
		t.Fatalf("Status().ConfigAppliedIndex = %d, want retained config entry index 4", st.ConfigAppliedIndex)
	}
}

func TestOpenSlotDoesNotUseSnapshotIndexAsConfigAppliedIndex(t *testing.T) {
	store := &internalFakeStorage{
		state: BootstrapState{
			HardState:    raftpb.HardState{Commit: 9},
			AppliedIndex: 9,
		},
		snapshot: raftpb.Snapshot{
			Metadata: raftpb.SnapshotMetadata{
				Index: 9,
				Term:  2,
				ConfState: raftpb.ConfState{
					Voters:   []uint64{1},
					Learners: []uint64{2},
				},
			},
		},
	}
	rt := newStartedRuntime(t)
	if err := rt.OpenSlot(context.Background(), SlotOptions{
		ID:           115,
		Storage:      store,
		StateMachine: &internalFakeStateMachine{},
	}); err != nil {
		t.Fatalf("OpenSlot() error = %v", err)
	}
	st, err := rt.Status(115)
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if st.ConfigAppliedIndex != 0 {
		t.Fatalf("Status().ConfigAppliedIndex = %d, want 0 without durable config entry index", st.ConfigAppliedIndex)
	}
}

func TestRuntimeDoesNotDoubleRefreshAfterReady(t *testing.T) {
	g, err := newSlot(context.Background(), 1, nil, RaftOptions{ElectionTick: 10, HeartbeatTick: 1}, newInternalSlotOptions(110), nil, nil)
	if err != nil {
		t.Fatalf("newSlot() error = %v", err)
	}
	if err := g.rawNode.Bootstrap([]raft.Peer{{ID: 1}}); err != nil {
		t.Fatalf("Bootstrap() error = %v", err)
	}
	rt := &Runtime{
		opts:  Options{Transport: &internalFakeTransport{}},
		slots: map[SlotID]*slot{110: g},
	}
	beforeBasic, beforeFull := slotStatusRefreshCounts(g)
	before := beforeBasic + beforeFull

	rt.processSlot(110)

	afterBasic, afterFull := slotStatusRefreshCounts(g)
	after := afterBasic + afterFull
	if got := after - before; got != 1 {
		t.Fatalf("status refreshes after ready = %d, want 1", got)
	}
}

func TestRuntimeProcessesHeartbeatTickBeforeProposalControls(t *testing.T) {
	slotID := SlotID(111)
	store := newBlockingSaveStorage()
	transport := &recordingTransport{}
	g, err := newSlot(context.Background(), 1, nil, RaftOptions{ElectionTick: 10, HeartbeatTick: 1}, SlotOptions{
		ID:           slotID,
		Storage:      store,
		StateMachine: &internalFakeStateMachine{},
	}, nil, nil)
	if err != nil {
		t.Fatalf("newSlot() error = %v", err)
	}
	rt := &Runtime{
		opts:  Options{Transport: transport},
		slots: map[SlotID]*slot{slotID: g},
	}
	if err := g.rawNode.Bootstrap([]raft.Peer{{ID: 1}, {ID: 2}, {ID: 3}}); err != nil {
		t.Fatalf("Bootstrap() error = %v", err)
	}
	for i := 0; i < 8; i++ {
		if !rt.processSlot(slotID) {
			break
		}
	}
	if err := g.rawNode.Campaign(); err != nil {
		t.Fatalf("Campaign() error = %v", err)
	}
	term := g.rawNode.BasicStatus().Term
	if err := g.rawNode.Step(raftpb.Message{Type: raftpb.MsgVoteResp, From: 2, To: 1, Term: term}); err != nil {
		t.Fatalf("Step(vote from 2) error = %v", err)
	}
	if err := g.rawNode.Step(raftpb.Message{Type: raftpb.MsgVoteResp, From: 3, To: 1, Term: term}); err != nil {
		t.Fatalf("Step(vote from 3) error = %v", err)
	}
	for i := 0; i < 8; i++ {
		if !rt.processSlot(slotID) {
			break
		}
	}
	st, err := rt.Status(slotID)
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if st.Role != RoleLeader {
		t.Fatalf("role = %v, want leader", st.Role)
	}

	transport.clear()
	store.armEntrySave()
	fut := newFuture(nil)
	if err := g.enqueueControl(controlAction{
		kind:          controlPropose,
		data:          proposalString("blocked"),
		proposalClass: ProposalClassForeground,
		future:        fut,
	}); err != nil {
		t.Fatalf("enqueueControl() error = %v", err)
	}
	g.markTickPending()

	done := make(chan struct{})
	go func() {
		defer close(done)
		rt.processSlot(slotID)
	}()

	select {
	case <-store.started:
	case <-time.After(time.Second):
		t.Fatal("proposal Save() did not start")
	}
	if got := transport.countMessagesOfType(raftpb.MsgHeartbeat); got == 0 {
		store.release()
		<-done
		t.Fatalf("heartbeat messages before proposal Save = %d, want > 0", got)
	}
	store.release()
	<-done
}

func slotStatusRefreshCounts(g *slot) (basic int, full int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.basicStatusRefreshCount, g.fullStatusRefreshCount
}

func TestCloseSlotStopsFurtherProcessing(t *testing.T) {
	rt := newStartedRuntime(t)
	slotID := SlotID(103)
	fsm := newBlockingStateMachine()
	t.Cleanup(func() {
		fsm.unblock()
	})

	err := rt.BootstrapSlot(context.Background(), BootstrapSlotRequest{
		Slot: SlotOptions{
			ID:           slotID,
			Storage:      &internalFakeStorage{},
			StateMachine: fsm,
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

	fut, err := rt.Propose(context.Background(), slotID, proposalString("slow"))
	if err != nil {
		t.Fatalf("Propose() error = %v", err)
	}

	select {
	case <-fsm.started:
	case <-time.After(time.Second):
		t.Fatal("Apply() did not start")
	}
	g := slotFor(rt, slotID)
	if g == nil {
		t.Fatal("slotFor() = nil")
	}

	closeDone := make(chan error, 1)
	go func() {
		closeDone <- rt.CloseSlot(context.Background(), slotID)
	}()
	waitForCondition(t, func() bool {
		g.mu.Lock()
		defer g.mu.Unlock()
		return g.closed && g.applying > 0
	})

	select {
	case err := <-closeDone:
		t.Fatalf("CloseSlot() returned before in-flight apply finished: %v", err)
	default:
	}

	fsm.unblock()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := fut.Wait(ctx); !errors.Is(err, ErrSlotClosed) {
		t.Fatalf("Wait() error = %v, want %v", err, ErrSlotClosed)
	}

	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("CloseSlot() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("CloseSlot() did not return after apply completed")
	}
}

func TestCloseSlotBlocksNewAdmissions(t *testing.T) {
	rt := newStartedRuntime(t)
	if err := rt.OpenSlot(context.Background(), newInternalSlotOptions(104)); err != nil {
		t.Fatalf("OpenSlot() error = %v", err)
	}

	g := slotFor(rt, 104)
	if g == nil {
		t.Fatal("slotFor() = nil")
	}
	if err := rt.CloseSlot(context.Background(), 104); err != nil {
		t.Fatalf("CloseSlot() error = %v", err)
	}

	if err := g.enqueueRequest(raftpb.Message{Type: raftpb.MsgHeartbeat}); !errors.Is(err, ErrSlotClosed) {
		t.Fatalf("enqueueRequest() error = %v, want %v", err, ErrSlotClosed)
	}
	if err := g.enqueueControl(controlAction{kind: controlTransferLeader, target: 2}); !errors.Is(err, ErrSlotClosed) {
		t.Fatalf("enqueueControl() error = %v, want %v", err, ErrSlotClosed)
	}
}

func TestRuntimeTickLoopDoesNotHoldLockAcrossEnqueue(t *testing.T) {
	rt := newStartedRuntimeWithTick(t, time.Millisecond)
	blockedSlotID := SlotID(105)
	store := newBlockingMarkAppliedStorage()
	t.Cleanup(func() {
		store.unblock()
	})

	if err := rt.BootstrapSlot(context.Background(), BootstrapSlotRequest{
		Slot: SlotOptions{
			ID:           blockedSlotID,
			Storage:      store,
			StateMachine: &internalFakeStateMachine{},
		},
		Voters: []NodeID{1},
	}); err != nil {
		t.Fatalf("BootstrapSlot() error = %v", err)
	}

	waitForCondition(t, func() bool {
		st, err := rt.Status(blockedSlotID)
		return err == nil && st.Role == RoleLeader
	})

	store.internalFakeStorage.mu.Lock()
	baselineApplied := store.internalFakeStorage.lastApplied
	store.internalFakeStorage.mu.Unlock()
	store.armAfter(baselineApplied + 1)

	fut, err := rt.ChangeConfig(context.Background(), blockedSlotID, ConfigChange{Type: AddVoter, NodeID: 2})
	if err != nil {
		t.Fatalf("ChangeConfig() error = %v", err)
	}

	select {
	case <-store.started:
	case <-time.After(time.Second):
		t.Fatal("MarkApplied() did not start")
	}

	for i := 0; i < cap(rt.scheduler.ch)+1; i++ {
		if err := rt.OpenSlot(context.Background(), newInternalSlotOptions(SlotID(2000+i))); err != nil {
			t.Fatalf("OpenSlot(%d) error = %v", 2000+i, err)
		}
	}

	waitForCondition(t, func() bool { return len(rt.scheduler.ch) == cap(rt.scheduler.ch) })

	openDone := make(chan error, 1)
	go func() {
		openDone <- rt.OpenSlot(context.Background(), newInternalSlotOptions(5000))
	}()

	select {
	case err := <-openDone:
		if err != nil {
			t.Fatalf("OpenSlot() error = %v", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("OpenSlot() blocked behind scheduler enqueue while ticker was running")
	}

	store.unblock()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := fut.Wait(ctx); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
}

func TestSchedulerBackpressureDoesNotBlockRuntime(t *testing.T) {
	rt := newStartedRuntimeWithTick(t, time.Millisecond)
	blockedSlotID := SlotID(106)
	store := newBlockingMarkAppliedStorage()
	t.Cleanup(func() {
		store.unblock()
	})

	if err := rt.BootstrapSlot(context.Background(), BootstrapSlotRequest{
		Slot: SlotOptions{
			ID:           blockedSlotID,
			Storage:      store,
			StateMachine: &internalFakeStateMachine{},
		},
		Voters: []NodeID{1},
	}); err != nil {
		t.Fatalf("BootstrapSlot() error = %v", err)
	}

	waitForCondition(t, func() bool {
		st, err := rt.Status(blockedSlotID)
		return err == nil && st.Role == RoleLeader
	})

	store.internalFakeStorage.mu.Lock()
	baselineApplied := store.internalFakeStorage.lastApplied
	store.internalFakeStorage.mu.Unlock()
	store.armAfter(baselineApplied + 1)

	fut, err := rt.ChangeConfig(context.Background(), blockedSlotID, ConfigChange{Type: AddVoter, NodeID: 2})
	if err != nil {
		t.Fatalf("ChangeConfig() error = %v", err)
	}

	select {
	case <-store.started:
	case <-time.After(time.Second):
		t.Fatal("MarkApplied() did not start")
	}

	targetSlotID := SlotID(3000)
	for i := 0; i < cap(rt.scheduler.ch)+1; i++ {
		id := SlotID(3000 + i)
		if err := rt.OpenSlot(context.Background(), newInternalSlotOptions(id)); err != nil {
			t.Fatalf("OpenSlot(%d) error = %v", id, err)
		}
	}

	waitForCondition(t, func() bool { return len(rt.scheduler.ch) == cap(rt.scheduler.ch) })

	closeDone := make(chan error, 1)
	go func() {
		closeDone <- rt.CloseSlot(context.Background(), targetSlotID)
	}()

	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("CloseSlot() error = %v", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("CloseSlot() blocked behind scheduler enqueue while ticker was running")
	}

	store.unblock()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := fut.Wait(ctx); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
}

func TestWrapMessagesIntoReusesDestinationSlice(t *testing.T) {
	backing := make([]Envelope, 4)
	dst := backing[:0]
	msgs := []raftpb.Message{{
		Type: raftpb.MsgHeartbeat,
		From: 1,
		To:   2,
	}}

	batch := wrapMessagesInto(dst, 42, msgs)

	if len(batch) != 1 {
		t.Fatalf("len(batch) = %d, want 1", len(batch))
	}
	if cap(batch) != cap(dst) {
		t.Fatalf("cap(batch) = %d, want %d", cap(batch), cap(dst))
	}
	if &batch[0] != &backing[0] {
		t.Fatal("expected destination backing slice to be reused")
	}
}

func TestWrapMessagesIntoClonesLargePayloadsByDefault(t *testing.T) {
	msgs := []raftpb.Message{{
		Type:    raftpb.MsgApp,
		From:    1,
		To:      2,
		Context: []byte("ctx"),
		Entries: []raftpb.Entry{{
			Index: 1,
			Term:  1,
			Data:  []byte("entry"),
		}},
		Snapshot: &raftpb.Snapshot{
			Data: []byte("snap"),
			Metadata: raftpb.SnapshotMetadata{
				Index: 1,
				Term:  1,
				ConfState: raftpb.ConfState{
					Voters: []uint64{1, 2, 3},
				},
			},
		},
	}}

	batch := wrapMessagesInto(nil, 42, msgs)

	msgs[0].Context[0] = 'x'
	msgs[0].Entries[0].Data[0] = 'X'
	msgs[0].Snapshot.Data[0] = 'Y'
	msgs[0].Snapshot.Metadata.ConfState.Voters[0] = 99

	got := batch[0].Message
	if string(got.Context) != "ctx" {
		t.Fatalf("Context = %q", got.Context)
	}
	if string(got.Entries[0].Data) != "entry" {
		t.Fatalf("Entries[0].Data = %q", got.Entries[0].Data)
	}
	if got.Snapshot == nil || string(got.Snapshot.Data) != "snap" {
		t.Fatalf("Snapshot.Data = %v", got.Snapshot)
	}
	if got.Snapshot.Metadata.ConfState.Voters[0] != 1 {
		t.Fatalf("Snapshot.Metadata.ConfState.Voters[0] = %d", got.Snapshot.Metadata.ConfState.Voters[0])
	}
}

func TestWrapMessagesIntoSharesLargePayloadsForOwningTransport(t *testing.T) {
	msgs := []raftpb.Message{{
		Type:    raftpb.MsgApp,
		From:    1,
		To:      2,
		Context: []byte("ctx"),
		Entries: []raftpb.Entry{{
			Index: 1,
			Term:  1,
			Data:  []byte("entry"),
		}},
		Snapshot: &raftpb.Snapshot{
			Data: []byte("snap"),
			Metadata: raftpb.SnapshotMetadata{
				Index: 1,
				Term:  1,
				ConfState: raftpb.ConfState{
					Voters: []uint64{1, 2, 3},
				},
			},
		},
	}}

	batch := wrapMessagesIntoForTransport(nil, 42, msgs, owningPayloadTransport{})

	msgs[0].Context[0] = 'x'
	msgs[0].Snapshot.Metadata.ConfState.Voters[0] = 99

	got := batch[0].Message
	if string(got.Context) != "ctx" {
		t.Fatalf("Context = %q", got.Context)
	}
	if len(got.Entries) != 1 || &got.Entries[0].Data[0] != &msgs[0].Entries[0].Data[0] {
		t.Fatalf("Entries[0].Data was cloned for owning transport")
	}
	if got.Snapshot == nil || &got.Snapshot.Data[0] != &msgs[0].Snapshot.Data[0] {
		t.Fatalf("Snapshot.Data was cloned for owning transport")
	}
	if got.Snapshot.Metadata.ConfState.Voters[0] != 1 {
		t.Fatalf("Snapshot.Metadata.ConfState.Voters[0] = %d", got.Snapshot.Metadata.ConfState.Voters[0])
	}
}

type owningPayloadTransport struct{}

func (owningPayloadTransport) Send(context.Context, []Envelope) error { return nil }

func (owningPayloadTransport) OwnsReadyMessagePayloads() bool { return true }

func TestRequestDrainHelperMovesQueuedMessagesIntoReusableWorkSlice(t *testing.T) {
	g := newTestSlotForDrain()
	if err := g.enqueueRequest(raftpb.Message{Type: raftpb.MsgHeartbeat, From: 2, To: 1}); err != nil {
		t.Fatalf("enqueueRequest() error = %v", err)
	}

	batch := g.takeRequestBatch()
	if len(batch) != 1 {
		t.Fatalf("len(batch) = %d, want 1", len(batch))
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.requests) != 0 {
		t.Fatalf("len(g.requests) = %d, want 0", len(g.requests))
	}
}

func TestControlDrainHelperMovesQueuedActionsIntoReusableWorkSlice(t *testing.T) {
	g := newLeaderTestSlotForDrain()
	if err := g.enqueueControl(controlAction{kind: controlTransferLeader, target: 2}); err != nil {
		t.Fatalf("enqueueControl() error = %v", err)
	}

	batch := g.takeControlBatch()
	if len(batch) != 1 {
		t.Fatalf("len(batch) = %d, want 1", len(batch))
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.controls) != 0 {
		t.Fatalf("len(g.controls) = %d, want 0", len(g.controls))
	}
}

func TestResolutionBufferHelpersReuseBackingSlice(t *testing.T) {
	g := newTestSlotForDrain()

	buf := g.takeResolutionBuffer()
	buf = append(buf, futureResolution{index: 1})
	g.releaseResolutionBuffer(buf)

	reused := g.takeResolutionBuffer()
	if len(reused) != 0 {
		t.Fatalf("len(reused) = %d, want 0", len(reused))
	}
	if cap(reused) == 0 {
		t.Fatal("expected reused capacity to be retained")
	}
}

func TestEnqueueRequestRejectsWhenQueueLimitReached(t *testing.T) {
	g := newTestSlotForDrain()
	g.maxQueuedRequests = 1

	if err := g.enqueueRequest(raftpb.Message{Type: raftpb.MsgHeartbeat, From: 2, To: 1}); err != nil {
		t.Fatalf("first enqueueRequest() error = %v", err)
	}
	err := g.enqueueRequest(raftpb.Message{Type: raftpb.MsgHeartbeat, From: 3, To: 1})
	if !errors.Is(err, ErrSlotBusy) {
		t.Fatalf("second enqueueRequest() error = %v, want %v", err, ErrSlotBusy)
	}
}

func TestEnqueueControlRejectsWhenQueueLimitReached(t *testing.T) {
	g := newLeaderTestSlotForDrain()
	g.maxQueuedControls = 1

	if err := g.enqueueControl(controlAction{kind: controlTransferLeader, target: 2}); err != nil {
		t.Fatalf("first enqueueControl() error = %v", err)
	}
	err := g.enqueueControl(controlAction{kind: controlTransferLeader, target: 3})
	if !errors.Is(err, ErrSlotBusy) {
		t.Fatalf("second enqueueControl() error = %v, want %v", err, ErrSlotBusy)
	}
}

func TestProposalClassContextDefaultsForeground(t *testing.T) {
	if got := ProposalClassFromContext(context.Background()); got != ProposalClassForeground {
		t.Fatalf("default proposal class = %q, want %q", got, ProposalClassForeground)
	}
	ctx := WithProposalClass(context.Background(), ProposalClassBackground)
	if got := ProposalClassFromContext(ctx); got != ProposalClassBackground {
		t.Fatalf("context proposal class = %q, want %q", got, ProposalClassBackground)
	}
}

func TestBackgroundProposalAdmissionLeavesForegroundReserve(t *testing.T) {
	g := newLeaderTestSlotForDrain()
	g.maxQueuedControls = 4
	g.maxQueuedBackgroundControls = 1

	if err := g.enqueueControl(controlAction{
		kind:          controlPropose,
		proposalClass: ProposalClassBackground,
		future:        newFuture(nil),
	}); err != nil {
		t.Fatalf("first background enqueueControl() error = %v", err)
	}
	if err := g.enqueueControl(controlAction{
		kind:          controlPropose,
		proposalClass: ProposalClassBackground,
		future:        newFuture(nil),
	}); !errors.Is(err, ErrBackgroundProposalThrottled) {
		t.Fatalf("second background enqueueControl() error = %v, want %v", err, ErrBackgroundProposalThrottled)
	}
	if err := g.enqueueControl(controlAction{
		kind:          controlPropose,
		proposalClass: ProposalClassForeground,
		future:        newFuture(nil),
	}); err != nil {
		t.Fatalf("foreground enqueueControl() after background throttle error = %v", err)
	}
}

func TestProposalAdmissionObserverRecordsBackgroundThrottle(t *testing.T) {
	observer := &recordingProposalAdmissionObserver{}
	g := newLeaderTestSlotForDrain()
	g.observer = observer
	g.maxQueuedControls = 4
	g.maxQueuedBackgroundControls = 1

	if err := g.enqueueControl(controlAction{
		kind:          controlPropose,
		proposalClass: ProposalClassBackground,
		future:        newFuture(nil),
	}); err != nil {
		t.Fatalf("first background enqueueControl() error = %v", err)
	}
	if err := g.enqueueControl(controlAction{
		kind:          controlPropose,
		proposalClass: ProposalClassBackground,
		future:        newFuture(nil),
	}); !errors.Is(err, ErrBackgroundProposalThrottled) {
		t.Fatalf("second background enqueueControl() error = %v, want %v", err, ErrBackgroundProposalThrottled)
	}
	if got := observer.results; !reflect.DeepEqual(got, []string{"background:ok", "background:throttled"}) {
		t.Fatalf("proposal admission results = %v, want background ok then throttled", got)
	}
}

func TestBeginApplyRejectsWhenApplyLimitReached(t *testing.T) {
	g := newTestSlotForDrain()
	g.maxApplyingTasks = 1

	if err := g.beginApply(); err != nil {
		t.Fatalf("first beginApply() error = %v", err)
	}
	if err := g.beginApply(); !errors.Is(err, ErrSlotBusy) {
		t.Fatalf("second beginApply() error = %v, want %v", err, ErrSlotBusy)
	}
	g.finishApply()
	if err := g.beginApply(); err != nil {
		t.Fatalf("beginApply() after finish error = %v", err)
	}
	g.finishApply()
}

func TestReusableBuffersClearRetainedReferences(t *testing.T) {
	g := newLeaderTestSlotForDrain()
	msg := raftpb.Message{
		Type:    raftpb.MsgApp,
		From:    2,
		To:      1,
		Context: []byte("ctx"),
		Entries: []raftpb.Entry{{Data: []byte("entry")}},
		Snapshot: &raftpb.Snapshot{
			Data: []byte("snapshot"),
		},
	}
	if err := g.enqueueRequest(msg); err != nil {
		t.Fatalf("enqueueRequest() error = %v", err)
	}
	requests := g.takeRequestBatch()
	g.releaseRequestBatch(requests)
	requestBacking := g.requestWorkBuf[:cap(g.requestWorkBuf)]
	if requestBacking[0].Context != nil || requestBacking[0].Entries != nil || requestBacking[0].Snapshot != nil {
		t.Fatalf("request work buffer retained payload references: %+v", requestBacking[0])
	}

	if err := g.enqueueControl(controlAction{
		kind:   controlPropose,
		data:   []byte("proposal"),
		future: newFuture(nil),
	}); err != nil {
		t.Fatalf("enqueueControl() error = %v", err)
	}
	controls := g.takeControlBatch()
	g.releaseControlBatch(controls)
	controlBacking := g.controlWorkBuf[:cap(g.controlWorkBuf)]
	if controlBacking[0].data != nil || controlBacking[0].future != nil {
		t.Fatalf("control work buffer retained references: %+v", controlBacking[0])
	}

	resolutions := g.takeResolutionBuffer()
	resolutions = append(resolutions, futureResolution{
		future: newFuture(nil),
		result: Result{Data: []byte("result")},
	})
	g.releaseResolutionBuffer(resolutions)
	resolutionBacking := g.resolutionBuf[:cap(g.resolutionBuf)]
	if resolutionBacking[0].future != nil || resolutionBacking[0].result.Data != nil {
		t.Fatalf("resolution buffer retained references: %+v", resolutionBacking[0])
	}
}

func TestTrackReadyEntriesClearsConsumedSubmittedFutureReference(t *testing.T) {
	first := newFuture(nil)
	second := newFuture(nil)
	submitted := []*future{first, second}
	g := &slot{submittedProposals: submitted}

	g.trackReadyEntries([]raftpb.Entry{{
		Index: 1,
		Term:  1,
		Type:  raftpb.EntryNormal,
		Data:  []byte("proposal"),
	}})

	if submitted[0] != nil {
		t.Fatal("consumed submitted proposal future was retained in backing slice")
	}
	if len(g.submittedProposals) != 1 || g.submittedProposals[0] != second {
		t.Fatalf("submittedProposals = %v, want only second future", g.submittedProposals)
	}
}

func TestApplyStateObserverCanReenterSlotWithoutDeadlock(t *testing.T) {
	g := newTestSlotForDrain()
	observer := &reentrantApplyStateObserver{slot: g, done: make(chan struct{})}
	g.observer = observer

	go g.refreshDurableAppliedStatus()

	select {
	case <-observer.done:
	case <-time.After(time.Second):
		t.Fatal("apply state observer reentry deadlocked")
	}
}

func TestLeaderChangeObserverIgnoresUnknownRecoveryToSameLeader(t *testing.T) {
	observer := &slotLeaderChangeObserver{}
	g := newTestSlotForDrain()
	g.id = 7
	g.observer = observer

	g.mu.Lock()
	firstKnown := g.setLeaderIDLocked(1)
	unknown := g.setLeaderIDLocked(0)
	sameKnown := g.setLeaderIDLocked(1)
	g.mu.Unlock()

	firstKnown.emit()
	unknown.emit()
	sameKnown.emit()

	observer.mu.Lock()
	defer observer.mu.Unlock()
	if len(observer.changes) != 0 {
		t.Fatalf("leader changes = %v, want no change for unknown recovery to same leader", observer.changes)
	}
}

func TestLeaderChangeObserverCountsUnknownGapToDifferentLeader(t *testing.T) {
	observer := &slotLeaderChangeObserver{}
	g := newTestSlotForDrain()
	g.id = 7
	g.observer = observer

	g.mu.Lock()
	firstKnown := g.setLeaderIDLocked(1)
	unknown := g.setLeaderIDLocked(0)
	differentKnown := g.setLeaderIDLocked(2)
	g.mu.Unlock()

	firstKnown.emit()
	unknown.emit()
	differentKnown.emit()

	observer.mu.Lock()
	defer observer.mu.Unlock()
	if len(observer.changes) != 1 {
		t.Fatalf("leader changes = %v, want one change after unknown gap to different leader", observer.changes)
	}
	if got := observer.changes[0]; got.from != 1 || got.to != 2 {
		t.Fatalf("leader change = %+v, want from=1 to=2", got)
	}
}

func TestLeaderChangeObserverMarksExpectedTransferAsPlanned(t *testing.T) {
	observer := &slotLeaderChangeObserver{}
	g := newTestSlotForDrain()
	g.id = 7
	g.observer = observer

	g.mu.Lock()
	firstKnown := g.setLeaderIDLocked(1)
	g.pendingLeaderTransferTarget = 2
	unknown := g.setLeaderIDLocked(0)
	planned := g.setLeaderIDLocked(2)
	g.mu.Unlock()

	firstKnown.emit()
	unknown.emit()
	planned.emit()

	observer.mu.Lock()
	defer observer.mu.Unlock()
	if len(observer.changes) != 1 {
		t.Fatalf("leader changes = %v, want one planned change", observer.changes)
	}
	if got := observer.changes[0]; got.from != 1 || got.to != 2 || got.cause != LeaderChangeCausePlannedTransfer {
		t.Fatalf("leader change = %+v, want planned transfer from=1 to=2", got)
	}
}

func newStartedRuntime(t *testing.T) *Runtime {
	return newStartedRuntimeWithTick(t, 10*time.Millisecond)
}

func newStartedRuntimeWithTick(t *testing.T, tickInterval time.Duration) *Runtime {
	return newStartedRuntimeWithOptions(t, tickInterval, nil)
}

func newStartedRuntimeWithObserver(t *testing.T, observer SchedulerObserver) *Runtime {
	return newStartedRuntimeWithOptions(t, 10*time.Millisecond, observer)
}

func newStartedRuntimeWithTickAndObserver(t *testing.T, tickInterval time.Duration, observer SchedulerObserver) *Runtime {
	return newStartedRuntimeWithOptions(t, tickInterval, observer)
}

func newStartedRuntimeWithOptions(t *testing.T, tickInterval time.Duration, observer SchedulerObserver) *Runtime {
	t.Helper()

	rt, err := New(Options{
		NodeID:       1,
		TickInterval: tickInterval,
		Workers:      1,
		Transport:    &internalFakeTransport{},
		Observer:     observer,
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
	return rt
}

func newInternalSlotOptions(id SlotID) SlotOptions {
	return SlotOptions{
		ID:           id,
		Storage:      &internalFakeStorage{},
		StateMachine: &internalFakeStateMachine{},
	}
}

func newTestSlotForDrain() *slot {
	g := &slot{}
	g.cond = sync.NewCond(&g.mu)
	return g
}

func newLeaderTestSlotForDrain() *slot {
	g := newTestSlotForDrain()
	g.status.Role = RoleLeader
	return g
}

type reentrantApplyStateObserver struct {
	slot *slot
	done chan struct{}
}

func (o *reentrantApplyStateObserver) SetSchedulerWorkers(int) {}

func (o *reentrantApplyStateObserver) SetSchedulerInflight(int) {}

func (o *reentrantApplyStateObserver) SetSchedulerState(SchedulerStateEvent) {}

func (o *reentrantApplyStateObserver) ObserveSchedulerAdmission(string) {}

func (o *reentrantApplyStateObserver) ObserveSchedulerTask(string, time.Duration) {}

func (o *reentrantApplyStateObserver) SetSlotApplyState(SlotID, uint64, uint64) {
	if o.slot != nil {
		_, _ = o.slot.statusSnapshot()
	}
	close(o.done)
}

type recordingProposalAdmissionObserver struct {
	results []string
}

func (o *recordingProposalAdmissionObserver) SetSchedulerWorkers(int) {}

func (o *recordingProposalAdmissionObserver) SetSchedulerInflight(int) {}

func (o *recordingProposalAdmissionObserver) SetSchedulerState(SchedulerStateEvent) {}

func (o *recordingProposalAdmissionObserver) ObserveSchedulerAdmission(string) {}

func (o *recordingProposalAdmissionObserver) ObserveSchedulerTask(string, time.Duration) {}

func (o *recordingProposalAdmissionObserver) ObserveSlotProposalAdmission(_ SlotID, class ProposalClass, result string) {
	o.results = append(o.results, string(class)+":"+result)
}

func proposalPayload(hashSlot uint16, data []byte) []byte {
	payload := make([]byte, proposalEnvelopeSize+len(data))
	binary.BigEndian.PutUint16(payload[:2], hashSlot)
	binary.BigEndian.PutUint64(payload[2:proposalEnvelopeSize], 1781754611123)
	copy(payload[proposalEnvelopeSize:], data)
	return payload
}

func proposalString(data string) []byte {
	return proposalPayload(0, []byte(data))
}

func waitForCondition(t *testing.T, fn func() bool) {
	t.Helper()

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not satisfied before timeout")
}

func slotRequestCount(rt *Runtime, id SlotID) int {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	g := rt.slots[id]
	if g == nil {
		return 0
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.requestCount
}

func slotTickCount(rt *Runtime, id SlotID) int {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	g := rt.slots[id]
	if g == nil {
		return 0
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.tickCount
}

type internalFakeTransport struct{}

func (f *internalFakeTransport) Send(ctx context.Context, batch []Envelope) error {
	return nil
}

type recordingTransport struct {
	mu   sync.Mutex
	sent []Envelope
}

func (t *recordingTransport) Send(ctx context.Context, batch []Envelope) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, env := range batch {
		t.sent = append(t.sent, Envelope{
			SlotID:  env.SlotID,
			Message: cloneMessage(env.Message, true),
		})
	}
	return nil
}

func (t *recordingTransport) clear() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.sent = nil
}

func (t *recordingTransport) countMessagesOfType(typ raftpb.MessageType) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	count := 0
	for _, env := range t.sent {
		if env.Message.Type == typ {
			count++
		}
	}
	return count
}

type blockingStateMachine struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func newBlockingStateMachine() *blockingStateMachine {
	return &blockingStateMachine{
		started: make(chan struct{}, 1),
		release: make(chan struct{}),
	}
}

func (f *blockingStateMachine) Apply(ctx context.Context, cmd Command) ([]byte, error) {
	select {
	case f.started <- struct{}{}:
	default:
	}
	<-f.release
	return append([]byte("ok:"), cmd.Data...), nil
}

func (f *blockingStateMachine) Restore(ctx context.Context, snap Snapshot) error {
	return nil
}

func (f *blockingStateMachine) Snapshot(ctx context.Context) (Snapshot, error) {
	return Snapshot{}, nil
}

func (f *blockingStateMachine) unblock() {
	f.once.Do(func() {
		close(f.release)
	})
}

type blockingSaveStorage struct {
	*internalFakeStorage
	started   chan struct{}
	releaseCh chan struct{}
	once      sync.Once
	mu        sync.Mutex
	armed     bool
}

func newBlockingSaveStorage() *blockingSaveStorage {
	return &blockingSaveStorage{
		internalFakeStorage: &internalFakeStorage{},
		started:             make(chan struct{}, 1),
		releaseCh:           make(chan struct{}),
	}
}

func (f *blockingSaveStorage) armEntrySave() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.armed = true
}

func (f *blockingSaveStorage) Save(ctx context.Context, st PersistentState) error {
	f.mu.Lock()
	armed := f.armed
	f.mu.Unlock()
	if !armed || len(st.Entries) == 0 {
		return f.internalFakeStorage.Save(ctx, st)
	}

	select {
	case f.started <- struct{}{}:
	default:
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-f.releaseCh:
	}
	return f.internalFakeStorage.Save(ctx, st)
}

func (f *blockingSaveStorage) release() {
	f.once.Do(func() {
		close(f.releaseCh)
	})
}

type internalFakeStorage struct {
	mu               sync.Mutex
	state            BootstrapState
	entries          []raftpb.Entry
	snapshot         raftpb.Snapshot
	saveCount        int
	saveErr          error
	lastSavedIndex   uint64
	lastApplied      uint64
	markAppliedCount int
	markAppliedErr   error
}

type entryRange struct {
	lo uint64
	hi uint64
}

type countingEntriesStorage struct {
	*internalFakeStorage
	entriesMu sync.Mutex
	ranges    []entryRange
}

func (s *countingEntriesStorage) Entries(ctx context.Context, lo, hi, maxSize uint64) ([]raftpb.Entry, error) {
	s.entriesMu.Lock()
	s.ranges = append(s.ranges, entryRange{lo: lo, hi: hi})
	s.entriesMu.Unlock()
	return s.internalFakeStorage.Entries(ctx, lo, hi, maxSize)
}

func (s *countingEntriesStorage) entriesCallCount() int {
	s.entriesMu.Lock()
	defer s.entriesMu.Unlock()
	return len(s.ranges)
}

func (s *countingEntriesStorage) entriesRanges() []entryRange {
	s.entriesMu.Lock()
	defer s.entriesMu.Unlock()
	return append([]entryRange(nil), s.ranges...)
}

func (f *internalFakeStorage) InitialState(ctx context.Context) (BootstrapState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.state, nil
}

func (f *internalFakeStorage) Entries(ctx context.Context, lo, hi, maxSize uint64) ([]raftpb.Entry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	var out []raftpb.Entry
	for _, entry := range f.entries {
		if entry.Index >= lo && entry.Index < hi {
			out = append(out, entry)
		}
	}
	return out, nil
}

func (f *internalFakeStorage) Term(ctx context.Context, index uint64) (uint64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	for _, entry := range f.entries {
		if entry.Index == index {
			return entry.Term, nil
		}
	}
	return 0, nil
}

func (f *internalFakeStorage) FirstIndex(ctx context.Context) (uint64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if len(f.entries) == 0 {
		if !raft.IsEmptySnap(f.snapshot) {
			return f.snapshot.Metadata.Index + 1, nil
		}
		return 1, nil
	}
	return f.entries[0].Index, nil
}

func (f *internalFakeStorage) LastIndex(ctx context.Context) (uint64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if len(f.entries) == 0 {
		return f.snapshot.Metadata.Index, nil
	}
	return f.entries[len(f.entries)-1].Index, nil
}

func (f *internalFakeStorage) Snapshot(ctx context.Context) (raftpb.Snapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.snapshot, nil
}

func (f *internalFakeStorage) Save(ctx context.Context, st PersistentState) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.saveErr != nil {
		return f.saveErr
	}

	f.saveCount++
	if st.HardState != nil {
		f.state.HardState = *st.HardState
		if st.HardState.Commit > f.lastSavedIndex {
			f.lastSavedIndex = st.HardState.Commit
		}
	}
	if len(st.Entries) > 0 {
		first := st.Entries[0].Index
		kept := f.entries[:0]
		for _, entry := range f.entries {
			if entry.Index < first {
				kept = append(kept, entry)
			}
		}
		f.entries = append(append([]raftpb.Entry(nil), kept...), st.Entries...)
		f.lastSavedIndex = st.Entries[len(st.Entries)-1].Index
	}
	if st.Snapshot != nil {
		f.snapshot = *st.Snapshot
		kept := f.entries[:0]
		for _, entry := range f.entries {
			if entry.Index > st.Snapshot.Metadata.Index {
				kept = append(kept, entry)
			}
		}
		f.entries = append([]raftpb.Entry(nil), kept...)
		f.lastSavedIndex = st.Snapshot.Metadata.Index
	}
	return nil
}

func (f *internalFakeStorage) MarkApplied(ctx context.Context, index uint64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.markAppliedErr != nil {
		return f.markAppliedErr
	}

	f.markAppliedCount++
	f.lastApplied = index
	f.state.AppliedIndex = index
	return nil
}

func (f *internalFakeStorage) MarkConfigApplied(ctx context.Context, index uint64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.state.ConfigAppliedIndex = index
	return nil
}

type internalFakeStateMachine struct {
	mu           sync.Mutex
	applied      [][]byte
	commands     []Command
	applyErr     error
	restoreCount int
	lastSnapshot Snapshot
	restoreErr   error
}

func (f *internalFakeStateMachine) Apply(ctx context.Context, cmd Command) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.applied = append(f.applied, append([]byte(nil), cmd.Data...))
	f.commands = append(f.commands, Command{
		SlotID:   cmd.SlotID,
		HashSlot: cmd.HashSlot,
		Index:    cmd.Index,
		Term:     cmd.Term,
		Data:     append([]byte(nil), cmd.Data...),
	})
	if f.applyErr != nil {
		return nil, f.applyErr
	}
	return append([]byte("ok:"), cmd.Data...), nil
}

func (f *internalFakeStateMachine) Restore(ctx context.Context, snap Snapshot) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.restoreCount++
	f.lastSnapshot = snap
	if f.restoreErr != nil {
		return f.restoreErr
	}
	return nil
}

func (f *internalFakeStateMachine) Snapshot(ctx context.Context) (Snapshot, error) {
	return Snapshot{}, nil
}
