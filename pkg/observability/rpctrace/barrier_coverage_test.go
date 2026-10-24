package rpctrace_test

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/observability/rpctrace"
)

// Failure matrix: fixed phase provenance, immutable clone, longest replacement,
// missing coverage distinct from zero, and atomic whole-group capacity drops.
func coverageCandidate(base time.Time, width time.Duration) ([rpctrace.MaxBarrierSpans]rpctrace.HistoricalSpan, rpctrace.BarrierCoverage) {
	var spans [rpctrace.MaxBarrierSpans]rpctrace.HistoricalSpan
	var c rpctrace.BarrierCoverage
	stages := [5]rpctrace.Stage{rpctrace.StageSlotControlWait, rpctrace.StageSlotReadEligibility, rpctrace.StageSlotRawQuorum, rpctrace.StageSlotReadyToAccept, rpctrace.StageSlotApplyWait}
	for i, stage := range stages {
		spans[i] = rpctrace.HistoricalSpan{Stage: stage, Start: base.Add(time.Duration(i) * width), End: base.Add(time.Duration(i+1) * width), Result: rpctrace.ResultOK}
		c.Phases[i] = rpctrace.BarrierPhaseCoverage{Stage: stage, Observed: true}
	}
	c.Phases[2].Save = rpctrace.BarrierOverlayCoverage{Duration: width, Count: 1}
	return spans, c
}

// Async storage allows Save and Send to overlap, while each kind remains
// serialized. Failure matrix: legal overlap drops the whole sampled barrier;
// cancellation loses either clipped kind; a published clone aliases coverage.
// Each kind must still fit its phase independently (invalid cases below).
func TestBarrierCoverageRetainsConcurrentSaveAndSend(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		name := "complete"
		if canceled {
			name = "canceled"
		}
		t.Run(name, func(t *testing.T) {
			r, _ := rpctrace.NewRecorder(1)
			tr := r.Start(context.Background(), rpctrace.SideServer, 91)
			base := time.Now()
			spans, c := coverageCandidate(base, 10)
			index, count, end, result := 2, 5, base.Add(50), rpctrace.ResultOK
			if canceled {
				index, count, end, result = 0, 1, base.Add(10), rpctrace.ResultCanceled
				spans[0].Incomplete = true
				spans[0].Result = result
				for i := 1; i < rpctrace.BarrierPhaseCount; i++ {
					c.Phases[i].Observed = false
					c.Phases[i].Save = rpctrace.BarrierOverlayCoverage{}
				}
			}
			c.Phases[index].Save = rpctrace.BarrierOverlayCoverage{Duration: 10, Count: 1, Unfinished: canceled}
			c.Phases[index].Send = rpctrace.BarrierOverlayCoverage{Duration: 10, Count: 1, Unfinished: canceled}
			tr.RetainLongestBarrierWithCoverage(base, end, spans, count, 0, c)
			s := tr.Finish(result)
			if s.BarrierCoverage == nil || s.BarrierGroups != 1 || s.DroppedSpans != 0 {
				t.Fatal("legal concurrent Save/Send discarded the barrier coverage")
			}
			if got := s.BarrierCoverage.Phases[index]; got.Save != c.Phases[index].Save || got.Send != c.Phases[index].Send {
				t.Fatalf("concurrent kinds changed: %+v", got)
			}
			clone := s.Clone()
			clone.BarrierCoverage.Phases[index].Send.Duration = 1
			if s.BarrierCoverage.Phases[index].Send.Duration != 10 {
				t.Fatal("published concurrent coverage aliases its clone")
			}
		})
	}
}

