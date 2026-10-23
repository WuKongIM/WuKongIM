package chatlifecycle

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/bench/target"
)

// legacyMetricsFetchWire deliberately uses default decoding, as the DTO did
// before optional parser diagnostics. It is an independent compatibility oracle.
type legacyMetricsFetchWire struct {
	SourceMask uint8                        `json:"source_mask"`
	Kinds      [3]target.MetricsFailureKind `json:"kinds"`
}

func TestMetricsParseReasonsPreserveLegacyReceiverFields(t *testing.T) {
	initial := legacyMetricsFetchWire{SourceMask: 5, Kinds: [3]target.MetricsFailureKind{"parse", "", "transport"}}
	for _, tc := range []struct {
		name        string
		encoded     string
		wantReasons *[3]target.MetricsParseReason
	}{
		{"valid_new_only", `{"parse_reasons":["invalid_value","",""]}`, &[3]target.MetricsParseReason{"invalid_value", "", ""}},
		{"malformed_new_only", `{"parse_reasons":[null,"",""]}`, nil},
		{"wrong_new_type", `{"parse_reasons":false}`, nil},
		{"empty_object", `{}`, nil},
		{"old_null_mask", `{"source_mask":null}`, nil},
		{"old_null_array", `{"kinds":null}`, nil},
		{"old_both_null", `{"source_mask":null,"kinds":null,"parse_reasons":[null,"",""]}`, nil},
		{"explicit_zero_mask", `{"source_mask":0}`, nil},
		{"explicit_zero_array", `{"kinds":["","",""]}`, nil},
		{"empty_array", `{"kinds":[]}`, nil},
		{"short_array", `{"kinds":["parse"]}`, nil},
		{"short_null_array", `{"kinds":[null]}`, nil},
		{"array_null_cells", `{"kinds":[null,null,null]}`, nil},
		{"partial_old_update", `{"source_mask":1,"kinds":["parse"]}`, nil},
		{"malformed_new_with_old_update", `{"source_mask":1,"kinds":["parse"],"parse_reasons":{}}`, nil},
		{"explicit_old_zero_and_bad_new", `{"source_mask":0,"kinds":[],"parse_reasons":[false,"",""]}`, nil},
		{"duplicate_old_null", `{"source_mask":1,"source_mask":null,"kinds":["parse"],"kinds":null}`, nil},
		{"whole_null", `null`, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			legacy := initial
			priorReasons := [3]target.MetricsParseReason{"invalid_queue_utilization", "", ""}
			current := MetricsFetchFailure{SourceMask: initial.SourceMask, Kinds: initial.Kinds, ParseReasons: &priorReasons}
			if err := json.Unmarshal([]byte(tc.encoded), &legacy); err != nil {
				t.Fatal("legacy fixture rejected:", err)
			}
			if err := json.Unmarshal([]byte(tc.encoded), &current); err != nil {
				t.Fatal("current fixture rejected:", err)
			}
			if current.SourceMask != legacy.SourceMask || current.Kinds != legacy.Kinds {
				t.Errorf("old fields changed: mask=%d kinds=%v; legacy mask=%d kinds=%v", current.SourceMask, current.Kinds, legacy.SourceMask, legacy.Kinds)
			}
			if !reflect.DeepEqual(current.ParseReasons, tc.wantReasons) {
				t.Errorf("optional reasons=%v want=%v", current.ParseReasons, tc.wantReasons)
			}
		})
	}
}

func TestMetricsParseReasonsPreserveRepeatedOuterFields(t *testing.T) {
	const initial = `{"source_mask":5,"kinds":["parse","","transport"]}`
	for _, delta := range []string{
		`{"parse_reasons":[null,"",""]}`,
		`{"parse_reasons":["invalid_value","",""]}`,
		`{"parse_reasons":false}`,
		`{}`,
		`{"source_mask":null}`,
		`{"kinds":null}`,
		`{"source_mask":null,"kinds":null,"parse_reasons":[null,"",""]}`,
		`{"source_mask":0}`,
		`{"kinds":[]}`,
		`{"kinds":["parse"]}`,
		`{"kinds":[null]}`,
		`{"kinds":[null,null,null]}`,
		`{"source_mask":1,"kinds":["parse"],"parse_reasons":{}}`,
		`null`,
	} {
		t.Run(delta, func(t *testing.T) {
			encoded := []byte(`{"stage":"meta_checkpoint","reason":"metrics_fetch","metrics_fetch":` + initial + `,"metrics_fetch":` + delta + `}`)
			var legacy struct {
				MetricsFetch *legacyMetricsFetchWire `json:"metrics_fetch"`
			}
			var current FinalizationFailure
			if json.Unmarshal(encoded, &legacy) != nil || json.Unmarshal(encoded, &current) != nil {
				t.Fatal("valid duplicate-object JSON was rejected")
			}
			if legacy.MetricsFetch == nil {
				if current.MetricsFetch != nil {
					t.Fatal("explicit outer null no longer clears the pointer")
				}
				return
			}
			if current.MetricsFetch == nil || current.MetricsFetch.SourceMask != legacy.MetricsFetch.SourceMask || current.MetricsFetch.Kinds != legacy.MetricsFetch.Kinds {
				t.Fatal("duplicate outer object changed legacy mask/kinds")
			}
			want := FinalizationFailure{Stage: "meta_checkpoint", Reason: "metrics_fetch", MetricsFetch: &MetricsFetchFailure{SourceMask: legacy.MetricsFetch.SourceMask, Kinds: legacy.MetricsFetch.Kinds}}
			wantBounded, gotBounded := want.Bounded(), current.Bounded()
			if (gotBounded.MetricsFetch == nil) != (wantBounded.MetricsFetch == nil) {
				t.Fatal("optional field changed legacy bounded diagnostic presence")
			}
		})
	}
}

func TestMetricsParseReasonsReuseStillRejectsOldErrors(t *testing.T) {
	for _, encoded := range []string{
		`{"source_mask":"wrong","parse_reasons":false}`,
		`{"source_mask":256,"parse_reasons":false}`,
		`{"kinds":false,"parse_reasons":false}`,
		`{"kinds":[1],"parse_reasons":false}`,
		`{"source_mask":5,"parse_reasons":[null,]}`,
		`{"parse_reasons":{}} trailing`,
	} {
		t.Run(encoded, func(t *testing.T) {
			legacy := legacyMetricsFetchWire{SourceMask: 5, Kinds: [3]target.MetricsFailureKind{"parse", "", "transport"}}
			current := MetricsFetchFailure{SourceMask: legacy.SourceMask, Kinds: legacy.Kinds}
			if json.Unmarshal([]byte(encoded), &legacy) == nil {
				t.Fatal("legacy rejection fixture became valid")
			}
			if json.Unmarshal([]byte(encoded), &current) == nil {
				t.Fatal("optional field accepted invalid JSON or an old field error")
			}
		})
	}
}
