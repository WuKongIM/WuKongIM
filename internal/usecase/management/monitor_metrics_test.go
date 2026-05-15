package management

import (
	"context"
	"testing"
	"time"

	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	"github.com/WuKongIM/WuKongIM/pkg/metrics"
	"github.com/stretchr/testify/require"
)

func TestMonitorMetricsFromDashboardBuildsTimestampedSeries(t *testing.T) {
	generatedAt := time.Date(2026, 5, 15, 8, 30, 0, 0, time.UTC)
	result := monitorMetricsFromDashboard(1, metrics.QueryResult{
		GeneratedAt:   generatedAt,
		WindowSeconds: 10,
		StepSeconds:   5,
		Points:        2,
		SendPerSec: metrics.MetricSeries{
			Latest: 8,
			Peak:   8,
			Avg:    6,
			Series: []float64{4, 8},
		},
		Connections: metrics.MetricSeries{
			Latest: 12,
			Peak:   12,
			Avg:    11,
			Series: []float64{10, 12},
		},
	}, false)

	require.Equal(t, generatedAt, result.GeneratedAt)
	require.Equal(t, MonitorMetricsScope{View: MonitorMetricsScopeLocalNode, LocalNodeID: 1, NodeID: 1}, result.Scope)
	require.False(t, result.Capabilities.NodeFilter)
	require.Equal(t, []MonitorMetricsNode{{
		NodeID:    1,
		Name:      "node-1",
		IsLocal:   true,
		Available: true,
	}}, result.Nodes)

	sendRate := result.Metrics["send_rate"]
	require.Equal(t, "msg/s", sendRate.Unit)
	require.Equal(t, float64(8), sendRate.Latest)
	require.Equal(t, []MonitorMetricPoint{
		{At: generatedAt.Add(-10 * time.Second), Value: 4},
		{At: generatedAt.Add(-5 * time.Second), Value: 8},
	}, sendRate.Points)

	connections := result.Metrics["online_connections"]
	require.Equal(t, "connections", connections.Unit)
	require.Equal(t, float64(12), connections.Latest)
	require.NotContains(t, result.Metrics, "cpu_usage")
	require.NotContains(t, result.Metrics, "write_iops")
}

func TestAggregateMonitorMetricsCombinesClusterSeries(t *testing.T) {
	generatedAt := time.Date(2026, 5, 15, 8, 30, 0, 0, time.UTC)
	nodes := []MonitorMetricsNode{
		{NodeID: 1, Name: "node-1", IsLocal: true, Available: true},
		{NodeID: 2, Name: "node-2", Available: true},
	}

	result := aggregateMonitorMetrics(generatedAt, MonitorMetricsScope{
		View:        MonitorMetricsScopeCluster,
		LocalNodeID: 1,
	}, MonitorMetricsCapabilities{NodeFilter: true}, nodes, []metrics.QueryResult{
		monitorQueryResult([]float64{10, 20}, []float64{100, 200}, []float64{5, 20}, []float64{50, 100}, []float64{5, 10}, []float64{30, 80}, []float64{10, 12}, []float64{4, 7}),
		monitorQueryResult([]float64{5, 15}, []float64{50, 100}, []float64{5, 10}, []float64{40, 100}, []float64{4, 20}, []float64{20, 40}, []float64{3, 4}, []float64{8, 6}),
	})

	require.Equal(t, MonitorMetricsScopeCluster, result.Scope.View)
	require.True(t, result.Capabilities.NodeFilter)
	require.Equal(t, nodes, result.Nodes)
	require.Equal(t, []MonitorMetricPoint{
		{At: generatedAt.Add(-10 * time.Second), Value: 3},
		{At: generatedAt.Add(-5 * time.Second), Value: 7},
	}, result.Metrics["send_rate"].Points)
	require.Equal(t, []MonitorMetricPoint{
		{At: generatedAt.Add(-10 * time.Second), Value: 13},
		{At: generatedAt.Add(-5 * time.Second), Value: 16},
	}, result.Metrics["online_connections"].Points)
	require.Equal(t, []MonitorMetricPoint{
		{At: generatedAt.Add(-10 * time.Second), Value: 8},
		{At: generatedAt.Add(-5 * time.Second), Value: 7},
	}, result.Metrics["send_latency_p99"].Points)
	require.Equal(t, []MonitorMetricPoint{
		{At: generatedAt.Add(-10 * time.Second), Value: 6.7},
		{At: generatedAt.Add(-5 * time.Second), Value: 10},
	}, result.Metrics["send_fail_rate"].Points)
	require.Equal(t, []MonitorMetricPoint{
		{At: generatedAt.Add(-10 * time.Second), Value: 3.3},
		{At: generatedAt.Add(-5 * time.Second), Value: 3.4},
	}, result.Metrics["fan_out_rate"].Points)
}

