package metrics

import (
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// MessageSnapshot captures the latest gauge values owned by MessageMetrics.
type MessageSnapshot struct {
	// CommittedDispatchQueueDepthByShard is the latest committed dispatch queue depth per shard.
	CommittedDispatchQueueDepthByShard map[string]int64
	// CommittedReplayLagByChannelType is the latest committed replay lag per channel type.
	CommittedReplayLagByChannelType map[string]uint64
}

// MessageMetrics exposes message send, metadata, dispatch, and replay metrics.
type MessageMetrics struct {
	metaRefreshTotal               *prometheus.CounterVec
	metaRefreshDuration            *prometheus.HistogramVec
	appendTotal                    *prometheus.CounterVec
	appendDuration                 *prometheus.HistogramVec
	committedDispatchQueueDepth    *prometheus.GaugeVec
	committedDispatchEnqueueTotal  *prometheus.CounterVec
	committedDispatchOverflowTotal *prometheus.CounterVec
	committedReplayLagMessages     *prometheus.GaugeVec
	committedReplayPassDuration    *prometheus.HistogramVec
	mu                             sync.Mutex
	dispatchQueueDepthByShard      map[string]int64
	replayLagMessagesByChannelType map[string]uint64
}

func newMessageMetrics(registry prometheus.Registerer, labels prometheus.Labels) *MessageMetrics {
	m := &MessageMetrics{
		metaRefreshTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_message_meta_refresh_total",
			Help:        "Total number of message metadata refresh attempts.",
			ConstLabels: labels,
		}, []string{"result"}),
		metaRefreshDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_message_meta_refresh_duration_seconds",
			Help:        "Message metadata refresh latency in seconds.",
			ConstLabels: labels,
			Buckets:     gatewayFrameDurationBuckets,
		}, []string{"result"}),
		appendTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_message_append_total",
			Help:        "Total number of message append attempts.",
			ConstLabels: labels,
		}, []string{"path", "result"}),
		appendDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_message_append_duration_seconds",
			Help:        "Message append latency in seconds.",
			ConstLabels: labels,
			Buckets:     gatewayFrameDurationBuckets,
		}, []string{"path", "result"}),
		committedDispatchQueueDepth: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_message_committed_dispatch_queue_depth",
			Help:        "Current committed message dispatch queue depth grouped by shard.",
			ConstLabels: labels,
		}, []string{"shard"}),
		committedDispatchEnqueueTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_message_committed_dispatch_enqueue_total",
			Help:        "Total number of committed message dispatch enqueue attempts.",
			ConstLabels: labels,
		}, []string{"shard", "result"}),
		committedDispatchOverflowTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_message_committed_dispatch_overflow_total",
			Help:        "Total number of committed message dispatch queue overflows.",
			ConstLabels: labels,
		}, []string{"shard"}),
		committedReplayLagMessages: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_message_committed_replay_lag_messages",
			Help:        "Current committed message replay lag in messages grouped by channel type.",
			ConstLabels: labels,
		}, []string{"channel_type"}),
		committedReplayPassDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_message_committed_replay_pass_duration_seconds",
			Help:        "Committed message replay pass latency in seconds.",
			ConstLabels: labels,
			Buckets:     gatewayFrameDurationBuckets,
		}, []string{"result"}),
		dispatchQueueDepthByShard:      make(map[string]int64),
		replayLagMessagesByChannelType: make(map[string]uint64),
	}

	registry.MustRegister(
		m.metaRefreshTotal,
		m.metaRefreshDuration,
		m.appendTotal,
		m.appendDuration,
		m.committedDispatchQueueDepth,
		m.committedDispatchEnqueueTotal,
		m.committedDispatchOverflowTotal,
		m.committedReplayLagMessages,
		m.committedReplayPassDuration,
	)

	return m
}

// ObserveMetaRefresh records a metadata refresh attempt and its latency.
func (m *MessageMetrics) ObserveMetaRefresh(result string, dur time.Duration) {
	if m == nil {
		return
	}
	m.metaRefreshTotal.WithLabelValues(result).Inc()
	m.metaRefreshDuration.WithLabelValues(result).Observe(dur.Seconds())
}

// ObserveAppend records a message append attempt and its latency.
func (m *MessageMetrics) ObserveAppend(path, result string, dur time.Duration) {
	if m == nil {
		return
	}
	m.appendTotal.WithLabelValues(path, result).Inc()
	m.appendDuration.WithLabelValues(path, result).Observe(dur.Seconds())
}

// SetCommittedDispatchQueueDepth sets the current committed dispatch queue depth for a shard.
func (m *MessageMetrics) SetCommittedDispatchQueueDepth(shard string, depth int) {
	if m == nil {
		return
	}
	m.committedDispatchQueueDepth.WithLabelValues(shard).Set(float64(depth))
	m.mu.Lock()
	m.dispatchQueueDepthByShard[shard] = int64(depth)
	m.mu.Unlock()
}

// ObserveCommittedDispatchEnqueue records a committed dispatch enqueue attempt.
func (m *MessageMetrics) ObserveCommittedDispatchEnqueue(shard, result string) {
	if m == nil {
		return
	}
	m.committedDispatchEnqueueTotal.WithLabelValues(shard, result).Inc()
}

// ObserveCommittedDispatchOverflow records a committed dispatch queue overflow.
func (m *MessageMetrics) ObserveCommittedDispatchOverflow(shard string) {
	if m == nil {
		return
	}
	m.committedDispatchOverflowTotal.WithLabelValues(shard).Inc()
}

// SetCommittedReplayLag sets the current committed replay lag for a channel type.
func (m *MessageMetrics) SetCommittedReplayLag(channelType string, lag uint64) {
	if m == nil {
		return
	}
	m.committedReplayLagMessages.WithLabelValues(channelType).Set(float64(lag))
	m.mu.Lock()
	m.replayLagMessagesByChannelType[channelType] = lag
	m.mu.Unlock()
}

// ObserveCommittedReplayPass records the latency of a committed replay pass.
func (m *MessageMetrics) ObserveCommittedReplayPass(result string, dur time.Duration) {
	if m == nil {
		return
	}
	m.committedReplayPassDuration.WithLabelValues(result).Observe(dur.Seconds())
}

// Snapshot returns a copy of the latest message gauge values.
func (m *MessageMetrics) Snapshot() MessageSnapshot {
	if m == nil {
		return MessageSnapshot{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	out := MessageSnapshot{
		CommittedDispatchQueueDepthByShard: make(map[string]int64, len(m.dispatchQueueDepthByShard)),
		CommittedReplayLagByChannelType:    make(map[string]uint64, len(m.replayLagMessagesByChannelType)),
	}
	for shard, depth := range m.dispatchQueueDepthByShard {
		out.CommittedDispatchQueueDepthByShard[shard] = depth
	}
	for channelType, lag := range m.replayLagMessagesByChannelType {
		out.CommittedReplayLagByChannelType[channelType] = lag
	}
	return out
}
