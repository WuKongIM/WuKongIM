# Dashboard Metrics API Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement `GET /manager/dashboard/metrics` endpoint that returns 10 time-series metrics via an in-memory ring-buffer collector sampling existing Prometheus instrumentation every second.

**Architecture:** A `DashboardCollector` in `pkg/metrics/` samples counters/gauges/histograms every 1s into a 3600-slot ring buffer. The management usecase queries the collector and maps results to a response DTO. The HTTP handler parses window/step params and serializes JSON.

**Tech Stack:** Go, Prometheus client_golang, Gin HTTP framework, existing `pkg/metrics` registry.

---

## File Structure

```
pkg/metrics/
  dashboard_collector.go          — RawSample, DashboardCollector, ring buffer, sampling, Query
  dashboard_collector_test.go     — unit tests with real prometheus metrics

internal/usecase/management/
  dashboard_metrics.go            — DashboardMetricsResult DTO, GetDashboardMetrics method on App

internal/access/manager/
  dashboard_metrics.go            — DashboardMetricsResponse DTO, handleDashboardMetrics handler
  routes.go                       — register GET /manager/dashboard/metrics (1 line addition)

internal/usecase/management/
  app.go                          — add MetricsRegistry field to Options and App
```

---

## Task 1: DashboardCollector — data structures and constructor

**Files:**
- Create: `pkg/metrics/dashboard_collector.go`
- Create: `pkg/metrics/dashboard_collector_test.go`

- [ ] **Step 1: Write the failing test**

Create `pkg/metrics/dashboard_collector_test.go`:

```go
package metrics

import (
	"testing"
	"time"
)

func TestNewDashboardCollector(t *testing.T) {
	reg := newTestRegistry()
	c := NewDashboardCollector(reg)
	if c == nil {
		t.Fatal("expected non-nil collector")
	}
	if c.capacity != 3600 {
		t.Fatalf("expected capacity 3600, got %d", c.capacity)
	}
	if len(c.ring) != 3600 {
		t.Fatalf("expected ring length 3600, got %d", len(c.ring))
	}
}

func newTestRegistry() *Registry {
	return New(1, "test-node")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM && go test ./pkg/metrics/ -run TestNewDashboardCollector -v 2>&1 | tail -10`
Expected: FAIL — `NewDashboardCollector` undefined.

- [ ] **Step 3: Implement the data structures and constructor**

Create `pkg/metrics/dashboard_collector.go`:

```go
package metrics

import (
	"math"
	"sync"
	"time"

	dto "github.com/prometheus/client_model/go"
)

const dashboardCollectorCapacity = 3600

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
	GeneratedAt        time.Time
	WindowSeconds      int
	StepSeconds        int
	Points             int
	SendPerSec         MetricSeries
	DeliverPerSec      MetricSeries
	Connections        MetricSeries
	SendLatencyP99Ms   MetricSeries
	DeliveryLatencyP99Ms MetricSeries
	SendFailRatePercent  MetricSeries
	DeliveryFailRatePercent MetricSeries
	ActiveChannels     MetricSeries
	RetryQueueDepth    MetricSeries
	FanOutRate         MetricSeries
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM && go test ./pkg/metrics/ -run TestNewDashboardCollector -v 2>&1 | tail -5`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add pkg/metrics/dashboard_collector.go pkg/metrics/dashboard_collector_test.go
git commit -m "feat(metrics): add DashboardCollector data structures and constructor"
```

---

## Task 2: DashboardCollector — sampling logic

**Files:**
- Modify: `pkg/metrics/dashboard_collector.go`
- Modify: `pkg/metrics/dashboard_collector_test.go`

- [ ] **Step 1: Write the failing test**

Append to `pkg/metrics/dashboard_collector_test.go`:

```go
func TestDashboardCollectorSample(t *testing.T) {
	reg := newTestRegistry()
	c := NewDashboardCollector(reg)

	// Simulate some activity
	reg.Gateway.MessageReceived("tcp", 100)
	reg.Gateway.MessageReceived("tcp", 200)
	reg.Gateway.MessageDelivered("tcp", 150)
	reg.Gateway.ConnectionOpened("tcp")
	reg.Gateway.ConnectionOpened("tcp")
	reg.Channel.SetActiveChannels(42)
	reg.Message.SetCommittedDispatchQueueDepth("shard-0", 3)
	reg.Message.SetCommittedDispatchQueueDepth("shard-1", 5)
	reg.Message.ObserveAppend("local", "ok", 50*time.Millisecond)
	reg.Message.ObserveAppend("local", "error", 10*time.Millisecond)
	reg.Delivery.ObservePushRPC("node-2", "ok", 100*time.Millisecond, 10)
	reg.Delivery.ObservePushRPC("node-2", "error", 200*time.Millisecond, 0)
	reg.Delivery.ObserveResolve("group", "ok", 5*time.Millisecond, 1, 25)

	sample := c.sample()

	if sample.SendCount != 2 {
		t.Errorf("SendCount: got %v, want 2", sample.SendCount)
	}
	if sample.DeliverCount != 1 {
		t.Errorf("DeliverCount: got %v, want 1", sample.DeliverCount)
	}
	if sample.Connections != 2 {
		t.Errorf("Connections: got %v, want 2", sample.Connections)
	}
	if sample.ActiveChannels != 42 {
		t.Errorf("ActiveChannels: got %v, want 42", sample.ActiveChannels)
	}
	if sample.RetryQueueDepth != 8 {
		t.Errorf("RetryQueueDepth: got %v, want 8", sample.RetryQueueDepth)
	}
	if sample.SendTotalCount != 2 {
		t.Errorf("SendTotalCount: got %v, want 2", sample.SendTotalCount)
	}
	if sample.SendFailCount != 1 {
		t.Errorf("SendFailCount: got %v, want 1", sample.SendFailCount)
	}
	if sample.DeliverTotalCount != 2 {
		t.Errorf("DeliverTotalCount: got %v, want 2", sample.DeliverTotalCount)
	}
	if sample.DeliverFailCount != 1 {
		t.Errorf("DeliverFailCount: got %v, want 1", sample.DeliverFailCount)
	}
	if sample.ResolveRoutesCount != 25 {
		t.Errorf("ResolveRoutesCount: got %v, want 25", sample.ResolveRoutesCount)
	}
	if sample.SendLatencyP99 <= 0 {
		t.Errorf("SendLatencyP99: got %v, want > 0", sample.SendLatencyP99)
	}
	if sample.DeliveryLatencyP99 <= 0 {
		t.Errorf("DeliveryLatencyP99: got %v, want > 0", sample.DeliveryLatencyP99)
	}
}

func TestDashboardCollectorStartStop(t *testing.T) {
	reg := newTestRegistry()
	c := NewDashboardCollector(reg)
	c.Start()
	time.Sleep(1500 * time.Millisecond)
	c.Stop()

	c.mu.RLock()
	count := c.count
	c.mu.RUnlock()

	if count < 1 {
		t.Fatalf("expected at least 1 sample after 1.5s, got %d", count)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM && go test ./pkg/metrics/ -run "TestDashboardCollectorSample|TestDashboardCollectorStartStop" -v -timeout 10s 2>&1 | tail -10`
Expected: FAIL — `sample`, `Start`, `Stop` undefined.

- [ ] **Step 3: Implement sampling and lifecycle**

Add to `pkg/metrics/dashboard_collector.go`:

```go
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
		SendCount:          sumCounterVecFromFamilies(families, "wukongim_gateway_messages_received_total"),
		DeliverCount:       sumCounterVecFromFamilies(families, "wukongim_gateway_messages_delivered_total"),
		Connections:        totalConns,
		SendTotalCount:     sumCounterVecFromFamilies(families, "wukongim_message_append_total"),
		SendFailCount:      sumCounterVecWithLabel(families, "wukongim_message_append_total", "result", "error"),
		DeliverTotalCount:  sumCounterVecFromFamilies(families, "wukongim_delivery_push_rpc_total"),
		DeliverFailCount:   sumCounterVecWithLabel(families, "wukongim_delivery_push_rpc_total", "result", "error"),
		ResolveRoutesCount: sumCounterVecFromFamilies(families, "wukongim_delivery_resolve_routes_total"),
		ActiveChannels:     chSnap.ActiveChannels,
		RetryQueueDepth:    retryDepth,
		SendLatencyP99:     histogramP99FromFamilies(families, "wukongim_message_append_duration_seconds"),
		DeliveryLatencyP99: histogramP99FromFamilies(families, "wukongim_delivery_push_rpc_duration_seconds"),
	}
}

