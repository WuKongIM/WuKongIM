package commit_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/db/internal/commit"
	"github.com/WuKongIM/WuKongIM/pkg/db/internal/dberrors"
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

func TestCoordinatorShardsCommitDifferentPartitionsConcurrently(t *testing.T) {
	db := openTestDB(t)
	c := commit.NewCoordinator(db, commit.Config{
		FlushWindow: time.Millisecond,
		QueueSize:   64,
		Shards:      4,
	})
	defer c.Close()

	var inFlight atomic.Int64
	var maxInFlight atomic.Int64
	entered := make(chan struct{}, 16)
	release := make(chan struct{})
	c.SetCommitFunc(func(batch *engine.Batch) error {
		current := inFlight.Add(1)
		for {
			previous := maxInFlight.Load()
			if current <= previous || maxInFlight.CompareAndSwap(previous, current) {
				break
			}
		}
		select {
		case entered <- struct{}{}:
		default:
		}
		<-release
		inFlight.Add(-1)
		return batch.Commit(false)
	})

	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for i := 0; i < 16; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- c.Submit(context.Background(), commit.Request{
				Partition: fmt.Sprintf("partition-%02d", i),
				Build: func(batch *engine.Batch) error {
					return batch.Set([]byte(fmt.Sprintf("key-%02d", i)), []byte("v"))
				},
			})
		}()
	}

	deadline := time.After(2 * time.Second)
	for maxInFlight.Load() < 2 {
		select {
		case <-entered:
		case <-deadline:
			close(release)
			wg.Wait()
			t.Fatalf("max concurrent commits = %d, want at least 2", maxInFlight.Load())
		}
	}
	close(release)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("Submit(): %v", err)
		}
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

func TestCoordinatorFinalizesRejectedRequestOnce(t *testing.T) {
	build := func(*engine.Batch) error { return nil }
	tests := []struct {
		name    string
		submit  func(commit.Request) error
		wantErr error
	}{
		{
			name: "nil coordinator",
			submit: func(req commit.Request) error {
				var c *commit.Coordinator
				return c.Submit(context.Background(), req)
			},
			wantErr: dberrors.ErrInvalidArgument,
		},
		{
			name: "nil database",
			submit: func(req commit.Request) error {
				return (&commit.Coordinator{}).Submit(context.Background(), req)
			},
			wantErr: dberrors.ErrInvalidArgument,
		},
		{
			name: "nil build",
			submit: func(req commit.Request) error {
				db := openTestDB(t)
				c := commit.NewCoordinator(db, commit.Config{QueueSize: 1})
				t.Cleanup(c.Close)
				req.Build = nil
				return c.Submit(context.Background(), req)
			},
			wantErr: dberrors.ErrInvalidArgument,
		},
		{
			name: "closed coordinator",
			submit: func(req commit.Request) error {
				db := openTestDB(t)
				c := commit.NewCoordinator(db, commit.Config{QueueSize: 1})
				c.Close()
				return c.Submit(context.Background(), req)
			},
			wantErr: commit.ErrClosed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var finalized atomic.Int64
			err := tt.submit(commit.Request{
				Build:    build,
				Finalize: func() { finalized.Add(1) },
			})
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Submit() err = %v, want %v", err, tt.wantErr)
			}
			if got := finalized.Load(); got != 1 {
				t.Fatalf("finalize count = %d, want 1", got)
			}
		})
	}
}

func TestCoordinatorFinalizesShardedRejectionOnce(t *testing.T) {
	db := openTestDB(t)
	c := commit.NewCoordinator(db, commit.Config{QueueSize: 1, Shards: 2})
	c.Close()

	var finalized atomic.Int64
	err := c.Submit(context.Background(), commit.Request{
		Partition: "partition-a",
		Build:     func(*engine.Batch) error { return nil },
		Finalize:  func() { finalized.Add(1) },
	})
	if !errors.Is(err, commit.ErrClosed) {
		t.Fatalf("Submit() err = %v, want %v", err, commit.ErrClosed)
	}
	if got := finalized.Load(); got != 1 {
		t.Fatalf("finalize count = %d, want 1", got)
	}
}

