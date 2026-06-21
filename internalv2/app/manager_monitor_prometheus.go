package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	accessmanager "github.com/WuKongIM/WuKongIM/internalv2/access/manager"
)

const (
	managerMonitorPrometheusQueryTimeout = 5 * time.Second
	managerMonitorPrometheusJobName      = "wukongimv2"
)

var managerMonitorPrometheusMetricSelectorRE = regexp.MustCompile(`\b(wukongim_[a-zA-Z0-9_:]+)(\{[^{}]*\})?`)

type managerPrometheusMonitorOptions struct {
	// Enabled reports whether Prometheus-backed manager monitor queries may run.
	Enabled bool
	// BaseURL is the Prometheus HTTP API base URL.
	BaseURL string
	// NodeID is the local cluster node ID.
	NodeID uint64
	// NodeName is the local cluster node name.
	NodeName string
	// Client is the HTTP client used for Prometheus API calls.
	Client *http.Client
	// Now returns the current time for deterministic tests.
	Now func() time.Time
}

type managerPrometheusMonitorProvider struct {
	options managerPrometheusMonitorOptions
	client  *http.Client
	now     func() time.Time
}

type monitorMetricDefinition struct {
	key               string
	stage             string
	tone              string
	unit              string
	unavailableReason string
	noDataMessage     string
	query             func(rateWindow string) string
}

type prometheusQueryRangeResponse struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
	Data   struct {
		ResultType string                    `json:"resultType"`
		Result     []prometheusMatrixElement `json:"result"`
	} `json:"data"`
}

type prometheusMatrixElement struct {
	Metric map[string]string   `json:"metric"`
	Values [][]json.RawMessage `json:"values"`
}

func newManagerPrometheusMonitorProvider(options managerPrometheusMonitorOptions) *managerPrometheusMonitorProvider {
	client := options.Client
	if client == nil {
		client = &http.Client{Timeout: managerMonitorPrometheusQueryTimeout}
	}
	now := options.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &managerPrometheusMonitorProvider{options: options, client: client, now: now}
}

func (p *managerPrometheusMonitorProvider) RealtimeMonitor(ctx context.Context, query accessmanager.RealtimeMonitorQuery) (accessmanager.RealtimeMonitorResponse, error) {
	now := p.now().UTC()
	if p == nil || !p.options.Enabled || strings.TrimSpace(p.options.BaseURL) == "" {
		return p.monitorDisabledResponse(query, now), nil
	}

	started := time.Now()
	defs := managerMonitorMetricDefinitions()
	cards := make([]accessmanager.RealtimeMonitorCard, 0, len(defs))
	rateWindow := managerMonitorRateWindow(query.Window, query.Step)
	end := now
	start := end.Add(-query.Window)
	var firstErr error
	var available int

	for _, def := range defs {
		promQL := prometheusFilterNodeID(def.query(rateWindow), query.NodeID)
		series, err := p.queryRange(ctx, promQL, start, end, query.Step)
		card := accessmanager.RealtimeMonitorCard{
			Key:       def.key,
			Stage:     def.stage,
			Tone:      def.tone,
			Unit:      def.unit,
			Series:    series,
			Available: len(series) > 0 && err == nil,
		}
		if err != nil {
			card.Error = err.Error()
			card.UnavailableReason = "prometheus_query_error"
			if firstErr == nil {
				firstErr = err
			}
		} else if len(series) == 0 {
			card.Error = monitorNoDataMessage(def.noDataMessage)
			card.UnavailableReason = monitorUnavailableReason(def.unavailableReason, "prometheus_no_data")
			if firstErr == nil {
				firstErr = fmt.Errorf("%s: %s", def.key, card.Error)
			}
		} else {
			available++
			card.Value = series[len(series)-1].Value
			card.Stats = monitorCardStats(series, query.Step)
		}
		cards = append(cards, card)
	}

	status := accessmanager.RealtimeMonitorStatusReady
	sourceErr := ""
	if available == 0 {
		status = accessmanager.RealtimeMonitorStatusPrometheusUnavailable
		if firstErr != nil {
			sourceErr = firstErr.Error()
		} else {
			sourceErr = "prometheus returned no monitor series"
		}
	} else if available < len(defs) {
		status = accessmanager.RealtimeMonitorStatusPartial
		if firstErr != nil {
			sourceErr = firstErr.Error()
		}
	}

	return accessmanager.RealtimeMonitorResponse{
		Status:        status,
		GeneratedAt:   now,
		WindowSeconds: int(query.Window / time.Second),
		StepSeconds:   int(query.Step / time.Second),
		Scope:         p.monitorScope(query.NodeID),
		Sources: accessmanager.RealtimeMonitorSources{
			Prometheus: accessmanager.RealtimeMonitorPrometheusSource{
				Enabled: true,
				BaseURL: strings.TrimRight(strings.TrimSpace(p.options.BaseURL), "/"),
				QueryMS: time.Since(started).Milliseconds(),
				Error:   sourceErr,
			},
		},
		Snapshot: monitorSnapshotFromCards(cards),
		Cards:    cards,
	}, nil
}

