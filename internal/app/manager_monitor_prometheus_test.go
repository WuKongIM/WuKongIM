package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	accessmanager "github.com/WuKongIM/WuKongIM/internal/access/manager"
)

func TestManagerMonitorPrometheusProviderBoundsConcurrentQueriesAndPreservesOrder(t *testing.T) {
	const concurrency = 8
	var active atomic.Int64
	var peak atomic.Int64
	var started atomic.Int64
	release := make(chan struct{})
	var releaseOnce sync.Once
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		current := active.Add(1)
		defer active.Add(-1)
		for {
			observed := peak.Load()
			if current <= observed || peak.CompareAndSwap(observed, current) {
				break
			}
		}
		if started.Add(1) == concurrency {
			releaseOnce.Do(func() { close(release) })
		}
		<-release
		body := io.NopCloser(bytes.NewBufferString(`{"status":"success","data":{"resultType":"matrix","result":[{"metric":{},"values":[[1781767200,"1"],[1781767220,"2"]]}]}}`))
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: body}, nil
	})}
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	timer := time.AfterFunc(5*time.Second, func() { releaseOnce.Do(func() { close(release) }) })
	defer timer.Stop()

	provider := newManagerPrometheusMonitorProvider(managerPrometheusMonitorOptions{
		Enabled: true,
		BaseURL: "http://prometheus.invalid",
		Client:  client,
		Now:     func() time.Time { return time.Unix(1781767240, 0).UTC() },
	})
	response, err := provider.RealtimeMonitor(context.Background(), accessmanager.RealtimeMonitorQuery{
		Window:   15 * time.Minute,
		Step:     20 * time.Second,
		Category: accessmanager.RealtimeMonitorCategoryGateway,
	})
	if err != nil {
		t.Fatalf("RealtimeMonitor() error = %v", err)
	}
	if peak.Load() != concurrency {
		t.Fatalf("peak concurrent Prometheus queries = %d, want %d", peak.Load(), concurrency)
	}
	if len(response.Cards) < 2 || response.Cards[0].Key != "sendRate" || response.Cards[1].Key != "sendSuccessRate" {
		t.Fatalf("card order = %#v, want stable definition order", response.Cards)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

type monitorQueryRecorder struct {
	mu      sync.Mutex
	queries []string
}

func (r *monitorQueryRecorder) add(query string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.queries = append(r.queries, query)
}

func (r *monitorQueryRecorder) values() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.queries...)
}

func (r *monitorQueryRecorder) joined() string {
	return strings.Join(r.values(), "\n")
}

