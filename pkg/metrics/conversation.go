package metrics

import (
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

var conversationListSizeBuckets = []float64{0, 1, 2, 4, 8, 16, 32, 64, 128, 256, 512, 1000, 2000, 5000}

// ConversationMetrics exposes conversation list read latency and page-shape metrics.
type ConversationMetrics struct {
	listTotal                 *prometheus.CounterVec
	listDuration              *prometheus.HistogramVec
	listReturnedItems         *prometheus.HistogramVec
	listSparseItems           *prometheus.HistogramVec
	listLastMessageLoads      *prometheus.HistogramVec
	listLastMessageErrors     *prometheus.HistogramVec
	listActiveIndexStaleSkips *prometheus.HistogramVec
	projectorDirtyKeys        prometheus.Gauge
	projectorDirtyCapacity    prometheus.Gauge
	projectorSubmitTotal      *prometheus.CounterVec
	projectorFlushTotal       *prometheus.CounterVec
	projectorFlushDuration    *prometheus.HistogramVec
	projectorFlushEvents      *prometheus.HistogramVec
	projectorProjectedRows    *prometheus.HistogramVec
	projectorProjectionEvents *prometheus.CounterVec
	projectorRequeuedEvents   *prometheus.HistogramVec
	projectorMemberClassify   *prometheus.CounterVec
	projectorWriteTotal       *prometheus.CounterVec
	projectorWriteDuration    *prometheus.HistogramVec
	projectorWriteRows        *prometheus.HistogramVec
	authorityAdmitTotal       *prometheus.CounterVec
	authorityCachePressure    *prometheus.CounterVec
	authorityListTotal        *prometheus.CounterVec
	authorityHandoffTotal     *prometheus.CounterVec
}

func newConversationMetrics(registry prometheus.Registerer, labels prometheus.Labels) *ConversationMetrics {
	m := &ConversationMetrics{
		listTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_conversation_list_total",
			Help:        "Total number of conversation list requests.",
			ConstLabels: labels,
		}, []string{"result", "more"}),
		listDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_conversation_list_duration_seconds",
			Help:        "Conversation list request latency in seconds.",
			ConstLabels: labels,
			Buckets:     gatewayFrameDurationBuckets,
		}, []string{"result", "more"}),
		listReturnedItems: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_conversation_list_returned_items",
			Help:        "Conversation rows returned by conversation list requests.",
			ConstLabels: labels,
			Buckets:     conversationListSizeBuckets,
		}, []string{"result", "more"}),
		listSparseItems: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_conversation_list_sparse_items",
			Help:        "Returned conversation rows using sparse active ordering.",
			ConstLabels: labels,
			Buckets:     conversationListSizeBuckets,
		}, []string{"result", "more"}),
		listLastMessageLoads: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_conversation_list_last_message_loads",
			Help:        "Last-message loads attempted by conversation list requests.",
			ConstLabels: labels,
			Buckets:     conversationListSizeBuckets,
		}, []string{"result", "more"}),
		listLastMessageErrors: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_conversation_list_last_message_errors",
			Help:        "Last-message load errors observed by conversation list requests.",
			ConstLabels: labels,
			Buckets:     conversationListSizeBuckets,
		}, []string{"result", "more"}),
		listActiveIndexStaleSkips: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_conversation_list_active_index_stale_skips",
			Help:        "Stale active-index rows skipped by conversation list requests.",
			ConstLabels: labels,
			Buckets:     conversationListSizeBuckets,
		}, []string{"result", "more"}),
		projectorDirtyKeys: prometheus.NewGauge(prometheus.GaugeOpts{
			Name:        "wukongim_conversation_projector_dirty_keys",
			Help:        "Current number of dirty committed-message keys retained by the conversation projector.",
			ConstLabels: labels,
		}),
		projectorDirtyCapacity: prometheus.NewGauge(prometheus.GaugeOpts{
			Name:        "wukongim_conversation_projector_dirty_capacity",
			Help:        "Maximum dirty committed-message keys retained by the conversation projector.",
			ConstLabels: labels,
		}),
		projectorSubmitTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_conversation_projector_submit_total",
			Help:        "Conversation projector committed-message submissions by result.",
			ConstLabels: labels,
		}, []string{"result"}),
		projectorFlushTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_conversation_projector_flush_total",
			Help:        "Conversation projector flush attempts by result.",
			ConstLabels: labels,
		}, []string{"result"}),
		projectorFlushDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_conversation_projector_flush_duration_seconds",
			Help:        "Conversation projector flush latency in seconds.",
			ConstLabels: labels,
			Buckets:     runtimePressureDurationBuckets,
		}, []string{"result"}),
		projectorFlushEvents: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_conversation_projector_flush_events",
			Help:        "Committed-message events drained by conversation projector flushes.",
			ConstLabels: labels,
			Buckets:     conversationListSizeBuckets,
		}, []string{"result"}),
		projectorProjectedRows: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_conversation_projector_projected_rows",
			Help:        "UID-owned conversation rows projected by conversation projector flushes.",
			ConstLabels: labels,
			Buckets:     conversationListSizeBuckets,
		}, []string{"result"}),
		projectorProjectionEvents: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_conversation_projector_projection_events_total",
			Help:        "Conversation projector events by dense or sparse projection mode.",
			ConstLabels: labels,
		}, []string{"mode", "result"}),
		projectorRequeuedEvents: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_conversation_projector_requeued_events",
			Help:        "Committed-message events requeued by conversation projector flushes.",
			ConstLabels: labels,
			Buckets:     conversationListSizeBuckets,
		}, []string{"result"}),
		projectorMemberClassify: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_conversation_projector_member_classify_total",
			Help:        "Conversation projector member classification lookups by cache status and result.",
			ConstLabels: labels,
		}, []string{"result", "cache"}),
		projectorWriteTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_conversation_projector_write_total",
			Help:        "Conversation projector durable write attempts by phase and result.",
			ConstLabels: labels,
		}, []string{"phase", "result"}),
		projectorWriteDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_conversation_projector_write_duration_seconds",
			Help:        "Conversation projector durable write latency in seconds.",
			ConstLabels: labels,
			Buckets:     runtimePressureDurationBuckets,
		}, []string{"phase", "result"}),
		projectorWriteRows: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_conversation_projector_write_rows",
			Help:        "UID-owned conversation rows written by conversation projector durable write attempts.",
			ConstLabels: labels,
			Buckets:     conversationListSizeBuckets,
		}, []string{"phase", "result"}),
		authorityAdmitTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_conversation_authority_admit_total",
			Help:        "Conversation authority foreground admissions by normalized result.",
			ConstLabels: labels,
		}, []string{"result"}),
		authorityCachePressure: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_conversation_authority_cache_pressure_total",
			Help:        "Conversation authority cache pressure observations by phase and result.",
			ConstLabels: labels,
		}, []string{"phase", "result"}),
		authorityListTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_conversation_authority_list_total",
			Help:        "Conversation authority active-view list requests by normalized result.",
			ConstLabels: labels,
		}, []string{"result"}),
		authorityHandoffTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_conversation_authority_handoff_total",
			Help:        "Conversation authority handoff and drain attempts by normalized result.",
			ConstLabels: labels,
		}, []string{"result"}),
	}

	registry.MustRegister(
		m.listTotal,
		m.listDuration,
		m.listReturnedItems,
		m.listSparseItems,
		m.listLastMessageLoads,
		m.listLastMessageErrors,
		m.listActiveIndexStaleSkips,
		m.projectorDirtyKeys,
		m.projectorDirtyCapacity,
		m.projectorSubmitTotal,
		m.projectorFlushTotal,
		m.projectorFlushDuration,
		m.projectorFlushEvents,
		m.projectorProjectedRows,
		m.projectorProjectionEvents,
		m.projectorRequeuedEvents,
		m.projectorMemberClassify,
		m.projectorWriteTotal,
		m.projectorWriteDuration,
		m.projectorWriteRows,
		m.authorityAdmitTotal,
		m.authorityCachePressure,
		m.authorityListTotal,
		m.authorityHandoffTotal,
	)

	return m
}

