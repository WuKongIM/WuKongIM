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

// Failure matrix: absent differs from zero; owner scopes share the selected
// phase boundaries; budget children are subsets; masks are closed; canceled
// prefixes, longest replacement, cloning and whole-group drops remain atomic.
// JSON is the public consumer seam, so these tests also fail behaviorally when
// an older producer silently discards the new optional owner_storage field.
func ownerCoverageDocument() map[string]any {
	phases := make([]any, 5)
	for i := range phases {
		phases[i] = map[string]any{
			"budget": map[string]any{"duration": int64(0), "count": uint64(0), "drain_wait": int64(0), "save_permit": int64(0), "sync_work": int64(0), "reject_mask": uint8(0)},
			"other":  map[string]any{"duration": int64(0), "count": uint64(0), "cause_mask": uint8(0)},
		}
	}
	phases[0] = map[string]any{
		"budget": map[string]any{"duration": int64(6), "count": uint64(1), "drain_wait": int64(1), "save_permit": int64(2), "sync_work": int64(3), "reject_mask": uint8(3)},
		"other":  map[string]any{"duration": int64(3), "count": uint64(1), "cause_mask": uint8(8)},
	}
	return map[string]any{"phases": phases}
}

func ownerPart(document map[string]any, phase int, kind string) map[string]any {
	return document["phases"].([]any)[phase].(map[string]any)[kind].(map[string]any)
}

func installOwnerDocument(t *testing.T, coverage *rpctrace.BarrierCoverage, owner map[string]any) {
	t.Helper()
	encoded, err := json.Marshal(coverage)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	fields["owner_storage"], err = json.Marshal(owner)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err = json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	// Decode into the existing value to exercise ownership of a non-nil nested
	// pointer, rather than replacing that pointer with a fresh test allocation.
	if err := json.Unmarshal(encoded, coverage); err != nil {
		t.Fatal(err)
	}
}

func readOwnerDocument(t *testing.T, coverage *rpctrace.BarrierCoverage) map[string]any {
	t.Helper()
	encoded, err := json.Marshal(coverage)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	raw, ok := fields["owner_storage"]
	if !ok {
		return nil
	}
	var owner map[string]any
	if err := json.Unmarshal(raw, &owner); err != nil {
		t.Fatal(err)
	}
	return owner
}

func TestBarrierOwnerCoverageRetainsSelectedGroupAndDetachesCopies(t *testing.T) {
	r, _ := rpctrace.NewRecorder(1)
	tr := r.Start(context.Background(), rpctrace.SideServer, 91)
	base := time.Now()
	spans, c := coverageCandidate(base, 10)
	owner := ownerCoverageDocument()
	installOwnerDocument(t, &c, owner)
	tr.RetainLongestBarrierWithCoverage(base, base.Add(50), spans, 5, 0, c)
	// A shorter candidate and a tie cannot change the selected owner data.
	shorter, shorterCoverage := coverageCandidate(base, 2)
	tr.RetainLongestBarrierWithCoverage(base, base.Add(10), shorter, 5, 0, shorterCoverage)
	tie, tieCoverage := coverageCandidate(base, 10)
	tr.RetainLongestBarrierWithCoverage(base, base.Add(50), tie, 5, 0, tieCoverage)
	ownerPart(owner, 0, "budget")["duration"] = int64(5)
	ownerPart(owner, 0, "budget")["sync_work"] = int64(2)
	installOwnerDocument(t, &c, owner)
	sample := tr.Finish(rpctrace.ResultOK)
	got := readOwnerDocument(t, sample.BarrierCoverage)
	if got == nil || ownerPart(got, 0, "budget")["duration"] != float64(6) || ownerPart(got, 0, "other")["duration"] != float64(3) {
		t.Fatal("selected owner coverage was omitted, replaced or aliased with its candidate")
	}
	if sample.BarrierGroups != 3 || len(sample.Spans) != 5 || sample.DroppedSpans != 0 {
		t.Fatal("owner fields changed the existing group/span bounds")
	}
	clone := sample.Clone()
	installOwnerDocument(t, clone.BarrierCoverage, owner)
	if ownerPart(readOwnerDocument(t, sample.BarrierCoverage), 0, "budget")["duration"] != float64(6) {
		t.Fatal("Sample.Clone aliases nested owner coverage")
	}
	before := sample.Clone()
	tr.RetainLongestBarrierWithCoverage(base, base.Add(50), spans, 5, 0, c)
	if !reflect.DeepEqual(sample, &before) {
		t.Fatal("late publication modified the finished owner sample")
	}
}