func TestManagerMonitorPrometheusProviderReturnsDisabledWhenNotEnabled(t *testing.T) {
	provider := newManagerPrometheusMonitorProvider(managerPrometheusMonitorOptions{
		Enabled: false,
		Now:     func() time.Time { return time.Unix(1781767200, 0).UTC() },
	})

	resp, err := provider.RealtimeMonitor(context.Background(), accessmanager.RealtimeMonitorQuery{
		Window:   15 * time.Minute,
		Step:     20 * time.Second,
		Category: accessmanager.RealtimeMonitorCategoryGateway,
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

func TestManagerMonitorPrometheusProviderReturnsGoroutineHistory(t *testing.T) {
	var queries monitorQueryRecorder
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query().Get("query")
		queries.add(query)
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[{"metric":{"node_id":"1","node_name":"node-1","module":"gateway"},"values":[[1781767200,"12"],[1781767220,"15"]]}]}}`))
	}))
	defer server.Close()
	provider := newManagerPrometheusMonitorProvider(managerPrometheusMonitorOptions{
		Enabled: true,
		BaseURL: server.URL,
		Client:  server.Client(),
		Now:     func() time.Time { return time.Unix(1781767240, 0).UTC() },
	})

	resp, err := provider.RealtimeMonitor(context.Background(), accessmanager.RealtimeMonitorQuery{
		Window:   15 * time.Minute,
		Step:     20 * time.Second,
		Category: accessmanager.RealtimeMonitorCategoryGoroutines,
	})
	if err != nil {
		t.Fatalf("RealtimeMonitor() error = %v", err)
	}
	wantKeys := []string{
		"goroutineProcessHistory", "goroutineModuleHistory", "goroutineStartRate", "goroutinePanicRate",
		"goroutinePoolBusy", "goroutinePoolQueueDepth", "goroutinePoolRejectionRate",
	}
	requireMonitorCardKeysForTest(t, resp.Cards, wantKeys)
	if resp.Categories[10].Count != len(wantKeys) {
		t.Fatalf("goroutine category = %#v, want count %d", resp.Categories[10], len(wantKeys))
	}
	joined := queries.joined()
	for _, want := range []string{"wukongim_node_goroutines", "sum by (node_id, node_name, module) (wukongim_goroutines_active"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("goroutine queries missing %q: %s", want, joined)
		}
	}
	if strings.Contains(joined, "topk(") {
		t.Fatalf("goroutine queries = %q, want backend Top 8 after all-series summary", joined)
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
		Window:   15 * time.Minute,
		Step:     20 * time.Second,
		Category: accessmanager.RealtimeMonitorCategoryGateway,
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
	if resp.Scope.NodeID != 1 || resp.Scope.View != accessmanager.RealtimeMonitorScopeUnified {
		t.Fatalf("Scope = %#v, want unified node scope", resp.Scope)
	}
	if len(resp.Cards) != len(filterMonitorMetricDefinitions(managerMonitorMetricDefinitions(), accessmanager.RealtimeMonitorCategoryGateway)) {
		t.Fatalf("cards = %d, want gateway cards", len(resp.Cards))
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

func TestManagerMonitorPrometheusProviderReturnsGatewayOperatorCards(t *testing.T) {
	var queries monitorQueryRecorder
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query().Get("query")
		queries.add(query)
		if strings.Contains(query, "sum by (reason) (rate(wukongim_gateway_connection_closes_total") {
			writePrometheusLabeledRangeForTest(w, "reason", "idle", 1, 2)
			return
		}
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
		Window:   15 * time.Minute,
		Step:     20 * time.Second,
		Category: accessmanager.RealtimeMonitorCategoryGateway,
	})

	if err != nil {
		t.Fatalf("RealtimeMonitor() error = %v", err)
	}
	wantKeys := []string{
		"sendRate",
		"sendSuccessRate",
		"entryLatencyP99",
		"activeConnections",
		"sendQueueUsage",
		"connectionOpenRate",
		"connectionCloseRate",
		"connectionCloseReasonRate",
		"authSuccessRate",
		"authLatencyP99",
		"sendackErrorRate",
		"gatewayInboundTraffic",
		"gatewayOutboundTraffic",
		"frameHandleLatencyP99",
		"asyncBatchWaitP99",
		"asyncBatchRecordsP95",
		"asyncBatchBytesP95",
		"authQueueUsage",
		"transportQueueUsage",
		"transportBytesUsage",
		"gatewayDeliveryRate",
		"gatewayTransportWriteLatencyP99",
	}
	requireMonitorCardKeysForTest(t, resp.Cards, wantKeys)
	if resp.Categories[1].Key != accessmanager.RealtimeMonitorCategoryGateway || resp.Categories[1].Count != len(wantKeys) {
		t.Fatalf("gateway category = %#v, want count %d", resp.Categories[1], len(wantKeys))
	}
	reasonCard := requireMonitorCardForTest(t, resp.Cards, "connectionCloseReasonRate")
	requireMonitorCardPointForTest(t, reasonCard, 1781767200000, "idle", 1)
	requireMonitorCardPointForTest(t, reasonCard, 1781767220000, "idle", 2)

	joinedQueries := queries.joined()
	for _, want := range []string{
		`wukongim_gateway_async_send_queue_depth{job="wukongim"}`,
		`wukongim_gateway_async_send_queue_capacity{job="wukongim"}`,
		`wukongim_gateway_connections_total{job="wukongim",event="open"}[1m]`,
		`wukongim_gateway_connections_total{job="wukongim",event="close"}[1m]`,
		`sum by (reason) (rate(wukongim_gateway_connection_closes_total{job="wukongim"}[1m]))`,
		`wukongim_gateway_auth_total{job="wukongim",status="ok"}[1m]`,
		`wukongim_gateway_auth_duration_seconds_bucket{job="wukongim"}[1m]`,
		`wukongim_gateway_sendacks_total{job="wukongim",reason!="success"}[1m]`,
		`wukongim_gateway_messages_received_bytes_total{job="wukongim"}[1m]`,
		`wukongim_gateway_messages_delivered_bytes_total{job="wukongim"}[1m]`,
		`sum by (le, frame_type) (rate(wukongim_gateway_frame_handle_duration_seconds_bucket{job="wukongim"}[1m]))`,
		`wukongim_gateway_async_send_batch_wait_duration_seconds_bucket{job="wukongim"}[1m]`,
		`wukongim_gateway_async_send_batch_records_bucket{job="wukongim"}[1m]`,
		`wukongim_gateway_async_send_batch_bytes_bucket{job="wukongim"}[1m]`,
		`wukongim_runtime_pool_queue_depth{job="wukongim",component="gateway",pool="async_auth",queue="auth"}`,
		`wukongim_runtime_pool_queue_depth{job="wukongim",component="gateway",pool!~"async_send|async_auth"}`,
		`wukongim_runtime_pool_queue_bytes{job="wukongim",component="gateway",pool!~"async_send|async_auth"}`,
		`sum by (protocol) (rate(wukongim_gateway_messages_delivered_total{job="wukongim"}[1m]))`,
		`sum by (le, frame_type) (rate(wukongim_gateway_transport_write_duration_seconds_bucket{job="wukongim",result="ok"}[1m]))`,
	} {
		if !strings.Contains(joinedQueries, want) {
			t.Fatalf("queries missing %q: %s", want, joinedQueries)
		}
	}
}

func TestManagerMonitorPrometheusProviderCapsLabeledSeriesWithoutChangingSummary(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Query().Get("query"), "wukongim_gateway_connection_closes_total") {
			writePrometheusReasonSeriesForTest(t, w, 10)
			return
		}
		writePrometheusRangeForTest(w, "1")
	}))
	defer server.Close()
	provider := newManagerPrometheusMonitorProvider(managerPrometheusMonitorOptions{
		Enabled: true,
		BaseURL: server.URL,
		Client:  server.Client(),
		Now:     func() time.Time { return time.Unix(1781767240, 0).UTC() },
	})

	response, err := provider.RealtimeMonitor(context.Background(), accessmanager.RealtimeMonitorQuery{
		Window:   15 * time.Minute,
		Step:     20 * time.Second,
		Category: accessmanager.RealtimeMonitorCategoryGateway,
	})
	if err != nil {
		t.Fatalf("RealtimeMonitor() error = %v", err)
	}
	card := requireMonitorCardForTest(t, response.Cards, "connectionCloseReasonRate")
	if len(card.Series) != 16 {
		t.Fatalf("connection close series points = %d, want 8 series x 2 points", len(card.Series))
	}
	if card.Value != 55 {
		t.Fatalf("connection close summary = %v, want all-series latest sum 55", card.Value)
	}
	for _, point := range card.Series {
		if point.Label == "reason-1" || point.Label == "reason-2" {
			t.Fatalf("low-value series was not removed: %#v", card.Series)
		}
	}
}

func TestManagerMonitorPrometheusProviderReturnsDeliveryLatencyStages(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Query().Get("query"), "wukongim_delivery_ack_batch_duration_seconds_bucket") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[{"metric":{"stage":"route_resolution"},"values":[[1781767200,"4"],[1781767220,"6"]]},{"metric":{"stage":"ack_batch"},"values":[[1781767200,"8"],[1781767220,"12"]]}]}}`))
			return
		}
		writePrometheusRangeForTest(w, "1")
	}))
	defer server.Close()
	provider := newManagerPrometheusMonitorProvider(managerPrometheusMonitorOptions{
		Enabled: true, BaseURL: server.URL, Client: server.Client(),
		Now: func() time.Time { return time.Unix(1781767240, 0).UTC() },
	})

	resp, err := provider.RealtimeMonitor(context.Background(), accessmanager.RealtimeMonitorQuery{
		Window: 15 * time.Minute, Step: 20 * time.Second, Category: accessmanager.RealtimeMonitorCategoryMessage,
	})
	if err != nil {
		t.Fatalf("RealtimeMonitor() error = %v", err)
	}
	card := requireMonitorCardForTest(t, resp.Cards, "deliveryLatencyP99")
	requireMonitorCardPointForTest(t, card, 1781767220000, "route_resolution", 6)
	requireMonitorCardPointForTest(t, card, 1781767220000, "ack_batch", 12)
	if card.Value != 12 {
		t.Fatalf("deliveryLatencyP99 value = %v, want slowest-stage value 12", card.Value)
	}
}

