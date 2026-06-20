package multiraft

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	raft "go.etcd.io/raft/v3"
	"go.etcd.io/raft/v3/raftpb"
)

func TestRuntimeLogCompactionSnapshotsAndCompactsAppliedEntries(t *testing.T) {
	rt := newCompactionRuntime(t, LogCompactionConfig{
		Enabled:        true,
		EnabledSet:     true,
		TriggerEntries: 1,
		CheckInterval:  time.Nanosecond,
	})
	store := &internalFakeStorage{}
	fsm := &snapshottingStateMachine{}
	slotID := SlotID(190)
	if err := rt.BootstrapSlot(context.Background(), BootstrapSlotRequest{
		Slot:   SlotOptions{ID: slotID, Storage: store, StateMachine: fsm},
		Voters: []NodeID{1},
	}); err != nil {
		t.Fatalf("BootstrapSlot() error = %v", err)
	}
	waitForSingleNodeLeader(t, rt, slotID)

	var last Result
	for i := 1; i <= 3; i++ {
		fut, err := rt.Propose(context.Background(), slotID, proposalString(fmt.Sprintf("set-%d", i)))
		if err != nil {
			t.Fatalf("Propose(%d) error = %v", i, err)
		}
		last = waitForFutureResult(t, fut)
	}

	waitForCondition(t, func() bool {
		snap, err := store.Snapshot(context.Background())
		return err == nil && snap.Metadata.Index >= last.Index
	})

	snap, err := store.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if snap.Metadata.Index != last.Index {
		t.Fatalf("Snapshot().Index = %d, want %d", snap.Metadata.Index, last.Index)
	}
	wantData := fmt.Sprintf("idx=%d data=set-3", last.Index)
	if string(snap.Data) != wantData {
		t.Fatalf("Snapshot().Data = %q, want final applied command", snap.Data)
	}
	first, err := store.FirstIndex(context.Background())
	if err != nil {
		t.Fatalf("FirstIndex() error = %v", err)
	}
	if first != snap.Metadata.Index+1 {
		t.Fatalf("FirstIndex() = %d, want %d", first, snap.Metadata.Index+1)
	}
}

func TestRuntimeLogCompactionCanBeDisabled(t *testing.T) {
	rt := newCompactionRuntime(t, LogCompactionConfig{
		Enabled:        false,
		EnabledSet:     true,
		TriggerEntries: 1,
		CheckInterval:  time.Nanosecond,
	})
	store := &internalFakeStorage{}
	slotID := SlotID(191)
	if err := rt.BootstrapSlot(context.Background(), BootstrapSlotRequest{
		Slot:   SlotOptions{ID: slotID, Storage: store, StateMachine: &snapshottingStateMachine{}},
		Voters: []NodeID{1},
	}); err != nil {
		t.Fatalf("BootstrapSlot() error = %v", err)
	}
	waitForSingleNodeLeader(t, rt, slotID)

	for i := 1; i <= 3; i++ {
		fut, err := rt.Propose(context.Background(), slotID, proposalString(fmt.Sprintf("disabled-%d", i)))
		if err != nil {
			t.Fatalf("Propose(%d) error = %v", i, err)
		}
		waitForFutureResult(t, fut)
	}

	snap, err := store.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if !raft.IsEmptySnap(snap) {
		t.Fatalf("Snapshot() = %+v, want empty when compaction disabled", snap.Metadata)
	}
}

func TestRuntimeLogCompactionFailureDoesNotFailSlot(t *testing.T) {
	rt := newCompactionRuntime(t, LogCompactionConfig{
		Enabled:        true,
		EnabledSet:     true,
		TriggerEntries: 1,
		CheckInterval:  time.Nanosecond,
	})
	store := &internalFakeStorage{}
	sentinel := errors.New("snapshot failed once")
	fsm := &snapshottingStateMachine{snapshotErr: sentinel}
	slotID := SlotID(192)
	if err := rt.BootstrapSlot(context.Background(), BootstrapSlotRequest{
		Slot:   SlotOptions{ID: slotID, Storage: store, StateMachine: fsm},
		Voters: []NodeID{1},
	}); err != nil {
		t.Fatalf("BootstrapSlot() error = %v", err)
	}
	waitForSingleNodeLeader(t, rt, slotID)

	first, err := rt.Propose(context.Background(), slotID, proposalString("after-failed-compaction"))
	if err != nil {
		t.Fatalf("first Propose() error = %v", err)
	}
	waitForFutureResult(t, first)

	second, err := rt.Propose(context.Background(), slotID, proposalString("after-retry"))
	if err != nil {
		t.Fatalf("second Propose() error = %v", err)
	}
	res := waitForFutureResult(t, second)
	waitForCondition(t, func() bool {
		snap, err := store.Snapshot(context.Background())
		return err == nil && snap.Metadata.Index >= res.Index
	})
}

