//go:build integration

// Approved seams: Runtime Propose/Step/ReadBarrier/Future and Storage/Transport ports.
package multiraft

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.etcd.io/raft/v3/raftpb"
)

// This distinct protocol variant proves whole HardState folding and the exact
// durable-apply prefix. The entries-only primary intentionally withholds all
// remote ACKs and cannot establish this contract. It reuses only the public
// Storage gate and external protocol witnesses from the primary protocol fixture.
func TestRuntimeStorageCoalescesCompleteHardStateWithoutCrossingApplyPrefix(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	a := &coalescingSaveGate{internalFakeStorage: &internalFakeStorage{}}
	for i := range a.entered {
		a.entered[i] = make(chan struct{})
		a.release[i] = make(chan struct{})
	}
	b := &internalFakeStorage{}
	fsm := &coalescingApplyGate{internalFakeStateMachine: &internalFakeStateMachine{}, store: a, entered: make(chan struct{}), release: make(chan struct{})}
	observer := newBlockingSchedulerInflightObserver()
	var releaseObserverOnce sync.Once
	releaseObserver := func() { releaseObserverOnce.Do(func() { close(observer.release) }) }
	transport := &storageCountTransport{sent: make(chan Envelope, 256)}
	rt, err := New(Options{NodeID: 1, Workers: 1, TickInterval: time.Hour, ReadBarrierTracing: true, Transport: transport, Observer: observer,
		Raft: RaftOptions{ElectionTick: 10, HeartbeatTick: 1, PreVote: false, CheckQuorum: false}})
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		releaseObserver()
		fsm.unblock()
		a.unblock(0)
		a.unblock(1)
		cancel()
		if e := rt.Close(); e != nil {
			t.Error(e)
		}
	})
	const slotA SlotID = 601
	const slotB SlotID = 602
	stores := map[SlotID]Storage{slotA: a, slotB: b}
	peers := map[SlotID]map[uint64]*storageTermPeer{}
	for _, id := range []SlotID{slotA, slotB} {
		var machine StateMachine = &internalFakeStateMachine{}
		if id == slotA {
			machine = fsm
		}
		if e := rt.BootstrapSlot(ctx, BootstrapSlotRequest{Slot: SlotOptions{ID: id, Storage: stores[id], StateMachine: machine}, Voters: []NodeID{1, 2, 3}, Campaign: true}); e != nil {
			t.Fatal(e)
		}
		var initial Status
		storageAsyncWait(t, ctx, func() bool {
			s, e := rt.Status(id)
			initial = s
			return e == nil && s.CommitIndex > 0 && s.AppliedIndex == s.CommitIndex
		})
		entries, e := stores[id].Entries(ctx, 1, initial.CommitIndex+1, ^uint64(0))
		if e != nil {
			t.Fatal(e)
		}
		peers[id] = map[uint64]*storageTermPeer{}
		for _, pid := range []uint64{2, 3} {
			p := &storageTermPeer{id: pid, term: initial.Term, commit: initial.CommitIndex, terms: map[uint64]uint64{0: 0}}
			for _, entry := range entries {
				p.terms[entry.Index] = entry.Term
				p.last = entry.Index
			}
			peers[id][pid] = p
		}
	}
	holdA := false
	held := map[uint64]raftpb.Message{}
	seedReplies := map[uint64]raftpb.Message{}
	var seedIndex uint64
	var aSeen uint64
	var bFresh bool
	drive := func(env Envelope) {
		t.Helper()
		m := env.Message
		p := peers[env.SlotID][m.To]
		if p == nil || m.From != 1 {
			t.Fatalf("unexpected protocol endpoint slot=%d from=%d to=%d", env.SlotID, m.From, m.To)
		}
		if env.SlotID == slotA && m.To == 2 {
			for _, entry := range m.Entries {
				aSeen = max(aSeen, entry.Index)
			}
		}
		if env.SlotID == slotB && m.Type == raftpb.MsgHeartbeat && len(m.Context) > 0 {
			bFresh = true
		}
		reply, ok := p.reply(t, m)
		if !ok {
			return
		}
		if holdA && env.SlotID == slotA && reply.Type == raftpb.MsgAppResp {
			if seedIndex != 0 && reply.Index == seedIndex && !reply.Reject {
				seedReplies[reply.From] = reply
			}
			held[reply.From] = reply
			return
		}
		if e := rt.Step(ctx, Envelope{SlotID: env.SlotID, Message: reply}); e != nil {
			t.Fatal(e)
		}
	}
	until := func(label string, done func() bool) {
		t.Helper()
		tick := time.NewTicker(time.Millisecond)
		defer tick.Stop()
		for !done() {
			select {
			case env := <-transport.sent:
				drive(env)
			case <-tick.C:
			case <-ctx.Done():
				t.Fatalf("%s: %v", label, ctx.Err())
			}
		}
	}
	baseline := map[SlotID]Status{}
	until("two current-term durable baselines", func() bool {
		for _, id := range []SlotID{slotA, slotB} {
			s, e := rt.Status(id)
			if e != nil || s.Role != RoleLeader || s.AppliedIndex != s.CommitIndex || peers[id][2].commit < s.CommitIndex || peers[id][3].commit < s.CommitIndex {
				return false
			}
			term, e := stores[id].Term(ctx, s.CommitIndex)
			if e != nil || term != s.Term {
				return false
			}
			baseline[id] = s
		}
		return true
	})
	for _, id := range []SlotID{slotA, slotB} {
		for i := 0; i < 2; i++ {
			if _, e := rt.FreshStatus(ctx, id); e != nil {
				t.Fatal(e)
			}
		}
	}

	generation := slotFor(rt, slotA)
	holdA = true
	seedIndex = baseline[slotA].CommitIndex + 1
	fsm.target.Store(seedIndex)
	seed, err := rt.Propose(ctx, slotA, proposalString("stable-uncommitted-X"))
	if err != nil {
		t.Fatal(err)
	}
	until("X stable locally with both actual peer ACKs held", func() bool {
		last, e := a.LastIndex(ctx)
		return e == nil && last == seedIndex && len(seedReplies) == 2
	})
	for pass := 0; pass < 2; pass++ {
		if _, e := rt.FreshStatus(ctx, slotA); e != nil {
			t.Fatal(e)
		}
	}
	beforeX, e := a.InitialState(ctx)
	if e != nil || beforeX.HardState.Commit != baseline[slotA].CommitIndex || beforeX.AppliedIndex != baseline[slotA].AppliedIndex {
		t.Fatalf("X must remain stable but uncommitted: commit=%d applied=%d err=%v", beforeX.HardState.Commit, beforeX.AppliedIndex, e)
	}
	probe, stop := context.WithTimeout(ctx, 30*time.Millisecond)
	_, e = seed.Wait(probe)
	stop()
	if !errors.Is(e, context.DeadlineExceeded) {
		t.Fatalf("uncommitted X completed: %v", e)
	}
	a.target.Store(seedIndex + 1)
	futures := make([]Future, 0, 3)
	for i := 1; i <= 2; i++ {
		f, e := rt.Propose(ctx, slotA, proposalString(fmt.Sprintf("count-%d", i)))
		if e != nil {
			t.Fatal(e)
		}
		futures = append(futures, f)
		target := seedIndex + uint64(i)
		until("A1 held and distinct A2 Ready", func() bool { return aSeen >= target })
		if i == 1 {
			storageFenceEntered(t, ctx, a.entered[0])
			if a.firstCount != 1 {
				t.Fatalf("first physical Save setup: entries=%d want1", a.firstCount)
			}
		}
	}
	storageFenceEntered(t, ctx, a.entered[0])
	// These are the actual responses previously produced after the two peers
	// durably accepted X. Replaying a previously held network reply does not
	// invent a vote, storage response, or a higher accepted index.
	until("both real remote X responses retained", func() bool { return len(seedReplies) == 2 })
	for _, pid := range []uint64{2, 3} {
		if err := rt.Step(ctx, Envelope{SlotID: slotA, Message: seedReplies[pid]}); err != nil {
			t.Fatal(err)
		}
	}
	for pass := 0; pass < 2; pass++ {
		if _, err := rt.FreshStatus(ctx, slotA); err != nil {
			t.Fatal(err)
		}
	}
	committed, err := rt.FreshStatus(ctx, slotA)
	if err != nil || committed.CommitIndex != seedIndex {
		t.Fatalf("quorum commit=%d want=%d err=%v", committed.CommitIndex, seedIndex, err)
	}
	durableBefore, err := a.InitialState(ctx)
	if err != nil || durableBefore.HardState.Commit != baseline[slotA].CommitIndex {
		t.Fatalf("held first Save unexpectedly persisted commit=%d err=%v", durableBefore.HardState.Commit, err)
	}
	// X is already stable, so this real quorum produces an HS/Apply(X)
	// Ready behind A2. A3 is the fourth owner; no cap is increased.
	third, err := rt.Propose(ctx, slotA, proposalString("count-3"))
	if err != nil {
		t.Fatal(err)
	}
	futures = append(futures, third)
	until("fourth owner A3 actual outbound entry", func() bool { return aSeen >= seedIndex+3 })
	if _, err := rt.FreshStatus(ctx, slotB); err != nil {
		t.Fatal(err)
	}
	read := make(chan error, 1)
	go func() { read <- rt.ReadBarrier(ctx, slotB) }()
	var readErr error
	until("unrelated fresh quorum during HS prefix setup", func() bool {
		select {
		case readErr = <-read:
			return true
		default:
			return false
		}
	})
	if readErr != nil || !bFresh {
		t.Fatalf("unrelated fresh quorum observed=%v err=%v", bFresh, readErr)
	}
	t.Log("setup reached: X stable/uncommitted; A1 held; distinct A2; actual two-peer X quorum; HS/Apply prefix; A3 outbound")
	a.unblock(0)
	storageFenceEntered(t, ctx, a.entered[1])
	expectedHS := durableBefore.HardState
	expectedHS.Commit = seedIndex
	if a.secondCount != 2 || len(a.second) != 2 {
		// The unmodified FIFO reports one entry. Prove the queued intermediate
		// HS-only work at the actual Storage boundary before reporting RED;
		// owner controls alone cannot establish how a Ready was constructed.
		observed := a.secondCount
		a.unblock(1)
		storageAsyncWait(t, ctx, func() bool { return a.sawStandaloneHardState(expectedHS) })
		t.Log("baseline Storage witnessed the distinct complete HS-only member")
		t.Fatalf("compatible entries/HS/entries prefix was not coalesced: entries=%d want2", observed)
	}
	if a.secondHS == nil || *a.secondHS != expectedHS {
		if a.secondHS == nil {
			t.Fatal("combined Save omitted whole HardState")
		}
		t.Fatalf("combined HardState term=%d vote=%d commit=%d wantterm=%d wantvote=%d wantcommit=%d", a.secondHS.Term, a.secondHS.Vote, a.secondHS.Commit, expectedHS.Term, expectedHS.Vote, expectedHS.Commit)
	}
	for i, e := range a.second {
		if e.index != seedIndex+uint64(i+2) || e.term != baseline[slotA].Term || e.digest != sha256.Sum256(proposalString(fmt.Sprintf("count-%d", i+2))) {
			t.Fatalf("combined entry %d index=%d term=%d digest=%x", i, e.index, e.term, e.digest)
		}
	}
	pending := append([]Future{seed}, futures...)
	for i, f := range pending {
		probe, stop := context.WithTimeout(ctx, 30*time.Millisecond)
		_, e := f.Wait(probe)
		stop()
		if !errors.Is(e, context.DeadlineExceeded) {
			t.Fatalf("Future before combined HS durable %d: %v", i, e)
		}
	}
	storageAsyncWait(t, ctx, func() bool {
		b := rt.storage.budget
		b.mu.Lock()
		defer b.mu.Unlock()
		return b.slots[generation].count <= 3
	})
	coalescingAssertUsage(t, rt, generation, 3, 3)
	a.unblock(1)
	storageFenceEntered(t, ctx, fsm.entered)
	// Other members are append-only. Once they retire, this original Ready's
	// Apply part must retain its complete lease while its physical FSM is held.
	storageAsyncWait(t, ctx, func() bool {
		b := rt.storage.budget
		b.mu.Lock()
		defer b.mu.Unlock()
		return b.slots[generation].count <= 1
	})
	coalescingAssertUsage(t, rt, generation, 1, 1)
	probe, stop = context.WithTimeout(ctx, 30*time.Millisecond)
	_, e = seed.Wait(probe)
	stop()
	if !errors.Is(e, context.DeadlineExceeded) {
		t.Fatalf("X completed before its Apply returned: %v", e)
	}
	during, e := a.InitialState(ctx)
	if e != nil || during.HardState != expectedHS || during.AppliedIndex != baseline[slotA].AppliedIndex {
		t.Fatalf("held Apply durable boundary commit=%d applied=%d err=%v", during.HardState.Commit, during.AppliedIndex, e)
	}
	fsm.unblock()
	first, err := seed.Wait(ctx)
	if err != nil || first.Index != seedIndex || first.Term != baseline[slotA].Term {
		t.Fatalf("committed prefix Future index=%d term=%d err=%v", first.Index, first.Term, err)
	}
	durablePrefix, err := a.InitialState(ctx)
	if err != nil || durablePrefix.HardState.Commit != expectedHS.Commit || durablePrefix.AppliedIndex != expectedHS.Commit {
		t.Fatalf("durable apply prefix commit=%d applied=%d err=%v", durablePrefix.HardState.Commit, durablePrefix.AppliedIndex, err)
	}
	for i, f := range futures {
		probe, stop := context.WithTimeout(ctx, 30*time.Millisecond)
		_, e := f.Wait(probe)
		stop()
		if !errors.Is(e, context.DeadlineExceeded) {
			t.Fatalf("uncommitted suffix Future %d completed: %v", i+1, e)
		}
	}
	holdA = false
	for _, m := range held {
		if err := rt.Step(ctx, Envelope{SlotID: slotA, Message: m}); err != nil {
			t.Fatal(err)
		}
	}
	for i, f := range futures {
		got, e := f.Wait(ctx)
		if e != nil || got.Index != seedIndex+uint64(i+1) || got.Term != baseline[slotA].Term {
			t.Fatalf("final Future %d index=%d term=%d err=%v", i, got.Index, got.Term, e)
		}
	}
	state, err := a.InitialState(ctx)
	if err != nil || state.AppliedIndex != seedIndex+3 {
		t.Fatalf("final applied=%d err=%v", state.AppliedIndex, err)
	}
	if transport.overflow.Load() {
		t.Fatal("bounded protocol witness overflow")
	}
	if err := rt.Close(); err != nil {
		t.Fatal(err)
	}
	coalescingAssertAllRetired(t, rt)
}

// The real FSM boundary retains the Apply part after the combined physical Save
// returned. InitialState inside Apply guards its complete durable HS prefix.
type coalescingApplyGate struct {
	*internalFakeStateMachine
	store            *coalescingSaveGate
	target           atomic.Uint64
	entered, release chan struct{}
	once             sync.Once
}

func (s *coalescingApplyGate) unblock() { s.once.Do(func() { close(s.release) }) }
func (s *coalescingApplyGate) Apply(ctx context.Context, cmd Command) ([]byte, error) {
	if cmd.Index == s.target.Load() {
		state, err := s.store.InitialState(ctx)
		if err != nil {
			return nil, err
		}
		if state.HardState.Commit < cmd.Index {
			return nil, errors.New("Apply crossed undurable HardState prefix")
		}
		close(s.entered)
		select {
		case <-s.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return s.internalFakeStateMachine.Apply(ctx, cmd)
}
