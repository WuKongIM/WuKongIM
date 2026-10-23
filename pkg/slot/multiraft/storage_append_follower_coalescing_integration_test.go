//go:build integration

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

// These public-port cases fix follower response ordering independently of the
// leader Future tests. Each heartbeat is an actual same-term protocol response;
// an unrelated owner control joins the last enqueue turn before releasing Save.
func TestRuntimeFollowerCoalescedAppendKeepsDurableACKOrder(t *testing.T) {
	testFollowerCoalescedAppend(t, false)
}

// W=1 makes the physical Storage entrance order observable. B is already ready
// when the A2/A3 group returns, and must run before A's later frozen-out tail.
func TestRuntimeCoalescedAppendRotatesPhysicalIOToOtherSlot(t *testing.T) {
	testFollowerCoalescedAppend(t, true)
}

func testFollowerCoalescedAppend(t *testing.T, fairness bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	const slotA SlotID = 611
	const slotB SlotID = 612
	journal := &followerCoalescingJournal{}
	stores := map[SlotID]*followerCoalescingStorage{}
	for _, id := range []SlotID{slotA, slotB} {
		s := &followerCoalescingStorage{internalFakeStorage: &internalFakeStorage{}, id: id, journal: journal}
		for i := range s.entered {
			s.entered[i] = make(chan struct{})
			s.release[i] = make(chan struct{})
		}
		stores[id] = s
	}
	transport := &followerCoalescingTransport{stores: stores, sent: &storageCountTransport{sent: make(chan Envelope, 128)}, acks: map[SlotID][]uint64{}}
	rt, err := New(Options{NodeID: 1, Workers: 1, TickInterval: time.Hour, Transport: transport,
		Raft: RaftOptions{ElectionTick: 10, HeartbeatTick: 1}})
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, s := range stores {
			s.unblock(0)
			s.unblock(1)
		}
		cancel()
		if err := rt.Close(); err != nil {
			t.Error(err)
		}
		t.Log("follower coalescing cleanup: every physical gate released and Runtime joined")
	})
	baseline := map[SlotID]Status{}
	previousTerm := map[SlotID]uint64{}
	for _, id := range []SlotID{slotA, slotB} {
		if err := rt.BootstrapSlot(ctx, BootstrapSlotRequest{Slot: SlotOptions{ID: id, Storage: stores[id], StateMachine: &internalFakeStateMachine{}}, Voters: []NodeID{1, 2, 3}}); err != nil {
			t.Fatal(err)
		}
		storageAsyncWait(t, ctx, func() bool {
			s, err := rt.Status(id)
			if err != nil || s.Role != RoleFollower || s.Term == 0 || s.CommitIndex == 0 || s.AppliedIndex != s.CommitIndex || len(s.CurrentVoters) != 3 {
				return false
			}
			baseline[id] = s
			return true
		})
		previousTerm[id], err = stores[id].Term(ctx, baseline[id].CommitIndex)
		if err != nil || previousTerm[id] == 0 {
			t.Fatalf("baseline durable term: %d, %v", previousTerm[id], err)
		}
	}
	heartbeat := func(id SlotID, marker string, commit uint64) {
		t.Helper()
		if err := rt.Step(ctx, Envelope{SlotID: id, Message: raftpb.Message{Type: raftpb.MsgHeartbeat, From: 2, To: 1, Term: baseline[id].Term, Commit: commit, Context: []byte(marker)}}); err != nil {
			t.Fatal(err)
		}
		for {
			select {
			case e := <-transport.sent.sent:
				m := e.Message
				if e.SlotID == id && m.Type == raftpb.MsgHeartbeatResp && m.From == 1 && m.To == 2 && m.Term == baseline[id].Term && string(m.Context) == marker {
					return
				}
			case <-ctx.Done():
				t.Fatalf("same-term heartbeat witness %s: %v", marker, ctx.Err())
			}
		}
	}
	for _, id := range []SlotID{slotA, slotB} {
		heartbeat(id, "follower-coalescing-baseline", baseline[id].CommitIndex)
		stores[id].target.Store(baseline[id].CommitIndex + 1)
	}
	appendEntry := func(id SlotID, offset uint64) {
		t.Helper()
		index := baseline[id].CommitIndex + offset
		logTerm := baseline[id].Term
		if offset == 1 {
			logTerm = previousTerm[id]
		}
		if err := rt.Step(ctx, Envelope{SlotID: id, Message: raftpb.Message{Type: raftpb.MsgApp, From: 2, To: 1, Term: baseline[id].Term,
			Index: index - 1, LogTerm: logTerm, Commit: baseline[id].CommitIndex,
			Entries: []raftpb.Entry{{Type: raftpb.EntryNormal, Index: index, Term: baseline[id].Term, Data: followerCoalescingPayload(id, offset)}},
		}}); err != nil {
			t.Fatal(err)
		}
	}
	a, b := stores[slotA], stores[slotB]
	appendEntry(slotA, 1)
	storageFenceEntered(t, ctx, a.entered[0])
	for _, offset := range []uint64{2, 3} {
		appendEntry(slotA, offset)
		heartbeat(slotA, fmt.Sprintf("follower-accepted-%d", offset), baseline[slotA].CommitIndex)
	}
	// The W=1 owner cannot execute B's control before A's final Ready enqueue
	// finishes. The preceding responses establish separate legal protocol turns.
	if _, err := rt.FreshStatus(ctx, slotB); err != nil {
		t.Fatal(err)
	}
	if len(transport.acknowledgements(slotA)) != 0 || transport.earlyACK.Load() {
		t.Fatal("follower acknowledged an index before its physical Save completed")
	}
	t.Log("setup reached: three-voter followers, A1 physical Save held, A2/A3 separate accepted protocol turns, no premature ACK")
	a.unblock(0)
	storageFenceEntered(t, ctx, a.entered[1])
	second := a.second
	if len(second) != 2 {
		t.Fatalf("follower queued suffix was not coalesced: second Save entries=%d, want 2", len(second))
	}
	for i, e := range second {
		offset := uint64(i + 2)
		if e.index != baseline[slotA].CommitIndex+offset || e.term != baseline[slotA].Term || e.digest != sha256.Sum256(followerCoalescingPayload(slotA, offset)) {
			t.Fatalf("grouped follower entry %d: index=%d term=%d digest=%x", i, e.index, e.term, e.digest)
		}
	}
	storageAsyncWait(t, ctx, func() bool { return len(transport.acknowledgements(slotA)) == 1 })
	if got := transport.acknowledgements(slotA); got[0] != baseline[slotA].CommitIndex+1 || transport.earlyACK.Load() {
		t.Fatalf("ACK before grouped Save release: indices=%v early=%v", got, transport.earlyACK.Load())
	}
	last, err := a.LastIndex(ctx)
	state, stateErr := a.InitialState(ctx)
	if err != nil || stateErr != nil || last != baseline[slotA].CommitIndex+1 || state.HardState.Commit != baseline[slotA].CommitIndex || state.AppliedIndex != baseline[slotA].AppliedIndex {
		t.Fatalf("held grouped Save violated durable prefix: last=%d commit=%d applied=%d errors=%v/%v", last, state.HardState.Commit, state.AppliedIndex, err, stateErr)
	}
	wantA := uint64(3)
	if fairness {
		// Two grouped members + late A4 + B1 use the original four node credits.
		appendEntry(slotA, 4)
		heartbeat(slotA, "late-follower-tail", baseline[slotA].CommitIndex)
		appendEntry(slotB, 1)
		heartbeat(slotB, "other-slot-queued", baseline[slotB].CommitIndex)
		if _, err := rt.FreshStatus(ctx, slotB); err != nil {
			t.Fatal(err)
		}
		wantA = 4
	}
	a.unblock(1)
	if fairness {
		storageFenceEntered(t, ctx, b.entered[0])
		order := journal.snapshot()
		if len(order) != 3 || order[0].slot != slotA || order[0].count != 1 || order[1].slot != slotA || order[1].count != 2 || order[2].slot != slotB || order[2].count != 1 || a.calls.Load() != 2 {
			t.Fatalf("physical Save failed Slot rotation before late tail: order=%+v A-calls=%d", order, a.calls.Load())
		}
		t.Log("physical fairness witness: B1 entered Save before frozen-out A4; original node credits remained bounded")
		b.unblock(0)
	}
	storageAsyncWait(t, ctx, func() bool { return uint64(len(transport.acknowledgements(slotA))) >= wantA })
	gotACKs := transport.acknowledgements(slotA)
	if uint64(len(gotACKs)) != wantA {
		t.Fatalf("follower lost or duplicated original ACKs: %v", gotACKs)
	}
	for i, index := range gotACKs {
		if index != baseline[slotA].CommitIndex+uint64(i+1) {
			t.Fatalf("follower ACK FIFO position %d: index=%d", i, index)
		}
	}
	// Storage.Save does not promise to discard the supplied Entry headers on
	// return. This decorator intentionally retains that exact input, without a
	// copy, just as a storage adapter may retain its immutable durable view.
	// Observing all member ACKs joins the Save call and its completion publish.
	if len(a.retainedSecond) != 2 {
		t.Fatalf("retained grouped Storage input length=%d, want 2", len(a.retainedSecond))
	}
	for i, e := range a.retainedSecond {
		offset := uint64(i + 2)
		if e.Index != baseline[slotA].CommitIndex+offset || e.Term != baseline[slotA].Term || sha256.Sum256(e.Data) != sha256.Sum256(followerCoalescingPayload(slotA, offset)) {
			t.Fatalf("Storage.Save retained input was rewritten after durable ACK: member=%d index=%d term=%d digest=%x", i, e.Index, e.Term, sha256.Sum256(e.Data))
		}
	}
	if transport.earlyACK.Load() || transport.sent.overflow.Load() {
		t.Fatal("undurable ACK or bounded transport overflow")
	}
	heartbeat(slotA, "commit-durable-coalesced-prefix", baseline[slotA].CommitIndex+wantA)
	storageAsyncWait(t, ctx, func() bool {
		s, err := rt.Status(slotA)
		return err == nil && s.CommitIndex == baseline[slotA].CommitIndex+wantA && s.AppliedIndex == s.CommitIndex
	})
	entries, err := a.Entries(ctx, baseline[slotA].CommitIndex+1, baseline[slotA].CommitIndex+wantA+1, ^uint64(0))
	if err != nil || uint64(len(entries)) != wantA {
		t.Fatalf("durable follower suffix: entries=%d err=%v", len(entries), err)
	}
	for i, e := range entries {
		if e.Index != baseline[slotA].CommitIndex+uint64(i+1) || e.Term != baseline[slotA].Term || sha256.Sum256(e.Data) != sha256.Sum256(followerCoalescingPayload(slotA, uint64(i+1))) {
			t.Fatalf("durable follower entry %d differs from original accepted payload", i)
		}
	}
	state, err = a.InitialState(ctx)
	if err != nil || state.AppliedIndex != baseline[slotA].CommitIndex+wantA {
		t.Fatalf("durable follower apply receipt: applied=%d err=%v", state.AppliedIndex, err)
	}
}

