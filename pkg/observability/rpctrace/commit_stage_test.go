package rpctrace

import (
	"context"
	"reflect"
	"testing"
)

// Fixed span/owner capacity, exclusion, and immutable Finish must apply before
// the coordinator can transport these handles across its owner boundary.
func TestCommitStageOwnerBoundsAndFinish(t *testing.T) {
	r, err := NewRecorder(4)
	if err != nil {
		t.Fatal(err)
	}
	a := r.Start(context.Background(), SideServer, 21)
	b := r.Start(context.Background(), SideServer, 21)
	c := r.Start(context.Background(), SideServer, 21)
	if a == nil || b == nil || c == nil {
		t.Fatal("fixture admission")
	}
	o := NewCommitObservation(a, b, c, a)
	span := o.BeginStage(StageCommitQueue, a)
	before := b.Finish(ResultCanceled)
	want := before.Clone()
	span.End(ResultOK)
	span.End(ResultError)
	if !reflect.DeepEqual(want, *before) {
		t.Fatal("late completion changed published owner")
	}
	if len(before.Spans) != 1 || !before.Spans[0].Incomplete {
		t.Fatalf("early finish: %+v", before)
	}
	if sample := a.Finish(ResultOK); len(sample.Spans) != 0 {
		t.Fatalf("excluded owner: %+v", sample.Spans)
	}
	if sample := c.Finish(ResultOK); len(sample.Spans) != 1 || sample.Spans[0].Incomplete || sample.Spans[0].Result != ResultOK {
		t.Fatalf("open owner: %+v", sample.Spans)
	}
}

func TestCommitStageFullOwnerCannotCloseUnrelatedSpan(t *testing.T) {
	r, err := NewRecorder(1)
	if err != nil {
		t.Fatal(err)
	}
	trace := r.Start(context.Background(), SideServer, 21)
	o := NewCommitObservation(trace)
	for range MaxSpans {
		trace.Begin(StageHandler)
	}
	o.BeginStage(StageCommitAdmission, nil).End(ResultOK)
	sample := trace.Finish(ResultCanceled)
	if len(sample.Spans) != MaxSpans || sample.DroppedSpans != 1 {
		t.Fatalf("capacity: %+v", sample)
	}
	for _, span := range sample.Spans {
		if !span.Incomplete {
			t.Fatal("failed admission ended an existing span")
		}
	}
}

func TestCommitStageDisabledRemainsAllocationFree(t *testing.T) {
	if n := testing.AllocsPerRun(100, func() { (CommitObservation{}).BeginStage(StageCommitQueue, nil).End(ResultOK) }); n != 0 {
		t.Fatalf("disabled allocated %v", n)
	}
}
