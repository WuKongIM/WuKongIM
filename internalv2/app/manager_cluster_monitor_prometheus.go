package app

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	accessmanager "github.com/WuKongIM/WuKongIM/internalv2/access/manager"
	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
)

type managerClusterControlReader interface {
	// ListNodes returns the bounded manager node inventory snapshot.
	ListNodes(context.Context) (managementusecase.NodeList, error)
	// ListSlots returns the bounded manager slot inventory snapshot.
	ListSlots(context.Context, managementusecase.ListSlotsOptions) ([]managementusecase.Slot, error)
}

type managerClusterPrometheusMonitorOptions struct {
	// Enabled reports whether Prometheus-backed cluster monitor queries may run.
	Enabled bool
	// BaseURL is the Prometheus HTTP API base URL.
	BaseURL string
	// Client is the HTTP client used for Prometheus API calls.
	Client *http.Client
	// Now returns the current time for deterministic tests.
	Now func() time.Time
	// Control reads bounded manager control snapshots for current cluster health values.
	Control managerClusterControlReader
}

type managerClusterPrometheusMonitorProvider struct {
	options managerClusterPrometheusMonitorOptions
	client  *http.Client
	now     func() time.Time
}

type clusterMonitorMetricDefinition struct {
	key       string
	stage     string
	tone      string
	unit      string
	query     func(rateWindow string) string
	transform func(float64) float64
	nodeStats bool
}

type managerClusterControlSnapshot struct {
	nodesAlive    int
	totalSlots    int
	slotsReady    int
	leaderMissing int
	quorumLost    int
	ok            bool
}

func newManagerClusterPrometheusMonitorProvider(options managerClusterPrometheusMonitorOptions) *managerClusterPrometheusMonitorProvider {
	client := options.Client
	if client == nil {
		client = &http.Client{Timeout: managerMonitorPrometheusQueryTimeout}
	}
	now := options.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &managerClusterPrometheusMonitorProvider{options: options, client: client, now: now}
}

func (p *managerClusterPrometheusMonitorProvider) ClusterRealtimeMonitor(ctx context.Context, query accessmanager.ClusterRealtimeMonitorQuery) (accessmanager.ClusterRealtimeMonitorResponse, error) {
	now := time.Now().UTC()
	if p != nil && p.now != nil {
		now = p.now().UTC()
	}
	if p == nil || !p.options.Enabled || strings.TrimSpace(p.options.BaseURL) == "" {
		baseURL := ""
		if p != nil {
			baseURL = p.options.BaseURL
		}
		return managerClusterMonitorDisabledResponse(query, now, baseURL), nil
	}

	started := time.Now()
	defs := managerClusterMonitorMetricDefinitions()
	rateWindow := managerMonitorRateWindow(query.Window, query.Step)
	end := now
	start := end.Add(-query.Window)
	cards, available, firstErr := p.clusterCards(ctx, defs, rateWindow, start, end, query.Step, query.NodeID)
	controlSnapshot, controlSource := p.controlSnapshot(ctx, query.NodeID)
	status, sourceErr := clusterMonitorStatus(len(defs), available, firstErr, controlSource)

	return accessmanager.ClusterRealtimeMonitorResponse{
		Status:        status,
		GeneratedAt:   now,
		WindowSeconds: int(query.Window / time.Second),
		StepSeconds:   int(query.Step / time.Second),
		Scope:         accessmanager.ClusterRealtimeMonitorScope{View: accessmanager.ClusterRealtimeMonitorScopeCluster, NodeID: query.NodeID},
		Sources: accessmanager.ClusterRealtimeMonitorSources{
			Prometheus: accessmanager.RealtimeMonitorPrometheusSource{
				Enabled: true,
				BaseURL: strings.TrimRight(strings.TrimSpace(p.options.BaseURL), "/"),
				QueryMS: time.Since(started).Milliseconds(),
				Error:   sourceErr,
			},
			ControlSnapshot: controlSource,
		},
		Snapshot: clusterMonitorSnapshot(cards, controlSnapshot),
		Cards:    cards,
	}, nil
}

