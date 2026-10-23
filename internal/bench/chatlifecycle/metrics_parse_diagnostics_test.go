package chatlifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/bench/target"
)

func parseDiagnosticJSON(vector string) []byte {
	return []byte(`{"stage":"meta_checkpoint","reason":"metrics_fetch","metrics_fetch":{"source_mask":5,"kinds":["parse","","transport"]` + vector + `}}`)
}
func parseDiagnosticDecode(t *testing.T, raw []byte) FinalizationFailure {
	t.Helper()
	var value FinalizationFailure
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	return value.Bounded()
}

func TestMetricsParseReasonsCollectedInDeclaredSourceOrder(t *testing.T) {
	typed := metricsFetchTargetError(t, "parse")
	for mask := 1; mask < 8; mask++ {
		t.Run(fmt.Sprint(mask), func(t *testing.T) {
			var sources [3]ProductionMetaMetricsSource
			for i := range sources {
				sources[i] = metricsFetchSourceFunc(func(context.Context) (target.MetricsSnapshot, error) {
					if mask&(1<<i) != 0 {
						return target.MetricsSnapshot{}, typed
					}
					return target.MetricsSnapshot{GoGoroutines: float64(i + 1)}, nil
				})
			}
			_, err := collectProductionMetaMetrics(context.Background(), sources)
			var failure *productionMetaMetricsFailure
			if !errors.As(err, &failure) || failure == nil {
				t.Fatal("missing metrics failure")
			}
			if !errors.Is(err, ErrLifecycleHarnessInvalid) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				t.Fatal("aggregate error chain changed")
			}
			bounded := failure.diagnostic.bounded()
			if bounded.SourceMask != uint8(mask) || bounded.ParseReasons == nil {
				t.Fatal("source diagnostic missing")
			}
			for i := range 3 {
				want := target.MetricsParseReason("")
				if mask&(1<<i) != 0 {
					want = "invalid_value"
				}
				if bounded.ParseReasons[i] != want {
					t.Errorf("source%d reason=%q want=%q", i, bounded.ParseReasons[i], want)
				}
			}
		})
	}
}

func TestMetricsParseReasonsOptionalJSONCompatibility(t *testing.T) {
	tests := []struct {
		name  string
		field string
		want  *[3]target.MetricsParseReason
	}{
		{"old_absent", "", nil},
		{"null", `,"parse_reasons":null`, nil},
		{"known", `,"parse_reasons":["invalid_queue_utilization","",""]`, &[3]target.MetricsParseReason{"invalid_queue_utilization", "", ""}},
		{"unknown", `,"parse_reasons":["private-token","",""]`, &[3]target.MetricsParseReason{"unknown", "", ""}},
		{"empty_parse", `,"parse_reasons":["","",""]`, &[3]target.MetricsParseReason{"unknown", "", ""}},
		{"stray_nonparse", `,"parse_reasons":["invalid_value","private-token","invalid_value"]`, &[3]target.MetricsParseReason{"invalid_value", "", ""}},
		{"object", `,"parse_reasons":{"private":"token"}`, nil},
		{"scalar", `,"parse_reasons":"private-token"`, nil},
		{"short0", `,"parse_reasons":[]`, nil},
		{"short1", `,"parse_reasons":["invalid_value"]`, nil},
		{"short2", `,"parse_reasons":["invalid_value",""]`, nil},
		{"long4", `,"parse_reasons":["invalid_value","","","private-token"]`, nil},
		{"nonstring", `,"parse_reasons":["invalid_value",1,""]`, nil},
		{"nested", `,"parse_reasons":[["invalid_value"],"",""]`, nil},
		{"decoded_string_limit", `,"parse_reasons":["` + strings.Repeat("p", 65) + `","",""]`, nil},
		{"raw_byte_limit", `,"parse_reasons":[` + strings.Repeat(" ", 1025) + `"invalid_value","",""]`, nil},
		{"huge_field", `,"parse_reasons":{"private":"` + strings.Repeat("private-token", 10000) + `"}`, nil},
		{"duplicate_last_invalid", `,"parse_reasons":["invalid_value","",""],"parse_reasons":false`, nil},
		{"duplicate_last_valid", `,"parse_reasons":false,"parse_reasons":["invalid_value","",""]`, &[3]target.MetricsParseReason{"invalid_value", "", ""}},
		{"escaped_known", `,"parse_reasons":["invalid\u005fvalue","",""]`, &[3]target.MetricsParseReason{"invalid_value", "", ""}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parseDiagnosticDecode(t, parseDiagnosticJSON(tc.field))
			if got.Stage != "meta_checkpoint" || got.Reason != "metrics_fetch" || got.MetricsFetch == nil || got.MetricsFetch.SourceMask != 5 || got.MetricsFetch.Kinds != [3]target.MetricsFailureKind{"parse", "", "transport"} {
				t.Fatal("optional field changed old diagnostic")
			}
			if !reflect.DeepEqual(got.MetricsFetch.ParseReasons, tc.want) {
				t.Errorf("reasons=%v want=%v", got.MetricsFetch.ParseReasons, tc.want)
			}
			raw, err := json.Marshal(got)
			if err != nil || strings.Contains(string(raw), "private") {
				t.Fatal("private optional data escaped")
			}
			var old struct {
				Stage        string
				Reason       string
				MetricsFetch struct {
					SourceMask uint8 `json:"source_mask"`
					Kinds      [3]string
				} `json:"metrics_fetch"`
			}
			if json.Unmarshal(raw, &old) != nil || old.MetricsFetch.SourceMask != 5 || old.MetricsFetch.Kinds != [3]string{"parse", "", "transport"} {
				t.Fatal("old reader compatibility changed")
			}
		})
	}
}

