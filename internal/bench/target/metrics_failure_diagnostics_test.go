package target

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// metricsDiagnosticContext changes cancellation deterministically without timers.
type metricsDiagnosticContext struct {
	mu   sync.Mutex
	done chan struct{}
	err  error
}

func newMetricsDiagnosticContext() *metricsDiagnosticContext {
	return &metricsDiagnosticContext{done: make(chan struct{})}
}
func (*metricsDiagnosticContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (c *metricsDiagnosticContext) Done() <-chan struct{}     { return c.done }
func (c *metricsDiagnosticContext) Err() error                { c.mu.Lock(); defer c.mu.Unlock(); return c.err }
func (*metricsDiagnosticContext) Value(any) any               { return nil }
func (c *metricsDiagnosticContext) stop(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err == nil {
		c.err = err
		close(c.done)
	}
}

type metricsDiagnosticSecret struct{ calls atomic.Int32 }

func (e *metricsDiagnosticSecret) Error() string {
	e.calls.Add(1)
	return "private-token private-uid http://private.invalid/response"
}

type metricsDiagnosticBody struct {
	reader               io.Reader
	closeErr             error
	reads, bytes, closes int
}

func (b *metricsDiagnosticBody) Read(p []byte) (int, error) {
	b.reads++
	n, err := b.reader.Read(p)
	b.bytes += n
	return n, err
}
func (b *metricsDiagnosticBody) Close() error { b.closes++; return b.closeErr }

type metricsDiagnosticReadFunc func([]byte) (int, error)

func (f metricsDiagnosticReadFunc) Read(p []byte) (int, error) { return f(p) }

type metricsDiagnosticRepeater struct{ remaining int64 }

func (r *metricsDiagnosticRepeater) Read(p []byte) (int, error) {
	if r.remaining == 0 {
		return 0, io.EOF
	}
	n := min(int64(len(p)), r.remaining)
	for i := range int(n) {
		p[i] = 'x'
	}
	r.remaining -= n
	return int(n), nil
}

func TestMetricsFailureBoundary(t *testing.T) {
	type testCase struct {
		name    string
		modes   []string
		kind    string
		calls   int
		initial error
		addrs   []string
		causal  error
		success bool
	}
	cases := []testCase{
		{name: "pre_canceled", kind: "context_canceled", initial: context.Canceled, causal: context.Canceled},
		{name: "pre_deadline", kind: "context_deadline", initial: context.DeadlineExceeded, causal: context.DeadlineExceeded},
		{name: "no_addresses", kind: "configuration", addrs: []string{" ", ""}},
		{name: "request_construction", kind: "configuration", addrs: []string{"://bad"}},
		{name: "transport", modes: []string{"transport"}, kind: "transport", calls: 1},
		{name: "client_timeout_live_parent", modes: []string{"transport_timeout"}, kind: "transport", calls: 1},
		{name: "causal_do_cancel", modes: []string{"transport_cancel"}, kind: "context_canceled", calls: 1, causal: context.Canceled},
		{name: "causal_do_deadline", modes: []string{"transport_deadline"}, kind: "context_deadline", calls: 1, causal: context.DeadlineExceeded},
		{name: "independent_do_at_cancel", modes: []string{"transport_cancel_independent"}, kind: "transport", calls: 1},
		{name: "context_at_next_address", modes: []string{"transport_stop_after"}, kind: "mixed", calls: 1, addrs: []string{"http://one.invalid", "http://two.invalid"}},
		{name: "unauthorized", modes: []string{"401", "valid"}, kind: "auth", calls: 1},
		{name: "forbidden", modes: []string{"403", "valid"}, kind: "auth", calls: 1},
		{name: "informational_status", modes: []string{"199"}, kind: "http_status", calls: 1},
		{name: "non_auth_client_status", modes: []string{"404"}, kind: "http_status", calls: 1},
		{name: "status", modes: []string{"503"}, kind: "http_status", calls: 1},
		{name: "redirect_without_location", modes: []string{"302"}, kind: "http_status", calls: 1},
		{name: "mixed_addresses", modes: []string{"transport", "503"}, kind: "mixed", calls: 2},
		{name: "same_statuses", modes: []string{"503", "503"}, kind: "http_status", calls: 2},
		{name: "same_transport", modes: []string{"transport", "transport"}, kind: "transport", calls: 2},
		{name: "fallback_success", modes: []string{"transport", "valid"}, calls: 2, success: true},
		{name: "fallback_status_success", modes: []string{"503", "valid"}, calls: 2, success: true},
		{name: "earlier_failure_then_auth", modes: []string{"transport", "403", "valid"}, kind: "auth", calls: 2},
		{name: "earlier_failure_then_length", modes: []string{"503", "huge_length", "valid"}, kind: "response_limit", calls: 2},
		{name: "content_length", modes: []string{"huge_length"}, kind: "response_limit", calls: 1},
		{name: "stream_length", modes: []string{"huge_read"}, kind: "response_limit", calls: 1},
		{name: "body_read", modes: []string{"body_fail"}, kind: "body_read", calls: 1},
		{name: "body_cancel", modes: []string{"body_cancel"}, kind: "context_canceled", calls: 1, causal: context.Canceled},
		{name: "body_deadline", modes: []string{"body_deadline"}, kind: "context_deadline", calls: 1, causal: context.DeadlineExceeded},
		{name: "body_independent_at_cancel", modes: []string{"body_cancel_independent"}, kind: "body_read", calls: 1},
		{name: "earlier_failure_then_body_cancel", modes: []string{"503", "body_cancel"}, kind: "mixed", calls: 2},
		{name: "earlier_failure_then_do_cancel", modes: []string{"body_fail", "transport_cancel"}, kind: "mixed", calls: 2},
		{name: "ignored_close_error", modes: []string{"valid_close_error"}, calls: 1, success: true},
		{name: "ignored_status_drain_error", modes: []string{"status_read_error"}, kind: "http_status", calls: 1},
		{name: "selected_body_parse_failure", modes: []string{"transport", "parse", "valid"}, kind: "parse", calls: 2},
		{name: "empty_204", modes: []string{"empty204"}, calls: 1, success: true},
		{name: "empty_body", modes: []string{"empty"}, calls: 1, success: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := newMetricsDiagnosticContext()
			if tc.initial != nil {
				ctx.stop(tc.initial)
			}
			secret := &metricsDiagnosticSecret{}
			var bodies []*metricsDiagnosticBody
			calls := 0
			hc := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				index := calls
				calls++
				if index >= len(tc.modes) {
					t.Errorf("unexpected request %d", calls)
					return nil, secret
				}
				mode := tc.modes[index]
				if req.Header.Get("Authorization") != "Bearer synthetic-token" {
					t.Error("auth header changed")
				}
				switch mode {
				case "transport":
					return nil, secret
				case "transport_timeout":
					return nil, &url.Error{Op: "Get", URL: "http://private.invalid", Err: context.DeadlineExceeded}
				case "transport_cancel":
					ctx.stop(context.Canceled)
					return nil, ctx.Err()
				case "transport_deadline":
					ctx.stop(context.DeadlineExceeded)
					return nil, ctx.Err()
				case "transport_cancel_independent", "transport_stop_after":
					ctx.stop(context.Canceled)
					return nil, secret
				}
				status := http.StatusOK
				length := int64(-1)
				var reader io.Reader = strings.NewReader("go_goroutines 7\n")
				switch mode {
				case "401":
					status = http.StatusUnauthorized
					reader = strings.NewReader(strings.Repeat("private-token", 100))
				case "403":
					status = http.StatusForbidden
					reader = strings.NewReader("private-token")
				case "199":
					status = 199
				case "404":
					status = 404
				case "503":
					status = http.StatusServiceUnavailable
					reader = strings.NewReader("private-token")
				case "302":
					status = http.StatusFound
				case "huge_length":
					length = maxObservationMetricsResponseBytes + 1
				case "huge_read":
					reader = &metricsDiagnosticRepeater{remaining: maxObservationMetricsResponseBytes + 2}
				case "body_fail":
					reader = metricsDiagnosticReadFunc(func([]byte) (int, error) { return 0, secret })
				case "body_cancel":
					reader = metricsDiagnosticReadFunc(func([]byte) (int, error) { ctx.stop(context.Canceled); return 0, ctx.Err() })
				case "body_deadline":
					reader = metricsDiagnosticReadFunc(func([]byte) (int, error) { ctx.stop(context.DeadlineExceeded); return 0, ctx.Err() })
				case "body_cancel_independent":
					reader = metricsDiagnosticReadFunc(func([]byte) (int, error) { ctx.stop(context.Canceled); return 0, secret })
				case "status_read_error":
					status = http.StatusServiceUnavailable
					reader = metricsDiagnosticReadFunc(func([]byte) (int, error) { return 0, secret })
				case "parse":
					reader = strings.NewReader("go_goroutines NaN\n")
				case "empty204":
					status = http.StatusNoContent
					reader = strings.NewReader("")
				case "empty":
					reader = strings.NewReader("")
				}
				body := &metricsDiagnosticBody{reader: reader}
				if mode == "valid_close_error" {
					body.closeErr = secret
				}
				bodies = append(bodies, body)
				return &http.Response{StatusCode: status, Header: make(http.Header), Body: body, ContentLength: length, Request: req}, nil
			})}
			addrs := tc.addrs
			if addrs == nil {
				for range max(1, len(tc.modes)) {
					addrs = append(addrs, "http://node.invalid")
				}
			}
			client := NewClient(Config{APIAddrs: addrs, Token: "synthetic-token", HTTPClient: hc})
			snapshot, err := client.Metrics(ctx)
			if tc.success {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if tc.name != "empty_body" && tc.name != "empty_204" && snapshot.GoGoroutines != 7 {
					t.Error("selected snapshot changed")
				}
			} else {
				if err == nil {
					t.Fatal("failure became success")
				}
				if got := err.Error(); got != "observation metrics "+tc.kind {
					t.Errorf("bounded class = %q, want %q", got, tc.kind)
				}
				if tc.causal != nil && !errors.Is(err, tc.causal) {
					t.Error("original causal context lost")
				}
				if tc.causal == nil && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
					t.Error("new causal context exposure")
				}
				if errors.Is(err, secret) {
					t.Error("previously discarded private transport/body cause newly exposed")
				}
			}
			if calls != tc.calls {
				t.Errorf("HTTP calls=%d want=%d", calls, tc.calls)
			}
			for _, body := range bodies {
				if body.closes != 1 {
					t.Error("response body not closed exactly once")
				}
				if body.bytes > int(maxObservationMetricsResponseBytes+1) {
					t.Error("read exceeded existing limit+1")
				}
			}
			if tc.name == "content_length" && bodies[0].reads != 0 {
				t.Error("oversized content-length body was read")
			}
			if tc.name == "unauthorized" && bodies[0].bytes > 512 {
				t.Error("status drain exceeded 512")
			}
			if secret.calls.Load() != 0 {
				t.Error("private error text formatted")
			}
		})
	}
}

