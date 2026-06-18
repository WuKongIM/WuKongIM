package app

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	accessmanager "github.com/WuKongIM/WuKongIM/internalv2/access/manager"
)

func TestManagerMonitorPrometheusProviderReturnsDisabledWhenNotEnabled(t *testing.T) {
	provider := newManagerPrometheusMonitorProvider(managerPrometheusMonitorOptions{
		Enabled: false,
		Now:     func() time.Time { return time.Unix(1781767200, 0).UTC() },
	})

	resp, err := provider.RealtimeMonitor(context.Background(), accessmanager.RealtimeMonitorQuery{
		Window: 15 * time.Minute,
		Step:   20 * time.Second,
	})

	if err != nil {
		t.Fatalf("RealtimeMonitor() error = %v", err)
	}
	if resp.Status != accessmanager.RealtimeMonitorStatusPrometheusDisabled {
		t.Fatalf("Status = %q, want %q", resp.Status, accessmanager.RealtimeMonitorStatusPrometheusDisabled)
	}
	if resp.Sources.Prometheus.Enabled {
		t.Fatalf("Prometheus.Enabled = true, want false")
	}
	if len(resp.Cards) != 0 || len(resp.Snapshot) != 0 {
		t.Fatalf("disabled response cards/snapshot = %d/%d, want empty", len(resp.Cards), len(resp.Snapshot))
	}
}