func TestManagerMonitorPrometheusTransportQueueUsageSkipsUnboundedItemDepth(t *testing.T) {
	query := requireMonitorDefinitionForTest(t, "transportQueueUsage").query("1m")

	if strings.Contains(query, "clamp_min(wukongim_runtime_pool_queue_capacity") {
		t.Fatalf("transport queue query = %q, want capacity=0 series filtered instead of clamped to 1", query)
	}
	for _, want := range []string{
		`wukongim_runtime_pool_queue_depth{component="gateway",pool!~"async_send|async_auth"}`,
		`wukongim_runtime_pool_queue_capacity{component="gateway",pool!~"async_send|async_auth"} > 0`,
	} {
		if !strings.Contains(query, want) {
			t.Fatalf("transport queue query = %q, want %q", query, want)
		}
	}

	bytesQuery := requireMonitorDefinitionForTest(t, "transportBytesUsage").query("1m")
	if strings.Contains(bytesQuery, "clamp_min(wukongim_runtime_pool_queue_bytes_capacity") {
		t.Fatalf("transport bytes query = %q, want bytes_capacity=0 series filtered instead of clamped to 1", bytesQuery)
	}
	for _, want := range []string{
		`wukongim_runtime_pool_queue_bytes{component="gateway",pool!~"async_send|async_auth"}`,
		`wukongim_runtime_pool_queue_bytes_capacity{component="gateway",pool!~"async_send|async_auth"} > 0`,
	} {
		if !strings.Contains(bytesQuery, want) {
			t.Fatalf("transport bytes query = %q, want %q", bytesQuery, want)
		}
	}
}

func TestManagerMonitorPrometheusProviderOmitsTotalStatForPercentCards(t *testing.T) {
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
		Window:   15 * time.Minute,
		Step:     20 * time.Second,
		Category: accessmanager.RealtimeMonitorCategoryGateway,
	})

	if err != nil {
		t.Fatalf("RealtimeMonitor() error = %v", err)
	}
	card := requireMonitorCardForTest(t, resp.Cards, "sendQueueUsage")
	if card.Unit != "%" {
		t.Fatalf("sendQueueUsage unit = %q, want %%", card.Unit)
	}
	var sawAvg, sawPeak bool
	for _, stat := range card.Stats {
		switch stat.Key {
		case "avg":
			sawAvg = true
		case "peak":
			sawPeak = true
		case "total":
			t.Fatalf("percent card stats = %#v, want no total stat", card.Stats)
		}
	}
	if !sawAvg || !sawPeak {
		t.Fatalf("percent card stats = %#v, want avg and peak", card.Stats)
	}
}

func TestManagerMonitorPrometheusProviderReturnsMessageOperatorCards(t *testing.T) {
	var queries monitorQueryRecorder
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query().Get("query")
		queries.add(query)
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
		Window:   15 * time.Minute,
		Step:     20 * time.Second,
		Category: accessmanager.RealtimeMonitorCategoryMessage,
	})

	if err != nil {
		t.Fatalf("RealtimeMonitor() error = %v", err)
	}
	wantKeys := []string{
		"messageSendRate",
		"messageSendackErrorRate",
		"commitRate",
		"messageAppendErrorRate",
		"messageAppendLatencyP95",
		"commitLatencyP99",
		"pendingCommitBacklog",
		"messageDispatchEnqueueRate",
		"messageDispatchOverflowRate",
		"deliveryRate",
		"deliveryLatencyP99",
		"fanOutRatio",
		"deliveryEnqueueRate",
		"deliveryQueueUsage",
		"deliveryRetryRate",
		"deliveryAdmissionErrorRate",
		"deliveryRouteExpireRate",
		"retryQueueDepth",
		"pathErrorRate",
		"messageAppendErrorBreakdown",
		"messageSendBatchStageLatencyP99",
		"messageEventRate",
		"messageEventErrorRate",
		"messageEventStageLatencyP99",
		"messageEventStreamCacheUsage",
		"messageCommittedReplayLag",
		"messageCommittedReplayLatencyP99",
		"deliveryErrorRate",
		"deliveryRecipientWorkerUsage",
		"deliveryRecipientAdmissionWaitP99",
		"deliveryAckFailureRate",
		"presenceEndpointLookupErrorRate",
		"presenceEndpointLookupLatencyP99",
		"presenceMaintenanceErrorRate",
		"presenceMaintenanceLatencyP99",
	}
	requireMonitorCardKeysForTest(t, resp.Cards, wantKeys)
	if resp.Categories[3].Key != accessmanager.RealtimeMonitorCategoryMessage || resp.Categories[3].Count != len(wantKeys) {
		t.Fatalf("message category = %#v, want count %d", resp.Categories[3], len(wantKeys))
	}

	joinedQueries := queries.joined()
	for _, want := range []string{
		`wukongim_gateway_messages_received_total{job="wukongim"}[1m]`,
		`wukongim_gateway_sendacks_total{job="wukongim",reason!="success"}[1m]`,
		`wukongim_message_append_total{job="wukongim",result!="ok"}[1m]`,
		`wukongim_message_append_duration_seconds_bucket{job="wukongim",result="ok"}[1m]`,
		`wukongim_message_committed_dispatch_enqueue_total{job="wukongim",result="ok"}[1m]`,
		`wukongim_message_committed_dispatch_overflow_total{job="wukongim"}[1m]`,
		`wukongim_delivery_event_queue_total{job="wukongim",result="ok"}[1m]`,
		`wukongim_delivery_recipient_worker_queue_depth{job="wukongim"}`,
		`wukongim_delivery_retry_total{job="wukongim",event="enqueue"}[1m]`,
		`wukongim_delivery_recipient_worker_admission_total{job="wukongim",result!="ok"}[1m]`,
		`wukongim_delivery_route_expired_total{job="wukongim"}[1m]`,
		`wukongim_message_append_errors_total{job="wukongim"}[1m]`,
		`wukongim_message_send_batch_stage_item_duration_seconds_bucket{job="wukongim",result="ok"}[1m]`,
		`wukongim_message_event_append_total{job="wukongim"}[1m]`,
		`wukongim_message_event_propose_total{job="wukongim"}[1m]`,
		`wukongim_message_event_stream_cache_sessions{job="wukongim"}`,
		`wukongim_message_committed_replay_lag_messages{job="wukongim"}`,
		`wukongim_delivery_errors_total{job="wukongim"}[1m]`,
		`wukongim_delivery_recipient_worker_inflight{job="wukongim"}`,
		`wukongim_delivery_recipient_worker_admission_wait_seconds_bucket{job="wukongim"}[1m]`,
		`wukongim_delivery_ack_batch_rejected_total{job="wukongim"}[1m]`,
		`wukongim_presence_endpoint_lookup_total{job="wukongim",outcome!="ok"}[1m]`,
		`wukongim_presence_endpoint_lookup_duration_seconds_bucket{job="wukongim",outcome="ok"}[1m]`,
		`wukongim_presence_touch_flush_total{job="wukongim",result!="ok"}[1m]`,
		`wukongim_presence_expiry_duration_seconds_bucket{job="wukongim"}[1m]`,
	} {
		if !strings.Contains(joinedQueries, want) {
			t.Fatalf("queries missing %q: %s", want, joinedQueries)
		}
	}
}

