package multiraft

import (
	"context"
	"errors"
	"testing"

	raft "go.etcd.io/raft/v3"
	"go.etcd.io/raft/v3/raftpb"
)

// Failure cases: queued readers amplify quorum heartbeats; a new batch borrows
// an earlier proof; canceled readers cancel siblings or escape the pending bound;
// confirmation completes before durable apply; term changes retain old readers.
func TestReadBarrierBatchSharesOnlyFreshQuorumConfirmation(t *testing.T) {
	g := newReadBarrierBatchLeader(t)
	const readers = 32
	requests := make([]*readBarrierRequest, readers)
	for i := range requests {
		requests[i] = &readBarrierRequest{ctx: context.Background(), resp: make(chan error, 1)}
		if err := g.enqueueControl(controlAction{kind: controlReadBarrier, readBarrier: requests[i]}); err != nil {
			t.Fatal(err)
		}
	}
	g.processControls(context.Background())
	ready := g.rawNode.Ready()
	var heartbeats []raftpb.Message
	for _, m := range ready.Messages {
		if m.Type == raftpb.MsgHeartbeat && len(m.Context) > 0 {
			heartbeats = append(heartbeats, m)
		}
	}
	if len(heartbeats) != 2 {
		t.Fatalf("%d queued readers emitted %d quorum heartbeats, want one round to two peers", readers, len(heartbeats))
	}
	g.rawNode.Advance(ready)
	// A later batch must issue its own proof even while the earlier one waits.
	later := &readBarrierRequest{ctx: context.Background(), resp: make(chan error, 1)}
	g.enqueueControl(controlAction{kind: controlReadBarrier, readBarrier: later})
	g.processControls(context.Background())
	laterReady := g.rawNode.Ready()
	var laterKey []byte
	for _, m := range laterReady.Messages {
		if m.Type == raftpb.MsgHeartbeat && len(m.Context) > 0 {
			laterKey = m.Context
		}
	}
	if len(laterKey) == 0 || string(laterKey) == string(heartbeats[0].Context) {
		t.Fatal("later batch reused an earlier quorum proof")
	}
	g.rawNode.Advance(laterReady)
	if err := g.rawNode.Step(raftpb.Message{Type: raftpb.MsgHeartbeatResp, From: 2, To: 1, Term: g.status.Term, Context: heartbeats[0].Context}); err != nil {
		t.Fatal(err)
	}
	confirmed := g.rawNode.Ready()
	g.durableAppliedIndex = g.rawNode.BasicStatus().Commit - 1
	g.acceptReadStates(confirmed.ReadStates)
	for _, request := range requests {
		select {
		case <-request.resp:
			t.Fatal("quorum-confirmed reader completed before durable apply")
		default:
		}
	}
	g.setDurableAppliedIndex(g.rawNode.BasicStatus().Commit)
	for _, request := range requests {
		select {
		case err := <-request.resp:
			if err != nil {
				t.Fatal(err)
			}
		default:
			t.Fatal("batch sibling did not receive quorum confirmation")
		}
	}
	select {
	case <-later.resp:
		t.Fatal("later reader completed from earlier batch confirmation")
	default:
	}
	g.status.Term++
	g.acceptReadStates(nil)
	if err := <-later.resp; !errors.Is(err, ErrNotLeader) {
		t.Fatalf("old-term batch reader: %v", err)
	}
	if len(g.pendingReads) != 0 {
		t.Fatal("completed batches retained pending readers")
	}
}

func TestReadBarrierBatchCancellationKeepsEachCallerBounded(t *testing.T) {
	g := newReadBarrierBatchLeader(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	requests := make([]*readBarrierRequest, maxPendingReadBarriers+1)
	for i := range requests {
		requests[i] = &readBarrierRequest{ctx: context.Background(), resp: make(chan error, 1)}
		if i == 0 {
			requests[i].ctx = ctx
		}
		g.enqueueControl(controlAction{kind: controlReadBarrier, readBarrier: requests[i]})
	}
	g.processControls(context.Background())
	if err := <-requests[maxPendingReadBarriers].resp; !errors.Is(err, ErrSlotBusy) {
		t.Fatalf("overflow reader: %v", err)
	}
	cancel()
	g.acceptReadStates(nil)
	if err := <-requests[0].resp; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled reader: %v", err)
	}
	if len(g.pendingReads) != maxPendingReadBarriers {
		t.Fatal("unconfirmed cancellation escaped the per-caller pending bound")
	}
	ready := g.rawNode.Ready()
	var key []byte
	for _, m := range ready.Messages {
		if m.Type == raftpb.MsgHeartbeat && len(m.Context) > 0 {
			key = m.Context
		}
	}
	g.acceptReadStates([]raft.ReadState{{Index: g.durableAppliedIndex, RequestCtx: key}})
	for _, request := range requests[1:maxPendingReadBarriers] {
		select {
		case err := <-request.resp:
			if err != nil {
				t.Fatal(err)
			}
		default:
			t.Fatal("canceled sibling prevented batch completion")
		}
	}
	if len(g.pendingReads) != 0 {
		t.Fatal("confirmed batch retained canceled ownership")
	}
}

// newReadBarrierBatchLeader establishes a real three-voter RawNode with a
// persisted current-term commit, excluding election messages from read evidence.
func newReadBarrierBatchLeader(t *testing.T) *slot {
	t.Helper()
	memory := raft.NewMemoryStorage()
	if err := memory.ApplySnapshot(raftpb.Snapshot{Metadata: raftpb.SnapshotMetadata{Index: 1, Term: 1, ConfState: raftpb.ConfState{Voters: []uint64{1, 2, 3}}}}); err != nil {
		t.Fatal(err)
	}
	memory.SetHardState(raftpb.HardState{Term: 1, Commit: 1})
	raw, err := raft.NewRawNode(&raft.Config{ID: 1, ElectionTick: 10, HeartbeatTick: 1, Storage: memory, Applied: 1, MaxInflightMsgs: 256, ReadOnlyOption: raft.ReadOnlySafe})
	if err != nil {
		t.Fatal(err)
	}
	persist := func() {
		t.Helper()
		ready := raw.Ready()
		if err := memory.Append(ready.Entries); err != nil {
			t.Fatal(err)
		}
		if !raft.IsEmptyHardState(ready.HardState) {
			memory.SetHardState(ready.HardState)
		}
		raw.Advance(ready)
	}
	if err := raw.Campaign(); err != nil {
		t.Fatal(err)
	}
	persist()
	term := raw.BasicStatus().Term
	if err := raw.Step(raftpb.Message{Type: raftpb.MsgVoteResp, From: 2, To: 1, Term: term}); err != nil {
		t.Fatal(err)
	}
	persist()
	if err := raw.Step(raftpb.Message{Type: raftpb.MsgAppResp, From: 2, To: 1, Term: term, Index: 2}); err != nil {
		t.Fatal(err)
	}
	persist()
	if raw.BasicStatus().Commit != 2 {
		t.Fatal("fixture has no current-term commit")
	}
	return &slot{rawNode: raw, storageView: &storageAdapter{memory: &loadedMemoryStorage{MemoryStorage: memory}}, status: Status{Role: RoleLeader, Term: term}, durableAppliedIndex: 2}
}
