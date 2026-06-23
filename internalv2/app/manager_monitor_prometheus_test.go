package app

import (
	"context"
	"encoding/json"
	"fmt"
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
	var queries []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query().Get("query")
		queries = append(queries, query)
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
	}
	requireMonitorCardKeysForTest(t, resp.Cards, wantKeys)
	if resp.Categories[1].Key != accessmanager.RealtimeMonitorCategoryGateway || resp.Categories[1].Count != len(wantKeys) {
		t.Fatalf("gateway category = %#v, want count %d", resp.Categories[1], len(wantKeys))
	}
	reasonCard := requireMonitorCardForTest(t, resp.Cards, "connectionCloseReasonRate")
	requireMonitorCardPointForTest(t, reasonCard, 1781767200000, "idle", 1)
	requireMonitorCardPointForTest(t, reasonCard, 1781767220000, "idle", 2)

	joinedQueries := strings.Join(queries, "\n")
	for _, want := range []string{
		`wukongim_gateway_async_send_queue_depth{job="wukongimv2"}`,
		`wukongim_gateway_async_send_queue_capacity{job="wukongimv2"}`,
		`wukongim_gateway_connections_total{job="wukongimv2",event="open"}[1m]`,
		`wukongim_gateway_connections_total{job="wukongimv2",event="close"}[1m]`,
		`sum by (reason) (rate(wukongim_gateway_connection_closes_total{job="wukongimv2"}[1m]))`,
		`wukongim_gateway_auth_total{job="wukongimv2",status="ok"}[1m]`,
		`wukongim_gateway_auth_duration_seconds_bucket{job="wukongimv2"}[1m]`,
		`wukongim_gateway_sendacks_total{job="wukongimv2",reason!="success"}[1m]`,
		`wukongim_gateway_messages_received_bytes_total{job="wukongimv2"}[1m]`,
		`wukongim_gateway_messages_delivered_bytes_total{job="wukongimv2"}[1m]`,
		`sum by (le, frame_type) (rate(wukongim_gateway_frame_handle_duration_seconds_bucket{job="wukongimv2"}[1m]))`,
		`wukongim_gateway_async_send_batch_wait_duration_seconds_bucket{job="wukongimv2"}[1m]`,
		`wukongim_gateway_async_send_batch_records_bucket{job="wukongimv2"}[1m]`,
		`wukongim_gateway_async_send_batch_bytes_bucket{job="wukongimv2"}[1m]`,
		`wukongim_runtime_pool_queue_depth{job="wukongimv2",component="gateway",pool="async_auth",queue="auth"}`,
		`wukongim_runtime_pool_queue_depth{job="wukongimv2",component="gateway",pool!~"async_send|async_auth"}`,
		`wukongim_runtime_pool_queue_bytes{job="wukongimv2",component="gateway",pool!~"async_send|async_auth"}`,
	} {
		if !strings.Contains(joinedQueries, want) {
			t.Fatalf("queries missing %q: %s", want, joinedQueries)
		}
	}
}

func TestManagerMonitorPrometheusProviderReturnsMessageOperatorCards(t *testing.T) {
	var queries []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query().Get("query")
		queries = append(queries, query)
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
	}
	requireMonitorCardKeysForTest(t, resp.Cards, wantKeys)
	if resp.Categories[3].Key != accessmanager.RealtimeMonitorCategoryMessage || resp.Categories[3].Count != len(wantKeys) {
		t.Fatalf("message category = %#v, want count %d", resp.Categories[3], len(wantKeys))
	}

	joinedQueries := strings.Join(queries, "\n")
	for _, want := range []string{
		`wukongim_gateway_messages_received_total{job="wukongimv2"}[1m]`,
		`wukongim_gateway_sendacks_total{job="wukongimv2",reason!="success"}[1m]`,
		`wukongim_message_append_total{job="wukongimv2",result!="ok"}[1m]`,
		`wukongim_message_append_duration_seconds_bucket{job="wukongimv2",result="ok"}[1m]`,
		`wukongim_message_committed_dispatch_enqueue_total{job="wukongimv2",result="ok"}[1m]`,
		`wukongim_message_committed_dispatch_overflow_total{job="wukongimv2"}[1m]`,
		`wukongim_delivery_event_queue_total{job="wukongimv2",result="ok"}[1m]`,
		`wukongim_delivery_recipient_worker_queue_depth{job="wukongimv2"}`,
		`wukongim_delivery_retry_total{job="wukongimv2",event="enqueue"}[1m]`,
		`wukongim_delivery_recipient_worker_admission_total{job="wukongimv2",result!="ok"}[1m]`,
		`wukongim_delivery_route_expired_total{job="wukongimv2"}[1m]`,
	} {
		if !strings.Contains(joinedQueries, want) {
			t.Fatalf("queries missing %q: %s", want, joinedQueries)
		}
	}
}

