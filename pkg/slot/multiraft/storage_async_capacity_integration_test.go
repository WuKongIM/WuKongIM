//go:build integration

package multiraft

import (
	"bytes"
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Capacity failure matrix, written before changing the storage protocol:
// an ordinary asynchronous Save and an oversized synchronous fallback must not
// exceed the original worker IO bound; a legal entry larger than the new queue
// byte allowance must still commit; a one-task apply allowance must not drop
// accepted proposal futures while saturation is drained. The public Storage
// port observes actual calls; no queue implementation is inspected.
func TestRuntimeStorageOversizeFallbackSharesPhysicalSaveLimit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	var active, peak atomic.Int64
	gate := newStorageFenceGate()
	ordinary := &storageCapacityPort{Storage: gate, active: &active, peak: &peak}
	oversized := &storageCapacityPort{Storage: &internalFakeStorage{}, active: &active, peak: &peak}
	rt, err := New(Options{NodeID: 1, Workers: 1, TickInterval: time.Hour,
		Transport: &internalFakeTransport{}, Raft: RaftOptions{ElectionTick: 10, HeartbeatTick: 1, MaxApplyingTasks: 1}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		gate.release()
		if err := rt.Close(); err != nil {
			t.Error(err)
		}
	})
	for id, store := range map[SlotID]Storage{411: ordinary, 412: oversized} {
		if err := rt.BootstrapSlot(ctx, BootstrapSlotRequest{Slot: SlotOptions{ID: id, Storage: store, StateMachine: &internalFakeStateMachine{}}, Voters: []NodeID{1}}); err != nil {
			t.Fatal(err)
		}
		storageAsyncWait(t, ctx, func() bool {
			s, e := rt.Status(id)
			return e == nil && s.Role == RoleLeader && s.AppliedIndex == s.CommitIndex && s.CommitIndex > 0
		})
	}
	baseline, _ := rt.Status(411)
	gate.target.Store(baseline.CommitIndex + 1)
	gate.armed.Store(true)
	first, err := rt.Propose(ctx, 411, proposalString("ordinary-held-before-oversize"))
	if err != nil {
		t.Fatal(err)
	}
	storageFenceEntered(t, ctx, gate.entered)
	// Default MaxSizePerMsg imposes no entry limit. The 9 MiB body deliberately
	// exceeds the new 8 MiB owned-queue allowance without changing that API.
	payload := proposalPayload(0, bytes.Repeat([]byte{'x'}, 9<<20))
	observer := &storageCapacityObserver{reached: make(chan struct{})}
	second, err := rt.Propose(WithProposalStageObserver(ctx, observer), 412, payload)
	if err != nil {
		t.Fatalf("legal oversized proposal rejected: %v", err)
	}
	select {
	case <-observer.reached:
	case <-ctx.Done():
		t.Fatal("oversized proposal never reached the owner control stage")
	}
	probe, stop := context.WithTimeout(ctx, 30*time.Millisecond)
	_, early := second.Wait(probe)
	stop()
	if early == nil {
		t.Fatal("oversized write completed while sole physical Save was held")
	}
	if n := peak.Load(); n != 1 {
		t.Fatalf("physical Save concurrency %d, want original Workers=1", n)
	}
	gate.release()
	for _, f := range []Future{first, second} {
		if _, err := f.Wait(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if n := peak.Load(); n != 1 {
		t.Fatalf("fallback increased physical Save concurrency to %d", n)
	}
	if n := oversized.maxEntry.Load(); n < 9<<20 {
		t.Fatalf("oversized legal payload did not reach durable Storage: %d", n)
	}
	// Saturation with the smallest existing apply bound keeps every accepted
	// Future and exact submission order; count is finite and cleanup is joined.
	futures := make([]Future, 16)
	for i := range futures {
		futures[i], err = rt.Propose(ctx, 411, proposalString("bounded-drain"))
		if err != nil {
			t.Fatal(err)
		}
	}
	var previous uint64
	for _, f := range futures {
		result, err := f.Wait(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if result.Index <= previous {
			t.Fatalf("non-increasing durable proposal index: %d after %d", result.Index, previous)
		}
		previous = result.Index
	}
}

type storageCapacityPort struct {
	Storage
	active, peak *atomic.Int64
	maxEntry     atomic.Int64
}

func (s *storageCapacityPort) Save(ctx context.Context, state PersistentState) error {
	n := s.active.Add(1)
	defer s.active.Add(-1)
	for old := s.peak.Load(); n > old && !s.peak.CompareAndSwap(old, n); old = s.peak.Load() {
	}
	for _, entry := range state.Entries {
		n := int64(len(entry.Data))
		for old := s.maxEntry.Load(); n > old && !s.maxEntry.CompareAndSwap(old, n); old = s.maxEntry.Load() {
		}
	}
	return s.Storage.Save(ctx, state)
}

// The public stage proves owner admission before the negative Save check. It
// does not claim physical IO admission; ledger tests and the common semaphore
// independently cover that bound.
type storageCapacityObserver struct {
	reached chan struct{}
	once    sync.Once
}

func (o *storageCapacityObserver) ObserveProposalStage(stage, result string, _ time.Duration) {
	if stage == "meta_create_slot_control_wait" && result == "ok" {
		o.once.Do(func() { close(o.reached) })
	}
}
