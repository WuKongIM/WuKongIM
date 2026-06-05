# Runtime Pressure Metrics Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Expose unified Prometheus/Grafana pressure metrics for bounded pools and queues in `pkg/gateway`, `pkg/channelv2`, `pkg/clusterv2`, `pkg/slot`, and `pkg/transportv2`, while keeping `pkg/transport` excluded.

**Architecture:** Add a unified `RuntimePressureMetrics` family in `pkg/metrics`, then keep each runtime package on local observer/event interfaces. `internalv2/app` adapts package-local observations into Prometheus metrics and `clusterv2` only passes observers into default runtimes.

**Tech Stack:** Go, Prometheus `client_golang`, existing package observers, Grafana JSON dashboards, `go test`.

---

## Scope Check

The spec covers multiple runtimes, but they are tied together by one shared metric family and one composition-root adapter. Keep this as one phased plan so metric names and labels stabilize in Task 1, then each runtime task can be implemented and verified independently.

Do not instrument `pkg/transport`.

## File Structure

- Create: `pkg/metrics/runtime_pressure.go`
  - Owns the new metric family, nil-safe methods, result normalization, and negative duration clamping.
- Modify: `pkg/metrics/registry.go`
  - Adds `RuntimePressure *RuntimePressureMetrics` to `Registry`.
- Modify: `pkg/metrics/registry_test.go`
  - Locks metric names, labels, nil safety, and normalization.
- Modify: `pkg/channelv2/worker/pool.go`
  - Adds worker queue capacity, workers, admission, queue wait, and execution observations without changing scheduling semantics.
- Modify: `pkg/channelv2/worker/pool_test.go`
  - Verifies worker pool pressure observations.
- Modify: `pkg/channelv2/reactor/metrics.go`
  - Adds optional mailbox capacity/admission and append queue observer interfaces.
- Modify: `pkg/channelv2/reactor/mailbox.go`
  - Adds capacity helper methods.
- Modify: `pkg/channelv2/reactor/reactor.go`
  - Emits mailbox admission and capacity observations around existing submit paths.
- Modify: `pkg/channelv2/reactor/append_queue.go`
  - Emits aggregate append queue pressure without channel labels.
- Modify: `pkg/channelv2/reactor/mailbox_test.go`, `pkg/channelv2/reactor/append_queue_test.go`
  - Verifies ChannelV2 mailbox and append queue observations.
- Modify: `pkg/gateway/types/observer.go`
  - Adds optional auth queue and transport pressure observer interfaces/events.
- Modify: `pkg/gateway/core/server.go`
  - Emits async auth and SEND admission/capacity/wait observations.
- Modify: `pkg/gateway/core/async_auth_test.go`, `pkg/gateway/core/async_dispatch_test.go`
  - Verifies gateway queue pressure observations.
- Modify: `pkg/gateway/transport/listener.go`, `pkg/gateway/transport/gnet/actor.go`, `pkg/gateway/transport/gnet/conn.go`, `pkg/gateway/transport/gnet/group.go`
  - Threads optional gateway transport observer and emits actor/inbound/outbound aggregate pressure.
- Modify: `pkg/gateway/transport/gnet/actor_test.go`, `pkg/gateway/transport/gnet/conn_backpressure_test.go`
  - Verifies gnet pressure observations.
- Modify: `pkg/slot/multiraft/types.go`
  - Adds package-local scheduler observer interfaces/events to `Options`.
- Modify: `pkg/slot/multiraft/scheduler.go`, `pkg/slot/multiraft/runtime.go`
  - Emits scheduler queue, admission, workers, inflight, and process duration observations.
- Modify: `pkg/slot/multiraft/scheduler_test.go`, `pkg/slot/multiraft/step_test.go`
  - Verifies Slot scheduler observations.
- Modify: `pkg/clusterv2/config.go`, `pkg/clusterv2/default_slots.go`, `pkg/clusterv2/FLOW.md`
  - Passes Slot observer into default Slot runtime and documents the pass-through.
- Modify: `pkg/transportv2/internal/core/types.go`
  - Extends `Event` with queue count/capacity fields and keeps event labels low-cardinality.
- Modify: `pkg/transportv2/internal/sched/scheduler.go`
  - Adds observer support for scheduler queue state, admission, and queue wait.
- Modify: `pkg/transportv2/internal/rpc/service.go`
  - Adds observer support for service queue state, admission, inflight, and task duration.
- Modify: `pkg/transportv2/internal/conn/conn.go`, `pkg/transportv2/internal/peer/manager.go`, `pkg/transportv2/client.go`, `pkg/transportv2/server.go`
  - Threads observer and emits pending RPC / peer pool events.
- Modify: `pkg/transportv2/internal/sched/scheduler_test.go`, `pkg/transportv2/internal/rpc/service_test.go`, `pkg/transportv2/internal/conn/conn_test.go`, `pkg/transportv2/internal/peer/manager_test.go`
  - Verifies TransportV2 observations.
- Modify: `internalv2/app/observability.go`, `internalv2/app/observability_test.go`
  - Maps package events to `RuntimePressureMetrics`, combines optional observers, and normalizes labels.
- Modify: `docker/observability/grafana/dashboards/wukongim-runtime-storage.json`
  - Adds Runtime Pressure panels using the new metric names.
- Modify: `docker/observability/grafana/dashboard_assets_test.go`
  - Existing coverage should pass because new metric names are referenced.

### Task 1: Unified Metrics Family

**Files:**
- Create: `pkg/metrics/runtime_pressure.go`
- Modify: `pkg/metrics/registry.go`
- Modify: `pkg/metrics/registry_test.go`

- [ ] **Step 1: Write the failing registry test**

Append this test to `pkg/metrics/registry_test.go`:

```go
func TestRuntimePressureMetricsTrackPoolsQueuesAndDurations(t *testing.T) {
	reg := New(9, "node-9")

	reg.RuntimePressure.SetPoolWorkers("channelv2", "store_append", 4)
	reg.RuntimePressure.SetPoolInflight("channelv2", "store_append", 2)
	reg.RuntimePressure.SetQueue("channelv2", "store_append", "worker", "none", RuntimePressureQueueObservation{
		Depth:         7,
		Capacity:      128,
		Bytes:         4096,
		BytesCapacity: 1 << 20,
	})
	reg.RuntimePressure.ObserveAdmission("channelv2", "store_append", "worker", "none", "ok")
	reg.RuntimePressure.ObserveAdmission("channelv2", "store_append", "worker", "none", "raw uid leaked")
	reg.RuntimePressure.ObserveQueueWait("channelv2", "store_append", "worker", "none", "ok", 2*time.Millisecond)
	reg.RuntimePressure.ObserveTaskDuration("channelv2", "store_append", "store_append", "ok", 3*time.Millisecond)
	reg.RuntimePressure.ObserveTaskDuration("channelv2", "store_append", "store_append", "ok", -time.Second)

	families, err := reg.Gather()
	require.NoError(t, err)

	workers := requireMetricFamily(t, families, "wukongim_runtime_pool_workers")
	require.Equal(t, float64(4), findMetricByLabels(t, workers, map[string]string{
		"node_id": "9", "node_name": "node-9", "component": "channelv2", "pool": "store_append",
	}).GetGauge().GetValue())

	inflight := requireMetricFamily(t, families, "wukongim_runtime_pool_inflight")
	require.Equal(t, float64(2), findMetricByLabels(t, inflight, map[string]string{
		"node_id": "9", "node_name": "node-9", "component": "channelv2", "pool": "store_append",
	}).GetGauge().GetValue())

	depth := requireMetricFamily(t, families, "wukongim_runtime_pool_queue_depth")
	require.Equal(t, float64(7), findMetricByLabels(t, depth, map[string]string{
		"node_id": "9", "node_name": "node-9", "component": "channelv2", "pool": "store_append", "queue": "worker", "priority": "none",
	}).GetGauge().GetValue())

	capacity := requireMetricFamily(t, families, "wukongim_runtime_pool_queue_capacity")
	require.Equal(t, float64(128), findMetricByLabels(t, capacity, map[string]string{
		"node_id": "9", "node_name": "node-9", "component": "channelv2", "pool": "store_append", "queue": "worker", "priority": "none",
	}).GetGauge().GetValue())

	bytes := requireMetricFamily(t, families, "wukongim_runtime_pool_queue_bytes")
	require.Equal(t, float64(4096), findMetricByLabels(t, bytes, map[string]string{
		"node_id": "9", "node_name": "node-9", "component": "channelv2", "pool": "store_append", "queue": "worker", "priority": "none",
	}).GetGauge().GetValue())

	byteCapacity := requireMetricFamily(t, families, "wukongim_runtime_pool_queue_bytes_capacity")
	require.Equal(t, float64(1<<20), findMetricByLabels(t, byteCapacity, map[string]string{
		"node_id": "9", "node_name": "node-9", "component": "channelv2", "pool": "store_append", "queue": "worker", "priority": "none",
	}).GetGauge().GetValue())

	admissions := requireMetricFamily(t, families, "wukongim_runtime_pool_admission_total")
	require.Equal(t, float64(1), findMetricByLabels(t, admissions, map[string]string{
		"node_id": "9", "node_name": "node-9", "component": "channelv2", "pool": "store_append", "queue": "worker", "priority": "none", "result": "ok",
	}).GetCounter().GetValue())
	require.Equal(t, float64(1), findMetricByLabels(t, admissions, map[string]string{
		"node_id": "9", "node_name": "node-9", "component": "channelv2", "pool": "store_append", "queue": "worker", "priority": "none", "result": "other",
	}).GetCounter().GetValue())

	requireMetricFamily(t, families, "wukongim_runtime_pool_wait_duration_seconds")
	requireMetricFamily(t, families, "wukongim_runtime_pool_task_duration_seconds")
}

func TestRuntimePressureMetricsNilSafe(t *testing.T) {
	var m *RuntimePressureMetrics
	m.SetPoolWorkers("gateway", "async_auth", 1)
	m.SetPoolInflight("gateway", "async_auth", 1)
	m.SetQueue("gateway", "async_auth", "auth", "none", RuntimePressureQueueObservation{Depth: 1})
	m.ObserveAdmission("gateway", "async_auth", "auth", "none", "ok")
	m.ObserveQueueWait("gateway", "async_auth", "auth", "none", "ok", time.Millisecond)
	m.ObserveTaskDuration("gateway", "async_auth", "auth", "ok", time.Millisecond)
}
```

- [ ] **Step 2: Run the failing test**

Run:

```bash
GOWORK=off go test ./pkg/metrics -run 'TestRuntimePressureMetrics' -count=1
```

Expected: FAIL because `RuntimePressureMetrics`, `Registry.RuntimePressure`, and `RuntimePressureQueueObservation` do not exist.

- [ ] **Step 3: Implement `runtime_pressure.go`**

Create `pkg/metrics/runtime_pressure.go` with this structure:

```go
package metrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

var runtimePressureDurationBuckets = []float64{0.0005, 0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5}

// RuntimePressureQueueObservation describes one bounded queue state sample.
type RuntimePressureQueueObservation struct {
	// Depth is the current queued item count.
	Depth int
	// Capacity is the configured queued item capacity.
	Capacity int
	// Bytes is the current queued payload byte count.
	Bytes int64
	// BytesCapacity is the configured queued payload byte capacity.
	BytesCapacity int64
}

// RuntimePressureMetrics exposes low-cardinality pool and queue pressure signals.
type RuntimePressureMetrics struct {
	poolWorkers        *prometheus.GaugeVec
	poolInflight       *prometheus.GaugeVec
	queueDepth         *prometheus.GaugeVec
	queueCapacity      *prometheus.GaugeVec
	queueBytes         *prometheus.GaugeVec
	queueBytesCapacity *prometheus.GaugeVec
	admissionTotal     *prometheus.CounterVec
	waitDuration       *prometheus.HistogramVec
	taskDuration       *prometheus.HistogramVec
}

func newRuntimePressureMetrics(registry prometheus.Registerer, labels prometheus.Labels) *RuntimePressureMetrics {
	m := &RuntimePressureMetrics{
		poolWorkers: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_runtime_pool_workers",
			Help:        "Configured worker count for bounded runtime pools.",
			ConstLabels: labels,
		}, []string{"component", "pool"}),
		poolInflight: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_runtime_pool_inflight",
			Help:        "Currently running work in bounded runtime pools.",
			ConstLabels: labels,
		}, []string{"component", "pool"}),
		queueDepth: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_runtime_pool_queue_depth",
			Help:        "Queued item count for bounded runtime queues.",
			ConstLabels: labels,
		}, []string{"component", "pool", "queue", "priority"}),
		queueCapacity: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_runtime_pool_queue_capacity",
			Help:        "Configured queued item capacity for bounded runtime queues.",
			ConstLabels: labels,
		}, []string{"component", "pool", "queue", "priority"}),
		queueBytes: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_runtime_pool_queue_bytes",
			Help:        "Queued payload bytes for bounded runtime queues.",
			ConstLabels: labels,
		}, []string{"component", "pool", "queue", "priority"}),
		queueBytesCapacity: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_runtime_pool_queue_bytes_capacity",
			Help:        "Configured queued payload byte capacity for bounded runtime queues.",
			ConstLabels: labels,
		}, []string{"component", "pool", "queue", "priority"}),
		admissionTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_runtime_pool_admission_total",
			Help:        "Total bounded runtime queue admission outcomes.",
			ConstLabels: labels,
		}, []string{"component", "pool", "queue", "priority", "result"}),
		waitDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_runtime_pool_wait_duration_seconds",
			Help:        "Time accepted work waits before execution or write.",
			ConstLabels: labels,
			Buckets:     runtimePressureDurationBuckets,
		}, []string{"component", "pool", "queue", "priority", "result"}),
		taskDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_runtime_pool_task_duration_seconds",
			Help:        "Execution duration for bounded runtime pool work.",
			ConstLabels: labels,
			Buckets:     runtimePressureDurationBuckets,
		}, []string{"component", "pool", "task", "result"}),
	}

	registry.MustRegister(
		m.poolWorkers,
		m.poolInflight,
		m.queueDepth,
		m.queueCapacity,
		m.queueBytes,
		m.queueBytesCapacity,
		m.admissionTotal,
		m.waitDuration,
		m.taskDuration,
	)
	return m
}

func (m *RuntimePressureMetrics) SetPoolWorkers(component, pool string, workers int) {
	if m == nil {
		return
	}
	m.poolWorkers.WithLabelValues(runtimePressureLabel(component, "unknown"), runtimePressureLabel(pool, "unknown")).Set(float64(clampInt(workers)))
}

func (m *RuntimePressureMetrics) SetPoolInflight(component, pool string, inflight int) {
	if m == nil {
		return
	}
	m.poolInflight.WithLabelValues(runtimePressureLabel(component, "unknown"), runtimePressureLabel(pool, "unknown")).Set(float64(clampInt(inflight)))
}

func (m *RuntimePressureMetrics) SetQueue(component, pool, queue, priority string, obs RuntimePressureQueueObservation) {
	if m == nil {
		return
	}
	m.SetQueueDepth(component, pool, queue, priority, obs.Depth)
	m.SetQueueCapacity(component, pool, queue, priority, obs.Capacity)
	m.SetQueueBytes(component, pool, queue, priority, obs.Bytes)
	m.SetQueueBytesCapacity(component, pool, queue, priority, obs.BytesCapacity)
}

func (m *RuntimePressureMetrics) SetQueueDepth(component, pool, queue, priority string, depth int) {
	if m == nil {
		return
	}
	m.queueDepth.WithLabelValues(runtimePressureQueueLabels(component, pool, queue, priority)...).Set(float64(clampInt(depth)))
}

func (m *RuntimePressureMetrics) SetQueueCapacity(component, pool, queue, priority string, capacity int) {
	if m == nil {
		return
	}
	m.queueCapacity.WithLabelValues(runtimePressureQueueLabels(component, pool, queue, priority)...).Set(float64(clampInt(capacity)))
}

func (m *RuntimePressureMetrics) SetQueueBytes(component, pool, queue, priority string, bytes int64) {
	if m == nil {
		return
	}
	m.queueBytes.WithLabelValues(runtimePressureQueueLabels(component, pool, queue, priority)...).Set(float64(clampInt64(bytes)))
}

func (m *RuntimePressureMetrics) SetQueueBytesCapacity(component, pool, queue, priority string, capacity int64) {
	if m == nil {
		return
	}
	m.queueBytesCapacity.WithLabelValues(runtimePressureQueueLabels(component, pool, queue, priority)...).Set(float64(clampInt64(capacity)))
}

func (m *RuntimePressureMetrics) ObserveAdmission(component, pool, queue, priority, result string) {
	if m == nil {
		return
	}
	m.admissionTotal.WithLabelValues(
		runtimePressureLabel(component, "unknown"),
		runtimePressureLabel(pool, "unknown"),
		runtimePressureLabel(queue, "unknown"),
		runtimePressureLabel(priority, "none"),
		NormalizeRuntimePressureResult(result),
	).Inc()
}

func (m *RuntimePressureMetrics) ObserveQueueWait(component, pool, queue, priority, result string, d time.Duration) {
	if m == nil {
		return
	}
	m.waitDuration.WithLabelValues(
		runtimePressureLabel(component, "unknown"),
		runtimePressureLabel(pool, "unknown"),
		runtimePressureLabel(queue, "unknown"),
		runtimePressureLabel(priority, "none"),
		NormalizeRuntimePressureResult(result),
	).Observe(clampDurationSeconds(d))
}

func (m *RuntimePressureMetrics) ObserveTaskDuration(component, pool, task, result string, d time.Duration) {
	if m == nil {
		return
	}
	m.taskDuration.WithLabelValues(
		runtimePressureLabel(component, "unknown"),
		runtimePressureLabel(pool, "unknown"),
		runtimePressureLabel(task, "unknown"),
		NormalizeRuntimePressureResult(result),
	).Observe(clampDurationSeconds(d))
}

// NormalizeRuntimePressureResult keeps the result label bounded.
func NormalizeRuntimePressureResult(result string) string {
	switch result {
	case "ok", "full", "busy", "closed", "canceled", "timeout", "too_large", "invalid", "dropped", "coalesced", "stopped", "err":
		return result
	default:
		return "other"
	}
}

func runtimePressureLabel(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func runtimePressureQueueLabels(component, pool, queue, priority string) []string {
	return []string{
		runtimePressureLabel(component, "unknown"),
		runtimePressureLabel(pool, "unknown"),
		runtimePressureLabel(queue, "unknown"),
		runtimePressureLabel(priority, "none"),
	}
}

func clampDurationSeconds(d time.Duration) float64 {
	if d < 0 {
		return 0
	}
	return d.Seconds()
}

func clampInt(v int) int {
	if v < 0 {
		return 0
	}
	return v
}

func clampInt64(v int64) int64 {
	if v < 0 {
		return 0
	}
	return v
}
```

- [ ] **Step 4: Wire the registry**

In `pkg/metrics/registry.go`, add a field:

```go
	RuntimePressure *RuntimePressureMetrics
```

Initialize it in `New`:

```go
RuntimePressure: newRuntimePressureMetrics(registry, labels),
```

- [ ] **Step 5: Run the metrics tests**

Run:

```bash
GOWORK=off go test ./pkg/metrics -run 'TestRuntimePressureMetrics|TestGatewayMetricsTrackConnectionAndTraffic' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add pkg/metrics/runtime_pressure.go pkg/metrics/registry.go pkg/metrics/registry_test.go
git commit -m "feat(metrics): add runtime pressure metrics"
```

### Task 2: ChannelV2 Worker Pool Pressure

**Files:**
- Modify: `pkg/channelv2/worker/pool.go`
- Modify: `pkg/channelv2/worker/pool_test.go`

- [ ] **Step 1: Write failing worker observer tests**

Append to `pkg/channelv2/worker/pool_test.go`:

```go
func TestPoolReportsCapacityWorkersAdmissionWaitAndTaskDuration(t *testing.T) {
	obs := &recordingPoolPressureObserver{}
	sink := &captureSink{ch: make(chan Result, 1)}
	pool, err := NewPool(PoolConfig{Name: "store_append", Workers: 1, QueueSize: 2}, Deps{}, sink)
	if err != nil {
		t.Fatalf("NewPool() error = %v", err)
	}
	defer pool.Close()
	pool.SetQueueObserver(obs)

	err = pool.Submit(context.Background(), Task{
		Kind: TaskFunc,
		RunFunc: func(context.Context) Result {
			time.Sleep(time.Millisecond)
			return Result{Kind: TaskFunc}
		},
	})
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	select {
	case <-sink.ch:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for worker result")
	}

	if got := obs.workers["store_append"]; got != 1 {
		t.Fatalf("workers = %d, want 1", got)
	}
	if got := obs.capacity["store_append"]; got != 2 {
		t.Fatalf("capacity = %d, want 2", got)
	}
	if got := obs.admissions["store_append:ok"]; got != 1 {
		t.Fatalf("ok admissions = %d, want 1", got)
	}
	if got := obs.waits["store_append:func"]; got == 0 {
		t.Fatal("queue wait was not observed")
	}
	if got := obs.tasks["store_append:func:ok"]; got == 0 {
		t.Fatal("task duration was not observed")
	}
}

func TestPoolReportsFullClosedAndCanceledAdmission(t *testing.T) {
	obs := &recordingPoolPressureObserver{}
	block := make(chan struct{})
	sink := &captureSink{ch: make(chan Result, 3)}
	pool, err := NewPool(PoolConfig{Name: "rpc", Workers: 1, QueueSize: 1}, Deps{}, sink)
	if err != nil {
		t.Fatalf("NewPool() error = %v", err)
	}
	pool.SetQueueObserver(obs)
	defer pool.Close()

	mustSubmit := func() {
		if err := pool.Submit(context.Background(), Task{
			Kind: TaskFunc,
			RunFunc: func(context.Context) Result {
				<-block
				return Result{Kind: TaskFunc}
			},
		}); err != nil {
			t.Fatalf("Submit() error = %v", err)
		}
	}
	mustSubmit()
	mustSubmit()
	if err := pool.Submit(context.Background(), Task{Kind: TaskFunc, RunFunc: func(context.Context) Result { return Result{} }}); !errors.Is(err, ch.ErrBackpressured) {
		t.Fatalf("Submit() error = %v, want %v", err, ch.ErrBackpressured)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_ = pool.Submit(ctx, Task{Kind: TaskFunc, RunFunc: func(context.Context) Result { return Result{} }})
	close(block)
	_ = pool.Close()
	_ = pool.Submit(context.Background(), Task{Kind: TaskFunc, RunFunc: func(context.Context) Result { return Result{} }})

	if got := obs.admissions["rpc:full"]; got != 1 {
		t.Fatalf("full admissions = %d, want 1", got)
	}
	if got := obs.admissions["rpc:canceled"]; got != 1 {
		t.Fatalf("canceled admissions = %d, want 1", got)
	}
	if got := obs.admissions["rpc:closed"]; got != 1 {
		t.Fatalf("closed admissions = %d, want 1", got)
	}
}

type recordingPoolPressureObserver struct {
	mu         sync.Mutex
	workers    map[string]int
	capacity   map[string]int
	admissions map[string]int
	waits      map[string]time.Duration
	tasks      map[string]time.Duration
}

func (o *recordingPoolPressureObserver) ensure() {
	if o.workers == nil {
		o.workers = make(map[string]int)
		o.capacity = make(map[string]int)
		o.admissions = make(map[string]int)
		o.waits = make(map[string]time.Duration)
		o.tasks = make(map[string]time.Duration)
	}
}

func (o *recordingPoolPressureObserver) SetWorkerQueueDepth(string, int) {}

func (o *recordingPoolPressureObserver) SetWorkerQueueCapacity(pool string, capacity int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.ensure()
	o.capacity[pool] = capacity
}

func (o *recordingPoolPressureObserver) SetWorkerWorkers(pool string, workers int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.ensure()
	o.workers[pool] = workers
}

func (o *recordingPoolPressureObserver) ObserveWorkerAdmission(pool string, result string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.ensure()
	o.admissions[pool+":"+result]++
}

func (o *recordingPoolPressureObserver) ObserveWorkerWait(pool string, kind TaskKind, d time.Duration) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.ensure()
	o.waits[pool+":"+taskKindTestLabel(kind)] += d
}

func (o *recordingPoolPressureObserver) ObserveWorkerTask(pool string, kind TaskKind, err error, d time.Duration) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.ensure()
	result := "ok"
	if err != nil {
		result = "err"
	}
	o.tasks[pool+":"+taskKindTestLabel(kind)+":"+result] += d
}

func taskKindTestLabel(kind TaskKind) string {
	if kind == TaskFunc {
		return "func"
	}
	return "other"
}
```