func TestManagerMonitorPrometheusProviderReturnsChannelOperatorCards(t *testing.T) {
	var queries monitorQueryRecorder
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query().Get("query")
		queries.add(query)
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
		Window:   15 * time.Minute,
		Step:     20 * time.Second,
		Category: accessmanager.RealtimeMonitorCategoryChannel,
	})

	if err != nil {
		t.Fatalf("RealtimeMonitor() error = %v", err)
	}
	wantKeys := []string{
		"channelCapacityUsage",
		"channelExecutionQueueDepth",
		"channelExecutionWorkerBusy",
		"channelExecutionEnqueueErrorRate",
		"channelExecutionMailboxWaitP99",
		"channelISRAnomalies",
		"channelWorkerQueueUsage",
		"channelWorkerAdmissionErrorRate",
		"channelPullErrorRate",
		"channelPullLatencyP99",
		"channelPendingMeta",
		"channelMetaCreateQueueDepth",
		"channelMetaCreateErrorRate",
		"channelAppendBatchWaitP99",
		"channelRouterGroupUsage",
		"channelRouterErrorRate",
		"channelRouterLatencyP99",
		"channelPostCommitHandoffUsage",
		"channelPostCommitRetryDepth",
		"channelEffectPoolUsage",
		"channelEffectErrorRate",
		"channelAppendLatencyP99",
		"activeChannels",
		"channelRuntimeLoadRate",
		"channelRuntimeIdleEvictionRate",
		"channelAppendBatchRecordsP95",
		"channelAppendBatchBytesP95",
		"channelAppendErrorRate",
		"channelWriterAdmissionUsage",
		"channelRuntimeFollowersParked",
		"channelActivationRejectRate",
		"channelReactorMailboxDepth",
		"channelWorkerQueueDepth",
		"channelPullHintErrorRate",
		"channelReplicationLatencyP99",
	}
	requireMonitorCardKeysForTest(t, resp.Cards, wantKeys)
	if resp.Categories[5].Key != accessmanager.RealtimeMonitorCategoryChannel || resp.Categories[5].Count != len(wantKeys) {
		t.Fatalf("channel category = %#v, want count %d", resp.Categories[5], len(wantKeys))
	}

	joinedQueries := queries.joined()
	for _, want := range []string{
		`wukongim_channelv2_append_duration_seconds_bucket{job="wukongim"}[1m]`,
		`wukongim_channelv2_active_runtimes{job="wukongim"}`,
		`wukongim_channelv2_runtime_load_total{job="wukongim"}[1m]`,
		`wukongim_channelv2_runtime_eviction_total{job="wukongim",reason="idle"}[1m]`,
		`wukongim_channelv2_append_batch_records_bucket{job="wukongim"}[1m]`,
		`wukongim_channelv2_append_batch_bytes_bucket{job="wukongim"}[1m]`,
		`wukongim_channelv2_append_stage_duration_seconds_count{job="wukongim",result!="ok"}[1m]`,
		`wukongim_channelappend_writer_admission_depth{job="wukongim"}`,
		`wukongim_channelv2_follower_parked{job="wukongim"}`,
		`wukongim_channelv2_activation_rejected_total{job="wukongim"}[1m]`,
		`wukongim_channelv2_reactor_mailbox_depth{job="wukongim"}`,
		`wukongim_channelv2_worker_queue_depth{job="wukongim"}`,
		`wukongim_channelv2_pull_hint_total{job="wukongim",result!="ok"}[1m]`,
		`wukongim_channelv2_replication_stage_duration_seconds_bucket{job="wukongim"}[1m]`,
	} {
		if !strings.Contains(joinedQueries, want) {
			t.Fatalf("queries missing %q: %s", want, joinedQueries)
		}
	}
	for _, want := range []string{
		`wukongim_channel_append_duration_seconds_bucket{job="wukongim"}[1m]`,
		`wukongim_channel_active_runtimes{job="wukongim"}`,
		`wukongim_channel_append_batch_records_bucket{job="wukongim"}[1m]`,
		`wukongim_channel_append_batch_bytes_bucket{job="wukongim"}[1m]`,
		`wukongim_channel_append_stage_duration_seconds_count{job="wukongim",result!="ok"}[1m]`,
		`wukongim_channel_follower_parked{job="wukongim"}`,
		`wukongim_channel_activation_rejected_total{job="wukongim"}[1m]`,
		`wukongim_channel_reactor_mailbox_depth{job="wukongim"}`,
		`wukongim_channel_worker_queue_depth{job="wukongim"}`,
		`wukongim_channel_pull_hint_total{job="wukongim",result!="ok"}[1m]`,
		`wukongim_channel_replication_stage_duration_seconds_bucket{job="wukongim"}[1m]`,
	} {
		if !strings.Contains(joinedQueries, want) {
			t.Fatalf("queries missing promoted channel metric %q: %s", want, joinedQueries)
		}
	}
}