func TestRuntimeManualLogCompactionForcesSnapshotBelowAutomaticThreshold(t *testing.T) {
	rt := newCompactionRuntime(t, LogCompactionConfig{
		Enabled:        true,
		EnabledSet:     true,
		TriggerEntries: 1000,
		CheckInterval:  time.Hour,
	})
	store := &internalFakeStorage{}
	slotID := SlotID(194)
	if err := rt.BootstrapSlot(context.Background(), BootstrapSlotRequest{
		Slot:   SlotOptions{ID: slotID, Storage: store, StateMachine: &snapshottingStateMachine{}},
		Voters: []NodeID{1},
	}); err != nil {
		t.Fatalf("BootstrapSlot() error = %v", err)
	}
	waitForSingleNodeLeader(t, rt, slotID)

	fut, err := rt.Propose(context.Background(), slotID, proposalString("manual"))
	if err != nil {
		t.Fatalf("Propose() error = %v", err)
	}
	applied := waitForFutureResult(t, fut).Index
	firstBefore, err := store.FirstIndex(context.Background())
	if err != nil {
		t.Fatalf("FirstIndex() before error = %v", err)
	}

	result, err := rt.CompactLog(context.Background(), slotID)
	if err != nil {
		t.Fatalf("CompactLog() error = %v", err)
	}

	if !result.Compacted {
		t.Fatalf("CompactLog().Compacted = false, skipped=%q", result.SkippedReason)
	}
	if result.NodeID != 1 || result.SlotID != slotID {
		t.Fatalf("CompactLog() node/slot = %d/%d, want 1/%d", result.NodeID, result.SlotID, slotID)
	}
	if result.AppliedIndex != applied || result.AfterSnapshotIndex != applied {
		t.Fatalf("CompactLog() indexes = applied:%d after:%d, want %d", result.AppliedIndex, result.AfterSnapshotIndex, applied)
	}
	if result.BeforeSnapshotIndex != 0 {
		t.Fatalf("BeforeSnapshotIndex = %d, want 0", result.BeforeSnapshotIndex)
	}
	firstAfter, err := store.FirstIndex(context.Background())
	if err != nil {
		t.Fatalf("FirstIndex() after error = %v", err)
	}
	if firstAfter <= firstBefore {
		t.Fatalf("FirstIndex after manual compaction = %d, want > %d", firstAfter, firstBefore)
	}
}

func TestRuntimeManualLogCompactionWaitsForAsyncApply(t *testing.T) {
	rt := newCompactionRuntime(t, LogCompactionConfig{
		Enabled:        true,
		EnabledSet:     true,
		TriggerEntries: 1000,
		CheckInterval:  time.Hour,
	})
	slotID := SlotID(197)
	fsm := newBlockingStateMachine()
	t.Cleanup(func() {
		fsm.unblock()
	})

	if err := rt.BootstrapSlot(context.Background(), BootstrapSlotRequest{
		Slot: SlotOptions{
			ID:           slotID,
			Storage:      &internalFakeStorage{},
			StateMachine: fsm,
		},
		Voters: []NodeID{1},
	}); err != nil {
		t.Fatalf("BootstrapSlot() error = %v", err)
	}
	waitForSingleNodeLeader(t, rt, slotID)

	fut, err := rt.Propose(context.Background(), slotID, proposalString("manual-barrier"))
	if err != nil {
		t.Fatalf("Propose() error = %v", err)
	}
	select {
	case <-fsm.started:
	case <-time.After(time.Second):
		t.Fatal("Apply() did not start")
	}

	done := make(chan error, 1)
	go func() {
		_, err := rt.CompactLog(context.Background(), slotID)
		done <- err
	}()
	waitForSlotWorkerWaitingOnApply(t, rt, slotID)
	select {
	case err := <-done:
		t.Fatalf("CompactLog() returned before async apply finished: %v", err)
	default:
	}

	fsm.unblock()
	if _, err := fut.Wait(context.Background()); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("CompactLog() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("CompactLog() did not return after async apply unblocked")
	}
}

