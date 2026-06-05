package metrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

var runtimePressureDurationBuckets = []float64{0.0005, 0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5}

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
	m.poolWorkers.WithLabelValues(
		normalizeRuntimePressureLabel(component),
		normalizeRuntimePressureLabel(pool),
	).Set(float64(clampRuntimePressureInt(workers)))
}

func (m *RuntimePressureMetrics) SetPoolInflight(component, pool string, inflight int) {
	if m == nil || m.poolInflight == nil {
		return
	}
	m.poolInflight.WithLabelValues(
		normalizeRuntimePressureLabel(component),
		normalizeRuntimePressureLabel(pool),
	).Set(float64(clampRuntimePressureInt(inflight)))
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
	if m == nil || m.queueDepth == nil {
		return
	}
	m.queueDepth.WithLabelValues(
		normalizeRuntimePressureLabel(component),
		normalizeRuntimePressureLabel(pool),
		normalizeRuntimePressureLabel(queue),
		normalizeRuntimePressurePriority(priority),
	).Set(float64(clampRuntimePressureInt(depth)))
}

func (m *RuntimePressureMetrics) SetQueueCapacity(component, pool, queue, priority string, capacity int) {
	if m == nil || m.queueCapacity == nil {
		return
	}
	m.queueCapacity.WithLabelValues(
		normalizeRuntimePressureLabel(component),
		normalizeRuntimePressureLabel(pool),
		normalizeRuntimePressureLabel(queue),
		normalizeRuntimePressurePriority(priority),
	).Set(float64(clampRuntimePressureInt(capacity)))
}

func (m *RuntimePressureMetrics) SetQueueBytes(component, pool, queue, priority string, bytes int64) {
	if m == nil || m.queueBytes == nil {
		return
	}
	m.queueBytes.WithLabelValues(
		normalizeRuntimePressureLabel(component),
		normalizeRuntimePressureLabel(pool),
		normalizeRuntimePressureLabel(queue),
		normalizeRuntimePressurePriority(priority),
	).Set(float64(clampRuntimePressureInt64(bytes)))
}

func (m *RuntimePressureMetrics) SetQueueBytesCapacity(component, pool, queue, priority string, capacity int64) {
	if m == nil || m.queueBytesCapacity == nil {
		return
	}
	m.queueBytesCapacity.WithLabelValues(
		normalizeRuntimePressureLabel(component),
		normalizeRuntimePressureLabel(pool),
		normalizeRuntimePressureLabel(queue),
		normalizeRuntimePressurePriority(priority),
	).Set(float64(clampRuntimePressureInt64(capacity)))
}

func (m *RuntimePressureMetrics) ObserveAdmission(component, pool, queue, priority, result string) {
	if m == nil || m.admissionTotal == nil {
		return
	}
	m.admissionTotal.WithLabelValues(
		normalizeRuntimePressureLabel(component),
		normalizeRuntimePressureLabel(pool),
		normalizeRuntimePressureLabel(queue),
		normalizeRuntimePressurePriority(priority),
		NormalizeRuntimePressureResult(result),
	).Inc()
}

func (m *RuntimePressureMetrics) ObserveQueueWait(component, pool, queue, priority, result string, d time.Duration) {
	if m == nil || m.waitDuration == nil {
		return
	}
	m.waitDuration.WithLabelValues(
		normalizeRuntimePressureLabel(component),
		normalizeRuntimePressureLabel(pool),
		normalizeRuntimePressureLabel(queue),
		normalizeRuntimePressurePriority(priority),
		NormalizeRuntimePressureResult(result),
	).Observe(clampRuntimePressureDuration(d).Seconds())
}

func (m *RuntimePressureMetrics) ObserveTaskDuration(component, pool, task, result string, d time.Duration) {
	if m == nil || m.taskDuration == nil {
		return
	}
	m.taskDuration.WithLabelValues(
		normalizeRuntimePressureLabel(component),
		normalizeRuntimePressureLabel(pool),
		normalizeRuntimePressureLabel(task),
		NormalizeRuntimePressureResult(result),
	).Observe(clampRuntimePressureDuration(d).Seconds())
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