func TestManagerMonitorPrometheusProviderMapsQueryRange(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/api/v1/query_range" {
			t.Fatalf("path = %s, want /api/v1/query_range", r.URL.Path)
		}
		if r.URL.Query().Get("step") != "20" {
			t.Fatalf("step = %q, want 20", r.URL.Query().Get("step"))
		}
		if !strings.Contains(r.URL.Query().Get("query"), "wukongim_") {
			t.Fatalf("query = %q, want wukongim metric", r.URL.Query().Get("query"))
		}
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[{"metric":{},"values":[[1781767200,"12.5"],[1781767220,"15"]]}]}}`))
	}))
	defer server.Close()
	provider := newManagerPrometheusMonitorProvider(managerPrometheusMonitorOptions{
		Enabled:  true,
		BaseURL:  server.URL,
		NodeID:   1,
		NodeName: "node-1",
		Client:   server.Client(),
		Now:      func() time.Time { return time.Unix(1781767240, 0).UTC() },
	})

	resp, err := provider.RealtimeMonitor(context.Background(), accessmanager.RealtimeMonitorQuery{
		Window: 15 * time.Minute,
		Step:   20 * time.Second,
	})

	if err != nil {
		t.Fatalf("RealtimeMonitor() error = %v", err)
	}
	if resp.Status != accessmanager.RealtimeMonitorStatusReady {
		t.Fatalf("Status = %q, want ready; source=%#v", resp.Status, resp.Sources.Prometheus)
	}
	if calls.Load() == 0 {
		t.Fatal("Prometheus server was not queried")
	}
	if resp.Scope.NodeID != 1 || resp.Scope.NodeName != "node-1" {
		t.Fatalf("Scope = %#v, want node identity", resp.Scope)
	}
	if len(resp.Cards) != len(managerMonitorMetricDefinitions()) {
		t.Fatalf("cards = %d, want %d", len(resp.Cards), len(managerMonitorMetricDefinitions()))
	}
	card := resp.Cards[0]
	if card.Key != "sendRate" || card.Value != 15 || !card.Available {
		t.Fatalf("first card = %#v, want sendRate latest 15 available", card)
	}
	if len(card.Series) != 2 || card.Series[0].Timestamp != 1781767200000 || card.Series[1].Value != 15 {
		t.Fatalf("series = %#v, want mapped millisecond timestamps and values", card.Series)
	}
	if len(card.Stats) < 2 || card.Stats[0].Key != "avg" || card.Stats[0].Value != 13.75 || card.Stats[1].Key != "peak" || card.Stats[1].Value != 15 {
		t.Fatalf("stats = %#v, want avg and peak", card.Stats)
	}
	if len(resp.Snapshot) == 0 || resp.Snapshot[0].MetricKey != "sendRate" {
		t.Fatalf("snapshot = %#v, want send summary from cards", resp.Snapshot)
	}
	if !resp.GeneratedAt.Equal(time.Unix(1781767240, 0).UTC()) {
		t.Fatalf("GeneratedAt = %s, want fixed now", resp.GeneratedAt)
	}
}

func TestManagerMonitorPrometheusProviderReturnsUnavailableWhenPrometheusFails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer server.Close()
	provider := newManagerPrometheusMonitorProvider(managerPrometheusMonitorOptions{
		Enabled: true,
		BaseURL: server.URL,
		Client:  server.Client(),
		Now:     func() time.Time { return time.Unix(1781767240, 0).UTC() },
	})

	resp, err := provider.RealtimeMonitor(context.Background(), accessmanager.RealtimeMonitorQuery{
		Window: 15 * time.Minute,
		Step:   20 * time.Second,
	})

	if err != nil {
		t.Fatalf("RealtimeMonitor() error = %v", err)
	}
	if resp.Status != accessmanager.RealtimeMonitorStatusPrometheusUnavailable {
		t.Fatalf("Status = %q, want unavailable", resp.Status)
	}
	if resp.Sources.Prometheus.Error == "" {
		t.Fatalf("Prometheus error is empty, want source error")
	}
}

func TestManagerMonitorPrometheusProviderReturnsPartialWhenOneMetricFails(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 2 {
			http.Error(w, "one bad query", http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[{"metric":{},"values":[[1781767200,"12.5"],[1781767220,"15"]]}]}}`))
	}))
	defer server.Close()
	provider := newManagerPrometheusMonitorProvider(managerPrometheusMonitorOptions{
		Enabled: true,
		BaseURL: server.URL,
		Client:  server.Client(),
		Now:     func() time.Time { return time.Unix(1781767240, 0).UTC() },
	})

	resp, err := provider.RealtimeMonitor(context.Background(), accessmanager.RealtimeMonitorQuery{
		Window: 15 * time.Minute,
		Step:   20 * time.Second,
	})

	if err != nil {
		t.Fatalf("RealtimeMonitor() error = %v", err)
	}
	if resp.Status != accessmanager.RealtimeMonitorStatusPartial {
		t.Fatalf("Status = %q, want partial", resp.Status)
	}
	var unavailable int
	for _, card := range resp.Cards {
		if !card.Available {
			unavailable++
		}
	}
	if unavailable != 1 {
		t.Fatalf("unavailable cards = %d, want 1; cards=%#v", unavailable, resp.Cards)
	}
}

func TestManagerMonitorPrometheusProviderZeroFillsSparseBusinessSeries(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query().Get("query")
		switch {
		case strings.Contains(query, "wukongim_delivery_recipient_worker_process_duration_seconds_bucket"),
			strings.Contains(query, "wukongim_delivery_push_rpc_duration_seconds_bucket"):
			writePrometheusMatrixForMonitorTest(t, w)
		case sparseZeroMonitorQueryForTest(query):
			if !strings.Contains(query, "vector(0)") {
				writePrometheusMatrixForMonitorTest(t, w)
				return
			}
			writePrometheusMatrixForMonitorTest(t, w, 0, 0)
		default:
			writePrometheusMatrixForMonitorTest(t, w, 2, 3)
		}
	}))
	defer server.Close()
	provider := newManagerPrometheusMonitorProvider(managerPrometheusMonitorOptions{
		Enabled: true,
		BaseURL: server.URL,
		Client:  server.Client(),
		Now:     func() time.Time { return time.Unix(1781767240, 0).UTC() },
	})

	resp, err := provider.RealtimeMonitor(context.Background(), accessmanager.RealtimeMonitorQuery{
		Window: 15 * time.Minute,
		Step:   20 * time.Second,
	})

	if err != nil {
		t.Fatalf("RealtimeMonitor() error = %v", err)
	}
	if resp.Status != accessmanager.RealtimeMonitorStatusPartial {
		t.Fatalf("Status = %q, want partial", resp.Status)
	}
	for _, key := range []string{"pendingCommitBacklog", "deliveryRate", "fanOutRatio", "retryQueueDepth", "pathErrorRate"} {
		card := requireMonitorCardForTest(t, resp.Cards, key)
		if !card.Available || card.Value != 0 || len(card.Series) == 0 || card.Error != "" {
			t.Fatalf("%s card = %#v, want available zero-filled card", key, card)
		}
	}
	card := requireMonitorCardForTest(t, resp.Cards, "deliveryLatencyP99")
	if card.Available || card.UnavailableReason != "no_delivery_latency_samples" {
		t.Fatalf("deliveryLatencyP99 card = %#v, want unavailable delivery latency reason", card)
	}
}