// ObserveList records one conversation list request result and page shape.
func (m *ConversationMetrics) ObserveList(result string, more bool, dur time.Duration, returnedItems, sparseItems, lastMessageLoads, lastMessageErrors, activeIndexStaleSkips int) {
	if m == nil {
		return
	}
	if result == "" {
		result = "unknown"
	}
	moreLabel := strconv.FormatBool(more)
	m.listTotal.WithLabelValues(result, moreLabel).Inc()
	m.listDuration.WithLabelValues(result, moreLabel).Observe(dur.Seconds())
	m.listReturnedItems.WithLabelValues(result, moreLabel).Observe(float64(nonNegative(returnedItems)))
	m.listSparseItems.WithLabelValues(result, moreLabel).Observe(float64(nonNegative(sparseItems)))
	m.listLastMessageLoads.WithLabelValues(result, moreLabel).Observe(float64(nonNegative(lastMessageLoads)))
	m.listLastMessageErrors.WithLabelValues(result, moreLabel).Observe(float64(nonNegative(lastMessageErrors)))
	m.listActiveIndexStaleSkips.WithLabelValues(result, moreLabel).Observe(float64(nonNegative(activeIndexStaleSkips)))
}

// SetProjectorDirty records the current dirty-key pressure for conversation projection.
func (m *ConversationMetrics) SetProjectorDirty(dirtyKeys, capacity int) {
	if m == nil {
		return
	}
	m.projectorDirtyKeys.Set(float64(nonNegative(dirtyKeys)))
	m.projectorDirtyCapacity.Set(float64(nonNegative(capacity)))
}