func TestBarrierOwnerCoverageRejectsInvalidScopesWithoutReplacingPriorGroup(t *testing.T) {
	cases := map[string]func(map[string]any){
		"budget_negative":               func(o map[string]any) { ownerPart(o, 0, "budget")["duration"] = int64(-1) },
		"other_negative":                func(o map[string]any) { ownerPart(o, 0, "other")["duration"] = int64(-1) },
		"budget_exceeds_phase":          func(o map[string]any) { ownerPart(o, 0, "budget")["duration"] = int64(11) },
		"other_exceeds_phase":           func(o map[string]any) { ownerPart(o, 0, "other")["duration"] = int64(11) },
		"scope_sum_exceeds_phase":       func(o map[string]any) { ownerPart(o, 0, "other")["duration"] = int64(5) },
		"budget_duration_without_count": func(o map[string]any) { ownerPart(o, 0, "budget")["count"] = uint64(0) },
		"other_duration_without_count":  func(o map[string]any) { ownerPart(o, 0, "other")["count"] = uint64(0) },
		"budget_count_above_ns":         func(o map[string]any) { ownerPart(o, 0, "budget")["count"] = uint64(7) },
		"other_count_above_ns":          func(o map[string]any) { ownerPart(o, 0, "other")["count"] = uint64(4) },
		"budget_mask_missing":           func(o map[string]any) { ownerPart(o, 0, "budget")["reject_mask"] = uint8(0) },
		"budget_mask_unknown":           func(o map[string]any) { ownerPart(o, 0, "budget")["reject_mask"] = uint8(16) },
		"other_mask_missing":            func(o map[string]any) { ownerPart(o, 0, "other")["cause_mask"] = uint8(0) },
		"other_mask_unknown":            func(o map[string]any) { ownerPart(o, 0, "other")["cause_mask"] = uint8(64) },
		"drain_negative":                func(o map[string]any) { ownerPart(o, 0, "budget")["drain_wait"] = int64(-1) },
		"permit_negative":               func(o map[string]any) { ownerPart(o, 0, "budget")["save_permit"] = int64(-1) },
		"work_negative":                 func(o map[string]any) { ownerPart(o, 0, "budget")["sync_work"] = int64(-1) },
		"drain_exceeds_parent":          func(o map[string]any) { ownerPart(o, 0, "budget")["drain_wait"] = int64(7) },
		"permit_exceeds_parent":         func(o map[string]any) { ownerPart(o, 0, "budget")["save_permit"] = int64(7) },
		"work_exceeds_parent":           func(o map[string]any) { ownerPart(o, 0, "budget")["sync_work"] = int64(7) },
		"children_sum_exceeds_parent":   func(o map[string]any) { ownerPart(o, 0, "budget")["sync_work"] = int64(4) },
		"zero_budget_count":             func(o map[string]any) { ownerPart(o, 1, "budget")["count"] = uint64(1) },
		"zero_other_count":              func(o map[string]any) { ownerPart(o, 1, "other")["count"] = uint64(1) },
		"zero_budget_unfinished":        func(o map[string]any) { ownerPart(o, 1, "budget")["unfinished"] = true },
		"zero_other_unfinished":         func(o map[string]any) { ownerPart(o, 1, "other")["unfinished"] = true },
		"zero_budget_mask":              func(o map[string]any) { ownerPart(o, 1, "budget")["reject_mask"] = uint8(1) },
		"zero_other_mask":               func(o map[string]any) { ownerPart(o, 1, "other")["cause_mask"] = uint8(1) },
		"zero_budget_child":             func(o map[string]any) { ownerPart(o, 1, "budget")["save_permit"] = int64(1) },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			r, _ := rpctrace.NewRecorder(1)
			tr := r.Start(context.Background(), rpctrace.SideServer, 91)
			base := time.Now()
			prior, priorCoverage := coverageCandidate(base, 1)
			tr.RetainLongestBarrierWithCoverage(base, base.Add(5), prior, 5, 0, priorCoverage)
			spans, invalid := coverageCandidate(base, 10)
			owner := ownerCoverageDocument()
			mutate(owner)
			installOwnerDocument(t, &invalid, owner)
			tr.RetainLongestBarrierWithCoverage(base, base.Add(50), spans, 5, 0, invalid)
			sample := tr.Finish(rpctrace.ResultOK)
			if sample.BarrierGroups != 1 || sample.DroppedSpans != 5 || sample.BarrierCoverage == nil || sample.BarrierCoverage.Phases[2].Save.Duration != 1 || readOwnerDocument(t, sample.BarrierCoverage) != nil {
				t.Fatal("invalid owner scope replaced or contaminated the prior group")
			}
		})
	}
}

