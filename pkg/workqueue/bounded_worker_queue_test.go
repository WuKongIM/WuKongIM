package workqueue

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	goruntimeregistry "github.com/WuKongIM/WuKongIM/pkg/goroutine"
)

func TestBoundedWorkerQueuePublishesOwnedWorkersAndPoolState(t *testing.T) {
	registry := goruntimeregistry.New()
	started := make(chan struct{})
	release := make(chan struct{})
	queue, err := NewBoundedWorkerQueue[int](BoundedWorkerQueueConfig{
		Name:       "delivery-manager",
		Goroutines: registry,
		Task:       goruntimeregistry.TaskDeliveryManagerAsync,
		Workers:    1,
		QueueSize:  1,
	}, func(context.Context, int) error {
		close(started)
		<-release
		return nil
	})
	if err != nil {
		t.Fatalf("NewBoundedWorkerQueue() error = %v", err)
	}
	if err := queue.Submit(context.Background(), 1); err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	waitForBatchPoolSignal(t, started, "owned worker start")

	task := waitForOwnedPoolSnapshot(t, registry, goruntimeregistry.TaskDeliveryManagerAsync)
	if task.Active != 1 {
		t.Fatalf("Active = %d, want one direct worker", task.Active)
	}
	if task.PoolCapacity != 1 {
		t.Fatalf("PoolCapacity = %d, want 1", task.PoolCapacity)
	}

	close(release)
	if err := queue.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := registry.Group(goruntimeregistry.ModuleDelivery).Wait(context.Background()); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
}

func TestBoundedWorkerQueueSubmitWaitWaitsForQueueSpace(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	queue, err := NewBoundedWorkerQueue[int](BoundedWorkerQueueConfig{
		Name:      "test-direct",
		Workers:   1,
		QueueSize: 1,
	}, func(context.Context, int) error {
		select {
		case <-started:
		default:
			close(started)
			<-release
		}
		return nil
	})
	if err != nil {
		t.Fatalf("NewBoundedWorkerQueue() error = %v", err)
	}
	defer func() {
		releaseOnce.Do(func() { close(release) })
		_ = queue.Close(context.Background())
	}()

	if err := queue.SubmitWait(context.Background(), 1); err != nil {
		t.Fatalf("SubmitWait(first) error = %v", err)
	}
	waitForBatchPoolSignal(t, started, "first item start")
	if err := queue.SubmitWait(context.Background(), 2); err != nil {
		t.Fatalf("SubmitWait(second) error = %v", err)
	}

	result := make(chan error, 1)
	go func() {
		result <- queue.SubmitWait(context.Background(), 3)
	}()
	select {
	case err := <-result:
		t.Fatalf("SubmitWait(third) returned before queue space: %v", err)
	case <-time.After(20 * time.Millisecond):
	}

	releaseOnce.Do(func() { close(release) })
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("SubmitWait(third) error = %v, want nil", err)
		}
	case <-time.After(time.Second):
		t.Fatal("SubmitWait(third) did not return after queue space opened")
	}
}

func TestBoundedWorkerQueueCloseRejectsWaitingSubmit(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	queue, err := NewBoundedWorkerQueue[int](BoundedWorkerQueueConfig{
		Name:      "test-direct",
		Workers:   1,
		QueueSize: 1,
	}, func(context.Context, int) error {
		select {
		case <-started:
		default:
			close(started)
			<-release
		}
		return nil
	})
	if err != nil {
		t.Fatalf("NewBoundedWorkerQueue() error = %v", err)
	}
	defer releaseOnce.Do(func() { close(release) })

	if err := queue.SubmitWait(context.Background(), 1); err != nil {
		t.Fatalf("SubmitWait(first) error = %v", err)
	}
	waitForBatchPoolSignal(t, started, "first item start")
	if err := queue.SubmitWait(context.Background(), 2); err != nil {
		t.Fatalf("SubmitWait(second) error = %v", err)
	}

	result := make(chan error, 1)
	go func() {
		result <- queue.SubmitWait(context.Background(), 3)
	}()
	time.Sleep(time.Millisecond)

	closeDone := make(chan error, 1)
	go func() {
		closeDone <- queue.Close(context.Background())
	}()
	select {
	case err := <-result:
		if !errors.Is(err, ErrClosed) {
			t.Fatalf("SubmitWait(third) error = %v, want ErrClosed", err)
		}
	case <-time.After(time.Second):
		t.Fatal("SubmitWait(third) did not return after Close")
	}

	releaseOnce.Do(func() { close(release) })
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close() did not return after handler release")
	}
}

func TestBoundedWorkerQueueCloseDrainsAcceptedWork(t *testing.T) {
	var ran atomic.Int64
	queue, err := NewBoundedWorkerQueue[int](BoundedWorkerQueueConfig{
		Name:      "test-direct",
		Workers:   1,
		QueueSize: 4,
	}, func(context.Context, int) error {
		ran.Add(1)
		return nil
	})
	if err != nil {
		t.Fatalf("NewBoundedWorkerQueue() error = %v", err)
	}

	for i := 0; i < 4; i++ {
		if err := queue.SubmitWait(context.Background(), i); err != nil {
			t.Fatalf("SubmitWait(%d) error = %v", i, err)
		}
	}
	if err := queue.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if got := ran.Load(); got != 4 {
		t.Fatalf("ran = %d, want 4 accepted tasks drained", got)
	}
	if err := queue.SubmitWait(context.Background(), 5); !errors.Is(err, ErrClosed) {
		t.Fatalf("SubmitWait(after close) error = %v, want ErrClosed", err)
	}
}
