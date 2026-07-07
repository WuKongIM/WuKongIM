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

// MessageEventStreamCacheObservation reports current message event stream-cache pressure.
type MessageEventStreamCacheObservation struct {
	// Sessions is the number of message stream sessions retained by the cache.
	Sessions int
	// OpenLanes is the number of non-terminal stream lanes currently buffered.
	OpenLanes int
	// PayloadBytes is the total cached snapshot payload bytes retained in memory.
	PayloadBytes int64
	// MaxSessions is the configured maximum number of retained stream sessions.
	MaxSessions int
}

// MessageMetrics exposes message send, metadata, dispatch, and replay metrics.
type MessageMetrics struct {
	metaRefreshTotal               *prometheus.CounterVec
	metaRefreshDuration            *prometheus.HistogramVec
	appendTotal                    *prometheus.CounterVec
	appendDuration                 *prometheus.HistogramVec
	appendErrorTotal               *prometheus.CounterVec
	eventAppendTotal               *prometheus.CounterVec
	eventAppendDuration            *prometheus.HistogramVec
	eventAppendStageDuration       *prometheus.HistogramVec
	eventProposeTotal              *prometheus.CounterVec
	eventProposeDuration           *prometheus.HistogramVec
	eventProposeStageDuration      *prometheus.HistogramVec
	eventProposeBatchEvents        *prometheus.HistogramVec
	eventStreamCacheSessions       prometheus.Gauge
	eventStreamCacheOpenLanes      prometheus.Gauge
	eventStreamCachePayloadBytes   prometheus.Gauge
	eventStreamCacheMaxSessions    prometheus.Gauge
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
		appendErrorTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_message_append_errors_total",
			Help:        "Total number of failed message append attempts grouped by path and error class.",
			ConstLabels: labels,
		}, []string{"path", "class"}),
		eventAppendTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_message_event_append_total",
			Help:        "Total number of message event append attempts grouped by low-cardinality path, event type, and result.",
			ConstLabels: labels,
		}, []string{"path", "event_type", "result"}),
		eventAppendDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_message_event_append_duration_seconds",
			Help:        "Message event append latency in seconds grouped by low-cardinality path, event type, and result.",
			ConstLabels: labels,
			Buckets:     gatewayFrameDurationBuckets,
		}, []string{"path", "event_type", "result"}),
		eventAppendStageDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_message_event_append_stage_duration_seconds",
			Help:        "Message event append stage latency in seconds grouped by low-cardinality path, result, and stage.",
			ConstLabels: labels,
			Buckets:     gatewayFrameDurationBuckets,
		}, []string{"path", "result", "stage"}),
		eventProposeTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_message_event_propose_total",
			Help:        "Total number of durable message event Slot proposals grouped by path and result.",
			ConstLabels: labels,
		}, []string{"path", "result"}),
		eventProposeDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_message_event_propose_duration_seconds",
			Help:        "Durable message event Slot proposal latency in seconds grouped by path and result.",
			ConstLabels: labels,
			Buckets:     gatewayFrameDurationBuckets,
		}, []string{"path", "result"}),
		eventProposeStageDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_message_event_propose_stage_duration_seconds",
			Help:        "Durable message event Slot proposal stage latency in seconds grouped by low-cardinality path, result, and stage.",
			ConstLabels: labels,
			Buckets:     gatewayFrameDurationBuckets,
		}, []string{"path", "result", "stage"}),
		eventProposeBatchEvents: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_message_event_propose_batch_events",
			Help:        "Number of message event updates carried by one durable Slot proposal.",
			ConstLabels: labels,
			Buckets:     channelRuntimeAppendBatchRecordBuckets,
		}, []string{"path", "result"}),
		eventStreamCacheSessions: prometheus.NewGauge(prometheus.GaugeOpts{
			Name:        "wukongim_message_event_stream_cache_sessions",
			Help:        "Current number of retained message event stream sessions.",
			ConstLabels: labels,
		}),
		eventStreamCacheOpenLanes: prometheus.NewGauge(prometheus.GaugeOpts{
			Name:        "wukongim_message_event_stream_cache_open_lanes",
			Help:        "Current number of non-terminal message event stream lanes buffered in memory.",
			ConstLabels: labels,
		}),
		eventStreamCachePayloadBytes: prometheus.NewGauge(prometheus.GaugeOpts{
			Name:        "wukongim_message_event_stream_cache_payload_bytes",
			Help:        "Current cached message event snapshot payload bytes retained in memory.",
			ConstLabels: labels,
		}),
		eventStreamCacheMaxSessions: prometheus.NewGauge(prometheus.GaugeOpts{
			Name:        "wukongim_message_event_stream_cache_max_sessions",
			Help:        "Configured maximum number of retained message event stream sessions.",
			ConstLabels: labels,
		}),
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
		m.appendErrorTotal,
		m.eventAppendTotal,
		m.eventAppendDuration,
		m.eventAppendStageDuration,
		m.eventProposeTotal,
		m.eventProposeDuration,
		m.eventProposeStageDuration,
		m.eventProposeBatchEvents,
		m.eventStreamCacheSessions,
		m.eventStreamCacheOpenLanes,
		m.eventStreamCachePayloadBytes,
		m.eventStreamCacheMaxSessions,
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