func TestManagerMonitorPrometheusProviderReturnsChannelOperatorCards(t *testing.T) {
	var queries []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query().Get("query")
		queries = append(queries, query)
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
		"channelAppendLatencyP99",
		"activeChannels",
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

	joinedQueries := strings.Join(queries, "\n")
	for _, want := range []string{
		`wukongim_channelv2_append_duration_seconds_bucket{job="wukongimv2"}[1m]`,
		`wukongim_channelv2_active_runtimes{job="wukongimv2"}`,
		`wukongim_channelv2_append_batch_records_bucket{job="wukongimv2"}[1m]`,
		`wukongim_channelv2_append_batch_bytes_bucket{job="wukongimv2"}[1m]`,
		`wukongim_channelv2_append_stage_duration_seconds_count{job="wukongimv2",result!="ok"}[1m]`,
		`wukongim_channelappend_writer_admission_depth{job="wukongimv2"}`,
		`wukongim_channelv2_follower_parked{job="wukongimv2"}`,
		`wukongim_channelv2_activation_rejected_total{job="wukongimv2"}[1m]`,
		`wukongim_channelv2_reactor_mailbox_depth{job="wukongimv2"}`,
		`wukongim_channelv2_worker_queue_depth{job="wukongimv2"}`,
		`wukongim_channelv2_pull_hint_total{job="wukongimv2",result!="ok"}[1m]`,
		`wukongim_channelv2_replication_stage_duration_seconds_bucket{job="wukongimv2"}[1m]`,
	} {
		if !strings.Contains(joinedQueries, want) {
			t.Fatalf("queries missing %q: %s", want, joinedQueries)
		}
	}
}

func TestManagerMonitorPrometheusProviderReturnsSlotOperatorCards(t *testing.T) {
	var queries []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query().Get("query")
		queries = append(queries, query)
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
	if resp.Categories[7].Key != accessmanager.RealtimeMonitorCategorySlot || resp.Categories[7].Count != len(wantKeys) {
		t.Fatalf("slot category = %#v, want count %d", resp.Categories[7], len(wantKeys))
	}

	joinedQueries := strings.Join(queries, "\n")
	for _, want := range []string{
		`wukongim_slot_proposals_total{job="wukongimv2"}[1m]`,
		`wukongim_slot_apply_gap{job="wukongimv2"}`,
		`wukongim_slot_apply_duration_seconds_bucket{job="wukongimv2"}[1m]`,
		`wukongim_slot_proposal_admission_total{job="wukongimv2",result!="ok"}[1m]`,
		`wukongim_slot_leader_changes_total{job="wukongimv2"}[1m]`,
		`wukongim_slot_replica_lag_seconds{job="wukongimv2"}`,
		`wukongim_runtime_pool_queue_depth{job="wukongimv2",component="slot",pool="scheduler"}`,
		`wukongim_runtime_pool_inflight{job="wukongimv2",component="slot",pool="scheduler"}`,
		`wukongim_runtime_pool_task_duration_seconds_bucket{job="wukongimv2",component="slot",pool="scheduler"}[1m]`,
	} {
		if !strings.Contains(joinedQueries, want) {
			t.Fatalf("queries missing %q: %s", want, joinedQueries)
		}
	}
}