func managerClusterMonitorDisabledResponse(query accessmanager.ClusterRealtimeMonitorQuery, now time.Time, baseURL string) accessmanager.ClusterRealtimeMonitorResponse {
	return accessmanager.ClusterRealtimeMonitorResponse{
		Status:        accessmanager.ClusterRealtimeMonitorStatusPrometheusDisabled,
		GeneratedAt:   now,
		WindowSeconds: int(query.Window / time.Second),
		StepSeconds:   int(query.Step / time.Second),
		Scope:         accessmanager.ClusterRealtimeMonitorScope{View: accessmanager.ClusterRealtimeMonitorScopeCluster, NodeID: query.NodeID},
		Sources: accessmanager.ClusterRealtimeMonitorSources{
			Prometheus: accessmanager.RealtimeMonitorPrometheusSource{
				Enabled: false,
				BaseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
				Error:   "prometheus is disabled; set WK_METRICS_ENABLE=true and WK_PROMETHEUS_ENABLE=true",
			},
			ControlSnapshot: accessmanager.ClusterRealtimeMonitorSource{Enabled: false},
		},
		Snapshot: []accessmanager.ClusterRealtimeMonitorSnapshotEntry{},
		Cards:    []accessmanager.ClusterRealtimeMonitorCard{},
	}
}

func (p *managerClusterPrometheusMonitorProvider) clusterCards(ctx context.Context, defs []clusterMonitorMetricDefinition, rateWindow string, start, end time.Time, step time.Duration, nodeID uint64) ([]accessmanager.ClusterRealtimeMonitorCard, int, error) {
	cards := make([]accessmanager.ClusterRealtimeMonitorCard, 0, len(defs))
	var firstErr error
	var available int
	for _, def := range defs {
		promQL := prometheusFilterNodeID(def.query(rateWindow), nodeID)
		series, stats, err := p.clusterCardSeries(ctx, def, promQL, start, end, step)
		card := accessmanager.ClusterRealtimeMonitorCard{
			Key:       def.key,
			Stage:     def.stage,
			Tone:      def.tone,
			Unit:      def.unit,
			Source:    accessmanager.ClusterRealtimeMonitorSourcePrometheus,
			Series:    series,
			Available: len(series) > 0 && err == nil,
		}
		if err != nil {
			card.Error = err.Error()
			if firstErr == nil {
				firstErr = fmt.Errorf("%s: %w", def.key, err)
			}
		} else if len(series) == 0 {
			card.Error = "prometheus returned no data"
			if firstErr == nil {
				firstErr = fmt.Errorf("%s: %s", def.key, card.Error)
			}
		} else {
			available++
			card.Value = clusterMonitorLatestValue(series)
			card.Stats = stats
		}
		cards = append(cards, card)
	}
	return cards, available, firstErr
}

func (p *managerClusterPrometheusMonitorProvider) clusterCardSeries(ctx context.Context, def clusterMonitorMetricDefinition, promQL string, start, end time.Time, step time.Duration) ([]accessmanager.RealtimeMonitorPoint, []accessmanager.ClusterRealtimeMonitorStat, error) {
	if def.nodeStats {
		results, err := managerMonitorQueryRangeResults(ctx, p.client, p.options.BaseURL, promQL, start, end, step)
		if err != nil {
			return nil, nil, err
		}
		series, stats, err := clusterMonitorNodeSeries(results, def.unit, def.transform)
		if err != nil {
			return nil, nil, err
		}
		if len(stats) == 0 {
			stats = clusterMonitorStats(series, def.unit, step)
		}
		return series, stats, nil
	}
	rawSeries, err := managerMonitorQueryRange(ctx, p.client, p.options.BaseURL, promQL, start, end, step)
	if err != nil {
		return nil, nil, err
	}
	series := clusterMonitorTransformSeries(rawSeries, def.transform)
	return series, clusterMonitorStats(series, def.unit, step), nil
}

