//go:build integration

package multiraft

import (
	"context"
	"encoding/json"
	"reflect"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/observability/rpctrace"
)

// Exact interval arithmetic cannot be inferred from a real scheduler's delays.
// These virtual-clock cases cover the failure matrix before implementation;
// the public Runtime tests also require the same immutable coverage contract.
type coverageWireOverlay struct {
	Duration   time.Duration `json:"duration"`
	Count      uint64        `json:"count"`
	Unfinished bool          `json:"unfinished"`
}
type coverageWirePhase struct {
	Stage    string              `json:"stage"`
	Observed bool                `json:"observed"`
	Save     coverageWireOverlay `json:"save"`
	Send     coverageWireOverlay `json:"send"`
}
type coverageWire struct {
	Phases [5]coverageWirePhase `json:"phases"`
}

func requireCoverageWire(t *testing.T, sample *rpctrace.Sample) coverageWire {
	t.Helper()
	data, err := json.Marshal(sample)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Coverage *coverageWire `json:"barrier_coverage"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Coverage == nil {
		t.Fatal("same-barrier cumulative coverage missing")
	}
	return *decoded.Coverage
}

func newCoverageClock(t *testing.T) (*readBarrierTiming, *rpctrace.Trace, func(int)) {
	t.Helper()
	r, _ := rpctrace.NewRecorder(1)
	trace := r.Start(context.Background(), rpctrace.SideServer, 91)
	base := time.Now()
	timing := &readBarrierTiming{}
	timing.points[0] = base
	step := func(ns int) {
		t.Helper()
		d := base.Add(time.Duration(ns)).Sub(time.Now())
		if d < 0 {
			t.Fatal("test clock moved backwards")
		}
		if d > 0 {
			time.Sleep(d)
		}
	}
	return timing, trace, step
}

func TestReadBarrierCoverageBeyondDetailCap(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o, tr, step := newCoverageClock(t)
		step(10)
		o.mark(1, time.Now())
		step(20)
		o.mark(2, time.Now())
		op := func(start, end int, send bool) {
			step(start)
			stage := rpctrace.StageSlotSaveEntries
			if send {
				stage = rpctrace.StageSlotRaftSend
			}
			o.openOverlay(stage, time.Now(), send)
			step(end)
			o.closeOverlay(time.Now(), nil, send)
		}
		op(25, 30, false)
		op(31, 33, true)
		op(40, 50, false)
		op(51, 53, true)
		op(60, 80, false)
		op(81, 83, true)
		op(85, 87, true)
		op(90, 100, false)
		o.mark(3, time.Now())
		op(100, 110, false)
		op(110, 115, true)
		step(120)
		o.mark(4, time.Now())
		step(130)
		o.finish(nil)
		o.publish(tr)
		s := tr.Finish(rpctrace.ResultOK)
		c := requireCoverageWire(t, s)
		if len(s.Spans) != 9 || s.DroppedSpans != 6 {
			t.Fatalf("detail cap changed: spans=%d drops=%d", len(s.Spans), s.DroppedSpans)
		}
		for i, p := range c.Phases {
			if !p.Observed {
				t.Fatalf("reached phase %d unknown", i)
			}
		}
		if c.Phases[2].Save != (coverageWireOverlay{Duration: 45, Count: 4}) || c.Phases[2].Send != (coverageWireOverlay{Duration: 8, Count: 4}) || c.Phases[3].Save != (coverageWireOverlay{Duration: 10, Count: 1}) || c.Phases[3].Send != (coverageWireOverlay{Duration: 5, Count: 1}) {
			t.Fatalf("capped or misallocated cumulative coverage: %+v", c)
		}
		for _, i := range []int{0, 1, 4} {
			if c.Phases[i].Save != (coverageWireOverlay{}) || c.Phases[i].Send != (coverageWireOverlay{}) {
				t.Fatalf("invented operation in phase %d", i)
			}
		}
	})
}

func TestReadBarrierCoveragePriorSaveCrossesAllPhases(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o, tr, step := newCoverageClock(t)
		g := &slot{readSaveStarted: time.Now().Add(-5), readSaveStage: rpctrace.StageSlotSaveEntries}
		if !g.registerReadTiming(o) {
			t.Fatal("registry rejected first observer")
		}
		for i := 1; i <= 4; i++ {
			step(i * 10)
			o.mark(i, time.Now())
		}
		step(45)
		o.closeOverlay(time.Now(), nil, false)
		step(50)
		o.finish(nil)
		g.unregisterReadTiming(o)
		o.publish(tr)
		c := requireCoverageWire(t, tr.Finish(rpctrace.ResultOK))
		for i, p := range c.Phases {
			want := time.Duration(10)
			if i == 4 {
				want = 5
			}
			if !p.Observed || p.Save != (coverageWireOverlay{Duration: want, Count: 1}) || p.Send != (coverageWireOverlay{}) {
				t.Fatalf("phase %d prior/cross-boundary coverage=%+v", i, p)
			}
		}
	})
}

func TestReadBarrierCoverageCanceledAfterDetailCapImmutable(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		o, tr, step := newCoverageClock(t)
		o.openOverlay(rpctrace.StageSlotSaveEntries, time.Now(), false)
		step(2)
		o.closeOverlay(time.Now(), nil, false)
		step(3)
		o.openOverlay(rpctrace.StageSlotSaveEntries, time.Now(), false)
		step(4)
		o.closeOverlay(time.Now(), nil, false)
		step(5)
		o.openOverlay(rpctrace.StageSlotSaveEntries, time.Now(), false)
		step(10)
		o.finish(context.Canceled)
		o.publish(tr)
		s := tr.Finish(rpctrace.ResultCanceled)
		before := s.Clone()
		c := requireCoverageWire(t, s)
		if c.Phases[0].Save != (coverageWireOverlay{Duration: 8, Count: 3, Unfinished: true}) || !c.Phases[0].Observed {
			t.Fatalf("third operation not clipped: %+v", c)
		}
		for i := 1; i < 5; i++ {
			if c.Phases[i].Observed || c.Phases[i].Save != (coverageWireOverlay{}) || c.Phases[i].Send != (coverageWireOverlay{}) {
				t.Fatalf("unreached phase %d treated as known", i)
			}
		}
		step(20)
		o.closeOverlay(time.Now(), nil, false)
		o.mark(1, time.Now())
		o.finish(nil)
		o.publish(tr)
		if !reflect.DeepEqual(s, &before) {
			t.Fatal("late completion changed published coverage")
		}
	})
}

func TestReadBarrierCoverageConcurrentFinishCloseExactlyOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		for i := 0; i < 32; i++ {
			o, tr, step := newCoverageClock(t)
			o.openOverlay(rpctrace.StageSlotSaveEntries, time.Now(), false)
			step(10)
			var wg sync.WaitGroup
			wg.Add(2)
			go func() { defer wg.Done(); o.closeOverlay(time.Now(), nil, false) }()
			go func() { defer wg.Done(); o.finish(context.Canceled) }()
			wg.Wait()
			o.publish(tr)
			c := requireCoverageWire(t, tr.Finish(rpctrace.ResultCanceled))
			got := c.Phases[0].Save
			if got.Duration != 10 || got.Count != 1 {
				t.Fatalf("finish/close double-counted: %+v", got)
			}
		}
	})
}
