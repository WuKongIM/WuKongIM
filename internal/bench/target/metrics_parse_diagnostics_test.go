package target

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

// This table is the approved pre-implementation diagnostic catalog, not a
// claim that defensive helper branches are reachable through an HTTP scrape.
var parseLeafCatalog = []struct {
	reason MetricsParseReason
	text   string
}{
	{"line_limit", "observation metrics line limit exceeded"},
	{"series_limit", "observation metrics series cardinality limit exceeded"},
	{"invalid_value", "observation metrics contains invalid value"},
	{"incomplete_metadata_results", "observation metrics contains incomplete metadata-create results"},
	{"invalid_sample", "observation metrics contains invalid sample"},
	{"invalid_labels", "observation metrics contains invalid labels"},
	{"invalid_metadata_labels", "observation metrics contains invalid metadata-create labels"},
	{"invalid_metadata_slot", "observation metrics contains invalid metadata-create Slot"},
	{"invalid_metadata_counter", "observation metrics contains invalid metadata-create counter"},
	{"invalid_metadata_result", "observation metrics contains invalid metadata-create result"},
	{"duplicate_metadata_slot_result", "observation metrics contains duplicate metadata-create Slot result"},
	{"aggregate_overflow", "observation metrics aggregate overflow"},
	{"unsupported_family", "observation metrics contains unsupported family"},
	{"invalid_queue_labels", "observation metrics contains invalid queue labels"},
	{"invalid_queue_family", "observation metrics contains invalid queue family"},
	{"duplicate_or_excess_queue_series", "observation metrics contains duplicate or excessive queue series"},
	{"unpaired_queue_series", "observation metrics contains unpaired queue series"},
	{"invalid_queue_utilization", "observation metrics contains invalid queue utilization"},
	{"runtime_queue_missing_capacity", "observation metrics contains invalid queue utilization"},
	{"runtime_queue_over_capacity", "observation metrics contains invalid queue utilization"},
	{"channel_queue_missing_capacity", "observation metrics contains invalid queue utilization"},
	{"channel_queue_over_capacity", "observation metrics contains invalid queue utilization"},
	{"invalid_sample_name", "observation metrics contains invalid sample name"},
	{"label_cardinality_limit", "observation metrics label cardinality limit exceeded"},
}

func parseLeafMetrics(t *testing.T, body string) (MetricsSnapshot, error) {
	t.Helper()
	client := NewClient(Config{APIAddrs: []string{"http://parser.invalid"}, HTTPClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodGet || req.URL.Host != "parser.invalid" || req.URL.Path != "/metrics" {
			t.Fatal("unexpected in-memory parser request")
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), ContentLength: int64(len(body)), Request: req}, nil
	})}})
	return client.Metrics(context.Background())
}

func parseLeafMeta(slot, result, number string) string {
	return fmt.Sprintf("wukongim_channelv2_meta_created_total{slot_id=%q,result=%q} %s\n", slot, result, number)
}
func parseLeafLabels(count int) string {
	labels := make([]string, count)
	for i := range count {
		labels[i] = fmt.Sprintf("l%d=\"v\"", i)
	}
	return strings.Join(labels, ",")
}
func parseLeafQueues(count int) string {
	var out strings.Builder
	for i := range count {
		fmt.Fprintf(&out, "wukongim_runtime_pool_queue_depth{pool=\"p%d\"} 1\nwukongim_runtime_pool_queue_capacity{pool=\"p%d\"} 1\n", i, i)
	}
	return out.String()
}

func TestMetricsParseLeafCatalogKeepsPrivateErrorText(t *testing.T) {
	for _, tc := range parseLeafCatalog {
		t.Run(string(tc.reason), func(t *testing.T) {
			err := newMetricsParseError(tc.reason)
			if err.Error() != tc.text {
				t.Errorf("original parser text changed: %q", err.Error())
			}
			if got := MetricsParseFailure(err); got != tc.reason {
				t.Errorf("reason=%q want=%q", got, tc.reason)
			}
			if got := tc.reason.Bounded(); got != tc.reason {
				t.Errorf("known reason=%q", got)
			}
		})
	}
}