func TestMetricsParseReasonsDoNotAcceptInvalidJSONOrOldFields(t *testing.T) {
	invalid := []string{
		`{"stage":"meta_checkpoint","reason":"metrics_fetch","metrics_fetch":{"source_mask":5,"kinds":["parse","","transport"],"parse_reasons":[}}`,
		`{"metrics_fetch":{"source_mask":"wrong","kinds":["parse","","transport"],"parse_reasons":false}}`,
		`{"metrics_fetch":{"source_mask":5,"kinds":"wrong","parse_reasons":false}}`,
		`{"metrics_fetch":{"source_mask":5,"kinds":["parse","","transport"],"parse_reasons":{"ignored":[1,]}}}`,
		string(parseDiagnosticJSON(`,"parse_reasons":["invalid_value","",""]`)) + ` trailing`,
	}
	for i, raw := range invalid {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			var value FinalizationFailure
			if json.Unmarshal([]byte(raw), &value) == nil {
				t.Fatal("overall invalid JSON or old type was accepted")
			}
		})
	}
}

func TestMetricsParseReasonsMaskStageAndOwnershipControls(t *testing.T) {
	for _, raw := range []string{
		`{"stage":"meta_checkpoint","reason":"metrics_fetch","metrics_fetch":{"source_mask":0,"kinds":["","",""],"parse_reasons":["invalid_value","",""]}}`,
		`{"stage":"meta_checkpoint","reason":"metrics_fetch","metrics_fetch":{"source_mask":8,"kinds":["parse","",""],"parse_reasons":["invalid_value","",""]}}`,
		`{"stage":"meta_checkpoint","reason":"metrics_fetch","metrics_fetch":{"source_mask":1,"kinds":["","",""],"parse_reasons":["invalid_value","",""]}}`,
		`{"stage":"meta_checkpoint","reason":"metrics_fetch","metrics_fetch":{"source_mask":1,"kinds":["parse","transport",""],"parse_reasons":["invalid_value","",""]}}`,
		`{"stage":"meta_checkpoint","reason":"context_canceled","metrics_fetch":{"source_mask":1,"kinds":["parse","",""],"parse_reasons":["invalid_value","",""]}}`,
		`{"stage":"write_json","reason":"output_failed","metrics_fetch":{"source_mask":1,"kinds":["parse","",""],"parse_reasons":["invalid_value","",""]}}`,
	} {
		if got := parseDiagnosticDecode(t, []byte(raw)); got.MetricsFetch != nil {
			t.Fatal("optional reasons rescued an invalid mask/stage")
		}
	}
	withoutParse := parseDiagnosticDecode(t, []byte(`{"stage":"meta_checkpoint","reason":"metrics_fetch","metrics_fetch":{"source_mask":1,"kinds":["transport","",""],"parse_reasons":["invalid_value","",""]}}`))
	if withoutParse.MetricsFetch == nil || withoutParse.MetricsFetch.ParseReasons != nil {
		t.Fatal("nonparse acquisition gained a parser reason")
	}
	input := [3]target.MetricsParseReason{"invalid_value", "", ""}
	original := FinalizationFailure{Stage: "meta_checkpoint", Reason: "metrics_fetch", MetricsFetch: &MetricsFetchFailure{SourceMask: 1, Kinds: [3]target.MetricsFailureKind{"parse", "", ""}, ParseReasons: &input}}
	bounded := original.Bounded()
	input[0] = "private-token"
	if bounded.MetricsFetch.ParseReasons == nil || bounded.MetricsFetch.ParseReasons[0] != "invalid_value" {
		t.Fatal("bounded reasons alias mutable input")
	}
}

func TestMetricsParseReasonsDoNotChangeObservedFailure(t *testing.T) {
	raw := []byte(`{"stage":"meta_checkpoint","reason":"metrics_fetch","observed_decision":"product_failure","observed_outcome":"product_failure","observed_cause":"message_loss","observed_terminal":true,"metrics_fetch":{"source_mask":4,"kinds":["","","parse"],"parse_reasons":["","","invalid_queue_utilization"]}}`)
	got := parseDiagnosticDecode(t, raw)
	if got.ObservedDecision != CoordinatorProductFailure || got.ObservedOutcome != VerdictProductFailure || got.ObservedCause != VerdictCauseMessageLoss || !got.ObservedTerminal {
		t.Fatal("diagnostic changed frozen product evidence")
	}
}

func TestMetricsParseReasonsDoNotReclassifyNonparseAcquisition(t *testing.T) {
	nested := metricsFetchTargetError(t, "parse")
	client := target.NewClient(target.Config{APIAddrs: []string{"http://node.invalid"}, HTTPClient: &http.Client{Transport: metricsFetchTripFunc(func(*http.Request) (*http.Response, error) { return nil, nested })}})
	_, outer := client.Metrics(context.Background())
	if target.MetricsFailure(outer) != "transport" {
		t.Fatal("fixture requires transport boundary")
	}
	var sources [3]ProductionMetaMetricsSource
	for i := range sources {
		sources[i] = metricsFetchSourceFunc(func(context.Context) (target.MetricsSnapshot, error) {
			if i == 0 {
				return target.MetricsSnapshot{}, outer
			}
			return target.MetricsSnapshot{}, nil
		})
	}
	_, err := collectProductionMetaMetrics(context.Background(), sources)
	var failure *productionMetaMetricsFailure
	if !errors.As(err, &failure) || failure.diagnostic.Kinds[0] != "transport" || failure.diagnostic.ParseReasons != nil {
		t.Fatal("nested parser cause escaped its acquisition kind")
	}
}