func TestBarrierCoverageLongestAtomicAndClone(t *testing.T) {
	r, _ := rpctrace.NewRecorder(1)
	tr := r.Start(context.Background(), rpctrace.SideServer, 91)
	base := time.Now()
	spans, c := coverageCandidate(base, 10)
	tr.RetainLongestBarrierWithCoverage(base, base.Add(50), spans, 5, 7, c)
	smaller, smallc := coverageCandidate(base, 5)
	tr.RetainLongestBarrierWithCoverage(base, base.Add(25), smaller, 5, 9, smallc)
	// A same-duration group cannot silently replace the selected coverage.
	tie := c
	tie.Phases[2].Save = rpctrace.BarrierOverlayCoverage{}
	tr.RetainLongestBarrierWithCoverage(base, base.Add(50), spans, 5, 1, tie)
	c.Phases[2].Save.Duration = 1
	s := tr.Finish(rpctrace.ResultOK)
	if s.BarrierCoverage == nil || s.BarrierCoverage.Phases[2].Save.Duration != 10 || s.BarrierGroups != 3 || s.DroppedSpans != 17 {
		t.Fatalf("longest coverage changed: %+v", s)
	}
	clone := s.Clone()
	clone.BarrierCoverage.Phases[2].Save.Duration = 2
	if s.BarrierCoverage.Phases[2].Save.Duration != 10 {
		t.Fatal("Clone aliases coverage")
	}
	before := s.Clone()
	tr.RetainLongestBarrierWithCoverage(base, base.Add(50), spans, 5, 0, tie)
	if !reflect.DeepEqual(s, &before) {
		t.Fatal("late publisher changed coverage")
	}
}

func TestBarrierCoverageLegacyReplacementAndWholeCapacity(t *testing.T) {
	for _, mode := range []string{"legacy", "capacity", "default"} {
		t.Run(mode, func(t *testing.T) {
			r, _ := rpctrace.NewRecorder(1)
			tr := r.Start(context.Background(), rpctrace.SideServer, 91)
			base := time.Now()
			spans, c := coverageCandidate(base, 10)
			if mode == "capacity" {
				for i := 0; i < rpctrace.MaxSpans-4; i++ {
					tr.Begin(rpctrace.StageHandler).End(rpctrace.ResultOK)
				}
			}
			if mode != "default" {
				tr.RetainLongestBarrierWithCoverage(base, base.Add(50), spans, 5, 0, c)
			}
			if mode == "legacy" {
				old, _ := coverageCandidate(base, 11)
				tr.RetainLongestBarrier(base, base.Add(55), old, 5, 0)
			}
			s := tr.Finish(rpctrace.ResultOK)
			if s.BarrierCoverage != nil {
				t.Fatal("coverage escaped legacy/default/whole-group drop")
			}
			if mode == "capacity" && (len(s.Spans) != rpctrace.MaxSpans-4 || s.DroppedSpans != 5) {
				t.Fatal("partial group published")
			}
			data, _ := json.Marshal(s)
			var wire map[string]json.RawMessage
			_ = json.Unmarshal(data, &wire)
			if _, ok := wire["barrier_coverage"]; ok {
				t.Fatal("absent coverage not omitted")
			}
		})
	}
}

