package rpctrace

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestStorePrepareDetailClockKnownAndBackwards(t *testing.T) {
	for _, backwards := range []bool{false, true} {
		recorder, err := NewRecorder(1)
		if err != nil {
			t.Fatal(err)
		}
		owner := recorder.Start(context.Background(), SideServer, 13)
		parent := NewCommitObservation(owner).BeginStage(StageStoreAppendPrepare, nil)
		values := []time.Time{time.Unix(0, 10), time.Unix(0, 20), time.Unix(0, 30), time.Unix(0, 40)}
		if backwards {
			values[1] = time.Unix(0, 5)
		}
		reads := 0
		now := func() time.Time {
			if reads >= len(values) {
				t.Fatal("unexpected extra preparation clock read")
			}
			value := values[reads]
			reads++
			return value
		}
		detail := parent.storePrepareDetailWithClock(now)
		if reads != 0 {
			t.Fatal("constructing the opaque child read the clock")
		}
		frontier := detail.BeginFrontier()
		frontier.End()
		item := detail.BeginItemPrepare()
		item.End()
		if reads != 4 {
			t.Errorf("reads=%d, want exactly4 for two complete intervals", reads)
		}
		parent.End(ResultOK)
		sample := owner.Finish(ResultOK)
		valuesWire := storePrepareClockDetail(t, sample)
		wantMask, wantFrontier := int64(3), int64(10)
		if backwards {
			wantMask, wantFrontier = 2, 0
		}
		if valuesWire["known_mask"] != wantMask || valuesWire["frontier_wall"] != wantFrontier || valuesWire["frontier_calls"] != 1 || valuesWire["item_prepare_wall"] != 10 || valuesWire["item_prepare_calls"] != 1 {
			t.Errorf("numeric detail=%v, want known%d frontier%d and item10", valuesWire, wantMask, wantFrontier)
		}
		if len(sample.Spans) != 1 || sample.DroppedSpans != 0 {
			t.Error("numeric detail consumed another span or changed drops")
		}
	}
}

func TestStorePrepareDetailClockZeroAndUnfinishedStayUnknown(t *testing.T) {
	for _, unfinished := range []bool{false, true} {
		recorder, err := NewRecorder(1)
		if err != nil {
			t.Fatal(err)
		}
		owner := recorder.Start(context.Background(), SideServer, 13)
		parent := NewCommitObservation(owner).BeginStage(StageStoreAppendPrepare, nil)
		reads := 0
		now := func() time.Time {
			reads++
			if unfinished {
				return time.Unix(0, 10)
			}
			return time.Time{}
		}
		detail := parent.storePrepareDetailWithClock(now)
		interval := detail.BeginFrontier()
		if !unfinished {
			interval.End()
		}
		// Parent closure does not manufacture a finish for an incomplete interval.
		parent.End(ResultOK)
		sample := owner.Finish(ResultOK)
		beforeLate := reads
		interval.End()
		detail.BeginItemPrepare().End()
		if reads != beforeLate {
			t.Error("late completion or new interval read the clock after immutable Finish")
		}
		fields := storePrepareClockDetail(t, sample)
		if fields["known_mask"] != 0 || fields["frontier_wall"] != 0 || fields["frontier_calls"] != 1 || fields["item_prepare_calls"] != 0 {
			t.Errorf("unknown phase became measured zero: %v", fields)
		}
	}
}

func TestStorePrepareDetailClockNilFullFinishedAndEndedAreInert(t *testing.T) {
	for _, mode := range []string{"nil", "full", "finished", "ended"} {
		recorder, err := NewRecorder(1)
		if err != nil {
			t.Fatal(err)
		}
		owner := recorder.Start(context.Background(), SideServer, 13)
		if mode == "full" {
			for range MaxSpans {
				owner.Begin(StageHandler).End(ResultOK)
			}
		}
		if mode == "finished" {
			owner.Finish(ResultOK)
		}
		var parent CommitSpan
		if mode != "nil" {
			parent = NewCommitObservation(owner).BeginStage(StageStoreAppendPrepare, nil)
		}
		if mode == "ended" {
			parent.End(ResultOK)
		}
		reads := 0
		detail := parent.storePrepareDetailWithClock(func() time.Time { reads++; return time.Unix(0, int64(reads)) })
		detail.BeginFrontier().End()
		detail.BeginItemPrepare().End()
		if reads != 0 {
			t.Errorf("%s parent read %d additional clocks", mode, reads)
		}
		owner.Finish(ResultOK)
	}
}

