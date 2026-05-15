package metrics

import (
	"errors"
	"sync"
	"time"
)

const dashboardCollectorCapacity = 3600

var ErrInsufficientData = errors.New("dashboard collector: insufficient data")

// RawSample holds one second of snapshotted metric values.
type RawSample struct {
	At time.Time
	// Counters (cumulative)
	SendCount          float64
	DeliverCount       float64
	SendTotalCount     float64
	SendFailCount      float64
	DeliverTotalCount  float64
	DeliverFailCount   float64
	ResolveRoutesCount float64
	// Gauges (instantaneous)
	Connections     int64
	ActiveChannels  int64
	RetryQueueDepth int64
	// Histogram p99 (seconds)
	SendLatencyP99     float64
	DeliveryLatencyP99 float64
}

// MetricSeries is the per-metric response shape returned by Query.
type MetricSeries struct {
	Latest float64
	Peak   float64
	Avg    float64
	Series []float64
}

// QueryResult holds all 10 metric series for a dashboard query.
type QueryResult struct {
	GeneratedAt             time.Time
	WindowSeconds           int
	StepSeconds             int
	Points                  int
	SendPerSec              MetricSeries
	DeliverPerSec           MetricSeries
	Connections             MetricSeries
	SendLatencyP99Ms        MetricSeries
	DeliveryLatencyP99Ms    MetricSeries
	SendFailRatePercent     MetricSeries
	DeliveryFailRatePercent MetricSeries
	ActiveChannels          MetricSeries
	RetryQueueDepth         MetricSeries
	FanOutRate              MetricSeries
}

// DashboardCollector samples metrics every second into a ring buffer.
type DashboardCollector struct {
	mu       sync.RWMutex
	ring     []RawSample
	head     int
	count    int
	capacity int
	registry *Registry
	stop     chan struct{}
	done     chan struct{}
}

// NewDashboardCollector creates a collector with a 1-hour ring buffer.
func NewDashboardCollector(registry *Registry) *DashboardCollector {
	return &DashboardCollector{
		ring:     make([]RawSample, dashboardCollectorCapacity),
		capacity: dashboardCollectorCapacity,
		registry: registry,
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
}