func TestBarrierOwnerCoverageCallerClippingAndUnknownPhases(t *testing.T) {
	for _, unknownData := range []bool{false, true} {
		r, _ := rpctrace.NewRecorder(1)
		tr := r.Start(context.Background(), rpctrace.SideServer, 91)
		base := time.Now()
		spans, coverage := coverageCandidate(base, 10)
		spans[1].Incomplete, spans[1].Result = true, rpctrace.ResultCanceled
		for i := 2; i < 5; i++ {
			coverage.Phases[i].Observed = false
			coverage.Phases[i].Save = rpctrace.BarrierOverlayCoverage{}
		}
		owner := ownerCoverageDocument()
		// The same caller-clipped scope can mark an earlier complete phase;
		// unfinished describes the operation, not that earlier phase's status.
		ownerPart(owner, 0, "budget")["unfinished"] = true
		ownerPart(owner, 1, "budget")["duration"] = int64(10)
		ownerPart(owner, 1, "budget")["count"] = uint64(1)
		ownerPart(owner, 1, "budget")["reject_mask"] = uint8(1)
		ownerPart(owner, 1, "budget")["unfinished"] = true
		if unknownData {
			ownerPart(owner, 3, "other")["duration"] = int64(1)
			ownerPart(owner, 3, "other")["count"] = uint64(1)
			ownerPart(owner, 3, "other")["cause_mask"] = uint8(1)
		}
		installOwnerDocument(t, &coverage, owner)
		tr.RetainLongestBarrierWithCoverage(base, base.Add(20), spans, 2, 0, coverage)
		sample := tr.Finish(rpctrace.ResultCanceled)
		if unknownData {
			if sample.BarrierCoverage != nil || sample.BarrierGroups != 0 || sample.DroppedSpans != 2 {
				t.Fatal("an unobserved phase acquired owner evidence")
			}
			continue
		}
		got := readOwnerDocument(t, sample.BarrierCoverage)
		if got == nil || ownerPart(got, 0, "budget")["unfinished"] != true || ownerPart(got, 1, "budget")["duration"] != float64(10) || sample.Spans[0].Incomplete || !sample.Spans[1].Incomplete {
			t.Fatal("caller-clipped owner scope lost its original phase boundaries")
		}
	}
}

func TestBarrierOwnerCoverageClearsOnReplacementAndWholeDrop(t *testing.T) {
	for _, mode := range []string{"no_owner", "legacy", "capacity"} {
		t.Run(mode, func(t *testing.T) {
			r, _ := rpctrace.NewRecorder(1)
			tr := r.Start(context.Background(), rpctrace.SideServer, 91)
			base := time.Now()
			spans, coverage := coverageCandidate(base, 10)
			installOwnerDocument(t, &coverage, ownerCoverageDocument())
			tr.RetainLongestBarrierWithCoverage(base, base.Add(50), spans, 5, 0, coverage)
			longer, noOwner := coverageCandidate(base, 11)
			switch mode {
			case "no_owner":
				tr.RetainLongestBarrierWithCoverage(base, base.Add(55), longer, 5, 0, noOwner)
			case "legacy":
				tr.RetainLongestBarrier(base, base.Add(55), longer, 5, 0)
			case "capacity":
				for i := 0; i < rpctrace.MaxSpans-4; i++ {
					tr.Begin(rpctrace.StageHandler).End(rpctrace.ResultOK)
				}
			}
			sample := tr.Finish(rpctrace.ResultOK)
			if mode == "no_owner" {
				if sample.BarrierCoverage == nil || readOwnerDocument(t, sample.BarrierCoverage) != nil {
					t.Fatal("missing owner data was not kept distinct from measured zero")
				}
			} else if sample.BarrierCoverage != nil {
				t.Fatal("owner evidence escaped an atomic group drop")
			}
		})
	}
}

