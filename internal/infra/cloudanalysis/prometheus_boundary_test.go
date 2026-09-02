package cloudanalysis

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	analysis "github.com/WuKongIM/WuKongIM/internal/usecase/cloudanalysis"
)

func TestPrometheusQueryRangeFailsClosedOnBoundedProtocolErrors(t *testing.T) {
	baseURL, err := url.Parse("http://prometheus.test/base")
	if err != nil {
		t.Fatal(err)
	}
	req := analysis.MetricsQueryRangeRequest{
		Start: time.Date(2026, 8, 30, 9, 0, 0, 500_000_000, time.UTC),
		End:   time.Date(2026, 8, 30, 10, 0, 0, 750_000_000, time.UTC),
		Step:  15 * time.Second,
	}
	tests := []struct {
		name      string
		roundTrip func(*http.Request) (*http.Response, error)
		want      string
	}{
		{
			name: "transport failure",
			roundTrip: func(*http.Request) (*http.Response, error) {
				return nil, errors.New("prometheus unavailable")
			},
			want: "prometheus unavailable",
		},
		{
			name: "body read failure",
			roundTrip: func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Body: failingReadCloser{err: errors.New("read failed")}}, nil
			},
			want: "read failed",
		},
		{
			name: "oversized body",
			roundTrip: func(*http.Request) (*http.Response, error) {
				return jsonHTTPResponse(http.StatusOK, strings.Repeat("x", maxPrivateJSONBytes+1)), nil
			},
			want: "response exceeds",
		},
		{
			name: "HTTP failure",
			roundTrip: func(*http.Request) (*http.Response, error) {
				return jsonHTTPResponse(http.StatusServiceUnavailable, "  temporarily unavailable  "), nil
			},
			want: "status 503: temporarily unavailable",
		},
		{
			name: "malformed JSON",
			roundTrip: func(*http.Request) (*http.Response, error) {
				return jsonHTTPResponse(http.StatusOK, `{`), nil
			},
			want: "query_range decode",
		},
		{
			name: "Prometheus failure envelope",
			roundTrip: func(*http.Request) (*http.Response, error) {
				return jsonHTTPResponse(http.StatusOK, `{"status":"error","errorType":"bad_data","error":"invalid range"}`), nil
			},
			want: "failed: bad_data: invalid range",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &prometheusClient{baseURL: baseURL, client: &http.Client{Transport: memoryRoundTripper(test.roundTrip)}}
			_, err := client.queryRange(context.Background(), req, `sum(rate(wk_send_total[1m]))`)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("queryRange() error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestPrometheusQueryRangePreservesResolvedQueryAndWindow(t *testing.T) {
	baseURL, err := url.Parse("http://prometheus.test/base/")
	if err != nil {
		t.Fatal(err)
	}
	req := analysis.MetricsQueryRangeRequest{
		Start: time.Date(2026, 8, 30, 9, 0, 0, 500_000_000, time.UTC),
		End:   time.Date(2026, 8, 30, 10, 0, 0, 750_000_000, time.UTC),
		Step:  2500 * time.Millisecond,
	}
	client := &prometheusClient{baseURL: baseURL, client: &http.Client{Transport: memoryRoundTripper(func(request *http.Request) (*http.Response, error) {
		if got, want := request.URL.Path, "/base/api/v1/query_range"; got != want {
			t.Errorf("path = %q, want %q", got, want)
		}
		query := request.URL.Query()
		if query.Get("query") != `sum(rate(wk_send_total[1m]))` || query.Get("start") != "1788080400.500" ||
			query.Get("end") != "1788084000.750" || query.Get("step") != "2.5" {
			t.Errorf("query = %q", request.URL.RawQuery)
		}
		return jsonHTTPResponse(http.StatusOK, `{"status":"success","data":{"resultType":"matrix","result":[]}}`), nil
	})}}
	result, err := client.queryRange(context.Background(), req, `sum(rate(wk_send_total[1m]))`)
	if err != nil {
		t.Fatalf("queryRange() error = %v", err)
	}
	if result.Node != "cluster" || result.Source != "prometheus" || result.Window == nil ||
		!result.Window.Start.Equal(req.Start) || !result.Window.End.Equal(req.End) {
		t.Fatalf("queryRange() result = %#v", result)
	}
}