func TestCoordinatorFinalizesPreAdmissionCancellationOnce(t *testing.T) {
	db := openTestDB(t)
	observer := &recordingObserver{depthCh: make(chan int, 16)}
	c := commit.NewCoordinator(db, commit.Config{
		FlushWindow: time.Hour,
		QueueSize:   1,
		MaxRequests: 1,
		Observer:    observer,
	})
	release := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(release) })
		c.Close()
	})

	building := make(chan struct{})
	firstErr := make(chan error, 1)
	go func() {
		firstErr <- c.Submit(context.Background(), commit.Request{
			Build: func(batch *engine.Batch) error {
				close(building)
				<-release
				return batch.Set([]byte("first"), []byte("value"))
			},
		})
	}()
	waitSignal(t, building, "first request build")
	drainDepths(observer.depthCh)

	secondErr := make(chan error, 1)
	go func() {
		secondErr <- c.Submit(context.Background(), commit.Request{
			Build: func(batch *engine.Batch) error {
				return batch.Set([]byte("second"), []byte("value"))
			},
		})
	}()
	waitForQueueDepth(t, observer.depthCh, 1)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var finalized atomic.Int64
	err := c.Submit(ctx, commit.Request{
		Build:    func(*engine.Batch) error { return nil },
		Finalize: func() { finalized.Add(1) },
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Submit() err = %v, want %v", err, context.Canceled)
	}
	if got := finalized.Load(); got != 1 {
		t.Fatalf("finalize count = %d, want 1", got)
	}

	releaseOnce.Do(func() { close(release) })
	if err := waitResult(t, firstErr, "first request"); err != nil {
		t.Fatalf("first Submit(): %v", err)
	}
	if err := waitResult(t, secondErr, "second request"); err != nil {
		t.Fatalf("second Submit(): %v", err)
	}
}

func TestCoordinatorDoesNotFinalizeAdmittedRequestWhenCallerCancels(t *testing.T) {
	db := openTestDB(t)
	c := commit.NewCoordinator(db, commit.Config{QueueSize: 1, MaxRequests: 1})
	defer c.Close()

	ctx, cancel := context.WithCancel(context.Background())
	building := make(chan struct{})
	release := make(chan struct{})
	finalizedCh := make(chan struct{}, 1)
	var finalized atomic.Int64
	resultCh := make(chan error, 1)
	go func() {
		resultCh <- c.Submit(ctx, commit.Request{
			Build: func(batch *engine.Batch) error {
				close(building)
				<-release
				return batch.Set([]byte("key"), []byte("value"))
			},
			Finalize: func() {
				finalized.Add(1)
				finalizedCh <- struct{}{}
			},
		})
	}()

	waitSignal(t, building, "request build")
	cancel()
	if err := waitResult(t, resultCh, "canceled Submit"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Submit() err = %v, want %v", err, context.Canceled)
	}
	if got := finalized.Load(); got != 0 {
		t.Fatalf("finalize count after caller cancellation = %d, want 0", got)
	}

	close(release)
	waitSignal(t, finalizedCh, "request finalization")
	if got := finalized.Load(); got != 1 {
		t.Fatalf("finalize count after terminal completion = %d, want 1", got)
	}
}

