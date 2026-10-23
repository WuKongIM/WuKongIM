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

// Failure matrix: a completed Save retained behind a held Raft owner must not
// strand node credits after a later Save seals the generation; physical work
// still running in another Slot must stay charged; neither case may require
// Close to repair accounting. Public Storage and Transport gates establish the
// order. The internal ledger is the explicit resource-contract assertion seam,
// because public APIs intentionally do not expose storage ownership counters.
func TestRuntimeStorageFatalRetiresAlreadyPublishedCompletions(t *testing.T) {
	const failedSlot SlotID = 319
	const liveSlot SlotID = 320
	const marker = "hold-owner-before-completion-retirement"
	failed := &storageRetirementPort{
		internalFakeStorage: &internalFakeStorage{}, firstEntered: make(chan struct{}), secondEntered: make(chan struct{}),
		firstRelease: make(chan struct{}), secondRelease: make(chan struct{}), failErr: errors.New("controlled second append failure"),
	}
	live := &storageAsyncGate{internalFakeStorage: &internalFakeStorage{}, entered: make(chan struct{}), releaseCh: make(chan struct{})}
	transport := &storageFailureTransport{slot: failedSlot, marker: marker, entered: make(chan struct{}), releaseCh: make(chan struct{})}
	rt, err := New(Options{NodeID: 1, Workers: 2, TickInterval: time.Hour, Transport: transport,
		Raft: RaftOptions{ElectionTick: 10, HeartbeatTick: 1}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	t.Cleanup(func() {
		failed.releaseFirst()
		failed.releaseSecond()
		live.release()
		transport.release()
		cancel()
		if err := rt.Close(); err != nil {
			t.Errorf("Runtime.Close: %v", err)
		}
		t.Log("completion retirement cleanup: all public gates released and Runtime joined")
	})
	baseline := make(map[SlotID]Status, 2)
	for _, item := range []struct {
		id    SlotID
		store Storage
	}{{failedSlot, failed}, {liveSlot, live}} {
		if err := rt.BootstrapSlot(ctx, BootstrapSlotRequest{Slot: SlotOptions{ID: item.id, Storage: item.store,
			StateMachine: &internalFakeStateMachine{}}, Voters: []NodeID{1, 2, 3}}); err != nil {
			t.Fatal(err)
		}
		storageAsyncWait(t, ctx, func() bool {
			s, err := rt.Status(item.id)
			if err != nil || s.Role != RoleFollower || s.CommitIndex == 0 || s.AppliedIndex != s.CommitIndex {
				return false
			}
			baseline[item.id] = s
			return true
		})
	}
	failed.target.Store(baseline[failedSlot].CommitIndex + 1)
	live.target.Store(baseline[liveSlot].CommitIndex + 1)
	live.armed.Store(true)
	appendEntry := func(id SlotID, offset uint64) {
		t.Helper()
		s := baseline[id]
		index := s.CommitIndex + offset
		if err := rt.Step(ctx, Envelope{SlotID: id, Message: raftpb.Message{Type: raftpb.MsgApp, From: 2, To: 1,
			Term: s.Term, Index: index - 1, LogTerm: s.Term, Commit: s.CommitIndex,
			Entries: []raftpb.Entry{{Type: raftpb.EntryNormal, Index: index, Term: s.Term, Data: proposalString("completion-retirement")}},
		}}); err != nil {
			t.Fatal(err)
		}
	}
	appendEntry(failedSlot, 1)
	storageFenceEntered(t, ctx, failed.firstEntered)
	appendEntry(failedSlot, 2)
	if _, err := rt.FreshStatus(ctx, failedSlot); err != nil {
		t.Fatal(err)
	}
	appendEntry(liveSlot, 1)
	storageFenceEntered(t, ctx, live.entered)
	s := baseline[failedSlot]
	if err := rt.Step(ctx, Envelope{SlotID: failedSlot, Message: raftpb.Message{Type: raftpb.MsgHeartbeat,
		From: 2, To: 1, Term: s.Term, Commit: s.CommitIndex, Context: []byte(marker)}}); err != nil {
		t.Fatal(err)
	}
	storageFenceEntered(t, ctx, transport.entered)
	failed.releaseFirst()
	storageFenceEntered(t, ctx, failed.secondEntered)
	g, other := slotFor(rt, failedSlot), slotFor(rt, liveSlot)
	g.mu.Lock()
	queued := len(g.storageCompletions)
	g.mu.Unlock()
	if queued != 1 {
		t.Fatalf("setup: second Save entered without exactly one retained predecessor completion: %d", queued)
	}
	t.Log("completion retirement setup: successful predecessor retained; second Save and other Slot Save physically held")
	failed.releaseSecond()
	storageAsyncWait(t, ctx, func() bool {
		g.mu.Lock()
		defer g.mu.Unlock()
		return errors.Is(g.fatalErr, failed.failErr) && g.storageTasks == 0
	})
	budget := rt.storage.budget
	budget.mu.Lock()
	failedUsage, liveUsage := budget.slots[g], budget.slots[other]
	budget.mu.Unlock()
	if failedUsage.count != 0 || failedUsage.bytes != 0 {
		t.Errorf("fatal generation retained completed ownership before Close: count=%d bytes=%d", failedUsage.count, failedUsage.bytes)
	}
	if liveUsage.count != 1 || liveUsage.bytes <= 0 {
		t.Errorf("unfinished physical Save lost ownership: count=%d bytes=%d", liveUsage.count, liveUsage.bytes)
	}
	g.mu.Lock()
	queued = len(g.storageCompletions)
	g.mu.Unlock()
	if queued != 0 {
		t.Errorf("fatal generation retained already completed payloads before Close: %d", queued)
	}
}

// This small ledger seam adds the same-generation case that public status cannot
// observe: failure may retire a published completion, but must not refund either
// a running append or a running apply. Their later completion retires exactly once.
func TestStorageFatalRetirementKeepsUnfinishedPartsCharged(t *testing.T) {
	for _, part := range []uint8{storageAppendPart, storageApplyPart} {
		t.Run(map[uint8]string{storageAppendPart: "append", storageApplyPart: "apply"}[part], func(t *testing.T) {
			budget, err := newStorageBudget(1, 4)
			if err != nil {
				t.Fatal(err)
			}
			g := &slot{}
			completed, ok := budget.reserve(g, 100, storageAppendPart)
			if !ok {
				t.Fatal("reserve completed owner")
			}
			pending, ok := budget.reserve(g, 200, part)
			if !ok {
				t.Fatal("reserve pending owner")
			}
			g.storageCompletions = []storageCompletion{{work: &storageWork{slot: g, lease: completed}, part: storageAppendPart}}
			if part == storageAppendPart {
				g.storageTasks = 1
			} else {
				g.applying = 1
			}
			g.fail(errors.New("controlled generation failure"))
			budget.mu.Lock()
			usage := budget.slots[g]
			budget.mu.Unlock()
			if usage.count != 1 || usage.bytes != 200 {
				t.Errorf("failure must retain only unfinished work: count=%d bytes=%d", usage.count, usage.bytes)
			}
			if len(g.storageCompletions) != 0 {
				t.Errorf("failure retained completed payloads: %d", len(g.storageCompletions))
			}
			g.publishStorageCompletion(storageCompletion{work: &storageWork{slot: g, lease: pending}, part: part})
			// Repeated retirement after cleanup must not double-refund either owner.
			g.retireStorageCompletions()
			completed.retire()
			pending.retire()
			budget.mu.Lock()
			defer budget.mu.Unlock()
			if budget.used.count != 0 || budget.used.bytes != 0 || len(budget.slots) != 0 {
				t.Fatalf("completed generation did not return exact ownership: %+v", budget.used)
			}
		})
	}
}

type storageRetirementPort struct {
	*internalFakeStorage
	target                      atomic.Uint64
	firstEntered, secondEntered chan struct{}
	firstRelease, secondRelease chan struct{}
	firstOnce, secondOnce       sync.Once
	failErr                     error
}

func (s *storageRetirementPort) releaseFirst()  { s.firstOnce.Do(func() { close(s.firstRelease) }) }
func (s *storageRetirementPort) releaseSecond() { s.secondOnce.Do(func() { close(s.secondRelease) }) }

func (s *storageRetirementPort) Save(ctx context.Context, state PersistentState) error {
	for _, entry := range state.Entries {
		if target := s.target.Load(); target != 0 && entry.Index == target {
			close(s.firstEntered)
			select {
			case <-s.firstRelease:
			case <-ctx.Done():
				return ctx.Err()
			}
		} else if target != 0 && entry.Index == target+1 {
			close(s.secondEntered)
			select {
			case <-s.secondRelease:
				return s.failErr
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	return s.internalFakeStorage.Save(ctx, state)
}
