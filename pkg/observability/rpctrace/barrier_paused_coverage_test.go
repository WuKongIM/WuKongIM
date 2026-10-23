package rpctrace_test

import (
	"context"
	"encoding/json"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/observability/diagnostics"
	"github.com/WuKongIM/WuKongIM/pkg/observability/rpctrace"
)

// Failure matrix: pause is optional independent coverage, never an owner
// subset. Fixed phase provenance, cancellation, clone, longest replacement and
// atomic capacity drops apply equally; malformed totals cannot imply zero work.
func pausedDocument() map[string]any {
	p := make([]any, 5)
	for i := range p {
		p[i] = map[string]any{"duration": int64(0), "count": uint64(0)}
	}
	p[2] = map[string]any{"duration": int64(10), "count": uint64(1)}
	return map[string]any{"phases": p}
}
func pausedPart(p map[string]any, i int) map[string]any {
	return p["phases"].([]any)[i].(map[string]any)
}
func installPaused(t *testing.T, c *rpctrace.BarrierCoverage, p map[string]any) {
	t.Helper()
	raw, _ := json.Marshal(c)
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	fields["storage_count_paused"], _ = json.Marshal(p)
	raw, _ = json.Marshal(fields)
	if err := json.Unmarshal(raw, c); err != nil {
		t.Fatal(err)
	}
}
func readPaused(t *testing.T, c *rpctrace.BarrierCoverage) map[string]any {
	t.Helper()
	raw, _ := json.Marshal(c)
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	if len(fields["storage_count_paused"]) == 0 {
		return nil
	}
	var p map[string]any
	if err := json.Unmarshal(fields["storage_count_paused"], &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestBarrierPausedCoverageIndependentLongestClone(t *testing.T) {
	r, _ := rpctrace.NewRecorder(1)
	tr := r.Start(context.Background(), rpctrace.SideServer, 91)
	base := time.Now()
	spans, c := coverageCandidate(base, 10)
	// All three independent tracks overlap the same whole phase legally.
	c.Phases[2].Send = rpctrace.BarrierOverlayCoverage{Duration: 10, Count: 1}
	p := pausedDocument()
	installPaused(t, &c, p)
	tr.RetainLongestBarrierWithCoverage(base, base.Add(50), spans, 5, 0, c)
	short, shortC := coverageCandidate(base, 2)
	tr.RetainLongestBarrierWithCoverage(base, base.Add(10), short, 5, 0, shortC)
	_, tieC := coverageCandidate(base, 10)
	tr.RetainLongestBarrierWithCoverage(base, base.Add(50), spans, 5, 0, tieC)
	pausedPart(p, 2)["duration"] = int64(1)
	installPaused(t, &c, p)
	s := tr.Finish(rpctrace.ResultOK)
	got := readPaused(t, s.BarrierCoverage)
	if got == nil || pausedPart(got, 2)["duration"] != float64(10) {
		t.Fatal("selected pause omitted, replaced or aliases candidate")
	}
	clone := s.Clone()
	installPaused(t, clone.BarrierCoverage, p)
	if pausedPart(readPaused(t, s.BarrierCoverage), 2)["duration"] != float64(10) {
		t.Fatal("Clone aliases pause coverage")
	}
	before := s.Clone()
	tr.RetainLongestBarrierWithCoverage(base, base.Add(50), spans, 5, 0, c)
	if !reflect.DeepEqual(s, &before) {
		t.Fatal("late group mutated finished sample")
	}
}

func TestBarrierPausedCoverageInvalidAndUnknown(t *testing.T) {
	cases := map[string]func(map[string]any){
		"negative":        func(p map[string]any) { pausedPart(p, 2)["duration"] = int64(-1) },
		"overphase":       func(p map[string]any) { pausedPart(p, 2)["duration"] = int64(11) },
		"missing_count":   func(p map[string]any) { pausedPart(p, 2)["count"] = uint64(0) },
		"count_over_ns":   func(p map[string]any) { pausedPart(p, 2)["count"] = uint64(11) },
		"zero_count":      func(p map[string]any) { pausedPart(p, 1)["count"] = uint64(1) },
		"zero_unfinished": func(p map[string]any) { pausedPart(p, 1)["unfinished"] = true },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			r, _ := rpctrace.NewRecorder(1)
			tr := r.Start(context.Background(), rpctrace.SideServer, 91)
			base := time.Now()
			prior, pc := coverageCandidate(base, 1)
			tr.RetainLongestBarrierWithCoverage(base, base.Add(5), prior, 5, 0, pc)
			spans, c := coverageCandidate(base, 10)
			p := pausedDocument()
			mutate(p)
			installPaused(t, &c, p)
			tr.RetainLongestBarrierWithCoverage(base, base.Add(50), spans, 5, 0, c)
			s := tr.Finish(rpctrace.ResultOK)
			if s.BarrierGroups != 1 || s.DroppedSpans != 5 || readPaused(t, s.BarrierCoverage) != nil {
				t.Fatal("invalid pause replaced prior valid group")
			}
		})
	}
	for _, invalid := range []bool{false, true} {
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
		p := pausedDocument()
		pausedPart(p, 2)["duration"] = int64(0)
		pausedPart(p, 2)["count"] = uint64(0)
		pausedPart(p, 0)["duration"] = int64(10)
		pausedPart(p, 0)["count"] = uint64(1)
		pausedPart(p, 0)["unfinished"] = true
		if invalid {
			pausedPart(p, 4)["duration"] = int64(1)
			pausedPart(p, 4)["count"] = uint64(1)
		}
		installPaused(t, &c, p)
		tr.RetainLongestBarrierWithCoverage(base, base.Add(10), spans, 1, 0, c)
		s := tr.Finish(rpctrace.ResultCanceled)
		if invalid {
			if s.BarrierCoverage != nil || s.BarrierGroups != 0 {
				t.Fatal("unknown phase received pause")
			}
			continue
		}
		if got := readPaused(t, s.BarrierCoverage); got == nil || pausedPart(got, 0)["unfinished"] != true {
			t.Fatal("clipped pause missing")
		}
	}
}