func (p *managerClusterPrometheusMonitorProvider) controlSnapshot(ctx context.Context, nodeID uint64) (managerClusterControlSnapshot, accessmanager.ClusterRealtimeMonitorSource) {
	if p.options.Control == nil {
		return managerClusterControlSnapshot{}, accessmanager.ClusterRealtimeMonitorSource{
			Enabled: false,
			Error:   "control snapshot reader is not configured",
		}
	}
	started := time.Now()
	nodes, err := p.options.Control.ListNodes(ctx)
	if err != nil {
		return managerClusterControlSnapshot{}, accessmanager.ClusterRealtimeMonitorSource{
			Enabled: false,
			QueryMS: time.Since(started).Milliseconds(),
			Error:   fmt.Sprintf("read control snapshot nodes: %v", err),
		}
	}
	slots, err := p.options.Control.ListSlots(ctx, managementusecase.ListSlotsOptions{NodeID: nodeID})
	if err != nil {
		return managerClusterControlSnapshot{}, accessmanager.ClusterRealtimeMonitorSource{
			Enabled: false,
			QueryMS: time.Since(started).Milliseconds(),
			Error:   fmt.Sprintf("read control snapshot slots: %v", err),
		}
	}
	snapshot := managerClusterControlSnapshot{totalSlots: len(slots), ok: true}
	for _, node := range nodes.Items {
		if nodeID != 0 && node.NodeID != nodeID {
			continue
		}
		if strings.EqualFold(node.Status, "alive") {
			snapshot.nodesAlive++
		}
	}
	for _, slot := range slots {
		leaderMissing := slot.Runtime.LeaderID == 0
		quorumLost := slot.State.Quorum == "lost"
		if !slot.Runtime.HasQuorum && len(slot.Runtime.CurrentVoters) > 0 {
			quorumLost = true
		}
		if leaderMissing {
			snapshot.leaderMissing++
		}
		if quorumLost {
			snapshot.quorumLost++
		}
		if !leaderMissing && !quorumLost && (slot.Runtime.HasQuorum || slot.State.Quorum == "ready") {
			snapshot.slotsReady++
		}
	}
	return snapshot, accessmanager.ClusterRealtimeMonitorSource{
		Enabled: true,
		QueryMS: time.Since(started).Milliseconds(),
	}
}

func clusterMonitorStatus(total, available int, firstErr error, controlSource accessmanager.ClusterRealtimeMonitorSource) (string, string) {
	if available == 0 {
		if firstErr != nil {
			return accessmanager.ClusterRealtimeMonitorStatusPrometheusUnavailable, firstErr.Error()
		}
		return accessmanager.ClusterRealtimeMonitorStatusPrometheusUnavailable, "prometheus returned no cluster monitor series"
	}
	if available < total {
		if firstErr != nil {
			return accessmanager.ClusterRealtimeMonitorStatusPartial, firstErr.Error()
		}
		return accessmanager.ClusterRealtimeMonitorStatusPartial, ""
	}
	if !controlSource.Enabled {
		return accessmanager.ClusterRealtimeMonitorStatusPartial, ""
	}
	return accessmanager.ClusterRealtimeMonitorStatusReady, ""
}