func TestManagerMonitorPrometheusProviderFiltersPromQLByNodeID(t *testing.T) {
	var queries []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query().Get("query")
		queries = append(queries, query)
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
	if len(queries) == 0 {
		t.Fatal("Prometheus server was not queried")
	}
	var sawBareMetric, sawExistingSelector bool
	for _, query := range queries {
		if strings.Contains(query, `wukongim_gateway_messages_received_total{job="wukongimv2",node_id="2"}[`) {
			sawBareMetric = true
		}
		if strings.Contains(query, `wukongim_gateway_sendacks_total{job="wukongimv2",node_id="2",reason="success"}[`) {
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
		t.Fatalf("queries = %#v, want node_id filter on bare and existing metric selectors", queries)
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
		Window:   15 * time.Minute,
		Step:     20 * time.Second,
		Category: accessmanager.RealtimeMonitorCategoryConversation,
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
		{key: "conversationActiveNormalRows", unit: "rows", tone: accessmanager.RealtimeMonitorToneNormal},
		{key: "conversationActiveCMDRows", unit: "rows", tone: accessmanager.RealtimeMonitorToneNormal},
		{key: "conversationActiveNormalDirtyRows", unit: "rows", tone: accessmanager.RealtimeMonitorToneWarning},
		{key: "conversationActiveCMDDirtyRows", unit: "rows", tone: accessmanager.RealtimeMonitorToneWarning},
		{key: "conversationActiveOldestDirtyAge", unit: "s", tone: accessmanager.RealtimeMonitorToneWarning},
		{key: "conversationActiveFlushLatencyP99", unit: "ms", tone: accessmanager.RealtimeMonitorToneWarning},
		{key: "conversationActiveFlushErrorRate", unit: "%", tone: accessmanager.RealtimeMonitorToneCritical},
		{key: "conversationAuthorityPressureRate", unit: "events/s", tone: accessmanager.RealtimeMonitorToneWarning},
	}
	for offset, expected := range expectedCards {
		got := resp.Cards[offset]
		if got.Key != expected.key {
			t.Fatalf("conversation card at offset %d = %q, want %q", offset, got.Key, expected.key)
		}
		card := requireMonitorCardForTest(t, resp.Cards, expected.key)
		if card.Stage != "conversationSync" {
			t.Fatalf("%s stage = %q, want conversationSync", card.Key, card.Stage)
		}
		if card.Unit != expected.unit || card.Tone != expected.tone || !card.Available || card.Value != 7 {
			t.Fatalf("%s card = %#v, want unit=%q tone=%q value=7 available", card.Key, card, expected.unit, expected.tone)
		}
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
	for offset, expected := range expectedSnapshots {
		got := resp.Snapshot[offset]
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
		Window:   15 * time.Minute,
		Step:     20 * time.Second,
		Category: accessmanager.RealtimeMonitorCategoryConversation,
	})

	if err != nil {
		t.Fatalf("RealtimeMonitor() error = %v", err)
	}
	if resp.Status != accessmanager.RealtimeMonitorStatusPartial {
		t.Fatalf("Status = %q, want partial", resp.Status)
	}
	recentLoad := requireMonitorCardForTest(t, resp.Cards, "conversationRecentLoadLatencyP99")
	if recentLoad.Available {
		t.Fatalf("recent-load card = %#v, want unavailable when Prometheus returns no series", recentLoad)
	}
	if recentLoad.Error == "" {
		t.Fatalf("recent-load card error is empty, want human readable no-data message")
	}
	requireCardUnavailableReasonForTest(t, recentLoad, "no_conversation_recent_load_samples")

	flushErrors := requireMonitorCardForTest(t, resp.Cards, "conversationActiveFlushErrorRate")
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

func TestManagerMonitorPrometheusProviderConversationHealthyZeroRatesStayAvailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		promQL := r.URL.Query().Get("query")
		switch {
		case strings.Contains(promQL, "wukongim_conversation_sync_total") && strings.Contains(promQL, `result!="ok"`):
			if strings.Contains(promQL, "or on()") && strings.Contains(promQL, `wukongim_conversation_sync_total{job="wukongimv2"}[`) {
				writePrometheusRangeForTest(w, "0")
				return
			}
			writePrometheusNoDataForTest(w)
		case strings.Contains(promQL, "wukongim_conversation_authority_cache_pressure_total") ||
			(strings.Contains(promQL, "wukongim_conversation_authority_admit_total") && strings.Contains(promQL, `result=~`)):
			if strings.Contains(promQL, "or on()") && strings.Contains(promQL, `wukongim_conversation_authority_admit_total{job="wukongimv2"}[`) {
				writePrometheusRangeForTest(w, "0")
				return
			}
			writePrometheusNoDataForTest(w)
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
		Window:   15 * time.Minute,
		Step:     20 * time.Second,
		Category: accessmanager.RealtimeMonitorCategoryConversation,
	})

	if err != nil {
		t.Fatalf("RealtimeMonitor() error = %v", err)
	}
	if resp.Status != accessmanager.RealtimeMonitorStatusReady {
		t.Fatalf("Status = %q, want ready; source=%#v", resp.Status, resp.Sources.Prometheus)
	}
	syncErrors := requireMonitorCardForTest(t, resp.Cards, "conversationSyncErrorRate")
	if !syncErrors.Available || syncErrors.Value != 0 {
		t.Fatalf("sync error card = %#v, want available zero value", syncErrors)
	}
	authorityPressure := requireMonitorCardForTest(t, resp.Cards, "conversationAuthorityPressureRate")
	if !authorityPressure.Available || authorityPressure.Value != 0 {
		t.Fatalf("authority pressure card = %#v, want available zero value", authorityPressure)
	}
}