func TestManagerMonitorPrometheusProviderReturnsSlotOperatorCards(t *testing.T) {
	var queries monitorQueryRecorder
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query().Get("query")
		queries.add(query)
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
		Window:   15 * time.Minute,
		Step:     20 * time.Second,
		Category: accessmanager.RealtimeMonitorCategorySlot,
	})

	if err != nil {
		t.Fatalf("RealtimeMonitor() error = %v", err)
	}
	wantKeys := []string{
		"slotPreferredLeaderReconcileRate",
		"slotPreferredLeaderWaitP99",
		"slotReplicaMoveLatencyP99",
		"slotReplicaMoveFailureRate",
		"slotReplicaMovePhaseFailureRate",
		"slotReplicaMovePhaseLatencyP99",
		"slotLeaderStability",
		"slotProposeRate",
		"slotApplyGap",
		"slotLatencyP99",
		"slotProposalAdmissionRejectRate",
		"slotLeaderChangeRate",
		"slotReplicaLagMax",
		"slotSchedulerQueueUsage",
		"slotSchedulerInflightUsage",
		"slotSchedulerTaskLatencyP99",
	}
	requireMonitorCardKeysForTest(t, resp.Cards, wantKeys)
	if resp.Categories[8].Key != accessmanager.RealtimeMonitorCategorySlot || resp.Categories[8].Count != len(wantKeys) {
		t.Fatalf("slot category = %#v, want count %d", resp.Categories[8], len(wantKeys))
	}

	joinedQueries := queries.joined()
	for _, want := range []string{
		`wukongim_slot_proposals_total{job="wukongim"}[1m]`,
		`wukongim_slot_apply_gap{job="wukongim"}`,
		`wukongim_slot_apply_duration_seconds_bucket{job="wukongim"}[1m]`,
		`wukongim_slot_proposal_admission_total{job="wukongim",result!="ok"}[1m]`,
		`wukongim_slot_leader_changes_total{job="wukongim"}[1m]`,
		`wukongim_slot_replica_lag_seconds{job="wukongim"}`,
		`wukongim_runtime_pool_queue_depth{job="wukongim",component="slot",pool="scheduler"}`,
		`wukongim_runtime_pool_inflight{job="wukongim",component="slot",pool="scheduler"}`,
		`wukongim_runtime_pool_task_duration_seconds_bucket{job="wukongim",component="slot",pool="scheduler"}[1m]`,
	} {
		if !strings.Contains(joinedQueries, want) {
			t.Fatalf("queries missing %q: %s", want, joinedQueries)
		}
	}
}

func TestPrometheusFilterNodeIDScopesGoRuntimeSelectors(t *testing.T) {
	promQL := `rate(go_gc_duration_seconds_sum[1m]) + go_memstats_gc_cpu_fraction + (go_memstats_heap_alloc_bytes / clamp_min(go_memstats_next_gc_bytes, 1)) + wukongim_node_goroutines`

	got := prometheusFilterNodeID(promQL, 2)

	for _, want := range []string{
		`go_gc_duration_seconds_sum{job="wukongim",node_id="2"}[1m]`,
		`go_memstats_gc_cpu_fraction{job="wukongim",node_id="2"}`,
		`go_memstats_heap_alloc_bytes{job="wukongim",node_id="2"}`,
		`go_memstats_next_gc_bytes{job="wukongim",node_id="2"}`,
		`wukongim_node_goroutines{job="wukongim",node_id="2"}`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("prometheusFilterNodeID() = %q, want selector %q", got, want)
		}
	}
}

