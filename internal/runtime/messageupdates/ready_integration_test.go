//go:build integration

package messageupdates

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
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
