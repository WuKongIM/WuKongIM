package management

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"

	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	"github.com/WuKongIM/WuKongIM/pkg/metrics"
)

const (
	// MonitorMetricsScopeCluster means the series aggregate all reachable cluster nodes.
	MonitorMetricsScopeCluster = "cluster"
	// MonitorMetricsScopeNode means the series were collected from one selected cluster node.
	MonitorMetricsScopeNode = "node"
	// MonitorMetricsScopeLocalNode means the series were collected from this node's local runtime.
	MonitorMetricsScopeLocalNode = "local_node"
)

// MonitorMetricsResult is the manager-facing real-time monitor DTO.
type MonitorMetricsResult struct {
	// GeneratedAt is the time when the monitor metrics response was assembled.
	GeneratedAt time.Time
	// WindowSeconds is the requested query window size in seconds.
	WindowSeconds int
	// StepSeconds is the requested chart bucket size in seconds.
	StepSeconds int
	// Points is the number of points in every returned series.
	Points int
	// Scope describes the observation scope for this first monitor API version.
	Scope MonitorMetricsScope
	// Capabilities reports which interactive filters are backed by real data.
	Capabilities MonitorMetricsCapabilities
	// Nodes lists nodes that can be represented by the current response.
	Nodes []MonitorMetricsNode
	// Metrics contains available real metric series keyed by stable snake_case names.
	Metrics map[string]MonitorMetricSeries
}

// MonitorMetricsScope describes the source scope for monitor metrics.
type MonitorMetricsScope struct {
	// View is the stable scope name.
	View string
	// LocalNodeID identifies the node that owns the local collector.
	LocalNodeID uint64
	// NodeID identifies the selected node when View is "node" or "local_node".
	NodeID uint64
}

// MonitorMetricsCapabilities reports UI capabilities that are backed by real data.
type MonitorMetricsCapabilities struct {
	// NodeFilter is true only when the API can return per-node series.
	NodeFilter bool
}

// MonitorMetricsNode describes one node visible to the monitor response.
type MonitorMetricsNode struct {
	// NodeID is the cluster node identifier.
	NodeID uint64
	// Name is the display name for the node.
	Name string
	// IsLocal reports whether this node owns the local collector.
	IsLocal bool
	// Available reports whether this node has metric series in the response.
	Available bool
}

// MonitorMetricSeries is one timestamped monitor metric series.
type MonitorMetricSeries struct {
	// Key is the stable snake_case metric key.
	Key string
	// Unit is the display unit for values.
	Unit string
	// Latest is the latest value in the series.
	Latest float64
	// Peak is the maximum value in the series.
	Peak float64
	// Avg is the average value in the series.
	Avg float64
	// Points contains timestamped values for chart rendering.
	Points []MonitorMetricPoint
}

// MonitorMetricPoint is one chart point for a monitor metric.
type MonitorMetricPoint struct {
	// At is the chart bucket start time.
	At time.Time
	// Value is the metric value for the bucket.
	Value float64
}

// GetMonitorMetrics returns timestamped real-time monitor metrics for manager pages.
func (a *App) GetMonitorMetrics(ctx context.Context, nodeID uint64, window, step time.Duration) (MonitorMetricsResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if nodeID != 0 {
		return a.getSelectedNodeMonitorMetrics(ctx, nodeID, window, step)
	}
	nodes, err := a.monitorClusterNodes(ctx)
	if err != nil || len(nodes) == 0 {
		return a.getLocalMonitorMetrics(window, step, false)
	}
	return a.getClusterMonitorMetrics(ctx, nodes, window, step)
}

// GetLocalMonitorMetrics returns this node's local monitor collector result for node RPC providers.
func (a *App) GetLocalMonitorMetrics(window, step time.Duration) (metrics.QueryResult, error) {
	if a == nil || a.dashCollector == nil {
		return metrics.QueryResult{}, metrics.ErrInsufficientData
	}
	return a.dashCollector.Query(window, step)
}

func (a *App) getLocalMonitorMetrics(window, step time.Duration, nodeFilter bool) (MonitorMetricsResult, error) {
	qr, err := a.GetLocalMonitorMetrics(window, step)
	if err != nil {
		return MonitorMetricsResult{}, err
	}
	return monitorMetricsFromDashboard(a.localNodeID, qr, nodeFilter), nil
}

