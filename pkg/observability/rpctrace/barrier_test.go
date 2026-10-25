package rpctrace_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/observability/rpctrace"
)

// Failure matrix: parallel Slot groups must not be summed, a shorter late
// group must not replace the critical group, overflow must drop the whole
// group, invalid timestamps/stages must not escape, and late publishers must
// not mutate the detached sample. Commit phases share the same closed cap.
func TestBarrierTraceRetainsLongestWholeGroup(t *testing.T) {
	r, _ := rpctrace.NewRecorder(1)
	tr := r.Start(context.Background(), rpctrace.SideServer, 13)
	base := time.Now()
	group := func(stage rpctrace.Stage, duration time.Duration) [rpctrace.MaxBarrierSpans]rpctrace.HistoricalSpan {
		return [rpctrace.MaxBarrierSpans]rpctrace.HistoricalSpan{{Stage: stage, Start: base, End: base.Add(duration), Result: rpctrace.ResultOK}}
	}
	tr.RetainLongestBarrier(base, base.Add(7*time.Nanosecond), group(rpctrace.StageSlotRawQuorum, 7*time.Nanosecond), 1, 0)
	tr.RetainLongestBarrier(base, base.Add(3*time.Nanosecond), group(rpctrace.StageSlotApplyWait, 3*time.Nanosecond), 1, 0)
	s := tr.Finish(rpctrace.ResultOK)
	if s.BarrierGroups != 2 || len(s.Spans) != 1 || s.Spans[0].Stage != rpctrace.StageSlotRawQuorum || s.Spans[0].Duration != 7*time.Nanosecond {
		t.Fatalf("parallel groups were summed/replaced: %+v", s)
	}
	before := s.Clone()
	tr.RetainLongestBarrier(base, base.Add(time.Hour), group(rpctrace.StageSlotApplyWait, time.Hour), 1, 5)
	if !reflect.DeepEqual(s, &before) || r.Active() != 0 {
		t.Fatal("late group mutated sample or leaked credit")
	}
}

func TestBarrierTraceCapacityDropsWholeGroup(t *testing.T) {
	r, _ := rpctrace.NewRecorder(1)
	tr := r.Start(context.Background(), rpctrace.SideServer, 13)
	for i := 0; i < rpctrace.MaxSpans-1; i++ {
		tr.Begin(rpctrace.StageHandler).End(rpctrace.ResultOK)
	}
	at := time.Now()
	var spans [rpctrace.MaxBarrierSpans]rpctrace.HistoricalSpan
	spans[0] = rpctrace.HistoricalSpan{Stage: rpctrace.StageSlotControlWait, Start: at, End: at, Result: rpctrace.ResultOK}
	spans[1] = rpctrace.HistoricalSpan{Stage: rpctrace.StageSlotRawQuorum, Start: at, End: at, Result: rpctrace.ResultCanceled, Incomplete: true}
	tr.RetainLongestBarrier(at, at, spans, 2, 3)
	s := tr.Finish(rpctrace.ResultCanceled)
	if len(s.Spans) != rpctrace.MaxSpans-1 || s.DroppedSpans != 5 || s.BarrierGroups != 1 {
		t.Fatalf("partial critical group escaped cap: %+v", s)
	}
}

func TestBarrierTraceHistoricalIncompleteStopsAtCallerReturn(t *testing.T) {
	r, _ := rpctrace.NewRecorder(1)
	tr := r.Start(context.Background(), rpctrace.SideServer, 13)
	at := time.Now()
	var spans [rpctrace.MaxBarrierSpans]rpctrace.HistoricalSpan
	spans[0] = rpctrace.HistoricalSpan{Stage: rpctrace.StageSlotControlWait, Start: at, End: at, Result: rpctrace.ResultCanceled, Incomplete: true}
	tr.RetainLongestBarrier(at, at, spans, 1, 0)
	s := tr.Finish(rpctrace.ResultCanceled)
	if len(s.Spans) != 1 || !s.Spans[0].Incomplete || s.Spans[0].Duration != 0 {
		t.Fatalf("historical unfinished phase was extended past return: %+v", s)
	}
}

func TestBarrierTraceRejectsInvalidGroup(t *testing.T) {
	r, _ := rpctrace.NewRecorder(1)
	tr := r.Start(context.Background(), rpctrace.SideServer, 13)
	at := time.Now()
	var spans [rpctrace.MaxBarrierSpans]rpctrace.HistoricalSpan
	spans[0] = rpctrace.HistoricalSpan{Stage: rpctrace.Stage("unbounded-user-value"), Start: at, End: at}
	tr.RetainLongestBarrier(at, at, spans, 1, 0)
	spans[0] = rpctrace.HistoricalSpan{Stage: rpctrace.StageSlotRawQuorum, Start: at, End: at.Add(-time.Nanosecond)}
	tr.RetainLongestBarrier(at, at, spans, 1, 0)
	s := tr.Finish(rpctrace.ResultOK)
	if len(s.Spans) != 0 || s.DroppedSpans != 2 {
		t.Fatalf("invalid group retained: %+v", s)
	}
}

func TestCommitTraceClosedStageVocabulary(t *testing.T) {
	r, _ := rpctrace.NewRecorder(1)
	tr := r.Start(context.Background(), rpctrace.SideServer, 13)
	for _, stage := range []rpctrace.Stage{"commit_admission", "commit_queue", "commit_collect", "commit_build", "commit_durable", "commit_publish"} {
		tr.Begin(stage).End(rpctrace.ResultOK)
	}
	s := tr.Finish(rpctrace.ResultOK)
	if len(s.Spans) != 6 || s.DroppedSpans != 0 {
		t.Fatalf("commit phases missing: %+v", s)
	}
}