func TestStorePrepareDetailClockInvalidCategoryStaysUnknown(t *testing.T) {
	for _, invalid := range []string{"zero", "backwards", "overflow"} {
		recorder, err := NewRecorder(1)
		if err != nil {
			t.Fatal(err)
		}
		owner := recorder.Start(context.Background(), SideServer, 13)
		parent := NewCommitObservation(owner).BeginStage(StageStoreAppendPrepare, nil)
		phase, reads := 0, 0
		base := time.Unix(0, 1)
		now := func() time.Time {
			reads++
			if phase == 0 {
				switch invalid {
				case "zero":
					return time.Time{}
				case "backwards":
					if reads == 1 {
						return base.Add(20)
					}
					return base.Add(10)
				case "overflow":
					if reads == 1 {
						return base
					}
					return base.Add(time.Duration(1<<63 - 5))
				}
			}
			if reads == 1 {
				return base.Add(30)
			}
			return base.Add(40)
		}
		detail := parent.storePrepareDetailWithClock(now)
		first := detail.BeginFrontier()
		first.End()
		phase, reads = 1, 0
		second := detail.BeginFrontier()
		second.End()
		parent.End(ResultOK)
		fields := storePrepareClockDetail(t, owner.Finish(ResultOK))
		if fields["known_mask"] != 0 || fields["frontier_wall"] != 0 || fields["frontier_calls"] != 2 || fields["item_prepare_calls"] != 0 {
			t.Errorf("%s phase regained known status or lost attempted calls: %v", invalid, fields)
		}
	}
}