func TestManagerMonitorPrometheusProviderFiltersPromQLByNodeID(t *testing.T) {
	var queries monitorQueryRecorder
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query().Get("query")
		queries.add(query)
		writePrometheusRangeForTest(w, "1")
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
		NodeID: 2,
	})

	if err != nil {
		t.Fatalf("RealtimeMonitor() error = %v", err)
	}
	if resp.Scope.NodeID != 2 {
		t.Fatalf("scope node_id = %d, want 2", resp.Scope.NodeID)
	}
	queryValues := queries.values()
	if len(queryValues) == 0 {
		t.Fatal("Prometheus server was not queried")
	}
	var sawBareMetric, sawExistingSelector bool
	for _, query := range queryValues {
		if strings.Contains(query, `wukongim_gateway_messages_received_total{job="wukongim",node_id="2"}[`) {
			sawBareMetric = true
		}
		if strings.Contains(query, `wukongim_gateway_sendacks_total{job="wukongim",node_id="2",reason="success"}[`) {
			sawExistingSelector = true
		}
		if strings.Contains(query, `wukongim_gateway_messages_received_total[`) ||
			strings.Contains(query, `wukongim_gateway_messages_received_total{node_id="2"}[`) ||
			strings.Contains(query, `wukongim_gateway_sendacks_total{node_id="2",reason="success"}[`) ||
			strings.Contains(query, `wukongim_gateway_sendacks_total{reason="success"}[`) {
			t.Fatalf("query %q was not node-filtered", query)
		}
	}
	if !sawBareMetric || !sawExistingSelector {
		t.Fatalf("queries = %#v, want node_id filter on bare and existing metric selectors", queryValues)
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
		Window:   15 * time.Minute,
		Step:     20 * time.Second,
		Category: accessmanager.RealtimeMonitorCategoryGateway,
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
		Window:   15 * time.Minute,
		Step:     20 * time.Second,
		Category: accessmanager.RealtimeMonitorCategoryMessage,
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
		Window:   15 * time.Minute,
		Step:     20 * time.Second,
		Category: accessmanager.RealtimeMonitorCategoryMessage,
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
		Window:   15 * time.Minute,
		Step:     20 * time.Second,
		Category: accessmanager.RealtimeMonitorCategoryMessage,
	})

	if err != nil {
		t.Fatalf("RealtimeMonitor() error = %v", err)
	}
	card := requireMonitorCardForTest(t, resp.Cards, "pendingCommitBacklog")
	if !card.Available || card.Value != 37 {
		t.Fatalf("pendingCommitBacklog card = %#v, want v2 channelappend backlog value 37", card)
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
		Window: 15 * time.Minute, Step: 20 * time.Second,
		Category: accessmanager.RealtimeMonitorCategoryConversation,
	})
	if err != nil {
		t.Fatalf("RealtimeMonitor() error = %v", err)
	}
	expectedCards := []struct {
		key, unit, tone string
	}{
		{"conversationDirectoryRate", "req/s", accessmanager.RealtimeMonitorToneNormal},
		{"conversationDirectoryLatencyP99", "ms", accessmanager.RealtimeMonitorToneWarning},
		{"conversationDirectoryErrorRate", "%", accessmanager.RealtimeMonitorToneCritical},
		{"conversationScannedCandidates", "items", accessmanager.RealtimeMonitorToneNormal},
		{"conversationReturnedItems", "items", accessmanager.RealtimeMonitorToneNormal},
		{"conversationDeletes", "items", accessmanager.RealtimeMonitorToneNormal},
		{"conversationUnresolved", "items", accessmanager.RealtimeMonitorToneWarning},
		{"conversationHydrationLatencyP99", "ms", accessmanager.RealtimeMonitorToneWarning},
		{"conversationHydrationRemoteBatches", "batches", accessmanager.RealtimeMonitorToneWarning},
		{"conversationHydrationLocalReads", "reads", accessmanager.RealtimeMonitorToneWarning},
	}
	for offset, expected := range expectedCards {
		if got := resp.Cards[offset].Key; got != expected.key {
			t.Fatalf("conversation card at offset %d=%q, want %q", offset, got, expected.key)
		}
		card := requireMonitorCardForTest(t, resp.Cards, expected.key)
		if card.Stage != accessmanager.RealtimeMonitorStageConversationSync ||
			card.Unit != expected.unit || card.Tone != expected.tone || !card.Available || card.Value != 7 {
			t.Fatalf("%s card=%#v", expected.key, card)
		}
	}
	expectedSnapshots := []struct {
		key, metricKey, unit, tone string
	}{
		{"conversationDirectoryP99", "conversationDirectoryLatencyP99", "ms", accessmanager.RealtimeMonitorToneWarning},
		{"conversationDirectoryErrors", "conversationDirectoryErrorRate", "%", accessmanager.RealtimeMonitorToneCritical},
		{"conversationUnresolved", "conversationUnresolved", "items", accessmanager.RealtimeMonitorToneWarning},
		{"conversationHydrationP99", "conversationHydrationLatencyP99", "ms", accessmanager.RealtimeMonitorToneWarning},
	}
	for offset, expected := range expectedSnapshots {
		if got := resp.Snapshot[offset].Key; got != expected.key {
			t.Fatalf("snapshot at offset %d=%q, want %q", offset, got, expected.key)
		}
		snapshot := requireMonitorSnapshotForTest(t, resp, expected.key)
		if snapshot.MetricKey != expected.metricKey || snapshot.Unit != expected.unit || snapshot.Tone != expected.tone || snapshot.Value != 7 {
			t.Fatalf("%s snapshot=%#v", expected.key, snapshot)
		}
	}
}

func TestManagerMonitorPrometheusProviderExposesApprovedImportantMetricCatalog(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writePrometheusRangeForTest(w, "7")
	}))
	defer server.Close()
	provider := newManagerPrometheusMonitorProvider(managerPrometheusMonitorOptions{
		Enabled: true, BaseURL: server.URL, Client: server.Client(),
		Now: func() time.Time { return time.Unix(1781767240, 0).UTC() },
	})

	wantByCategory := map[string][]string{
		accessmanager.RealtimeMonitorCategoryConversation: {
			"conversationHydrationErrorRate", "conversationHydrationBatchItemsP95",
		},
		accessmanager.RealtimeMonitorCategoryChannel: {
			"channelCapacityUsage", "channelExecutionQueueDepth", "channelExecutionWorkerBusy",
			"channelExecutionEnqueueErrorRate", "channelExecutionMailboxWaitP99", "channelISRAnomalies",
			"channelWorkerQueueUsage", "channelWorkerAdmissionErrorRate", "channelPullErrorRate",
			"channelPullLatencyP99", "channelPendingMeta", "channelMetaCreateQueueDepth",
			"channelMetaCreateErrorRate", "channelAppendBatchWaitP99", "channelRouterGroupUsage",
			"channelRouterErrorRate", "channelRouterLatencyP99", "channelPostCommitHandoffUsage",
			"channelPostCommitRetryDepth", "channelEffectPoolUsage", "channelEffectErrorRate",
		},
		accessmanager.RealtimeMonitorCategoryDatabase: {
			"storageMemtableUsage", "storageWALPhysicalSize", "storageSSTSize", "storageWALAmplification",
			"storageFlushThroughput", "storageCompactionReadThroughput", "storageCompactionWriteThroughput",
			"storageBackgroundJobs", "storageCompactionInflightBytes", "channelStoreOwnership",
		},
		accessmanager.RealtimeMonitorCategoryControl: {
			"controllerDecisionRate", "controllerDecisionLatencyP99", "controllerOldestTaskAge",
			"controllerTaskFailureRate", "controllerMigrationsActive", "controllerMigrationFailureRate",
			"controllerRaftMembership", "controllerVoterPromotionRate", "controllerVoterPromotionBlockers",
			"controllerVoterPromotionLatencyP99", "nodeLifecycleState", "nodeHealthFreshness",
			"nodeHealthReportAge", "nodeLifecycleFailureRate", "nodeLifecycleBlockers",
		},
		accessmanager.RealtimeMonitorCategorySlot: {
			"slotPreferredLeaderReconcileRate", "slotPreferredLeaderWaitP99", "slotReplicaMoveLatencyP99",
			"slotReplicaMoveFailureRate", "slotReplicaMovePhaseFailureRate", "slotReplicaMovePhaseLatencyP99",
		},
		accessmanager.RealtimeMonitorCategoryNode: {
			"nodeThreads", "nodeAntsPoolUsage", "nodeAntsPoolWaiting", "runtimePoolWaitP99",
			"runtimePoolTaskP99", "runtimePoolAdmissionErrorRate", "runtimePoolInflightUsage",
			"runtimePoolQueueBytesUsage", "diagnosticsBufferUsage", "diagnosticsDroppedRate",
		},
		accessmanager.RealtimeMonitorCategoryGoroutines: {
			"goroutineStartRate", "goroutinePanicRate", "goroutinePoolBusy", "goroutinePoolQueueDepth",
			"goroutinePoolRejectionRate",
		},
	}

	for category, wantKeys := range wantByCategory {
		resp, err := provider.RealtimeMonitor(context.Background(), accessmanager.RealtimeMonitorQuery{
			Window: 15 * time.Minute, Step: 20 * time.Second, Category: category,
		})
		if err != nil {
			t.Fatalf("RealtimeMonitor(%s) error = %v", category, err)
		}
		for _, key := range wantKeys {
			if card := requireMonitorCardForTest(t, resp.Cards, key); !card.Available {
				t.Fatalf("%s card %q = %#v, want available", category, key, card)
			}
		}
	}
}

