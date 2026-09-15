//go:build integration

package messageupdates

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	goroutineregistry "github.com/WuKongIM/WuKongIM/pkg/goroutine"
)

type readySource struct{ scans atomic.Int64 }

func (s *readySource) LocalLeaderHashSlots(context.Context) ([]metadb.HashSlot, error) {
	s.scans.Add(1)
	return []metadb.HashSlot{0}, nil
}
func (*readySource) ListPendingMessageUpdates(context.Context, metadb.HashSlot, metadb.MessageUpdatePendingCursor, int) ([]metadb.MessageUpdate, metadb.MessageUpdatePendingCursor, bool, error) {
	return []metadb.MessageUpdate{readyTask(99)}, metadb.MessageUpdatePendingCursor{}, true, nil
}
func (*readySource) ListMessageUpdateRetentionCandidates(context.Context, metadb.HashSlot, metadb.MessageUpdateRetentionCursor, int) ([]metadb.MessageUpdate, metadb.MessageUpdateRetentionCursor, bool, error) {
	return nil, metadb.MessageUpdateRetentionCursor{}, true, nil
}

type readyDispatcher struct{ seen chan uint64 }

func (d readyDispatcher) DispatchMessageUpdate(_ context.Context, task metadb.MessageUpdate) (bool, error) {
	select {
	case d.seen <- task.MessageID:
	default:
	}
	return false, nil
}
func (readyDispatcher) PruneMessageUpdate(context.Context, metadb.MessageUpdate) error { return nil }