func TestRuntimeManualLogCompactionCancellationDoesNotPinSlotWorker(t *testing.T) {
	rt := newCompactionRuntime(t, LogCompactionConfig{
		Enabled:        true,
		EnabledSet:     true,
		TriggerEntries: 1000,
		CheckInterval:  time.Hour,
	})
	slotID := SlotID(198)
	fsm := newBlockingStateMachine()
	t.Cleanup(func() {
		fsm.unblock()
	})

	if err := rt.BootstrapSlot(context.Background(), BootstrapSlotRequest{
		Slot: SlotOptions{
			ID:           slotID,
			Storage:      &internalFakeStorage{},
			StateMachine: fsm,
		},
		Voters: []NodeID{1},
	}); err != nil {
		t.Fatalf("BootstrapSlot() error = %v", err)
	}
	waitForSingleNodeLeader(t, rt, slotID)

	fut, err := rt.Propose(context.Background(), slotID, proposalString("manual-cancel"))
	if err != nil {
		t.Fatalf("Propose() error = %v", err)
	}
	select {
	case <-fsm.started:
	case <-time.After(time.Second):
		t.Fatal("Apply() did not start")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err = rt.CompactLog(ctx, slotID)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("CompactLog() error = %v, want %v", err, context.DeadlineExceeded)
	}

	beforeTicks := slotTickCount(rt, slotID)
	g := slotFor(rt, slotID)
	if g == nil {
		t.Fatal("slotFor() = nil")
	}
	g.markTickPending()
	rt.scheduler.enqueue(slotID)
	waitForCondition(t, func() bool {
		return slotTickCount(rt, slotID) > beforeTicks
	})

	fsm.unblock()
	if _, err := fut.Wait(context.Background()); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
}

func TestRuntimeManualCompactionReadsSnapshotMetadataWithoutPayload(t *testing.T) {
	sentinel := errors.New("snapshot payload unavailable")
	store := &slotSnapshotMetadataOnlyStorage{
		firstIndex:  11,
		lastIndex:   12,
		snapshotErr: sentinel,
		terms:       map[uint64]uint64{10: 7},
	}
	g := &slot{
		id:      SlotID(196),
		storage: store,
		status:  Status{NodeID: 1},
		compactor: newLogCompactor(LogCompactionConfig{
			Enabled:        true,
			EnabledSet:     true,
			TriggerEntries: 1000,
			CheckInterval:  time.Hour,
		}, 0),
	}

	result, err := g.compactLogManually(context.Background(), 10)
	if err != nil {
		t.Fatalf("compactLogManually() error = %v", err)
	}
	if result.Compacted {
		t.Fatalf("compactLogManually().Compacted = true, want false")
	}
	if result.SkippedReason != LogCompactionSkippedUpToDate {
		t.Fatalf("compactLogManually().SkippedReason = %q, want %q", result.SkippedReason, LogCompactionSkippedUpToDate)
	}
	if result.BeforeSnapshotIndex != 10 || result.AfterSnapshotIndex != 10 {
		t.Fatalf("compactLogManually() snapshot indexes = before:%d after:%d, want 10", result.BeforeSnapshotIndex, result.AfterSnapshotIndex)
	}
	if store.snapshotCalls != 0 {
		t.Fatalf("Snapshot() calls = %d, want 0", store.snapshotCalls)
	}
	if store.firstIndexCalls == 0 || store.termCalls == 0 {
		t.Fatalf("metadata calls = FirstIndex:%d Term:%d, want both used", store.firstIndexCalls, store.termCalls)
	}
}