// ObserveProjectorSubmit records one committed-message projector submission.
func (m *ConversationMetrics) ObserveProjectorSubmit(result string) {
	if m == nil {
		return
	}
	m.projectorSubmitTotal.WithLabelValues(conversationProjectorResult(result)).Inc()
}

// ObserveProjectorFlush records one conversation projector flush result and shape.
func (m *ConversationMetrics) ObserveProjectorFlush(result string, dur time.Duration, drainedEvents, projectedRows, denseEvents, sparseEvents, requeuedEvents int) {
	if m == nil {
		return
	}
	result = conversationProjectorResult(result)
	m.projectorFlushTotal.WithLabelValues(result).Inc()
	m.projectorFlushDuration.WithLabelValues(result).Observe(dur.Seconds())
	m.projectorFlushEvents.WithLabelValues(result).Observe(float64(nonNegative(drainedEvents)))
	m.projectorProjectedRows.WithLabelValues(result).Observe(float64(nonNegative(projectedRows)))
	m.projectorProjectionEvents.WithLabelValues("dense", result).Add(float64(nonNegative(denseEvents)))
	m.projectorProjectionEvents.WithLabelValues("sparse", result).Add(float64(nonNegative(sparseEvents)))
	m.projectorRequeuedEvents.WithLabelValues(result).Observe(float64(nonNegative(requeuedEvents)))
}

// ObserveProjectorMemberClassify records one member classification cache hit or miss.
func (m *ConversationMetrics) ObserveProjectorMemberClassify(result, cache string) {
	if m == nil {
		return
	}
	if cache != "hit" && cache != "miss" {
		cache = "miss"
	}
	m.projectorMemberClassify.WithLabelValues(conversationProjectorResult(result), cache).Inc()
}

// ObserveProjectorWrite records one durable projector write attempt.
func (m *ConversationMetrics) ObserveProjectorWrite(phase, result string, dur time.Duration, rows int) {
	if m == nil {
		return
	}
	switch phase {
	case "batch", "fallback":
	default:
		phase = "other"
	}
	result = conversationProjectorResult(result)
	m.projectorWriteTotal.WithLabelValues(phase, result).Inc()
	m.projectorWriteDuration.WithLabelValues(phase, result).Observe(dur.Seconds())
	m.projectorWriteRows.WithLabelValues(phase, result).Observe(float64(nonNegative(rows)))
}

// ObserveAuthorityAdmit records one conversation authority foreground admission outcome.
func (m *ConversationMetrics) ObserveAuthorityAdmit(result string) {
	if m == nil {
		return
	}
	m.authorityAdmitTotal.WithLabelValues(conversationAuthorityResult(result)).Inc()
}

// ObserveAuthorityCachePressure records one authority cache pressure observation.
func (m *ConversationMetrics) ObserveAuthorityCachePressure(phase, result string) {
	if m == nil {
		return
	}
	m.authorityCachePressure.WithLabelValues(conversationAuthorityPhase(phase), conversationAuthorityResult(result)).Inc()
}

// ObserveAuthorityList records one authority active-view list outcome.
func (m *ConversationMetrics) ObserveAuthorityList(result string) {
	if m == nil {
		return
	}
	m.authorityListTotal.WithLabelValues(conversationAuthorityResult(result)).Inc()
}

// ObserveAuthorityHandoff records one authority handoff or drain outcome.
func (m *ConversationMetrics) ObserveAuthorityHandoff(result string) {
	if m == nil {
		return
	}
	m.authorityHandoffTotal.WithLabelValues(conversationAuthorityResult(result)).Inc()
}

func conversationProjectorResult(result string) string {
	switch result {
	case "ok", "accepted", "coalesced", "dropped", "ignored", "error":
		return result
	default:
		return "other"
	}
}

func conversationAuthorityPhase(phase string) string {
	switch phase {
	case "admit", "list", "flush":
		return phase
	default:
		return "other"
	}
}

func conversationAuthorityResult(result string) string {
	switch result {
	case "ok", "error", "ignored", "cache_pressure", "route_not_ready", "stale_route", "not_leader", "timeout", "drained", "no_dirty", "busy", "transferred":
		return result
	case "accepted":
		return "ok"
	default:
		return "other"
	}
}

func nonNegative(v int) int {
	if v < 0 {
		return 0
	}
	return v
}