func managerClusterMonitorMetricDefinitions() []clusterMonitorMetricDefinition {
	return []clusterMonitorMetricDefinition{
		clusterMetric("controllerProposeRate", accessmanager.ClusterRealtimeMonitorStageControlPlane, accessmanager.RealtimeMonitorToneNormal, "cmd/s", "sum(rate(wukongim_controller_decisions_total[%s]))"),
		clusterMetric("controllerApplyGap", accessmanager.ClusterRealtimeMonitorStageControlPlane, accessmanager.RealtimeMonitorToneWarning, "entries", "max(wukongim_controller_apply_gap)"),
		clusterMetric("slotLeaderStability", accessmanager.ClusterRealtimeMonitorStageSlotReplication, accessmanager.RealtimeMonitorToneNormal, "%", "(1 - clamp_max(sum(rate(wukongim_slot_leader_elections_total[%s])) / 10, 1)) * 100"),
		clusterMetric("slotProposeRate", accessmanager.ClusterRealtimeMonitorStageSlotReplication, accessmanager.RealtimeMonitorToneNormal, "cmd/s", prometheusZeroFallback("sum(rate(wukongim_slot_proposals_total[%s]))")),
		clusterMetric("slotApplyGap", accessmanager.ClusterRealtimeMonitorStageSlotReplication, accessmanager.RealtimeMonitorToneWarning, "entries", prometheusZeroFallback("max(wukongim_slot_apply_gap)")),
		clusterMetric("slotLatencyP99", accessmanager.ClusterRealtimeMonitorStageSlotReplication, accessmanager.RealtimeMonitorToneWarning, "ms", prometheusZeroFallback("histogram_quantile(0.99, sum(rate(wukongim_slot_apply_duration_seconds_bucket[%s])) by (le)) * 1000")),
		clusterMetric("channelAppendLatencyP99", accessmanager.ClusterRealtimeMonitorStageChannelReplication, accessmanager.RealtimeMonitorToneWarning, "ms", "histogram_quantile(0.99, sum(rate(wukongim_channelv2_append_duration_seconds_bucket[%s])) by (le)) * 1000"),
		clusterMetric("activeChannels", accessmanager.ClusterRealtimeMonitorStageChannelReplication, accessmanager.RealtimeMonitorToneNormal, "", prometheusZeroFallback("sum(wukongim_channelv2_active_runtimes)")),
		clusterMetric("internalTraffic", accessmanager.ClusterRealtimeMonitorStageInternalNetwork, accessmanager.RealtimeMonitorToneNormal, "B/s", "sum(rate(wukongim_transport_sent_bytes_total[%s])) + sum(rate(wukongim_transport_received_bytes_total[%s]))"),
		clusterMetric("rpcSuccessRate", accessmanager.ClusterRealtimeMonitorStageInternalNetwork, accessmanager.RealtimeMonitorToneNormal, "%", "(sum(rate(wukongim_transport_rpc_total{result=\"ok\"}[%s])) / clamp_min(sum(rate(wukongim_transport_rpc_total[%s])), 1)) * 100"),
		clusterMetric("rpcLatencyP95", accessmanager.ClusterRealtimeMonitorStageInternalNetwork, accessmanager.RealtimeMonitorToneWarning, "ms", "histogram_quantile(0.95, sum(rate(wukongim_transport_rpc_duration_seconds_bucket[%s])) by (le)) * 1000"),
		clusterMetric("workqueuePressure", accessmanager.ClusterRealtimeMonitorStageRuntimePressure, accessmanager.RealtimeMonitorToneWarning, "%", "max(wukongim_runtime_pool_queue_depth / clamp_min(wukongim_runtime_pool_queue_capacity, 1)) * 100"),
		clusterNodeMetric("nodeCpuPercent", accessmanager.ClusterRealtimeMonitorStageRuntimePressure, accessmanager.RealtimeMonitorToneWarning, "%", prometheusZeroFallback("wukongim_node_cpu_percent")),
		clusterNodeMetric("nodeMemoryRSS", accessmanager.ClusterRealtimeMonitorStageRuntimePressure, accessmanager.RealtimeMonitorToneWarning, "B", prometheusZeroFallback("wukongim_node_memory_rss_bytes")),
		clusterNodeMetric("nodeGoroutines", accessmanager.ClusterRealtimeMonitorStageRuntimePressure, accessmanager.RealtimeMonitorToneWarning, "", prometheusZeroFallback("wukongim_node_goroutines")),
		clusterMetric("storageWriteP99", accessmanager.ClusterRealtimeMonitorStageRuntimePressure, accessmanager.RealtimeMonitorToneWarning, "ms", "histogram_quantile(0.99, sum(rate(wukongim_storage_commit_request_duration_seconds_bucket[%s])) by (le)) * 1000"),
	}
}

func clusterMetricWithTransform(key, stage, tone, unit, pattern string, transform func(float64) float64) clusterMonitorMetricDefinition {
	def := clusterMetric(key, stage, tone, unit, pattern)
	def.transform = transform
	return def
}

func clusterNodeMetric(key, stage, tone, unit, pattern string) clusterMonitorMetricDefinition {
	def := clusterMetric(key, stage, tone, unit, pattern)
	def.nodeStats = true
	return def
}

func clusterMetric(key, stage, tone, unit, pattern string) clusterMonitorMetricDefinition {
	return clusterMonitorMetricDefinition{
		key:   key,
		stage: stage,
		tone:  tone,
		unit:  unit,
		query: func(rateWindow string) string {
			switch strings.Count(pattern, "%s") {
			case 2:
				return fmt.Sprintf(pattern, rateWindow, rateWindow)
			case 1:
				return fmt.Sprintf(pattern, rateWindow)
			default:
				return pattern
			}
		},
	}
}