func TestMetricsParseLeavesAtRealParserBoundaries(t *testing.T) {
	cases := []struct {
		name   string
		reason MetricsParseReason
		body   string
		helper func() error
	}{
		{"line_count", "line_limit", strings.Repeat("# comment\n", 100001), nil},
		{"line_token", "line_limit", "go_goroutines " + strings.Repeat("1", 65537) + "\n", nil},
		{"series_count", "series_limit", strings.Repeat("ignored_family 1\n", 32769), nil},
		{"nonfinite", "invalid_value", "go_goroutines NaN\n", nil},
		{"negative_metadata_first_guard", "invalid_value", parseLeafMeta("1", "created", "-1"), nil},
		{"metadata_incomplete", "incomplete_metadata_results", parseLeafMeta("1", "created", "0"), nil},
		{"sample_fields", "invalid_sample", "go_goroutines 1 2\n", nil},
		{"labels_syntax", "invalid_labels", "go_goroutines{l=unquoted} 1\n", nil},
		{"metadata_labels", "invalid_metadata_labels", "wukongim_channelv2_meta_created_total{slot_id=\"1\",result=\"created\",extra=\"x\"} 0\n", nil},
		{"metadata_slot", "invalid_metadata_slot", parseLeafMeta("13", "created", "0"), nil},
		{"metadata_slot_noncanonical", "invalid_metadata_slot", parseLeafMeta("01", "created", "0"), nil},
		{"metadata_fraction", "invalid_metadata_counter", parseLeafMeta("1", "created", "0.5"), nil},
		{"metadata_counter_bound", "invalid_metadata_counter", parseLeafMeta("1", "created", "9007199254740993"), nil},
		{"metadata_result", "invalid_metadata_result", parseLeafMeta("1", "other", "0"), nil},
		{"metadata_duplicate", "duplicate_metadata_slot_result", parseLeafMeta("1", "created", "0") + parseLeafMeta("1", "created", "1"), nil},
		{"float_sum", "aggregate_overflow", "go_goroutines 1.7976931348623157e+308\ngo_goroutines 1.7976931348623157e+308\n", nil},
		{"metadata_sum", "aggregate_overflow", parseLeafMeta("1", "created", "9007199254740992") + parseLeafMeta("2", "created", "1"), nil},
		{"unsupported_helper", "unsupported_family", "", func() error { var s MetricsSnapshot; return s.addMetric(0, nil, 0, "0") }},
		// map[string]string JSON cannot normally reach invalid_queue_labels.
		// Its exact typed text is covered by the catalog, without a product hook.
		{"queue_family_helper", "invalid_queue_family", "", func() error { var s MetricsSnapshot; return s.addQueueSeries(0, nil, 0) }},
		{"queue_duplicate", "duplicate_or_excess_queue_series", "wukongim_runtime_pool_queue_depth{pool=\"p\"} 1\nwukongim_runtime_pool_queue_depth{pool=\"p\"} 2\n", nil},
		{"queue_count", "duplicate_or_excess_queue_series", parseLeafQueues(129), nil},
		{"queue_unpaired", "unpaired_queue_series", "wukongim_runtime_pool_queue_depth{pool=\"a\"} 1\nwukongim_runtime_pool_queue_depth{pool=\"b\"} 1\nwukongim_runtime_pool_queue_capacity{pool=\"a\"} 2\n", nil},
		{"queue_over_capacity", "runtime_queue_over_capacity", "wukongim_runtime_pool_queue_depth{pool=\"a\"} 2\nwukongim_runtime_pool_queue_capacity{pool=\"a\"} 1\n", nil},
		{"queue_missing_key_same_count", "runtime_queue_missing_capacity", "wukongim_runtime_pool_queue_depth{pool=\"a\"} 1\nwukongim_runtime_pool_queue_capacity{pool=\"b\"} 2\n", nil},
		{"sample_name_helper", "invalid_sample_name", "", func() error { _, _, err := parseObservationMetricToken("}"); return err }},
		{"label_count", "label_cardinality_limit", "go_goroutines{" + parseLeafLabels(33) + "} 1\n", nil},
		{"label_duplicate", "label_cardinality_limit", "go_goroutines{l=\"a\",l=\"b\"} 1\n", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var err error
			if tc.helper != nil {
				err = tc.helper()
			} else {
				_, err = parseLeafMetrics(t, tc.body)
			}
			if err == nil {
				t.Fatal("invalid scrape became success")
			}
			if tc.helper == nil && (MetricsFailure(err) != "parse" || err.Error() != "observation metrics parse") {
				t.Errorf("outer parse kind/text changed: %v", err)
			}
			if got := MetricsParseFailure(err); got != tc.reason {
				t.Errorf("reason=%q want=%q", got, tc.reason)
			}
			cause := err
			if tc.helper == nil {
				cause = errors.Unwrap(err)
			}
			for _, known := range parseLeafCatalog {
				if known.reason == tc.reason && cause.Error() != known.text {
					t.Errorf("original cause text changed: %q", cause.Error())
				}
			}
		})
	}
}

