//go:build integration

package multiraft

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/observability/rpctrace"
	"go.etcd.io/raft/v3/raftpb"
)

// Failure matrix: four retained Ready owners in A must not occupy the sole
// Runtime worker while B obtains a new quorum. The first complete credit must
// resume A's FIFO before its second physical Save returns. No fifth Ready or
// network response may bypass the count cap, and none of the five Futures may
// succeed before durable commit/apply. Public ports drive both three-voter Slots.
func TestRuntimeStorageCountBackpressureYieldsWorkerAndResumesFirstCredit(t *testing.T) {
	testRuntimeStorageCountObservation(t, true)
}

func TestRuntimeStorageCountPausedReadSucceedsAfterFirstWholeLease(t *testing.T) {
	testRuntimeStorageCountObservation(t, false)
}

func testRuntimeStorageCountObservation(t *testing.T, fifthWrite bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	a := &storageCountGate{internalFakeStorage: &internalFakeStorage{}}
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
	}
	storageFenceEntered(t, ctx, a.entered[0])
	// W=1: the completed B control proves A's fourth Ready pass has ended.
	// Hold the next real worker turn before dispatch; only A has been enqueued.
	if _, err := rt.FreshStatus(ctx, slotB); err != nil {
		t.Fatal(err)
	}
	observer.Arm()
	var survivingRead <-chan pausedReadResult
	if fifthWrite {
		fifth, err := rt.Propose(ctx, slotA, proposalString("count-5"))
		if err != nil {
			t.Fatal(err)
		}
		futures = append(futures, fifth)
	} else {
		var stopRead context.CancelFunc
		var waiting <-chan struct{}
		survivingRead, stopRead, waiting = pausedObservationStart(rt, ctx, slotA)
		defer stopRead()
		storageFenceEntered(t, ctx, waiting)
	}
	storageFenceEntered(t, ctx, observer.blocked)
	t.Log("setup reached: two durable current-term baselines, four outbound Ready entries, first physical Save held, next write or fresh read in bounded public control queue")
	// Register A before its owner enters count pause. Waiting is witnessed at
	// the public context boundary after ReadBarrier enqueues its control.
	earlyRead, cancelEarly, earlyWaiting := pausedObservationStart(rt, ctx, slotA)
	defer cancelEarly()
	storageFenceEntered(t, ctx, earlyWaiting)
	type readResult struct {
		sample *rpctrace.Sample
		err    error
	}
	read := make(chan readResult, 1)
	go func() { s, e := ownerObservationRead(rt, ctx, slotB, 500*time.Millisecond); read <- readResult{s, e} }()
	releaseObserver()
	var result readResult
	until("unrelated Slot fresh quorum", func() bool {
		select {
		case result = <-read:
			return true
		default:
			return false
		}
	})
	if result.err != nil {
		t.Fatalf("unrelated Slot fresh quorum blocked by saturated Slot: %v", result.err)
	}
	if !bFresh {
		t.Fatal("unrelated read did not obtain a new network quorum")
	}
	requirePausedObservation(t, result.sample, false)
	for _, phase := range ownerObservationWire(t, result.sample).Phases {
		if phase.Budget.Duration != 0 || phase.Other.Duration != 0 {
			t.Fatal("parked Slot charged as B owner storage work")
		}
	}
	cancelEarly()
	early := <-earlyRead
	if !errors.Is(early.err, context.Canceled) {
		t.Fatalf("pre-registered paused read: %v", early.err)
	}
	requirePausedObservation(t, early.sample, true)
	earlyFrozen, _ := json.Marshal(early.sample)
	canceled, canceledErr := ownerObservationRead(rt, ctx, slotA, 30*time.Millisecond)
	if !errors.Is(canceledErr, context.DeadlineExceeded) {
		t.Fatalf("saturated Slot read escaped cancellation: %v", canceledErr)
	}
	for _, phase := range ownerObservationWire(t, canceled).Phases {
		if phase.Budget.Duration != 0 || phase.Other.Duration != 0 {
			t.Fatal("park wait mislabeled as synchronous owner storage occupancy")
		}
	}
	requirePausedObservation(t, canceled, true)
	frozen, _ := json.Marshal(canceled)

	if aSeen >= baseline[slotA].CommitIndex+5 {
		t.Fatal("fifth Ready escaped full count before the first credit returned")
	}
	state, e := a.InitialState(ctx)
	if e != nil || state.AppliedIndex != baseline[slotA].AppliedIndex || state.HardState.Commit != baseline[slotA].CommitIndex {
		t.Fatalf("premature durable advance: applied=%d commit=%d err=%v", state.AppliedIndex, state.HardState.Commit, e)
	}
	for _, f := range futures {
		c := storageTermObserve(t, f)
		select {
		case r := <-c:
			t.Fatalf("proposal completed before durable quorum: err=%v", r.err)
		default:
		}
	}
	a.unblock(0)
	storageFenceEntered(t, ctx, a.entered[1])
	if fifthWrite {
		until("first returned credit resumes fifth Ready", func() bool { return aSeen >= baseline[slotA].CommitIndex+5 })
	} else {
		var survived pausedReadResult
		until("fresh read resumes on first whole credit with second Save held", func() bool {
			select {
			case survived = <-survivingRead:
				return true
			default:
				return false
			}
		})
		if survived.err != nil {
			t.Fatalf("fresh read after first credit: %v", survived.err)
		}
		var positive bool
		for _, phase := range pausedObservationWire(t, survived.sample) {
			positive = positive || phase.Duration > 0
			if phase.Unfinished {
				t.Fatal("successfully resumed read kept open pause")
			}
		}
		if !positive {
			t.Fatal("successfully resumed read lost prior pause")
		}
		proof, e := a.InitialState(ctx)
		if e != nil || proof.AppliedIndex != baseline[slotA].AppliedIndex {
			t.Fatal("fresh read skipped baseline durable apply")
		}
	}
	if a.calls.Load() != 2 {
		t.Fatalf("FIFO ran beyond held second Save: calls=%d", a.calls.Load())
	}
	t.Log("first-credit witness: next write or fresh read completed its network progress with second Save still held")
	a.unblock(1)
	holdA = false
	for _, m := range held {
		if e := rt.Step(ctx, Envelope{SlotID: slotA, Message: m}); e != nil {
			t.Fatal(e)
		}
	}
	for i, f := range futures {
		r, e := f.Wait(ctx)
		if e != nil || r.Index != baseline[slotA].CommitIndex+uint64(i+1) || r.Term != baseline[slotA].Term {
			t.Fatalf("exact durable Future %d: index=%d term=%d err=%v", i, r.Index, r.Term, e)
		}
	}
	final, e := a.InitialState(ctx)
	if e != nil || final.AppliedIndex != baseline[slotA].CommitIndex+uint64(len(futures)) {
		t.Fatalf("final durable apply: index=%d err=%v", final.AppliedIndex, e)
	}
	after, _ := json.Marshal(canceled)
	if !bytes.Equal(frozen, after) {
		t.Fatal("resumed owner mutated canceled read sample")
	}
	earlyAfter, _ := json.Marshal(early.sample)
	if !bytes.Equal(earlyFrozen, earlyAfter) {
		t.Fatal("resume changed pre-registered canceled observation")
	}
	settledDone := make(chan readResult, 1)
	go func() {
		s, e := ownerObservationRead(rt, ctx, slotA, 200*time.Millisecond)
		settledDone <- readResult{s, e}
	}()
	var settled readResult
	until("resumed Slot fresh quorum has no pause", func() bool {
		select {
		case settled = <-settledDone:
			return true
		default:
			return false
		}
	})
	if settled.err != nil {
		t.Fatal(settled.err)
	}
	requirePausedObservation(t, settled.sample, false)

	if transport.overflow.Load() {
		t.Fatal("bounded transport overflow")
	}
}

