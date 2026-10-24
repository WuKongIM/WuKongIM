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

// Failure matrix is recorded before adding the append-lane failure fence:
// a first Save error must stop later same-generation physical writes even while
// the Raft owner cannot consume its completion; another Slot must still drain;
// no fatal-generation append acknowledgement or durable advancement may escape.
// The single physical worker's bounded per-Slot FIFO rotation supplies a
// positive progress witness: A1(error), B1, A2(skip), B2. Holding the public
// Transport call prevents owner scheduling from hiding the missing lane fence.
func TestRuntimeStorageFailureStopsQueuedWritesBeforeOwnerConsumesError(t *testing.T) {
	storageFailureQueuedCase(t, false)
}

// Closing the Slot before Save returns must not suppress the append failure
// fence. Close rejection of new requests is separate from queued physical IO.
func TestRuntimeStorageFailureStopsQueuedWritesAfterCloseBegins(t *testing.T) {
	storageFailureQueuedCase(t, true)
}

func storageFailureQueuedCase(t *testing.T, closeBeforeFailure bool) {
	t.Helper()
	const failedSlot SlotID = 315
	const canarySlot SlotID = 316
	const blockedHeartbeat = "hold-owner-after-queued-appends"
	failed := &storageFailurePort{
		internalFakeStorage: &internalFakeStorage{}, entered: make(chan struct{}), releaseCh: make(chan struct{}),
		failErr: errors.New("controlled first append failure"),
	}
	canary := &storageFailureCanary{internalFakeStorage: &internalFakeStorage{}, secondSaved: make(chan struct{})}
	transport := &storageFailureTransport{slot: failedSlot, marker: blockedHeartbeat, entered: make(chan struct{}), releaseCh: make(chan struct{})}
	rt, err := New(Options{NodeID: 1, Workers: 1, TickInterval: time.Hour, Transport: transport,
		Raft: RaftOptions{ElectionTick: 10, HeartbeatTick: 1}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	t.Cleanup(func() {
		failed.release()
		transport.release()
		cancel()
		if err := rt.Close(); err != nil {
			t.Errorf("Runtime.Close: %v", err)
		}
		t.Log("failed-append cleanup: both public gates released and Runtime joined")
	})
	baseline := make(map[SlotID]Status, 2)
	stores := map[SlotID]Storage{failedSlot: failed, canarySlot: canary}
	for _, id := range []SlotID{failedSlot, canarySlot} {
		if err := rt.BootstrapSlot(ctx, BootstrapSlotRequest{
			Slot: SlotOptions{ID: id, Storage: stores[id], StateMachine: &internalFakeStateMachine{}}, Voters: []NodeID{1, 2, 3},
		}); err != nil {
			t.Fatal(err)
		}
		storageAsyncWait(t, ctx, func() bool {
			s, e := rt.Status(id)
			if e != nil || s.Role != RoleFollower || s.CommitIndex == 0 || s.AppliedIndex != s.CommitIndex {
				return false
			}
			baseline[id] = s
			return true
		})
	}
	failed.target.Store(baseline[failedSlot].CommitIndex + 1)
	canary.target.Store(baseline[canarySlot].CommitIndex + 2)
	appendEntry := func(id SlotID, offset uint64) {
		t.Helper()
		s := baseline[id]
		index := s.CommitIndex + offset
		if err := rt.Step(ctx, Envelope{SlotID: id, Message: raftpb.Message{
			Type: raftpb.MsgApp, From: 2, To: 1, Term: s.Term,
			Index: index - 1, LogTerm: s.Term, Commit: s.CommitIndex,
			Entries: []raftpb.Entry{{Type: raftpb.EntryNormal, Index: index, Term: s.Term, Data: proposalString("queued-durable-order-probe")}},
		}}); err != nil {
			t.Fatal(err)
		}
	}
	appendEntry(failedSlot, 1)
	storageFenceEntered(t, ctx, failed.entered)
	// FreshStatus is an owner-executed public control. Requests queued before
	// it have already received their Ready turn when the response is returned.
	appendEntry(failedSlot, 2)
	if _, err := rt.FreshStatus(ctx, failedSlot); err != nil {
		t.Fatalf("second failed-Slot append was not accepted before release: %v", err)
	}
	for _, offset := range []uint64{1, 2} {
		appendEntry(canarySlot, offset)
		if _, err := rt.FreshStatus(ctx, canarySlot); err != nil {
			t.Fatalf("canary append %d was not accepted before release: %v", offset, err)
		}
	}
	s := baseline[failedSlot]
	if err := rt.Step(ctx, Envelope{SlotID: failedSlot, Message: raftpb.Message{
		Type: raftpb.MsgHeartbeat, From: 2, To: 1, Term: s.Term, Commit: s.CommitIndex, Context: []byte(blockedHeartbeat),
	}}); err != nil {
		t.Fatal(err)
	}
	storageFenceEntered(t, ctx, transport.entered)
	t.Log("failed-append setup: four FIFO owners accepted; Raft owner held in public Transport")
	var closeDone chan error
	if closeBeforeFailure {
		closeDone = make(chan error, 1)
		go func() { closeDone <- rt.CloseSlot(ctx, failedSlot) }()
		storageAsyncWait(t, ctx, func() bool {
			_, err := rt.Status(failedSlot)
			return errors.Is(err, ErrSlotNotFound)
		})
		select {
		case err := <-closeDone:
			t.Fatalf("CloseSlot returned while both public gates remained held: %v", err)
		default:
		}
		t.Log("failed-append close witness: public Slot admission removed before Save failure")
	}
	failed.release()
	storageFenceEntered(t, ctx, canary.secondSaved)
	// B2 is a positive physical-worker progress witness, not an absence timeout.
	// The owner is still unable to drain A1's failed completion at this point.
	if calls := failed.laterCalls.Load(); calls != 0 {
		t.Errorf("later same-generation physical Save entered after predecessor failure: %d", calls)
	}
	transport.release()
	if closeDone != nil {
		select {
		case err := <-closeDone:
			if err != nil {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal("CloseSlot did not join after both public gates released")
		}
	}
	if err := rt.Close(); err != nil {
		t.Fatal(err)
	}
	if calls := failed.laterCalls.Load(); calls != 0 {
		t.Errorf("failed generation performed later Save before joined close: %d", calls)
	}
	state, err := failed.InitialState(ctx)
	if err != nil || state.AppliedIndex != s.AppliedIndex || state.HardState.Commit != s.CommitIndex {
		t.Fatalf("failed generation changed durable state: %+v err=%v", state, err)
	}
	last, err := failed.LastIndex(ctx)
	if err != nil || last != s.CommitIndex {
		t.Fatalf("failed generation changed durable log: last=%d baseline=%d err=%v", last, s.CommitIndex, err)
	}
}

type storageFailurePort struct {
	*internalFakeStorage
	entered, releaseCh chan struct{}
	once               sync.Once
	target             atomic.Uint64
	failed             atomic.Bool
	laterCalls         atomic.Int64
	failErr            error
}

func (s *storageFailurePort) release() { s.once.Do(func() { close(s.releaseCh) }) }

func (s *storageFailurePort) Save(ctx context.Context, state PersistentState) error {
	if s.failed.Load() {
		s.laterCalls.Add(1)
		return s.failErr
	}
	for _, entry := range state.Entries {
		if entry.Index == s.target.Load() && entry.Index != 0 {
			close(s.entered)
			select {
			case <-s.releaseCh:
			case <-ctx.Done():
				return ctx.Err()
			}
			s.failed.Store(true)
			return s.failErr
		}
	}
	return s.internalFakeStorage.Save(ctx, state)
}

type storageFailureCanary struct {
	*internalFakeStorage
	target      atomic.Uint64
	secondSaved chan struct{}
	once        sync.Once
}

func (s *storageFailureCanary) Save(ctx context.Context, state PersistentState) error {
	if err := s.internalFakeStorage.Save(ctx, state); err != nil {
		return err
	}
	for _, entry := range state.Entries {
		if entry.Index == s.target.Load() && entry.Index != 0 {
			s.once.Do(func() { close(s.secondSaved) })
		}
	}
	return nil
}

type storageFailureTransport struct {
	slot               SlotID
	marker             string
	entered, releaseCh chan struct{}
	once, enteredOnce  sync.Once
}

func (p *storageFailureTransport) release() { p.once.Do(func() { close(p.releaseCh) }) }

func (p *storageFailureTransport) Send(ctx context.Context, batch []Envelope) error {
	for _, env := range batch {
		if env.SlotID == p.slot && env.Message.Type == raftpb.MsgHeartbeatResp && string(env.Message.Context) == p.marker {
			p.enteredOnce.Do(func() { close(p.entered) })
			select {
			case <-p.releaseCh:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	return nil
}