Ensure the import block includes:

```go
import (
	"errors"
	"sync"
)
```

- [ ] **Step 2: Run the failing worker tests**

Run:

```bash
GOWORK=off go test ./pkg/channelv2/worker -run 'TestPoolReports' -count=1
```

Expected: FAIL because the observer interfaces do not exist and `captureSink` may need a buffered channel field adjustment.

- [ ] **Step 3: Add worker observer interfaces and queued task timestamps**

In `pkg/channelv2/worker/pool.go`, add these interfaces below `InflightObserver`:

```go
// QueueCapacityObserver receives configured worker queue capacity and worker count.
type QueueCapacityObserver interface {
	SetWorkerQueueCapacity(pool string, capacity int)
	SetWorkerWorkers(pool string, workers int)
}

// AdmissionObserver receives bounded worker enqueue outcomes.
type AdmissionObserver interface {
	ObserveWorkerAdmission(pool string, result string)
}

// WaitObserver receives queue wait time for accepted worker tasks.
type WaitObserver interface {
	ObserveWorkerWait(pool string, kind TaskKind, d time.Duration)
}

// TaskObserver receives execution duration for worker tasks with pool context.
type TaskObserver interface {
	ObserveWorkerTask(pool string, kind TaskKind, err error, d time.Duration)
}

type queuedTask struct {
	task       Task
	enqueuedAt time.Time
}
```

Change `Pool.queue` from `chan Task` to:

```go
queue chan queuedTask
```

Initialize it with:

```go
queue: make(chan queuedTask, cfg.QueueSize),
```

In `SetQueueObserver`, call:

```go
p.observeQueueCapacity()
p.observeWorkers()
p.observeQueueDepth()
```

Update `Submit` to classify outcomes:

```go
func (p *Pool) Submit(ctx context.Context, task Task) error {
	if p == nil {
		return ch.ErrClosed
	}
	result := "ok"
	defer func() {
		p.observeAdmission(result)
		p.observeQueueDepth()
	}()
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-p.stop:
		result = "closed"
		return ch.ErrClosed
	default:
	}
	queued := queuedTask{task: task, enqueuedAt: time.Now()}
	select {
	case p.queue <- queued:
		return nil
	case <-p.stop:
		result = "closed"
		return ch.ErrClosed
	case <-ctx.Done():
		result = workerAdmissionResult(ctx.Err())
		return ctx.Err()
	default:
		result = "full"
		return ch.ErrBackpressured
	}
}
```

Update `run` to use `queuedTask`:

```go
case queued := <-p.queue:
	p.observeQueueDepth()
	p.observeWait(queued.task.Kind, time.Since(queued.enqueuedAt))
	running := int(p.inflight.Add(1))
	p.observeInflight(running)
	started := time.Now()
	result := queued.task.Run(p.ctx, p.deps)
	result.Duration = time.Since(started)
	p.observeTask(result.Kind, result.Err, result.Duration)
	running = int(p.inflight.Add(-1))
	p.observeInflight(running)
	p.sink.Complete(result)
```

Add helper methods:

```go
func (p *Pool) observeQueueCapacity() {
	if obs, ok := p.obs.(QueueCapacityObserver); ok {
		obs.SetWorkerQueueCapacity(p.cfg.Name, cap(p.queue))
	}
}

func (p *Pool) observeWorkers() {
	if obs, ok := p.obs.(QueueCapacityObserver); ok {
		obs.SetWorkerWorkers(p.cfg.Name, p.cfg.Workers)
	}
}

func (p *Pool) observeAdmission(result string) {
	if obs, ok := p.obs.(AdmissionObserver); ok {
		obs.ObserveWorkerAdmission(p.cfg.Name, result)
	}
}

func (p *Pool) observeWait(kind TaskKind, d time.Duration) {
	if d < 0 {
		d = 0
	}
	if obs, ok := p.obs.(WaitObserver); ok {
		obs.ObserveWorkerWait(p.cfg.Name, kind, d)
	}
}

func (p *Pool) observeTask(kind TaskKind, err error, d time.Duration) {
	if d < 0 {
		d = 0
	}
	if obs, ok := p.obs.(TaskObserver); ok {
		obs.ObserveWorkerTask(p.cfg.Name, kind, err, d)
	}
}

func workerAdmissionResult(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	default:
		return "other"
	}
}
```

Add `errors` to imports.

- [ ] **Step 4: Run worker tests**

Run:

```bash
GOWORK=off go test ./pkg/channelv2/worker -run 'TestPoolReports|TestPoolSubmit|TestPoolClose' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/channelv2/worker/pool.go pkg/channelv2/worker/pool_test.go
git commit -m "feat(channelv2): observe worker pool pressure"
```

### Task 3: ChannelV2 Reactor Mailbox And Append Queue Pressure

**Files:**
- Modify: `pkg/channelv2/reactor/metrics.go`
- Modify: `pkg/channelv2/reactor/mailbox.go`
- Modify: `pkg/channelv2/reactor/reactor.go`
- Modify: `pkg/channelv2/reactor/append_queue.go`
- Modify: `pkg/channelv2/reactor/mailbox_test.go`
- Modify: `pkg/channelv2/reactor/append_queue_test.go`

- [ ] **Step 1: Write failing mailbox observer test**

Add to `pkg/channelv2/reactor/mailbox_test.go`:

```go
func TestReactorReportsMailboxCapacityAdmissionAndDepth(t *testing.T) {
	obs := &recordingMailboxPressureObserver{}
	r := NewReactor(ReactorConfig{ID: 3, LocalNode: 1, Store: store.NewMemoryFactory(), MailboxSize: 1, Observer: obs})

	err := r.Submit(PriorityNormal, Event{Kind: EventTick})
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	err = r.Submit(PriorityNormal, Event{Kind: EventTick})
	if !errors.Is(err, ch.ErrBackpressured) {
		t.Fatalf("Submit() error = %v, want %v", err, ch.ErrBackpressured)
	}
	err = r.Submit(PriorityLow, Event{Kind: EventTick, Future: NewFuture()})
	if err != nil {
		t.Fatalf("low priority Submit() error = %v", err)
	}

	if got := obs.capacity["3:normal"]; got != 1 {
		t.Fatalf("normal capacity = %d, want 1", got)
	}
	if got := obs.admission["3:normal:ok"]; got != 1 {
		t.Fatalf("normal ok admission = %d, want 1", got)
	}
	if got := obs.admission["3:normal:full"]; got != 1 {
		t.Fatalf("normal full admission = %d, want 1", got)
	}
	if got := obs.admission["3:low:coalesced"]; got != 1 {
		t.Fatalf("low coalesced admission = %d, want 1", got)
	}
}

type recordingMailboxPressureObserver struct {
	captureObserver
	mu        sync.Mutex
	capacity  map[string]int
	admission map[string]int
}

func (o *recordingMailboxPressureObserver) ensure() {
	if o.capacity == nil {
		o.capacity = make(map[string]int)
		o.admission = make(map[string]int)
	}
}

func (o *recordingMailboxPressureObserver) SetReactorMailboxCapacity(reactorID int, priority string, capacity int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.ensure()
	o.capacity[strconv.Itoa(reactorID)+":"+priority] = capacity
}

func (o *recordingMailboxPressureObserver) ObserveReactorMailboxAdmission(reactorID int, priority string, result string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.ensure()
	o.admission[strconv.Itoa(reactorID)+":"+priority+":"+result]++
}
```

Add missing imports:

```go
import (
	"errors"
	"strconv"
	"sync"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/store"
)
```

- [ ] **Step 2: Write failing append queue observer test**

Add to `pkg/channelv2/reactor/append_queue_test.go`:

```go
func TestAppendQueueReportsAggregatePressure(t *testing.T) {
	obs := &recordingAppendQueuePressureObserver{}
	r := &Reactor{cfg: ReactorConfig{ID: 0, AppendQueueMaxRequests: 1, AppendQueueMaxBytes: 8, Observer: obs}}
	q := newAppendQueue(appendQueueConfig{MaxRecords: 10, MaxBytes: 1024, MaxWait: time.Second, MaxPending: 1, MaxPendingBytes: 8})
	err := q.push(appendRequest{opID: 1, enqueuedAt: time.Now(), records: []ch.Record{{SizeBytes: 4}}})
	if err != nil {
		t.Fatalf("push() error = %v", err)
	}
	rc := &runtimeChannel{appendQ: q}
	r.observeAppendQueuePressure(rc)

	if got := obs.depth; got != 1 {
		t.Fatalf("append queue depth = %d, want 1", got)
	}
	if got := obs.capacity; got != 1 {
		t.Fatalf("append queue capacity = %d, want 1", got)
	}
	if got := obs.bytes; got != 4 {
		t.Fatalf("append queue bytes = %d, want 4", got)
	}
	if got := obs.bytesCapacity; got != 8 {
		t.Fatalf("append queue byte capacity = %d, want 8", got)
	}
}

type recordingAppendQueuePressureObserver struct {
	captureObserver
	depth         int
	capacity      int
	bytes         int
	bytesCapacity int
}

func (o *recordingAppendQueuePressureObserver) SetAppendQueuePressure(event AppendQueuePressureEvent) {
	o.depth = event.Depth
	o.capacity = event.Capacity
	o.bytes = event.Bytes
	o.bytesCapacity = event.BytesCapacity
}
```

- [ ] **Step 3: Run failing ChannelV2 reactor tests**

Run:

```bash
GOWORK=off go test ./pkg/channelv2/reactor -run 'TestReactorReportsMailboxCapacityAdmissionAndDepth|TestAppendQueueReportsAggregatePressure' -count=1
```

Expected: FAIL because the new observer interfaces and capacity helpers do not exist.

- [ ] **Step 4: Add observer interfaces**

In `pkg/channelv2/reactor/metrics.go`, add:

```go
// MailboxPressureObserver receives bounded reactor mailbox capacity and admission events.
type MailboxPressureObserver interface {
	SetReactorMailboxCapacity(reactorID int, priority string, capacity int)
	ObserveReactorMailboxAdmission(reactorID int, priority string, result string)
}

// AppendQueuePressureEvent reports aggregate per-reactor append queue pressure.
type AppendQueuePressureEvent struct {
	// ReactorID identifies the reactor partition.
	ReactorID int
	// Depth is the number of queued append requests.
	Depth int
	// Capacity is the maximum accepted append requests.
	Capacity int
	// Bytes is the queued append payload bytes.
	Bytes int
	// BytesCapacity is the queued append payload byte capacity.
	BytesCapacity int
}

// AppendQueuePressureObserver receives aggregate append queue pressure without channel labels.
type AppendQueuePressureObserver interface {
	SetAppendQueuePressure(event AppendQueuePressureEvent)
}
```

Add no-op methods:

```go
func (noopObserver) SetReactorMailboxCapacity(int, string, int)      {}
func (noopObserver) ObserveReactorMailboxAdmission(int, string, string) {}
func (noopObserver) SetAppendQueuePressure(AppendQueuePressureEvent) {}
```

Because these are optional interfaces, do not add them to the base `Observer` interface.

- [ ] **Step 5: Add mailbox capacity helpers**

In `pkg/channelv2/reactor/mailbox.go`, add:

```go
// Capacity returns the configured capacity for one priority queue.
func (m *Mailbox) Capacity(priority Priority) int {
	if m == nil {
		return 0
	}
	switch priority {
	case PriorityHigh:
		return cap(m.high)
	case PriorityLow:
		return cap(m.low)
	default:
		return cap(m.normal)
	}
}
```

- [ ] **Step 6: Emit mailbox pressure from `Reactor`**

In `pkg/channelv2/reactor/reactor.go`, after `NewMailbox`, call `observeAllMailboxCapacities` before returning or immediately after construction:

```go
r := &Reactor{cfg: cfg, mailbox: NewMailbox(MailboxConfig{HighSize: cfg.MailboxSize, NormalSize: cfg.MailboxSize, LowSize: cfg.MailboxSize}), drainBuf: make([]Event, 0, defaultReactorDrain), channels: make(map[ch.ChannelKey]*runtimeChannel), stop: make(chan struct{}), done: make(chan struct{})}
r.observeAllMailboxCapacities()
return r
```

In `Submit`, classify the result after `mailbox.Submit`:

```go
err := r.mailbox.Submit(priority, event)
r.observeMailboxAdmission(priority, mailboxAdmissionResult(priority, err))
r.observeMailboxDepth(priority)
return err
```

For low priority, `Mailbox.Submit` returns nil on coalesced/drop. Add a `Mailbox.SubmitWithResult` helper or change `Submit` to return an internal result. Keep public `Submit` behavior unchanged:

```go
type mailboxSubmitResult string

const (
	mailboxSubmitOK        mailboxSubmitResult = "ok"
	mailboxSubmitFull      mailboxSubmitResult = "full"
	mailboxSubmitClosed    mailboxSubmitResult = "closed"
	mailboxSubmitCoalesced mailboxSubmitResult = "coalesced"
)
```

Use `SubmitWithResult` internally:

```go
func (m *Mailbox) SubmitWithResult(priority Priority, event Event) (mailboxSubmitResult, error) {
	if m == nil {
		return mailboxSubmitClosed, ch.ErrClosed
	}
	queue := m.normal
	switch priority {
	case PriorityHigh:
		queue = m.high
	case PriorityLow:
		queue = m.low
	}
	select {
	case queue <- event:
		return mailboxSubmitOK, nil
	default:
		if priority == PriorityLow {
			if event.Future != nil {
				event.Future.Complete(Result{})
			}
			return mailboxSubmitCoalesced, nil
		}
		return mailboxSubmitFull, ch.ErrBackpressured
	}
}
```

Keep `Submit` as:

```go
func (m *Mailbox) Submit(priority Priority, event Event) error {
	_, err := m.SubmitWithResult(priority, event)
	return err
}
```

In `metrics.go`, add helper methods:

```go
func (r *Reactor) observeMailboxCapacity(priority Priority) {
	if observer, ok := r.cfg.Observer.(MailboxPressureObserver); ok {
		observer.SetReactorMailboxCapacity(r.cfg.ID, priorityName(priority), r.mailbox.Capacity(priority))
	}
}

func (r *Reactor) observeMailboxAdmission(priority Priority, result mailboxSubmitResult) {
	if observer, ok := r.cfg.Observer.(MailboxPressureObserver); ok {
		observer.ObserveReactorMailboxAdmission(r.cfg.ID, priorityName(priority), string(result))
	}
}

func (r *Reactor) observeAllMailboxCapacities() {
	r.observeMailboxCapacity(PriorityHigh)
	r.observeMailboxCapacity(PriorityNormal)
	r.observeMailboxCapacity(PriorityLow)
}
```

- [ ] **Step 7: Emit append queue pressure**

In `pkg/channelv2/reactor/append_queue.go`, after every append queue accept, flush, complete, backpressure, or drop path that changes queued requests or bytes, call:

```go
r.observeAppendQueuePressure(rc)
```

Add helper to `metrics.go`:

```go
func (r *Reactor) observeAppendQueuePressure(rc *runtimeChannel) {
	if r == nil || rc == nil {
		return
	}
	observer, ok := r.cfg.Observer.(AppendQueuePressureObserver)
	if !ok {
		return
	}
	observer.SetAppendQueuePressure(AppendQueuePressureEvent{
		ReactorID:      r.cfg.ID,
		Depth:          rc.appendQueue.PendingRequests(),
		Capacity:       r.cfg.AppendQueueMaxRequests,
		Bytes:          rc.appendQueue.PendingBytes(),
		BytesCapacity:  r.cfg.AppendQueueMaxBytes,
	})
}
```

Add these accessors to `append_queue.go`:

```go
func (q *appendQueue) PendingRequests() int {
	if q == nil {
		return 0
	}
	return len(q.pending)
}

func (q *appendQueue) PendingBytes() int {
	if q == nil {
		return 0
	}
	return q.bytes
}
```

- [ ] **Step 8: Run ChannelV2 reactor tests**

Run:

```bash
GOWORK=off go test ./pkg/channelv2/reactor -run 'TestReactorReportsMailboxCapacityAdmissionAndDepth|TestAppendQueueReportsAggregatePressure|TestDirectEventTickFutureCompletesWhenLowMailboxDrops|TestGroupCompleteDoesNotDropWhenHighMailboxIsFull' -count=1
```

Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add pkg/channelv2/reactor/metrics.go pkg/channelv2/reactor/mailbox.go pkg/channelv2/reactor/reactor.go pkg/channelv2/reactor/append_queue.go pkg/channelv2/reactor/mailbox_test.go pkg/channelv2/reactor/append_queue_test.go
git commit -m "feat(channelv2): observe reactor queue pressure"
```

### Task 4: Gateway Async And Gnet Pressure

**Files:**
- Modify: `pkg/gateway/types/observer.go`
- Modify: `pkg/gateway/event.go`
- Modify: `pkg/gateway/core/server.go`
- Modify: `pkg/gateway/core/async_auth_test.go`
- Modify: `pkg/gateway/core/async_dispatch_test.go`
- Modify: `pkg/gateway/transport/listener.go`
- Modify: `pkg/gateway/transport/gnet/group.go`
- Modify: `pkg/gateway/transport/gnet/actor.go`
- Modify: `pkg/gateway/transport/gnet/conn.go`
- Modify: `pkg/gateway/transport/gnet/actor_test.go`
- Modify: `pkg/gateway/transport/gnet/conn_backpressure_test.go`
- Modify: `pkg/gateway/FLOW.md`

- [ ] **Step 1: Write failing gateway core tests**

Extend `recordingAsyncSendObserver` in `pkg/gateway/core/async_dispatch_test.go` to implement new interfaces:

```go
authQueues      []gatewaytypes.AsyncAuthQueueEvent
authAdmissions []gatewaytypes.AsyncAuthAdmissionEvent
sendAdmissions []gatewaytypes.AsyncSendAdmissionEvent
```

Add methods:

```go
func (o *recordingAsyncSendObserver) OnAsyncAuthQueue(event gatewaytypes.AsyncAuthQueueEvent) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.authQueues = append(o.authQueues, event)
}

func (o *recordingAsyncSendObserver) OnAsyncAuthAdmission(event gatewaytypes.AsyncAuthAdmissionEvent) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.authAdmissions = append(o.authAdmissions, event)
}

func (o *recordingAsyncSendObserver) OnAsyncSendAdmission(event gatewaytypes.AsyncSendAdmissionEvent) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.sendAdmissions = append(o.sendAdmissions, event)
}
```

Add tests:

```go
func TestAsyncSendReportsAdmissionResults(t *testing.T) {
	observer := &recordingAsyncSendObserver{}
	handler := &countingAsyncFrameHandler{}
	srv := &Server{dispatcher: newDispatcher(handler), options: gatewaytypes.Options{Observer: observer}}
	queue := newAsyncDispatchQueueWithCapacity(1, 1)
	srv.asyncDispatch.Store(queue)

	state := &sessionState{server: srv, key: connKey{connID: 1}}
	send := &frame.SendPacket{ChannelID: "c1", ChannelType: 1}
	if !queue.submitSend(state, "r1", send) {
		t.Fatal("first submitSend failed")
	}
	srv.observeAsyncSendAdmission(queue, true)
	if queue.submitSend(state, "r2", send) {
		t.Fatal("second submitSend unexpectedly succeeded")
	}
	srv.observeAsyncSendAdmission(queue, false)

	if got := observer.sendAdmissions[0].Result; got != "ok" {
		t.Fatalf("first admission result = %q, want ok", got)
	}
	if got := observer.sendAdmissions[1].Result; got != "full" {
		t.Fatalf("second admission result = %q, want full", got)
	}
}
```

Append to `pkg/gateway/core/async_auth_test.go`:

```go
func TestAsyncAuthReportsQueueAndAdmission(t *testing.T) {
	observer := &recordingAsyncSendObserver{}
	srv := &Server{options: gatewaytypes.Options{Observer: observer}}
	queue := newAsyncAuthQueueWithCapacity(1)
	srv.asyncAuth.Store(queue)
	state := &sessionState{}

	if !queue.submit(asyncAuthTask{state: state, connect: &frame.ConnectPacket{UID: "u1"}}) {
		t.Fatal("first auth submit failed")
	}
	srv.observeAsyncAuthQueue(queue)
	srv.observeAsyncAuthAdmission(queue, true)
	if queue.submit(asyncAuthTask{state: state, connect: &frame.ConnectPacket{UID: "u2"}}) {
		t.Fatal("second auth submit unexpectedly succeeded")
	}
	srv.observeAsyncAuthAdmission(queue, false)

	if got := observer.authQueues[0].Depth; got != 1 {
		t.Fatalf("auth queue depth = %d, want 1", got)
	}
	if got := observer.authAdmissions[0].Result; got != "ok" {
		t.Fatalf("first auth result = %q, want ok", got)
	}
	if got := observer.authAdmissions[1].Result; got != "full" {
		t.Fatalf("second auth result = %q, want full", got)
	}
}
```

- [ ] **Step 2: Run failing gateway core tests**

Run:

```bash
GOWORK=off go test ./pkg/gateway/core -run 'TestAsyncSendReportsAdmissionResults|TestAsyncAuthReportsQueueAndAdmission' -count=1
```

Expected: FAIL because new event types and observer methods do not exist.

- [ ] **Step 3: Add gateway observer event types**

In `pkg/gateway/types/observer.go`, add:

```go
// AsyncAuthObserver receives optional observations from the asynchronous CONNECT authentication path.
type AsyncAuthObserver interface {
	OnAsyncAuthQueue(event AsyncAuthQueueEvent)
	OnAsyncAuthAdmission(event AsyncAuthAdmissionEvent)
	OnAsyncAuthWait(event AsyncAuthWaitEvent)
}