func followerCoalescingPayload(slot SlotID, offset uint64) []byte {
	return proposalString(fmt.Sprintf("follower-prefix-%d-%d", slot, offset))
}

type followerCoalescingEntry struct {
	index, term uint64
	digest      [32]byte
}

type followerCoalescingEntrance struct {
	slot  SlotID
	count int
}

// The journal contains only bounded public Storage entrance facts.
type followerCoalescingJournal struct {
	mu      sync.Mutex
	entries []followerCoalescingEntrance
}

func (j *followerCoalescingJournal) snapshot() []followerCoalescingEntrance {
	j.mu.Lock()
	defer j.mu.Unlock()
	return append([]followerCoalescingEntrance(nil), j.entries...)
}

type followerCoalescingStorage struct {
	*internalFakeStorage
	id      SlotID
	journal *followerCoalescingJournal
	target  atomic.Uint64
	calls   atomic.Int64
	entered [2]chan struct{}
	release [2]chan struct{}
	once    [2]sync.Once
	second  []followerCoalescingEntry
	// retainedSecond is the original caller-owned header slice, not a copy.
	// Its continued validity after Save returns is part of the public port probe.
	retainedSecond []raftpb.Entry
}

func (s *followerCoalescingStorage) unblock(i int) { s.once[i].Do(func() { close(s.release[i]) }) }

