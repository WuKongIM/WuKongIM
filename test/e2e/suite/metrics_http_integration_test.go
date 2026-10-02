//go:build e2e && integration

package suite

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
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
	"time"

	"github.com/stretchr/testify/require"
)

// These contracts exercise the harness's real HTTP boundary. They do not
// replace process-level product E2E or qualify SEND latency and node CPU.
func TestFetchMetricSamplesHTTPIdentityFreshComplete(t *testing.T) {
	var requests, wrongEncoding atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		generation := requests.Add(1)
		if r.Method != http.MethodGet || r.URL.RequestURI() != "/metrics" || r.Header.Get("Accept-Encoding") != "identity" {
			wrongEncoding.Add(1)
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		writer := io.Writer(w)
		if r.Header.Get("Accept-Encoding") == "gzip" {
			w.Header().Set("Content-Encoding", "gzip")
			gz := gzip.NewWriter(w)
			defer func() { _ = gz.Close() }()
			writer = gz
		}
		_, _ = io.WriteString(writer, metricHTTPFixture(generation))
	}))
	defer server.Close()
	addr := strings.TrimPrefix(server.URL, "http://")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	for generation := int64(1); generation <= 2; generation++ {
		samples, err := FetchMetricSamples(ctx, addr)
		require.NoError(t, err)
		requireCompleteMetricHTTPFixture(t, samples)
		require.Equal(t, float64(generation), SumMetricSamples(samples, "observer_generation", nil))
	}

	type result struct {
		samples []MetricSample
		err     error
	}
	results := make(chan result, 16)
	for i := 0; i < cap(results); i++ {
		go func() {
			samples, err := FetchMetricSamples(ctx, addr)
			results <- result{samples: samples, err: err}
		}()
	}
	generations := map[float64]bool{}
	for i := 0; i < cap(results); i++ {
		result := <-results
		require.NoError(t, result.err)
		requireCompleteMetricHTTPFixture(t, result.samples)
		generation := SumMetricSamples(result.samples, "observer_generation", nil)
		require.False(t, generations[generation], "each fetch must collect a fresh snapshot")
		require.GreaterOrEqual(t, generation, float64(3))
		require.LessOrEqual(t, generation, float64(18))
		generations[generation] = true
	}
	require.Equal(t, int64(18), requests.Load(), "no extra requests or retries")
	require.Zero(t, wrongEncoding.Load(), "full metrics must explicitly request identity")
}

func TestFetchMetricSamplesHTTPRejectsInvalidSnapshots(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{name: "500-valid-looking-sample", status: 500, body: "valid_total 1\n"},
		{name: "503", status: 503, body: "temporarily unavailable\n"},
		{name: "empty", status: 200},
		{name: "comments-only", status: 200, body: "# HELP valid_total fixture\n# TYPE valid_total counter\n"},
		{name: "HTML", status: 200, body: "<html>not metrics</html>\n"},
		{name: "invalid-name", status: 200, body: "<html> 1\n"},
		{name: "empty-name", status: 200, body: "{} 1\n"},
		{name: "mixed-malformed", status: 200, body: "valid_total 1\ninvalid_total not-a-number\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			samples, err := FetchMetricSamples(ctx, strings.TrimPrefix(server.URL, "http://"))
			require.Error(t, err)
			require.Nil(t, samples, "invalid snapshots cannot become partial evidence")
			require.Equal(t, int64(1), requests.Load())
		})
	}
}

func TestFetchMetricSamplesHTTPRejectsRedirect(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path == "/metrics" {
			http.Redirect(w, r, "/redirected", http.StatusFound)
			return
		}
		_, _ = io.WriteString(w, "valid_total 1\n")
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	samples, err := FetchMetricSamples(ctx, strings.TrimPrefix(server.URL, "http://"))
	require.Error(t, err)
	require.Nil(t, samples)
	require.Equal(t, int64(1), requests.Load(), "a redirect must not cause another metrics request")
}

func TestFetchMetricSamplesHTTPPreservesReadFailure(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Length", "100")
		_, _ = io.WriteString(w, "valid_total 1\n")
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	samples, err := FetchMetricSamples(ctx, strings.TrimPrefix(server.URL, "http://"))
	require.ErrorIs(t, err, io.ErrUnexpectedEOF)
	require.Nil(t, samples)
	require.Equal(t, int64(1), requests.Load())
}