// AsyncSendAdmissionObserver receives optional asynchronous SEND enqueue outcomes.
type AsyncSendAdmissionObserver interface {
	OnAsyncSendAdmission(event AsyncSendAdmissionEvent)
}

// TransportPressureObserver receives optional gateway transport pressure observations.
type TransportPressureObserver interface {
	OnTransportPressure(event TransportPressureEvent)
}

// AsyncAuthQueueEvent reports aggregate asynchronous auth queue occupancy.
type AsyncAuthQueueEvent struct {
	Depth    int
	Capacity int
	Workers  int
}

// AsyncAuthAdmissionEvent reports one asynchronous auth enqueue outcome.
type AsyncAuthAdmissionEvent struct {
	Result string
}

// AsyncAuthWaitEvent reports how long a CONNECT waited before authentication work began.
type AsyncAuthWaitEvent struct {
	ConnectionEvent
	Duration time.Duration
}

// AsyncSendAdmissionEvent reports one asynchronous SEND enqueue outcome.
type AsyncSendAdmissionEvent struct {
	Result string
}

// TransportPressureEvent reports aggregate gateway transport queue or byte pressure.
type TransportPressureEvent struct {
	Name          string
	Queue         string
	Depth         int
	Capacity      int
	Bytes         int64
	BytesCapacity int64
	Result        string
}
```

In `pkg/gateway/event.go`, re-export the new public observer extension types:

```go
type AsyncAuthObserver = gatewaytypes.AsyncAuthObserver
type AsyncAuthQueueEvent = gatewaytypes.AsyncAuthQueueEvent
type AsyncAuthAdmissionEvent = gatewaytypes.AsyncAuthAdmissionEvent
type AsyncAuthWaitEvent = gatewaytypes.AsyncAuthWaitEvent
type AsyncSendAdmissionObserver = gatewaytypes.AsyncSendAdmissionObserver
type AsyncSendAdmissionEvent = gatewaytypes.AsyncSendAdmissionEvent
type TransportPressureObserver = gatewaytypes.TransportPressureObserver
type TransportPressureEvent = gatewaytypes.TransportPressureEvent
```

- [ ] **Step 4: Emit async auth and SEND pressure in gateway core**

In `pkg/gateway/core/server.go`:

Add `workers int` to `asyncAuthQueue` and initialize:

```go
return &asyncAuthQueue{
	tasks:    make(chan asyncAuthTask, capacity),
	capacity: capacity,
	workers:  workers,
}
```

Add methods:

```go
func (q *asyncAuthQueue) depth() int {
	if q == nil {
		return 0
	}
	depth := q.queued.Load()
	if depth < 0 {
		return 0
	}
	return int(depth)
}

func (q *asyncAuthQueue) totalCapacity() int {
	if q == nil {
		return 0
	}
	return q.capacity
}
```

After successful auth queue start, call:

```go
s.observeAsyncAuthQueue(queue)
```

After auth submit in `handleAuthFrame`, call:

```go
s.observeAsyncAuthAdmission(queue, true)
s.observeAsyncAuthQueue(queue)
```

On failure:

```go
s.observeAsyncAuthAdmission(queue, false)
s.observeAsyncAuthQueue(queue)
```

In `runAsyncAuthWorker`, after `queue.consume(1)`:

```go
s.observeAsyncAuthQueue(queue)
s.observeAsyncAuthWait(task)
```

For SEND, in `dispatchSendFrameAsync`, call `observeAsyncSendAdmission(queue, accepted)` before queue observations.

Add observer helper methods:

```go
func (s *Server) asyncAuthObserver() gatewaytypes.AsyncAuthObserver {
	if s == nil || s.options.Observer == nil {
		return nil
	}
	observer, ok := s.options.Observer.(gatewaytypes.AsyncAuthObserver)
	if !ok {
		return nil
	}
	return observer
}

func (s *Server) asyncSendAdmissionObserver() gatewaytypes.AsyncSendAdmissionObserver {
	if s == nil || s.options.Observer == nil {
		return nil
	}
	observer, ok := s.options.Observer.(gatewaytypes.AsyncSendAdmissionObserver)
	if !ok {
		return nil
	}
	return observer
}

func (s *Server) observeAsyncAuthQueue(queue *asyncAuthQueue) {
	observer := s.asyncAuthObserver()
	if observer == nil || queue == nil {
		return
	}
	observer.OnAsyncAuthQueue(gatewaytypes.AsyncAuthQueueEvent{
		Depth:    queue.depth(),
		Capacity: queue.totalCapacity(),
		Workers:  queue.workers,
	})
}

func (s *Server) observeAsyncAuthAdmission(_ *asyncAuthQueue, accepted bool) {
	observer := s.asyncAuthObserver()
	if observer == nil {
		return
	}
	result := "full"
	if accepted {
		result = "ok"
	}
	observer.OnAsyncAuthAdmission(gatewaytypes.AsyncAuthAdmissionEvent{Result: result})
}

func (s *Server) observeAsyncAuthWait(task asyncAuthTask) {
	observer := s.asyncAuthObserver()
	if observer == nil || task.enqueuedAt.IsZero() {
		return
	}
	d := time.Since(task.enqueuedAt)
	if d < 0 {
		d = 0
	}
	observer.OnAsyncAuthWait(gatewaytypes.AsyncAuthWaitEvent{
		ConnectionEvent: connectionEventForState(task.state),
		Duration:        d,
	})
}

func (s *Server) observeAsyncSendAdmission(_ *asyncDispatchQueue, accepted bool) {
	observer := s.asyncSendAdmissionObserver()
	if observer == nil {
		return
	}
	result := "full"
	if accepted {
		result = "ok"
	}
	observer.OnAsyncSendAdmission(gatewaytypes.AsyncSendAdmissionEvent{Result: result})
}
```

Keep `AsyncSendObserver` unchanged.

- [ ] **Step 5: Add gnet observer pass-through**

In `pkg/gateway/transport/listener.go`, extend `ListenerOptions` with:

```go
// Observer receives aggregate transport pressure observations.
Observer gatewaytypes.TransportPressureObserver
```

Update imports:

```go
import (
	gatewaytypes "github.com/WuKongIM/WuKongIM/pkg/gateway/types"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)
```

In `pkg/gateway/core/server.go`, when building `transport.ListenerSpec`, set:

```go
Observer: s.transportPressureObserver(),
```

Add helper:

```go
func (s *Server) transportPressureObserver() gatewaytypes.TransportPressureObserver {
	if s == nil || s.options.Observer == nil {
		return nil
	}
	observer, ok := s.options.Observer.(gatewaytypes.TransportPressureObserver)
	if !ok {
		return nil
	}
	return observer
}
```

In `actorShard.schedule`, after appending to `ready`, emit:

```go
state.observeTransport("actor_ready", "ready", len(s.ready), actorReadyQueueSize, 0, 0, "ok")
```

On stopped, emit result `closed`.

In `connState.enqueueCopiedData`, after accepted append, emit:

```go
s.observeTransport("inbound_pending", "inbound", len(s.queue), 0, int64(s.pendingBytes), int64(s.maxPendingBytes), "ok")
```

On pending byte rejection, emit result `too_large`.

In `beginOutboundWrite`, after admission, emit outbound bytes with result `ok`; on rejection emit result `full`.

Add one helper on `connState` so actor, inbound, and outbound observations share the same low-cardinality shape:

```go
func (s *connState) observeTransport(name, queue string, depth, capacity int, bytes, bytesCapacity int64, result string) {
	if s == nil || s.runtime == nil || s.runtime.opts.Observer == nil {
		return
	}
	s.runtime.opts.Observer.OnTransportPressure(gatewaytypes.TransportPressureEvent{
		Name:          name,
		Queue:         queue,
		Depth:         depth,
		Capacity:      capacity,
		Bytes:         bytes,
		BytesCapacity: bytesCapacity,
		Result:        result,
	})
}
```

Use aggregate queue names and never include conn id, uid, channel id, or remote address.

- [ ] **Step 6: Run gateway tests**

Run:

```bash
GOWORK=off go test ./pkg/gateway/core ./pkg/gateway/transport/gnet -run 'TestAsyncSendReportsAdmissionResults|TestAsyncAuthReportsQueueAndAdmission|TestActor|TestConnBackpressure' -count=1
```

Expected: PASS.

- [ ] **Step 7: Update `pkg/gateway/FLOW.md` with the expanded observer contract**

Add one short paragraph near the existing async SEND observer description:

```markdown
Gateway pressure observers are optional extensions on top of the base Observer. They report async auth queue pressure, SEND admission outcomes, and gnet aggregate actor/inbound/outbound pressure without per-connection labels or behavior changes.
```

- [ ] **Step 8: Commit**

```bash
git add pkg/gateway/types/observer.go pkg/gateway/event.go pkg/gateway/core/server.go pkg/gateway/core/async_auth_test.go pkg/gateway/core/async_dispatch_test.go pkg/gateway/transport/listener.go pkg/gateway/transport/gnet/group.go pkg/gateway/transport/gnet/actor.go pkg/gateway/transport/gnet/conn.go pkg/gateway/transport/gnet/actor_test.go pkg/gateway/transport/gnet/conn_backpressure_test.go pkg/gateway/FLOW.md
git commit -m "feat(gateway): observe async and transport pressure"
```

The `git add` command includes `pkg/gateway/FLOW.md` because this plan expands the observer contract.

### Task 5: Slot Multi-Raft Scheduler Pressure And ClusterV2 Pass-Through

**Files:**
- Modify: `pkg/slot/multiraft/types.go`
- Modify: `pkg/slot/multiraft/scheduler.go`
- Modify: `pkg/slot/multiraft/runtime.go`
- Modify: `pkg/slot/multiraft/scheduler_test.go`
- Modify: `pkg/slot/multiraft/step_test.go`
- Modify: `pkg/clusterv2/config.go`
- Modify: `pkg/clusterv2/default_slots.go`
- Modify: `pkg/clusterv2/FLOW.md`

- [ ] **Step 1: Write failing Slot scheduler tests**

Append to `pkg/slot/multiraft/scheduler_test.go`:

```go
func TestSchedulerReportsAdmissionAndState(t *testing.T) {
	obs := &recordingSlotSchedulerObserver{}
	s := newScheduler(obs)
	s.ch = make(chan SlotID, 1)

	s.enqueue(1)
	s.enqueue(1)
	s.begin(1)
	s.enqueue(1)
	requeue := s.done(1)
	if !requeue {
		t.Fatal("done() did not report dirty requeue")
	}
	s.requeue(1)

	if got := obs.admissions["ok"]; got != 1 {
		t.Fatalf("ok admissions = %d, want 1", got)
	}
	if got := obs.admissions["coalesced"]; got != 1 {
		t.Fatalf("coalesced admissions = %d, want 1", got)
	}
	if got := obs.admissions["dirty"]; got != 1 {
		t.Fatalf("dirty admissions = %d, want 1", got)
	}
	if len(obs.states) == 0 {
		t.Fatal("scheduler state was not observed")
	}
}

type recordingSlotSchedulerObserver struct {
	admissions map[string]int
	states     []SchedulerStateEvent
}

func (o *recordingSlotSchedulerObserver) ObserveSchedulerAdmission(result string) {
	if o.admissions == nil {
		o.admissions = make(map[string]int)
	}
	o.admissions[result]++
}

func (o *recordingSlotSchedulerObserver) SetSchedulerState(event SchedulerStateEvent) {
	o.states = append(o.states, event)
}

