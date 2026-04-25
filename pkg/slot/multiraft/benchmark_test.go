package multiraft

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestBenchmarkAppliedTrackerWaitForNode(t *testing.T) {
	tracker := newBenchmarkAppliedTracker()
	done := make(chan error, 1)

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		done <- tracker.waitForNode(ctx, 1, 100, 7)
	}()

	time.Sleep(10 * time.Millisecond)
	tracker.markApplied(1, 100, 6)

	select {
	case err := <-done:
		t.Fatalf("waitForNode() returned early: %v", err)
	default:
	}

	tracker.markApplied(1, 100, 7)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("waitForNode() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("waitForNode() did not unblock")
	}
}

func TestBenchmarkAppliedTrackerWaitForAll(t *testing.T) {
	tracker := newBenchmarkAppliedTracker()
	done := make(chan error, 1)

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		done <- tracker.waitForAll(ctx, []NodeID{1, 2, 3}, 101, 9)
	}()

	time.Sleep(10 * time.Millisecond)
	tracker.markApplied(1, 101, 9)
	tracker.markApplied(2, 101, 8)

	select {
	case err := <-done:
		t.Fatalf("waitForAll() returned early: %v", err)
	default:
	}

	tracker.markApplied(2, 101, 9)
	tracker.markApplied(3, 101, 9)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("waitForAll() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("waitForAll() did not unblock")
	}
}

func BenchmarkRuntimeTickFanout(b *testing.B) {
	for _, slotCount := range []int{64, 2048} {
		b.Run("slots="+strconv.Itoa(slotCount), func(b *testing.B) {
			rt := &Runtime{
				slots:     make(map[SlotID]*slot, slotCount),
				scheduler: newScheduler(),
			}
			slotIDs := make([]SlotID, 0, slotCount)
			for i := 0; i < slotCount; i++ {
				id := SlotID(i + 1)
				rt.slots[id] = &slot{id: id}
				slotIDs = append(slotIDs, id)
			}

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				benchmarkTickFanout(rt)
				benchmarkDrainScheduler(rt.scheduler, len(slotIDs))
			}
		})
	}
}

func benchmarkTickFanout(rt *Runtime) {
	rt.mu.RLock()
	slots := make([]*slot, 0, len(rt.slots))
	for _, g := range rt.slots {
		slots = append(slots, g)
	}
	rt.mu.RUnlock()

	for _, g := range slots {
		g.markTickPending()
		rt.scheduler.enqueue(g.id)
	}
}

func benchmarkDrainScheduler(s *scheduler, slotCount int) {
	for i := 0; i < slotCount; i++ {
		slotID := <-s.ch
		s.begin(slotID)
		_ = s.done(slotID)
	}
}

func BenchmarkThreeNodeMultiSlotProposalRoundTrip(b *testing.B) {
	for _, slotCount := range []int{8, 32} {
		b.Run("slots="+strconv.Itoa(slotCount), func(b *testing.B) {
			cluster := newAsyncTestCluster(b, []NodeID{1, 2, 3}, asyncNetworkConfig{
				MaxDelay: 2 * time.Millisecond,
				Seed:     int64(slotCount),
			})

			slotIDs := make([]SlotID, 0, slotCount)
			leaders := make(map[SlotID]NodeID, slotCount)
			for i := 0; i < slotCount; i++ {
				slotID := SlotID(10_000 + i)
				slotIDs = append(slotIDs, slotID)
				cluster.bootstrapSlot(b, slotID, []NodeID{1, 2, 3})
				cluster.waitForBootstrapApplied(b, slotID, 3)

				targetLeader := NodeID((i % 3) + 1)
				leaderID := cluster.waitForLeader(b, slotID)
				if leaderID != targetLeader {
					if err := cluster.runtime(leaderID).TransferLeadership(context.Background(), slotID, targetLeader); err != nil {
						b.Fatalf("TransferLeadership(slot=%d) error = %v", slotID, err)
					}
					cluster.waitForSpecificLeader(b, slotID, targetLeader)
					leaderID = targetLeader
				}
				leaders[slotID] = leaderID
			}

			payloads := make([][]byte, len(slotIDs))
			for i, slotID := range slotIDs {
				payloads[i] = []byte("bench-slot-" + strconv.FormatUint(uint64(slotID), 10))
			}

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				slotIndex := i % len(slotIDs)
				slotID := slotIDs[slotIndex]
				leaderID := leaders[slotID]

				fut, err := cluster.runtime(leaderID).Propose(context.Background(), slotID, proposalPayload(0, payloads[slotIndex]))
				if err != nil {
					b.Fatalf("Propose(slot=%d leader=%d) error = %v", slotID, leaderID, err)
				}

				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				res, err := fut.Wait(ctx)
				cancel()
				if err != nil {
					b.Fatalf("Wait(slot=%d) error = %v", slotID, err)
				}

				cluster.waitForAllNodesAppliedIndex(b, slotID, res.Index)
			}
		})
	}
}