// ObserveAppendError records a failed message append attempt by low-cardinality class.
func (m *MessageMetrics) ObserveAppendError(path, class string) {
	if m == nil {
		return
	}
	if class == "" {
		class = "unknown"
	}
	m.appendErrorTotal.WithLabelValues(path, class).Inc()
}

// ObserveEventAppend records one message event append attempt and its latency.
func (m *MessageMetrics) ObserveEventAppend(path, eventType, result string, dur time.Duration) {
	if m == nil {
		return
	}
	path = boundedMessageEventPath(path)
	eventType = boundedMessageEventType(eventType)
	result = boundedMessageEventResult(result)
	m.eventAppendTotal.WithLabelValues(path, eventType, result).Inc()
	m.eventAppendDuration.WithLabelValues(path, eventType, result).Observe(dur.Seconds())
}

// ObserveEventAppendStage records one bounded message event append stage latency.
func (m *MessageMetrics) ObserveEventAppendStage(path, result, stage string, dur time.Duration) {
	if m == nil {
		return
	}
	path = boundedMessageEventPath(path)
	result = boundedMessageEventResult(result)
	stage = boundedMessageEventAppendStage(stage)
	m.eventAppendStageDuration.WithLabelValues(path, result, stage).Observe(dur.Seconds())
}

// ObserveEventPropose records one durable message event Slot proposal and its batch size.
func (m *MessageMetrics) ObserveEventPropose(path, result string, batchSize int, dur time.Duration) {
	if m == nil {
		return
	}
	if batchSize < 0 {
		batchSize = 0
	}
	path = boundedMessageEventPath(path)
	result = boundedMessageEventResult(result)
	m.eventProposeTotal.WithLabelValues(path, result).Inc()
	m.eventProposeDuration.WithLabelValues(path, result).Observe(dur.Seconds())
	m.eventProposeBatchEvents.WithLabelValues(path, result).Observe(float64(batchSize))
}

// ObserveEventProposeStage records one bounded message event proposal stage latency.
func (m *MessageMetrics) ObserveEventProposeStage(path, result, stage string, dur time.Duration) {
	if m == nil {
		return
	}
	path = boundedMessageEventPath(path)
	result = boundedMessageEventResult(result)
	stage = boundedMessageEventProposeStage(stage)
	m.eventProposeStageDuration.WithLabelValues(path, result, stage).Observe(dur.Seconds())
}

// SetEventStreamCache sets the current message event stream-cache pressure gauges.
func (m *MessageMetrics) SetEventStreamCache(event MessageEventStreamCacheObservation) {
	if m == nil {
		return
	}
	m.eventStreamCacheSessions.Set(float64(event.Sessions))
	m.eventStreamCacheOpenLanes.Set(float64(event.OpenLanes))
	m.eventStreamCachePayloadBytes.Set(float64(event.PayloadBytes))
	m.eventStreamCacheMaxSessions.Set(float64(event.MaxSessions))
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

func boundedMessageEventPath(path string) string {
	switch path {
	case "cache", "durable", "finish_batch", "forward":
		return path
	default:
		return "unknown"
	}
}

func boundedMessageEventType(eventType string) string {
	switch eventType {
	case "stream.open", "stream.delta", "stream.snapshot", "stream.close", "stream.error", "stream.cancel", "stream.finish":
		return eventType
	default:
		return "unknown"
	}
}

func boundedMessageEventResult(result string) string {
	switch result {
	case "ok", "error", "backpressured", "not_leader", "not_ready", "no_slot_leader", "cache_miss", "invalid":
		return result
	default:
		return "unknown"
	}
}

func boundedMessageEventAppendStage(stage string) string {
	switch stage {
	case "finish_cache_open", "finish_batch_build", "finish_cache_remove":
		return stage
	default:
		return "unknown"
	}
}

func boundedMessageEventProposeStage(stage string) string {
	switch stage {
	case "encode", "slot_propose_wait", "slot_propose_submit", "slot_future_wait", "slot_control_wait",
		"slot_raft_commit_wait", "slot_fsm_apply", "slot_fsm_commit", "slot_mark_applied", "decode":
		return stage
	default:
		return "unknown"
	}
}
