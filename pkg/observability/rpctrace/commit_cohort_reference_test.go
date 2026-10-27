package rpctrace

import (
	"context"
	"math"
	"sync"
	"testing"
)

func TestCommitCohortReferenceReusesRecorderNamespaceAndIndependentSequence(t *testing.T) {
	r, err := NewRecorder(1)
	if err != nil {
		t.Fatal(err)
	}
	trace := r.Start(context.Background(), SideServer, 21)
	if trace == nil || !trace.Bind([16]byte{1}, 101) {
		t.Fatal("trace fixture binding")
	}
	observation := NewCommitObservation(trace)
	first := NewCommitCohortRef(observation)
	if first.Instance != r.prefix || first.Attempt != 1 {
		t.Fatalf("cohort ref=%+v, want opaque recorder namespace and independent sequence", first)
	}
	if first.Instance == trace.ID() {
		t.Fatal("cohort instance copied bound business RPC identity")
	}
	if got := testing.AllocsPerRun(100, func() { _ = NewCommitCohortRef(observation) }); got != 0 {
		t.Fatalf("hot cohort identity allocated=%v", got)
	}
	trace.Finish(ResultOK)
	before := r.cohortSequence.Load()
	if ref := NewCommitCohortRef(observation); ref.Instance != "" || ref.Attempt != 0 {
		t.Fatalf("finished trace generated new cohort ref=%+v", ref)
	}
	if r.cohortSequence.Load() != before {
		t.Fatal("finished observation consumed an identity")
	}
}

func TestCommitCohortReferenceExhaustionIsSaturatingAndObservationOnly(t *testing.T) {
	r, err := NewRecorder(1)
	if err != nil {
		t.Fatal(err)
	}
	trace := r.Start(context.Background(), SideServer, 21)
	if trace == nil {
		t.Fatal("trace fixture admission")
	}
	defer trace.Finish(ResultOK)
	observation := NewCommitObservation(trace)
	r.cohortSequence.Store(math.MaxUint64 - 1)
	refs := make(chan CommitCohortRef, 8)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() { defer wg.Done(); refs <- NewCommitCohortRef(observation) }()
	}
	wg.Wait()
	close(refs)
	valid := 0
	for ref := range refs {
		if ref.Instance != "" || ref.Attempt != 0 {
			valid++
			if ref.Instance != r.prefix || ref.Attempt != math.MaxUint64 {
				t.Fatalf("counter wrapped/forged cohort: %+v", ref)
			}
		}
	}
	if valid != 1 || r.cohortSequence.Load() != math.MaxUint64 {
		t.Fatalf("counter terminal valid=%d sequence=%d", valid, r.cohortSequence.Load())
	}
	if r.Active() != 1 || r.Dropped() != 0 {
		t.Fatal("identity exhaustion changed business-independent recorder admission")
	}
	if ref := NewCommitCohortRef(CommitObservation{}); ref.Instance != "" || ref.Attempt != 0 {
		t.Fatalf("nil observation produced ref=%+v", ref)
	}
}