func TestRuntimeCompactedLeaderSendsSnapshotToNewLearner(t *testing.T) {
	cluster := newAsyncTestCluster(t, []NodeID{1, 2, 3}, asyncNetworkConfig{
		MaxDelay: time.Millisecond,
		Seed:     19,
	})
	slotID := SlotID(195)
	voters := []NodeID{1, 2}
	cluster.bootstrapSlot(t, slotID, voters)
	cluster.waitForLeaderAmong(t, slotID, voters)

	leaderID := cluster.waitForLeaderAmong(t, slotID, voters)
	var last Result
	for i := 0; i < 3; i++ {
		fut, err := cluster.runtime(leaderID).Propose(context.Background(), slotID, proposalString(fmt.Sprintf("before-learner-%d", i)))
		if err != nil {
			t.Fatalf("Propose(%d) error = %v", i, err)
		}
		last = waitForFutureResult(t, fut)
	}
	cluster.waitForNodeCommitIndex(t, leaderID, slotID, last.Index)

	compacted, err := cluster.runtime(leaderID).CompactLog(context.Background(), slotID)
	if err != nil {
		t.Fatalf("CompactLog() error = %v", err)
	}
	if !compacted.Compacted {
		t.Fatalf("CompactLog().Compacted = false, skipped=%q", compacted.SkippedReason)
	}

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

	change, err := cluster.runtime(leaderID).ChangeConfig(context.Background(), slotID, ConfigChange{
		Type:   AddLearner,
		NodeID: 3,
	})
	if err != nil {
		t.Fatalf("ChangeConfig(AddLearner) error = %v", err)
	}
	changeResult := waitForFutureResult(t, change)

	var learnerStatus Status
	var learnerStatusErr error
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		cluster.requireHealthyNetwork(t)
		learnerStatus, learnerStatusErr = cluster.runtime(3).Status(slotID)
		if learnerStatusErr == nil && learnerStatus.AppliedIndex >= changeResult.Index {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if learnerStatusErr != nil || learnerStatus.AppliedIndex < changeResult.Index {
		t.Fatalf("learner status = %+v err=%v, want applied >= %d", learnerStatus, learnerStatusErr, changeResult.Index)
	}
	learnerSnap, err := learnerStore.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("learner Snapshot() error = %v", err)
	}
	if learnerSnap.Metadata.Index < compacted.AfterSnapshotIndex {
		t.Fatalf("learner snapshot index = %d, want >= compacted index %d", learnerSnap.Metadata.Index, compacted.AfterSnapshotIndex)
	}
	learnerFSM.mu.Lock()
	restoreCount := learnerFSM.restoreCount
	learnerFSM.mu.Unlock()
	if restoreCount == 0 {
		t.Fatal("learner Restore() count = 0, want snapshot restore")
	}
}

