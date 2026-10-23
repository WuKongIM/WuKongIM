//go:build integration

package multiraft

import (
	"bytes"
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.etcd.io/raft/v3/raftpb"
)

// Failure cases precede the scheduler repair:
//   - A persisted append's self-response creates another Ready on Advance, and
//     each next scheduler pass receives one more bounded proposal message.
//   - A control submitted through Runtime.Propose must progress before this
//     finite Ready chain is exhausted, without bypassing persistence or apply.
//   - The injection must stop and all futures must join even when the assertion
//     fails; no RawNode state or internal control queue is edited by the test.
func TestRuntimeProposalControlProgressesDuringFiniteReadyChain(t *testing.T) {
	rt := newStartedRuntimeWithTick(t, time.Hour)
	const slotID SlotID = 290
	const chainLimit int64 = 8
	probe := proposalString("control-probe")
	store := &finiteReadyChainStorage{
		internalFakeStorage: &internalFakeStorage{},
		runtime:             rt,
		slotID:              slotID,
		limit:               chainLimit,
		probe:               probe,
		firstSave:           make(chan struct{}),
		releaseFirst:        make(chan struct{}),
	}
	store.probeAfter.Store(-1)
	t.Cleanup(store.unblock)
	if err := rt.BootstrapSlot(context.Background(), BootstrapSlotRequest{
		Slot:   SlotOptions{ID: slotID, Storage: store, StateMachine: &internalFakeStateMachine{}},
		Voters: []NodeID{1},
	}); err != nil {
		t.Fatal(err)
	}
	waitForCondition(t, func() bool {
		status, err := rt.Status(slotID)
		return err == nil && status.Role == RoleLeader && status.CommitIndex > 0 && status.AppliedIndex == status.CommitIndex
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	store.armed.Store(true)
	seed, err := rt.Propose(ctx, slotID, proposalString("chain-seed"))
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-store.firstSave:
	case <-ctx.Done():
		t.Fatal("seed append did not reach the controlled durable Save")
	}
	// Propose returns only after enqueueing the control, while the owning worker
	// remains inside the first Save. The fixture needs no private queue oracle.
	queued, err := rt.Propose(ctx, slotID, probe)
	if err != nil {
		t.Fatal(err)
	}
	store.unblock()
	if _, err := queued.Wait(ctx); err != nil {
		t.Fatalf("queued proposal failed to finish after the finite Ready chain: %v", err)
	}
	store.armed.Store(false)
	if _, err := seed.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	// This public fresh barrier also joins the single-node cluster's durable
	// apply boundary; observing a control turn alone cannot fabricate success.
	if err := rt.ReadBarrier(ctx, slotID); err != nil {
		t.Fatalf("fresh barrier after finite injection: %v", err)
	}
	got := store.probeAfter.Load()
	if got < 0 || got >= chainLimit {
		t.Fatalf("queued proposal reached Save after %d/%d injected Ready appends; control waited for the entire finite Ready chain", got, chainLimit)
	}
}

// finiteReadyChainStorage injects at most limit ordinary Raft proposal messages
// through Runtime.Step after real fake-storage persistence. For a single-node
// cluster, Advance processes the append's self-response and exposes the next
// committed Ready. No timer-driven or infinite producer keeps the chain alive.
type finiteReadyChainStorage struct {
	*internalFakeStorage
	runtime      *Runtime
	slotID       SlotID
	limit        int64
	probe        []byte
	firstSave    chan struct{}
	releaseFirst chan struct{}
	releaseOnce  sync.Once
	armed        atomic.Bool
	injected     atomic.Int64
	probeAfter   atomic.Int64
	entered      atomic.Bool
}

func (s *finiteReadyChainStorage) unblock() {
	s.releaseOnce.Do(func() { close(s.releaseFirst) })
}

func (s *finiteReadyChainStorage) Save(ctx context.Context, state PersistentState) error {
	if err := s.internalFakeStorage.Save(ctx, state); err != nil {
		return err
	}
	if !s.armed.Load() || len(state.Entries) == 0 {
		return nil
	}
	for _, entry := range state.Entries {
		if bytes.Equal(entry.Data, s.probe) {
			s.probeAfter.CompareAndSwap(-1, s.injected.Load())
		}
	}
	if s.entered.CompareAndSwap(false, true) {
		close(s.firstSave)
		<-s.releaseFirst
	}
	if s.injected.Load() >= s.limit {
		return nil
	}
	next := s.injected.Add(1)
	return s.runtime.Step(ctx, Envelope{SlotID: s.slotID, Message: raftpb.Message{
		Type: raftpb.MsgProp, From: 1, To: 1,
		Entries: []raftpb.Entry{{Data: proposalString(fmt.Sprintf("chain-%d", next))}},
	}})
}