func TestMetricsFailureParserBoundary(t *testing.T) {
	var queueExcess strings.Builder
	for i := range 129 {
		queueExcess.WriteString(`wukongim_runtime_pool_queue_depth{pool="p` + strconv.Itoa(i) + `"} 1` + "\n")
	}
	cases := []struct {
		name, body string
		wantError  bool
	}{
		{"sample", "go_goroutines 1 2\n", true}, {"labels", `go_goroutines{x="oops} 1` + "\n", true},
		{"negative", "go_goroutines -1\n", true}, {"infinite", "go_goroutines +Inf\n", true},
		{"metadata_labels", `wukongim_channelv2_meta_created_total{slot_id="1",result="created",extra="private"} 1` + "\n", true},
		{"metadata_slot", `wukongim_channelv2_meta_created_total{slot_id="13",result="created"} 1` + "\n", true},
		{"metadata_counter", `wukongim_channelv2_meta_created_total{slot_id="1",result="created"} 0.5` + "\n", true},
		{"metadata_counter_overflow", `wukongim_channelv2_meta_created_total{slot_id="1",result="created"} 9007199254740993` + "\n", true},
		{"metadata_aggregate_overflow", `wukongim_channelv2_meta_created_total{slot_id="1",result="created"} 9007199254740992` + "\n" + `wukongim_channelv2_meta_created_total{slot_id="2",result="created"} 1` + "\n", true},
		{"metadata_result", `wukongim_channelv2_meta_created_total{slot_id="1",result="private"} 1` + "\n", true},
		{"metadata_duplicate", strings.Repeat(`wukongim_channelv2_meta_created_total{slot_id="1",result="created"} 1`+"\n", 2), true},
		{"metadata_incomplete", `wukongim_channelv2_meta_created_total{slot_id="1",result="created"} 1` + "\n", true},
		{"queue_excess", queueExcess.String(), true},
		{"queue_duplicate", strings.Repeat(`wukongim_runtime_pool_queue_depth{pool="a"} 1`+"\n", 2), true},
		{"queue_unpaired", "wukongim_runtime_pool_queue_depth{pool=\"a\"} 1\nwukongim_runtime_pool_queue_capacity{pool=\"b\"} 2\n", true},
		{"queue_overflow", "wukongim_runtime_pool_queue_depth{pool=\"a\"} 3\nwukongim_runtime_pool_queue_capacity{pool=\"a\"} 2\n", true},
		{"aggregate_overflow", "go_goroutines 1.7976931348623157e308\ngo_goroutines 1.7976931348623157e308\n", true},
		{"line_count", strings.Repeat("#\n", maxObservationMetricLines+1), true},
		{"line_bytes", "#" + strings.Repeat("x", maxObservationMetricLineBytes) + "\n", true},
		{"series_count", strings.Repeat("future_metric 1\n", maxObservationMetricSeries+1), true},
		{"unknown_family_ignored", "private_future_metric{invalid labels\ngo_goroutines 7\n", false},
		{"missing_required_stays_fetch_success", "go_goroutines 7\n", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			direct, directErr := parseObservationMetrics([]byte(tc.body))
			calls := 0
			client := NewClient(Config{APIAddrs: []string{"http://node.invalid"}, HTTPClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(tc.body)), ContentLength: int64(len(tc.body)), Request: req}, nil
			})}})
			got, err := client.Metrics(context.Background())
			if calls != 1 {
				t.Error("parser failure retried HTTP")
			}
			if tc.wantError {
				if directErr == nil || err == nil {
					t.Fatal("parser rejection changed")
				}
				if err.Error() != "observation metrics parse" {
					t.Errorf("parser class=%q", err.Error())
				}
			} else {
				if directErr != nil || err != nil {
					t.Fatal("successful parser changed")
				}
				if got.GoGoroutines != direct.GoGoroutines {
					t.Error("parser snapshot changed")
				}
			}
		})
	}
}

func TestMetricsSharedObservationHelpers(t *testing.T) {
	for _, operation := range []string{"debug", "profile"} {
		t.Run(operation, func(t *testing.T) {
			calls := 0
			client := NewClient(Config{APIAddrs: []string{"http://one.invalid", "http://two.invalid"}, HTTPClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				calls++
				status := http.StatusServiceUnavailable
				body := "private-token"
				if calls == 2 {
					status = http.StatusOK
					if operation == "debug" {
						body = `{"node_id":7}`
					} else {
						body = "profile"
					}
				}
				return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), ContentLength: int64(len(body)), Request: req}, nil
			})}})
			if operation == "debug" {
				got, err := client.DebugConfig(context.Background())
				if err != nil || got.NodeID != 7 {
					t.Error("debug helper changed")
				}
			} else if err := client.ForceGC(context.Background()); err != nil {
				t.Error("profile helper changed")
			}
			if calls != 2 {
				t.Error("other observation fallback changed")
			}
		})
	}
}
