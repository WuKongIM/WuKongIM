package cluster

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestObserverLoopStopStopsFutureTicks(t *testing.T) {
	var ticks atomic.Int32

	loop := newObserverLoop(5*time.Millisecond, func(context.Context) {
		ticks.Add(1)
	})
	loop.Start(context.Background())

	deadline := time.Now().Add(100 * time.Millisecond)
	for ticks.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if ticks.Load() == 0 {
		t.Fatal("observer loop did not tick")
	}

	loop.Stop()
	stoppedAt := ticks.Load()
	time.Sleep(20 * time.Millisecond)
	if ticks.Load() != stoppedAt {
		t.Fatalf("observer loop kept ticking after Stop(): got %d, want %d", ticks.Load(), stoppedAt)
	}
}
