package workqueue

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

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
