package metrics

import (
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

var conversationListSizeBuckets = []float64{0, 1, 2, 4, 8, 16, 32, 64, 128, 256, 512, 1000, 2000, 5000}

// ConversationMetrics exposes conversation read latency and page-shape metrics.
type ConversationMetrics struct {
	listTotal                 *prometheus.CounterVec
	listDuration              *prometheus.HistogramVec
	listReturnedItems         *prometheus.HistogramVec
	listSparseItems           *prometheus.HistogramVec
	listLastMessageLoads      *prometheus.HistogramVec
	listLastMessageErrors     *prometheus.HistogramVec
	listActiveIndexStaleSkips *prometheus.HistogramVec
	syncTotal                 *prometheus.CounterVec
	syncDuration              *prometheus.HistogramVec
	syncReturnedItems         *prometheus.HistogramVec
	syncOverlayItems          *prometheus.HistogramVec
	syncRecentLoadDuration    *prometheus.HistogramVec
	authorityAdmitTotal       *prometheus.CounterVec
	authorityCachePressure    *prometheus.CounterVec
	authorityListTotal        *prometheus.CounterVec
	authorityHandoffTotal     *prometheus.CounterVec
	activeCacheRows           prometheus.Gauge
	activeCacheDirtyRows      prometheus.Gauge
	activeCacheOldestDirtyAge prometheus.Gauge
	activeFlushTotal          *prometheus.CounterVec
	activeFlushRows           *prometheus.HistogramVec
	activeFlushDuration       *prometheus.HistogramVec
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
		syncTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_conversation_sync_total",
			Help:        "Total number of conversation sync requests.",
			ConstLabels: labels,
		}, []string{"result", "only_unread", "with_recents"}),
		syncDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_conversation_sync_duration_seconds",
			Help:        "Conversation sync request latency in seconds.",
			ConstLabels: labels,
			Buckets:     gatewayFrameDurationBuckets,
		}, []string{"result", "only_unread", "with_recents"}),
		syncReturnedItems: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_conversation_sync_returned_items",
			Help:        "Conversation rows returned by conversation sync requests.",
			ConstLabels: labels,
			Buckets:     conversationListSizeBuckets,
		}, []string{"result", "only_unread", "with_recents"}),
		syncOverlayItems: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_conversation_sync_overlay_items",
			Help:        "Overlay rows merged by conversation sync requests.",
			ConstLabels: labels,
			Buckets:     conversationListSizeBuckets,
		}, []string{"result", "only_unread", "with_recents"}),
		syncRecentLoadDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_conversation_sync_recent_load_duration_seconds",
			Help:        "Recent conversation load latency within sync requests in seconds.",
			ConstLabels: labels,
			Buckets:     gatewayFrameDurationBuckets,
		}, []string{"result", "only_unread"}),
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
		activeCacheRows: prometheus.NewGauge(prometheus.GaugeOpts{
			Name:        "wukongim_conversation_active_cache_rows",
			Help:        "Current rows in the conversation active cache.",
			ConstLabels: labels,
		}),
		activeCacheDirtyRows: prometheus.NewGauge(prometheus.GaugeOpts{
			Name:        "wukongim_conversation_active_cache_dirty_rows",
			Help:        "Current dirty rows in the conversation active cache.",
			ConstLabels: labels,
		}),
		activeCacheOldestDirtyAge: prometheus.NewGauge(prometheus.GaugeOpts{
			Name:        "wukongim_conversation_active_cache_oldest_dirty_age_seconds",
			Help:        "Age in seconds of the oldest dirty conversation active cache row.",
			ConstLabels: labels,
		}),
		activeFlushTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_conversation_active_flush_total",
			Help:        "Total conversation active cache flush attempts by normalized result.",
			ConstLabels: labels,
		}, []string{"result"}),
		activeFlushRows: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_conversation_active_flush_rows",
			Help:        "Conversation active cache rows selected or flushed per attempt.",
			ConstLabels: labels,
			Buckets:     conversationListSizeBuckets,
		}, []string{"result", "kind"}),
		activeFlushDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_conversation_active_flush_duration_seconds",
			Help:        "Conversation active cache flush latency in seconds.",
			ConstLabels: labels,
			Buckets:     gatewayFrameDurationBuckets,
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
		m.syncTotal,
		m.syncDuration,
		m.syncReturnedItems,
		m.syncOverlayItems,
		m.syncRecentLoadDuration,
		m.authorityAdmitTotal,
		m.authorityCachePressure,
		m.authorityListTotal,
		m.authorityHandoffTotal,
		m.activeCacheRows,
		m.activeCacheDirtyRows,
		m.activeCacheOldestDirtyAge,
		m.activeFlushTotal,
		m.activeFlushRows,
		m.activeFlushDuration,
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

// ObserveSync records one conversation sync request result and returned row shape.
func (m *ConversationMetrics) ObserveSync(result string, onlyUnread, withRecents bool, dur time.Duration, returnedItems, overlayItems int, recentLoadDuration time.Duration) {
	if m == nil {
		return
	}
	result = conversationSyncResult(result)
	onlyUnreadLabel := strconv.FormatBool(onlyUnread)
	withRecentsLabel := strconv.FormatBool(withRecents)
	if dur < 0 {
		dur = 0
	}
	m.syncTotal.WithLabelValues(result, onlyUnreadLabel, withRecentsLabel).Inc()
	m.syncDuration.WithLabelValues(result, onlyUnreadLabel, withRecentsLabel).Observe(dur.Seconds())
	m.syncReturnedItems.WithLabelValues(result, onlyUnreadLabel, withRecentsLabel).Observe(float64(nonNegative(returnedItems)))
	m.syncOverlayItems.WithLabelValues(result, onlyUnreadLabel, withRecentsLabel).Observe(float64(nonNegative(overlayItems)))
	if withRecents && recentLoadDuration > 0 {
		m.syncRecentLoadDuration.WithLabelValues(result, onlyUnreadLabel).Observe(recentLoadDuration.Seconds())
	}
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

// SetActiveCache sets the current conversation active cache pressure gauges.
func (m *ConversationMetrics) SetActiveCache(rows, dirtyRows int, oldestDirtyAge time.Duration) {
	if m == nil {
		return
	}
	m.activeCacheRows.Set(float64(nonNegative(rows)))
	m.activeCacheDirtyRows.Set(float64(nonNegative(dirtyRows)))
	if oldestDirtyAge < 0 {
		oldestDirtyAge = 0
	}
	m.activeCacheOldestDirtyAge.Set(oldestDirtyAge.Seconds())
}

// ObserveActiveFlush records one conversation active cache flush attempt.
func (m *ConversationMetrics) ObserveActiveFlush(result string, selected, flushed int, dur time.Duration) {
	if m == nil {
		return
	}
	result = conversationAuthorityResult(result)
	m.activeFlushTotal.WithLabelValues(result).Inc()
	m.activeFlushRows.WithLabelValues(result, conversationActiveFlushRowsKind("selected")).Observe(float64(nonNegative(selected)))
	m.activeFlushRows.WithLabelValues(result, conversationActiveFlushRowsKind("flushed")).Observe(float64(nonNegative(flushed)))
	if dur < 0 {
		dur = 0
	}
	m.activeFlushDuration.WithLabelValues(result).Observe(dur.Seconds())
}

func conversationSyncResult(result string) string {
	switch result {
	case "ok", "invalid_request", "parse_last_msg_seqs_error", "not_configured", "store_error", "recent_message_error", "error":
		return result
	default:
		return "error"
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

func conversationActiveFlushRowsKind(kind string) string {
	switch kind {
	case "selected", "flushed":
		return kind
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