// storageCountGate holds the first two post-baseline ordinary Save calls. The
// second gate proves resumption does not depend on emptying the entire FIFO.
type storageCountGate struct {
	*internalFakeStorage
	target  atomic.Uint64
	calls   atomic.Int64
	entered [2]chan struct{}
	release [2]chan struct{}
	once    [2]sync.Once
}

func (s *storageCountGate) unblock(i int) { s.once[i].Do(func() { close(s.release[i]) }) }
func (s *storageCountGate) Save(ctx context.Context, state PersistentState) error {
	target := s.target.Load()
	if target != 0 {
		for _, entry := range state.Entries {
			if entry.Index >= target {
				n := s.calls.Add(1)
				if n <= 2 {
					close(s.entered[n-1])
					select {
					case <-s.release[n-1]:
					case <-ctx.Done():
						return ctx.Err()
					}
				}
				break
			}
		}
	}
	return s.internalFakeStorage.Save(ctx, state)
}

// storageCountTransport retains bounded public envelopes with detached bytes.
type storageCountTransport struct {
	sent     chan Envelope
	overflow atomic.Bool
}

func (p *storageCountTransport) Send(_ context.Context, batch []Envelope) error {
	for _, env := range batch {
		m := env.Message
		if len(m.Entries) > 32 || len(m.Context) > 4096 || m.Snapshot != nil || len(m.Responses) != 0 {
			p.overflow.Store(true)
			return errors.New("count protocol envelope bound")
		}
		m.Context = append([]byte(nil), m.Context...)
		m.Entries = append([]raftpb.Entry(nil), m.Entries...)
		for i := range m.Entries {
			if len(m.Entries[i].Data) > 4096 {
				p.overflow.Store(true)
				return errors.New("count protocol entry bound")
			}
			m.Entries[i].Data = append([]byte(nil), m.Entries[i].Data...)
		}
		env.Message = m
		select {
		case p.sent <- env:
		default:
			p.overflow.Store(true)
			return errors.New("count protocol queue bound")
		}
	}
	return nil
}
