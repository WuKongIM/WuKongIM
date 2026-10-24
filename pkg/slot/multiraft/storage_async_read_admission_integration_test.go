//go:build integration

package multiraft

import (
	"context"
	"testing"
	"time"

	"go.etcd.io/raft/v3/raftpb"
)

// Failure matrix: a remote durable majority may advance commit while the
// leader's next Save is held. An established durable current-term prefix must
// still admit a fresh read, while its newer read index waits for durable apply.
// One remote ACK is the quorum control. Neither early read/proposal success nor
// bypassing the pre-current-term/pending-count fences is an acceptable repair.
// Only public Runtime, Storage, Transport and Future ports drive this scenario.
func TestRuntimeReadBarrierWaitsForCommittedEntryAwaitingLocalSave(t *testing.T) {
	store := newStorageFenceGate()
	fsm := &storageAsyncFSM{internalFakeStateMachine: &internalFakeStateMachine{}, store: store}
	transport := &storageTermTransport{sent: make(chan raftpb.Message, 128)}
	rt, err := New(Options{
		NodeID: 1, Workers: 1, TickInterval: 10 * time.Millisecond, Transport: transport,
		Raft: RaftOptions{ElectionTick: 10, HeartbeatTick: 1, PreVote: false, CheckQuorum: false},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	var readDone chan error
	readFinished := false
	t.Cleanup(func() {
		store.release()
		cancel()
		if readDone != nil && !readFinished {
			select {
			case <-readDone:
			case <-time.After(time.Second):
				t.Error("ReadBarrier did not join after cancellation")
			}
		}
		if err := rt.Close(); err != nil {
			t.Errorf("Runtime.Close: %v", err)
		}
		t.Log("read admission cleanup: held Save released, read canceled/joined and Runtime joined")
	})
	const slotID SlotID = 326
	if err := rt.BootstrapSlot(ctx, BootstrapSlotRequest{
		Slot: SlotOptions{ID: slotID, Storage: store, StateMachine: fsm}, Voters: []NodeID{1, 2, 3},
	}); err != nil {
		t.Fatal(err)
	}
	var baseline Status
	storageAsyncWait(t, ctx, func() bool {
		status, err := rt.Status(slotID)
		if err != nil || status.CommitIndex == 0 || status.AppliedIndex != status.CommitIndex || len(status.CurrentVoters) != 3 {
			return false
		}
		baseline = status
		return true
	})
	entries, err := store.Entries(ctx, 1, baseline.CommitIndex+1, ^uint64(0))
	if err != nil || len(entries) != int(baseline.CommitIndex) {
		t.Fatalf("initial peer history: entries=%d commit=%d err=%v", len(entries), baseline.CommitIndex, err)
	}
	peers := make(map[uint64]*storageTermPeer, 2)
	for _, id := range []uint64{2, 3} {
		peer := &storageTermPeer{id: id, term: baseline.Term, commit: baseline.CommitIndex, terms: map[uint64]uint64{0: 0}}
		for _, entry := range entries {
			peer.terms[entry.Index] = entry.Term
			peer.last = entry.Index
		}
		peers[id] = peer
	}
	holdPeer3 := false
	var heldPeer3 *raftpb.Message
	var peer2Ack uint64
	readHeartbeat := false
	drive := func(message raftpb.Message) {
		t.Helper()
		peer := peers[message.To]
		if peer == nil || message.From != 1 {
			t.Fatalf("unexpected public protocol endpoints: from=%d to=%d", message.From, message.To)
		}
		if message.Type == raftpb.MsgHeartbeat && len(message.Context) > 0 {
			readHeartbeat = true
		}
		reply, ok := peer.reply(t, message)
		if !ok {
			return
		}
		if reply.Type == raftpb.MsgAppResp && !reply.Reject {
			if reply.From == 2 {
				peer2Ack = max(peer2Ack, reply.Index)
			}
			if reply.From == 3 && holdPeer3 {
				if heldPeer3 == nil || reply.Index >= heldPeer3.Index {
					copy := reply
					heldPeer3 = &copy
				}
				return
			}
		}
		if err := rt.Step(ctx, Envelope{SlotID: slotID, Message: reply}); err != nil {
			t.Fatal(err)
		}
	}
	until := func(label string, done func() bool) {
		t.Helper()
		tick := time.NewTicker(time.Millisecond)
		defer tick.Stop()
		for !done() {
			select {
			case message := <-transport.sent:
				drive(message)
			case <-tick.C:
			case <-ctx.Done():
				t.Fatalf("%s did not complete through public protocol: %v", label, ctx.Err())
			}
		}
	}
	until("durable current-term leader baseline", func() bool {
		status, err := rt.Status(slotID)
		if err != nil || status.Role != RoleLeader || status.CommitIndex == 0 || status.AppliedIndex != status.CommitIndex || peers[2].commit < status.CommitIndex || peers[3].commit < status.CommitIndex {
			return false
		}
		state, stateErr := store.InitialState(ctx)
		term, termErr := store.Term(ctx, status.CommitIndex)
		if stateErr != nil || termErr != nil || state.HardState.Commit != status.CommitIndex || state.AppliedIndex != status.CommitIndex || term != status.Term {
			return false
		}
		baseline = status
		return true
	})
	target := baseline.CommitIndex + 1
	store.target.Store(target)
	store.armed.Store(true)
	holdPeer3 = true
	future, err := rt.Propose(ctx, slotID, proposalString("read-admission-held-entry"))
	if err != nil {
		t.Fatal(err)
	}
	proposalDone := storageTermObserve(t, future)
	storageFenceEntered(t, ctx, store.entered)
	until("one remote ACK and one held remote ACK", func() bool {
		return peer2Ack >= target && heldPeer3 != nil && heldPeer3.Index >= target
	})
	status, err := rt.FreshStatus(ctx, slotID)
	if err != nil || status.Role != RoleLeader || status.Term != baseline.Term || status.CommitIndex != baseline.CommitIndex {
		t.Fatalf("one remote ACK advanced commit without a durable majority: term=%d commit=%d err=%v", status.Term, status.CommitIndex, err)
	}
	holdPeer3 = false
	if err := rt.Step(ctx, Envelope{SlotID: slotID, Message: *heldPeer3}); err != nil {
		t.Fatal(err)
	}
	until("remote-only durable majority commit", func() bool {
		status, err := rt.Status(slotID)
		return err == nil && status.Role == RoleLeader && status.Term == baseline.Term && status.CommitIndex == target
	})
	assertHeld := func() {
		t.Helper()
		last, lastErr := store.LastIndex(ctx)
		state, stateErr := store.InitialState(ctx)
		if lastErr != nil || stateErr != nil || last != baseline.CommitIndex || state.HardState.Commit != baseline.CommitIndex || state.AppliedIndex != baseline.AppliedIndex || store.durable.Load() || fsm.earlyApply.Load() {
			t.Fatalf("held Save durability boundary changed: last=%d commit=%d applied=%d lastErr=%v stateErr=%v", last, state.HardState.Commit, state.AppliedIndex, lastErr, stateErr)
		}
		select {
		case result := <-proposalDone:
			t.Fatalf("proposal completed before local durability: index=%d term=%d err=%v", result.result.Index, result.result.Term, result.err)
		default:
		}
	}
	assertHeld()
	t.Log("read admission setup: durable current-term prefix; two actual remote durable ACKs advanced public commit beyond held local Save")

	readDone = make(chan error, 1)
	go func() { readDone <- rt.ReadBarrier(ctx, slotID) }()
	until("fresh quorum read while target Save remains held", func() bool {
		select {
		case err := <-readDone:
			readFinished = true
			t.Fatalf("ReadBarrier returned before fresh quorum and durable apply despite established current-term prefix: %v", err)
		default:
		}
		return readHeartbeat
	})
	// Two sequential controls cross an owner pass after the real heartbeat
	// response was admitted, even if the first pass already drained requests.
	// The accepted read must still wait for its own index's durable application.
	for range 2 {
		if _, err := rt.FreshStatus(ctx, slotID); err != nil {
			t.Fatal(err)
		}
	}
	assertHeld()
	select {
	case err := <-readDone:
		readFinished = true
		t.Fatalf("fresh read completed before its committed index became durable-applied: %v", err)
	default:
	}
	store.release()
	var readErr error
	until("same read after durable apply", func() bool {
		select {
		case readErr = <-readDone:
			readFinished = true
			return true
		default:
			return false
		}
	})
	if readErr != nil {
		t.Fatalf("same read failed after Save release: %v", readErr)
	}
	result, err := future.Wait(ctx)
	if err != nil || result.Index != target || result.Term != baseline.Term {
		t.Fatalf("proposal after release: index=%d term=%d err=%v", result.Index, result.Term, err)
	}
	state, err := store.InitialState(ctx)
	if err != nil || state.HardState.Commit < target || state.AppliedIndex < target || !store.durable.Load() || fsm.earlyApply.Load() || transport.overflow.Load() {
		t.Fatalf("read completion lacked durable apply: commit=%d applied=%d err=%v", state.HardState.Commit, state.AppliedIndex, err)
	}
}