func TestBarrierOwnerCoverageFixedJSONBodyBound(t *testing.T) {
	base := time.Now()
	_, coverage := coverageCandidate(base, 10)
	owner := ownerCoverageDocument()
	for i := 0; i < 5; i++ {
		budget, other := ownerPart(owner, i, "budget"), ownerPart(owner, i, "other")
		for _, part := range []map[string]any{budget, other} {
			part["duration"], part["count"], part["unfinished"] = int64(math.MaxInt64), uint64(math.MaxUint64), true
		}
		budget["drain_wait"], budget["save_permit"], budget["sync_work"], budget["reject_mask"] = int64(math.MaxInt64), int64(math.MaxInt64), int64(math.MaxInt64), uint8(15)
		other["cause_mask"] = uint8(63)
	}
	// Numeric maxima deliberately overestimate valid producer values. This
	// checks serialization size, not admission of these invalid durations.
	installOwnerDocument(t, &coverage, owner)
	got := readOwnerDocument(t, &coverage)
	if got == nil || len(got["phases"].([]any)) != 5 {
		t.Fatal("owner JSON must retain exactly five fixed entries")
	}
	sample := rpctrace.Sample{TraceID: "0123456789abcdef0123456789abcdef/0123456789abcdef", ParentTraceID: "0123456789abcdef0123456789abcdef/0123456789abcdef", At: base, Side: rpctrace.SideServer, ServiceID: math.MaxUint16, Duration: math.MaxInt64, Result: rpctrace.ResultUnfinished, BarrierCoverage: &coverage, DroppedSpans: math.MaxUint64, RecorderDropped: math.MaxUint64, BarrierGroups: math.MaxUint64, DroppedBarrierGroups: math.MaxUint64}
	for i := 0; i < rpctrace.MaxSpans; i++ {
		sample.Spans = append(sample.Spans, rpctrace.SpanSample{Stage: rpctrace.StageSlotReadEligibility, Offset: math.MaxInt64, Duration: math.MaxInt64, Result: rpctrace.ResultUnfinished, Incomplete: true})
	}
	encoded, err := json.Marshal(sample)
	if err != nil || len(encoded) > 12*1024 {
		t.Fatalf("fixed owner schema exceeds the bounded sample budget: bytes=%d err=%v", len(encoded), err)
	}
	// The current app leaves MaxEventsPerKey at its Store default (256), so a
	// single-stage collector query with limit=500 still returns at most 256.
	// Marshal the real envelope with the fields filled by its RPC observer.
	response := diagnostics.QueryResult{Scope: "local", NodeID: math.MaxUint64, Query: diagnostics.Query{Stage: diagnostics.StageRPCServer, Limit: 500}, Status: diagnostics.StatusPartial, StartedAt: base, DurationMS: math.MaxInt64}
	event := diagnostics.Event{RPC: &sample, TraceID: sample.TraceID, Stage: diagnostics.StageRPCServer, At: base, Duration: math.MaxInt64, NodeID: math.MaxUint64, PeerNodeID: math.MaxUint64, Service: "channel_append_authority", Result: diagnostics.ResultPartial}
	for i := 0; i < 256; i++ {
		response.Events = append(response.Events, event)
	}
	body, err := json.Marshal(response)
	if err != nil || len(body)+4096 > 4*1024*1024 {
		t.Fatalf("current bounded RPC event response exceeds 4 MiB: bytes=%d err=%v", len(body), err)
	}
	boundedBytes := len(body)
	for i := 256; i < 500; i++ {
		response.Events = append(response.Events, event)
	}
	upper, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("bounded JSON bytes: sample=%d actual_stage_cap_256=%d hypothetical_500=%d", len(encoded), boundedBytes, len(upper))
}
