package multiraft

import (
	"context"
	"errors"
	"testing"
	"time"
)

type readBarrierProbe struct {
	schedulerObserverNop
	results []string
}

func (p *readBarrierProbe) ObserveSlotReadBarrier(result string, d time.Duration) {
	if d < 0 {
		panic("negative duration")
	}
	p.results = append(p.results, result)
}

// Read barrier waits are reported with a fixed result set, so a slow or busy
// Slot is visible without per-caller or per-Slot series.
func TestReadBarrierObserverClassifiesFixedResults(t *testing.T) {
	p := &readBarrierProbe{}
	for _, err := range []error{nil, ErrNotLeader, ErrSlotBusy, context.Canceled, context.DeadlineExceeded, ErrRuntimeClosed, errors.New("x")} {
		observeReadBarrier(p, err, time.Millisecond)
	}
	want := []string{"ok", "not_leader", "busy", "canceled", "deadline", "error", "error"}
	if len(p.results) != len(want) {
		t.Fatalf("results = %v", p.results)
	}
	for i := range want {
		if p.results[i] != want[i] {
			t.Fatalf("result %d = %q, want %q", i, p.results[i], want[i])
		}
	}
	// Observers without the capability are ignored.
	observeReadBarrier(schedulerObserverNop{}, nil, time.Millisecond)
	observeReadBarrier(nil, nil, time.Millisecond)
}

// schedulerObserverNop satisfies SchedulerObserver without any extra capability.
type schedulerObserverNop struct{}

func (schedulerObserverNop) SetSchedulerWorkers(int)                    {}
func (schedulerObserverNop) SetSchedulerInflight(int)                   {}
func (schedulerObserverNop) SetSchedulerState(SchedulerStateEvent)      {}
func (schedulerObserverNop) ObserveSchedulerAdmission(string)           {}
func (schedulerObserverNop) ObserveSchedulerTask(string, time.Duration) {}