func TestCoordinatorFinalizesAfterBuildError(t *testing.T) {
	db := openTestDB(t)
	c := commit.NewCoordinator(db, commit.Config{QueueSize: 1, MaxRequests: 1})
	defer c.Close()

	wantErr := errors.New("build failed")
	var finalized atomic.Int64
	err := c.Submit(context.Background(), commit.Request{
		Build:    func(*engine.Batch) error { return wantErr },
		Finalize: func() { finalized.Add(1) },
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Submit() err = %v, want %v", err, wantErr)
	}
	if got := finalized.Load(); got != 1 {
		t.Fatalf("finalize count = %d, want 1", got)
	}
}

func TestCoordinatorFinalizesAfterCommitError(t *testing.T) {
	db := openTestDB(t)
	c := commit.NewCoordinator(db, commit.Config{QueueSize: 1, MaxRequests: 1})
	defer c.Close()

	wantErr := errors.New("commit failed")
	c.SetCommitFunc(func(*engine.Batch) error { return wantErr })
	var finalized atomic.Int64
	err := c.Submit(context.Background(), commit.Request{
		Build:    func(batch *engine.Batch) error { return batch.Set([]byte("key"), []byte("value")) },
		Finalize: func() { finalized.Add(1) },
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Submit() err = %v, want %v", err, wantErr)
	}
	if got := finalized.Load(); got != 1 {
		t.Fatalf("finalize count = %d, want 1", got)
	}
}

func TestCoordinatorFinalizesAfterPublishError(t *testing.T) {
	db := openTestDB(t)
	c := commit.NewCoordinator(db, commit.Config{QueueSize: 1, MaxRequests: 1})
	defer c.Close()

	wantErr := errors.New("publish failed")
	var finalized atomic.Int64
	err := c.Submit(context.Background(), commit.Request{
		Build:    func(batch *engine.Batch) error { return batch.Set([]byte("key"), []byte("value")) },
		Publish:  func() error { return wantErr },
		Finalize: func() { finalized.Add(1) },
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Submit() err = %v, want %v", err, wantErr)
	}
	if got := finalized.Load(); got != 1 {
		t.Fatalf("finalize count = %d, want 1", got)
	}
}

func TestCoordinatorFinalizesSuccessfulRequestOnce(t *testing.T) {
	db := openTestDB(t)
	c := commit.NewCoordinator(db, commit.Config{QueueSize: 1, MaxRequests: 1})
	defer c.Close()

	var finalized atomic.Int64
	err := c.Submit(context.Background(), commit.Request{
		Build:    func(batch *engine.Batch) error { return batch.Set([]byte("key"), []byte("value")) },
		Finalize: func() { finalized.Add(1) },
	})
	if err != nil {
		t.Fatalf("Submit(): %v", err)
	}
	if got := finalized.Load(); got != 1 {
		t.Fatalf("finalize count = %d, want 1", got)
	}
}

func TestCoordinatorFinalizesBeforeSubmitReturns(t *testing.T) {
	db := openTestDB(t)
	synctest.Test(t, func(t *testing.T) {
		c := commit.NewCoordinator(db, commit.Config{QueueSize: 1, MaxRequests: 1})
		c.SetCommitFunc(func(*engine.Batch) error { return nil })
		releaseFinalize := make(chan struct{})
		var releaseOnce sync.Once
		defer func() {
			releaseOnce.Do(func() { close(releaseFinalize) })
			c.Close()
		}()

		var submitErr error
		finalizeStarted := false
		finalizeCompleted := false
		submitReturned := false
		go func() {
			submitErr = c.Submit(context.Background(), commit.Request{
				Build: func(batch *engine.Batch) error {
					return batch.Set([]byte("ordered"), []byte("value"))
				},
				Finalize: func() {
					finalizeStarted = true
					<-releaseFinalize
					finalizeCompleted = true
				},
			})
			submitReturned = true
		}()

		synctest.Wait()
		if !finalizeStarted {
			t.Fatal("Finalize did not start")
		}
		if finalizeCompleted {
			t.Fatal("Finalize completed before release")
		}
		if submitReturned {
			t.Fatalf("Submit() returned %v before Finalize completed", submitErr)
		}

		releaseOnce.Do(func() { close(releaseFinalize) })
		synctest.Wait()
		if !finalizeCompleted {
			t.Fatal("Finalize did not complete after release")
		}
		if !submitReturned {
			t.Fatal("Submit() did not return after Finalize completed")
		}
		if submitErr != nil {
			t.Fatalf("Submit(): %v", submitErr)
		}
	})
}

func TestCoordinatorFinalizesDeferredRequestOnceAfterCallerCancels(t *testing.T) {
	db := openTestDB(t)
	observer := &recordingObserver{depthCh: make(chan int, 32)}
	c := commit.NewCoordinator(db, commit.Config{
		FlushWindow: time.Hour,
		QueueSize:   1,
		MaxBytes:    3,
		Observer:    observer,
	})
	releaseFirst := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(releaseFirst) })
		c.Close()
	})

	firstBuilding := make(chan struct{})
	firstResult := make(chan error, 1)
	go func() {
		firstResult <- c.Submit(context.Background(), commit.Request{
			Bytes: 2,
			Build: func(batch *engine.Batch) error {
				close(firstBuilding)
				<-releaseFirst
				return batch.Set([]byte("first-deferred-test"), []byte("value"))
			},
		})
	}()
	waitForQueueObservation(t, observer.depthCh)
	drainDepths(observer.depthCh)

	deferredCtx, cancelDeferred := context.WithCancel(context.Background())
	deferredResult := make(chan error, 1)
	finalizedCh := make(chan struct{}, 1)
	var finalized atomic.Int64
	var deferredBuilt atomic.Bool
	go func() {
		deferredResult <- c.Submit(deferredCtx, commit.Request{
			Bytes: 2,
			Build: func(batch *engine.Batch) error {
				deferredBuilt.Store(true)
				return batch.Set([]byte("deferred"), []byte("value"))
			},
			Finalize: func() {
				finalized.Add(1)
				finalizedCh <- struct{}{}
			},
		})
	}()

	// With a one-hour flush window, reaching Build proves the second request
	// exceeded MaxBytes and was copied into the deferred queue.
	waitSignal(t, firstBuilding, "first deferred-path build")
	cancelDeferred()
	if err := waitResult(t, deferredResult, "deferred caller cancellation"); !errors.Is(err, context.Canceled) {
		t.Fatalf("deferred Submit() err = %v, want %v", err, context.Canceled)
	}
	if got := finalized.Load(); got != 0 {
		t.Fatalf("finalize count after caller cancellation = %d, want 0", got)
	}
	drainDepths(observer.depthCh)

	var queuedBuilt atomic.Bool
	queuedResult := make(chan error, 1)
	go func() {
		queuedResult <- c.Submit(context.Background(), commit.Request{
			Bytes: 0,
			Build: func(batch *engine.Batch) error {
				queuedBuilt.Store(true)
				return batch.Set([]byte("queued-behind-deferred"), []byte("value"))
			},
		})
	}()
	// Depth two is one deferred request plus the raw queued request. Keeping the
	// raw queue full lets the test observe the Close admission gate exactly.
	waitForQueueDepth(t, observer.depthCh, 2)

	closed := make(chan struct{})
	go func() {
		c.Close()
		close(closed)
	}()
	waitForCoordinatorClosed(t, c)

	releaseOnce.Do(func() { close(releaseFirst) })
	waitSignal(t, finalizedCh, "deferred request finalization")
	waitSignal(t, closed, "coordinator close")
	if got := finalized.Load(); got != 1 {
		t.Fatalf("finalize count after deferred terminal state = %d, want 1", got)
	}
	if deferredBuilt.Load() {
		t.Fatal("deferred Build ran after coordinator close")
	}
	if queuedBuilt.Load() {
		t.Fatal("queued Build ran after coordinator close")
	}
	if err := waitResult(t, firstResult, "first deferred-path request"); err != nil {
		t.Fatalf("first Submit(): %v", err)
	}
	if err := waitResult(t, queuedResult, "queued request behind deferred request"); !errors.Is(err, commit.ErrClosed) {
		t.Fatalf("queued Submit() err = %v, want %v", err, commit.ErrClosed)
	}
}

