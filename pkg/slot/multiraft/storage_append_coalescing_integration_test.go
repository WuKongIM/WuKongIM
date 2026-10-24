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

// The first behavioral RED must occur only after actual distinct MsgApp entries
// prove four Ready turns and the first physical Save has been held. The next
// physical Save must contain the already-queued three-entry suffix. No sleep or
// internal queue inspection establishes membership, and late A5 is submitted
// only after the second Save entrance to make the frozen-prefix boundary exact.
func TestRuntimeStorageCoalescesOnlyAlreadyQueuedOrdinaryPrefix(t *testing.T) {
	coalescingProtocolCase(t, "success")
}

// These safety cases must be evaluated only after the three-member group is
// positively observed. A missing group is setup failure for these selectors,
// not evidence about their fatal/close behavior.
func TestRuntimeStorageCoalescedPrefixTerminalFences(t *testing.T) {
	for _, mode := range []string{"group_error", "close_slot", "runtime_close"} {
		t.Run(mode, func(t *testing.T) { coalescingProtocolCase(t, mode) })
	}
}

func coalescingProtocolCase(t *testing.T, mode string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	a := &coalescingSaveGate{internalFakeStorage: &internalFakeStorage{}}
	if mode == "group_error" {
		a.failSecond = errors.New("controlled combined Save failure")
	}
	for i := range a.entered {
		a.entered[i] = make(chan struct{})
		a.release[i] = make(chan struct{})
	}
	b := &internalFakeStorage{}
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
		if e := rt.BootstrapSlot(ctx, BootstrapSlotRequest{Slot: SlotOptions{ID: id, Storage: stores[id], StateMachine: &internalFakeStateMachine{}}, Voters: []NodeID{1, 2, 3}, Campaign: true}); e != nil {
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
	a.target.Store(baseline[slotA].CommitIndex + 1)
	futures := make([]Future, 0, 5)
	for i := 1; i <= 4; i++ {
		f, e := rt.Propose(ctx, slotA, proposalString(fmt.Sprintf("count-%d", i)))
		if e != nil {
			t.Fatal(e)
		}
		futures = append(futures, f)
		target := baseline[slotA].CommitIndex + uint64(i)
		until("four distinct accepted Ready transport entries", func() bool { return aSeen >= target })
		if i == 1 {
			storageFenceEntered(t, ctx, a.entered[0])
			if a.firstCount != 1 {
				t.Fatalf("first physical Save setup: entries=%d want1", a.firstCount)
			}
		}
	}
	storageFenceEntered(t, ctx, a.entered[0])
	if _, err := rt.FreshStatus(ctx, slotB); err != nil {
		t.Fatal(err)
	}
	read := make(chan error, 1)
	go func() { read <- rt.ReadBarrier(ctx, slotB) }()
	var readErr error
	until("unrelated Slot obtains fresh quorum during first Save", func() bool {
		select {
		case readErr = <-read:
			return true
		default:
			return false
		}
	})
	if readErr != nil || !bFresh {
		t.Fatalf("fresh unrelated quorum: error=%v observed=%v", readErr, bFresh)
	}
	t.Log("setup reached: current-term durable baselines, four distinct MsgApp entries, first Save held, unrelated Slot fresh quorum")
	a.unblock(0)
	storageFenceEntered(t, ctx, a.entered[1])
	if a.secondCount != 3 || len(a.second) != 3 {
		t.Fatalf("queued ordinary prefix was not coalesced: second Save entries=%d, want 3", a.secondCount)
	}
	for i, e := range a.second {
		wantIndex := baseline[slotA].CommitIndex + uint64(i+2)
		wantDigest := sha256.Sum256(proposalString(fmt.Sprintf("count-%d", i+2)))
		if e.index != wantIndex || e.term != baseline[slotA].Term || e.digest != wantDigest {
			t.Fatalf("second Save prefix position %d: index=%d term=%d digest=%x", i, e.index, e.term, e.digest)
		}
	}
	// This arrival occurs after the physical batch is already frozen.
	fifth, err := rt.Propose(ctx, slotA, proposalString("count-5"))
	if err != nil {
		t.Fatal(err)
	}
	futures = append(futures, fifth)
	until("first retired credit permits late fifth Ready", func() bool { return aSeen >= baseline[slotA].CommitIndex+5 })
	if a.calls.Load() != 2 {
		t.Fatalf("Save bypassed held prefix: calls=%d", a.calls.Load())
	}
	// The group is physically one call, but all original leases remain owned.
	coalescingAssertUsage(t, rt, generation, 4, 4)
	state, err := a.InitialState(ctx)
	if err != nil || state.AppliedIndex != baseline[slotA].AppliedIndex || state.HardState.Commit != baseline[slotA].CommitIndex {
		t.Fatalf("premature durable advance: applied=%d commit=%d err=%v", state.AppliedIndex, state.HardState.Commit, err)
	}
	for i, f := range futures {
		probe, stop := context.WithTimeout(ctx, 30*time.Millisecond)
		_, waitErr := f.Wait(probe)
		stop()
		if !errors.Is(waitErr, context.DeadlineExceeded) {
			t.Fatalf("premature Future %d completion: err=%v", i, waitErr)
		}
	}
	if mode != "success" {
		expected := a.failSecond
		var terminalDone chan error
		if mode != "group_error" {
			expected = ErrSlotClosed
			terminalDone = make(chan error, 1)
			if mode == "runtime_close" {
				expected = ErrRuntimeClosed
				go func() { terminalDone <- rt.Close() }()
			} else {
				go func() { terminalDone <- rt.CloseSlot(ctx, slotA) }()
			}
		} else {
			a.unblock(1)
		}
		// Exact terminal Futures positively witness that the terminal fence
		// began. The physical call must still be joined in both close cases.
		for i, f := range futures {
			_, e := f.Wait(ctx)
			if !errors.Is(e, expected) {
				t.Fatalf("terminal Future %d got=%v want=%v", i, e, expected)
			}
		}
		if terminalDone != nil {
			coalescingAssertUsage(t, rt, generation, 3, 4)
			select {
			case e := <-terminalDone:
				t.Fatalf("close returned while combined Save held: %v", e)
			default:
			}
			e := rt.OpenSlot(ctx, SlotOptions{ID: slotA, Storage: a, StateMachine: &internalFakeStateMachine{}})
			want := ErrSlotExists
			if mode == "runtime_close" {
				want = ErrRuntimeClosed
			}
			if !errors.Is(e, want) {
				t.Fatalf("old generation identity reused while Save held: got=%v want=%v", e, want)
			}
			a.unblock(1)
			select {
			case e := <-terminalDone:
				if e != nil {
					t.Fatal(e)
				}
			case <-ctx.Done():
				t.Fatal("close did not join combined Save")
			}
		}
		if err := rt.Close(); err != nil {
			t.Fatal(err)
		}
		coalescingAssertAllRetired(t, rt)
		// Runtime.Close joins all queued physical work. This is stronger than
		// a timed absence check for the late A5 write.
		if a.calls.Load() != 2 || a.afterFailure.Load() != 0 {
			t.Fatalf("late physical write after terminal fence: entryCalls=%d afterFailure=%d", a.calls.Load(), a.afterFailure.Load())
		}
		final, e := a.InitialState(ctx)
		if e != nil || final.AppliedIndex != baseline[slotA].AppliedIndex || final.HardState.Commit != baseline[slotA].CommitIndex {
			t.Fatalf("terminal durable state advanced: applied=%d commit=%d err=%v", final.AppliedIndex, final.HardState.Commit, e)
		}
		last, e := a.LastIndex(ctx)
		wantLast := baseline[slotA].CommitIndex + 4
		if mode == "group_error" {
			wantLast = baseline[slotA].CommitIndex + 1
		}
		if e != nil || last != wantLast {
			t.Fatalf("terminal tail=%d want=%d err=%v", last, wantLast, e)
		}
		for i, f := range futures {
			if _, e = f.Wait(ctx); !errors.Is(e, expected) {
				t.Fatalf("late terminal Future %d changed: %v", i, e)
			}
		}
		return
	}
	a.unblock(1)
	holdA = false
	for _, m := range held {
		if err := rt.Step(ctx, Envelope{SlotID: slotA, Message: m}); err != nil {
			t.Fatal(err)
		}
	}
	for i, f := range futures {
		got, err := f.Wait(ctx)
		if err != nil || got.Index != baseline[slotA].CommitIndex+uint64(i+1) || got.Term != baseline[slotA].Term {
			t.Fatalf("Future %d: index=%d term=%d err=%v", i, got.Index, got.Term, err)
		}
	}
	state, err = a.InitialState(ctx)
	if err != nil || state.AppliedIndex != baseline[slotA].CommitIndex+5 {
		t.Fatalf("durable applied=%d err=%v", state.AppliedIndex, err)
	}
	entries, err := a.Entries(ctx, baseline[slotA].CommitIndex+1, baseline[slotA].CommitIndex+6, ^uint64(0))
	if err != nil || len(entries) != 5 {
		t.Fatalf("durable entries=%d err=%v", len(entries), err)
	}
	for i, e := range entries {
		if e.Index != baseline[slotA].CommitIndex+uint64(i+1) || e.Term != baseline[slotA].Term || sha256.Sum256(e.Data) != sha256.Sum256(proposalString(fmt.Sprintf("count-%d", i+1))) {
			t.Fatalf("durable entry %d index=%d term=%d digest=%x", i, e.Index, e.Term, sha256.Sum256(e.Data))
		}
	}
	if transport.overflow.Load() {
		t.Fatal("bounded transport overflow")
	}
	if err := rt.Close(); err != nil {
		t.Fatal(err)
	}
	coalescingAssertAllRetired(t, rt)
}

type coalescingEntryWitness struct {
	index, term uint64
	digest      [32]byte
}

// A Storage decorator retains only bounded integer/hash observations. Channel
// publication makes second immutable before the test reads it; no runtime field
// or private queue is inspected.
type coalescingSaveGate struct {
	*internalFakeStorage
	target          atomic.Uint64
	calls           atomic.Int64
	entered         [2]chan struct{}
	release         [2]chan struct{}
	once            [2]sync.Once
	firstCount      int
	secondCount     int
	second          []coalescingEntryWitness
	secondHS        *raftpb.HardState
	failSecond      error
	failed          atomic.Bool
	afterFailure    atomic.Int64
	witnessMu       sync.Mutex
	witnesses       []coalescingSaveWitness
	witnessOverflow bool
}

func (s *coalescingSaveGate) unblock(i int) { s.once[i].Do(func() { close(s.release[i]) }) }
func (s *coalescingSaveGate) Save(ctx context.Context, state PersistentState) error {
	if s.failed.Load() {
		s.afterFailure.Add(1)
		return s.failSecond
	}
	target := s.target.Load()
	if target != 0 {
		witness := coalescingSaveWitness{count: len(state.Entries)}
		if state.HardState != nil {
			witness.hasHS = true
			witness.hs = *state.HardState
		}
		if len(state.Entries) > 0 {
			witness.first = state.Entries[0].Index
			witness.last = state.Entries[len(state.Entries)-1].Index
		}
		s.witnessMu.Lock()
		if len(s.witnesses) < 16 {
			s.witnesses = append(s.witnesses, witness)
		} else {
			s.witnessOverflow = true
		}
		s.witnessMu.Unlock()
	}
	relevant := false
	for _, e := range state.Entries {
		relevant = relevant || (target != 0 && e.Index >= target)
	}
	if relevant {
		n := s.calls.Add(1)
		if n == 1 {
			s.firstCount = len(state.Entries)
		}
		if n == 2 {
			s.secondCount = len(state.Entries)
			if state.HardState != nil {
				copied := *state.HardState
				s.secondHS = &copied
			}
			for i, e := range state.Entries {
				if i == 16 {
					break
				}
				s.second = append(s.second, coalescingEntryWitness{e.Index, e.Term, sha256.Sum256(e.Data)})
			}
		}
		if n <= 2 {
			close(s.entered[n-1])
			select {
			case <-s.release[n-1]:
			case <-ctx.Done():
				return ctx.Err()
			}
			if n == 2 && s.failSecond != nil {
				s.failed.Store(true)
				return s.failSecond
			}
		}
	}
	return s.internalFakeStorage.Save(ctx, state)
}

// These assertions inspect only the existing ownership contract, not scheduling
// or group membership. Actual public protocol/Storage events prove the latter.
func coalescingAssertUsage(t *testing.T, rt *Runtime, g *slot, minCount, maxCount int) {
	t.Helper()
	b := rt.storage.budget
	b.mu.Lock()
	usage := b.slots[g]
	b.mu.Unlock()
	if usage.count < minCount || usage.count > maxCount || usage.bytes <= 0 {
		t.Fatalf("coalesced members lost ownership: count=%d expected=[%d,%d] bytes=%d", usage.count, minCount, maxCount, usage.bytes)
	}
}
func coalescingAssertAllRetired(t *testing.T, rt *Runtime) {
	t.Helper()
	b := rt.storage.budget
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.used.count != 0 || b.used.bytes != 0 || len(b.slots) != 0 {
		t.Fatalf("joined Runtime retained storage ownership: count=%d bytes=%d generations=%d", b.used.count, b.used.bytes, len(b.slots))
	}
}

type coalescingSaveWitness struct {
	hasHS       bool
	hs          raftpb.HardState
	count       int
	first, last uint64
}

func (s *coalescingSaveGate) sawStandaloneHardState(expected raftpb.HardState) bool {
	s.witnessMu.Lock()
	defer s.witnessMu.Unlock()
	for _, w := range s.witnesses {
		if w.count == 0 && w.hasHS && w.hs == expected {
			return true
		}
	}
	return false
}
