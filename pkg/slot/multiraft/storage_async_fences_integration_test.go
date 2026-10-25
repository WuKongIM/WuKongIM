//go:build integration

package multiraft

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	raft "go.etcd.io/raft/v3"
	"go.etcd.io/raft/v3/raftpb"
)

// Safety matrix written before async storage product changes:
// failed Save must not resolve a proposal successfully or reach FSM apply;
// Close, CloseSlot and ReloadSlot must join active physical append before a new
// generation can use its storage; late completion cannot rewrite an old Future;
// snapshot Restore and configuration application must not pass an earlier
// durable-apply fence. Existing real-Pebble compaction/restart and learner catchup
// tests remain required. These public Runtime/Storage/Future probes establish
// baseline safety; their deadline is cleanup, never a performance threshold.
func TestRuntimeStorageSaveFailureCannotConfirmOrApply(t *testing.T) {
	store := newStorageFenceGate()
	fsm := &storageAsyncFSM{internalFakeStateMachine: &internalFakeStateMachine{}, store: store}
	rt, ctx, baseline := storageFenceRuntime(t, store, fsm)
	t.Cleanup(store.release)
	store.target.Store(baseline.CommitIndex + 1)
	store.armed.Store(true)
	future, err := rt.Propose(ctx, storageFenceSlot, proposalString("must-not-confirm-failed-save"))
	if err != nil {
		t.Fatal(err)
	}
	storageFenceEntered(t, ctx, store.entered)
	pending, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	_, err = future.Wait(pending)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("proposal escaped held Save: %v", err)
	}
	saveErr := errors.New("controlled append failure")
	store.internalFakeStorage.mu.Lock()
	store.internalFakeStorage.saveErr = saveErr
	store.internalFakeStorage.mu.Unlock()
	store.release()
	if _, err = future.Wait(ctx); !errors.Is(err, saveErr) {
		t.Fatalf("failed Save future = %v", err)
	}
	state, err := store.InitialState(ctx)
	if err != nil || state.AppliedIndex != baseline.AppliedIndex {
		t.Fatalf("failed Save changed durable apply: %+v %v", state, err)
	}
	fsm.mu.Lock()
	applied := len(fsm.applied)
	fsm.mu.Unlock()
	if applied != 0 || fsm.earlyApply.Load() || store.durable.Load() {
		t.Fatal("failed append reached FSM/durable success")
	}
	// A second Wait observes the same terminal failure, not a later success.
	if _, err = future.Wait(ctx); !errors.Is(err, saveErr) {
		t.Fatalf("failure was not stable: %v", err)
	}
}

