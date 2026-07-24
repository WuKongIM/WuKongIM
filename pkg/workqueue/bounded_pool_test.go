package workqueue

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	goruntimeregistry "github.com/WuKongIM/WuKongIM/pkg/goroutine"
)

func TestBoundedPoolPublishesOwnedGoroutineAndPressureSnapshot(t *testing.T) {
	registry := goruntimeregistry.New()
	entered := make(chan struct{})
	release := make(chan struct{})
	pool, err := NewBoundedPool[int](BoundedPoolConfig{
		Name:       "gateway-auth",
		Workers:    1,
		QueueSize:  2,
		Goroutines: registry,
		Task:       goruntimeregistry.TaskGatewayAsyncAuth,
	}, func(context.Context, int) error {
		close(entered)
		<-release
		return nil
	})
	if err != nil {
		t.Fatalf("NewBoundedPool() error = %v", err)
	}
	if err := pool.Submit(context.Background(), 1); err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	<-entered

	task := waitForOwnedPoolSnapshot(t, registry, goruntimeregistry.TaskGatewayAsyncAuth)
	if task.Active != 3 {
		t.Fatalf("Active = %d, want dispatcher + ants worker + ants maintenance = 3", task.Active)
	}
	if task.BusyTasks != 1 || task.PoolCapacity != 1 {
		t.Fatalf("pool pressure = %+v, want busy=1 capacity=1", task)
	}

	close(release)
	if err := pool.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := registry.Group(goruntimeregistry.ModuleGateway).Wait(ctx); err != nil {
		t.Fatalf("gateway group Wait() error = %v", err)
	}
}

func TestBoundedPoolRoutesWorkerPanicToTaskOwner(t *testing.T) {
	panicEvents := make(chan goruntimeregistry.PanicEvent, 1)
	registry := goruntimeregistry.New(goruntimeregistry.WithPanicObserver(func(event goruntimeregistry.PanicEvent) {
		panicEvents <- event
	}))
	pool, err := NewBoundedPool[int](BoundedPoolConfig{
		Name:       "gateway-auth-panic",
		Workers:    1,
		QueueSize:  1,
		Goroutines: registry,
		Task:       goruntimeregistry.TaskGatewayAsyncAuth,
	}, func(context.Context, int) error {
		panic("worker panic")
	})
	if err != nil {
		t.Fatalf("NewBoundedPool() error = %v", err)
	}
	if err := pool.Submit(context.Background(), 1); err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	select {
	case event := <-panicEvents:
		if event.Task != goruntimeregistry.TaskGatewayAsyncAuth || event.Recovered != "worker panic" {
			t.Fatalf("panic event = %+v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for owned pool panic")
	}
	if err := pool.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	task := findOwnedTaskSnapshot(t, registry, goruntimeregistry.TaskGatewayAsyncAuth)
	if task.PanicCount != 1 {
		t.Fatalf("PanicCount = %d, want 1", task.PanicCount)
	}
}

func TestBoundedPoolKeepsOwnershipUntilTimedOutWorkerExits(t *testing.T) {
	registry := goruntimeregistry.New()
	entered := make(chan struct{})
	release := make(chan struct{})
	pool, err := NewBoundedPool[int](BoundedPoolConfig{
		Name:           "gateway-auth-timeout",
		Workers:        1,
		QueueSize:      1,
		ReleaseTimeout: 10 * time.Millisecond,
		Goroutines:     registry,
		Task:           goruntimeregistry.TaskGatewayAsyncAuth,
	}, func(context.Context, int) error {
		close(entered)
		<-release
		return nil
	})
	if err != nil {
		t.Fatalf("NewBoundedPool() error = %v", err)
	}
	if err := pool.Submit(context.Background(), 1); err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	if err := pool.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Close() error = %v, want deadline exceeded", err)
	}
	task := findOwnedTaskSnapshot(t, registry, goruntimeregistry.TaskGatewayAsyncAuth)
	if task.Active == 0 || task.BusyTasks != 1 {
		t.Fatalf("timed-out pool snapshot = %+v, want live worker ownership", task)
	}
	close(release)
	waitCtx, waitCancel := context.WithTimeout(context.Background(), time.Second)
	defer waitCancel()
	if err := registry.Group(goruntimeregistry.ModuleGateway).Wait(waitCtx); err != nil {
		t.Fatalf("gateway group Wait() error = %v", err)
	}
}