func TestBarrierCoverageRejectsInvalidPhaseProvenance(t *testing.T) {
	cases := map[string]func(*[rpctrace.MaxBarrierSpans]rpctrace.HistoricalSpan, *rpctrace.BarrierCoverage){
		"wrong_stage": func(_ *[rpctrace.MaxBarrierSpans]rpctrace.HistoricalSpan, c *rpctrace.BarrierCoverage) {
			c.Phases[1].Stage = rpctrace.StageSlotRawQuorum
		},
		"false_observed": func(_ *[rpctrace.MaxBarrierSpans]rpctrace.HistoricalSpan, c *rpctrace.BarrierCoverage) {
			c.Phases[1].Observed = false
		},
		"negative": func(_ *[rpctrace.MaxBarrierSpans]rpctrace.HistoricalSpan, c *rpctrace.BarrierCoverage) {
			c.Phases[2].Save.Duration = -1
		},
		"send_exceeds_phase": func(_ *[rpctrace.MaxBarrierSpans]rpctrace.HistoricalSpan, c *rpctrace.BarrierCoverage) {
			c.Phases[2].Send = rpctrace.BarrierOverlayCoverage{Duration: 11, Count: 1}
		},
		"save_exceeds_phase": func(_ *[rpctrace.MaxBarrierSpans]rpctrace.HistoricalSpan, c *rpctrace.BarrierCoverage) {
			c.Phases[2].Save.Duration = 11
		},
		"duration_without_count": func(_ *[rpctrace.MaxBarrierSpans]rpctrace.HistoricalSpan, c *rpctrace.BarrierCoverage) {
			c.Phases[2].Save.Count = 0
		},
		"count_without_duration": func(_ *[rpctrace.MaxBarrierSpans]rpctrace.HistoricalSpan, c *rpctrace.BarrierCoverage) {
			c.Phases[0].Save.Count = 1
		},
		"unfinished_without_count": func(_ *[rpctrace.MaxBarrierSpans]rpctrace.HistoricalSpan, c *rpctrace.BarrierCoverage) {
			c.Phases[0].Save.Unfinished = true
		},
		"phase_gap": func(s *[rpctrace.MaxBarrierSpans]rpctrace.HistoricalSpan, _ *rpctrace.BarrierCoverage) {
			s[2].Start = s[2].Start.Add(1)
		},
		"phase_reorder": func(s *[rpctrace.MaxBarrierSpans]rpctrace.HistoricalSpan, _ *rpctrace.BarrierCoverage) {
			s[1], s[2] = s[2], s[1]
		},
		"wrong_start": func(s *[rpctrace.MaxBarrierSpans]rpctrace.HistoricalSpan, _ *rpctrace.BarrierCoverage) {
			s[0].Start = s[0].Start.Add(1)
		},
		"incomplete_before_next": func(s *[rpctrace.MaxBarrierSpans]rpctrace.HistoricalSpan, _ *rpctrace.BarrierCoverage) {
			s[1].Incomplete = true
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			r, _ := rpctrace.NewRecorder(1)
			tr := r.Start(context.Background(), rpctrace.SideServer, 91)
			base := time.Now()
			valid, c := coverageCandidate(base, 1)
			tr.RetainLongestBarrierWithCoverage(base, base.Add(5), valid, 5, 0, c)
			invalid, bad := coverageCandidate(base, 10)
			mutate(&invalid, &bad)
			tr.RetainLongestBarrierWithCoverage(base, base.Add(50), invalid, 5, 0, bad)
			s := tr.Finish(rpctrace.ResultOK)
			if s.BarrierGroups != 1 || s.DroppedSpans != 5 || s.BarrierCoverage == nil || s.BarrierCoverage.Phases[2].Save.Duration != 1 {
				t.Fatalf("invalid coverage escaped or corrupted selected group: %+v", s)
			}
		})
	}
}

func TestBarrierCoveragePartialObservedPrefix(t *testing.T) {
	r, _ := rpctrace.NewRecorder(1)
	tr := r.Start(context.Background(), rpctrace.SideServer, 91)
	base := time.Now()
	spans, c := coverageCandidate(base, 10)
	spans[0].Incomplete = true
	spans[0].Result = rpctrace.ResultCanceled
	for i := 1; i < 5; i++ {
		c.Phases[i].Observed = false
		c.Phases[i].Save = rpctrace.BarrierOverlayCoverage{}
	}
	c.Phases[0].Save = rpctrace.BarrierOverlayCoverage{Duration: 10, Count: 1, Unfinished: true}
	tr.RetainLongestBarrierWithCoverage(base, base.Add(10), spans, 1, 0, c)
	s := tr.Finish(rpctrace.ResultCanceled)
	if s.BarrierCoverage == nil || !s.BarrierCoverage.Phases[0].Observed || s.BarrierCoverage.Phases[1].Observed {
		t.Fatal("partial observed prefix lost")
	}
	// Nonzero values for an unreached phase cannot masquerade as measurements.
	r, _ = rpctrace.NewRecorder(1)
	tr = r.Start(context.Background(), rpctrace.SideServer, 91)
	base = time.Now()
	spans[0].Start = base
	spans[0].End = base.Add(10)
	c.Phases[4].Send = rpctrace.BarrierOverlayCoverage{Duration: 1, Count: 1}
	tr.RetainLongestBarrierWithCoverage(base, base.Add(10), spans, 1, 0, c)
	if got := tr.Finish(rpctrace.ResultCanceled); got.BarrierCoverage != nil || got.BarrierGroups != 0 {
		t.Fatal("unknown phase contained measurement")
	}
}