func (a *App) getSelectedNodeMonitorMetrics(ctx context.Context, nodeID uint64, window, step time.Duration) (MonitorMetricsResult, error) {
	nodes, _ := a.monitorClusterNodes(ctx)
	qr, err := a.queryNodeMonitorMetrics(ctx, nodeID, window, step)
	if err != nil {
		return MonitorMetricsResult{}, err
	}
	scopeView := MonitorMetricsScopeNode
	if nodeID == a.localNodeID {
		scopeView = MonitorMetricsScopeLocalNode
	}
	return aggregateMonitorMetrics(qr.GeneratedAt, MonitorMetricsScope{
		View:        scopeView,
		LocalNodeID: a.localNodeID,
		NodeID:      nodeID,
	}, MonitorMetricsCapabilities{NodeFilter: len(nodes) > 0}, monitorNodesWithAvailability(a.localNodeID, nodes, map[uint64]bool{nodeID: true}), []metrics.QueryResult{qr}), nil
}

func (a *App) getClusterMonitorMetrics(ctx context.Context, nodes []controllermeta.ClusterNode, window, step time.Duration) (MonitorMetricsResult, error) {
	targets := monitorMetricTargets(nodes)
	results := make([]metrics.QueryResult, 0, len(targets))
	available := make(map[uint64]bool, len(targets))
	for _, node := range targets {
		qr, err := a.queryNodeMonitorMetrics(ctx, node.NodeID, window, step)
		if err != nil {
			continue
		}
		results = append(results, qr)
		available[node.NodeID] = true
	}
	if len(results) == 0 {
		return MonitorMetricsResult{}, metrics.ErrInsufficientData
	}
	return aggregateMonitorMetrics(a.now(), MonitorMetricsScope{
		View:        MonitorMetricsScopeCluster,
		LocalNodeID: a.localNodeID,
	}, MonitorMetricsCapabilities{NodeFilter: true}, monitorNodesWithAvailability(a.localNodeID, nodes, available), results), nil
}

func (a *App) queryNodeMonitorMetrics(ctx context.Context, nodeID uint64, window, step time.Duration) (metrics.QueryResult, error) {
	if nodeID == 0 || nodeID == a.localNodeID {
		return a.GetLocalMonitorMetrics(window, step)
	}
	if a == nil || a.monitorMetrics == nil {
		return metrics.QueryResult{}, metrics.ErrInsufficientData
	}
	return a.monitorMetrics.NodeMonitorMetrics(ctx, nodeID, window, step)
}

func (a *App) monitorClusterNodes(ctx context.Context) ([]controllermeta.ClusterNode, error) {
	if a == nil || a.cluster == nil {
		return nil, fmt.Errorf("management: cluster unavailable")
	}
	nodes, err := a.cluster.ListNodesStrict(ctx)
	if err != nil {
		return nil, err
	}
	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i].NodeID < nodes[j].NodeID
	})
	return nodes, nil
}

func monitorMetricsFromDashboard(localNodeID uint64, qr metrics.QueryResult, nodeFilter bool) MonitorMetricsResult {
	return MonitorMetricsResult{
		GeneratedAt:   qr.GeneratedAt,
		WindowSeconds: qr.WindowSeconds,
		StepSeconds:   qr.StepSeconds,
		Points:        qr.Points,
		Scope: MonitorMetricsScope{
			View:        MonitorMetricsScopeLocalNode,
			LocalNodeID: localNodeID,
			NodeID:      localNodeID,
		},
		Capabilities: MonitorMetricsCapabilities{
			NodeFilter: nodeFilter,
		},
		Nodes: []MonitorMetricsNode{{
			NodeID:    localNodeID,
			Name:      fmt.Sprintf("node-%d", localNodeID),
			IsLocal:   true,
			Available: true,
		}},
		Metrics: map[string]MonitorMetricSeries{
			"send_rate":            monitorMetricSeries("send_rate", "msg/s", qr.SendPerSec, qr),
			"deliver_rate":         monitorMetricSeries("deliver_rate", "msg/s", qr.DeliverPerSec, qr),
			"online_connections":   monitorMetricSeries("online_connections", "connections", qr.Connections, qr),
			"send_latency_p99":     monitorMetricSeries("send_latency_p99", "ms", qr.SendLatencyP99Ms, qr),
			"delivery_latency_p99": monitorMetricSeries("delivery_latency_p99", "ms", qr.DeliveryLatencyP99Ms, qr),
			"send_fail_rate":       monitorMetricSeries("send_fail_rate", "%", qr.SendFailRatePercent, qr),
			"delivery_fail_rate":   monitorMetricSeries("delivery_fail_rate", "%", qr.DeliveryFailRatePercent, qr),
			"active_channels":      monitorMetricSeries("active_channels", "channels", qr.ActiveChannels, qr),
			"retry_queue_depth":    monitorMetricSeries("retry_queue_depth", "messages", qr.RetryQueueDepth, qr),
			"fan_out_rate":         monitorMetricSeries("fan_out_rate", "x", qr.FanOutRate, qr),
		},
	}
}