func TestStorePrepareDetailClockClosedOwnersDoNotAliasOpenSibling(t *testing.T) {
	recorder, err := NewRecorder(2)
	if err != nil {
		t.Fatal(err)
	}
	left := recorder.Start(context.Background(), SideServer, 13)
	right := recorder.Start(context.Background(), SideServer, 13)
	parent := NewCommitObservation(left, right).BeginStage(StageStoreAppendPrepare, nil)
	reads := 0
	detail := parent.storePrepareDetailWithClock(func() time.Time { reads++; return time.Unix(0, int64(reads*10)) })
	detail.BeginFrontier().End()
	frozen := left.Finish(ResultOK)
	clone := frozen.Clone()
	before, err := json.Marshal(frozen)
	if err != nil {
		t.Fatal(err)
	}
	cloneBefore, err := json.Marshal(clone)
	if err != nil {
		t.Fatal(err)
	}
	detail.BeginItemPrepare().End()
	parent.End(ResultOK)
	openSibling := right.Finish(ResultOK)
	if reads != 4 {
		t.Errorf("shared interval read clock%d times, want4 regardless of2owners", reads)
	}
	after, err := json.Marshal(frozen)
	if err != nil {
		t.Fatal(err)
	}
	cloneAfter, err := json.Marshal(clone)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) || string(cloneBefore) != string(cloneAfter) {
		t.Error("later sibling work mutated a published or cloned numeric child")
	}
	rightFields := storePrepareClockDetail(t, openSibling)
	// A minimal producer may publish only at parent.End. Early Finish may
	// therefore omit the child; if partial detail exists, it cannot borrow work.
	for _, span := range frozen.Spans {
		body, marshalErr := json.Marshal(span)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(body, &fields); err != nil {
			t.Fatal(err)
		}
		if raw, present := fields["store_prepare_detail"]; present && string(raw) != "null" {
			var leftFields map[string]int64
			if err := json.Unmarshal(raw, &leftFields); err != nil {
				t.Fatal(err)
			}
			if leftFields["known_mask"] != 1 || leftFields["frontier_calls"] != 1 || leftFields["item_prepare_calls"] != 0 {
				t.Errorf("finished owner borrowed later work: %v", leftFields)
			}
		}
	}
	if rightFields["known_mask"] != 3 || rightFields["frontier_calls"] != 1 || rightFields["item_prepare_calls"] != 1 {
		t.Errorf("open sibling lost group detail: %v", rightFields)
	}
	// Mutate the cloned public JSON representation through its existing API;
	// a pointer-backed child must not alias the original Sample after Clone.
	completeClone := openSibling.Clone()
	completeBefore, err := json.Marshal(openSibling)
	if err != nil {
		t.Fatal(err)
	}
	var spanWire map[string]json.RawMessage
	body, err := json.Marshal(completeClone.Spans[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(body, &spanWire); err != nil {
		t.Fatal(err)
	}
	var child map[string]int64
	if err := json.Unmarshal(spanWire["store_prepare_detail"], &child); err != nil {
		t.Fatal(err)
	}
	child["frontier_wall"] = 0
	spanWire["store_prepare_detail"], err = json.Marshal(child)
	if err != nil {
		t.Fatal(err)
	}
	body, err = json.Marshal(spanWire)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(body, &completeClone.Spans[0]); err != nil {
		t.Fatal(err)
	}
	unchanged, err := json.Marshal(openSibling)
	if err != nil {
		t.Fatal(err)
	}
	if string(completeBefore) != string(unchanged) {
		t.Error("mutating cloned preparation child modified source Sample")
	}
}

func TestStorePrepareDetailClockInertParentsAllocateNothing(t *testing.T) {
	for _, mode := range []string{"nil", "full", "finished", "ended"} {
		recorder, err := NewRecorder(1)
		if err != nil {
			t.Fatal(err)
		}
		owner := recorder.Start(context.Background(), SideServer, 13)
		if mode == "full" {
			for range MaxSpans {
				owner.Begin(StageHandler).End(ResultOK)
			}
		}
		var parent CommitSpan
		if mode != "nil" {
			parent = NewCommitObservation(owner).BeginStage(StageStoreAppendPrepare, nil)
		}
		var published *Sample
		var before []byte
		if mode == "finished" {
			published = owner.Finish(ResultOK)
			before, err = json.Marshal(published)
			if err != nil {
				t.Fatal(err)
			}
		}
		if mode == "ended" {
			parent.End(ResultOK)
		}
		reads := 0
		now := func() time.Time { reads++; return time.Unix(0, int64(reads)) }
		allocations := testing.AllocsPerRun(25, func() {
			detail := parent.storePrepareDetailWithClock(now)
			detail.BeginFrontier().End()
			detail.BeginItemPrepare().End()
		})
		if allocations != 0 || reads != 0 {
			t.Errorf("%s parent allocated%v and read%d extra clocks", mode, allocations, reads)
		}
		if mode == "finished" {
			after, err := json.Marshal(published)
			if err != nil || string(before) != string(after) {
				t.Error("finished child mutated during inert measurement")
			}
			continue
		}
		sample := owner.Finish(ResultOK)
		wantDropped := uint64(0)
		if mode == "full" {
			wantDropped = 1
		}
		if sample.DroppedSpans != wantDropped {
			t.Errorf("%s child changed dropped spans=%d, want%d", mode, sample.DroppedSpans, wantDropped)
		}
	}
}

func storePrepareClockDetail(t *testing.T, sample *Sample) map[string]int64 {
	t.Helper()
	if sample == nil {
		t.Fatal("missing sampled owner")
	}
	for _, span := range sample.Spans {
		if span.Stage != StageStoreAppendPrepare {
			continue
		}
		body, err := json.Marshal(span)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(body, &fields); err != nil {
			t.Fatal(err)
		}
		raw, found := fields["store_prepare_detail"]
		if !found {
			t.Fatal("missing fixed numeric preparation child")
		}
		var detail map[string]int64
		if err := json.Unmarshal(raw, &detail); err != nil {
			t.Fatal("preparation child is not numeric")
		}
		if len(detail) != 5 {
			t.Fatal("preparation child changed fixed numeric cardinality")
		}
		return detail
	}
	t.Fatal("missing existing preparation parent")
	return nil
}
