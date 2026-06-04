package commit_test

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/commit"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/engine"
)

func TestConfigDoesNotExposeNoSync(t *testing.T) {
	if _, ok := reflect.TypeOf(commit.Config{}).FieldByName("NoSync"); ok {
		t.Fatal("Config exposes NoSync, want durable sync fixed on")
	}
}

func TestCoordinatorPublishesAfterCommit(t *testing.T) {
	db := openTestDB(t)
	c := commit.NewCoordinator(db, commit.Config{FlushWindow: time.Millisecond, QueueSize: 8})
	defer c.Close()

	var committed atomic.Bool
	c.SetCommitFunc(func(batch *engine.Batch) error {
		committed.Store(true)
		return batch.Commit(false)
	})

	var published atomic.Bool
	err := c.Submit(context.Background(), commit.Request{
		Build: func(batch *engine.Batch) error {
			return batch.Set([]byte("k"), []byte("v"))
		},
		Publish: func() error {
			if !committed.Load() {
				t.Fatal("published before commit")
			}
			published.Store(true)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Submit(): %v", err)
	}
	if !published.Load() {
		t.Fatal("publish was not called")
	}
}

func TestCoordinatorFailedCommitDoesNotPublish(t *testing.T) {
	db := openTestDB(t)
	c := commit.NewCoordinator(db, commit.Config{FlushWindow: time.Millisecond, QueueSize: 8})
	defer c.Close()

	wantErr := errors.New("commit failed")
	c.SetCommitFunc(func(batch *engine.Batch) error { return wantErr })

	var published atomic.Bool
	err := c.Submit(context.Background(), commit.Request{
		Build:   func(batch *engine.Batch) error { return batch.Set([]byte("k"), []byte("v")) },
		Publish: func() error { published.Store(true); return nil },
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Submit() err = %v, want %v", err, wantErr)
	}
	if published.Load() {
		t.Fatal("publish called after failed commit")
	}
}

func TestCoordinatorFlushWindowBatchesConcurrentRequests(t *testing.T) {
	db := openTestDB(t)
	c := commit.NewCoordinator(db, commit.Config{FlushWindow: 20 * time.Millisecond, QueueSize: 8, MaxRequests: 4})
	defer c.Close()

	var commits atomic.Int64
	c.SetCommitFunc(func(batch *engine.Batch) error {
		commits.Add(1)
		return batch.Commit(false)
	})

	var wg sync.WaitGroup
	for _, key := range []string{"k1", "k2"} {
		key := key
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := c.Submit(context.Background(), commit.Request{
				Build: func(batch *engine.Batch) error { return batch.Set([]byte(key), []byte("v")) },
			})
			if err != nil {
				t.Errorf("Submit(%s): %v", key, err)
			}
		}()
	}
	wg.Wait()
	if commits.Load() != 1 {
		t.Fatalf("commits = %d, want 1", commits.Load())
	}
}

func TestCoordinatorObservesCommitBatch(t *testing.T) {
	db := openTestDB(t)
	observer := &recordingObserver{}
	c := commit.NewCoordinator(db, commit.Config{
		FlushWindow: time.Second,
		QueueSize:   8,
		MaxRequests: 2,
		Observer:    observer,
	})
	defer c.Close()

	var commits atomic.Int64
	c.SetCommitFunc(func(batch *engine.Batch) error {
		commits.Add(1)
		return batch.Commit(false)
	})

	var wg sync.WaitGroup
	for _, req := range []struct {
		key     string
		records int
		bytes   int
	}{
		{key: "k1", records: 1, bytes: 10},
		{key: "k2", records: 2, bytes: 20},
	} {
		req := req
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := c.Submit(context.Background(), commit.Request{
				Records: req.records,
				Bytes:   req.bytes,
				Build:   func(batch *engine.Batch) error { return batch.Set([]byte(req.key), []byte("v")) },
			})
			if err != nil {
				t.Errorf("Submit(%s): %v", req.key, err)
			}
		}()
	}
	wg.Wait()

	if commits.Load() != 1 {
		t.Fatalf("commits = %d, want 1", commits.Load())
	}
	event := observer.onlyBatch(t)
	if event.Requests != 2 || event.Records != 3 || event.Bytes != 30 {
		t.Fatalf("observed batch = %#v, want 2 requests, 3 records, 30 bytes", event)
	}
	if event.CommitDuration <= 0 || event.TotalDuration <= 0 {
		t.Fatalf("observed durations = %#v, want positive commit and total durations", event)
	}
	if len(observer.depthsSnapshot()) == 0 {
		t.Fatal("queue depth was not observed")
	}
}

func TestCoordinatorObservesSubmitRequest(t *testing.T) {
	db := openTestDB(t)
	observer := &recordingObserver{}
	c := commit.NewCoordinator(db, commit.Config{
		FlushWindow: time.Millisecond,
		QueueSize:   8,
		Observer:    observer,
	})
	defer c.Close()

	err := c.Submit(context.Background(), commit.Request{
		Lane:    commit.Lane{Name: "append", Priority: commit.PriorityHigh},
		Records: 2,
		Bytes:   32,
		Build:   func(batch *engine.Batch) error { return batch.Set([]byte("k"), []byte("v")) },
	})
	if err != nil {
		t.Fatalf("Submit(): %v", err)
	}

	event := observer.onlyRequest(t)
	if event.Lane.Name != "append" || event.Lane.Priority != commit.PriorityHigh {
		t.Fatalf("request lane = %#v, want append/high", event.Lane)
	}
	if event.Records != 2 || event.Bytes != 32 {
		t.Fatalf("request sizing = records:%d bytes:%d, want 2/32", event.Records, event.Bytes)
	}
	if event.Duration <= 0 {
		t.Fatalf("request duration = %v, want positive", event.Duration)
	}
	if event.Err != nil {
		t.Fatalf("request err = %v, want nil", event.Err)
	}
}

func TestCoordinatorCloseRejectsSubmit(t *testing.T) {
	db := openTestDB(t)
	c := commit.NewCoordinator(db, commit.Config{FlushWindow: time.Millisecond, QueueSize: 8})
	c.Close()
	err := c.Submit(context.Background(), commit.Request{Build: func(batch *engine.Batch) error { return nil }})
	if !errors.Is(err, commit.ErrClosed) {
		t.Fatalf("Submit() err = %v, want closed", err)
	}
}

func openTestDB(t *testing.T) *engine.DB {
	t.Helper()
	db, err := engine.Open(t.TempDir(), engine.Options{})
	if err != nil {
		t.Fatalf("Open(): %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

type recordingObserver struct {
	mu       sync.Mutex
	batches  []commit.BatchEvent
	requests []commit.RequestEvent
	depths   []int
}

func (o *recordingObserver) SetQueueDepth(depth int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.depths = append(o.depths, depth)
}

func (o *recordingObserver) ObserveBatch(event commit.BatchEvent) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.batches = append(o.batches, event)
}

func (o *recordingObserver) ObserveRequest(event commit.RequestEvent) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.requests = append(o.requests, event)
}

func (o *recordingObserver) onlyBatch(t *testing.T) commit.BatchEvent {
	t.Helper()
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.batches) != 1 {
		t.Fatalf("observed batches = %d, want 1", len(o.batches))
	}
	return o.batches[0]
}

func (o *recordingObserver) onlyRequest(t *testing.T) commit.RequestEvent {
	t.Helper()
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.requests) != 1 {
		t.Fatalf("observed requests = %d, want 1", len(o.requests))
	}
	return o.requests[0]
}

func (o *recordingObserver) depthsSnapshot() []int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]int(nil), o.depths...)
}