func (p *managerPrometheusMonitorProvider) monitorDisabledResponse(query accessmanager.RealtimeMonitorQuery, now time.Time) accessmanager.RealtimeMonitorResponse {
	return accessmanager.RealtimeMonitorResponse{
		Status:        accessmanager.RealtimeMonitorStatusPrometheusDisabled,
		GeneratedAt:   now,
		WindowSeconds: int(query.Window / time.Second),
		StepSeconds:   int(query.Step / time.Second),
		Scope:         p.monitorScope(query.NodeID),
		Sources: accessmanager.RealtimeMonitorSources{
			Prometheus: accessmanager.RealtimeMonitorPrometheusSource{
				Enabled: false,
				BaseURL: strings.TrimRight(strings.TrimSpace(p.options.BaseURL), "/"),
				Error:   "prometheus is disabled; set WK_METRICS_ENABLE=true and WK_PROMETHEUS_ENABLE=true",
			},
		},
		Snapshot: []accessmanager.RealtimeMonitorSnapshotEntry{},
		Cards:    []accessmanager.RealtimeMonitorCard{},
	}
}

func (p *managerPrometheusMonitorProvider) monitorScope(queryNodeID uint64) accessmanager.RealtimeMonitorScope {
	scope := accessmanager.RealtimeMonitorScope{View: accessmanager.RealtimeMonitorScopePrometheus}
	if queryNodeID != 0 {
		scope.NodeID = queryNodeID
		if p != nil && p.options.NodeID == queryNodeID {
			scope.NodeName = strings.TrimSpace(p.options.NodeName)
		}
		return scope
	}
	if p != nil {
		scope.NodeID = p.options.NodeID
		scope.NodeName = strings.TrimSpace(p.options.NodeName)
	}
	return scope
}

func (p *managerPrometheusMonitorProvider) queryRange(ctx context.Context, promQL string, start, end time.Time, step time.Duration) ([]accessmanager.RealtimeMonitorPoint, error) {
	return managerMonitorQueryRange(ctx, p.client, p.options.BaseURL, promQL, start, end, step)
}

func managerMonitorQueryRange(ctx context.Context, client *http.Client, baseURL, promQL string, start, end time.Time, step time.Duration) ([]accessmanager.RealtimeMonitorPoint, error) {
	results, err := managerMonitorQueryRangeResults(ctx, client, baseURL, promQL, start, end, step)
	if err != nil {
		return nil, err
	}
	return parsePrometheusMatrix(results)
}

