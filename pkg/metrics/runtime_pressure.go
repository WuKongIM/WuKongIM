package metrics

import (
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

var runtimePressureDurationBuckets = []float64{0.0005, 0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30}

// RuntimePressureQueueObservation captures current pressure gauges for a bounded runtime queue.
type RuntimePressureQueueObservation struct {
	// Depth is the current number of queued items.
	Depth int
	// Capacity is the configured maximum number of queued items.
	Capacity int
	// Bytes is the current number of queued payload bytes.
	Bytes int64
	// BytesCapacity is the configured maximum number of queued payload bytes.
	BytesCapacity int64
}

// RuntimePressureMetrics exposes low-cardinality pressure metrics for bounded runtime pools and queues.
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

	// seriesMu protects cached Prometheus children after their normalized label sets are bound.
	// The caches mirror MetricVec children and do not introduce additional label combinations.
	seriesMu        sync.RWMutex
	poolSeries      map[runtimePressurePoolKey]runtimePressurePoolSeries
	queueSeries     map[runtimePressureQueueKey]runtimePressureQueueSeries
	admissionSeries map[runtimePressureResultKey]prometheus.Counter
	waitSeries      map[runtimePressureResultKey]prometheus.Observer
	taskSeries      map[runtimePressureTaskKey]prometheus.Observer
}

type runtimePressurePoolKey struct {
	component string
	pool      string
}

type runtimePressureQueueKey struct {
	component string
	pool      string
	queue     string
	priority  string
}

type runtimePressureResultKey struct {
	runtimePressureQueueKey
	result string
}

type runtimePressureTaskKey struct {
	component string
	pool      string
	task      string
	result    string
}

type runtimePressurePoolSeries struct {
	workers  prometheus.Gauge
	inflight prometheus.Gauge
}

type runtimePressureQueueSeries struct {
	depth         prometheus.Gauge
	capacity      prometheus.Gauge
	bytes         prometheus.Gauge
	bytesCapacity prometheus.Gauge
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
	if m == nil || m.poolWorkers == nil {
		return
	}
	series := m.boundPoolSeries(component, pool)
	series.workers.Set(float64(clampRuntimePressureInt(workers)))
}

func (m *RuntimePressureMetrics) SetPoolInflight(component, pool string, inflight int) {
	if m == nil || m.poolInflight == nil {
		return
	}
	series := m.boundPoolSeries(component, pool)
	series.inflight.Set(float64(clampRuntimePressureInt(inflight)))
}

func (m *RuntimePressureMetrics) SetQueue(component, pool, queue, priority string, obs RuntimePressureQueueObservation) {
	if m == nil {
		return
	}
	series := m.boundQueueSeries(component, pool, queue, priority)
	if series.depth != nil {
		series.depth.Set(float64(clampRuntimePressureInt(obs.Depth)))
	}
	if series.capacity != nil {
		series.capacity.Set(float64(clampRuntimePressureInt(obs.Capacity)))
	}
	if series.bytes != nil {
		series.bytes.Set(float64(clampRuntimePressureInt64(obs.Bytes)))
	}
	if series.bytesCapacity != nil {
		series.bytesCapacity.Set(float64(clampRuntimePressureInt64(obs.BytesCapacity)))
	}
}

func (m *RuntimePressureMetrics) SetQueueDepth(component, pool, queue, priority string, depth int) {
	if m == nil || m.queueDepth == nil {
		return
	}
	series := m.boundQueueSeries(component, pool, queue, priority)
	series.depth.Set(float64(clampRuntimePressureInt(depth)))
}

func (m *RuntimePressureMetrics) SetQueueCapacity(component, pool, queue, priority string, capacity int) {
	if m == nil || m.queueCapacity == nil {
		return
	}
	series := m.boundQueueSeries(component, pool, queue, priority)
	series.capacity.Set(float64(clampRuntimePressureInt(capacity)))
}

func (m *RuntimePressureMetrics) SetQueueBytes(component, pool, queue, priority string, bytes int64) {
	if m == nil || m.queueBytes == nil {
		return
	}
	series := m.boundQueueSeries(component, pool, queue, priority)
	series.bytes.Set(float64(clampRuntimePressureInt64(bytes)))
}

