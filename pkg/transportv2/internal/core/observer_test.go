package core

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestObserverDrainDoesNotBlockWhenSinkIsSlow(t *testing.T) {
	sink := &blockingObserver{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	drain := NewObserverDrain(sink)
	defer func() {
		close(sink.release)
		drain.Stop()
	}()

	drain.ObserveTransport(Event{Name: "first"})
	waitCoreClosed(t, sink.entered)

	done := make(chan struct{})
	go func() {
		for i := 0; i < 4096; i++ {
			drain.ObserveTransport(Event{Name: "overflow"})
		}
		close(done)
	}()
	waitCoreClosed(t, done)
}

type blockingObserver struct {
	enteredOnce atomic.Bool
	entered     chan struct{}
	release     chan struct{}
}

func (o *blockingObserver) ObserveTransport(Event) {
	if o.enteredOnce.CompareAndSwap(false, true) {
		close(o.entered)
	}
	<-o.release
}

func waitCoreClosed(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for channel")
	}
}