func TestReadyWorkerWakeStopRestartAndRepair(t *testing.T) {
	source := &readySource{}
	d := readyDispatcher{seen: make(chan uint64, 4096)}
	w := New(source, d, nil, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	t.Cleanup(func() {
		if err := w.Stop(context.Background()); err != nil {
			t.Error(err)
		}
	})
	if err := w.Start(ctx); err != nil {
		t.Fatal(err)
	}
	w.NotifyCommitted(readyTask(1))
	select {
	case id := <-d.seen:
		if id != 1 {
			t.Fatalf("cold repair ran before known commit: %d", id)
		}
	case <-ctx.Done():
		t.Fatal("worker did not wake")
	}
	// Exercise enqueue/stop concurrently; shutdown must join before dropping queues.
	var producers sync.WaitGroup
	for p := 0; p < 4; p++ {
		producers.Add(1)
		go func() {
			defer producers.Done()
			for i := 0; i < 1000; i++ {
				w.NotifyCommitted(readyTask(2))
			}
		}()
	}
	if err := w.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	producers.Wait()
	if len(w.pending) != 0 || w.runContext != nil {
		t.Fatal("stopped worker retained state")
	}
	for len(d.seen) > 0 {
		<-d.seen
	}
	if err := w.Start(ctx); err != nil {
		t.Fatal(err)
	}
	// No enqueue after restart: only durable repair can rediscover this target.
	select {
	case id := <-d.seen:
		if id != 99 {
			t.Fatalf("old volatile work survived restart: %d", id)
		}
	case <-ctx.Done():
		t.Fatal("restart lost durable repair")
	}
}

func TestContinuousReadyWorkDoesNotStarveRepair(t *testing.T) {
	source := &readySource{}
	d := readyDispatcher{seen: make(chan uint64, 4096)}
	w := New(source, d, nil, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := w.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer w.Stop(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		for ctx.Err() == nil {
			w.NotifyCommitted(readyTask(1))
		}
	}()
	defer func() { cancel(); <-done }()
	for {
		select {
		case id := <-d.seen:
			if id == 99 {
				return
			}
		case <-ctx.Done():
			t.Fatal("ready traffic starved durable scan")
		}
	}
}

// Four blocked identities must start together without exhausting the wave's
// eight visits or creating an unbounded goroutine for each queued identity.
type parallelReadyDispatcher struct {
	started chan uint64
	release chan struct{}
}

func (d parallelReadyDispatcher) DispatchMessageUpdate(ctx context.Context, task metadb.MessageUpdate) (bool, error) {
	d.started <- task.MessageID
	select {
	case <-d.release:
		return false, nil
	case <-ctx.Done():
		return false, ctx.Err()
	}
}
func (d parallelReadyDispatcher) PruneMessageUpdate(context.Context, metadb.MessageUpdate) error {
	return nil
}
func TestReadyWaveOverlapsBoundedIdentityDispatch(t *testing.T) {
	d := parallelReadyDispatcher{started: make(chan uint64, 8), release: make(chan struct{})}
	w := readyWorker(d)
	w.registry = goroutineregistry.New()
	for i := uint64(1); i <= 8; i++ {
		w.NotifyCommitted(readyTask(i))
	}
	done := make(chan int, 1)
	go func() { done <- w.dispatchReady(context.Background()) }()
	defer func() { close(d.release); <-done }()
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	for i := 0; i < 4; i++ {
		select {
		case <-d.started:
		case <-ctx.Done():
			t.Fatal("independent durable commits remained serial")
		}
	}
	dispatchActive := int64(0)
	for _, module := range w.registry.Snapshot().Modules {
		for _, task := range module.Tasks {
			if task.Task == goroutineregistry.TaskMessageUpdateDispatch {
				dispatchActive = task.Active
			}
			if task.Task == goroutineregistry.TaskMessageUpdateWorker && task.Active > 1 {
				t.Fatal("dispatch lanes over-declared the supervising singleton")
			}
		}
	}
	if dispatchActive != 4 {
		t.Fatalf("managed dispatch lanes=%d, want 4", dispatchActive)
	}
	select {
	case <-d.started:
		t.Fatal("dispatch exceeded four concurrent identities")
	default:
	}
}

func TestRepairWaveOverlapsBoundedIdentityDispatch(t *testing.T) {
	d := parallelReadyDispatcher{started: make(chan uint64, 8), release: make(chan struct{})}
	w := readyWorker(d)
	rows := make([]metadb.MessageUpdate, 8)
	for i := range rows {
		rows[i] = readyTask(uint64(i + 1))
	}
	remaining := 32
	done := make(chan struct{})
	go func() { defer close(done); w.dispatchRows(context.Background(), rows, &remaining) }()
	defer func() { close(d.release); <-done }()
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	for i := 0; i < 4; i++ {
		select {
		case <-d.started:
		case <-ctx.Done():
			t.Fatal("durable repair serialized independent progress commits")
		}
	}
	select {
	case <-d.started:
		t.Fatal("repair exceeded four active lanes")
	default:
	}
}

// A call can inherit only the tail of the shared scheduling deadline. That
// scheduling expiration must not force known work to wait for cold-slot discovery.
type expiringReadyDispatcher struct {
	calls  int
	always bool
}

func (d *expiringReadyDispatcher) DispatchMessageUpdate(ctx context.Context, _ metadb.MessageUpdate) (bool, error) {
	d.calls++
	if d.always || d.calls == 1 {
		<-ctx.Done()
		return false, ctx.Err()
	}
	return false, nil
}
func (*expiringReadyDispatcher) PruneMessageUpdate(context.Context, metadb.MessageUpdate) error {
	return nil
}

func TestReadyWaveDeadlinePreservesOneRetryWithoutDiscovery(t *testing.T) {
	d := &expiringReadyDispatcher{}
	w := readyWorker(d)
	w.NotifyCommitted(readyTask(1))
	if w.dispatchReady(context.Background()) != 1 || len(w.pending) != 1 {
		t.Fatal("wave deadline discarded known work into the cold-slot scan")
	}
	w.dispatchReady(context.Background())
	if d.calls != 2 || len(w.pending) != 0 {
		t.Fatalf("calls=%d pending=%d", d.calls, len(w.pending))
	}
}

func TestReadyWaveDeadlineRetryRemainsBounded(t *testing.T) {
	d := &expiringReadyDispatcher{always: true}
	w := readyWorker(d)
	w.NotifyCommitted(readyTask(1))
	w.dispatchReady(context.Background())
	w.dispatchReady(context.Background())
	w.dispatchReady(context.Background())
	if d.calls != 2 || len(w.pending) != 0 {
		t.Fatalf("persistent deadline did not fall back after one retry: calls=%d pending=%d", d.calls, len(w.pending))
	}
}

func TestReadyParentDeadlineDoesNotEnqueueBudgetRetry(t *testing.T) {
	d := &expiringReadyDispatcher{always: true}
	w := readyWorker(d)
	w.NotifyCommitted(readyTask(1))
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if w.dispatchReady(ctx) != 1 || len(w.pending) != 0 || d.calls != 1 {
		t.Fatal("parent deadline retained work for another wave")
	}
}

// Only the second lane consumes the shared budget; joining it must not reclassify
// the first lane's earlier dependency error as scheduling expiration.
type mixedDeadlineDispatcher struct{}

func (mixedDeadlineDispatcher) DispatchMessageUpdate(ctx context.Context, task metadb.MessageUpdate) (bool, error) {
	if task.MessageID == 2 {
		<-ctx.Done()
	}
	return false, context.DeadlineExceeded
}
func (mixedDeadlineDispatcher) PruneMessageUpdate(context.Context, metadb.MessageUpdate) error {
	return nil
}

func TestReadyDeadlineClassificationPrecedesLaneJoin(t *testing.T) {
	w := readyWorker(mixedDeadlineDispatcher{})
	w.NotifyCommitted(readyTask(1))
	w.NotifyCommitted(readyTask(2))
	if w.dispatchReady(context.Background()) != 2 || len(w.pending) != 1 {
		t.Fatal("joining a slow lane changed an earlier dependency failure into a retry")
	}
	for _, entry := range w.pending {
		if entry.MessageID != 2 || !entry.budgetRetried {
			t.Fatal("wrong lane retained a budget retry")
		}
	}
}