func managerMonitorQueryRangeResults(ctx context.Context, client *http.Client, baseURL, promQL string, start, end time.Time, step time.Duration) ([]prometheusMatrixElement, error) {
	base, err := url.Parse(strings.TrimRight(strings.TrimSpace(baseURL), "/") + "/api/v1/query_range")
	if err != nil {
		return nil, fmt.Errorf("prometheus base url invalid: %w", err)
	}
	values := base.Query()
	values.Set("query", promQL)
	values.Set("start", strconv.FormatInt(start.Unix(), 10))
	values.Set("end", strconv.FormatInt(end.Unix(), 10))
	values.Set("step", strconv.FormatInt(int64(step/time.Second), 10))
	base.RawQuery = values.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create prometheus query request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("query prometheus: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, fmt.Errorf("read prometheus response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("prometheus query_range returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var decoded prometheusQueryRangeResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil, fmt.Errorf("decode prometheus response: %w", err)
	}
	if decoded.Status != "success" {
		if decoded.Error != "" {
			return nil, fmt.Errorf("prometheus query_range failed: %s", decoded.Error)
		}
		return nil, fmt.Errorf("prometheus query_range failed: %s", decoded.Status)
	}
	return decoded.Data.Result, nil
}

func prometheusFilterNodeID(promQL string, nodeID uint64) string {
	return managerMonitorPrometheusMetricSelectorRE.ReplaceAllStringFunc(promQL, func(selector string) string {
		matches := managerMonitorPrometheusMetricSelectorRE.FindStringSubmatch(selector)
		if len(matches) < 3 {
			return selector
		}
		metric := matches[1]
		labels := matches[2]
		injected := make([]string, 0, 2)
		if !prometheusSelectorHasLabel(labels, "job") {
			injected = append(injected, fmt.Sprintf(`job="%s"`, managerMonitorPrometheusJobName))
		}
		if nodeID != 0 && !prometheusSelectorHasLabel(labels, "node_id") {
			injected = append(injected, fmt.Sprintf(`node_id="%d"`, nodeID))
		}
		if len(injected) == 0 {
			return selector
		}
		prefix := strings.Join(injected, ",")
		if labels == "" {
			return metric + "{" + prefix + "}"
		}
		return metric + "{" + prefix + "," + strings.TrimPrefix(labels, "{")
	})
}

func prometheusSelectorHasLabel(labels string, name string) bool {
	return strings.Contains(labels, name+"=")
}

func managerMonitorMetricDefinitions() []monitorMetricDefinition {
	return []monitorMetricDefinition{
		{
			key:   "sendRate",
			stage: accessmanager.RealtimeMonitorStageSendEntry,
			tone:  accessmanager.RealtimeMonitorToneNormal,
			unit:  "msg/s",
			query: func(rateWindow string) string {
				return prometheusZeroFallback("sum(rate(wukongim_gateway_messages_received_total[" + rateWindow + "]))")
			},
		},
		{
			key:   "sendSuccessRate",
			stage: accessmanager.RealtimeMonitorStageSendEntry,
			tone:  accessmanager.RealtimeMonitorToneNormal,
			unit:  "%",
			query: func(rateWindow string) string {
				success := prometheusZeroFallback("sum(rate(wukongim_gateway_sendacks_total{reason=\"success\"}[" + rateWindow + "]))")
				total := prometheusZeroFallback("sum(rate(wukongim_gateway_sendacks_total[" + rateWindow + "]))")
				return "(" + success + " / clamp_min(" + total + ", 1)) * 100"
			},
		},
		{
			key:               "entryLatencyP99",
			stage:             accessmanager.RealtimeMonitorStageSendEntry,
			tone:              accessmanager.RealtimeMonitorToneWarning,
			unit:              "ms",
			unavailableReason: "no_entry_latency_samples",
			noDataMessage:     "no entry latency samples in selected window",
			query: func(rateWindow string) string {
				return "histogram_quantile(0.99, sum(rate(wukongim_gateway_async_send_dispatch_wait_duration_seconds_bucket[" + rateWindow + "])) by (le)) * 1000"
			},
		},
		{
			key:   "commitRate",
			stage: accessmanager.RealtimeMonitorStageAppendCommit,
			tone:  accessmanager.RealtimeMonitorToneNormal,
			unit:  "msg/s",
			query: func(rateWindow string) string {
				return prometheusZeroFallback("sum(rate(wukongim_message_append_total{result=\"ok\"}[" + rateWindow + "]))")
			},
		},
		{
			key:               "commitLatencyP99",
			stage:             accessmanager.RealtimeMonitorStageAppendCommit,
			tone:              accessmanager.RealtimeMonitorToneNormal,
			unit:              "ms",
			unavailableReason: "no_commit_latency_samples",
			noDataMessage:     "no commit latency samples in selected window",
			query: func(rateWindow string) string {
				return "histogram_quantile(0.99, sum(rate(wukongim_message_append_duration_seconds_bucket{result=\"ok\"}[" + rateWindow + "])) by (le)) * 1000"
			},
		},
		{
			key:   "pendingCommitBacklog",
			stage: accessmanager.RealtimeMonitorStageAppendCommit,
			tone:  accessmanager.RealtimeMonitorToneWarning,
			unit:  "",
			query: func(string) string {
				v2Backlog := "sum(wukongim_channelappend_writer_state_items{kind=~\"pending_append|append_inflight\"})"
				legacyBacklog := "sum(wukongim_message_committed_dispatch_queue_depth)"
				return prometheusFirstAvailable(v2Backlog, legacyBacklog, "vector(0)")
			},
		},
		{
			key:               "conversationSyncRate",
			stage:             accessmanager.RealtimeMonitorStageConversationSync,
			tone:              accessmanager.RealtimeMonitorToneNormal,
			unit:              "req/s",
			unavailableReason: "no_conversation_sync_samples",
			noDataMessage:     "no conversation sync samples in the selected window",
			query: func(rateWindow string) string {
				return "sum(rate(wukongim_conversation_sync_total[" + rateWindow + "]))"
			},
		},
		{
			key:               "conversationSyncLatencyP99",
			stage:             accessmanager.RealtimeMonitorStageConversationSync,
			tone:              accessmanager.RealtimeMonitorToneWarning,
			unit:              "ms",
			unavailableReason: "no_conversation_sync_latency_samples",
			noDataMessage:     "no conversation sync latency samples in the selected window",
			query: func(rateWindow string) string {
				return "histogram_quantile(0.99, sum(rate(wukongim_conversation_sync_duration_seconds_bucket[" + rateWindow + "])) by (le)) * 1000"
			},
		},
		{
			key:   "conversationSyncErrorRate",
			stage: accessmanager.RealtimeMonitorStageConversationSync,
			tone:  accessmanager.RealtimeMonitorToneCritical,
			unit:  "%",
			query: func(rateWindow string) string {
				errors := "sum(rate(wukongim_conversation_sync_total{result!=\"ok\"}[" + rateWindow + "]))"
				total := "sum(rate(wukongim_conversation_sync_total[" + rateWindow + "]))"
				return "(" + prometheusZeroWhenPresent(errors, total) + " / clamp_min(" + total + ", 1)) * 100"
			},
		},
		{
			key:   "conversationReturnedItems",
			stage: accessmanager.RealtimeMonitorStageConversationSync,
			tone:  accessmanager.RealtimeMonitorToneNormal,
			unit:  "items",
			query: func(rateWindow string) string {
				return "sum(rate(wukongim_conversation_sync_returned_items_sum{result=\"ok\"}[" + rateWindow + "])) / clamp_min(sum(rate(wukongim_conversation_sync_returned_items_count{result=\"ok\"}[" + rateWindow + "])), 1)"
			},
		},
		{
			key:               "conversationRecentLoadLatencyP99",
			stage:             accessmanager.RealtimeMonitorStageConversationSync,
			tone:              accessmanager.RealtimeMonitorToneWarning,
			unit:              "ms",
			unavailableReason: "no_conversation_recent_load_samples",
			noDataMessage:     "no conversation recent-load samples in the selected window",
			query: func(rateWindow string) string {
				return "histogram_quantile(0.99, sum(rate(wukongim_conversation_sync_recent_load_duration_seconds_bucket[" + rateWindow + "])) by (le)) * 1000"
			},
		},
		{
			key:   "conversationActiveDirtyRows",
			stage: accessmanager.RealtimeMonitorStageConversationSync,
			tone:  accessmanager.RealtimeMonitorToneWarning,
			unit:  "rows",
			query: func(string) string {
				return "sum(wukongim_conversation_active_cache_dirty_rows)"
			},
		},
		{
			key:   "conversationActiveNormalRows",
			stage: accessmanager.RealtimeMonitorStageConversationSync,
			tone:  accessmanager.RealtimeMonitorToneNormal,
			unit:  "rows",
			query: func(string) string {
				return "sum(wukongim_conversation_active_cache_kind_rows{kind=\"normal\"})"
			},
		},
		{
			key:   "conversationActiveCMDRows",
			stage: accessmanager.RealtimeMonitorStageConversationSync,
			tone:  accessmanager.RealtimeMonitorToneNormal,
			unit:  "rows",
			query: func(string) string {
				return "sum(wukongim_conversation_active_cache_kind_rows{kind=\"cmd\"})"
			},
		},
		{
			key:   "conversationActiveNormalDirtyRows",
			stage: accessmanager.RealtimeMonitorStageConversationSync,
			tone:  accessmanager.RealtimeMonitorToneWarning,
			unit:  "rows",
			query: func(string) string {
				return "sum(wukongim_conversation_active_cache_kind_dirty_rows{kind=\"normal\"})"
			},
		},
		{
			key:   "conversationActiveCMDDirtyRows",
			stage: accessmanager.RealtimeMonitorStageConversationSync,
			tone:  accessmanager.RealtimeMonitorToneWarning,
			unit:  "rows",
			query: func(string) string {
				return "sum(wukongim_conversation_active_cache_kind_dirty_rows{kind=\"cmd\"})"
			},
		},
		{
			key:   "conversationActiveOldestDirtyAge",
			stage: accessmanager.RealtimeMonitorStageConversationSync,
			tone:  accessmanager.RealtimeMonitorToneWarning,
			unit:  "s",
			query: func(string) string {
				return "max(wukongim_conversation_active_cache_oldest_dirty_age_seconds)"
			},
		},
		{
			key:               "conversationActiveFlushLatencyP99",
			stage:             accessmanager.RealtimeMonitorStageConversationSync,
			tone:              accessmanager.RealtimeMonitorToneWarning,
			unit:              "ms",
			unavailableReason: "no_conversation_active_flush_samples",
			noDataMessage:     "no conversation active-flush samples in the selected window",
			query: func(rateWindow string) string {
				return "histogram_quantile(0.99, sum(rate(wukongim_conversation_active_flush_duration_seconds_bucket[" + rateWindow + "])) by (le)) * 1000"
			},
		},
		{
			key:   "conversationActiveFlushErrorRate",
			stage: accessmanager.RealtimeMonitorStageConversationSync,
			tone:  accessmanager.RealtimeMonitorToneCritical,
			unit:  "%",
			query: func(rateWindow string) string {
				return prometheusZeroFallback("(sum(rate(wukongim_conversation_active_flush_total{result!~\"ok|no_dirty\"}[" + rateWindow + "])) / clamp_min(sum(rate(wukongim_conversation_active_flush_total[" + rateWindow + "])), 1)) * 100")
			},
		},
		{
			key:   "conversationAuthorityPressureRate",
			stage: accessmanager.RealtimeMonitorStageConversationSync,
			tone:  accessmanager.RealtimeMonitorToneWarning,
			unit:  "events/s",
			query: func(rateWindow string) string {
				cachePressure := "sum(rate(wukongim_conversation_authority_cache_pressure_total{result!=\"ok\"}[" + rateWindow + "]))"
				admitPressure := "sum(rate(wukongim_conversation_authority_admit_total{result=~\"cache_pressure|route_not_ready|stale_route|not_leader|timeout\"}[" + rateWindow + "]))"
				activity := prometheusAnySeries(
					"sum(rate(wukongim_conversation_authority_cache_pressure_total["+rateWindow+"]))",
					"sum(rate(wukongim_conversation_authority_admit_total["+rateWindow+"]))",
					"sum(rate(wukongim_conversation_authority_list_total["+rateWindow+"]))",
					"sum(rate(wukongim_conversation_authority_handoff_total["+rateWindow+"]))",
				)
				return prometheusZeroWhenPresent(cachePressure, activity) + " + " + prometheusZeroWhenPresent(admitPressure, activity)
			},
		},
		{
			key:   "deliveryRate",
			stage: accessmanager.RealtimeMonitorStageOnlineDelivery,
			tone:  accessmanager.RealtimeMonitorToneNormal,
			unit:  "msg/s",
			query: func(rateWindow string) string {
				recipientWorkerRate := "sum(rate(wukongim_delivery_recipient_worker_process_recipients_sum{result=\"ok\"}[" + rateWindow + "]))"
				pushRPCRate := "sum(rate(wukongim_delivery_push_rpc_routes_total{result=\"ok\"}[" + rateWindow + "]))"
				return prometheusFirstAvailable(recipientWorkerRate, pushRPCRate, "vector(0)")
			},
		},
		{
			key:               "deliveryLatencyP99",
			stage:             accessmanager.RealtimeMonitorStageOnlineDelivery,
			tone:              accessmanager.RealtimeMonitorToneWarning,
			unit:              "ms",
			unavailableReason: "no_delivery_latency_samples",
			noDataMessage:     "no delivery latency samples in selected window",
			query: func(rateWindow string) string {
				recipientWorkerP99 := "histogram_quantile(0.99, sum(rate(wukongim_delivery_recipient_worker_process_duration_seconds_bucket{result=\"ok\"}[" + rateWindow + "])) by (le)) * 1000"
				pushRPCP99 := "histogram_quantile(0.99, sum(rate(wukongim_delivery_push_rpc_duration_seconds_bucket{result=\"ok\"}[" + rateWindow + "])) by (le)) * 1000"
				return prometheusFirstAvailable(recipientWorkerP99, pushRPCP99)
			},
		},
		{
			key:   "fanOutRatio",
			stage: accessmanager.RealtimeMonitorStageOnlineDelivery,
			tone:  accessmanager.RealtimeMonitorToneNormal,
			unit:  "x",
			query: func(rateWindow string) string {
				delivered := prometheusFirstAvailable(
					"sum(rate(wukongim_delivery_recipient_worker_process_recipients_sum{result=\"ok\"}["+rateWindow+"]))",
					"sum(rate(wukongim_delivery_resolve_routes_total{result=\"ok\"}["+rateWindow+"]))",
					"vector(0)",
				)
				sent := prometheusZeroFallback("sum(rate(wukongim_gateway_messages_received_total[" + rateWindow + "]))")
				return delivered + " / clamp_min(" + sent + ", 1)"
			},
		},
		{
			key:   "retryQueueDepth",
			stage: accessmanager.RealtimeMonitorStageOfflineRetry,
			tone:  accessmanager.RealtimeMonitorToneWarning,
			unit:  "",
			query: func(string) string {
				return prometheusZeroFallback("sum(wukongim_delivery_retry_queue_depth)")
			},
		},
		{
			key:   "pathErrorRate",
			stage: accessmanager.RealtimeMonitorStageErrorClosure,
			tone:  accessmanager.RealtimeMonitorToneCritical,
			unit:  "%",
			query: func(rateWindow string) string {
				gatewayErrors := prometheusZeroFallback("sum(rate(wukongim_gateway_sendacks_total{reason!=\"success\"}[" + rateWindow + "]))")
				pushRPCErrors := prometheusZeroFallback("sum(rate(wukongim_delivery_push_rpc_total{result!=\"ok\"}[" + rateWindow + "]))")
				recipientWorkerErrors := prometheusZeroFallback("sum(rate(wukongim_delivery_recipient_worker_process_total{result!=\"ok\"}[" + rateWindow + "]))")
				gatewayTotal := prometheusZeroFallback("sum(rate(wukongim_gateway_sendacks_total[" + rateWindow + "]))")
				pushRPCTotal := prometheusZeroFallback("sum(rate(wukongim_delivery_push_rpc_total[" + rateWindow + "]))")
				recipientWorkerTotal := prometheusZeroFallback("sum(rate(wukongim_delivery_recipient_worker_process_total[" + rateWindow + "]))")
				errors := "(" + gatewayErrors + " + " + pushRPCErrors + " + " + recipientWorkerErrors + ")"
				total := "(" + gatewayTotal + " + " + pushRPCTotal + " + " + recipientWorkerTotal + ")"
				return "(" + errors + " / clamp_min(" + total + ", 1)) * 100"
			},
		},
		{
			key:   "activeConnections",
			stage: accessmanager.RealtimeMonitorStageSendEntry,
			tone:  accessmanager.RealtimeMonitorToneNormal,
			unit:  "",
			query: func(string) string {
				return prometheusZeroFallback("sum(wukongim_gateway_connections_active)")
			},
		},
	}
}

func prometheusZeroFallback(expr string) string {
	return "((" + expr + ") or vector(0))"
}

func prometheusFirstAvailable(exprs ...string) string {
	if len(exprs) == 0 {
		return "vector(0)"
	}
	out := "(" + exprs[0] + ")"
	for _, expr := range exprs[1:] {
		out += " or (" + expr + ")"
	}
	return "(" + out + ")"
}

func managerMonitorRateWindow(window, step time.Duration) string {
	rateWindow := step * 3
	if rateWindow < 30*time.Second {
		rateWindow = 30 * time.Second
	}
	if rateWindow > window {
		rateWindow = window
	}
	return prometheusDuration(rateWindow)
}

func monitorUnavailableReason(configured, fallback string) string {
	if reason := strings.TrimSpace(configured); reason != "" {
		return reason
	}
	return fallback
}

func monitorNoDataMessage(configured string) string {
	if message := strings.TrimSpace(configured); message != "" {
		return message
	}
	return "prometheus returned no data"
}

func prometheusZeroWhenPresent(query, presence string) string {
	return "((" + query + ") or on() ((" + presence + ") * 0))"
}

func prometheusAnySeries(queries ...string) string {
	clean := make([]string, 0, len(queries))
	for _, query := range queries {
		if query = strings.TrimSpace(query); query != "" {
			clean = append(clean, query)
		}
	}
	if len(clean) == 0 {
		return ""
	}
	return "(" + strings.Join(clean, ") or (") + ")"
}

func parsePrometheusMatrix(results []prometheusMatrixElement) ([]accessmanager.RealtimeMonitorPoint, error) {
	byTimestamp := make(map[int64]float64)
	for _, result := range results {
		points, err := parsePrometheusMatrixValues(result.Values)
		if err != nil {
			return nil, err
		}
		for _, point := range points {
			byTimestamp[point.Timestamp] += point.Value
		}
	}
	timestamps := make([]int64, 0, len(byTimestamp))
	for timestamp := range byTimestamp {
		timestamps = append(timestamps, timestamp)
	}
	sort.Slice(timestamps, func(i, j int) bool { return timestamps[i] < timestamps[j] })
	points := make([]accessmanager.RealtimeMonitorPoint, 0, len(timestamps))
	for _, timestamp := range timestamps {
		points = append(points, accessmanager.RealtimeMonitorPoint{
			Timestamp: timestamp,
			Value:     byTimestamp[timestamp],
		})
	}
	return points, nil
}

func parsePrometheusMatrixValues(values [][]json.RawMessage) ([]accessmanager.RealtimeMonitorPoint, error) {
	points := make([]accessmanager.RealtimeMonitorPoint, 0, len(values))
	for _, raw := range values {
		if len(raw) != 2 {
			return nil, fmt.Errorf("prometheus matrix value must contain timestamp and value")
		}
		timestamp, err := parsePrometheusTimestamp(raw[0])
		if err != nil {
			return nil, err
		}
		value, err := parsePrometheusSample(raw[1])
		if err != nil {
			return nil, err
		}
		if math.IsNaN(value) || math.IsInf(value, 0) {
			continue
		}
		points = append(points, accessmanager.RealtimeMonitorPoint{
			Timestamp: timestamp,
			Value:     value,
		})
	}
	return points, nil
}

func parsePrometheusTimestamp(raw json.RawMessage) (int64, error) {
	var seconds float64
	if err := json.Unmarshal(raw, &seconds); err != nil {
		return 0, fmt.Errorf("decode prometheus timestamp: %w", err)
	}
	return int64(seconds * 1000), nil
}

func parsePrometheusSample(raw json.RawMessage) (float64, error) {
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		value, parseErr := strconv.ParseFloat(text, 64)
		if parseErr != nil {
			return 0, fmt.Errorf("decode prometheus sample %q: %w", text, parseErr)
		}
		return value, nil
	}
	var value float64
	if err := json.Unmarshal(raw, &value); err != nil {
		return 0, fmt.Errorf("decode prometheus sample: %w", err)
	}
	return value, nil
}