func TestFetchMetricSamplesHTTPPreservesCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered := make(chan struct{})
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_, _ = io.WriteString(w, "valid_total 1\n")
		w.(http.Flusher).Flush()
		close(entered)
		<-r.Context().Done()
	}))
	defer func() {
		cancel()
		server.Close()
	}()
	type result struct {
		samples []MetricSample
		err     error
	}
	done := make(chan result, 1)
	go func() {
		samples, err := FetchMetricSamples(ctx, strings.TrimPrefix(server.URL, "http://"))
		done <- result{samples: samples, err: err}
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("metrics handler was not entered")
	}
	cancel()
	select {
	case result := <-done:
		require.True(t, errors.Is(result.err, context.Canceled), "cancellation must remain inspectable: %v", result.err)
		require.Nil(t, result.samples)
	case <-time.After(5 * time.Second):
		t.Fatal("canceled fetch did not return")
	}
	require.Equal(t, int64(1), requests.Load())
	samples, err := FetchMetricSamples(ctx, strings.TrimPrefix(server.URL, "http://"))
	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, samples)
	require.Equal(t, int64(1), requests.Load(), "pre-canceled fetch must not reach the server")
}

func metricHTTPFixture(generation int64) string {
	var body strings.Builder
	body.WriteString("# HELP observer_generation Fresh HTTP snapshot generation.\n# TYPE observer_generation gauge\n")
	fmt.Fprintf(&body, "observer_generation %d\n", generation)
	body.WriteString("fixture_counter_total{node_id=\"1\",node_name=\"node one\"} 7 1720000000000\n")
	body.WriteString("fixture_gauge{node_id=\"1\"} -2\n")
	body.WriteString("fixture_duration_seconds_bucket{result=\"ok\",le=\"0.01\"} 1\nfixture_duration_seconds_bucket{result=\"ok\",le=\"+Inf\"} 2\nfixture_duration_seconds_sum{result=\"ok\"} 0.03\nfixture_duration_seconds_count{result=\"ok\"} 2\n")
	body.WriteString("fixture_summary{quantile=\"0.5\"} 1\nfixture_summary_sum 2\nfixture_summary_count 2\nfixture_nan NaN\nfixture_inf +Inf\n")
	for slot := 1; slot <= 256; slot++ {
		for _, prefix := range []string{"wukongim_channelv2_", "wukongim_channel_"} {
			for _, result := range []string{"created", "already_existing", "error"} {
				fmt.Fprintf(&body, "%smeta_created_total{result=\"%s\",slot_id=\"%d\"} 0\n", prefix, result, slot)
			}
		}
	}
	return body.String()
}

func requireCompleteMetricHTTPFixture(t *testing.T, samples []MetricSample) {
	t.Helper()
	require.Len(t, samples, 256*2*3+12)
	require.Equal(t, float64(7), SumMetricSamples(samples, "fixture_counter_total", map[string]string{"node_id": "1", "node_name": "node one"}))
	require.Equal(t, float64(-2), SumMetricSamples(samples, "fixture_gauge", nil))
	require.Equal(t, float64(1), SumMetricSamples(samples, "fixture_duration_seconds_bucket", map[string]string{"result": "ok", "le": "0.01"}))
	require.Equal(t, float64(2), SumMetricSamples(samples, "fixture_duration_seconds_bucket", map[string]string{"result": "ok", "le": "+Inf"}))
	require.Equal(t, MetricHistogramSnapshot{Count: 2, Sum: 0.03}, HistogramSnapshot(samples, "fixture_duration_seconds", map[string]string{"result": "ok"}))
	require.Equal(t, float64(1), SumMetricSamples(samples, "fixture_summary", map[string]string{"quantile": "0.5"}))
	require.True(t, math.IsNaN(SumMetricSamples(samples, "fixture_nan", nil)))
	require.True(t, math.IsInf(SumMetricSamples(samples, "fixture_inf", nil), 1))
	series := make(map[string]bool, len(samples))
	for _, sample := range samples {
		if strings.HasSuffix(sample.Name, "meta_created_total") {
			series[sample.Name+"|"+sample.Labels["slot_id"]+"|"+sample.Labels["result"]] = true
		}
	}
	for slot := 1; slot <= 256; slot++ {
		for _, prefix := range []string{"wukongim_channelv2_", "wukongim_channel_"} {
			for _, result := range []string{"created", "already_existing", "error"} {
				require.True(t, series[prefix+"meta_created_total|"+strconv.Itoa(slot)+"|"+result])
			}
		}
	}
}