func TestBarrierPausedCoverageLegacyAndWholeDrop(t *testing.T) {
	for _, mode := range []string{"absent", "legacy", "capacity"} {
		t.Run(mode, func(t *testing.T) {
			r, _ := rpctrace.NewRecorder(1)
			tr := r.Start(context.Background(), rpctrace.SideServer, 91)
			base := time.Now()
			spans, c := coverageCandidate(base, 10)
			installPaused(t, &c, pausedDocument())
			tr.RetainLongestBarrierWithCoverage(base, base.Add(50), spans, 5, 0, c)
			long, lc := coverageCandidate(base, 11)
			switch mode {
			case "absent":
				tr.RetainLongestBarrierWithCoverage(base, base.Add(55), long, 5, 0, lc)
			case "legacy":
				tr.RetainLongestBarrier(base, base.Add(55), long, 5, 0)
			case "capacity":
				for i := 0; i < rpctrace.MaxSpans-4; i++ {
					tr.Begin(rpctrace.StageHandler).End(rpctrace.ResultOK)
				}
			}
			s := tr.Finish(rpctrace.ResultOK)
			if mode == "absent" {
				if s.BarrierCoverage == nil || readPaused(t, s.BarrierCoverage) != nil {
					t.Fatal("absent pause did not stay unknown")
				}
			} else if s.BarrierCoverage != nil {
				t.Fatal("pause escaped whole group drop")
			}
		})
	}
}

func TestBarrierPausedCoverageJSONBodyBound(t *testing.T) {
	base := time.Now()
	_, c := coverageCandidate(base, 10)
	p := pausedDocument()
	owner := ownerCoverageDocument()
	for i := 0; i < 5; i++ {
		pausedPart(p, i)["duration"] = int64(math.MaxInt64)
		pausedPart(p, i)["count"] = uint64(math.MaxUint64)
		pausedPart(p, i)["unfinished"] = true
		for _, kind := range []string{"budget", "other"} {
			v := ownerPart(owner, i, kind)
			v["duration"] = int64(math.MaxInt64)
			v["count"] = uint64(math.MaxUint64)
			v["unfinished"] = true
		}
		v := ownerPart(owner, i, "budget")
		for _, key := range []string{"drain_wait", "save_permit", "sync_work"} {
			v[key] = int64(math.MaxInt64)
		}
		v["reject_mask"] = uint8(15)
		ownerPart(owner, i, "other")["cause_mask"] = uint8(63)
	}
	installPaused(t, &c, p)
	installOwnerDocument(t, &c, owner)
	if readPaused(t, &c) == nil {
		t.Fatal("fixed pause schema omitted")
	}
	s := rpctrace.Sample{TraceID: "0123456789abcdef0123456789abcdef/0123456789abcdef", ParentTraceID: "0123456789abcdef0123456789abcdef/0123456789abcdef", At: base, Side: rpctrace.SideServer, ServiceID: math.MaxUint16, Duration: math.MaxInt64, Result: rpctrace.ResultUnfinished, BarrierCoverage: &c, DroppedSpans: math.MaxUint64, RecorderDropped: math.MaxUint64, BarrierGroups: math.MaxUint64, DroppedBarrierGroups: math.MaxUint64}
	for i := 0; i < rpctrace.MaxSpans; i++ {
		s.Spans = append(s.Spans, rpctrace.SpanSample{Stage: rpctrace.StageSlotReadEligibility, Offset: math.MaxInt64, Duration: math.MaxInt64, Result: rpctrace.ResultUnfinished, Incomplete: true})
	}
	e := diagnostics.Event{RPC: &s, TraceID: s.TraceID, Stage: diagnostics.StageRPCServer, At: base, Duration: math.MaxInt64, NodeID: math.MaxUint64, PeerNodeID: math.MaxUint64, Service: "channel_append_authority", Result: diagnostics.ResultPartial}
	q := diagnostics.QueryResult{Scope: "local", NodeID: math.MaxUint64, Query: diagnostics.Query{Stage: diagnostics.StageRPCServer, Limit: 500}, Status: diagnostics.StatusPartial, StartedAt: base, DurationMS: math.MaxInt64}
	for i := 0; i < 500; i++ {
		q.Events = append(q.Events, e)
	}
	raw, err := json.Marshal(q)
	if err != nil || len(raw)+4096 > 4<<20 {
		t.Fatalf("500 event response exceeds original4MiB: bytes=%d err=%v", len(raw), err)
	}
	t.Logf("fixed maximum-width JSON 500 events=%d bytes", len(raw))
}
