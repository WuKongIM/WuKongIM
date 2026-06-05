package sched

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/transportv2/internal/core"
)

func TestEnqueueRejectsInvalidPriority(t *testing.T) {
	s := New(Config{})

	err := s.Enqueue(context.Background(), Item{
		Priority: core.Priority(99),
		Bytes:    1,
		Value:    "bad",
	})

	if !errors.Is(err, core.ErrInvalidPriority) {
		t.Fatalf("Enqueue() error = %v, want ErrInvalidPriority", err)
	}
}

func TestEnqueueEnforcesMaxBytes(t *testing.T) {
	s := New(Config{MaxBytes: 10})

	if err := s.Enqueue(context.Background(), Item{
		Priority: core.PriorityRPC,
		Bytes:    7,
		Value:    "first",
	}); err != nil {
		t.Fatalf("Enqueue(first) error = %v", err)
	}

	err := s.Enqueue(context.Background(), Item{
		Priority: core.PriorityRPC,
		Bytes:    4,
		Value:    "second",
	})

	if !errors.Is(err, core.ErrQueueFull) {
		t.Fatalf("Enqueue(second) error = %v, want ErrQueueFull", err)
	}
}

func TestEnqueueEnforcesMaxItems(t *testing.T) {
	s := New(Config{MaxItems: 1})

	if err := s.Enqueue(context.Background(), Item{
		Priority: core.PriorityRPC,
		Bytes:    1,
		Value:    "first",
	}); err != nil {
		t.Fatalf("Enqueue(first) error = %v", err)
	}

	err := s.Enqueue(context.Background(), Item{
		Priority: core.PriorityRPC,
		Bytes:    1,
		Value:    "second",
	})

	if !errors.Is(err, core.ErrQueueFull) {
		t.Fatalf("Enqueue(second) error = %v, want ErrQueueFull", err)
	}
}

func TestEnqueueRejectsItemLargerThanMaxBatchBytes(t *testing.T) {
	s := New(Config{MaxBatchBytes: 8})

	err := s.Enqueue(context.Background(), Item{
		Priority: core.PriorityRPC,
		Bytes:    9,
		Value:    "too-large",
	})

	if !errors.Is(err, core.ErrMsgTooLarge) {
		t.Fatalf("Enqueue() error = %v, want ErrMsgTooLarge", err)
	}
	if batch := s.NextBatch(); batch != nil {
		t.Fatalf("NextBatch() = %#v, want nil after rejected enqueue", batch)
	}
}

func TestNextBatchHonorsMaxBatchBytes(t *testing.T) {
	s := New(Config{
		MaxBatchFrames: 4,
		MaxBatchBytes:  8,
	})

	for _, item := range []Item{
		{Priority: core.PriorityRaft, Bytes: 5, Value: "first"},
		{Priority: core.PriorityRaft, Bytes: 4, Value: "second"},
	} {
		if err := s.Enqueue(context.Background(), item); err != nil {
			t.Fatalf("Enqueue(%v) error = %v", item.Value, err)
		}
	}

	batch := s.NextBatch()
	if len(batch) != 1 {
		t.Fatalf("NextBatch() len = %d, want 1", len(batch))
	}
	if batch[0].Value != "first" {
		t.Fatalf("NextBatch()[0].Value = %v, want first", batch[0].Value)
	}
	if batch[0].Bytes > 8 {
		t.Fatalf("NextBatch() bytes = %d, want <= 8", batch[0].Bytes)
	}
}

func TestWeightedBatchEventuallyIncludesLowerPriority(t *testing.T) {
	s := New(Config{
		MaxBatchFrames: 1,
		MaxBatchBytes:  8,
	})

	for i := 0; i < 16; i++ {
		if err := s.Enqueue(context.Background(), Item{
			Priority: core.PriorityRaft,
			Bytes:    8,
			Value:    "raft",
		}); err != nil {
			t.Fatalf("Enqueue(raft %d) error = %v", i, err)
		}
	}
	if err := s.Enqueue(context.Background(), Item{
		Priority: core.PriorityBulk,
		Bytes:    8,
		Value:    "bulk",
	}); err != nil {
		t.Fatalf("Enqueue(bulk) error = %v", err)
	}

	for i := 0; i < 16; i++ {
		batch := s.NextBatch()
		if len(batch) != 1 {
			t.Fatalf("NextBatch(%d) len = %d, want 1", i, len(batch))
		}
		if batch[0].Priority == core.PriorityBulk {
			return
		}
	}

	t.Fatal("NextBatch() did not include bulk item within weighted turns")
}

func TestWaitBatchBlocksWhileEmptyAndWakesWhenEnqueued(t *testing.T) {
	s := New(Config{})
	got := make(chan []Item, 1)
	errc := make(chan error, 1)

	go func() {
		batch, err := s.WaitBatch()
		if err != nil {
			errc <- err
			return
		}
		got <- batch
	}()

	select {
	case batch := <-got:
		t.Fatalf("WaitBatch returned early with %#v", batch)
	case err := <-errc:
		t.Fatalf("WaitBatch returned early with error %v", err)
	case <-time.After(25 * time.Millisecond):
	}

	if err := s.Enqueue(context.Background(), Item{
		Priority: core.PriorityControl,
		Bytes:    3,
		Value:    "wake",
	}); err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}

	select {
	case err := <-errc:
		t.Fatalf("WaitBatch() error = %v", err)
	case batch := <-got:
		if len(batch) != 1 || batch[0].Value != "wake" {
			t.Fatalf("WaitBatch() = %#v, want wake item", batch)
		}
	case <-time.After(time.Second):
		t.Fatal("WaitBatch did not wake after enqueue")
	}
}

func TestStopWakesWaitBatchAndReturnsQueuedItems(t *testing.T) {
	draining := New(Config{})
	if err := draining.Enqueue(context.Background(), Item{
		Priority: core.PriorityRPC,
		Bytes:    5,
		Value:    "drained",
	}); err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}
	drained := draining.Stop(errors.New("stop test"))
	if len(drained) != 1 || drained[0].Value != "drained" {
		t.Fatalf("Stop() = %#v, want drained item", drained)
	}

	s := New(Config{})
	errc := make(chan error, 1)
	go func() {
		_, err := s.WaitBatch()
		errc <- err
	}()

	select {
	case err := <-errc:
		t.Fatalf("WaitBatch returned early with error %v", err)
	case <-time.After(25 * time.Millisecond):
	}

	if drained := s.Stop(errors.New("stop test")); len(drained) != 0 {
		t.Fatalf("Stop(empty) = %#v, want no drained items", drained)
	}

	select {
	case err := <-errc:
		if !errors.Is(err, core.ErrStopped) {
			t.Fatalf("WaitBatch() error = %v, want ErrStopped", err)
		}
	case <-time.After(time.Second):
		t.Fatal("WaitBatch did not wake after Stop")
	}

	if err := s.Enqueue(context.Background(), Item{
		Priority: core.PriorityRPC,
		Value:    "after stop",
	}); !errors.Is(err, core.ErrStopped) {
		t.Fatalf("Enqueue(after stop) error = %v, want ErrStopped", err)
	}

	if _, err := s.WaitBatch(); !errors.Is(err, core.ErrStopped) {
		t.Fatalf("WaitBatch(after stop) error = %v, want ErrStopped", err)
	}
}
