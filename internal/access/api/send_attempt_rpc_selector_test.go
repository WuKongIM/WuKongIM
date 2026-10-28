package api

import (
	"encoding/json"
	"math"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/observability/diagnostics"
	"github.com/WuKongIM/WuKongIM/pkg/observability/rpctrace"
)

// F11: exact identity, side and service are required together. Latest events,
// nil typed payloads, a different service or duplicate matches are not proof.
func TestAttemptRPCSelectorValidatesEveryTypedIdentityAndDetaches(t *testing.T) {
	id := "0123456789abcdef0123456789abcdef/8000000000000001"
	selector := RPCSelector{TraceID: id, Side: rpctrace.SideServer, ServiceID: 21}
	original := rpctrace.Sample{TraceID: id, Side: rpctrace.SideServer, ServiceID: 21, At: time.Unix(10, 0), Result: rpctrace.ResultOK, Spans: []rpctrace.SpanSample{{Stage: rpctrace.Stage("runtime_future_wait"), Duration: time.Millisecond, Result: rpctrace.ResultOK}}}
	base := diagnostics.Event{TraceID: id, Stage: diagnostics.StageRPCServer, RPC: &original}
	for _, tc := range []struct {
		name   string
		mutate func(*diagnostics.Event)
		want   string
	}{
		{"exact", func(*diagnostics.Event) {}, "retained"},
		{"wrong outer ID", func(e *diagnostics.Event) { e.TraceID = "11112233445566778899aabbccddeeff/1" }, "unknown"},
		{"wrong stage", func(e *diagnostics.Event) { e.Stage = diagnostics.StageRPCClient }, "unknown"},
		{"wrong inner ID", func(e *diagnostics.Event) { e.RPC.TraceID = "11112233445566778899aabbccddeeff/1" }, "unknown"},
		{"wrong side", func(e *diagnostics.Event) { e.RPC.Side = rpctrace.SideClient }, "unknown"},
		{"wrong service", func(e *diagnostics.Event) { e.RPC.ServiceID = 86 }, "unknown"},
		{"missing sample", func(e *diagnostics.Event) { e.RPC = nil }, "unknown"},
		{"unbound local identity", func(e *diagnostics.Event) { e.RPC.Unbound = true }, "unknown"},
		{"too many spans", func(e *diagnostics.Event) { e.RPC.Spans = make([]rpctrace.SpanSample, 33) }, "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sample := original.Clone()
			event := base
			event.RPC = &sample
			tc.mutate(&event)
			got := selectExactRPCSample(selector, diagnostics.QueryResult{Events: []diagnostics.Event{event}})
			if got.Status != tc.want {
				t.Fatalf("status=%s want%s", got.Status, tc.want)
			}
			if tc.want != "retained" {
				if got.Sample != nil {
					t.Fatal("unknown carried a misleading sample")
				}
				return
			}
			got.Sample.Spans[0].Duration = time.Hour
			if original.Spans[0].Duration != time.Millisecond || sample.Spans[0].Duration != time.Millisecond {
				t.Fatal("pure selector returned shared sample backing")
			}
		})
	}
	if got := selectExactRPCSample(selector, diagnostics.QueryResult{}); got.Status != "not_retained" || got.Sample != nil {
		t.Fatal("empty source is not_retained")
	}
	if got := selectExactRPCSample(selector, diagnostics.QueryResult{Events: []diagnostics.Event{base, base}}); got.Status != "ambiguous" || got.Sample != nil {
		t.Fatal("duplicate exact matches must remain ambiguous")
	}
}
func TestAttemptRPCMaximumTypedEncodingIncludesCohortAndExistingOptionalFields(t *testing.T) {
	raw, err := os.ReadFile("testdata/attempt_rpc_max.json")
	if err != nil {
		t.Fatal(err)
	}
	var sample rpctrace.Sample
	if err = json.Unmarshal(raw, &sample); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(sample)
	if err != nil {
		t.Fatal(err)
	}
	// Do not let a baseline decoder silently drop the future optional sidecar
	// and then claim the smaller JSON proves the candidate's actual budget.
	var shape map[string]json.RawMessage
	if err = json.Unmarshal(encoded, &shape); err != nil {
		t.Fatal(err)
	}
	var cohorts struct {
		Links         []json.RawMessage `json:"links"`
		Dropped       uint64            `json:"dropped"`
		OwnersDropped uint64            `json:"owners_dropped"`
	}
	if err = json.Unmarshal(shape["commit_cohorts"], &cohorts); err != nil || len(cohorts.Links) != 2 || cohorts.Dropped != math.MaxUint64 || cohorts.OwnersDropped != math.MaxUint64 {
		t.Fatalf("typed candidate lost two-link maximum fixture: %v %s", err, shape["commit_cohorts"])
	}
	if len(sample.Spans) != 32 || sample.Dispatch == nil || sample.ReceivePrefix == nil || sample.BarrierCoverage == nil || sample.BarrierCoverage.OwnerStorage == nil || sample.BarrierCoverage.StorageCountPaused == nil {
		t.Fatal("maximum fixture omitted an existing optional family")
	}
	response := SendAttemptQueryResponse{Version: 1, Status: "ok", RPCResults: make([]RPCSelectorResult, 16)}
	for i := range response.RPCResults {
		copy := sample.Clone()
		response.RPCResults[i] = RPCSelectorResult{Selector: RPCSelector{TraceID: sample.TraceID, Side: rpctrace.SideServer, ServiceID: 21}, Status: "retained", Sample: &copy}
	}
	body, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	if len(body)+1 > 256<<10 {
		t.Fatalf("shrink optional diagnostics rather than expand the 256KiB RPC cap: %d", len(body)+1)
	}
	t.Logf("maximum-width typed RPC response=%d bytes (32 spans, 16 dispatch points, all barrier sidecars, two physical cohorts per each of 16 selectors)", len(body)+1)
	// Values are a conservative byte maximum, not a physically consistent
	// timeline. Encoding success makes no duration/root-cause assertion.
}
func TestAttemptRPCSelectedCohortBackingIsDetached(t *testing.T) {
	raw, err := os.ReadFile("testdata/attempt_rpc_max.json")
	if err != nil {
		t.Fatal(err)
	}
	var original rpctrace.Sample
	if err = json.Unmarshal(raw, &original); err != nil {
		t.Fatal(err)
	}
	id := "0123456789abcdef0123456789abcdef/8000000000000001"
	original.TraceID = id
	original.Unbound = false
	before, _ := json.Marshal(original)
	got := selectExactRPCSample(RPCSelector{TraceID: id, Side: rpctrace.SideServer, ServiceID: 21}, diagnostics.QueryResult{Events: []diagnostics.Event{{TraceID: id, Stage: diagnostics.StageRPCServer, RPC: &original}}})
	if got.Sample == nil {
		t.Fatal("fixture must retain typed sample before testing copy ownership")
	}
	// Decode a new sidecar over the selected sample, replacing mutable backing;
	// producer and later query snapshots must retain their previous bytes.
	if err = json.Unmarshal([]byte(`{"commit_cohorts":{"links":[],"dropped":0}}`), got.Sample); err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(original)
	if string(before) != string(after) {
		t.Fatal("selected sidecar mutated source-owned sample")
	}
	if !strings.Contains(string(after), `"commit_cohorts"`) {
		t.Fatal("candidate did not retain physical cohort field")
	}
}