func TestOpenSlotRestoresSnapshotThenReplaysPostSnapshotEntries(t *testing.T) {
	store := &internalFakeStorage{}
	ctx := context.Background()
	snap := raftpb.Snapshot{
		Data: []byte("snap"),
		Metadata: raftpb.SnapshotMetadata{
			Index: 2,
			Term:  1,
			ConfState: raftpb.ConfState{
				Voters: []uint64{1},
			},
		},
	}
	hs := raftpb.HardState{Term: 1, Commit: 4}
	if err := store.Save(ctx, PersistentState{
		HardState: &hs,
		Snapshot:  &snap,
		Entries: []raftpb.Entry{
			{Index: 3, Term: 1, Type: raftpb.EntryNormal, Data: proposalString("post-snap-3")},
			{Index: 4, Term: 1, Type: raftpb.EntryNormal, Data: proposalString("post-snap-4")},
		},
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if err := store.MarkApplied(ctx, 4); err != nil {
		t.Fatalf("MarkApplied() error = %v", err)
	}

	rt := newCompactionRuntime(t, LogCompactionConfig{Enabled: false, EnabledSet: true})
	fsm := &snapshottingStateMachine{}
	if err := rt.OpenSlot(ctx, SlotOptions{ID: 193, Storage: store, StateMachine: fsm}); err != nil {
		t.Fatalf("OpenSlot() error = %v", err)
	}

	waitForCondition(t, func() bool {
		fsm.mu.Lock()
		defer fsm.mu.Unlock()
		return fsm.restoreCount == 1 && len(fsm.commands) == 2
	})
	fsm.mu.Lock()
	defer fsm.mu.Unlock()
	if got := fsm.commands[0].Data; string(got) != "post-snap-3" {
		t.Fatalf("first replayed command = %q, want post-snap-3", got)
	}
	if got := fsm.commands[1].Data; string(got) != "post-snap-4" {
		t.Fatalf("second replayed command = %q, want post-snap-4", got)
	}
}

func newCompactionRuntime(t *testing.T, compaction LogCompactionConfig) *Runtime {
	t.Helper()
	rt, err := New(Options{
		NodeID:       1,
		TickInterval: 10 * time.Millisecond,
		Workers:      1,
		Transport:    &internalFakeTransport{},
		Raft: RaftOptions{
			ElectionTick:  10,
			HeartbeatTick: 1,
			LogCompaction: compaction,
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

func waitForSingleNodeLeader(t *testing.T, rt *Runtime, slotID SlotID) {
	t.Helper()
	waitForCondition(t, func() bool {
		st, err := rt.Status(slotID)
		return err == nil && st.Role == RoleLeader
	})
}

func waitForSlotWorkerWaitingOnApply(t *testing.T, rt *Runtime, slotID SlotID) {
	t.Helper()
	waitForCondition(t, func() bool {
		g := slotFor(rt, slotID)
		if g == nil {
			return false
		}
		g.mu.Lock()
		defer g.mu.Unlock()
		return g.processing && g.applying > 0
	})
}

type snapshottingStateMachine struct {
	mu           sync.Mutex
	commands     []Command
	restoreCount int
	snapshotErr  error
}

func (s *snapshottingStateMachine) Apply(_ context.Context, cmd Command) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.commands = append(s.commands, Command{
		SlotID:   cmd.SlotID,
		HashSlot: cmd.HashSlot,
		Index:    cmd.Index,
		Term:     cmd.Term,
		Data:     append([]byte(nil), cmd.Data...),
	})
	return append([]byte("ok:"), cmd.Data...), nil
}

func (s *snapshottingStateMachine) Restore(_ context.Context, snap Snapshot) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.restoreCount++
	return nil
}

func (s *snapshottingStateMachine) Snapshot(context.Context) (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.snapshotErr != nil {
		err := s.snapshotErr
		s.snapshotErr = nil
		return Snapshot{}, err
	}
	if len(s.commands) == 0 {
		return Snapshot{Data: []byte("empty")}, nil
	}
	last := s.commands[len(s.commands)-1]
	return Snapshot{Data: []byte(fmt.Sprintf("idx=%d data=%s", last.Index, last.Data))}, nil
}

type slotSnapshotMetadataOnlyStorage struct {
	state BootstrapState

	firstIndex uint64
	lastIndex  uint64
	terms      map[uint64]uint64

	snapshotErr     error
	firstIndexCalls int
	termCalls       int
	snapshotCalls   int
}

func (s *slotSnapshotMetadataOnlyStorage) InitialState(context.Context) (BootstrapState, error) {
	return s.state, nil
}

func (s *slotSnapshotMetadataOnlyStorage) Entries(context.Context, uint64, uint64, uint64) ([]raftpb.Entry, error) {
	return nil, nil
}

func (s *slotSnapshotMetadataOnlyStorage) Term(_ context.Context, index uint64) (uint64, error) {
	s.termCalls++
	return s.terms[index], nil
}

func (s *slotSnapshotMetadataOnlyStorage) FirstIndex(context.Context) (uint64, error) {
	s.firstIndexCalls++
	return s.firstIndex, nil
}

func (s *slotSnapshotMetadataOnlyStorage) LastIndex(context.Context) (uint64, error) {
	return s.lastIndex, nil
}

func (s *slotSnapshotMetadataOnlyStorage) Snapshot(context.Context) (raftpb.Snapshot, error) {
	s.snapshotCalls++
	return raftpb.Snapshot{}, s.snapshotErr
}

func (s *slotSnapshotMetadataOnlyStorage) Save(context.Context, PersistentState) error {
	return nil
}

func (s *slotSnapshotMetadataOnlyStorage) MarkApplied(context.Context, uint64) error {
	return nil
}
