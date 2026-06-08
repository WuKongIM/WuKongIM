package metrics

import (
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

var conversationListSizeBuckets = []float64{0, 1, 2, 4, 8, 16, 32, 64, 128, 256, 512, 1000, 2000, 5000}

// ConversationMetrics exposes conversation list read latency and page-shape metrics.
type ConversationMetrics struct {
	listTotal                  *prometheus.CounterVec
	listDuration               *prometheus.HistogramVec
	listReturnedItems          *prometheus.HistogramVec
	listSparseItems            *prometheus.HistogramVec
	listLastMessageLoads       *prometheus.HistogramVec
	listLastMessageErrors      *prometheus.HistogramVec
	listActiveIndexStaleSkips  *prometheus.HistogramVec
	authorityAdmitTotal        *prometheus.CounterVec
	authorityCachePressure     *prometheus.CounterVec
	authorityListTotal         *prometheus.CounterVec
	authorityHandoffTotal      *prometheus.CounterVec
	projectionDirtyKeys        prometheus.Gauge
	projectionDirtyCapacity    prometheus.Gauge
	projectionSubmitTotal      *prometheus.CounterVec
	projectionFlushTotal       *prometheus.CounterVec
	projectionFlushDuration    *prometheus.HistogramVec
	projectionFlushEvents      *prometheus.HistogramVec
	projectionFlushPatches     *prometheus.HistogramVec
	projectionMemberClassify   *prometheus.CounterVec
	projectionAuthorityAdmit   *prometheus.CounterVec
	projectionAuthorityBatches *prometheus.HistogramVec
	projectionRetryPatches     prometheus.Gauge
	projectionRetryDropTotal   *prometheus.CounterVec
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
		authorityAdmitTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_conversation_authority_admit_total",
			Help:        "Conversation authority cache admissions by normalized result.",
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
		projectionDirtyKeys: prometheus.NewGauge(prometheus.GaugeOpts{
			Name:        "wukongim_conversation_projection_dirty_keys",
			Help:        "Conversation projection dirty event keys currently retained in memory.",
			ConstLabels: labels,
		}),
		projectionDirtyCapacity: prometheus.NewGauge(prometheus.GaugeOpts{
			Name:        "wukongim_conversation_projection_dirty_capacity",
			Help:        "Conversation projection dirty event key capacity.",
			ConstLabels: labels,
		}),
		projectionSubmitTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_conversation_projection_submit_total",
			Help:        "Conversation projection foreground metadata submissions by normalized result.",
			ConstLabels: labels,
		}, []string{"result"}),
		projectionFlushTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_conversation_projection_flush_total",
			Help:        "Conversation projection background flushes by normalized result.",
			ConstLabels: labels,
		}, []string{"result"}),
		projectionFlushDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_conversation_projection_flush_duration_seconds",
			Help:        "Conversation projection background flush duration in seconds.",
			ConstLabels: labels,
			Buckets:     gatewayFrameDurationBuckets,
		}, []string{"result"}),
		projectionFlushEvents: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_conversation_projection_flush_events",
			Help:        "Conversation projection event counts observed during background flush.",
			ConstLabels: labels,
			Buckets:     conversationListSizeBuckets,
		}, []string{"kind"}),
		projectionFlushPatches: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_conversation_projection_flush_patches",
			Help:        "Conversation projection active patch counts observed during background flush.",
			ConstLabels: labels,
			Buckets:     conversationListSizeBuckets,
		}, []string{"kind"}),
		projectionMemberClassify: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_conversation_projection_member_classify_total",
			Help:        "Conversation projection member classification attempts by normalized result and cache hit.",
			ConstLabels: labels,
		}, []string{"result", "cache_hit"}),
		projectionAuthorityAdmit: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_conversation_projection_authority_admit_total",
			Help:        "Conversation projection background authority admission attempts by normalized result.",
			ConstLabels: labels,
		}, []string{"result"}),
		projectionAuthorityBatches: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_conversation_projection_authority_batches",
			Help:        "Conversation projection authority admission batch counts.",
			ConstLabels: labels,
			Buckets:     conversationListSizeBuckets,
		}, []string{"side"}),
		projectionRetryPatches: prometheus.NewGauge(prometheus.GaugeOpts{
			Name:        "wukongim_conversation_projection_retry_patches",
			Help:        "Conversation projection active patches retained for retry.",
			ConstLabels: labels,
		}),
		projectionRetryDropTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_conversation_projection_retry_drop_total",
			Help:        "Conversation projection retry patches dropped by normalized reason.",
			ConstLabels: labels,
		}, []string{"reason"}),
	}

	registry.MustRegister(
		m.listTotal,
		m.listDuration,
		m.listReturnedItems,
		m.listSparseItems,
		m.listLastMessageLoads,
		m.listLastMessageErrors,
		m.listActiveIndexStaleSkips,
		m.authorityAdmitTotal,
		m.authorityCachePressure,
		m.authorityListTotal,
		m.authorityHandoffTotal,
		m.projectionDirtyKeys,
		m.projectionDirtyCapacity,
		m.projectionSubmitTotal,
		m.projectionFlushTotal,
		m.projectionFlushDuration,
		m.projectionFlushEvents,
		m.projectionFlushPatches,
		m.projectionMemberClassify,
		m.projectionAuthorityAdmit,
		m.projectionAuthorityBatches,
		m.projectionRetryPatches,
		m.projectionRetryDropTotal,
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

// ObserveAuthorityAdmit records one conversation authority cache admission outcome.
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

// SetProjectionDirty records the in-memory dirty event key count and capacity.
func (m *ConversationMetrics) SetProjectionDirty(keys, capacity int) {
	if m == nil {
		return
	}
	m.projectionDirtyKeys.Set(float64(nonNegative(keys)))
	m.projectionDirtyCapacity.Set(float64(nonNegative(capacity)))
}

// ObserveProjectionSubmit records one foreground projection submission outcome.
func (m *ConversationMetrics) ObserveProjectionSubmit(result string) {
	if m == nil {
		return
	}
	m.projectionSubmitTotal.WithLabelValues(conversationProjectionResult(result)).Inc()
}

// ObserveProjectionFlush records one background projection flush.
func (m *ConversationMetrics) ObserveProjectionFlush(result string, dur time.Duration, events, patches, dense, sparse int) {
	if m == nil {
		return
	}
	label := conversationProjectionResult(result)
	m.projectionFlushTotal.WithLabelValues(label).Inc()
	m.projectionFlushDuration.WithLabelValues(label).Observe(dur.Seconds())
	m.projectionFlushEvents.WithLabelValues("drained").Observe(float64(nonNegative(events)))
	m.projectionFlushEvents.WithLabelValues("dense").Observe(float64(nonNegative(dense)))
	m.projectionFlushEvents.WithLabelValues("sparse").Observe(float64(nonNegative(sparse)))
	m.projectionFlushPatches.WithLabelValues("projected").Observe(float64(nonNegative(patches)))
}

// ObserveProjectionMemberClassify records one member classification performed by the projector.
func (m *ConversationMetrics) ObserveProjectionMemberClassify(result string, cacheHit bool) {
	if m == nil {
		return
	}
	m.projectionMemberClassify.WithLabelValues(conversationProjectionResult(result), strconv.FormatBool(cacheHit)).Inc()
}

// ObserveProjectionAuthorityAdmit records one background authority admission attempt.
func (m *ConversationMetrics) ObserveProjectionAuthorityAdmit(result string, _ int, localBatches, remoteBatches int) {
	if m == nil {
		return
	}
	label := conversationProjectionResult(result)
	m.projectionAuthorityAdmit.WithLabelValues(label).Inc()
	m.projectionAuthorityBatches.WithLabelValues("local").Observe(float64(nonNegative(localBatches)))
	m.projectionAuthorityBatches.WithLabelValues("remote").Observe(float64(nonNegative(remoteBatches)))
}

// SetProjectionRetry records retained retry patch count.
func (m *ConversationMetrics) SetProjectionRetry(patches int) {
	if m == nil {
		return
	}
	m.projectionRetryPatches.Set(float64(nonNegative(patches)))
}

// ObserveProjectionRetryDrop records one retry patch drop reason.
func (m *ConversationMetrics) ObserveProjectionRetryDrop(reason string) {
	if m == nil {
		return
	}
	m.projectionRetryDropTotal.WithLabelValues(conversationProjectionRetryDropReason(reason)).Inc()
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

func conversationProjectionResult(result string) string {
	switch result {
	case "ok", "error", "accepted", "coalesced", "dropped", "ignored", "cache_pressure", "route_not_ready", "stale_route", "not_leader", "timeout":
		return result
	default:
		return "other"
	}
}

func conversationProjectionRetryDropReason(reason string) string {
	switch reason {
	case "capacity", "age", "invalid":
		return reason
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
