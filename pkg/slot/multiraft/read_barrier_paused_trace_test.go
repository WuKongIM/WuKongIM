package multiraft

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/observability/rpctrace"
)

// Failure matrix precedes producer hooks: prior-pause registration, multiple
// noncontiguous pauses crossing phases, caller clipping, concurrent finish and
// resumption, and disabled instrumentation. No test changes Raft scheduling.
func TestReadBarrierPausedCoverageCrossingPhases(t *testing.T) {
	start := time.Unix(1100, 0)
	o := &readBarrierTiming{}
	o.points[0] = start
	o.openCountPaused(start.Add(3))
	o.mark(1, start.Add(10))
	o.mark(2, start.Add(20))
	o.closeCountPaused(start.Add(25), false)
	o.openCountPaused(start.Add(27))
	o.mark(3, start.Add(30))
	o.closeCountPaused(start.Add(36), false)
	for i, want := range []time.Duration{7, 10, 8, 6, 0} {
		got := o.coverage[i][2]
		count := uint64(1)
		if i == 2 {
			count = 2
		}
		if i == 4 {
			count = 0
		}
		if got.Duration != want || got.Count != count || got.Unfinished {
			t.Fatalf("phase%d=%+v wantduration=%d count=%d", i, got, want, count)
		}
	}
}

func TestReadBarrierPausedCoverageRegistrationAndClip(t *testing.T) {
	start := time.Unix(1101, 0)
	g := &slot{readBarrierTracing: true, readCountPausedStarted: start.Add(-time.Second)}
	o := &readBarrierTiming{}
	o.points[0] = start
	if !g.registerReadTiming(o) {
		t.Fatal("registry setup")
	}
	o.mark(1, start.Add(10))
	o.closeCountPaused(start.Add(17), true)
	o.terminal = start.Add(17)
	before := o.coverage
	o.closeCountPaused(start.Add(100), false)
	o.openCountPaused(start.Add(101))
	if o.coverage != before || !o.open[2].IsZero() {
		t.Fatal("late pause callbacks changed clipped observation")
	}
	for i, want := range []time.Duration{10, 7, 0, 0, 0} {
		got := o.coverage[i][2]
		if got.Duration != want || (want > 0 && (got.Count != 1 || !got.Unfinished)) {
			t.Fatalf("phase%d clip=%+v", i, got)
		}
	}
	g.unregisterReadTiming(o)
}

func TestReadBarrierPausedCoverageDisabledDoesNotTakeOptionalLock(t *testing.T) {
	g := &slot{}
	g.mu.Lock()
	// Disabled hooks must not acquire mu, read a clock, or visit observers.
	g.setStorageCountPaused(true)
	g.setStorageCountPaused(false)
	g.mu.Unlock()
	if !g.readCountPausedStarted.IsZero() || g.storageCountPaused {
		t.Fatal("disabled tracing retained optional state")
	}
}

func TestReadBarrierPausedCoverageConcurrentFinish(t *testing.T) {
	for i := 0; i < 32; i++ {
		r, _ := rpctrace.NewRecorder(1)
		trace := r.Start(context.Background(), rpctrace.SideServer, 91)
		start := time.Now()
		o := &readBarrierTiming{}
		o.points[0] = start
		o.openCountPaused(start)
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); o.finish(context.Canceled) }()
		go func() { defer wg.Done(); o.closeCountPaused(time.Now(), false) }()
		wg.Wait()
		o.publish(trace)
		sample := trace.Finish(rpctrace.ResultCanceled)
		if sample.BarrierCoverage == nil || sample.BarrierCoverage.StorageCountPaused == nil {
			t.Fatal("finish lost whole pause group")
		}
		before, _ := json.Marshal(sample)
		o.closeCountPaused(time.Now(), false)
		o.finish(nil)
		after, _ := json.Marshal(sample)
		if string(before) != string(after) {
			t.Fatal("late callback mutated finished pause")
		}
	}
}
