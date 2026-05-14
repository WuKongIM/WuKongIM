package metrics

import (
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

type ChannelSnapshot struct {
	ActiveChannels int64
	MaxChannels    int64
}

type ChannelMetrics struct {
	appendTotal             *prometheus.CounterVec
	appendDuration          prometheus.Histogram
	fetchTotal              prometheus.Counter
	fetchDuration           prometheus.Histogram
	activeChannels          prometheus.Gauge
	maxChannels             prometheus.Gauge
	activationRejectedTotal *prometheus.CounterVec
	idleEvictionsTotal      prometheus.Counter
	executionQueueDepth     prometheus.Gauge
	executionEnqueueTotal   *prometheus.CounterVec
	executionWorkerBusy     prometheus.Gauge
	executionMailboxWait    prometheus.Histogram
	mu                      sync.Mutex
	activeChannelsV         int64
	maxChannelsV            int64
}

func newChannelMetrics(registry prometheus.Registerer, labels prometheus.Labels) *ChannelMetrics {
	m := &ChannelMetrics{
		appendTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_channel_append_total",
			Help:        "Total number of channel append operations.",
			ConstLabels: labels,
		}, []string{"result"}),
		appendDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:        "wukongim_channel_append_duration_seconds",
			Help:        "Channel append latency in seconds.",
			ConstLabels: labels,
			Buckets:     gatewayFrameDurationBuckets,
		}),
		fetchTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name:        "wukongim_channel_fetch_total",
			Help:        "Total number of channel fetch operations.",
			ConstLabels: labels,
		}),
		fetchDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:        "wukongim_channel_fetch_duration_seconds",
			Help:        "Channel fetch latency in seconds.",
			ConstLabels: labels,
			Buckets:     gatewayFrameDurationBuckets,
		}),
		activeChannels: prometheus.NewGauge(prometheus.GaugeOpts{
			Name:        "wukongim_channel_active_channels",
			Help:        "Number of active local channel runtimes.",
			ConstLabels: labels,
		}),
		maxChannels: prometheus.NewGauge(prometheus.GaugeOpts{
			Name:        "wukongim_channel_max_channels",
			Help:        "Configured maximum number of active local channel runtimes on this node. A value of 0 means unlimited.",
			ConstLabels: labels,
		}),
		activationRejectedTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_channel_activation_rejected_total",
			Help:        "Total number of local channel runtime activation attempts rejected before creating a runtime.",
			ConstLabels: labels,
		}, []string{"reason"}),
		idleEvictionsTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name:        "wukongim_channel_idle_evictions_total",
			Help:        "Total number of idle local channel runtimes evicted from this node.",
			ConstLabels: labels,
		}),
		executionQueueDepth: prometheus.NewGauge(prometheus.GaugeOpts{
			Name:        "wukongim_channel_execution_queue_depth",
			Help:        "Current queued work depth across local pooled channel replica execution.",
			ConstLabels: labels,
		}),
		executionEnqueueTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_channel_execution_enqueue_total",
			Help:        "Total number of local pooled channel replica execution enqueue attempts.",
			ConstLabels: labels,
		}, []string{"result"}),
		executionWorkerBusy: prometheus.NewGauge(prometheus.GaugeOpts{
			Name:        "wukongim_channel_execution_worker_busy_ratio",
			Help:        "Approximate busy ratio for local pooled channel replica execution workers.",
			ConstLabels: labels,
		}),
		executionMailboxWait: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:        "wukongim_channel_execution_mailbox_wait_duration_seconds",
			Help:        "Time local channel replica work waits in pooled execution mailboxes.",
			ConstLabels: labels,
			Buckets:     gatewayFrameDurationBuckets,
		}),
	}

	registry.MustRegister(
		m.appendTotal,
		m.appendDuration,
		m.fetchTotal,
		m.fetchDuration,
		m.activeChannels,
		m.maxChannels,
		m.activationRejectedTotal,
		m.idleEvictionsTotal,
		m.executionQueueDepth,
		m.executionEnqueueTotal,
		m.executionWorkerBusy,
		m.executionMailboxWait,
	)

	return m
}

func (m *ChannelMetrics) ObserveAppend(result string, dur time.Duration) {
	if m == nil {
		return
	}
	m.appendTotal.WithLabelValues(result).Inc()
	m.appendDuration.Observe(dur.Seconds())
}

func (m *ChannelMetrics) ObserveFetch(dur time.Duration) {
	if m == nil {
		return
	}
	m.fetchTotal.Inc()
	m.fetchDuration.Observe(dur.Seconds())
}

func (m *ChannelMetrics) SetActiveChannels(v int) {
	if m == nil {
		return
	}
	m.activeChannels.Set(float64(v))
	m.mu.Lock()
	m.activeChannelsV = int64(v)
	m.mu.Unlock()
}

func (m *ChannelMetrics) SetMaxChannels(v int) {
	if m == nil {
		return
	}
	if v < 0 {
		v = 0
	}
	m.maxChannels.Set(float64(v))
	m.mu.Lock()
	m.maxChannelsV = int64(v)
	m.mu.Unlock()
}

func (m *ChannelMetrics) ObserveActivationRejected(reason string) {
	if m == nil {
		return
	}
	if reason == "" {
		reason = "unknown"
	}
	m.activationRejectedTotal.WithLabelValues(reason).Inc()
}

func (m *ChannelMetrics) ObserveIdleEvict() {
	if m == nil {
		return
	}
	m.idleEvictionsTotal.Inc()
}

func (m *ChannelMetrics) SetExecutionQueueDepth(v int) {
	if m == nil {
		return
	}
	if v < 0 {
		v = 0
	}
	m.executionQueueDepth.Set(float64(v))
}

func (m *ChannelMetrics) ObserveExecutionEnqueue(result string) {
	if m == nil {
		return
	}
	if result == "" {
		result = "unknown"
	}
	m.executionEnqueueTotal.WithLabelValues(result).Inc()
}

func (m *ChannelMetrics) SetExecutionWorkerBusyRatio(v float64) {
	if m == nil {
		return
	}
	if v < 0 {
		v = 0
	}
	if v > 1 {
		v = 1
	}
	m.executionWorkerBusy.Set(v)
}

func (m *ChannelMetrics) ObserveExecutionMailboxWait(d time.Duration) {
	if m == nil {
		return
	}
	if d < 0 {
		d = 0
	}
	m.executionMailboxWait.Observe(d.Seconds())
}

func (m *ChannelMetrics) Snapshot() ChannelSnapshot {
	if m == nil {
		return ChannelSnapshot{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return ChannelSnapshot{ActiveChannels: m.activeChannelsV, MaxChannels: m.maxChannelsV}
}
