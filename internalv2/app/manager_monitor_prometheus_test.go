package app

import (
	"context"
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
