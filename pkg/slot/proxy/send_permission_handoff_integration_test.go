//go:build integration

package proxy

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"testing"
	"time"
)

// A transferred permit must stop consuming a waiting position before its owner
// resumes. Otherwise an arrival can receive busy while the real queue has room.
// Existing bounded-wait tests cover capacity, cancellation, deadlines and reuse;
// this case isolates the scheduler gap between handoff and waiter resumption.
func TestSendPermissionHandoffDoesNotRejectAvailableWaitPosition(t *testing.T) {
	previous := runtime.GOMAXPROCS(1)
	t.Cleanup(func() { runtime.GOMAXPROCS(previous) })
	s := &Store{}
	ctx, cancel := context.WithCancel(context.Background())
	finish := make(chan struct{})
	var workers sync.WaitGroup
	var releases []func()
	t.Cleanup(func() {
		cancel()
		close(finish)
		for _, release := range releases {
			if release != nil {
				release()
			}
		}
		workers.Wait()
	})
	for range sendPermissionMaxExecuting {
		release, err := s.acquireSendPermissionEnvelope(ctx, 0)
		if err != nil {
			t.Fatal(err)
		}
		releases = append(releases, release)
	}
	for range sendPermissionMaxWaiting {
		workers.Add(1)
		go func() {
			defer workers.Done()
			release, err := s.acquireSendPermissionEnvelope(ctx, 0)
			if err == nil {
				<-finish
				release()
			}
		}()
	}
	deadline := time.Now().Add(5 * time.Second)
	for s.permissionWaiting.Load() != sendPermissionMaxWaiting {
		if time.Now().After(deadline) {
			t.Fatal("waiting queue did not fill before its deadline")
		}
		runtime.Gosched()
	}
	// On one P, the releasing caller continues while the selected waiter is
	// runnable. The next arrival must be queued, then observe cancellation.
	releases[0]()
	releases[0] = nil
	arrival, stop := context.WithCancel(ctx)
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		stop()
	}()
	defer func() { stop(); <-stopped }()
	release, err := s.acquireSendPermissionEnvelope(arrival, 0)
	if release != nil {
		release()
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("arrival after permit handoff = %v; want cancellation after a freed waiting position", err)
	}
}

// Cancellation after assignment must return the reserved permit even if the
// assigned goroutine has not resumed to acknowledge ownership yet.
func TestSendPermissionCanceledHandoffReturnsPermit(t *testing.T) {
	previous := runtime.GOMAXPROCS(1)
	t.Cleanup(func() { runtime.GOMAXPROCS(previous) })
	s := &Store{}
	var releases []func()
	t.Cleanup(func() {
		for _, release := range releases {
			if release != nil {
				release()
			}
		}
	})
	for range sendPermissionMaxExecuting {
		release, err := s.acquireSendPermissionEnvelope(context.Background(), 0)
		if err != nil {
			t.Fatal(err)
		}
		releases = append(releases, release)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		release, err := s.acquireSendPermissionEnvelope(ctx, 0)
		if release != nil {
			release()
		}
		done <- err
	}()
	deadline := time.Now().Add(50 * time.Millisecond)
	for s.permissionWaiting.Load() != 1 {
		if time.Now().After(deadline) {
			cancel()
			<-done
			t.Fatal("waiter did not join before its deadline")
		}
		runtime.Gosched()
	}
	releases[0]()
	releases[0] = nil
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled handoff = %v; want context cancellation", err)
	}
	for i, release := range releases {
		if release != nil {
			release()
			releases[i] = nil
		}
	}
	for range sendPermissionMaxExecuting {
		release, err := s.acquireSendPermissionEnvelope(context.Background(), 0)
		if err != nil {
			t.Fatalf("permit leaked after canceled handoff: %v", err)
		}
		releases = append(releases, release)
	}
	if waiting := s.permissionWaiting.Load(); waiting != 0 {
		t.Fatalf("waiting after canceled handoff = %d; want zero", waiting)
	}
}
