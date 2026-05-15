package metrics

import (
	"errors"
	"math"
	"sync"
	"time"

	dto "github.com/prometheus/client_model/go"
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

// Start launches the background sampling goroutine.
func (c *DashboardCollector) Start() {
	go c.run()
}

// Stop signals the sampling goroutine to exit and waits for it.
func (c *DashboardCollector) Stop() {
	close(c.stop)
	<-c.done
}

func (c *DashboardCollector) run() {
	defer close(c.done)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-c.stop:
			return
		case <-ticker.C:
			s := c.sample()
			c.mu.Lock()
			c.ring[c.head] = s
			c.head = (c.head + 1) % c.capacity
			if c.count < c.capacity {
				c.count++
			}
			c.mu.Unlock()
		}
	}
}

func (c *DashboardCollector) sample() RawSample {
	now := time.Now().UTC()
	gwSnap := c.registry.Gateway.Snapshot()
	chSnap := c.registry.Channel.Snapshot()
	msgSnap := c.registry.Message.Snapshot()

	var totalConns int64
	for _, v := range gwSnap.ActiveConnections {
		totalConns += v
	}

	var retryDepth int64
	for _, v := range msgSnap.CommittedDispatchQueueDepthByShard {
		retryDepth += v
	}

	families, _ := c.registry.Gather()

	return RawSample{
		At:                 now,
		SendCount:          sumCounterFamily(families, "wukongim_gateway_messages_received_total"),
		DeliverCount:       sumCounterFamily(families, "wukongim_gateway_messages_delivered_total"),
		Connections:        totalConns,
		SendTotalCount:     sumCounterFamily(families, "wukongim_message_append_total"),
		SendFailCount:      sumCounterFamilyWithLabel(families, "wukongim_message_append_total", "result", "error"),
		DeliverTotalCount:  sumCounterFamily(families, "wukongim_delivery_push_rpc_total"),
		DeliverFailCount:   sumCounterFamilyWithLabel(families, "wukongim_delivery_push_rpc_total", "result", "error"),
		ResolveRoutesCount: sumCounterFamily(families, "wukongim_delivery_resolve_routes_total"),
		ActiveChannels:     chSnap.ActiveChannels,
		RetryQueueDepth:    retryDepth,
		SendLatencyP99:     histogramP99(families, "wukongim_message_append_duration_seconds"),
		DeliveryLatencyP99: histogramP99(families, "wukongim_delivery_push_rpc_duration_seconds"),
	}
}

func sumCounterFamily(families []*dto.MetricFamily, name string) float64 {
	for _, f := range families {
		if f.GetName() == name {
			var total float64
			for _, m := range f.GetMetric() {
				total += m.GetCounter().GetValue()
			}
			return total
		}
	}
	return 0
}

func sumCounterFamilyWithLabel(families []*dto.MetricFamily, name, labelName, labelValue string) float64 {
	for _, f := range families {
		if f.GetName() == name {
			var total float64
			for _, m := range f.GetMetric() {
				for _, lp := range m.GetLabel() {
					if lp.GetName() == labelName && lp.GetValue() == labelValue {
						total += m.GetCounter().GetValue()
						break
					}
				}
			}
			return total
		}
	}
	return 0
}

func histogramP99(families []*dto.MetricFamily, name string) float64 {
	for _, f := range families {
		if f.GetName() == name {
			var totalCount uint64
			bucketMap := make(map[float64]uint64)
			var bounds []float64

			for _, m := range f.GetMetric() {
				h := m.GetHistogram()
				totalCount += h.GetSampleCount()
				for _, b := range h.GetBucket() {
					ub := b.GetUpperBound()
					if _, exists := bucketMap[ub]; !exists {
						bounds = append(bounds, ub)
					}
					bucketMap[ub] += b.GetCumulativeCount()
				}
			}
			if totalCount == 0 {
				return 0
			}
			sortFloat64s(bounds)
			rank := 0.99 * float64(totalCount)
			var prev float64
			for _, ub := range bounds {
				count := bucketMap[ub]
				if float64(count) >= rank {
					if math.IsInf(ub, 1) {
						return prev
					}
					return ub
				}
				prev = ub
			}
			return prev
		}
	}
	return 0
}

func sortFloat64s(a []float64) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j-1] > a[j]; j-- {
			a[j-1], a[j] = a[j], a[j-1]
		}
	}
}