func clusterMonitorNodeSeries(results []prometheusMatrixElement, unit string, transform func(float64) float64) ([]accessmanager.RealtimeMonitorPoint, []accessmanager.ClusterRealtimeMonitorStat, error) {
	series := make([]accessmanager.RealtimeMonitorPoint, 0)
	sortKeys := make(map[string]string)
	stats := make([]clusterMonitorNodeStat, 0, len(results))
	for index, result := range results {
		points, err := parsePrometheusMatrixValues(result.Values)
		if err != nil {
			return nil, nil, err
		}
		points = clusterMonitorTransformSeries(points, transform)
		if len(points) == 0 {
			continue
		}
		label, seriesKey, sortKey, ok := clusterMonitorNodeIdentity(result.Metric, index)
		for _, point := range points {
			if ok {
				point.Label = label
				point.SeriesKey = seriesKey
				sortKeys[seriesKey] = sortKey
			}
			series = append(series, point)
		}
		if ok {
			value := points[len(points)-1].Value
			stats = append(stats, clusterMonitorNodeStat{
				sortKey: sortKey,
				stat: accessmanager.ClusterRealtimeMonitorStat{
					Key:   "node",
					Label: label,
					Value: &value,
					Unit:  unit,
				},
			})
		}
	}
	sort.Slice(series, func(i, j int) bool {
		if series[i].Timestamp != series[j].Timestamp {
			return series[i].Timestamp < series[j].Timestamp
		}
		left := sortKeys[series[i].SeriesKey]
		right := sortKeys[series[j].SeriesKey]
		if left == right {
			return series[i].Label < series[j].Label
		}
		return left < right
	})
	sort.Slice(stats, func(i, j int) bool { return stats[i].sortKey < stats[j].sortKey })
	outStats := make([]accessmanager.ClusterRealtimeMonitorStat, 0, len(stats))
	for _, stat := range stats {
		outStats = append(outStats, stat.stat)
	}
	return series, outStats, nil
}

func clusterMonitorLatestValue(series []accessmanager.RealtimeMonitorPoint) float64 {
	if len(series) == 0 {
		return 0
	}
	latestTimestamp := series[0].Timestamp
	for _, point := range series[1:] {
		if point.Timestamp > latestTimestamp {
			latestTimestamp = point.Timestamp
		}
	}
	var value float64
	var found bool
	for _, point := range series {
		if point.Timestamp != latestTimestamp {
			continue
		}
		if !found || point.Value > value {
			value = point.Value
			found = true
		}
	}
	return value
}

type clusterMonitorNodeStat struct {
	sortKey string
	stat    accessmanager.ClusterRealtimeMonitorStat
}

func clusterMonitorNodeIdentity(metric map[string]string, index int) (string, string, string, bool) {
	if len(metric) == 0 {
		return "", "", "", false
	}
	nodeName := strings.TrimSpace(metric["node_name"])
	nodeID := strings.TrimSpace(metric["node_id"])
	if nodeName != "" {
		seriesKey := clusterMonitorNodeSeriesKey(nodeID, nodeName, index)
		return nodeName, seriesKey, clusterMonitorNodeSortKey(nodeID, nodeName, index), true
	}
	if nodeID != "" {
		label := "node-" + nodeID
		seriesKey := clusterMonitorNodeSeriesKey(nodeID, label, index)
		return label, seriesKey, clusterMonitorNodeSortKey(nodeID, label, index), true
	}
	return "", "", "", false
}

func clusterMonitorNodeSeriesKey(nodeID, fallback string, index int) string {
	if strings.TrimSpace(nodeID) != "" {
		return "node-" + strings.TrimSpace(nodeID)
	}
	key := strings.TrimSpace(fallback)
	if key == "" {
		key = fmt.Sprintf("node-%d", index+1)
	}
	return key
}

func clusterMonitorNodeSortKey(nodeID, fallback string, index int) string {
	if parsed, err := strconv.ParseUint(strings.TrimSpace(nodeID), 10, 64); err == nil {
		return fmt.Sprintf("%020d", parsed)
	}
	return fmt.Sprintf("z-%020d-%s", index, fallback)
}

func clusterMonitorTransformSeries(series []accessmanager.RealtimeMonitorPoint, transform func(float64) float64) []accessmanager.RealtimeMonitorPoint {
	if transform == nil || len(series) == 0 {
		return series
	}
	out := make([]accessmanager.RealtimeMonitorPoint, 0, len(series))
	for _, point := range series {
		out = append(out, accessmanager.RealtimeMonitorPoint{
			Timestamp: point.Timestamp,
			Value:     transform(point.Value),
			Label:     point.Label,
			SeriesKey: point.SeriesKey,
		})
	}
	return out
}