func TestManagerMonitorFrontendCatalogCoversBackendDefinitions(t *testing.T) {
	read := func(path string) string {
		t.Helper()
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		return string(contents)
	}
	typesSource := read("../../web/src/pages/cluster-monitor/types.ts")
	configSource := read("../../web/src/pages/cluster-monitor/metric-config.ts")
	englishSource := read("../../web/src/i18n/messages/en.ts")
	chineseSource := read("../../web/src/i18n/messages/zh-CN.ts")

	allKeys := make([]string, 0)
	for _, def := range managerMonitorMetricDefinitions() {
		allKeys = append(allKeys, def.key)
	}
	for _, def := range managerClusterMonitorMetricDefinitions() {
		allKeys = append(allKeys, def.key)
	}
	for _, key := range allKeys {
		if !strings.Contains(typesSource, `| "`+key+`"`) {
			t.Errorf("frontend metric key union missing %q", key)
		}
		if !strings.Contains(configSource, "\n  "+key+":") {
			t.Errorf("frontend metric config missing %q", key)
		}
	}
	for _, def := range managerAdditionalMonitorMetricDefinitions() {
		messageID := `"clusterMonitor.metrics.` + def.key + `"`
		if !strings.Contains(englishSource, messageID) || !strings.Contains(chineseSource, messageID) {
			t.Errorf("frontend translations missing %q", def.key)
		}
	}
	for _, source := range []string{englishSource, chineseSource} {
		if !strings.Contains(source, `"clusterMonitor.help.importantMetric"`) {
			t.Error("frontend translations missing important metric help")
		}
	}
}

func TestManagerMonitorPrometheusCatalogQueriesFormatRateWindow(t *testing.T) {
	for _, def := range managerMonitorMetricDefinitions() {
		if query := def.query("1m"); strings.Contains(query, "%!") {
			t.Errorf("business metric %q has malformed formatted query %q", def.key, query)
		}
	}
	for _, def := range managerClusterMonitorMetricDefinitions() {
		if query := def.query("1m"); strings.Contains(query, "%!") {
			t.Errorf("cluster metric %q has malformed formatted query %q", def.key, query)
		}
	}
}

func TestManagerAdditionalMonitorCatalogKeepsOptionalMetricsUnavailableWhenMissing(t *testing.T) {
	for _, def := range managerAdditionalMonitorMetricDefinitions() {
		if query := def.query("1m"); strings.Contains(query, "vector(0)") {
			t.Errorf("optional metric %q has unconditional zero fallback in %q", def.key, query)
		}
	}
}

func TestManagerAdditionalMonitorCatalogUsesPresenceAwareRPCClientErrorZero(t *testing.T) {
	query := requireMonitorDefinitionForTest(t, "rpcClientErrorRate").query("1m")
	if !strings.Contains(query, "or on(target_node, service)") || strings.Contains(query, "vector(0)") {
		t.Fatalf("rpcClientErrorRate query = %q, want total-traffic presence-aware zero", query)
	}
}

func TestManagerMonitorPrometheusProviderKeepsSlotOwnedMetadataQueriesClusterScoped(t *testing.T) {
	var queries monitorQueryRecorder
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries.add(r.URL.Query().Get("query"))
		writePrometheusNoDataForTest(w)
	}))
	defer server.Close()
	provider := newManagerPrometheusMonitorProvider(managerPrometheusMonitorOptions{
		Enabled: true, BaseURL: server.URL, Client: server.Client(),
		Now: func() time.Time { return time.Unix(1781767240, 0).UTC() },
	})

	resp, err := provider.RealtimeMonitor(context.Background(), accessmanager.RealtimeMonitorQuery{
		Window: 15 * time.Minute, Step: 20 * time.Second,
		Category: accessmanager.RealtimeMonitorCategoryChannel, NodeID: 2,
	})
	if err != nil {
		t.Fatalf("RealtimeMonitor() error = %v", err)
	}
	for _, key := range []string{"channelMetaCreateQueueDepth", "channelMetaCreateErrorRate"} {
		card := requireMonitorCardForTest(t, resp.Cards, key)
		if card.Available || card.UnavailableReason != "cluster_scoped_metric" {
			t.Fatalf("node-view card %s = %#v, want explicit cluster-scoped unavailable", key, card)
		}
	}
	for _, metric := range []string{"wukongim_channelv2_meta_create_queue_depth", "wukongim_channelv2_meta_created_total"} {
		var query string
		for _, candidate := range queries.values() {
			if strings.Contains(candidate, metric) {
				query = candidate
				break
			}
		}
		if query != "" {
			t.Fatalf("node view unexpectedly queried cluster-scoped metric %s: %q", metric, query)
		}
	}
}