func sumCounterVecFromFamilies(families []*dto.MetricFamily, name string) float64 {
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

func sumCounterVecWithLabel(families []*dto.MetricFamily, name, labelName, labelValue string) float64 {
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

func histogramP99FromFamilies(families []*dto.MetricFamily, name string) float64 {
	for _, f := range families {
		if f.GetName() == name {
			var totalCount uint64
			var buckets []*dto.Bucket
			for _, m := range f.GetMetric() {
				h := m.GetHistogram()
				totalCount += h.GetSampleCount()
				for _, b := range h.GetBucket() {
					buckets = mergeBucket(buckets, b)
				}
			}
			if totalCount == 0 {
				return 0
			}
			return quantileFromBuckets(buckets, totalCount, 0.99)
		}
	}
	return 0
}

func mergeBucket(existing []*dto.Bucket, b *dto.Bucket) []*dto.Bucket {
	for _, e := range existing {
		if e.GetUpperBound() == b.GetUpperBound() {
			count := e.GetCumulativeCount() + b.GetCumulativeCount()
			e.CumulativeCount = &count
			return existing
		}
	}
	return append(existing, b)
}

func quantileFromBuckets(buckets []*dto.Bucket, totalCount uint64, q float64) float64 {
	if len(buckets) == 0 || totalCount == 0 {
		return 0
	}
	rank := q * float64(totalCount)
	var prev float64
	for _, b := range buckets {
		if float64(b.GetCumulativeCount()) >= rank {
			upper := b.GetUpperBound()
			if math.IsInf(upper, 1) {
				return prev
			}
			return upper
		}
		prev = b.GetUpperBound()
	}
	return prev
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM && go test ./pkg/metrics/ -run "TestDashboardCollectorSample|TestDashboardCollectorStartStop" -v -timeout 10s 2>&1 | tail -10`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add pkg/metrics/dashboard_collector.go pkg/metrics/dashboard_collector_test.go
git commit -m "feat(metrics): implement DashboardCollector sampling from Prometheus registry"
```

---

## Task 3: DashboardCollector — Query method

**Files:**
- Modify: `pkg/metrics/dashboard_collector.go`
- Modify: `pkg/metrics/dashboard_collector_test.go`

- [ ] **Step 1: Write the failing test**

Append to `pkg/metrics/dashboard_collector_test.go`:

```go
func TestDashboardCollectorQuery(t *testing.T) {
	reg := newTestRegistry()
	c := NewDashboardCollector(reg)

	// Manually insert 10 samples (simulating 10 seconds of data)
	now := time.Now().UTC()
	for i := 0; i < 10; i++ {
		c.ring[i] = RawSample{
			At:                 now.Add(time.Duration(i) * time.Second),
			SendCount:          float64(i * 100),
			DeliverCount:       float64(i * 90),
			Connections:        int64(800 + i*10),
			SendTotalCount:     float64(i * 100),
			SendFailCount:      float64(i),
			DeliverTotalCount:  float64(i * 90),
			DeliverFailCount:   float64(i),
			ResolveRoutesCount: float64(i * 500),
			ActiveChannels:     int64(300 + i*5),
			RetryQueueDepth:    int64(i),
			SendLatencyP99:     0.04 + float64(i)*0.001,
			DeliveryLatencyP99: 0.1 + float64(i)*0.005,
		}
	}
	c.head = 10
	c.count = 10

	result, err := c.Query(10*time.Second, 5*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Points != 2 {
		t.Fatalf("expected 2 points, got %d", result.Points)
	}
	if result.WindowSeconds != 10 {
		t.Fatalf("expected window 10, got %d", result.WindowSeconds)
	}
	if result.StepSeconds != 5 {
		t.Fatalf("expected step 5, got %d", result.StepSeconds)
	}
	if len(result.SendPerSec.Series) != 2 {
		t.Fatalf("expected 2 series points, got %d", len(result.SendPerSec.Series))
	}
	// First bucket: samples 0-4, rate = (400-0)/5 = 80 msg/s
	if result.SendPerSec.Series[0] != 80 {
		t.Errorf("SendPerSec[0]: got %v, want 80", result.SendPerSec.Series[0])
	}
	// Second bucket: samples 5-9, rate = (900-400)/5 = 100 msg/s
	if result.SendPerSec.Series[1] != 100 {
		t.Errorf("SendPerSec[1]: got %v, want 100", result.SendPerSec.Series[1])
	}
	// Connections: last value in each bucket
	if result.Connections.Series[0] != 840 {
		t.Errorf("Connections[0]: got %v, want 840", result.Connections.Series[0])
	}
	if result.Connections.Series[1] != 890 {
		t.Errorf("Connections[1]: got %v, want 890", result.Connections.Series[1])
	}
	// Latest = last element
	if result.Connections.Latest != result.Connections.Series[1] {
		t.Errorf("Connections.Latest: got %v, want %v", result.Connections.Latest, result.Connections.Series[1])
	}
	// Peak = max
	if result.Connections.Peak != 890 {
		t.Errorf("Connections.Peak: got %v, want 890", result.Connections.Peak)
	}
}

func TestDashboardCollectorQueryInsufficientData(t *testing.T) {
	reg := newTestRegistry()
	c := NewDashboardCollector(reg)
	// No samples
	_, err := c.Query(30*time.Second, 5*time.Second)
	if err == nil {
		t.Fatal("expected error for insufficient data")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM && go test ./pkg/metrics/ -run "TestDashboardCollectorQuery" -v -timeout 10s 2>&1 | tail -10`
Expected: FAIL — `Query` undefined.

- [ ] **Step 3: Implement Query**

Add to `pkg/metrics/dashboard_collector.go`:

```go
import "errors"

var ErrInsufficientData = errors.New("dashboard collector: insufficient data")

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

	for i, b := range buckets {
		if len(b.samples) < 2 {
			if len(b.samples) == 1 {
				connections[i] = float64(b.samples[0].Connections)
				activeChans[i] = float64(b.samples[0].ActiveChannels)
				retryQueue[i] = float64(b.samples[0].RetryQueueDepth)
				sendLatP99[i] = b.samples[0].SendLatencyP99 * 1000
				delivLatP99[i] = b.samples[0].DeliveryLatencyP99 * 1000
			}
			continue
		}
		first := b.samples[0]
		last := b.samples[len(b.samples)-1]

		sendDelta := last.SendCount - first.SendCount
		if sendDelta < 0 { sendDelta = 0 }
		sendPerSec[i] = math.Round(sendDelta / stepSec)

		delivDelta := last.DeliverCount - first.DeliverCount
		if delivDelta < 0 { delivDelta = 0 }
		deliverPerSec[i] = math.Round(delivDelta / stepSec)

		connections[i] = float64(last.Connections)
		activeChans[i] = float64(last.ActiveChannels)
		retryQueue[i] = float64(last.RetryQueueDepth)

		// Latency: max p99 in bucket
		var maxSendLat, maxDelivLat float64
		for _, s := range b.samples {
			if s.SendLatencyP99 > maxSendLat { maxSendLat = s.SendLatencyP99 }
			if s.DeliveryLatencyP99 > maxDelivLat { maxDelivLat = s.DeliveryLatencyP99 }
		}
		sendLatP99[i] = math.Round(maxSendLat * 1000)
		delivLatP99[i] = math.Round(maxDelivLat * 1000)

		// Fail rates
		sendTotalDelta := last.SendTotalCount - first.SendTotalCount
		sendFailDelta := last.SendFailCount - first.SendFailCount
		if sendTotalDelta > 0 {
			sendFailRate[i] = math.Round(sendFailDelta/sendTotalDelta*1000) / 10
		}

		delivTotalDelta := last.DeliverTotalCount - first.DeliverTotalCount
		delivFailDelta := last.DeliverFailCount - first.DeliverFailCount
		if delivTotalDelta > 0 {
			delivFailRate[i] = math.Round(delivFailDelta/delivTotalDelta*1000) / 10
		}

		// Fan-out
		routesDelta := last.ResolveRoutesCount - first.ResolveRoutesCount
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
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM && go test ./pkg/metrics/ -run "TestDashboardCollectorQuery" -v -timeout 10s 2>&1 | tail -10`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add pkg/metrics/dashboard_collector.go pkg/metrics/dashboard_collector_test.go
git commit -m "feat(metrics): implement DashboardCollector.Query with bucket aggregation"
```

---

## Task 4: Management usecase — GetDashboardMetrics

**Files:**
- Create: `internal/usecase/management/dashboard_metrics.go`
- Modify: `internal/usecase/management/app.go` (add MetricsRegistry to Options/App)

- [ ] **Step 1: Add MetricsRegistry to Options and App**

In `internal/usecase/management/app.go`, add to `Options` struct (after the `Now` field):

```go
	// MetricsRegistry provides the Prometheus metrics registry for dashboard collector.
	MetricsRegistry *metrics.Registry
```

Add to `App` struct:

```go
	dashCollector *metrics.DashboardCollector
```

In the `New` function, after the existing initialization, add:

```go
	var dashCollector *metrics.DashboardCollector
	if opts.MetricsRegistry != nil {
		dashCollector = metrics.NewDashboardCollector(opts.MetricsRegistry)
		dashCollector.Start()
	}
```

And set `dashCollector` on the returned `App`.

Add a `Stop()` method to `App`:

```go
func (a *App) Stop() {
	if a.dashCollector != nil {
		a.dashCollector.Stop()
	}
}
```

- [ ] **Step 2: Create dashboard_metrics.go**

Create `internal/usecase/management/dashboard_metrics.go`:

```go
package management

import (
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/metrics"
)

// DashboardMetricsResult is the usecase-layer DTO for dashboard metrics.
type DashboardMetricsResult struct {
	GeneratedAt time.Time
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
```

- [ ] **Step 3: Verify compilation**

Run: `cd /Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM && go build ./internal/usecase/management/ 2>&1 | head -10`
Expected: no errors (or only unrelated).

- [ ] **Step 4: Commit**

```bash
git add internal/usecase/management/dashboard_metrics.go internal/usecase/management/app.go
git commit -m "feat(management): add GetDashboardMetrics usecase method"
```

---

## Task 5: HTTP handler and route registration

**Files:**
- Create: `internal/access/manager/dashboard_metrics.go`
- Modify: `internal/access/manager/routes.go`
- Modify: `internal/access/manager/server.go` (add to Management interface)

- [ ] **Step 1: Add to Management interface**

In `internal/access/manager/server.go`, add to the `Management` interface:

```go
	// GetDashboardMetrics returns time-series dashboard metrics for the given window and step.
	GetDashboardMetrics(window, step time.Duration) (managementusecase.DashboardMetricsResult, error)
```

- [ ] **Step 2: Create the HTTP handler**

Create `internal/access/manager/dashboard_metrics.go`:

```go
package manager

import (
	"net/http"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/metrics"
	"github.com/gin-gonic/gin"
)

type DashboardMetricsResponse struct {
	GeneratedAt   time.Time                      `json:"generated_at"`
	WindowSeconds int                            `json:"window_seconds"`
	StepSeconds   int                            `json:"step_seconds"`
	Points        int                            `json:"points"`
	Metrics       DashboardMetricsResponseMap    `json:"metrics"`
}

type DashboardMetricsResponseMap struct {
	SendPerSec              MetricSeriesDTO `json:"send_per_sec"`
	DeliverPerSec           MetricSeriesDTO `json:"deliver_per_sec"`
	Connections             MetricSeriesDTO `json:"connections"`
	SendLatencyP99Ms        MetricSeriesDTO `json:"send_latency_p99_ms"`
	DeliveryLatencyP99Ms    MetricSeriesDTO `json:"delivery_latency_p99_ms"`
	SendFailRatePercent     MetricSeriesDTO `json:"send_fail_rate_percent"`
	DeliveryFailRatePercent MetricSeriesDTO `json:"delivery_fail_rate_percent"`
	ActiveChannels          MetricSeriesDTO `json:"active_channels"`
	RetryQueueDepth         MetricSeriesDTO `json:"retry_queue_depth"`
	FanOutRate              MetricSeriesDTO `json:"fan_out_rate"`
}

type MetricSeriesDTO struct {
	Latest float64   `json:"latest"`
	Peak   float64   `json:"peak"`
	Avg    float64   `json:"avg"`
	Series []float64 `json:"series"`
}

func metricSeriesDTO(ms metrics.MetricSeries) MetricSeriesDTO {
	series := ms.Series
	if series == nil {
		series = []float64{}
	}
	return MetricSeriesDTO{Latest: ms.Latest, Peak: ms.Peak, Avg: ms.Avg, Series: series}
}

func (s *Server) handleDashboardMetrics(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}

	windowStr := c.DefaultQuery("window", "30m")
	stepStr := c.DefaultQuery("step", "30s")

	window, err := time.ParseDuration(windowStr)
	if err != nil || window < time.Minute || window > time.Hour {
		jsonError(c, http.StatusBadRequest, "invalid_param", "window must be between 1m and 1h")
		return
	}

	step, err := time.ParseDuration(stepStr)
	if err != nil || step < 5*time.Second || step > 60*time.Second {
		jsonError(c, http.StatusBadRequest, "invalid_param", "step must be between 5s and 60s")
		return
	}

	if window%step != 0 {
		jsonError(c, http.StatusBadRequest, "invalid_param", "window must be evenly divisible by step")
		return
	}

	result, err := s.management.GetDashboardMetrics(window, step)
	if err != nil {
		if err == metrics.ErrInsufficientData {
			jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "dashboard metrics collector warming up")
			return
		}
		jsonError(c, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}

	c.JSON(http.StatusOK, DashboardMetricsResponse{
		GeneratedAt:   result.GeneratedAt,
		WindowSeconds: result.WindowSeconds,
		StepSeconds:   result.StepSeconds,
		Points:        result.Points,
		Metrics: DashboardMetricsResponseMap{
			SendPerSec:              metricSeriesDTO(result.Metrics.SendPerSec),
			DeliverPerSec:           metricSeriesDTO(result.Metrics.DeliverPerSec),
			Connections:             metricSeriesDTO(result.Metrics.Connections),
			SendLatencyP99Ms:        metricSeriesDTO(result.Metrics.SendLatencyP99Ms),
			DeliveryLatencyP99Ms:    metricSeriesDTO(result.Metrics.DeliveryLatencyP99Ms),
			SendFailRatePercent:     metricSeriesDTO(result.Metrics.SendFailRatePercent),
			DeliveryFailRatePercent: metricSeriesDTO(result.Metrics.DeliveryFailRatePercent),
			ActiveChannels:          metricSeriesDTO(result.Metrics.ActiveChannels),
			RetryQueueDepth:         metricSeriesDTO(result.Metrics.RetryQueueDepth),
			FanOutRate:              metricSeriesDTO(result.Metrics.FanOutRate),
		},
	})
}
```

- [ ] **Step 3: Register the route**

In `internal/access/manager/routes.go`, after the overview route registration (line ~124), add:

```go
	overview.GET("/dashboard/metrics", s.handleDashboardMetrics)
```

This reuses the `cluster.overview` read permission from the existing overview group.

- [ ] **Step 4: Verify compilation**

Run: `cd /Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM && go build ./... 2>&1 | head -20`
Expected: no errors.

- [ ] **Step 5: Commit**

```bash
git add internal/access/manager/dashboard_metrics.go internal/access/manager/routes.go internal/access/manager/server.go
git commit -m "feat(manager): add GET /manager/dashboard/metrics endpoint"
```

---

## Task 6: Bootstrap integration

**Files:**
- Modify: the file that constructs `management.Options` and passes it to `management.New()`

- [ ] **Step 1: Find the bootstrap location**

Run: `grep -rn "management.New\|management.Options" /Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/internal/ --include='*.go' | grep -v _test`

- [ ] **Step 2: Add MetricsRegistry to the Options construction**

At the location where `management.Options{...}` is constructed, add:

```go
	MetricsRegistry: metricsRegistry, // the existing *metrics.Registry instance
```

- [ ] **Step 3: Add App.Stop() call to shutdown**

Where the management app is shut down (or where the server stops), add:

```go
	mgmtApp.Stop()
```

- [ ] **Step 4: Verify compilation and run**

Run: `cd /Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM && go build ./... 2>&1 | head -10`
Expected: no errors.

- [ ] **Step 5: Commit**

```bash
git add <modified-bootstrap-file>
git commit -m "feat(app): wire DashboardCollector into management bootstrap"
```

---

## Task 7: Frontend integration — replace mock with real API

**Files:**
- Modify: `web/src/lib/manager-api.ts` (add `getDashboardMetrics` function)
- Modify: `web/src/pages/dashboard/use-dashboard-pulse.ts` (call real API, fallback to mock)

- [ ] **Step 1: Add API function**

In `web/src/lib/manager-api.ts`, add:

```typescript
export async function getDashboardMetrics(params?: { window?: string; step?: string }) {
  const searchParams = new URLSearchParams()
  if (params?.window) searchParams.set("window", params.window)
  if (params?.step) searchParams.set("step", params.step)
  const query = searchParams.toString()
  return managerGet(`/dashboard/metrics${query ? `?${query}` : ""}`)
}
```

- [ ] **Step 2: Update the hook to call real API with mock fallback**

Replace `web/src/pages/dashboard/use-dashboard-pulse.ts`:

```typescript
import { useCallback, useEffect, useMemo, useState } from "react"

import { getDashboardMetrics } from "@/lib/manager-api"

export type PulseSeries = {
  latest: number
  peak: number
  avg: number
  series: number[]
}

export type PulseData = {
  sendPerSec: PulseSeries
  deliverPerSec: PulseSeries
  connections: PulseSeries
  sendLatencyP99: PulseSeries
  deliveryLatencyP99: PulseSeries
  sendFailRate: PulseSeries
  deliveryFailRate: PulseSeries
  activeChannels: PulseSeries
  retryQueueDepth: PulseSeries
  fanOutRate: PulseSeries
  mocked: boolean
}

// ... keep generatePulseData for fallback ...

export function useDashboardPulse(generatedAt: string | null): PulseData | null {
  const [data, setData] = useState<PulseData | null>(null)
  const fallback = useMemo(() => generatedAt ? { ...generatePulseData(generatedAt), mocked: true } : null, [generatedAt])

  const load = useCallback(async () => {
    try {
      const resp = await getDashboardMetrics({ window: "30m", step: "30s" })
      setData({
        sendPerSec: resp.metrics.send_per_sec,
        deliverPerSec: resp.metrics.deliver_per_sec,
        connections: resp.metrics.connections,
        sendLatencyP99: resp.metrics.send_latency_p99_ms,
        deliveryLatencyP99: resp.metrics.delivery_latency_p99_ms,
        sendFailRate: resp.metrics.send_fail_rate_percent,
        deliveryFailRate: resp.metrics.delivery_fail_rate_percent,
        activeChannels: resp.metrics.active_channels,
        retryQueueDepth: resp.metrics.retry_queue_depth,
        fanOutRate: resp.metrics.fan_out_rate,
        mocked: false,
      })
    } catch {
      // API not available yet — use mock fallback
      setData(fallback)
    }
  }, [fallback])

  useEffect(() => {
    if (generatedAt) void load()
  }, [generatedAt, load])

  return data ?? fallback
}
```

- [ ] **Step 3: Update PulseTile `mocked` prop to use `pulse.mocked`**

In `page.tsx`, change all `mocked` props on PulseTile from `mocked` (always true) to `mocked={pulse.mocked}`.

- [ ] **Step 4: Run tests**

Run: `cd /Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/web && npx vitest run src/pages/dashboard/ 2>&1 | tail -5`
Expected: all pass (tests mock the API so the hook falls back to mock data).

- [ ] **Step 5: Commit**

```bash
git add web/src/lib/manager-api.ts web/src/pages/dashboard/use-dashboard-pulse.ts web/src/pages/dashboard/page.tsx
git commit -m "feat(web): integrate dashboard metrics API with mock fallback"
```