func clusterMonitorStats(series []accessmanager.RealtimeMonitorPoint, unit string, step time.Duration) []accessmanager.ClusterRealtimeMonitorStat {
	base := monitorCardStats(series, step)
	out := make([]accessmanager.ClusterRealtimeMonitorStat, 0, len(base))
	for _, stat := range base {
		value := stat.Value
		out = append(out, accessmanager.ClusterRealtimeMonitorStat{Key: stat.Key, Value: &value, Unit: unit})
	}
	return out
}

func clusterMonitorSnapshot(cards []accessmanager.ClusterRealtimeMonitorCard, control managerClusterControlSnapshot) []accessmanager.ClusterRealtimeMonitorSnapshotEntry {
	byKey := make(map[string]accessmanager.ClusterRealtimeMonitorCard, len(cards))
	for _, card := range cards {
		if card.Available {
			byKey[card.Key] = card
		}
	}
	out := make([]accessmanager.ClusterRealtimeMonitorSnapshotEntry, 0, 7)
	if control.ok {
		out = append(out,
			accessmanager.ClusterRealtimeMonitorSnapshotEntry{
				Key:       "nodesAlive",
				MetricKey: "nodesAlive",
				Value:     float64(control.nodesAlive),
				Tone:      accessmanager.RealtimeMonitorToneNormal,
				Source:    accessmanager.ClusterRealtimeMonitorSourceControlSnapshot,
			},
			accessmanager.ClusterRealtimeMonitorSnapshotEntry{
				Key:       "slotsReady",
				MetricKey: "slotsReady",
				Value:     clusterMonitorSlotsReadyPercent(control),
				Unit:      "%",
				Tone:      clusterMonitorSlotsTone(control),
				Source:    accessmanager.ClusterRealtimeMonitorSourceControlSnapshot,
			},
		)
	}
	out = appendClusterCardSnapshot(out, byKey, "controllerApplyGap", "controllerApplyGap", accessmanager.RealtimeMonitorToneWarning, func(value float64) float64 { return value })
	out = appendClusterCardSnapshot(out, byKey, "rpcErrorRate", "rpcSuccessRate", accessmanager.RealtimeMonitorToneWarning, func(value float64) float64 {
		if value > 100 {
			return 0
		}
		return 100 - value
	})
	out = appendClusterCardSnapshot(out, byKey, "queuePressure", "workqueuePressure", accessmanager.RealtimeMonitorToneWarning, func(value float64) float64 { return value })
	out = appendClusterCardSnapshot(out, byKey, "storageWriteP99", "storageWriteP99", accessmanager.RealtimeMonitorToneWarning, func(value float64) float64 { return value })
	return out
}

func appendClusterCardSnapshot(out []accessmanager.ClusterRealtimeMonitorSnapshotEntry, byKey map[string]accessmanager.ClusterRealtimeMonitorCard, key, metricKey, tone string, value func(float64) float64) []accessmanager.ClusterRealtimeMonitorSnapshotEntry {
	card, ok := byKey[metricKey]
	if !ok {
		return out
	}
	return appendClusterCardSnapshotEntry(out, card, key, metricKey, tone, card.Unit, value)
}

func appendClusterCardSnapshotEntry(out []accessmanager.ClusterRealtimeMonitorSnapshotEntry, card accessmanager.ClusterRealtimeMonitorCard, key, metricKey, tone, unit string, value func(float64) float64) []accessmanager.ClusterRealtimeMonitorSnapshotEntry {
	return append(out, accessmanager.ClusterRealtimeMonitorSnapshotEntry{
		Key:       key,
		MetricKey: metricKey,
		Value:     value(card.Value),
		Unit:      unit,
		Tone:      tone,
		Source:    accessmanager.ClusterRealtimeMonitorSourcePrometheus,
	})
}

func clusterMonitorSlotsReadyPercent(control managerClusterControlSnapshot) float64 {
	if control.totalSlots <= 0 {
		return 0
	}
	return (float64(control.slotsReady) / float64(control.totalSlots)) * 100
}

func clusterMonitorSlotsTone(control managerClusterControlSnapshot) string {
	if control.totalSlots <= 0 || control.leaderMissing > 0 || control.quorumLost > 0 || control.slotsReady < control.totalSlots {
		return accessmanager.RealtimeMonitorToneWarning
	}
	return accessmanager.RealtimeMonitorToneNormal
}