func TestManagerMonitorPrometheusProviderConversationHydrationNoData(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Query().Get("query"), "wukongim_conversation_hydration_batch_duration_seconds_bucket") {
			writePrometheusNoDataForTest(w)
			return
		}
		writePrometheusRangeForTest(w, "3")
	}))
	defer server.Close()
	provider := newManagerPrometheusMonitorProvider(managerPrometheusMonitorOptions{
		Enabled: true, BaseURL: server.URL, Client: server.Client(),
		Now: func() time.Time { return time.Unix(1781767240, 0).UTC() },
	})

	resp, err := provider.RealtimeMonitor(context.Background(), accessmanager.RealtimeMonitorQuery{
		Window: 15 * time.Minute, Step: 20 * time.Second,
		Category: accessmanager.RealtimeMonitorCategoryConversation,
	})
	if err != nil {
		t.Fatalf("RealtimeMonitor() error=%v", err)
	}
	if resp.Status != accessmanager.RealtimeMonitorStatusPartial {
		t.Fatalf("status=%q, want partial", resp.Status)
	}
	card := requireMonitorCardForTest(t, resp.Cards, "conversationHydrationLatencyP99")
	if card.Available {
		t.Fatalf("hydration card=%#v, want unavailable", card)
	}
	requireCardUnavailableReasonForTest(t, card, "no_conversation_hydration_samples")
}

func TestManagerMonitorPrometheusConversationDirectoryErrorFallbackIsGrouped(t *testing.T) {
	query := requireMonitorDefinitionForTest(t, "conversationDirectoryErrorRate").query("1m")
	if !strings.Contains(query, `) * 0)) / clamp_min(sum(rate(wukongim_conversation_directory_list_total[1m])), 1)) * 100`) {
		t.Fatalf("directory error query=%q, want zero fallback grouped before division", query)
	}
}

func sparseZeroMonitorQueryForTest(query string) bool {
	return strings.Contains(query, "wukongim_message_committed_dispatch_queue_depth") ||
		strings.Contains(query, "wukongim_delivery_recipient_worker_process_recipients_sum") ||
		strings.Contains(query, "wukongim_delivery_resolve_routes_total") ||
		strings.Contains(query, "wukongim_delivery_retry_queue_depth") ||
		(strings.Contains(query, "wukongim_gateway_sendacks_total") && strings.Contains(query, `reason!="success"`)) ||
		(strings.Contains(query, "wukongim_delivery_push_rpc_total") && strings.Contains(query, `result!="ok"`))
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

func requireMonitorCardKeysForTest(t *testing.T, cards []accessmanager.RealtimeMonitorCard, want []string) {
	t.Helper()
	if len(cards) != len(want) {
		t.Fatalf("cards = %d, want %d; cards=%#v", len(cards), len(want), cards)
	}
	for i, key := range want {
		if cards[i].Key != key {
			t.Fatalf("card[%d].Key = %q, want %q; cards=%#v", i, cards[i].Key, key, cards)
		}
	}
}

func requireMonitorCardPointForTest(t *testing.T, card accessmanager.RealtimeMonitorCard, timestamp int64, label string, want float64) {
	t.Helper()
	for _, point := range card.Series {
		if point.Timestamp == timestamp && point.Label == label {
			if point.Value != want {
				t.Fatalf("point %s/%d = %#v, want %v", label, timestamp, point, want)
			}
			return
		}
	}
	t.Fatalf("card %s missing point label %q timestamp %d: %#v", card.Key, label, timestamp, card.Series)
}

func requireMonitorDefinitionForTest(t *testing.T, key string) monitorMetricDefinition {
	t.Helper()
	for _, def := range managerMonitorMetricDefinitions() {
		if def.key == key {
			return def
		}
	}
	t.Fatalf("monitor definition %q not found", key)
	return monitorMetricDefinition{}
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

func writePrometheusRangeForTest(w http.ResponseWriter, value string) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[{"metric":{},"values":[[1781767200,"` + value + `"],[1781767220,"` + value + `"]]}]}}`))
}

func writePrometheusLabeledRangeForTest(w http.ResponseWriter, labelKey, labelValue string, first, second float64) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(fmt.Sprintf(
		`{"status":"success","data":{"resultType":"matrix","result":[{"metric":{"%s":"%s"},"values":[[1781767200,"%g"],[1781767220,"%g"]]}]}}`,
		labelKey,
		labelValue,
		first,
		second,
	)))
}

func writePrometheusReasonSeriesForTest(t *testing.T, w http.ResponseWriter, count int) {
	writePrometheusLabelSeriesForTest(t, w, "reason", "reason-", count)
}

func writePrometheusLabelSeriesForTest(t *testing.T, w http.ResponseWriter, labelKey, labelPrefix string, count int) {
	t.Helper()
	type matrixResult struct {
		Metric map[string]string `json:"metric"`
		Values [][]any           `json:"values"`
	}
	results := make([]matrixResult, 0, count)
	for index := 1; index <= count; index++ {
		value := fmt.Sprintf("%d", index)
		results = append(results, matrixResult{
			Metric: map[string]string{labelKey: fmt.Sprintf("%s%d", labelPrefix, index)},
			Values: [][]any{{1781767200, value}, {1781767220, value}},
		})
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{
		"status": "success",
		"data":   map[string]any{"resultType": "matrix", "result": results},
	}); err != nil {
		t.Fatalf("encode prometheus response: %v", err)
	}
}

func writePrometheusNodeSeriesForTest(t *testing.T, w http.ResponseWriter, count int) {
	t.Helper()
	type matrixResult struct {
		Metric map[string]string `json:"metric"`
		Values [][]any           `json:"values"`
	}
	results := make([]matrixResult, 0, count)
	for index := 1; index <= count; index++ {
		value := fmt.Sprintf("%d", index)
		results = append(results, matrixResult{
			Metric: map[string]string{"node_id": value, "node_name": "node-" + value},
			Values: [][]any{{1781767200, value}, {1781767220, value}},
		})
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{
		"status": "success",
		"data":   map[string]any{"resultType": "matrix", "result": results},
	}); err != nil {
		t.Fatalf("encode prometheus response: %v", err)
	}
}

func writePrometheusNoDataForTest(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[]}}`))
}