func (o *recordingSlotSchedulerObserver) SetSchedulerWorkers(int) {}
func (o *recordingSlotSchedulerObserver) SetSchedulerInflight(int) {}
func (o *recordingSlotSchedulerObserver) ObserveSchedulerTask(string, time.Duration) {}
```

- [ ] **Step 2: Run failing Slot tests**

Run:

```bash
GOWORK=off go test ./pkg/slot/multiraft -run 'TestSchedulerReportsAdmissionAndState' -count=1
```

Expected: FAIL because scheduler observer APIs do not exist and `newScheduler` has no observer argument.

- [ ] **Step 3: Add Slot observer types**

In `pkg/slot/multiraft/types.go`, add:

```go
// SchedulerObserver receives low-cardinality Slot scheduler pressure observations.
type SchedulerObserver interface {
	SetSchedulerWorkers(workers int)
	SetSchedulerInflight(inflight int)
	SetSchedulerState(event SchedulerStateEvent)
	ObserveSchedulerAdmission(result string)
	ObserveSchedulerTask(task string, d time.Duration)
}

// SchedulerStateEvent reports aggregate Slot scheduler queue state.
type SchedulerStateEvent struct {
	// Depth is the number of runnable Slot IDs queued in the dispatch channel.
	Depth int
	// Capacity is the dispatch channel capacity.
	Capacity int
	// Pending is the number of Slot IDs waiting behind a full dispatch channel.
	Pending int
	// Queued is the number of Slot IDs marked queued.
	Queued int
	// Processing is the number of Slot IDs currently being processed.
	Processing int
	// Dirty is the number of processing Slot IDs marked for another pass.
	Dirty int
}
```

Add to `Options`:

```go
// Observer receives low-cardinality scheduler pressure observations.
Observer SchedulerObserver
```

- [ ] **Step 4: Emit scheduler observations**

Change scheduler construction:

```go
func newScheduler(observer SchedulerObserver) *scheduler {
	return &scheduler{
		ch:         make(chan SlotID, 1024),
		queued:     make(map[SlotID]struct{}),
		processing: make(map[SlotID]struct{}),
		dirty:      make(map[SlotID]struct{}),
		observer:   observer,
	}
}
```

Add field:

```go
observer SchedulerObserver
```

In `enqueue`, emit:

```go
if _, ok := s.queued[slotID]; ok {
	s.observeAdmissionLocked("coalesced")
	s.observeStateLocked()
	s.mu.Unlock()
	return
}
if _, ok := s.processing[slotID]; ok {
	s.dirty[slotID] = struct{}{}
	s.observeAdmissionLocked("dirty")
	s.observeStateLocked()
	s.mu.Unlock()
	return
}
...
s.observeAdmissionLocked("ok")
s.dispatchLocked()
s.observeStateLocked()
```

In `begin`, `done`, and `requeue`, call `observeStateLocked`; in `requeue` call `ObserveSchedulerAdmission("requeued")` for accepted requeues.

Add helpers:

```go
func (s *scheduler) observeAdmissionLocked(result string) {
	if s.observer != nil {
		s.observer.ObserveSchedulerAdmission(result)
	}
}

func (s *scheduler) observeStateLocked() {
	if s.observer == nil {
		return
	}
	s.observer.SetSchedulerState(SchedulerStateEvent{
		Depth:      len(s.ch),
		Capacity:   cap(s.ch),
		Pending:    len(s.pending),
		Queued:     len(s.queued),
		Processing: len(s.processing),
		Dirty:      len(s.dirty),
	})
}
```

- [ ] **Step 5: Emit worker and task observations from Runtime**

In `pkg/slot/multiraft/runtime.go`, construct scheduler with:

```go
scheduler: newScheduler(opts.Observer),
```

After runtime construction and before `rt.start()`:

```go
if opts.Observer != nil {
	opts.Observer.SetSchedulerWorkers(opts.Workers)
}
```

Add `inflight atomic.Int64` to `Runtime` and import `sync/atomic`.

In `runWorker`, around `processSlot`:

```go
r.observeSchedulerInflight(int(r.inflight.Add(1)))
started := time.Now()
requeue := r.processSlot(slotID)
r.observeSchedulerTask("process_slot", time.Since(started))
r.observeSchedulerInflight(int(r.inflight.Add(-1)))
```

Add helpers:

```go
func (r *Runtime) observeSchedulerInflight(inflight int) {
	if r != nil && r.opts.Observer != nil {
		r.opts.Observer.SetSchedulerInflight(inflight)
	}
}

func (r *Runtime) observeSchedulerTask(task string, d time.Duration) {
	if d < 0 {
		d = 0
	}
	if r != nil && r.opts.Observer != nil {
		r.opts.Observer.ObserveSchedulerTask(task, d)
	}
}
```

- [ ] **Step 6: Pass Slot observer through ClusterV2**

In `pkg/clusterv2/config.go`, add to `SlotConfig`:

```go
// Observer receives low-cardinality Slot scheduler pressure observations.
Observer multiraft.SchedulerObserver
```

Import `github.com/WuKongIM/WuKongIM/pkg/slot/multiraft`.

In `pkg/clusterv2/default_slots.go`, pass:

```go
Observer: n.cfg.Slots.Observer,
```

into `multiraft.Options`.

Update `pkg/clusterv2/FLOW.md` with one short line in the default Slot section:

```markdown
`Config.Slots.Observer` is passed to the default Slot Multi-Raft runtime so composition roots can expose scheduler pressure without changing Slot processing semantics.
```

- [ ] **Step 7: Run Slot and ClusterV2 tests**

Run:

```bash
GOWORK=off go test ./pkg/slot/multiraft ./pkg/clusterv2 -run 'TestSchedulerReportsAdmissionAndState|TestRuntimeTickLoopEnqueuesOpenSlots|TestDefault' -count=1
```

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add pkg/slot/multiraft/types.go pkg/slot/multiraft/scheduler.go pkg/slot/multiraft/runtime.go pkg/slot/multiraft/scheduler_test.go pkg/slot/multiraft/step_test.go pkg/clusterv2/config.go pkg/clusterv2/default_slots.go pkg/clusterv2/FLOW.md
git commit -m "feat(slot): observe multiraft scheduler pressure"
```

### Task 6: TransportV2 Pressure Events

**Files:**
- Modify: `pkg/transportv2/internal/core/types.go`
- Modify: `pkg/transportv2/internal/sched/scheduler.go`
- Modify: `pkg/transportv2/internal/rpc/service.go`
- Modify: `pkg/transportv2/internal/conn/conn.go`
- Modify: `pkg/transportv2/internal/peer/manager.go`
- Modify: `pkg/transportv2/client.go`
- Modify: `pkg/transportv2/server.go`
- Modify: `pkg/transportv2/internal/sched/scheduler_test.go`
- Modify: `pkg/transportv2/internal/rpc/service_test.go`
- Modify: `pkg/transportv2/internal/conn/conn_test.go`
- Modify: `pkg/transportv2/internal/peer/manager_test.go`

- [ ] **Step 1: Write failing scheduler observer test**

Append to `pkg/transportv2/internal/sched/scheduler_test.go`:

```go
func TestSchedulerObservesQueueAdmissionAndWait(t *testing.T) {
	obs := &recordingTransportObserver{}
	s := New(Config{MaxItems: 1, MaxBytes: 10, MaxBatchBytes: 10, MaxBatchFrames: 2, Observer: obs})
	if err := s.Enqueue(context.Background(), Item{Priority: core.PriorityRPC, Bytes: 4, Value: "one"}); err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}
	if err := s.Enqueue(context.Background(), Item{Priority: core.PriorityRPC, Bytes: 4, Value: "two"}); !errors.Is(err, core.ErrQueueFull) {
		t.Fatalf("second Enqueue() error = %v, want %v", err, core.ErrQueueFull)
	}
	batch := s.NextBatch()
	if len(batch) != 1 {
		t.Fatalf("NextBatch len = %d, want 1", len(batch))
	}

	if got := obs.count("scheduler_admission", "ok"); got != 1 {
		t.Fatalf("scheduler ok admissions = %d, want 1", got)
	}
	if got := obs.count("scheduler_admission", "full"); got != 1 {
		t.Fatalf("scheduler full admissions = %d, want 1", got)
	}
	if got := obs.count("scheduler_wait", "ok"); got != 1 {
		t.Fatalf("scheduler wait events = %d, want 1", got)
	}
}
```

- [ ] **Step 2: Write failing service observer test**

Append to `pkg/transportv2/internal/rpc/service_test.go`:

```go
func TestServiceObservesQueueAdmissionInflightAndTask(t *testing.T) {
	obs := &recordingTransportObserver{}
	done := make(chan struct{})
	svc := NewService(7, func(context.Context, []byte) ([]byte, error) {
		close(done)
		return []byte("ok"), nil
	}, core.ServiceOptions{Concurrency: 1, QueueSize: 1, MaxQueueBytes: 64}, obs)
	defer svc.Stop()

	reply := make(chan Response, 1)
	if err := svc.Enqueue(Request{Payload: core.CopyOwnedBuffer([]byte("hello")), Reply: reply}); err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for handler")
	}

	if got := obs.count("service_admission", "ok"); got != 1 {
		t.Fatalf("service ok admissions = %d, want 1", got)
	}
	if got := obs.count("service_task", "ok"); got != 1 {
		t.Fatalf("service task events = %d, want 1", got)
	}
}
```

Define `recordingTransportObserver` once in each internal package test or in a local `_test.go` helper:

```go
type recordingTransportObserver struct {
	mu     sync.Mutex
	events []core.Event
}

func (o *recordingTransportObserver) ObserveTransport(event core.Event) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.events = append(o.events, event)
}

func (o *recordingTransportObserver) count(name, result string) int {
	o.mu.Lock()
	defer o.mu.Unlock()
	n := 0
	for _, event := range o.events {
		if event.Name == name && event.Result == result {
			n++
		}
	}
	return n
}
```

- [ ] **Step 3: Run failing TransportV2 tests**

Run:

```bash
GOWORK=off go test ./pkg/transportv2/internal/sched ./pkg/transportv2/internal/rpc -run 'TestSchedulerObservesQueueAdmissionAndWait|TestServiceObservesQueueAdmissionInflightAndTask' -count=1
```

Expected: FAIL because scheduler/service observer config is missing.

- [ ] **Step 4: Extend `core.Event`**

In `pkg/transportv2/internal/core/types.go`, add fields:

```go
// Items is the queued item count or current count associated with the event.
Items int
// Capacity is the queued item capacity associated with the event.
Capacity int
// BytesCapacity is the queued byte capacity associated with the event.
BytesCapacity int64
// Inflight is the currently running handler or pending RPC count associated with the event.
Inflight int
```

- [ ] **Step 5: Instrument scheduler**

In `pkg/transportv2/internal/sched/scheduler.go`, add to `Config`:

```go
// Observer receives scheduler pressure events.
Observer core.Observer
```

Add to `Item`:

```go
enqueuedAt time.Time
```

Add `observer core.Observer` to `Scheduler` and initialize it.

In `Enqueue`, classify and emit:

```go
result := "ok"
defer func() {
	s.observeAdmission(item.Priority, result)
	s.observeQueue(item.Priority)
}()
```

Set `result` to `canceled`, `invalid`, `too_large`, `stopped`, or `full` on each existing return path.

Before appending:

```go
item.enqueuedAt = time.Now()
```

In `nextBatchLocked`, when removing an item:

```go
s.observeWait(item)
```

Add helpers:

```go
func (s *Scheduler) observeQueue(priority core.Priority) {
	if s == nil || s.observer == nil {
		return
	}
	s.observer.ObserveTransport(core.Event{
		Name:          "scheduler_queue",
		Priority:      priority,
		Result:        "ok",
		Items:         s.queuedItems,
		Capacity:      s.maxItems,
		Bytes:         int(s.queuedBytes),
		BytesCapacity: s.maxBytes,
	})
}

func (s *Scheduler) observeAdmission(priority core.Priority, result string) {
	if s != nil && s.observer != nil {
		s.observer.ObserveTransport(core.Event{Name: "scheduler_admission", Priority: priority, Result: result})
	}
}

func (s *Scheduler) observeWait(item Item) {
	if s == nil || s.observer == nil || item.enqueuedAt.IsZero() {
		return
	}
	d := time.Since(item.enqueuedAt)
	if d < 0 {
		d = 0
	}
	s.observer.ObserveTransport(core.Event{Name: "scheduler_wait", Priority: item.Priority, Result: "ok", Bytes: item.Bytes, Duration: d})
}
```

- [ ] **Step 6: Instrument RPC service**

Change constructor signature in `pkg/transportv2/internal/rpc/service.go`:

```go
func NewService(id uint16, handler core.Handler, opts core.ServiceOptions, observer core.Observer) *Service
```

Add fields:

```go
observer core.Observer
inflight atomic.Int64
```

In `Enqueue`, classify outcomes `ok`, `too_large`, `stopped`, or `busy`, and emit `service_admission` plus `service_queue` after state changes.

In `worker`, after dequeue, emit queue state. Around `handle(req)`:

```go
running := int(s.inflight.Add(1))
s.observeInflight(running)
started := time.Now()
s.handle(req)
s.observeTask("ok", time.Since(started))
running = int(s.inflight.Add(-1))
s.observeInflight(running)
```

In `handle`, return the handler error so `observeTask` can use result `timeout`, `err`, or `ok`:

```go
func (s *Service) handle(req Request) error
```

Keep response behavior unchanged.

- [ ] **Step 7: Thread observer through conn, peer, client, and server**

In `conn.Config`, add:

```go
Observer core.Observer
NodeID   core.NodeID
```

Pass `Observer` into `sched.New`.

In `Call`, after `pending.Store` and every `pending.Delete`, `Complete`, or `FailAll` point available to `Conn`, emit:

```go
c.observePendingRPC()
```

Add:

```go
func (c *Conn) observePendingRPC() {
	if c == nil || c.cfg.Observer == nil {
		return
	}
	c.cfg.Observer.ObserveTransport(core.Event{Name: "pending_rpc", NodeID: c.cfg.NodeID, Result: "ok", Inflight: c.pending.Len()})
}
```

`PendingTable` already exposes `Len() int`; use it for `pending_rpc` observations and keep the existing pending table tests passing.

In `peer.Config`, add `Observer core.Observer`, pass it into `conn.New`, and emit `peer_pool` from `Stats`, `Acquire` installed connection, `ClosePeer`, and `Stop`:

```go
m.observePeerPool(nodeID)
```

In `Client.NewClient`, pass `normalized.Observer` into peer config.

In `Server.Handle`, call:

```go
rpc.NewService(serviceID, handler, opts, s.cfg.Observer)
```

In `acceptLoop`, pass `Observer` and local `NodeID` to inbound `conn.New`.

- [ ] **Step 8: Run TransportV2 tests**

Run:

```bash
GOWORK=off go test ./pkg/transportv2/... -run 'TestSchedulerObservesQueueAdmissionAndWait|TestServiceObservesQueueAdmissionInflightAndTask|TestPending|TestManager|TestClientServer' -count=1
```

Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add pkg/transportv2/internal/core/types.go pkg/transportv2/internal/sched/scheduler.go pkg/transportv2/internal/rpc/service.go pkg/transportv2/internal/rpc/pending.go pkg/transportv2/internal/conn/conn.go pkg/transportv2/internal/peer/manager.go pkg/transportv2/client.go pkg/transportv2/server.go pkg/transportv2/internal/sched/scheduler_test.go pkg/transportv2/internal/rpc/service_test.go pkg/transportv2/internal/rpc/pending_test.go pkg/transportv2/internal/conn/conn_test.go pkg/transportv2/internal/peer/manager_test.go
git commit -m "feat(transportv2): emit runtime pressure events"
```

### Task 7: InternalV2 Observability Adapter

**Files:**
- Modify: `internalv2/app/observability.go`
- Modify: `internalv2/app/observability_test.go`
- Modify: `internalv2/app/app.go`

- [ ] **Step 1: Write failing adapter tests**

Append to `internalv2/app/observability_test.go`:

```go
func TestRuntimePressureAdapterMapsGatewayChannelSlotAndTransport(t *testing.T) {
	reg := obsmetrics.New(1, "n1")
	gatewayObs := gatewayMetricsObserver{metrics: reg}
	gatewayObs.OnAsyncAuthQueue(gateway.AsyncAuthQueueEvent{Depth: 1, Capacity: 8, Workers: 2})
	gatewayObs.OnAsyncAuthAdmission(gateway.AsyncAuthAdmissionEvent{Result: "ok"})
	gatewayObs.OnAsyncSendAdmission(gateway.AsyncSendAdmissionEvent{Result: "full"})

	channelObs := channelV2MetricsObserver{metrics: reg}
	channelObs.SetWorkerQueueCapacity("store_append", 64)
	channelObs.SetWorkerWorkers("store_append", 4)
	channelObs.ObserveWorkerAdmission("store_append", "ok")
	channelObs.ObserveWorkerWait("store_append", worker.TaskStoreAppend, time.Millisecond)
	channelObs.ObserveWorkerTask("store_append", worker.TaskStoreAppend, nil, time.Millisecond)
	channelObs.SetReactorMailboxCapacity(0, "high", 16)
	channelObs.ObserveReactorMailboxAdmission(0, "high", "full")
	channelObs.SetAppendQueuePressure(reactor.AppendQueuePressureEvent{ReactorID: 0, Depth: 2, Capacity: 32, Bytes: 100, BytesCapacity: 4096})

	slotObs := slotMetricsObserver{metrics: reg}
	slotObs.SetSchedulerWorkers(1)
	slotObs.SetSchedulerInflight(1)
	slotObs.SetSchedulerState(multiraft.SchedulerStateEvent{Depth: 1, Capacity: 1024, Pending: 2, Queued: 3, Processing: 1, Dirty: 1})
	slotObs.ObserveSchedulerAdmission("dirty")
	slotObs.ObserveSchedulerTask("process_slot", time.Millisecond)

	transportObs := transportV2MetricsObserver{metrics: reg}
	transportObs.ObserveTransport(transportv2.Event{Name: "scheduler_queue", Priority: transportv2.PriorityRPC, Items: 3, Capacity: 32, Bytes: 128, BytesCapacity: 4096, Result: "ok"})
	transportObs.ObserveTransport(transportv2.Event{Name: "service_task", ServiceID: 9, Result: "ok", Duration: time.Millisecond})

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}
	names := make(map[string]struct{}, len(families))
	for _, family := range families {
		names[family.GetName()] = struct{}{}
	}
	for _, want := range []string{
		"wukongim_runtime_pool_queue_depth",
		"wukongim_runtime_pool_admission_total",
		"wukongim_runtime_pool_task_duration_seconds",
	} {
		if _, ok := names[want]; !ok {
			t.Fatalf("metric family %q not found", want)
		}
	}
}
```

Add imports:

```go
obsmetrics "github.com/WuKongIM/WuKongIM/pkg/metrics"
"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
transportv2 "github.com/WuKongIM/WuKongIM/pkg/transportv2"
```

Use the existing public aliases from `pkg/transportv2/types.go`:

```go
transportv2.Event
transportv2.PriorityRPC
```

- [ ] **Step 2: Run failing adapter test**

Run:

```bash
GOWORK=off go test ./internalv2/app -run 'TestRuntimePressureAdapterMapsGatewayChannelSlotAndTransport' -count=1
```

Expected: FAIL because adapter methods and observer types do not exist.

- [ ] **Step 3: Implement adapter methods**

In `internalv2/app/observability.go`, add types:

```go
type slotMetricsObserver struct {
	metrics *obsmetrics.Registry
}

type transportV2MetricsObserver struct {
	metrics *obsmetrics.Registry
}
```

Add gateway adapter methods:

```go
func (o gatewayMetricsObserver) OnAsyncAuthQueue(event accessgateway.AsyncAuthQueueEvent) {
	if o.metrics == nil {
		return
	}
	o.metrics.RuntimePressure.SetPoolWorkers("gateway", "async_auth", event.Workers)
	o.metrics.RuntimePressure.SetQueue("gateway", "async_auth", "auth", "none", obsmetrics.RuntimePressureQueueObservation{Depth: event.Depth, Capacity: event.Capacity})
}

func (o gatewayMetricsObserver) OnAsyncAuthAdmission(event accessgateway.AsyncAuthAdmissionEvent) {
	if o.metrics != nil {
		o.metrics.RuntimePressure.ObserveAdmission("gateway", "async_auth", "auth", "none", event.Result)
	}
}

func (o gatewayMetricsObserver) OnAsyncAuthWait(event accessgateway.AsyncAuthWaitEvent) {
	if o.metrics != nil {
		o.metrics.RuntimePressure.ObserveQueueWait("gateway", "async_auth", "auth", "none", "ok", event.Duration)
	}
}

func (o gatewayMetricsObserver) OnAsyncSendAdmission(event accessgateway.AsyncSendAdmissionEvent) {
	if o.metrics != nil {
		o.metrics.RuntimePressure.ObserveAdmission("gateway", "async_send", "send", "none", event.Result)
	}
}

func (o gatewayMetricsObserver) OnTransportPressure(event accessgateway.TransportPressureEvent) {
	if o.metrics == nil {
		return
	}
	pool := runtimePressureFallback(event.Name, "gnet")
	queue := runtimePressureFallback(event.Queue, "transport")
	o.metrics.RuntimePressure.SetQueue("gateway", pool, queue, "none", obsmetrics.RuntimePressureQueueObservation{
		Depth: event.Depth, Capacity: event.Capacity, Bytes: event.Bytes, BytesCapacity: event.BytesCapacity,
	})
	if event.Result != "" {
		o.metrics.RuntimePressure.ObserveAdmission("gateway", pool, queue, "none", event.Result)
	}
}
```

Add ChannelV2 adapter methods for new optional interfaces:

```go
func (o channelV2MetricsObserver) SetWorkerQueueCapacity(pool string, capacity int) {
	if o.metrics != nil {
		o.metrics.RuntimePressure.SetQueueCapacity("channelv2", pool, "worker", "none", capacity)
	}
}

func (o channelV2MetricsObserver) SetWorkerWorkers(pool string, workers int) {
	if o.metrics != nil {
		o.metrics.RuntimePressure.SetPoolWorkers("channelv2", pool, workers)
	}
}

func (o channelV2MetricsObserver) ObserveWorkerAdmission(pool string, result string) {
	if o.metrics != nil {
		o.metrics.RuntimePressure.ObserveAdmission("channelv2", pool, "worker", "none", result)
	}
}

func (o channelV2MetricsObserver) ObserveWorkerWait(pool string, kind worker.TaskKind, d time.Duration) {
	if o.metrics != nil {
		o.metrics.RuntimePressure.ObserveQueueWait("channelv2", pool, "worker", "none", "ok", d)
	}
}

func (o channelV2MetricsObserver) ObserveWorkerTask(pool string, kind worker.TaskKind, err error, d time.Duration) {
	if o.metrics == nil {
		return
	}
	result := "ok"
	if err != nil {
		result = "err"
	}
	o.metrics.RuntimePressure.ObserveTaskDuration("channelv2", pool, channelV2WorkerKindLabel(kind), result, d)
}

func (o channelV2MetricsObserver) SetReactorMailboxCapacity(reactorID int, priority string, capacity int) {
	if o.metrics != nil {
		o.metrics.RuntimePressure.SetQueueCapacity("channelv2", "reactor", "mailbox", priority, capacity)
	}
}

