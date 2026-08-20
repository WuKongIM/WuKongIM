package multiraft

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRuntimeDoesNotPublishOlderInflightAfterPhysicalZero(t *testing.T) {
	observer := newBlockingSchedulerInflightObserver()
	runtime := &Runtime{opts: Options{Observer: observer}}
	runtime.inflight.Store(1)
	observer.Arm()
	olderDone := make(chan struct{})
	go func() {
		runtime.observeSchedulerInflight()
		close(olderDone)
	}()
	select {
	case <-observer.blocked:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for older inflight=1 publication")
	}

	runtime.inflight.Store(0)
	newerDone := make(chan struct{})
	go func() {
		runtime.observeSchedulerInflight()
		close(newerDone)
	}()
	select {
	case <-newerDone:
	case <-time.After(25 * time.Millisecond):
		// The corrected path serializes this newer publication behind the blocked
		// older callback. Releasing the callback below lets the zero publish last.
	}

	close(observer.release)
	select {
	case <-olderDone:
	case <-time.After(time.Second):
		t.Fatal("timed out releasing older publication")
	}
	select {
	case <-newerDone:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for physical zero publication")
	}
	if got := observer.Inflight(); got != 0 {
		t.Fatalf("published scheduler inflight = %d, want physical zero", got)
	}
}

type blockingSchedulerInflightObserver struct {
	mu       sync.Mutex
	inflight int
	armed    atomic.Bool
	blocked  chan struct{}
	release  chan struct{}
}

func newBlockingSchedulerInflightObserver() *blockingSchedulerInflightObserver {
	return &blockingSchedulerInflightObserver{
		blocked: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (o *blockingSchedulerInflightObserver) SetSchedulerWorkers(int) {}

func (o *blockingSchedulerInflightObserver) SetSchedulerInflight(inflight int) {
	if inflight == 1 && o.armed.CompareAndSwap(true, false) {
		close(o.blocked)
		<-o.release
	}
	o.mu.Lock()
	o.inflight = inflight
	o.mu.Unlock()
}

func (o *blockingSchedulerInflightObserver) SetSchedulerState(SchedulerStateEvent) {}

func (o *blockingSchedulerInflightObserver) ObserveSchedulerAdmission(string) {}

func (o *blockingSchedulerInflightObserver) ObserveSchedulerTask(string, time.Duration) {}

func (o *blockingSchedulerInflightObserver) Arm() {
	o.armed.Store(true)
}

func (o *blockingSchedulerInflightObserver) Inflight() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.inflight
}