func (m *RuntimePressureMetrics) SetQueueBytesCapacity(component, pool, queue, priority string, capacity int64) {
	if m == nil || m.queueBytesCapacity == nil {
		return
	}
	series := m.boundQueueSeries(component, pool, queue, priority)
	series.bytesCapacity.Set(float64(clampRuntimePressureInt64(capacity)))
}

func (m *RuntimePressureMetrics) ObserveAdmission(component, pool, queue, priority, result string) {
	if m == nil || m.admissionTotal == nil {
		return
	}
	m.boundAdmissionCounter(component, pool, queue, priority, result).Inc()
}

func (m *RuntimePressureMetrics) ObserveQueueWait(component, pool, queue, priority, result string, d time.Duration) {
	if m == nil || m.waitDuration == nil {
		return
	}
	m.boundWaitObserver(component, pool, queue, priority, result).Observe(clampRuntimePressureDuration(d).Seconds())
}

func (m *RuntimePressureMetrics) ObserveTaskDuration(component, pool, task, result string, d time.Duration) {
	if m == nil || m.taskDuration == nil {
		return
	}
	m.boundTaskObserver(component, pool, task, result).Observe(clampRuntimePressureDuration(d).Seconds())
}

func (m *RuntimePressureMetrics) boundPoolSeries(component, pool string) runtimePressurePoolSeries {
	key := runtimePressurePoolKey{
		component: normalizeRuntimePressureLabel(component),
		pool:      normalizeRuntimePressureLabel(pool),
	}
	m.seriesMu.RLock()
	series, ok := m.poolSeries[key]
	m.seriesMu.RUnlock()
	if ok {
		return series
	}

	m.seriesMu.Lock()
	defer m.seriesMu.Unlock()
	if series, ok = m.poolSeries[key]; ok {
		return series
	}
	if m.poolWorkers != nil {
		series.workers = m.poolWorkers.WithLabelValues(key.component, key.pool)
	}
	if m.poolInflight != nil {
		series.inflight = m.poolInflight.WithLabelValues(key.component, key.pool)
	}
	if m.poolSeries == nil {
		m.poolSeries = make(map[runtimePressurePoolKey]runtimePressurePoolSeries)
	}
	m.poolSeries[key] = series
	return series
}

func (m *RuntimePressureMetrics) boundQueueSeries(component, pool, queue, priority string) runtimePressureQueueSeries {
	key := runtimePressureQueueKey{
		component: normalizeRuntimePressureLabel(component),
		pool:      normalizeRuntimePressureLabel(pool),
		queue:     normalizeRuntimePressureLabel(queue),
		priority:  normalizeRuntimePressurePriority(priority),
	}
	m.seriesMu.RLock()
	series, ok := m.queueSeries[key]
	m.seriesMu.RUnlock()
	if ok {
		return series
	}

	m.seriesMu.Lock()
	defer m.seriesMu.Unlock()
	if series, ok = m.queueSeries[key]; ok {
		return series
	}
	if m.queueDepth != nil {
		series.depth = m.queueDepth.WithLabelValues(key.component, key.pool, key.queue, key.priority)
	}
	if m.queueCapacity != nil {
		series.capacity = m.queueCapacity.WithLabelValues(key.component, key.pool, key.queue, key.priority)
	}
	if m.queueBytes != nil {
		series.bytes = m.queueBytes.WithLabelValues(key.component, key.pool, key.queue, key.priority)
	}
	if m.queueBytesCapacity != nil {
		series.bytesCapacity = m.queueBytesCapacity.WithLabelValues(key.component, key.pool, key.queue, key.priority)
	}
	if m.queueSeries == nil {
		m.queueSeries = make(map[runtimePressureQueueKey]runtimePressureQueueSeries)
	}
	m.queueSeries[key] = series
	return series
}

