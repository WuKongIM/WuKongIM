package replica

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestDurableLaneAcquireHonorsContextCancellation(t *testing.T) {
	r := &replica{}
	r.initDurableLane()
	release, err := r.acquireDurableLane(context.Background())
	if err != nil {
		t.Fatalf("first acquireDurableLane() error = %v", err)
	}
	defer release()

	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	_, err = r.acquireDurableLane(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second acquireDurableLane() error = %v, want DeadlineExceeded", err)
	}
}

func TestDurableLaneSerializesMutations(t *testing.T) {
	r := &replica{}
	r.initDurableLane()

	release, err := r.acquireDurableLane(context.Background())
	if err != nil {
		t.Fatalf("first acquireDurableLane() error = %v", err)
	}

	acquired := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		secondRelease, err := r.acquireDurableLane(context.Background())
		if err != nil {
			done <- err
			return
		}
		close(acquired)
		secondRelease()
		done <- nil
	}()

	select {
	case <-acquired:
		t.Fatal("second acquire completed before first release")
	case <-time.After(2 * time.Millisecond):
	}

	release()
	select {
	case <-acquired:
	case <-time.After(time.Second):
		t.Fatal("second acquire did not complete after release")
	}
	if err := <-done; err != nil {
		t.Fatalf("second acquire error = %v", err)
	}
}
