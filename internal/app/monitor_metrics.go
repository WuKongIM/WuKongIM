package app

import (
	"context"
	"time"

	accessnode "github.com/WuKongIM/WuKongIM/internal/access/node"
	"github.com/WuKongIM/WuKongIM/pkg/metrics"
)

type nodeMonitorMetricsProvider struct {
	collector *metrics.DashboardCollector
}

func (p nodeMonitorMetricsProvider) LocalMonitorMetrics(_ context.Context, window, step time.Duration) (metrics.QueryResult, error) {
	if p.collector == nil {
		return metrics.QueryResult{}, metrics.ErrInsufficientData
	}
	return p.collector.Query(window, step)
}

type managementMonitorMetricsReader struct {
	localNodeID uint64
	collector   *metrics.DashboardCollector
	remote      *accessnode.Client
}

func (r managementMonitorMetricsReader) NodeMonitorMetrics(ctx context.Context, nodeID uint64, window, step time.Duration) (metrics.QueryResult, error) {
	if nodeID == r.localNodeID {
		if r.collector == nil {
			return metrics.QueryResult{}, metrics.ErrInsufficientData
		}
		return r.collector.Query(window, step)
	}
	if r.remote == nil {
		return metrics.QueryResult{}, metrics.ErrInsufficientData
	}
	return r.remote.MonitorMetrics(ctx, nodeID, window, step)
}
