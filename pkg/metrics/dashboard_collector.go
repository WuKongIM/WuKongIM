package metrics

import (
	"errors"
	"math"
	"sync"
	"time"

	goruntimeregistry "github.com/WuKongIM/WuKongIM/pkg/goroutine"
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
	SendDelta               MetricSeries
	DeliverDelta            MetricSeries
	SendTotalDelta          MetricSeries
	SendFailDelta           MetricSeries
	DeliverTotalDelta       MetricSeries
	DeliverFailDelta        MetricSeries
	ResolveRoutesDelta      MetricSeries
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
	goruntimeregistry.SafeGo(nil, goruntimeregistry.TaskObservabilityDashboard, c.run)
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

// Query returns aggregated metric series for the given window and step.
func (c *DashboardCollector) Query(window, step time.Duration) (QueryResult, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.count < 2 {
		return QueryResult{}, ErrInsufficientData
	}

	now := time.Now().UTC()
	cutoff := now.Add(-window)
	bucketCount := int(window / step)
	stepSec := step.Seconds()

	// Collect samples within window
	samples := make([]RawSample, 0, c.count)
	for i := 0; i < c.count; i++ {
		idx := (c.head - c.count + i + c.capacity) % c.capacity
		s := c.ring[idx]
		if !s.At.Before(cutoff) {
			samples = append(samples, s)
		}
	}

	if len(samples) < 2 {
		return QueryResult{}, ErrInsufficientData
	}

	// Assign samples to buckets
	type bucket struct {
		samples []RawSample
	}
	buckets := make([]bucket, bucketCount)
	for _, s := range samples {
		idx := int(s.At.Sub(cutoff).Seconds() / stepSec)
		if idx >= bucketCount {
			idx = bucketCount - 1
		}
		if idx < 0 {
			idx = 0
		}
		buckets[idx].samples = append(buckets[idx].samples, s)
	}

	// Build series
	sendPerSec := make([]float64, bucketCount)
	deliverPerSec := make([]float64, bucketCount)
	connections := make([]float64, bucketCount)
	sendLatP99 := make([]float64, bucketCount)
	delivLatP99 := make([]float64, bucketCount)
	sendFailRate := make([]float64, bucketCount)
	delivFailRate := make([]float64, bucketCount)
	activeChans := make([]float64, bucketCount)
	retryQueue := make([]float64, bucketCount)
	fanOut := make([]float64, bucketCount)
	sendDeltas := make([]float64, bucketCount)
	deliverDeltas := make([]float64, bucketCount)
	sendTotalDeltas := make([]float64, bucketCount)
	sendFailDeltas := make([]float64, bucketCount)
	deliverTotalDeltas := make([]float64, bucketCount)
	deliverFailDeltas := make([]float64, bucketCount)
	resolveRoutesDeltas := make([]float64, bucketCount)

	for i, b := range buckets {
		if len(b.samples) == 0 {
			continue
		}
		if len(b.samples) == 1 {
			connections[i] = float64(b.samples[0].Connections)
			activeChans[i] = float64(b.samples[0].ActiveChannels)
			retryQueue[i] = float64(b.samples[0].RetryQueueDepth)
			sendLatP99[i] = b.samples[0].SendLatencyP99 * 1000
			delivLatP99[i] = b.samples[0].DeliveryLatencyP99 * 1000
			continue
		}
		first := b.samples[0]
		last := b.samples[len(b.samples)-1]

		sendDelta := last.SendCount - first.SendCount
		if sendDelta < 0 {
			sendDelta = 0
		}
		sendDeltas[i] = sendDelta
		sendPerSec[i] = math.Round(sendDelta / stepSec)

		delivDelta := last.DeliverCount - first.DeliverCount
		if delivDelta < 0 {
			delivDelta = 0
		}
		deliverDeltas[i] = delivDelta
		deliverPerSec[i] = math.Round(delivDelta / stepSec)

		connections[i] = float64(last.Connections)
		activeChans[i] = float64(last.ActiveChannels)
		retryQueue[i] = float64(last.RetryQueueDepth)

		// Latency: max p99 in bucket
		var maxSendLat, maxDelivLat float64
		for _, s := range b.samples {
			if s.SendLatencyP99 > maxSendLat {
				maxSendLat = s.SendLatencyP99
			}
			if s.DeliveryLatencyP99 > maxDelivLat {
				maxDelivLat = s.DeliveryLatencyP99
			}
		}
		sendLatP99[i] = math.Round(maxSendLat * 1000)
		delivLatP99[i] = math.Round(maxDelivLat * 1000)

		// Fail rates
		sendTotalDelta := last.SendTotalCount - first.SendTotalCount
		sendFailDelta := last.SendFailCount - first.SendFailCount
		if sendTotalDelta < 0 {
			sendTotalDelta = 0
		}
		if sendFailDelta < 0 {
			sendFailDelta = 0
		}
		sendTotalDeltas[i] = sendTotalDelta
		sendFailDeltas[i] = sendFailDelta
		if sendTotalDelta > 0 {
			sendFailRate[i] = math.Round(sendFailDelta/sendTotalDelta*1000) / 10
		}

		delivTotalDelta := last.DeliverTotalCount - first.DeliverTotalCount
		delivFailDelta := last.DeliverFailCount - first.DeliverFailCount
		if delivTotalDelta < 0 {
			delivTotalDelta = 0
		}
		if delivFailDelta < 0 {
			delivFailDelta = 0
		}
		deliverTotalDeltas[i] = delivTotalDelta
		deliverFailDeltas[i] = delivFailDelta
		if delivTotalDelta > 0 {
			delivFailRate[i] = math.Round(delivFailDelta/delivTotalDelta*1000) / 10
		}

		// Fan-out
		routesDelta := last.ResolveRoutesCount - first.ResolveRoutesCount
		if routesDelta < 0 {
			routesDelta = 0
		}
		resolveRoutesDeltas[i] = routesDelta
		if sendDelta > 0 {
			fanOut[i] = math.Round(routesDelta/sendDelta*10) / 10
		}
	}

	return QueryResult{
		GeneratedAt:             now,
		WindowSeconds:           int(window.Seconds()),
		StepSeconds:             int(step.Seconds()),
		Points:                  bucketCount,
		SendPerSec:              buildMetricSeries(sendPerSec),
		DeliverPerSec:           buildMetricSeries(deliverPerSec),
		Connections:             buildMetricSeries(connections),
		SendLatencyP99Ms:        buildMetricSeries(sendLatP99),
		DeliveryLatencyP99Ms:    buildMetricSeries(delivLatP99),
		SendFailRatePercent:     buildMetricSeries(sendFailRate),
		DeliveryFailRatePercent: buildMetricSeries(delivFailRate),
		ActiveChannels:          buildMetricSeries(activeChans),
		RetryQueueDepth:         buildMetricSeries(retryQueue),
		FanOutRate:              buildMetricSeries(fanOut),
		SendDelta:               buildMetricSeries(sendDeltas),
		DeliverDelta:            buildMetricSeries(deliverDeltas),
		SendTotalDelta:          buildMetricSeries(sendTotalDeltas),
		SendFailDelta:           buildMetricSeries(sendFailDeltas),
		DeliverTotalDelta:       buildMetricSeries(deliverTotalDeltas),
		DeliverFailDelta:        buildMetricSeries(deliverFailDeltas),
		ResolveRoutesDelta:      buildMetricSeries(resolveRoutesDeltas),
	}, nil
}

func buildMetricSeries(series []float64) MetricSeries {
	if len(series) == 0 {
		return MetricSeries{}
	}
	latest := series[len(series)-1]
	var peak, sum float64
	for _, v := range series {
		sum += v
		if v > peak {
			peak = v
		}
	}
	avg := math.Round(sum/float64(len(series))*10) / 10
	return MetricSeries{
		Latest: latest,
		Peak:   peak,
		Avg:    avg,
		Series: series,
	}
}
