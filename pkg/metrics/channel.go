package metrics

import (
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

type ChannelSnapshot struct {
	ActiveChannels int64
}

type ChannelMetrics struct {
	appendTotal     *prometheus.CounterVec
	appendDuration  prometheus.Histogram
	fetchTotal      prometheus.Counter
	fetchDuration   prometheus.Histogram
	activeChannels  prometheus.Gauge
	mu              sync.Mutex
	activeChannelsV int64
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
	}

	registry.MustRegister(
		m.appendTotal,
		m.appendDuration,
		m.fetchTotal,
		m.fetchDuration,
		m.activeChannels,
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

func (m *ChannelMetrics) Snapshot() ChannelSnapshot {
	if m == nil {
		return ChannelSnapshot{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return ChannelSnapshot{ActiveChannels: m.activeChannelsV}
}
