//go:build integration

package multiraft

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/observability/rpctrace"
)

// This context witnesses the public ReadBarrier entering its wait select after
// registration/enqueue. It does not inspect or alter a Runtime/RawNode field.
type pausedWaitContext struct {
	context.Context
	waiting chan struct{}
	once    sync.Once
}

func (c *pausedWaitContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.waiting) })
	return c.Context.Done()
}

type pausedReadResult struct {
	sample *rpctrace.Sample
	err    error
}

func pausedObservationStart(rt *Runtime, ctx context.Context, id SlotID) (<-chan pausedReadResult, context.CancelFunc, <-chan struct{}) {
	child, cancel := context.WithCancel(ctx)
	witness := &pausedWaitContext{Context: child, waiting: make(chan struct{})}
	r, _ := rpctrace.NewRecorder(1)
	trace := r.Start(witness, rpctrace.SideServer, 91)
	done := make(chan pausedReadResult, 1)
	go func() {
		err := rt.ReadBarrier(rpctrace.With(witness, trace), id)
		done <- pausedReadResult{trace.Finish(rpctrace.ResultFor(err)), err}
	}()
	return done, cancel, witness.waiting
}
func pausedObservationWire(t *testing.T, s *rpctrace.Sample) *[5]rpctrace.BarrierOverlayCoverage {
	t.Helper()
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Coverage *struct {
			Paused *struct {
				Phases [5]rpctrace.BarrierOverlayCoverage
			} `json:"storage_count_paused"`
		} `json:"barrier_coverage"`
	}
	if err = json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	if wire.Coverage == nil || wire.Coverage.Paused == nil {
		t.Fatal("pause coverage missing after public full-count witness")
	}
	return &wire.Coverage.Paused.Phases
}
func requirePausedObservation(t *testing.T, s *rpctrace.Sample, want bool) {
	t.Helper()
	var positive bool
	for _, p := range pausedObservationWire(t, s) {
		positive = positive || p.Duration > 0
		if p.Duration > 0 && (p.Count == 0 || !p.Unfinished) {
			t.Fatal("caller-clipped pause lost its count or unfinished flag")
		}
	}
	if positive != want {
		t.Fatalf("pause positive=%v want=%v", positive, want)
	}
}
