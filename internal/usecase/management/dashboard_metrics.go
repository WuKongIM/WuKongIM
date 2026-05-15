package management

import (
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/metrics"
)

// DashboardMetricsResult is the usecase-layer DTO for dashboard metrics.
type DashboardMetricsResult struct {
	GeneratedAt   time.Time
	WindowSeconds int
	StepSeconds   int
	Points        int
	Metrics       DashboardMetricsMap
}

// DashboardMetricsMap holds all 10 metric series.
type DashboardMetricsMap struct {
	SendPerSec              metrics.MetricSeries
	DeliverPerSec           metrics.MetricSeries
	Connections             metrics.MetricSeries
	SendLatencyP99Ms        metrics.MetricSeries
	DeliveryLatencyP99Ms    metrics.MetricSeries
	SendFailRatePercent     metrics.MetricSeries
	DeliveryFailRatePercent metrics.MetricSeries
	ActiveChannels          metrics.MetricSeries
	RetryQueueDepth         metrics.MetricSeries
	FanOutRate              metrics.MetricSeries
}

// GetDashboardMetrics queries the dashboard collector for time-series metrics.
func (a *App) GetDashboardMetrics(window, step time.Duration) (DashboardMetricsResult, error) {
	if a.dashCollector == nil {
		return DashboardMetricsResult{}, metrics.ErrInsufficientData
	}
	qr, err := a.dashCollector.Query(window, step)
	if err != nil {
		return DashboardMetricsResult{}, err
	}
	return DashboardMetricsResult{
		GeneratedAt:   qr.GeneratedAt,
		WindowSeconds: qr.WindowSeconds,
		StepSeconds:   qr.StepSeconds,
		Points:        qr.Points,
		Metrics: DashboardMetricsMap{
			SendPerSec:              qr.SendPerSec,
			DeliverPerSec:           qr.DeliverPerSec,
			Connections:             qr.Connections,
			SendLatencyP99Ms:        qr.SendLatencyP99Ms,
			DeliveryLatencyP99Ms:    qr.DeliveryLatencyP99Ms,
			SendFailRatePercent:     qr.SendFailRatePercent,
			DeliveryFailRatePercent: qr.DeliveryFailRatePercent,
			ActiveChannels:          qr.ActiveChannels,
			RetryQueueDepth:         qr.RetryQueueDepth,
			FanOutRate:              qr.FanOutRate,
		},
	}, nil
}