func TestMetricsQueueFailureReasonsThroughClient(t *testing.T) {
	cases := []struct {
		name   string
		body   string
		reason MetricsParseReason
	}{
		{"runtime_missing_capacity", "wukongim_runtime_pool_queue_depth{pool=\"a\"} 1\nwukongim_runtime_pool_queue_capacity{pool=\"b\"} 2\n", "runtime_queue_missing_capacity"},
		{"runtime_over_capacity", "wukongim_runtime_pool_queue_depth{pool=\"a\"} 2\nwukongim_runtime_pool_queue_capacity{pool=\"a\"} 1\n", "runtime_queue_over_capacity"},
		{"channel_missing_capacity", "wukongim_channelv2_worker_queue_depth{pool=\"a\"} 1\nwukongim_channelv2_worker_queue_capacity{pool=\"b\"} 2\n", "channel_queue_missing_capacity"},
		{"channel_over_capacity", "wukongim_channelv2_worker_queue_depth{pool=\"a\"} 2\nwukongim_channelv2_worker_queue_capacity{pool=\"a\"} 1\n", "channel_queue_over_capacity"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != http.MethodGet || r.URL.Path != "/metrics" {
					t.Error("unexpected metrics request")
					http.Error(w, "unexpected request", http.StatusNotFound)
					return
				}
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			client := NewClient(Config{APIAddrs: []string{server.URL}})
			_, err := client.Metrics(context.Background())
			if err == nil {
				t.Fatal("invalid queue scrape became success")
			}
			if got := MetricsParseFailure(err); got != tc.reason {
				t.Errorf("reason=%q want=%q", got, tc.reason)
			}
			if MetricsFailure(err) != "parse" || err.Error() != "observation metrics parse" {
				t.Errorf("outer parse kind/text changed: %v", err)
			}
			if cause := errors.Unwrap(err); cause == nil || cause.Error() != "observation metrics contains invalid queue utilization" {
				t.Errorf("fixed parser cause text changed: %v", cause)
			}
			if got := requests.Load(); got != 1 {
				t.Errorf("metrics requests=%d want=1", got)
			}
		})
	}
}

func TestMetricsParseLeafValidAndExistingGuardControls(t *testing.T) {
	meta := parseLeafMeta("1", "created", "9007199254740992") + parseLeafMeta("12", "already_existing", "0") + parseLeafMeta("12", "error", "0")
	cases := map[string]string{
		"line_limit":                    strings.Repeat("\n", 100000),
		"series_limit":                  strings.Repeat("ignored_family 1\n", 32768),
		"finite":                        "go_goroutines " + strconv.FormatFloat(math.MaxFloat64, 'g', -1, 64) + "\n",
		"zero":                          "go_goroutines 0\n",
		"negative_zero":                 "go_goroutines -0\n",
		"metadata_limits":               meta,
		"queue_limit":                   parseLeafQueues(128),
		"queue_zero_capacity":           "wukongim_runtime_pool_queue_depth{pool=\"p\"} 8\nwukongim_runtime_pool_queue_capacity{pool=\"p\"} 0\n",
		"channel_queue_zero_capacity":   "wukongim_channelv2_worker_queue_depth{pool=\"p\"} 8\nwukongim_channelv2_worker_queue_capacity{pool=\"p\"} 0\n",
		"queue_single_family_unchanged": "wukongim_runtime_pool_queue_depth{pool=\"p\"} 1\n",
		"unknown_family_ignored":        "foreign_family invalid sample is ignored\n",
		"label_limit":                   "go_goroutines{" + parseLeafLabels(32) + "} 1\n",
		"empty_still_parses":            "",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			snapshot, err := parseLeafMetrics(t, body)
			if err != nil || MetricsParseFailure(err) != "" {
				t.Fatalf("accepted control changed: %v", err)
			}
			if name == "empty_still_parses" && snapshot.ValidateRequired() == nil {
				t.Error("separate required-family guard changed")
			}
			if name == "queue_zero_capacity" && snapshot.RuntimeQueueMaxPercent != 0 {
				t.Error("zero-capacity denominator changed")
			}
			if name == "channel_queue_zero_capacity" && snapshot.ChannelWorkerQueueMaxPercent != 0 {
				t.Error("channel zero-capacity denominator changed")
			}
		})
	}
}

type parseLeafSecret struct{ formatted atomic.Int32 }

func (e *parseLeafSecret) Error() string            { e.formatted.Add(1); return "private-token raw-metric-label" }
func (*parseLeafSecret) MetricsParseReason() string { return "invalid_value" }

func TestMetricsParseReasonProvenanceAndUnknown(t *testing.T) {
	secret := &parseLeafSecret{}
	var typedNil *metricsParseError
	cases := []struct {
		name string
		err  error
		want MetricsParseReason
	}{
		{"nil", nil, ""},
		{"same_text_is_not_provenance", errors.New("observation metrics contains invalid value"), "unknown"},
		{"foreign_interface", secret, "unknown"},
		{"typed_nil", typedNil, "unknown"},
		{"forged_private_enum", &metricsParseError{reason: "private-token"}, "unknown"},
		{"wrapped_authentic", fmt.Errorf("wrapper: %w", newMetricsParseError("invalid_value")), "invalid_value"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := MetricsParseFailure(tc.err); got != tc.want {
				t.Errorf("reason=%q want=%q", got, tc.want)
			}
		})
	}
	if secret.formatted.Load() != 0 {
		t.Fatal("diagnostic formatted a foreign error")
	}
	for _, value := range []MetricsParseReason{"", "forged", MetricsParseReason(strings.Repeat("private-token", 1000))} {
		if value.Bounded() != "unknown" {
			t.Error("unknown reason escaped closed vocabulary")
		}
	}
	encoded, err := json.Marshal((&metricsParseError{reason: "private-token"}).reason.Bounded())
	if err != nil || strings.Contains(string(encoded), "private") {
		t.Error("private reason escaped bounded output")
	}
}