func (o channelV2MetricsObserver) ObserveReactorMailboxAdmission(reactorID int, priority string, result string) {
	if o.metrics != nil {
		o.metrics.RuntimePressure.ObserveAdmission("channelv2", "reactor", "mailbox", priority, result)
	}
}

func (o channelV2MetricsObserver) SetAppendQueuePressure(event reactor.AppendQueuePressureEvent) {
	if o.metrics != nil {
		o.metrics.RuntimePressure.SetQueueDepth("channelv2", "append", "append", "none", event.Depth)
		o.metrics.RuntimePressure.SetQueueCapacity("channelv2", "append", "append", "none", event.Capacity)
		o.metrics.RuntimePressure.SetQueueBytes("channelv2", "append", "append", "none", int64(event.Bytes))
		o.metrics.RuntimePressure.SetQueueBytesCapacity("channelv2", "append", "append", "none", int64(event.BytesCapacity))
	}
}
```

Also extend existing `SetWorkerQueueDepth` and `SetReactorMailboxDepth` methods so they mirror depth into unified metrics:

```go
func (o channelV2MetricsObserver) SetWorkerQueueDepth(pool string, depth int) {
	if o.metrics == nil {
		return
	}
	o.metrics.ChannelV2.SetWorkerQueueDepth(pool, depth)
	o.metrics.RuntimePressure.SetQueueDepth("channelv2", pool, "worker", "none", depth)
}

func (o channelV2MetricsObserver) SetReactorMailboxDepth(reactorID int, priority string, depth int) {
	if o.metrics == nil {
		return
	}
	o.metrics.ChannelV2.SetReactorMailboxDepth(reactorID, priority, depth)
	o.metrics.RuntimePressure.SetQueueDepth("channelv2", "reactor", "mailbox", priority, depth)
}
```

Add Slot adapter:

```go
func (o slotMetricsObserver) SetSchedulerWorkers(workers int) {
	if o.metrics != nil {
		o.metrics.RuntimePressure.SetPoolWorkers("slot", "scheduler", workers)
	}
}

func (o slotMetricsObserver) SetSchedulerInflight(inflight int) {
	if o.metrics != nil {
		o.metrics.RuntimePressure.SetPoolInflight("slot", "scheduler", inflight)
	}
}

func (o slotMetricsObserver) SetSchedulerState(event multiraft.SchedulerStateEvent) {
	if o.metrics != nil {
		o.metrics.RuntimePressure.SetQueue("slot", "scheduler", "scheduler", "none", obsmetrics.RuntimePressureQueueObservation{Depth: event.Depth + event.Pending, Capacity: event.Capacity})
	}
}

func (o slotMetricsObserver) ObserveSchedulerAdmission(result string) {
	if o.metrics != nil {
		o.metrics.RuntimePressure.ObserveAdmission("slot", "scheduler", "scheduler", "none", result)
	}
}

func (o slotMetricsObserver) ObserveSchedulerTask(task string, d time.Duration) {
	if o.metrics != nil {
		o.metrics.RuntimePressure.ObserveTaskDuration("slot", "scheduler", task, "ok", d)
	}
}
```

Add TransportV2 adapter:

```go
func (o transportV2MetricsObserver) ObserveTransport(event transportv2.Event) {
	if o.metrics == nil {
		return
	}
	pool, queue, priority := transportV2RuntimeLabels(event)
	switch event.Name {
	case "scheduler_queue", "service_queue":
		o.metrics.RuntimePressure.SetQueue("transportv2", pool, queue, priority, obsmetrics.RuntimePressureQueueObservation{
			Depth: event.Items, Capacity: event.Capacity, Bytes: int64(event.Bytes), BytesCapacity: event.BytesCapacity,
		})
	case "scheduler_admission", "service_admission":
		o.metrics.RuntimePressure.ObserveAdmission("transportv2", pool, queue, priority, event.Result)
	case "scheduler_wait":
		o.metrics.RuntimePressure.ObserveQueueWait("transportv2", pool, queue, priority, event.Result, event.Duration)
	case "service_task":
		o.metrics.RuntimePressure.ObserveTaskDuration("transportv2", pool, queue, event.Result, event.Duration)
	case "pending_rpc":
		o.metrics.RuntimePressure.SetPoolInflight("transportv2", "rpc", event.Inflight)
	case "peer_pool":
		o.metrics.RuntimePressure.SetPoolWorkers("transportv2", "peer_pool", event.Capacity)
		o.metrics.RuntimePressure.SetPoolInflight("transportv2", "peer_pool", event.Inflight)
	}
}
```

Add label helpers:

```go
func runtimePressureFallback(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func transportV2RuntimeLabels(event transportv2.Event) (pool string, queue string, priority string) {
	priority = transportV2PriorityLabel(event.Priority)
	switch event.Name {
	case "service_queue", "service_admission", "service_task":
		return "service", "service_" + strconv.Itoa(int(event.ServiceID)), "none"
	case "pending_rpc":
		return "rpc", "pending", "none"
	case "peer_pool":
		return "peer_pool", "connections", "none"
	default:
		return "scheduler", "scheduler", priority
	}
}

func transportV2PriorityLabel(priority transportv2.Priority) string {
	switch priority {
	case transportv2.PriorityRaft:
		return "raft"
	case transportv2.PriorityControl:
		return "control"
	case transportv2.PriorityRPC:
		return "rpc"
	case transportv2.PriorityBulk:
		return "bulk"
	default:
		return "none"
	}
}
```

- [ ] **Step 4: Wire observers in `app.go` and cluster config**

In `internalv2/app/app.go`, where metrics observers are wired:

```go
clusterCfg.Channel.Observer = combineChannelV2Observers(clusterCfg.Channel.Observer, channelV2MetricsObserver{metrics: app.metrics})
clusterCfg.Control.RaftObserver = combineControllerRaftObservers(clusterCfg.Control.RaftObserver, controllerRaftMetricsObserver{metrics: app.metrics})
clusterCfg.Slots.Observer = combineSlotObservers(clusterCfg.Slots.Observer, slotMetricsObserver{metrics: app.metrics})
```

When creating `transportv2.ClientConfig` or `transportv2.ServerConfig`, set:

```go
Observer: transportV2MetricsObserver{metrics: app.metrics},
```

Add `multiSlotObserver`:

```go
type multiSlotObserver []multiraft.SchedulerObserver

func combineSlotObservers(first, second multiraft.SchedulerObserver) multiraft.SchedulerObserver {
	if first == nil {
		return second
	}
	if second == nil {
		return first
	}
	return multiSlotObserver{first, second}
}
```

Implement `multiSlotObserver` methods by forwarding to all observers.

Extend `multiChannelV2Observer` to forward new optional ChannelV2 worker/mailbox/append queue methods.

- [ ] **Step 5: Run adapter tests**

Run:

```bash
GOWORK=off go test ./internalv2/app -run 'TestRuntimePressureAdapterMapsGatewayChannelSlotAndTransport|TestChannelV2PullHintMetricLabels|TestMessageAppendMetricErrorLabels' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internalv2/app/observability.go internalv2/app/observability_test.go internalv2/app/app.go
git commit -m "feat(app): map runtime pressure observations"
```

### Task 8: Grafana Runtime Pressure Panels

**Files:**
- Modify: `docker/observability/grafana/dashboards/wukongim-runtime-storage.json`
- Modify: `docker/observability/grafana/dashboard_assets_test.go`

- [ ] **Step 1: Run dashboard coverage to see current failure**

Run:

```bash
GOWORK=off go test ./docker/observability/grafana -run TestDashboardAssetsCoverAllExportedMetrics -count=1
```

Expected: FAIL because new `wukongim_runtime_pool_*` metric names are not referenced by dashboards.

- [ ] **Step 2: Add Runtime Pressure panels**

Edit `docker/observability/grafana/dashboards/wukongim-runtime-storage.json` and add panels whose PromQL expressions reference every new metric:

```promql
wukongim_runtime_pool_inflight / clamp_min(wukongim_runtime_pool_workers, 1)
wukongim_runtime_pool_queue_depth / clamp_min(wukongim_runtime_pool_queue_capacity, 1)
wukongim_runtime_pool_queue_bytes / clamp_min(wukongim_runtime_pool_queue_bytes_capacity, 1)
sum by (component, pool, result) (rate(wukongim_runtime_pool_admission_total[$__rate_interval]))
histogram_quantile(0.95, sum by (le, component, pool, queue) (rate(wukongim_runtime_pool_wait_duration_seconds_bucket[$__rate_interval])))
histogram_quantile(0.99, sum by (le, component, pool, task) (rate(wukongim_runtime_pool_task_duration_seconds_bucket[$__rate_interval])))
```

Panel titles:

```text
Runtime Pool Busy Ratio
Runtime Queue Fill Ratio
Runtime Queue Byte Fill Ratio
Runtime Admission Failures
Runtime Queue Wait p95
Runtime Task Duration p99
```

Keep dashboard JSON valid and preserve existing dashboard UID.

- [ ] **Step 3: Run dashboard tests**

Run:

```bash
GOWORK=off go test ./docker/observability/grafana -run TestDashboardAssetsCoverAllExportedMetrics -count=1
```

Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add docker/observability/grafana/dashboards/wukongim-runtime-storage.json docker/observability/grafana/dashboard_assets_test.go
git commit -m "feat(grafana): add runtime pressure panels"
```

Keep `dashboard_assets_test.go` in the `git add` command; `git add` is harmless when the file content did not change.

### Task 9: Focused Verification And Final Sweep

**Files:**
- Review all changed files.
- Update `FLOW.md` files only when behavior descriptions changed:
  - `pkg/gateway/FLOW.md`
  - `pkg/channelv2/FLOW.md`
  - `pkg/channelv2/reactor/FLOW.md`
  - `pkg/clusterv2/FLOW.md`
  - `pkg/slot/FLOW.md`

- [ ] **Step 1: Search for accidental old transport instrumentation**

Run:

```bash
git diff --name-only HEAD~8..HEAD | rg 'pkg/transport($|/)'
```

Expected: no output. Any reported file under `pkg/transport` is a verification failure; inspect and remove that instrumentation unless the hit is only a doc line saying the package is excluded.

- [ ] **Step 2: Check for high-cardinality labels**

Run:

```bash
rg -n 'channel_id|ChannelID|uid|UID|connID|RemoteAddr|request_id|RequestID|err\\.Error\\(\\)' pkg/metrics internalv2/app pkg/gateway pkg/channelv2 pkg/slot pkg/transportv2
```

Expected: occurrences in existing domain logic are allowed, but no new Runtime Pressure metric label should use raw channel id, uid, connection id, request id, remote address, or raw error strings.

- [ ] **Step 3: Run focused verification**

Run:

```bash
GOWORK=off go test ./pkg/metrics ./pkg/gateway/... ./pkg/channelv2/... ./pkg/slot/... ./pkg/clusterv2 ./pkg/transportv2/... ./internalv2/app ./docker/observability/grafana -count=1
```

Expected: PASS.

- [ ] **Step 4: Run broader package verification**

Run:

```bash
GOWORK=off go test ./pkg/... ./internalv2/... -count=1
```

Expected: PASS.

- [ ] **Step 5: Inspect final diff**

Run:

```bash
git status --short
git diff --stat
git diff --check
```

Expected:
- `git diff --check` prints no whitespace errors.
- Only runtime pressure metrics, observer wiring, Grafana, and required `FLOW.md` docs changed.

- [ ] **Step 6: Final commit for verification fixes**

When Step 5 has additional unstaged fixes:

```bash
git add <changed-files>
git commit -m "chore: polish runtime pressure metrics"
```

When there are no additional fixes, do not create an empty commit.
