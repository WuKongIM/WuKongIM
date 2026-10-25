//go:build integration

package multiraft

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.etcd.io/raft/v3/raftpb"
)

// Failure matrix precedes an async-storage implementation:
// a held ordinary append blocks a safe same-term, old-commit heartbeat;
// replication or apply acknowledges bytes before durable Save; a later append
// overtakes its predecessor; failed writes publish success; term, snapshot,
// configuration, compaction, Close/reopen or byte-credit fences are bypassed.
// This first public Runtime probe establishes progress and durability together.
// Safety deadlines bound test cleanup; they are not performance thresholds.
func TestRuntimeHeartbeatProgressesWhileFollowerAppendSaveIsBlocked(t *testing.T) {
	store := &storageAsyncGate{
		internalFakeStorage: &internalFakeStorage{},
		entered:             make(chan struct{}),
		releaseCh:           make(chan struct{}),
	}
	transport := &storageAsyncTransport{store: store, sent: make(chan storageAsyncWitness, 64)}
	fsm := &storageAsyncFSM{internalFakeStateMachine: &internalFakeStateMachine{}, store: store}
	rt, err := New(Options{
		NodeID: 1, Workers: 1, TickInterval: time.Hour, Transport: transport,
		Raft: RaftOptions{ElectionTick: 10, HeartbeatTick: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Always release the physical write before joining the Runtime, including RED.
	t.Cleanup(func() {
		store.release()
		if err := rt.Close(); err != nil {
			t.Errorf("Runtime.Close: %v", err)
		}
		t.Log("storage_async cleanup: write released and Runtime joined")
	})
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	const slotID SlotID = 312
	if err := rt.BootstrapSlot(ctx, BootstrapSlotRequest{
		Slot:   SlotOptions{ID: slotID, Storage: store, StateMachine: fsm},
		Voters: []NodeID{1, 2, 3},
	}); err != nil {
		t.Fatal(err)
	}
	var baseline Status
	storageAsyncWait(t, ctx, func() bool {
		status, err := rt.Status(slotID)
		if err != nil || status.Role != RoleFollower || status.Term == 0 || status.CommitIndex == 0 || status.AppliedIndex != status.CommitIndex || len(status.CurrentVoters) != 3 {
			return false
		}
		baseline = status
		return true
	})
	const baselineContext = "storage-async-baseline"
	if err := rt.Step(ctx, Envelope{SlotID: slotID, Message: raftpb.Message{
		Type: raftpb.MsgHeartbeat, From: 2, To: 1, Term: baseline.Term,
		Commit: baseline.CommitIndex, Context: []byte(baselineContext),
	}}); err != nil {
		t.Fatal(err)
	}
	storageAsyncAwait(t, ctx, transport.sent, func(w storageAsyncWitness) bool {
		return w.kind == raftpb.MsgHeartbeatResp && w.context == baselineContext && w.term == baseline.Term
	})
	previousTerm, err := store.Term(ctx, baseline.CommitIndex)
	if err != nil || previousTerm == 0 {
		t.Fatalf("baseline durable term: %d, %v", previousTerm, err)
	}
	t.Log("storage_async setup: three-voter follower and same-term baseline heartbeat confirmed")

	target := baseline.CommitIndex + 1
	store.target.Store(target)
	store.armed.Store(true)
	if err := rt.Step(ctx, Envelope{SlotID: slotID, Message: raftpb.Message{
		Type: raftpb.MsgApp, From: 2, To: 1, Term: baseline.Term,
		Index: baseline.CommitIndex, LogTerm: previousTerm, Commit: baseline.CommitIndex,
		Entries: []raftpb.Entry{{Index: target, Term: baseline.Term, Type: raftpb.EntryNormal, Data: proposalString("held-follower-entry")}},
	}}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-store.entered:
	case <-ctx.Done():
		t.Fatal("ordinary MsgApp did not reach controlled pre-durable Save")
	}
	assertUndurable := func() {
		t.Helper()
		last, err := store.LastIndex(ctx)
		if err != nil || last != baseline.CommitIndex {
			t.Fatalf("held append changed durable last index: got %d, want %d, err %v", last, baseline.CommitIndex, err)
		}
		state, err := store.InitialState(ctx)
		if err != nil || state.HardState.Commit != baseline.CommitIndex || state.AppliedIndex != baseline.AppliedIndex {
			t.Fatalf("held append advanced durable commit/apply: %+v, %v", state, err)
		}
		if transport.earlyAppResp.Load() || fsm.earlyApply.Load() {
			t.Fatal("append response or FSM apply escaped the durable Save fence")
		}
	}
	assertUndurable()
	t.Log("storage_async setup: entry Save held before durability; AppResp/commit/apply fence intact")

	const probeContext = "storage-async-old-commit-probe"
	if err := rt.Step(ctx, Envelope{SlotID: slotID, Message: raftpb.Message{
		Type: raftpb.MsgHeartbeat, From: 2, To: 1, Term: baseline.Term,
		Commit: baseline.CommitIndex, Context: []byte(probeContext),
	}}); err != nil {
		t.Fatal(err)
	}
	probeTimer := time.NewTimer(2 * time.Second)
	defer probeTimer.Stop()
	responded := false
probe:
	for {
		select {
		case witness := <-transport.sent:
			if witness.kind == raftpb.MsgHeartbeatResp && witness.context == probeContext && witness.term == baseline.Term {
				responded = true
				break probe
			}
		case <-probeTimer.C:
			break probe
		case <-ctx.Done():
			t.Fatal("test deadline expired before heartbeat progress check")
		}
	}
	assertUndurable()
	if !responded {
		t.Error("same-term old-commit heartbeat response did not progress before unrelated append Save release")
	}

	store.release()
	storageAsyncAwait(t, ctx, transport.sent, func(w storageAsyncWitness) bool {
		return w.kind == raftpb.MsgAppResp && !w.reject && w.index >= target && w.term == baseline.Term
	})
	if !store.durable.Load() || transport.earlyAppResp.Load() || fsm.earlyApply.Load() {
		t.Fatal("append acknowledgement was not fenced by completed durable Save")
	}
	if err := rt.Step(ctx, Envelope{SlotID: slotID, Message: raftpb.Message{
		Type: raftpb.MsgHeartbeat, From: 2, To: 1, Term: baseline.Term,
		Commit: target, Context: []byte("storage-async-commit"),
	}}); err != nil {
		t.Fatal(err)
	}
	storageAsyncWait(t, ctx, func() bool {
		status, err := rt.Status(slotID)
		return err == nil && status.CommitIndex == target && status.AppliedIndex == target
	})
	if err := rt.CloseSlot(ctx, slotID); err != nil {
		t.Fatal(err)
	}
	t.Log("storage_async completion: durable append response, committed apply and CloseSlot joined")
}

// storageAsyncGate blocks exactly one target ordinary append before persistence.
// Ownership is retained until release; inspection uses only the Storage port.
type storageAsyncGate struct {
	*internalFakeStorage
	entered, releaseCh chan struct{}
	releaseOnce        sync.Once
	armed, gated       atomic.Bool
	durable            atomic.Bool
	target             atomic.Uint64
}

func (s *storageAsyncGate) release() { s.releaseOnce.Do(func() { close(s.releaseCh) }) }

func (s *storageAsyncGate) Save(ctx context.Context, state PersistentState) error {
	match := false
	for _, entry := range state.Entries {
		match = match || entry.Index == s.target.Load()
	}
	if s.armed.Load() && match && s.gated.CompareAndSwap(false, true) {
		close(s.entered)
		select {
		case <-s.releaseCh:
		case <-ctx.Done():
			return ctx.Err()
		}
		if err := s.internalFakeStorage.Save(ctx, state); err != nil {
			return err
		}
		s.durable.Store(true)
		return nil
	}
	return s.internalFakeStorage.Save(ctx, state)
}

type storageAsyncWitness struct {
	kind    raftpb.MessageType
	index   uint64
	term    uint64
	reject  bool
	context string
}

// storageAsyncTransport retains only bounded immutable protocol witnesses.
type storageAsyncTransport struct {
	store        *storageAsyncGate
	sent         chan storageAsyncWitness
	earlyAppResp atomic.Bool
}

func (p *storageAsyncTransport) Send(_ context.Context, batch []Envelope) error {
	for _, env := range batch {
		m := env.Message
		if m.Type == raftpb.MsgAppResp && !m.Reject && p.store.armed.Load() && m.Index >= p.store.target.Load() && !p.store.durable.Load() {
			p.earlyAppResp.Store(true)
		}
		select {
		case p.sent <- storageAsyncWitness{m.Type, m.Index, m.Term, m.Reject, string(m.Context)}:
		default:
			return errors.New("controlled protocol witness capacity exceeded")
		}
	}
	return nil
}

type storageAsyncFSM struct {
	*internalFakeStateMachine
	store      *storageAsyncGate
	earlyApply atomic.Bool
}

func (s *storageAsyncFSM) Apply(ctx context.Context, cmd Command) ([]byte, error) {
	if s.store.armed.Load() && cmd.Index >= s.store.target.Load() && !s.store.durable.Load() {
		s.earlyApply.Store(true)
	}
	return s.internalFakeStateMachine.Apply(ctx, cmd)
}

func storageAsyncAwait(t *testing.T, ctx context.Context, events <-chan storageAsyncWitness, predicate func(storageAsyncWitness) bool) {
	t.Helper()
	for {
		select {
		case event := <-events:
			if predicate(event) {
				return
			}
		case <-ctx.Done():
			t.Fatal("expected public protocol witness did not arrive")
		}
	}
}

func storageAsyncWait(t *testing.T, ctx context.Context, predicate func() bool) {
	t.Helper()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for !predicate() {
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatal("expected public Runtime status did not arrive")
		}
	}
}