func TestGetMonitorMetricsSelectsRequestedNode(t *testing.T) {
	generatedAt := time.Date(2026, 5, 15, 8, 30, 0, 0, time.UTC)
	reader := &monitorMetricsReaderStub{
		results: map[uint64]metrics.QueryResult{
			2: monitorQueryResult([]float64{10, 20}, []float64{100, 200}, []float64{5, 20}, []float64{50, 100}, []float64{5, 10}, []float64{30, 80}, []float64{10, 12}, []float64{4, 7}),
		},
	}
	app := New(Options{
		LocalNodeID: 1,
		Cluster: fakeClusterReader{nodes: []controllermeta.ClusterNode{
			{NodeID: 1, Name: "node-1", Status: controllermeta.NodeStatusAlive},
			{NodeID: 2, Name: "node-2", Status: controllermeta.NodeStatusAlive},
		}},
		MonitorMetrics: reader,
		Now:            func() time.Time { return generatedAt },
	})

	result, err := app.GetMonitorMetrics(context.Background(), 2, 10*time.Second, 5*time.Second)

	require.NoError(t, err)
	require.Equal(t, []uint64{2}, reader.calls)
	require.Equal(t, MonitorMetricsScopeNode, result.Scope.View)
	require.Equal(t, uint64(2), result.Scope.NodeID)
	require.Equal(t, []MonitorMetricsNode{
		{NodeID: 1, Name: "node-1", IsLocal: true, Available: false},
		{NodeID: 2, Name: "node-2", Available: true},
	}, result.Nodes)
	require.Equal(t, float64(4), result.Metrics["send_rate"].Latest)
}

func monitorQueryResult(sendDelta, sendTotalDelta, sendFailDelta, deliverTotalDelta, deliverFailDelta, routesDelta, connections, sendLatency []float64) metrics.QueryResult {
	return metrics.QueryResult{
		GeneratedAt:        time.Date(2026, 5, 15, 8, 30, 0, 0, time.UTC),
		WindowSeconds:      10,
		StepSeconds:        5,
		Points:             2,
		SendDelta:          metrics.MetricSeries{Series: sendDelta},
		SendTotalDelta:     metrics.MetricSeries{Series: sendTotalDelta},
		SendFailDelta:      metrics.MetricSeries{Series: sendFailDelta},
		DeliverTotalDelta:  metrics.MetricSeries{Series: deliverTotalDelta},
		DeliverFailDelta:   metrics.MetricSeries{Series: deliverFailDelta},
		ResolveRoutesDelta: metrics.MetricSeries{Series: routesDelta},
		Connections:        metrics.MetricSeries{Latest: connections[len(connections)-1], Series: connections},
		SendLatencyP99Ms:   metrics.MetricSeries{Latest: sendLatency[len(sendLatency)-1], Series: sendLatency},
	}
}

type monitorMetricsReaderStub struct {
	results map[uint64]metrics.QueryResult
	calls   []uint64
}

func (s *monitorMetricsReaderStub) NodeMonitorMetrics(_ context.Context, nodeID uint64, _, _ time.Duration) (metrics.QueryResult, error) {
	s.calls = append(s.calls, nodeID)
	return s.results[nodeID], nil
}