func BenchmarkThreeNodeMultiSlotProposalRoundTripNotified(b *testing.B) {
	for _, slotCount := range []int{8, 32} {
		b.Run("slots="+strconv.Itoa(slotCount), func(b *testing.B) {
			harness := newBenchmarkClusterHarness(b, slotCount, newBenchmarkAppliedTracker())

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				slotIndex := i % len(harness.slotIDs)
				if err := benchmarkProposeRoundTripNotified(harness, slotIndex); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkThreeNodeMultiSlotConcurrentProposalThroughput(b *testing.B) {
	for _, slotCount := range []int{8, 32} {
		b.Run("slots="+strconv.Itoa(slotCount), func(b *testing.B) {
			harness := newBenchmarkClusterHarness(b, slotCount, newBenchmarkAppliedTracker())
			workerCount := slotCount
			if workerCount > 12 {
				workerCount = 12
			}

			var (
				next   uint64
				failed atomic.Bool
				wg     sync.WaitGroup
			)
			errCh := make(chan error, 1)

			b.ReportAllocs()
			b.ResetTimer()
			for worker := 0; worker < workerCount; worker++ {
				wg.Add(1)
				go func() {
					defer wg.Done()

					for {
						if failed.Load() {
							return
						}

						op := int(atomic.AddUint64(&next, 1)) - 1
						if op >= b.N {
							return
						}

						slotIndex := op % len(harness.slotIDs)
						if err := benchmarkProposeRoundTripNotified(harness, slotIndex); err != nil {
							if failed.CompareAndSwap(false, true) {
								errCh <- err
							}
							return
						}
					}
				}()
			}
			wg.Wait()
			b.StopTimer()

			select {
			case err := <-errCh:
				b.Fatal(err)
			default:
			}
		})
	}
}

type benchmarkClusterHarness struct {
	cluster  *testCluster
	nodeIDs  []NodeID
	slotIDs  []SlotID
	leaders  []NodeID
	payloads [][]byte
	tracker  *benchmarkAppliedTracker
}

func newBenchmarkClusterHarness(b testing.TB, slotCount int, tracker *benchmarkAppliedTracker) *benchmarkClusterHarness {
	b.Helper()

	nodeIDs := []NodeID{1, 2, 3}
	cluster := newAsyncTestCluster(b, nodeIDs, asyncNetworkConfig{
		MaxDelay: 2 * time.Millisecond,
		Seed:     int64(slotCount),
	})

	slotIDs := make([]SlotID, 0, slotCount)
	leaders := make([]NodeID, 0, slotCount)
	payloads := make([][]byte, 0, slotCount)

	for i := 0; i < slotCount; i++ {
		slotID := SlotID(10_000 + i)
		slotIDs = append(slotIDs, slotID)

		if tracker != nil {
			cluster.bootstrapSlotWithAppliedTracker(b, tracker, slotID, nodeIDs)
		} else {
			cluster.bootstrapSlot(b, slotID, nodeIDs)
		}
		cluster.waitForBootstrapApplied(b, slotID, 3)

		targetLeader := nodeIDs[i%len(nodeIDs)]
		leaderID := cluster.waitForLeader(b, slotID)
		if leaderID != targetLeader {
			if err := cluster.runtime(leaderID).TransferLeadership(context.Background(), slotID, targetLeader); err != nil {
				b.Fatalf("TransferLeadership(slot=%d) error = %v", slotID, err)
			}
			cluster.waitForSpecificLeader(b, slotID, targetLeader)
			leaderID = targetLeader
		}

		leaders = append(leaders, leaderID)
		payloads = append(payloads, []byte("bench-slot-"+strconv.FormatUint(uint64(slotID), 10)))
	}

	return &benchmarkClusterHarness{
		cluster:  cluster,
		nodeIDs:  append([]NodeID(nil), nodeIDs...),
		slotIDs:  slotIDs,
		leaders:  leaders,
		payloads: payloads,
		tracker:  tracker,
	}
}

func benchmarkProposeRoundTripNotified(harness *benchmarkClusterHarness, slotIndex int) error {
	slotID := harness.slotIDs[slotIndex]
	leaderID := harness.leaders[slotIndex]

	fut, err := harness.cluster.runtime(leaderID).Propose(context.Background(), slotID, proposalPayload(0, harness.payloads[slotIndex]))
	if err != nil {
		return fmt.Errorf("Propose(slot=%d leader=%d): %w", slotID, leaderID, err)
	}

	res, err := fut.Wait(context.Background())
	if err != nil {
		return fmt.Errorf("Wait(slot=%d): %w", slotID, err)
	}

	if err := harness.tracker.waitForAll(context.Background(), harness.nodeIDs, slotID, res.Index); err != nil {
		return fmt.Errorf("waitForAll(slot=%d index=%d): %w", slotID, res.Index, err)
	}

	return nil
}

type benchmarkAppliedKey struct {
	nodeID NodeID
	slotID SlotID
}

type benchmarkAppliedWaiter struct {
	index uint64
	ch    chan struct{}
}

type benchmarkAppliedTracker struct {
	mu      sync.Mutex
	applied map[benchmarkAppliedKey]uint64
	waiters map[benchmarkAppliedKey][]benchmarkAppliedWaiter
}

func newBenchmarkAppliedTracker() *benchmarkAppliedTracker {
	return &benchmarkAppliedTracker{
		applied: make(map[benchmarkAppliedKey]uint64),
		waiters: make(map[benchmarkAppliedKey][]benchmarkAppliedWaiter),
	}
}

func (t *benchmarkAppliedTracker) markApplied(nodeID NodeID, slotID SlotID, index uint64) {
	key := benchmarkAppliedKey{nodeID: nodeID, slotID: slotID}

	t.mu.Lock()
	if t.applied[key] >= index {
		t.mu.Unlock()
		return
	}
	t.applied[key] = index

	waiters := t.waiters[key]
	kept := waiters[:0]
	for _, waiter := range waiters {
		if waiter.index <= index {
			close(waiter.ch)
			continue
		}
		kept = append(kept, waiter)
	}
	if len(kept) == 0 {
		delete(t.waiters, key)
	} else {
		t.waiters[key] = kept
	}
	t.mu.Unlock()
}

func (t *benchmarkAppliedTracker) waitForNode(ctx context.Context, nodeID NodeID, slotID SlotID, index uint64) error {
	key := benchmarkAppliedKey{nodeID: nodeID, slotID: slotID}
	waiter := benchmarkAppliedWaiter{
		index: index,
		ch:    make(chan struct{}),
	}

	t.mu.Lock()
	if t.applied[key] >= index {
		t.mu.Unlock()
		return nil
	}
	t.waiters[key] = append(t.waiters[key], waiter)
	t.mu.Unlock()

	select {
	case <-waiter.ch:
		return nil
	case <-ctx.Done():
		t.mu.Lock()
		if t.applied[key] >= index {
			t.mu.Unlock()
			return nil
		}

		waiters := t.waiters[key]
		kept := waiters[:0]
		for _, candidate := range waiters {
			if candidate.ch == waiter.ch {
				continue
			}
			kept = append(kept, candidate)
		}
		if len(kept) == 0 {
			delete(t.waiters, key)
		} else {
			t.waiters[key] = kept
		}
		t.mu.Unlock()
		return ctx.Err()
	}
}

func (t *benchmarkAppliedTracker) waitForAll(ctx context.Context, nodeIDs []NodeID, slotID SlotID, index uint64) error {
	for _, nodeID := range nodeIDs {
		if err := t.waitForNode(ctx, nodeID, slotID, index); err != nil {
			return err
		}
	}
	return nil
}

type benchmarkNotifyingStorage struct {
	*internalFakeStorage
	tracker *benchmarkAppliedTracker
	nodeID  NodeID
	slotID  SlotID
}

func newBenchmarkNotifyingStorage(tracker *benchmarkAppliedTracker, nodeID NodeID, slotID SlotID) *benchmarkNotifyingStorage {
	return &benchmarkNotifyingStorage{
		internalFakeStorage: &internalFakeStorage{},
		tracker:             tracker,
		nodeID:              nodeID,
		slotID:              slotID,
	}
}

func (s *benchmarkNotifyingStorage) MarkApplied(ctx context.Context, index uint64) error {
	if err := s.internalFakeStorage.MarkApplied(ctx, index); err != nil {
		return err
	}
	s.tracker.markApplied(s.nodeID, s.slotID, index)
	return nil
}

func (c *testCluster) bootstrapSlotWithAppliedTracker(t testing.TB, tracker *benchmarkAppliedTracker, slotID SlotID, voters []NodeID) {
	t.Helper()

	for _, nodeID := range voters {
		store := newBenchmarkNotifyingStorage(tracker, nodeID, slotID)
		fsm := &internalFakeStateMachine{}
		c.stores[nodeID][slotID] = store.internalFakeStorage
		c.fsms[nodeID][slotID] = fsm

		err := c.runtime(nodeID).BootstrapSlot(context.Background(), BootstrapSlotRequest{
			Slot: SlotOptions{
				ID:           slotID,
				Storage:      store,
				StateMachine: fsm,
			},
			Voters: voters,
		})
		if err != nil {
			t.Fatalf("BootstrapSlot(node=%d) error = %v", nodeID, err)
		}
	}
}
