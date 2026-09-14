package multiraft

import (
	"context"
	"errors"
	"testing"

	raft "go.etcd.io/raft/v3"
	"go.etcd.io/raft/v3/raftpb"
)

func TestReadBarrierWaitsForDurableApplyAndRejectsTermChange(t *testing.T) {
	request := &readBarrierRequest{ctx: context.Background(), resp: make(chan error, 1), term: 3}
	g := &slot{status: Status{Role: RoleLeader, Term: 3}, durableAppliedIndex: 8, pendingReads: map[string]*readBarrierRequest{"read": request}}
	g.acceptReadStates([]raft.ReadState{{Index: 9, RequestCtx: []byte("read")}})
	select {
	case <-request.resp:
		t.Fatal("read completed before durable apply")
	default:
	}
	g.setDurableAppliedIndex(9)
	if err := <-request.resp; err != nil {
		t.Fatal(err)
	}
	if len(g.pendingReads) != 0 {
		t.Fatal("completed read retained")
	}
	request = &readBarrierRequest{ctx: context.Background(), resp: make(chan error, 1), term: 3}
	g.pendingReads["old"] = request
	g.status.Term = 4
	g.acceptReadStates([]raft.ReadState{{Index: 9, RequestCtx: []byte("old")}})
	if err := <-request.resp; !errors.Is(err, ErrNotLeader) {
		t.Fatalf("old-term read: %v", err)
	}
}

func TestReadBarrierCanceledUnconfirmedRequestsKeepRaftBudget(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	request := &readBarrierRequest{ctx: ctx, resp: make(chan error, 1), term: 3}
	g := &slot{status: Status{Role: RoleLeader, Term: 3}, durableAppliedIndex: 8, pendingReads: map[string]*readBarrierRequest{"read": request}}
	g.acceptReadStates(nil)
	if err := <-request.resp; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if len(g.pendingReads) != 1 {
		t.Fatal("unconfirmed canceled read escaped Raft budget")
	}
	g.acceptReadStates([]raft.ReadState{{Index: 9, RequestCtx: []byte("read")}})
	if len(g.pendingReads) != 0 {
		t.Fatal("confirmed canceled read retained")
	}
}

func TestReadBarrierTransientFailureKeepsUnconfirmedBudget(t *testing.T) {
	request := &readBarrierRequest{ctx: context.Background(), resp: make(chan error, 1), term: 3}
	g := &slot{pendingReads: map[string]*readBarrierRequest{"read": request}}
	failure := errors.New("transient storage failure")
	g.failPending(failure)
	if err := <-request.resp; !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if len(g.pendingReads) != 1 {
		t.Fatal("transient Ready error released live Raft read budget")
	}
	g.mu.Lock()
	g.closed = true
	g.mu.Unlock()
	g.failPending(ErrSlotClosed)
	if len(g.pendingReads) != 0 {
		t.Fatal("closed runtime retained pending reads")
	}
}

func TestReadBarrierRefusesLeaderBeforeCurrentTermCommit(t *testing.T) {
	memory := raft.NewMemoryStorage()
	if err := memory.ApplySnapshot(raftpb.Snapshot{Metadata: raftpb.SnapshotMetadata{Index: 1, Term: 1, ConfState: raftpb.ConfState{Voters: []uint64{1, 2, 3}}}}); err != nil {
		t.Fatal(err)
	}
	memory.SetHardState(raftpb.HardState{Term: 1, Commit: 1})
	raw, err := raft.NewRawNode(&raft.Config{ID: 1, ElectionTick: 10, HeartbeatTick: 1, Storage: memory, Applied: 1, MaxInflightMsgs: 256, ReadOnlyOption: raft.ReadOnlySafe})
	if err != nil {
		t.Fatal(err)
	}
	if err = raw.Campaign(); err != nil {
		t.Fatal(err)
	}
	// Persist this node's vote before delivering the second vote; RawNode
	// releases its own vote response only after the Ready is advanced.
	ready := raw.Ready()
	if err := memory.Append(ready.Entries); err != nil {
		t.Fatal(err)
	}
	if !raft.IsEmptyHardState(ready.HardState) {
		memory.SetHardState(ready.HardState)
	}
	raw.Advance(ready)
	term := raw.BasicStatus().Term
	if err = raw.Step(raftpb.Message{Type: raftpb.MsgVoteResp, From: 2, To: 1, Term: term}); err != nil {
		t.Fatal(err)
	}
	if raw.BasicStatus().RaftState != raft.StateLeader {
		t.Fatal("fixture is not leader")
	}
	g := &slot{rawNode: raw, storageView: &storageAdapter{memory: &loadedMemoryStorage{MemoryStorage: memory}}, status: Status{Role: RoleLeader, Term: term}}
	request := &readBarrierRequest{ctx: context.Background(), resp: make(chan error, 1)}
	g.issueReadBarrier(request)
	if err := <-request.resp; !errors.Is(err, ErrSlotBusy) {
		t.Fatalf("pre-current-term read=%v", err)
	}
	if len(g.pendingReads) != 0 {
		t.Fatal("precommit read entered uncancelable Raft queue")
	}
	// A taken control executed after close must complete without reinserting work.
	g.closed = true
	request = &readBarrierRequest{ctx: context.Background(), resp: make(chan error, 1)}
	g.issueReadBarrier(request)
	if err := <-request.resp; !errors.Is(err, ErrSlotClosed) {
		t.Fatalf("closed taken control=%v", err)
	}
	if len(g.pendingReads) != 0 {
		t.Fatal("taken read inserted after close")
	}
}