func TestCoordinatorFinalizesQueuedRequestOnClose(t *testing.T) {
	db := openTestDB(t)
	observer := &recordingObserver{depthCh: make(chan int, 32)}
	c := commit.NewCoordinator(db, commit.Config{
		FlushWindow: time.Hour,
		QueueSize:   4,
		MaxRequests: 2,
		Observer:    observer,
	})
	releaseCommit := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(releaseCommit) })
		c.Close()
	})

	commitStarted := make(chan struct{})
	c.SetCommitFunc(func(batch *engine.Batch) error {
		close(commitStarted)
		<-releaseCommit
		return batch.Commit(false)
	})

	firstResults := make(chan error, 2)
	for _, key := range []string{"first", "second"} {
		key := key
		go func() {
			firstResults <- c.Submit(context.Background(), commit.Request{
				Build: func(batch *engine.Batch) error {
					return batch.Set([]byte(key), []byte("value"))
				},
			})
		}()
	}
	waitSignal(t, commitStarted, "physical commit")
	drainDepths(observer.depthCh)

	queuedCtx, cancelQueued := context.WithCancel(context.Background())
	queuedResult := make(chan error, 1)
	finalizedCh := make(chan struct{}, 1)
	var finalized atomic.Int64
	go func() {
		queuedResult <- c.Submit(queuedCtx, commit.Request{
			Build: func(batch *engine.Batch) error {
				return batch.Set([]byte("queued"), []byte("value"))
			},
			Finalize: func() {
				finalized.Add(1)
				finalizedCh <- struct{}{}
			},
		})
	}()
	waitForQueueDepth(t, observer.depthCh, 1)
	cancelQueued()
	if err := waitResult(t, queuedResult, "queued caller cancellation"); !errors.Is(err, context.Canceled) {
		t.Fatalf("queued Submit() err = %v, want %v", err, context.Canceled)
	}
	if got := finalized.Load(); got != 0 {
		t.Fatalf("finalize count before coordinator close = %d, want 0", got)
	}

	closed := make(chan struct{})
	go func() {
		c.Close()
		close(closed)
	}()
	releaseOnce.Do(func() { close(releaseCommit) })
	waitSignal(t, closed, "coordinator close")
	waitSignal(t, finalizedCh, "queued request finalization")
	if got := finalized.Load(); got != 1 {
		t.Fatalf("finalize count after coordinator close = %d, want 1", got)
	}
	for i := 0; i < 2; i++ {
		if err := waitResult(t, firstResults, "committed request"); err != nil {
			t.Fatalf("committed Submit(): %v", err)
		}
	}
}