func monitorCardStats(series []accessmanager.RealtimeMonitorPoint, step time.Duration) []accessmanager.RealtimeMonitorStat {
	if len(series) == 0 {
		return nil
	}
	var sum float64
	peak := series[0].Value
	for _, point := range series {
		sum += point.Value
		if point.Value > peak {
			peak = point.Value
		}
	}
	return []accessmanager.RealtimeMonitorStat{
		{Key: "avg", Value: sum / float64(len(series))},
		{Key: "peak", Value: peak},
		{Key: "total", Value: sum * step.Seconds()},
	}
}

func monitorSnapshotFromCards(cards []accessmanager.RealtimeMonitorCard) []accessmanager.RealtimeMonitorSnapshotEntry {
	byKey := make(map[string]accessmanager.RealtimeMonitorCard, len(cards))
	for _, card := range cards {
		if card.Available {
			byKey[card.Key] = card
		}
	}
	specs := []struct {
		key       string
		metricKey string
		unit      string
		tone      string
	}{
		{key: "send", metricKey: "sendRate", unit: "msg/s", tone: accessmanager.RealtimeMonitorToneNormal},
		{key: "delivery", metricKey: "deliveryRate", unit: "msg/s", tone: accessmanager.RealtimeMonitorToneNormal},
		{key: "entryP99", metricKey: "entryLatencyP99", unit: "ms", tone: accessmanager.RealtimeMonitorToneWarning},
		{key: "conversationSyncP99", metricKey: "conversationSyncLatencyP99", unit: "ms", tone: accessmanager.RealtimeMonitorToneWarning},
		{key: "conversationSyncErrors", metricKey: "conversationSyncErrorRate", unit: "%", tone: accessmanager.RealtimeMonitorToneCritical},
		{key: "conversationDirtyAge", metricKey: "conversationActiveOldestDirtyAge", unit: "s", tone: accessmanager.RealtimeMonitorToneWarning},
		{key: "conversationFlushErrors", metricKey: "conversationActiveFlushErrorRate", unit: "%", tone: accessmanager.RealtimeMonitorToneCritical},
		{key: "deliveryP99", metricKey: "deliveryLatencyP99", unit: "ms", tone: accessmanager.RealtimeMonitorToneWarning},
		{key: "errors", metricKey: "pathErrorRate", unit: "%", tone: accessmanager.RealtimeMonitorToneCritical},
		{key: "retryDepth", metricKey: "retryQueueDepth", tone: accessmanager.RealtimeMonitorToneWarning},
		{key: "online", metricKey: "activeConnections", tone: accessmanager.RealtimeMonitorToneNormal},
	}
	out := make([]accessmanager.RealtimeMonitorSnapshotEntry, 0, len(specs))
	for _, spec := range specs {
		card, ok := byKey[spec.metricKey]
		if !ok {
			continue
		}
		out = append(out, accessmanager.RealtimeMonitorSnapshotEntry{
			Key:       spec.key,
			MetricKey: spec.metricKey,
			Value:     card.Value,
			Unit:      spec.unit,
			Tone:      spec.tone,
		})
	}
	return out
}