func (m *RuntimePressureMetrics) boundAdmissionCounter(component, pool, queue, priority, result string) prometheus.Counter {
	key := runtimePressureResultKey{
		runtimePressureQueueKey: runtimePressureQueueKey{
			component: normalizeRuntimePressureLabel(component),
			pool:      normalizeRuntimePressureLabel(pool),
			queue:     normalizeRuntimePressureLabel(queue),
			priority:  normalizeRuntimePressurePriority(priority),
		},
		result: NormalizeRuntimePressureResult(result),
	}
	m.seriesMu.RLock()
	series, ok := m.admissionSeries[key]
	m.seriesMu.RUnlock()
	if ok {
		return series
	}

	m.seriesMu.Lock()
	defer m.seriesMu.Unlock()
	if series, ok = m.admissionSeries[key]; ok {
		return series
	}
	series = m.admissionTotal.WithLabelValues(key.component, key.pool, key.queue, key.priority, key.result)
	if m.admissionSeries == nil {
		m.admissionSeries = make(map[runtimePressureResultKey]prometheus.Counter)
	}
	m.admissionSeries[key] = series
	return series
}

func (m *RuntimePressureMetrics) boundWaitObserver(component, pool, queue, priority, result string) prometheus.Observer {
	key := runtimePressureResultKey{
		runtimePressureQueueKey: runtimePressureQueueKey{
			component: normalizeRuntimePressureLabel(component),
			pool:      normalizeRuntimePressureLabel(pool),
			queue:     normalizeRuntimePressureLabel(queue),
			priority:  normalizeRuntimePressurePriority(priority),
		},
		result: NormalizeRuntimePressureResult(result),
	}
	m.seriesMu.RLock()
	series, ok := m.waitSeries[key]
	m.seriesMu.RUnlock()
	if ok {
		return series
	}

	m.seriesMu.Lock()
	defer m.seriesMu.Unlock()
	if series, ok = m.waitSeries[key]; ok {
		return series
	}
	series = m.waitDuration.WithLabelValues(key.component, key.pool, key.queue, key.priority, key.result)
	if m.waitSeries == nil {
		m.waitSeries = make(map[runtimePressureResultKey]prometheus.Observer)
	}
	m.waitSeries[key] = series
	return series
}

func (m *RuntimePressureMetrics) boundTaskObserver(component, pool, task, result string) prometheus.Observer {
	key := runtimePressureTaskKey{
		component: normalizeRuntimePressureLabel(component),
		pool:      normalizeRuntimePressureLabel(pool),
		task:      normalizeRuntimePressureLabel(task),
		result:    NormalizeRuntimePressureResult(result),
	}
	m.seriesMu.RLock()
	series, ok := m.taskSeries[key]
	m.seriesMu.RUnlock()
	if ok {
		return series
	}

	m.seriesMu.Lock()
	defer m.seriesMu.Unlock()
	if series, ok = m.taskSeries[key]; ok {
		return series
	}
	series = m.taskDuration.WithLabelValues(key.component, key.pool, key.task, key.result)
	if m.taskSeries == nil {
		m.taskSeries = make(map[runtimePressureTaskKey]prometheus.Observer)
	}
	m.taskSeries[key] = series
	return series
}

// NormalizeRuntimePressureResult maps raw outcomes into the stable runtime pressure result label set.
func NormalizeRuntimePressureResult(result string) string {
	switch result {
	case "ok",
		"full",
		"busy",
		"closed",
		"canceled",
		"timeout",
		"too_large",
		"invalid",
		"dropped",
		"coalesced",
		"stopped",
		"dirty",
		"requeued",
		"err":
		return result
	default:
		return "other"
	}
}

func normalizeRuntimePressureLabel(value string) string {
	if value == "" {
		return "unknown"
	}
	return value
}

func normalizeRuntimePressurePriority(priority string) string {
	if priority == "" {
		return "none"
	}
	return priority
}

func clampRuntimePressureInt(value int) int {
	if value < 0 {
		return 0
	}
	return value
}

func clampRuntimePressureInt64(value int64) int64 {
	if value < 0 {
		return 0
	}
	return value
}

func clampRuntimePressureDuration(d time.Duration) time.Duration {
	if d < 0 {
		return 0
	}
	return d
}