func waitSignal(t *testing.T, ch <-chan struct{}, name string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s", name)
	}
}

func waitResult(t *testing.T, ch <-chan error, name string) error {
	t.Helper()
	select {
	case err := <-ch:
		return err
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s", name)
		return nil
	}
}

func waitForQueueDepth(t *testing.T, depths <-chan int, want int) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case depth := <-depths:
			if depth >= want {
				return
			}
		case <-deadline:
			t.Fatalf("timed out waiting for queue depth %d", want)
		}
	}
}

func waitForQueueObservation(t *testing.T, depths <-chan int) {
	t.Helper()
	select {
	case <-depths:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for queue observation")
	}
}

func waitForCoordinatorClosed(t *testing.T, c *commit.Coordinator) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		err := c.Submit(ctx, commit.Request{Build: func(*engine.Batch) error { return nil }})
		if errors.Is(err, commit.ErrClosed) {
			return
		}
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("close probe Submit() err = %v, want canceled or closed", err)
		}
		select {
		case <-deadline:
			t.Fatal("timed out waiting for coordinator close gate")
		default:
		}
	}
}

func drainDepths(depths <-chan int) {
	for {
		select {
		case <-depths:
		default:
			return
		}
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
	depthCh  chan int
}

func (o *recordingObserver) SetQueueDepth(depth int) {
	o.mu.Lock()
	o.depths = append(o.depths, depth)
	o.mu.Unlock()
	select {
	case o.depthCh <- depth:
	default:
	}
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