func TestFetchMetricSamplesReceiptSameHTTPObservation(t *testing.T) {
	var requests atomic.Int64
	received := make(chan time.Time, 1)
	body := metricHTTPFixture(1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		received <- time.Now().UTC()
		if r.Header.Get("Accept-Encoding") != "identity" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_, _ = io.WriteString(w, body)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	before := time.Now()
	samples, receipt, err := FetchMetricSamplesWithReceipt(ctx, strings.TrimPrefix(server.URL, "http://"))
	after := time.Now()
	require.NoError(t, err)
	requireCompleteMetricHTTPFixture(t, samples)
	requireMetricReceiptBounds(t, receipt, before, after)
	require.Equal(t, http.StatusOK, receipt.StatusCode)
	require.Equal(t, "identity", receipt.RequestedEncoding)
	require.Equal(t, "identity", receipt.ReceivedEncoding)
	require.NotNil(t, receipt.BodyBytes)
	require.Equal(t, int64(len(body)), *receipt.BodyBytes)
	require.Equal(t, metricHTTPBodySHA(body), receipt.BodySHA256)
	require.False(t, receipt.StartedAt.After(<-received))
	require.Equal(t, int64(1), requests.Load(), "metadata cannot add a second observation")
	encoded, err := json.Marshal(receipt)
	require.NoError(t, err)
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(encoded, &fields))
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	require.ElementsMatch(t, []string{"status_code", "requested_encoding", "received_encoding", "body_bytes", "body_sha256", "started_at", "finished_at", "duration_ns"}, keys)
	require.NotContains(t, string(encoded), "node one", "safe receipt must not retain metric labels or body")
}

func TestFetchMetricSamplesReceiptRetainsFailedObservation(t *testing.T) {
	for _, tc := range []struct {
		name         string
		status       int
		encoding     string
		body         string
		truncated    bool
		bodyObserved bool
	}{
		{name: "non200", status: 503, body: "valid_total 1\n", bodyObserved: true},
		{name: "invalid", status: 200, body: "valid_total 1\ninvalid_total broken\n", bodyObserved: true},
		{name: "empty", status: 200, bodyObserved: true},
		{name: "read-error", status: 200, body: "valid_total 1\n", truncated: true, bodyObserved: true},
		{name: "unsupported-encoding", status: 200, encoding: "gzip", body: "valid_total 1\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				if tc.encoding != "" {
					w.Header().Set("Content-Encoding", tc.encoding)
				}
				if tc.truncated {
					w.Header().Set("Content-Length", strconv.Itoa(len(tc.body)+100))
				}
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			before := time.Now()
			samples, receipt, err := FetchMetricSamplesWithReceipt(ctx, strings.TrimPrefix(server.URL, "http://"))
			after := time.Now()
			require.Error(t, err)
			require.Nil(t, samples)
			requireMetricReceiptBounds(t, receipt, before, after)
			require.Equal(t, tc.status, receipt.StatusCode)
			require.Equal(t, "identity", receipt.RequestedEncoding)
			if tc.encoding == "" {
				require.Equal(t, "identity", receipt.ReceivedEncoding)
			} else {
				require.Equal(t, tc.encoding, receipt.ReceivedEncoding)
			}
			if tc.bodyObserved {
				require.NotNil(t, receipt.BodyBytes)
				require.Equal(t, int64(len(tc.body)), *receipt.BodyBytes)
				require.Equal(t, metricHTTPBodySHA(tc.body), receipt.BodySHA256)
			} else {
				require.Nil(t, receipt.BodyBytes, "encoded bytes are not logical body evidence")
				require.Empty(t, receipt.BodySHA256)
			}
			if tc.truncated {
				require.ErrorIs(t, err, io.ErrUnexpectedEOF)
			}
			require.Equal(t, int64(1), requests.Load())
		})
	}
}

func TestFetchMetricSamplesReceiptCanceledRequestIsUnknown(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = io.WriteString(w, "valid_total 1\n")
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	before := time.Now()
	samples, receipt, err := FetchMetricSamplesWithReceipt(ctx, strings.TrimPrefix(server.URL, "http://"))
	after := time.Now()
	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, samples)
	requireMetricReceiptBounds(t, receipt, before, after)
	require.Equal(t, "identity", receipt.RequestedEncoding)
	require.Zero(t, receipt.StatusCode)
	require.Empty(t, receipt.ReceivedEncoding)
	require.Nil(t, receipt.BodyBytes)
	require.Empty(t, receipt.BodySHA256)
	require.Zero(t, requests.Load())
}

func requireMetricReceiptBounds(t *testing.T, receipt MetricScrapeReceipt, before, after time.Time) {
	t.Helper()
	require.Equal(t, time.UTC, receipt.StartedAt.Location())
	require.Equal(t, time.UTC, receipt.FinishedAt.Location())
	require.False(t, receipt.StartedAt.Before(before))
	require.False(t, receipt.FinishedAt.After(after))
	require.False(t, receipt.FinishedAt.Before(receipt.StartedAt))
	require.Positive(t, receipt.DurationNS)
	require.LessOrEqual(t, receipt.DurationNS, after.Sub(before).Nanoseconds())
}

func metricHTTPBodySHA(body string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(body)))
}