func TestManagerMonitorPrometheusProviderReadsV2ChannelAppendBacklog(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query().Get("query")
		switch {
		case strings.Contains(query, "wukongim_channelappend_writer_state_items"):
			writePrometheusMatrixForMonitorTest(t, w, 24, 37)
		case strings.Contains(query, "wukongim_message_committed_dispatch_queue_depth"):
			writePrometheusMatrixForMonitorTest(t, w, 0, 0)
		default:
			writePrometheusMatrixForMonitorTest(t, w, 1, 1)
		}
	}))
	defer server.Close()
	provider := newManagerPrometheusMonitorProvider(managerPrometheusMonitorOptions{
		Enabled: true,
		BaseURL: server.URL,
		Client:  server.Client(),
		Now:     func() time.Time { return time.Unix(1781767240, 0).UTC() },
	})

	resp, err := provider.RealtimeMonitor(context.Background(), accessmanager.RealtimeMonitorQuery{
		Window: 15 * time.Minute,
		Step:   20 * time.Second,
	})

	if err != nil {
		t.Fatalf("RealtimeMonitor() error = %v", err)
	}
	card := requireMonitorCardForTest(t, resp.Cards, "pendingCommitBacklog")
	if !card.Available || card.Value != 37 {
		t.Fatalf("pendingCommitBacklog card = %#v, want v2 channelappend backlog value 37", card)
	}
}

func sparseZeroMonitorQueryForTest(query string) bool {
	return strings.Contains(query, "wukongim_message_committed_dispatch_queue_depth") ||
		strings.Contains(query, "wukongim_delivery_recipient_worker_process_recipients_sum") ||
		strings.Contains(query, "wukongim_delivery_resolve_routes_total") ||
		strings.Contains(query, "wukongim_delivery_retry_queue_depth") ||
		strings.Contains(query, "wukongim_gateway_sendacks_total{reason!=\"success\"}") ||
		strings.Contains(query, "wukongim_delivery_push_rpc_total{result!=\"ok\"}")
}

func requireMonitorCardForTest(t *testing.T, cards []accessmanager.RealtimeMonitorCard, key string) accessmanager.RealtimeMonitorCard {
	t.Helper()
	for _, card := range cards {
		if card.Key == key {
			return card
		}
	}
	t.Fatalf("card %q not found in %#v", key, cards)
	return accessmanager.RealtimeMonitorCard{}
}

func writePrometheusMatrixForMonitorTest(t *testing.T, w http.ResponseWriter, values ...float64) {
	t.Helper()
	if len(values) == 0 {
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[]}}`))
		return
	}
	if len(values) != 2 {
		t.Fatalf("writePrometheusMatrixForMonitorTest values = %d, want 0 or 2", len(values))
	}
	_, _ = w.Write([]byte(fmt.Sprintf(
		`{"status":"success","data":{"resultType":"matrix","result":[{"metric":{},"values":[[1781767200,"%g"],[1781767220,"%g"]]}]}}`,
		values[0],
		values[1],
	)))
}
