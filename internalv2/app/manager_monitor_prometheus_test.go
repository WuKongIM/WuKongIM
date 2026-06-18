package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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

func TestManagerMonitorPrometheusProviderIncludesConversationCardsAndSnapshots(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writePrometheusRangeForTest(w, "7")
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
	if resp.Status != accessmanager.RealtimeMonitorStatusReady {
		t.Fatalf("Status = %q, want ready; source=%#v", resp.Status, resp.Sources.Prometheus)
	}
	expectedCards := []struct {
		key  string
		unit string
		tone string
	}{
		{key: "conversationSyncRate", unit: "req/s", tone: accessmanager.RealtimeMonitorToneNormal},
		{key: "conversationSyncLatencyP99", unit: "ms", tone: accessmanager.RealtimeMonitorToneWarning},
		{key: "conversationSyncErrorRate", unit: "%", tone: accessmanager.RealtimeMonitorToneCritical},
		{key: "conversationReturnedItems", unit: "items", tone: accessmanager.RealtimeMonitorToneNormal},
		{key: "conversationRecentLoadLatencyP99", unit: "ms", tone: accessmanager.RealtimeMonitorToneWarning},
		{key: "conversationActiveDirtyRows", unit: "rows", tone: accessmanager.RealtimeMonitorToneWarning},
		{key: "conversationActiveOldestDirtyAge", unit: "s", tone: accessmanager.RealtimeMonitorToneWarning},
		{key: "conversationActiveFlushLatencyP99", unit: "ms", tone: accessmanager.RealtimeMonitorToneWarning},
		{key: "conversationActiveFlushErrorRate", unit: "%", tone: accessmanager.RealtimeMonitorToneCritical},
		{key: "conversationAuthorityPressureRate", unit: "events/s", tone: accessmanager.RealtimeMonitorToneWarning},
	}
	pendingIndex := monitorCardIndexForTest(t, resp, "pendingCommitBacklog")
	for offset, expected := range expectedCards {
		got := resp.Cards[pendingIndex+1+offset]
		if got.Key != expected.key {
			t.Fatalf("conversation card at offset %d = %q, want %q", offset, got.Key, expected.key)
		}
		card := requireMonitorCardForTest(t, resp, expected.key)
		if card.Stage != "conversationSync" {
			t.Fatalf("%s stage = %q, want conversationSync", card.Key, card.Stage)
		}
		if card.Unit != expected.unit || card.Tone != expected.tone || !card.Available || card.Value != 7 {
			t.Fatalf("%s card = %#v, want unit=%q tone=%q value=7 available", card.Key, card, expected.unit, expected.tone)
		}
	}
	deliveryIndex := monitorCardIndexForTest(t, resp, "deliveryRate")
	if deliveryIndex != pendingIndex+1+len(expectedCards) {
		t.Fatalf("deliveryRate index = %d, want immediately after conversation cards at %d", deliveryIndex, pendingIndex+1+len(expectedCards))
	}

	expectedSnapshots := []struct {
		key       string
		metricKey string
		unit      string
		tone      string
	}{
		{key: "conversationSyncP99", metricKey: "conversationSyncLatencyP99", unit: "ms", tone: accessmanager.RealtimeMonitorToneWarning},
		{key: "conversationSyncErrors", metricKey: "conversationSyncErrorRate", unit: "%", tone: accessmanager.RealtimeMonitorToneCritical},
		{key: "conversationDirtyAge", metricKey: "conversationActiveOldestDirtyAge", unit: "s", tone: accessmanager.RealtimeMonitorToneWarning},
		{key: "conversationFlushErrors", metricKey: "conversationActiveFlushErrorRate", unit: "%", tone: accessmanager.RealtimeMonitorToneCritical},
	}
	entryP99Index := monitorSnapshotIndexForTest(t, resp, "entryP99")
	for offset, expected := range expectedSnapshots {
		got := resp.Snapshot[entryP99Index+1+offset]
		if got.Key != expected.key {
			t.Fatalf("conversation snapshot at offset %d = %q, want %q", offset, got.Key, expected.key)
		}
		snapshot := requireMonitorSnapshotForTest(t, resp, expected.key)
		if snapshot.MetricKey != expected.metricKey || snapshot.Unit != expected.unit || snapshot.Tone != expected.tone || snapshot.Value != 7 {
			t.Fatalf("%s snapshot = %#v, want metric=%q unit=%q tone=%q value=7", snapshot.Key, snapshot, expected.metricKey, expected.unit, expected.tone)
		}
	}
}

