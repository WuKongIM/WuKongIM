package cluster

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	observe "github.com/WuKongIM/WuKongIM/internal/usecase/opsobserve"
)

func TestOpsPrometheusReaderUsesServerQueryAndDropsInternalAddressLabel(t *testing.T) {
	var query string
	client := newOpsMetricsTestClient(func(request *http.Request) string {
		query = request.URL.Query().Get("query")
		return `{"status":"success","data":{"resultType":"matrix","result":[{"metric":{"instance":"10.0.0.1:5300","node_id":"2","node_name":"node-2"},"values":[[1,"3"]]}]}}`
	})
	originalTransport := http.DefaultTransport
	http.DefaultTransport = client.Transport
	t.Cleanup(func() { http.DefaultTransport = originalTransport })
	reader, err := NewOpsPrometheusReader("http://prometheus.test", nil)
	if err != nil {
		t.Fatalf("NewOpsPrometheusReader() error = %v", err)
	}
	data, err := reader.QueryOpsMetrics(context.Background(), observe.MetricsQueryRangeRequest{
		QueryID: observe.MetricQueryGoGoroutines, NodeID: 2,
		Start: time.Now().Add(-time.Minute), End: time.Now(), StepSeconds: 15,
	})
	if err != nil {
		t.Fatalf("QueryOpsMetrics() error = %v", err)
	}
	got := data
	if !strings.Contains(query, `node_id="2"`) {
		t.Fatalf("query = %q, want fixed node matcher", query)
	}
	if _, found := got.Series[0].Labels["instance"]; found {
		t.Fatalf("internal address label leaked: %#v", got.Series[0].Labels)
	}
	if got.Series[0].Labels["node_id"] != "2" {
		t.Fatalf("labels = %#v", got.Series[0].Labels)
	}
	if !strings.Contains(query, "wukongim_node_goroutines") {
		t.Fatalf("query = %q, want node-labeled WuKongIM resource metric", query)
	}
}

func TestOpsPrometheusReaderBuildsEveryMetricFromServerOwnedQueryID(t *testing.T) {
	t.Parallel()

	queries := make(chan string, len(observe.MetricQueryIDs()))
	client := newOpsMetricsTestClient(func(request *http.Request) string {
		queries <- request.URL.Query().Get("query")
		return `{"status":"success","data":{"resultType":"matrix","result":[]}}`
	})
	reader, err := NewOpsPrometheusReader("http://prometheus.test", client)
	if err != nil {
		t.Fatalf("NewOpsPrometheusReader() error = %v", err)
	}
	windowEnd := time.Unix(200, 0).UTC()
	for _, queryID := range observe.MetricQueryIDs() {
		data, err := reader.QueryOpsMetrics(context.Background(), observe.MetricsQueryRangeRequest{
			QueryID: queryID, NodeID: 7, Start: windowEnd.Add(-time.Minute), End: windowEnd, StepSeconds: 15,
		})
		if err != nil {
			t.Fatalf("QueryOpsMetrics(%q) error = %v", queryID, err)
		}
		if data.QueryID != queryID {
			t.Fatalf("QueryOpsMetrics(%q) returned query id %q", queryID, data.QueryID)
		}
		query := <-queries
		if !strings.Contains(query, `job="wukongim"`) || !strings.Contains(query, `node_id="7"`) {
			t.Fatalf("server query %q = %q, want fixed job and numeric node matcher", queryID, query)
		}
	}

	if _, err := reader.QueryOpsMetrics(context.Background(), observe.MetricsQueryRangeRequest{QueryID: "user_supplied_promql"}); !errors.Is(err, observe.ErrInvalidToolInput) {
		t.Fatalf("unknown query error = %v, want %v", err, observe.ErrInvalidToolInput)
	}
}

type opsMetricsRoundTripFunc func(*http.Request) (*http.Response, error)

func (f opsMetricsRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func newOpsMetricsTestClient(bodyFor func(*http.Request) string) *http.Client {
	return &http.Client{Transport: opsMetricsRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(bodyFor(request))),
			Request:    request,
		}, nil
	})}
}

func TestOpsPrometheusReaderRejectsCredentialedOrMutableOrigins(t *testing.T) {
	t.Parallel()

	for _, rawURL := range []string{
		"ftp://prometheus.example", "http://user:pass@prometheus.example", "http://prometheus.example?query=up", "http://prometheus.example/#fragment",
	} {
		if reader, err := NewOpsPrometheusReader(rawURL, nil); !errors.Is(err, errOpsPrometheusUnavailable) || reader != nil {
			t.Fatalf("NewOpsPrometheusReader(%q) = %#v err=%v, want unavailable", rawURL, reader, err)
		}
	}
	reader, err := NewOpsPrometheusReader("   ", nil)
	if err != nil || reader != nil {
		t.Fatalf("NewOpsPrometheusReader(blank) = %#v err=%v, want optional nil", reader, err)
	}
}