func aggregateMonitorMetrics(generatedAt time.Time, scope MonitorMetricsScope, capabilities MonitorMetricsCapabilities, nodes []MonitorMetricsNode, results []metrics.QueryResult) MonitorMetricsResult {
	if len(results) == 0 {
		return MonitorMetricsResult{}
	}
	base := results[0]
	stepSec := float64(base.StepSeconds)
	if stepSec <= 0 {
		stepSec = 1
	}
	points := base.Points
	if points <= 0 {
		points = len(base.SendPerSec.Series)
	}
	sendDelta := sumMetricSeries(points, results, func(qr metrics.QueryResult) metrics.MetricSeries { return qr.SendDelta })
	deliverDelta := sumMetricSeries(points, results, func(qr metrics.QueryResult) metrics.MetricSeries { return qr.DeliverDelta })
	sendRate := scaleAndRoundSeries(sendDelta, stepSec)
	deliverRate := scaleAndRoundSeries(deliverDelta, stepSec)
	sendTotalDelta := sumMetricSeries(points, results, func(qr metrics.QueryResult) metrics.MetricSeries { return qr.SendTotalDelta })
	sendFailDelta := sumMetricSeries(points, results, func(qr metrics.QueryResult) metrics.MetricSeries { return qr.SendFailDelta })
	deliverTotalDelta := sumMetricSeries(points, results, func(qr metrics.QueryResult) metrics.MetricSeries { return qr.DeliverTotalDelta })
	deliverFailDelta := sumMetricSeries(points, results, func(qr metrics.QueryResult) metrics.MetricSeries { return qr.DeliverFailDelta })
	resolveRoutesDelta := sumMetricSeries(points, results, func(qr metrics.QueryResult) metrics.MetricSeries { return qr.ResolveRoutesDelta })

	qr := metrics.QueryResult{
		GeneratedAt:             generatedAt,
		WindowSeconds:           base.WindowSeconds,
		StepSeconds:             base.StepSeconds,
		Points:                  points,
		SendPerSec:              buildMonitorMetricSeries(sendRate),
		DeliverPerSec:           buildMonitorMetricSeries(deliverRate),
		Connections:             buildMonitorMetricSeries(sumMetricSeries(points, results, func(qr metrics.QueryResult) metrics.MetricSeries { return qr.Connections })),
		SendLatencyP99Ms:        buildMonitorMetricSeries(maxMetricSeries(points, results, func(qr metrics.QueryResult) metrics.MetricSeries { return qr.SendLatencyP99Ms })),
		DeliveryLatencyP99Ms:    buildMonitorMetricSeries(maxMetricSeries(points, results, func(qr metrics.QueryResult) metrics.MetricSeries { return qr.DeliveryLatencyP99Ms })),
		SendFailRatePercent:     buildMonitorMetricSeries(ratioPercentSeries(sendFailDelta, sendTotalDelta)),
		DeliveryFailRatePercent: buildMonitorMetricSeries(ratioPercentSeries(deliverFailDelta, deliverTotalDelta)),
		ActiveChannels:          buildMonitorMetricSeries(sumMetricSeries(points, results, func(qr metrics.QueryResult) metrics.MetricSeries { return qr.ActiveChannels })),
		RetryQueueDepth:         buildMonitorMetricSeries(sumMetricSeries(points, results, func(qr metrics.QueryResult) metrics.MetricSeries { return qr.RetryQueueDepth })),
		FanOutRate:              buildMonitorMetricSeries(ratioSeries(resolveRoutesDelta, sendDelta)),
	}
	out := monitorMetricsFromDashboard(scope.LocalNodeID, qr, capabilities.NodeFilter)
	out.Scope = scope
	out.Capabilities = capabilities
	out.Nodes = nodes
	return out
}