func waitForOwnedPoolSnapshot(t *testing.T, registry *goruntimeregistry.Registry, taskID goruntimeregistry.TaskID) goruntimeregistry.TaskSnapshot {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		for _, module := range registry.Snapshot().Modules {
			for _, task := range module.Tasks {
				if task.Task == taskID && task.BusyTasks == 1 {
					return task
				}
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("task %q did not publish busy pool snapshot", taskID)
	return goruntimeregistry.TaskSnapshot{}
}

func findOwnedTaskSnapshot(t *testing.T, registry *goruntimeregistry.Registry, taskID goruntimeregistry.TaskID) goruntimeregistry.TaskSnapshot {
	t.Helper()
	for _, module := range registry.Snapshot().Modules {
		for _, task := range module.Tasks {
			if task.Task == taskID {
				return task
			}
		}
	}
	t.Fatalf("task %q missing from snapshot", taskID)
	return goruntimeregistry.TaskSnapshot{}
}

func TestBoundedPoolRejectsFullAdmission(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	pool, err := NewBoundedPool[int](BoundedPoolConfig{
		Name:      "test",
		Workers:   1,
		QueueSize: 1,
	}, func(ctx context.Context, item int) error {
		if item == 1 {
			close(entered)
			<-release
		}
		return nil
	})
	if err != nil {
		t.Fatalf("NewBoundedPool() error = %v", err)
	}
	defer func() {
		close(release)
		_ = pool.Close(context.Background())
	}()

	if err := pool.Submit(context.Background(), 1); err != nil {
		t.Fatalf("Submit(first) error = %v", err)
	}
	<-entered

	if err := pool.Submit(context.Background(), 2); err != nil {
		t.Fatalf("Submit(second) error = %v", err)
	}
	if err := pool.Submit(context.Background(), 3); !errors.Is(err, ErrFull) {
		t.Fatalf("Submit(third) error = %v, want ErrFull", err)
	}
}

func TestBoundedPoolSubmitWaitHonorsCallerContext(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	pool, err := NewBoundedPool[int](BoundedPoolConfig{
		Name:      "test",
		Workers:   1,
		QueueSize: 1,
	}, func(ctx context.Context, item int) error {
		if item == 1 {
			close(entered)
			<-release
		}
		return nil
	})
	if err != nil {
		t.Fatalf("NewBoundedPool() error = %v", err)
	}
	defer func() {
		close(release)
		_ = pool.Close(context.Background())
	}()

	if err := pool.Submit(context.Background(), 1); err != nil {
		t.Fatalf("Submit(first) error = %v", err)
	}
	<-entered
	if err := pool.Submit(context.Background(), 2); err != nil {
		t.Fatalf("Submit(second) error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := pool.SubmitWait(ctx, 3); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("SubmitWait() error = %v, want deadline exceeded", err)
	}
}

func TestBoundedPoolCloseDrainsAcceptedWorkAndRejectsNewSubmissions(t *testing.T) {
	var ran atomic.Int64
	pool, err := NewBoundedPool[int](BoundedPoolConfig{
		Name:      "test",
		Workers:   1,
		QueueSize: 4,
	}, func(ctx context.Context, item int) error {
		ran.Add(1)
		return nil
	})
	if err != nil {
		t.Fatalf("NewBoundedPool() error = %v", err)
	}

	for i := 0; i < 4; i++ {
		if err := pool.Submit(context.Background(), i); err != nil {
			t.Fatalf("Submit(%d) error = %v", i, err)
		}
	}
	if err := pool.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if got := ran.Load(); got != 4 {
		t.Fatalf("ran = %d, want 4 accepted tasks drained", got)
	}
	if err := pool.Submit(context.Background(), 5); !errors.Is(err, ErrClosed) {
		t.Fatalf("Submit(after close) error = %v, want ErrClosed", err)
	}
}