func TestRuntimeStorageCloseAndReloadJoinAppendGeneration(t *testing.T) {
	for _, operation := range []string{"runtime_close", "close_slot", "reload_slot"} {
		t.Run(operation, func(t *testing.T) {
			store := newStorageFenceGate()
			fsm := &storageAsyncFSM{internalFakeStateMachine: &internalFakeStateMachine{}, store: store}
			rt, ctx, baseline := storageFenceRuntime(t, store, fsm)
			t.Cleanup(store.release)
			// The existing in-memory Storage fixture does not derive ConfState
			// from committed membership entries. A public compaction installs
			// the actual baseline membership in durable snapshot metadata, so
			// reopen tests a recoverable store without mutating Runtime state.
			initial, err := store.InitialState(ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("fixture baseline: durable voters=%v; Runtime voters=%v", initial.ConfState.Voters, baseline.CurrentVoters)
			compacted, err := rt.CompactLog(ctx, storageFenceSlot)
			if err != nil || !compacted.Compacted {
				t.Fatalf("recoverable baseline snapshot: %+v %v", compacted, err)
			}
			snap, err := store.Snapshot(ctx)
			if err != nil || snap.Metadata.Index != baseline.AppliedIndex || !reflect.DeepEqual(snap.Metadata.ConfState.Voters, []uint64{1}) {
				t.Fatalf("baseline snapshot lacks durable membership: %+v %v", snap.Metadata, err)
			}
			store.target.Store(baseline.CommitIndex + 1)
			store.armed.Store(true)
			old, err := rt.Propose(ctx, storageFenceSlot, proposalString("old-generation-held-write"))
			if err != nil {
				t.Fatal(err)
			}
			storageFenceEntered(t, ctx, store.entered)
			done := make(chan error, 1)
			go func() {
				switch operation {
				case "runtime_close":
					done <- rt.Close()
				case "close_slot":
					done <- rt.CloseSlot(ctx, storageFenceSlot)
				case "reload_slot":
					done <- rt.ReloadSlot(ctx, storageFenceSlot)
				}
			}()
			expected := ErrSlotClosed
			if operation == "runtime_close" {
				expected = ErrRuntimeClosed
			}
			// The public Future proves close has fenced admissions before checking join.
			if _, err = old.Wait(ctx); !errors.Is(err, expected) {
				t.Fatalf("old Future close result=%v want=%v", err, expected)
			}
			select {
			case err := <-done:
				t.Fatalf("%s returned while physical Save remained held: %v", operation, err)
			default:
			}
			if store.durable.Load() {
				t.Fatal("held append unexpectedly durable")
			}
			store.release()
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-ctx.Done():
				t.Fatal("close/reload did not join released Save")
			}
			if !store.durable.Load() {
				t.Fatal("lifecycle finished before append worker returned")
			}
			if _, err = old.Wait(ctx); !errors.Is(err, expected) {
				t.Fatalf("late append rewrote old Future: %v", err)
			}
			if operation == "runtime_close" {
				return
			}
			if operation == "close_slot" {
				if err := rt.OpenSlot(ctx, SlotOptions{ID: storageFenceSlot, Storage: store, StateMachine: fsm}); err != nil {
					t.Fatal(err)
				}
			}
			storageAsyncWait(t, ctx, func() bool {
				s, e := rt.Status(storageFenceSlot)
				return e == nil && s.Role == RoleLeader && s.AppliedIndex == s.CommitIndex
			})
			fresh, err := rt.Propose(ctx, storageFenceSlot, proposalString("new-generation-result"))
			if err != nil {
				t.Fatal(err)
			}
			result, err := fresh.Wait(ctx)
			if err != nil || string(result.Data) != "ok:new-generation-result" {
				t.Fatalf("new generation corrupted: %+v %v", result, err)
			}
			if _, err = old.Wait(ctx); !errors.Is(err, expected) {
				t.Fatalf("new generation rewrote old Future: %v", err)
			}
			if err := rt.ReadBarrier(ctx, storageFenceSlot); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRuntimeStorageSnapshotWaitsForPredecessorDurableApply(t *testing.T) {
	store := newBlockingMarkAppliedStorage()
	probe := &storageFenceRestoreProbe{internalFakeStateMachine: &internalFakeStateMachine{}, store: store, restored: make(chan struct{})}
	rt, ctx, baseline := storageFenceRuntime(t, store, probe)
	t.Cleanup(store.unblock)
	probe.required.Store(baseline.CommitIndex + 1)
	store.armAfter(baseline.CommitIndex + 1)
	_, err := rt.Propose(ctx, storageFenceSlot, proposalString("apply-before-snapshot"))
	if err != nil {
		t.Fatal(err)
	}
	storageFenceEntered(t, ctx, store.started)
	snapIndex := baseline.CommitIndex + 100
	if err := rt.Step(ctx, Envelope{SlotID: storageFenceSlot, Message: raftpb.Message{Type: raftpb.MsgSnap, From: 2, To: 1, Term: baseline.Term + 1, Snapshot: &raftpb.Snapshot{Data: []byte("fenced-snapshot"), Metadata: raftpb.SnapshotMetadata{Index: snapIndex, Term: baseline.Term + 1, ConfState: raftpb.ConfState{Voters: []uint64{1}}}}}}); err != nil {
		t.Fatal(err)
	}
	// Restoring before MarkApplied returns is forbidden even when snapshot bytes
	// themselves are already durable. The probe checks the Storage port on entry.
	timer := time.NewTimer(20 * time.Millisecond)
	select {
	case <-probe.restored:
		timer.Stop()
		t.Fatal("snapshot Restore bypassed preceding durable apply")
	case <-timer.C:
	case <-ctx.Done():
		timer.Stop()
		t.Fatal("snapshot safety deadline")
	}
	store.unblock()
	storageFenceEntered(t, ctx, probe.restored)
	if probe.early.Load() {
		t.Fatal("snapshot Restore observed predecessor below durable watermark")
	}
	storageAsyncWait(t, ctx, func() bool { s, e := rt.Status(storageFenceSlot); return e == nil && s.AppliedIndex >= snapIndex })
	probe.mu.Lock()
	got := probe.lastSnapshot
	probe.mu.Unlock()
	if got.Index != snapIndex || string(got.Data) != "fenced-snapshot" {
		t.Fatalf("snapshot was not installed exactly: %+v", got)
	}
}

func TestRuntimeStorageConfigWaitsForPredecessorDurableApply(t *testing.T) {
	store := newBlockingMarkAppliedStorage()
	rt, ctx, baseline := storageFenceRuntime(t, store, &internalFakeStateMachine{})
	t.Cleanup(store.unblock)
	store.armAfter(baseline.CommitIndex + 1)
	prior, err := rt.Propose(ctx, storageFenceSlot, proposalString("apply-before-config"))
	if err != nil {
		t.Fatal(err)
	}
	storageFenceEntered(t, ctx, store.started)
	config, err := rt.ChangeConfig(ctx, storageFenceSlot, ConfigChange{Type: AddLearner, NodeID: 2})
	if err != nil {
		t.Fatal(err)
	}
	pending, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	_, err = config.Wait(pending)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("config bypassed predecessor: %v", err)
	}
	status, err := rt.Status(storageFenceSlot)
	if err != nil || len(status.CurrentLearners) != 0 || status.ConfigAppliedIndex != baseline.ConfigAppliedIndex {
		t.Fatalf("config published across durable fence: %+v %v", status, err)
	}
	store.unblock()
	priorResult, err := prior.Wait(ctx)
	if err != nil {
		t.Fatal(err)
	}
	configResult, err := config.Wait(ctx)
	if err != nil || configResult.Index <= priorResult.Index {
		t.Fatalf("config order=%+v after=%+v err=%v", configResult, priorResult, err)
	}
	storageAsyncWait(t, ctx, func() bool {
		s, e := rt.Status(storageFenceSlot)
		return e == nil && len(s.CurrentLearners) == 1 && s.CurrentLearners[0] == 2 && s.ConfigAppliedIndex == configResult.Index && s.AppliedIndex >= configResult.Index
	})
	state, err := store.InitialState(ctx)
	if err != nil || state.ConfigAppliedIndex != configResult.Index || state.AppliedIndex < configResult.Index {
		t.Fatalf("config future preceded durable metadata: %+v %v", state, err)
	}
	if err := rt.ReadBarrier(ctx, storageFenceSlot); err != nil {
		t.Fatal(err)
	}
}

const storageFenceSlot SlotID = 313

func newStorageFenceGate() *storageAsyncGate {
	return &storageAsyncGate{internalFakeStorage: &internalFakeStorage{}, entered: make(chan struct{}), releaseCh: make(chan struct{})}
}

// Reuse existing port fixtures; tests never step RawNode or mutate Runtime queues.
func storageFenceRuntime(t *testing.T, store Storage, fsm StateMachine) (*Runtime, context.Context, Status) {
	t.Helper()
	rt := newStartedRuntimeWithTick(t, 10*time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	t.Cleanup(cancel)
	if err := rt.BootstrapSlot(ctx, BootstrapSlotRequest{Slot: SlotOptions{ID: storageFenceSlot, Storage: store, StateMachine: fsm}, Voters: []NodeID{1}}); err != nil {
		t.Fatal(err)
	}
	var baseline Status
	storageAsyncWait(t, ctx, func() bool {
		s, e := rt.Status(storageFenceSlot)
		if e != nil || s.Role != RoleLeader || s.CommitIndex == 0 || s.AppliedIndex != s.CommitIndex {
			return false
		}
		baseline = s
		return true
	})
	return rt, ctx, baseline
}
func storageFenceEntered(t *testing.T, ctx context.Context, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-ctx.Done():
		t.Fatal("controlled public port boundary not reached")
	}
}

type storageFenceRestoreProbe struct {
	*internalFakeStateMachine
	store    Storage
	required atomic.Uint64
	early    atomic.Bool
	restored chan struct{}
	once     sync.Once
}

func (p *storageFenceRestoreProbe) Restore(ctx context.Context, s Snapshot) error {
	if required := p.required.Load(); required != 0 {
		state, err := p.store.InitialState(ctx)
		if err != nil || state.AppliedIndex < required {
			p.early.Store(true)
		}
	}
	err := p.internalFakeStateMachine.Restore(ctx, s)
	p.once.Do(func() { close(p.restored) })
	return err
}

// External Step is a network entry, never a reliable local storage completion
// channel. Reserved messages/targets must fail synchronously before enqueue.
// The first case is deliberately harmless on the old implementation so the
// behavioral RED cannot inject a dangerous forged completion into RawNode.
func TestRuntimeStorageStepRejectsReservedCompletions(t *testing.T) {
	store := &internalFakeStorage{}
	fsm := &internalFakeStateMachine{}
	rt, ctx, baseline := storageFenceRuntime(t, store, fsm)
	before, err := store.InitialState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	last, err := store.LastIndex(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name    string
		message raftpb.Message
	}{
		{"append_type", raftpb.Message{Type: raftpb.MsgStorageAppend, From: 2, To: 1}},
		{"apply_type", raftpb.Message{Type: raftpb.MsgStorageApply, From: 2, To: 1}},
		{"append_response_type", raftpb.Message{Type: raftpb.MsgStorageAppendResp, From: raft.LocalAppendThread, To: 1}},
		{"apply_response_type", raftpb.Message{Type: raftpb.MsgStorageApplyResp, From: raft.LocalApplyThread, To: 1}},
		{"append_sender", raftpb.Message{Type: raftpb.MsgHeartbeat, From: raft.LocalAppendThread, To: 1}},
		{"apply_sender", raftpb.Message{Type: raftpb.MsgHeartbeat, From: raft.LocalApplyThread, To: 1}},
		{"append_target", raftpb.Message{Type: raftpb.MsgHeartbeat, From: 2, To: raft.LocalAppendThread}},
		{"apply_target", raftpb.Message{Type: raftpb.MsgHeartbeat, From: 2, To: raft.LocalApplyThread}},
	}
	for _, test := range cases {
		test.message.Term = baseline.Term
		test.message.Commit = baseline.CommitIndex
		if err := rt.Step(ctx, Envelope{SlotID: storageFenceSlot, Message: test.message}); err == nil {
			t.Fatalf("external Step accepted reserved local storage message/target: %s", test.name)
		}
		state, err := store.InitialState(ctx)
		if err != nil || !reflect.DeepEqual(state, before) {
			t.Fatalf("rejected %s changed durable state: %+v %v", test.name, state, err)
		}
		current, err := store.LastIndex(ctx)
		if err != nil || current != last {
			t.Fatalf("rejected %s changed durable log: %d %v", test.name, current, err)
		}
	}
	if err := rt.ReadBarrier(ctx, storageFenceSlot); err != nil {
		t.Fatal(err)
	}
	state, err := store.InitialState(ctx)
	if err != nil || !reflect.DeepEqual(state, before) {
		t.Fatalf("rejected completion later changed durable/apply state: %+v %v", state, err)
	}
	fsm.mu.Lock()
	applied := len(fsm.applied)
	fsm.mu.Unlock()
	if applied != 0 {
		t.Fatal("reserved completion reached FSM")
	}
}