func monitorMetricTargets(nodes []controllermeta.ClusterNode) []controllermeta.ClusterNode {
	targets := make([]controllermeta.ClusterNode, 0, len(nodes))
	for _, node := range nodes {
		switch node.Status {
		case controllermeta.NodeStatusAlive, controllermeta.NodeStatusSuspect, controllermeta.NodeStatusDraining:
			targets = append(targets, node)
		}
	}
	return targets
}

func monitorNodesWithAvailability(localNodeID uint64, nodes []controllermeta.ClusterNode, available map[uint64]bool) []MonitorMetricsNode {
	if len(nodes) == 0 {
		return []MonitorMetricsNode{{
			NodeID:    localNodeID,
			Name:      fmt.Sprintf("node-%d", localNodeID),
			IsLocal:   true,
			Available: available[localNodeID],
		}}
	}
	out := make([]MonitorMetricsNode, 0, len(nodes))
	for _, node := range nodes {
		out = append(out, MonitorMetricsNode{
			NodeID:    node.NodeID,
			Name:      monitorNodeName(node),
			IsLocal:   node.NodeID == localNodeID,
			Available: available[node.NodeID],
		})
	}
	return out
}

func monitorNodeName(node controllermeta.ClusterNode) string {
	if node.Name != "" {
		return node.Name
	}
	return fmt.Sprintf("node-%d", node.NodeID)
}

func sumMetricSeries(points int, results []metrics.QueryResult, pick func(metrics.QueryResult) metrics.MetricSeries) []float64 {
	out := make([]float64, points)
	for _, result := range results {
		series := pick(result).Series
		for i := 0; i < points && i < len(series); i++ {
			out[i] += series[i]
		}
	}
	return out
}

func maxMetricSeries(points int, results []metrics.QueryResult, pick func(metrics.QueryResult) metrics.MetricSeries) []float64 {
	out := make([]float64, points)
	for _, result := range results {
		series := pick(result).Series
		for i := 0; i < points && i < len(series); i++ {
			if series[i] > out[i] {
				out[i] = series[i]
			}
		}
	}
	return out
}

func scaleAndRoundSeries(values []float64, divisor float64) []float64 {
	out := make([]float64, len(values))
	for i, value := range values {
		out[i] = math.Round(value / divisor)
	}
	return out
}

func ratioPercentSeries(numerator, denominator []float64) []float64 {
	out := make([]float64, len(numerator))
	for i := range numerator {
		if i < len(denominator) && denominator[i] > 0 {
			out[i] = math.Round(numerator[i]/denominator[i]*1000) / 10
		}
	}
	return out
}

func ratioSeries(numerator, denominator []float64) []float64 {
	out := make([]float64, len(numerator))
	for i := range numerator {
		if i < len(denominator) && denominator[i] > 0 {
			out[i] = math.Round(numerator[i]/denominator[i]*10) / 10
		}
	}
	return out
}

func buildMonitorMetricSeries(series []float64) metrics.MetricSeries {
	if len(series) == 0 {
		return metrics.MetricSeries{}
	}
	latest := series[len(series)-1]
	var peak, sum float64
	for _, v := range series {
		sum += v
		if v > peak {
			peak = v
		}
	}
	return metrics.MetricSeries{
		Latest: latest,
		Peak:   peak,
		Avg:    math.Round(sum/float64(len(series))*10) / 10,
		Series: series,
	}
}

func monitorMetricSeries(key, unit string, series metrics.MetricSeries, qr metrics.QueryResult) MonitorMetricSeries {
	points := make([]MonitorMetricPoint, 0, len(series.Series))
	step := time.Duration(qr.StepSeconds) * time.Second
	start := qr.GeneratedAt.Add(-time.Duration(qr.WindowSeconds) * time.Second)
	for i, value := range series.Series {
		points = append(points, MonitorMetricPoint{
			At:    start.Add(time.Duration(i) * step),
			Value: value,
		})
	}
	return MonitorMetricSeries{
		Key:    key,
		Unit:   unit,
		Latest: series.Latest,
		Peak:   series.Peak,
		Avg:    series.Avg,
		Points: points,
	}
}