func TestManagerMonitorPrometheusConversationZeroFallbackQueriesAreGrouped(t *testing.T) {
	syncErrors := requireMonitorDefinitionForTest(t, "conversationSyncErrorRate").query("1m")
	if !strings.Contains(syncErrors, `) * 0)) / clamp_min(sum(rate(wukongim_conversation_sync_total[1m])), 1)) * 100`) {
		t.Fatalf("sync error query = %q, want zero fallback grouped before division", syncErrors)
	}

	authorityPressure := requireMonitorDefinitionForTest(t, "conversationAuthorityPressureRate").query("1m")
	if !strings.Contains(authorityPressure, `)) + ((sum(rate(wukongim_conversation_authority_admit_total{result=~"cache_pressure|route_not_ready|stale_route|not_leader|timeout"}[1m]))`) {
		t.Fatalf("authority pressure query = %q, want cache and admit pressure fallbacks grouped before addition", authorityPressure)
	}
}

func TestManagerMonitorPrometheusProviderConversationQueryErrorUsesGenericUnavailableReason(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		promQL := r.URL.Query().Get("query")
		if strings.Contains(promQL, "wukongim_conversation_sync_recent_load_duration_seconds_bucket") {
			http.Error(w, "recent load prometheus failed", http.StatusInternalServerError)
			return
		}
		writePrometheusRangeForTest(w, "3")
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
		Category: accessmanager.RealtimeMonitorCategoryConversation,
	})

	if err != nil {
		t.Fatalf("RealtimeMonitor() error = %v", err)
	}
	if resp.Status != accessmanager.RealtimeMonitorStatusPartial {
		t.Fatalf("Status = %q, want partial", resp.Status)
	}
	recentLoad := requireMonitorCardForTest(t, resp.Cards, "conversationRecentLoadLatencyP99")
	if recentLoad.Available {
		t.Fatalf("recent-load card = %#v, want unavailable when query fails", recentLoad)
	}
	if !strings.Contains(recentLoad.Error, "prometheus query_range returned 500") {
		t.Fatalf("recent-load error = %q, want HTTP 500 query error", recentLoad.Error)
	}
	requireCardUnavailableReasonForTest(t, recentLoad, "prometheus_query_error")
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

func writePrometheusNoDataForTest(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[]}}`))
}