func TestManagerMonitorPrometheusProviderConversationNoDataAndNoDirtyHandling(t *testing.T) {
	var mu sync.Mutex
	var queries []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		promQL := r.URL.Query().Get("query")
		mu.Lock()
		queries = append(queries, promQL)
		mu.Unlock()
		switch {
		case strings.Contains(promQL, "wukongim_conversation_sync_recent_load_duration_seconds_bucket"):
			writePrometheusNoDataForTest(w)
		case strings.Contains(promQL, "wukongim_conversation_active_flush_total") && strings.Contains(promQL, "result!~"):
			writePrometheusRangeForTest(w, "0")
		default:
			writePrometheusRangeForTest(w, "3")
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
	recentLoad := requireMonitorCardForTest(t, resp, "conversationRecentLoadLatencyP99")
	if recentLoad.Available {
		t.Fatalf("recent-load card = %#v, want unavailable when Prometheus returns no series", recentLoad)
	}
	if recentLoad.Error == "" {
		t.Fatalf("recent-load card error is empty, want human readable no-data message")
	}
	requireCardUnavailableReasonForTest(t, recentLoad, "no_conversation_recent_load_samples")

	flushErrors := requireMonitorCardForTest(t, resp, "conversationActiveFlushErrorRate")
	if !flushErrors.Available || flushErrors.Value != 0 {
		t.Fatalf("flush error card = %#v, want available zero value", flushErrors)
	}

	mu.Lock()
	defer mu.Unlock()
	var flushErrorQuery string
	for _, query := range queries {
		if strings.Contains(query, "wukongim_conversation_active_flush_total") && strings.Contains(query, "result!~") {
			flushErrorQuery = query
			break
		}
	}
	if flushErrorQuery == "" {
		t.Fatalf("flush error query was not issued; queries=%#v", queries)
	}
	if !strings.Contains(flushErrorQuery, `result!~"ok|no_dirty"`) {
		t.Fatalf("flush error query = %q, want no_dirty excluded from failures", flushErrorQuery)
	}
	if !strings.Contains(flushErrorQuery, "or vector(0)") {
		t.Fatalf("flush error query = %q, want zero fallback for no-dirty windows", flushErrorQuery)
	}
}

func requireMonitorCardForTest(t *testing.T, resp accessmanager.RealtimeMonitorResponse, key string) accessmanager.RealtimeMonitorCard {
	t.Helper()
	for _, card := range resp.Cards {
		if card.Key == key {
			return card
		}
	}
	t.Fatalf("card %q not found; cards=%#v", key, resp.Cards)
	return accessmanager.RealtimeMonitorCard{}
}

func requireMonitorSnapshotForTest(t *testing.T, resp accessmanager.RealtimeMonitorResponse, key string) accessmanager.RealtimeMonitorSnapshotEntry {
	t.Helper()
	for _, snapshot := range resp.Snapshot {
		if snapshot.Key == key {
			return snapshot
		}
	}
	t.Fatalf("snapshot %q not found; snapshot=%#v", key, resp.Snapshot)
	return accessmanager.RealtimeMonitorSnapshotEntry{}
}

func monitorCardIndexForTest(t *testing.T, resp accessmanager.RealtimeMonitorResponse, key string) int {
	t.Helper()
	for i, card := range resp.Cards {
		if card.Key == key {
			return i
		}
	}
	t.Fatalf("card %q not found; cards=%#v", key, resp.Cards)
	return -1
}

func monitorSnapshotIndexForTest(t *testing.T, resp accessmanager.RealtimeMonitorResponse, key string) int {
	t.Helper()
	for i, snapshot := range resp.Snapshot {
		if snapshot.Key == key {
			return i
		}
	}
	t.Fatalf("snapshot %q not found; snapshot=%#v", key, resp.Snapshot)
	return -1
}

func requireCardUnavailableReasonForTest(t *testing.T, card accessmanager.RealtimeMonitorCard, want string) {
	t.Helper()
	encoded, err := json.Marshal(card)
	if err != nil {
		t.Fatalf("marshal card: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(encoded, &raw); err != nil {
		t.Fatalf("unmarshal card JSON: %v", err)
	}
	if got, _ := raw["unavailable_reason"].(string); got != want {
		t.Fatalf("%s unavailable_reason = %q, want %q; card_json=%s", card.Key, got, want, encoded)
	}
}

func writePrometheusRangeForTest(w http.ResponseWriter, value string) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[{"metric":{},"values":[[1781767200,"` + value + `"],[1781767220,"` + value + `"]]}]}}`))
}

func writePrometheusNoDataForTest(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[]}}`))
}