func (s *followerCoalescingStorage) Save(ctx context.Context, state PersistentState) error {
	match := false
	for _, e := range state.Entries {
		match = match || (s.target.Load() != 0 && e.Index >= s.target.Load())
	}
	if match {
		if len(state.Entries) > 8 {
			return errors.New("follower test Save witness capacity exceeded")
		}
		s.journal.mu.Lock()
		if len(s.journal.entries) >= 16 {
			s.journal.mu.Unlock()
			return errors.New("follower test Save journal capacity exceeded")
		}
		s.journal.entries = append(s.journal.entries, followerCoalescingEntrance{s.id, len(state.Entries)})
		s.journal.mu.Unlock()
		n := s.calls.Add(1)
		if n == 2 {
			s.retainedSecond = state.Entries
			for _, e := range state.Entries {
				s.second = append(s.second, followerCoalescingEntry{e.Index, e.Term, sha256.Sum256(e.Data)})
			}
		}
		if n <= 2 {
			close(s.entered[n-1])
			select {
			case <-s.release[n-1]:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	return s.internalFakeStorage.Save(ctx, state)
}

// ACK durability is checked at the actual Transport call, before the test can
// consume its witness. This detects an early ACK even if the test reads it later.
type followerCoalescingTransport struct {
	stores   map[SlotID]*followerCoalescingStorage
	sent     *storageCountTransport
	mu       sync.Mutex
	acks     map[SlotID][]uint64
	earlyACK atomic.Bool
}

func (p *followerCoalescingTransport) Send(ctx context.Context, batch []Envelope) error {
	for _, e := range batch {
		m := e.Message
		s := p.stores[e.SlotID]
		if s == nil || m.Type != raftpb.MsgAppResp || m.Reject || s.target.Load() == 0 || m.Index < s.target.Load() {
			continue
		}
		last, err := s.LastIndex(ctx)
		if err != nil || last < m.Index {
			p.earlyACK.Store(true)
		}
		p.mu.Lock()
		if len(p.acks[e.SlotID]) >= 16 {
			p.mu.Unlock()
			return errors.New("follower test ACK witness capacity exceeded")
		}
		p.acks[e.SlotID] = append(p.acks[e.SlotID], m.Index)
		p.mu.Unlock()
	}
	return p.sent.Send(ctx, batch)
}

func (p *followerCoalescingTransport) acknowledgements(id SlotID) []uint64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]uint64(nil), p.acks[id]...)
}
