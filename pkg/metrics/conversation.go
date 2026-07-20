package metrics

import (
	"strconv"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

var conversationListSizeBuckets = []float64{0, 1, 2, 4, 8, 16, 32, 64, 128, 256, 512, 1000, 2000, 5000}

// ConversationMetrics exposes conversation read latency and page-shape metrics.
type ConversationMetrics struct {
	activeCacheMu             sync.Mutex
	activeCacheRevision       uint64
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
	activeCacheKindRows       *prometheus.GaugeVec
	activeCacheKindDirtyRows  *prometheus.GaugeVec
	activeCacheOldestDirtyAge prometheus.Gauge
	activePressureDraining    prometheus.Gauge
	activeDirtyMutations      *prometheus.CounterVec
	activeFlushTotal          *prometheus.CounterVec
	activeFlushRows           *prometheus.HistogramVec
	activeFlushRowsTotal      *prometheus.CounterVec
	activeFlushDuration       *prometheus.HistogramVec
	activeFlushStageDuration  *prometheus.HistogramVec
	activePressureEvents      *prometheus.CounterVec
	activePressureLastEvent   *prometheus.GaugeVec
	activePressureWakeupWait  prometheus.Histogram
}

// ConversationActiveCacheSample contains one ordered active-cache gauge snapshot.
type ConversationActiveCacheSample struct {
	// Revision is the positive manager-local monotonic observation order; zero is ignored.
	Revision uint64
	// Rows is the total cached active-row count.
	Rows int
	// DirtyRows is the total dirty active-row count.
	DirtyRows int
	// OldestDirtyAge is the age of the oldest dirty row.
	OldestDirtyAge time.Duration
	// PressureDraining reports whether pressure draining is active.
	PressureDraining bool
	// NormalRows is the cached normal-conversation row count.
	NormalRows int
	// NormalDirtyRows is the dirty normal-conversation row count.
	NormalDirtyRows int
	// CMDRows is the cached CMD-conversation row count.
	CMDRows int
	// CMDDirtyRows is the dirty CMD-conversation row count.
	CMDDirtyRows int
}

// ConversationActiveFlushSample contains one low-cardinality active-cache flush observation.
type ConversationActiveFlushSample struct {
	// Result is the bounded flush outcome.
	Result string
	// FailureStage identifies filter or persist when the outcome failed.
	FailureStage string
	// Selected is the number of dirty snapshots selected for this attempt.
	Selected int
	// Persisted is the success-only acknowledged durable row count.
	Persisted int
	// Skipped is the number of rows routed through the cooldown no-write path.
	Skipped int
	// Cleared is the number of version-fenced dirty markers actually cleared.
	Cleared int
	// VersionConflicts is the number of newer dirty versions retained for retry.
	VersionConflicts int
	// Superseded is the number of selected snapshots no longer present or dirty.
	Superseded int
	// Requeued is the number of dirty rows retained for retry.
	Requeued int
	// LaneWaitDuration is time spent waiting for the serialized flush lane.
	LaneWaitDuration time.Duration
	// SelectDuration is time spent selecting a bounded dirty snapshot.
	SelectDuration time.Duration
	// FilterDuration is time spent comparing cooldown candidates with durable state.
	FilterDuration time.Duration
	// PersistDuration is time spent in the store call.
	PersistDuration time.Duration
	// ClearDuration is time spent applying version-fenced clear results.
	ClearDuration time.Duration
	// Duration is total flush attempt latency including lane wait.
	Duration time.Duration
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
		activeCacheKindRows: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_conversation_active_cache_kind_rows",
			Help:        "Current rows in the conversation active cache by fixed conversation kind.",
			ConstLabels: labels,
		}, []string{"kind"}),
		activeCacheKindDirtyRows: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_conversation_active_cache_kind_dirty_rows",
			Help:        "Current dirty rows in the conversation active cache by fixed conversation kind.",
			ConstLabels: labels,
		}, []string{"kind"}),
		activeCacheOldestDirtyAge: prometheus.NewGauge(prometheus.GaugeOpts{
			Name:        "wukongim_conversation_active_cache_oldest_dirty_age_seconds",
			Help:        "Age in seconds of the oldest dirty conversation active cache row.",
			ConstLabels: labels,
		}),
		activePressureDraining: prometheus.NewGauge(prometheus.GaugeOpts{
			Name:        "wukongim_conversation_active_pressure_draining",
			Help:        "Reports 1 while the conversation active cache is draining toward its low watermark.",
			ConstLabels: labels,
		}),
		activeDirtyMutations: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_conversation_active_dirty_mutations_total",
			Help:        "Conversation active cache row mutations grouped by bounded event.",
			ConstLabels: labels,
		}, []string{"event"}),
		activeFlushTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_conversation_active_flush_total",
			Help:        "Total conversation active cache flush attempts by normalized result.",
			ConstLabels: labels,
		}, []string{"result"}),
		activeFlushRows: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_conversation_active_flush_rows",
			Help:        "Conversation active cache rows observed at each bounded flush stage per attempt; legacy flushed means persisted.",
			ConstLabels: labels,
			Buckets:     conversationListSizeBuckets,
		}, []string{"result", "kind"}),
		activeFlushRowsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_conversation_active_flush_rows_total",
			Help:        "Cumulative conversation active cache rows grouped by flush stage and bounded reason.",
			ConstLabels: labels,
		}, []string{"result", "stage", "reason"}),
		activeFlushDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_conversation_active_flush_duration_seconds",
			Help:        "Conversation active cache flush latency in seconds.",
			ConstLabels: labels,
			Buckets:     gatewayFrameDurationBuckets,
		}, []string{"result"}),
		activeFlushStageDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_conversation_active_flush_stage_duration_seconds",
			Help:        "Conversation active cache flush latency grouped by bounded stage.",
			ConstLabels: labels,
			Buckets:     gatewayFrameDurationBuckets,
		}, []string{"result", "stage"}),
		activePressureEvents: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_conversation_active_pressure_events_total",
			Help:        "Conversation active cache pressure-drain lifecycle events.",
			ConstLabels: labels,
		}, []string{"event"}),
		activePressureLastEvent: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_conversation_active_pressure_last_event_timestamp_seconds",
			Help:        "Unix timestamp of the most recent conversation active pressure event.",
			ConstLabels: labels,
		}, []string{"event"}),
		activePressureWakeupWait: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:        "wukongim_conversation_active_pressure_wakeup_wait_duration_seconds",
			Help:        "Time from a coalesced pressure signal enqueue to flush-worker receipt.",
			ConstLabels: labels,
			Buckets:     gatewayFrameDurationBuckets,
		}),
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
		m.activeCacheKindRows,
		m.activeCacheKindDirtyRows,
		m.activeCacheOldestDirtyAge,
		m.activePressureDraining,
		m.activeDirtyMutations,
		m.activeFlushTotal,
		m.activeFlushRows,
		m.activeFlushRowsTotal,
		m.activeFlushDuration,
		m.activeFlushStageDuration,
		m.activePressureEvents,
		m.activePressureLastEvent,
		m.activePressureWakeupWait,
	)
	m.initializeActiveConservationSeries()

	return m
}

// initializeActiveConservationSeries keeps bounded zero-valued baselines
// present before the first non-zero observation so measured-window deltas do
// not silently lose the first flush or mutation event.
func (m *ConversationMetrics) initializeActiveConservationSeries() {
	for _, event := range []string{"became_dirty", "dirty_updated", "unchanged"} {
		m.activeDirtyMutations.WithLabelValues(event)
	}
	for _, labels := range [][3]string{
		{"ok", "selected", "none"},
		{"ok", "persisted", "none"},
		{"ok", "skipped", "active_cooldown"},
		{"ok", "cleared", "none"},
		{"ok", "requeued", "version_conflict"},
		{"ok", "superseded", "stale_snapshot"},
	} {
		m.activeFlushRowsTotal.WithLabelValues(labels[0], labels[1], labels[2])
	}
	for _, event := range []string{
		"start_high_watermark",
		"start_hard_limit",
		"signal_sent",
		"signal_coalesced",
		"signal_received",
		"requeue_progress",
		"requeue_no_progress",
		"stop_low_watermark",
		"pause_error",
		"pause_timeout",
	} {
		m.activePressureEvents.WithLabelValues(event)
	}
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

// SetActiveCache sets all active-cache gauges from one ordered snapshot.
func (m *ConversationMetrics) SetActiveCache(sample ConversationActiveCacheSample) {
	if m == nil {
		return
	}
	m.activeCacheMu.Lock()
	defer m.activeCacheMu.Unlock()
	if sample.Revision == 0 || sample.Revision <= m.activeCacheRevision {
		return
	}
	m.activeCacheRevision = sample.Revision
	m.activeCacheRows.Set(float64(nonNegative(sample.Rows)))
	m.activeCacheDirtyRows.Set(float64(nonNegative(sample.DirtyRows)))
	m.activeCacheOldestDirtyAge.Set(nonNegativeMetricDuration(sample.OldestDirtyAge).Seconds())
	if sample.PressureDraining {
		m.activePressureDraining.Set(1)
	} else {
		m.activePressureDraining.Set(0)
	}
	m.activeCacheKindRows.WithLabelValues("normal").Set(float64(nonNegative(sample.NormalRows)))
	m.activeCacheKindDirtyRows.WithLabelValues("normal").Set(float64(nonNegative(sample.NormalDirtyRows)))
	m.activeCacheKindRows.WithLabelValues("cmd").Set(float64(nonNegative(sample.CMDRows)))
	m.activeCacheKindDirtyRows.WithLabelValues("cmd").Set(float64(nonNegative(sample.CMDDirtyRows)))
}

// ObserveActiveMutation records one conversation active cache mutation batch.
func (m *ConversationMetrics) ObserveActiveMutation(becameDirty, dirtyUpdated, unchanged int) {
	if m == nil {
		return
	}
	m.addActiveDirtyMutation("became_dirty", becameDirty)
	m.addActiveDirtyMutation("dirty_updated", dirtyUpdated)
	m.addActiveDirtyMutation("unchanged", unchanged)
}

func (m *ConversationMetrics) addActiveDirtyMutation(event string, rows int) {
	if rows <= 0 {
		return
	}
	m.activeDirtyMutations.WithLabelValues(conversationActiveMutationEvent(event)).Add(float64(rows))
}

// ObserveActiveFlush records one conversation active cache flush attempt.
func (m *ConversationMetrics) ObserveActiveFlush(sample ConversationActiveFlushSample) {
	if m == nil {
		return
	}
	result := conversationAuthorityResult(sample.Result)
	m.activeFlushTotal.WithLabelValues(result).Inc()
	m.observeActiveFlushRows(result, "selected", "none", sample.Selected)
	m.observeActiveFlushRows(result, "persisted", "none", sample.Persisted)
	// Keep the legacy histogram label stable; explicit conservation uses persisted.
	m.activeFlushRows.WithLabelValues(result, "flushed").Observe(float64(nonNegative(sample.Persisted)))
	m.observeActiveFlushRows(result, "skipped", "active_cooldown", sample.Skipped)
	m.observeActiveFlushRows(result, "cleared", "none", sample.Cleared)
	if sample.VersionConflicts > 0 {
		m.observeActiveFlushRows(result, "requeued", "version_conflict", sample.VersionConflicts)
	}
	if sample.Superseded > 0 {
		m.observeActiveFlushRows(result, "superseded", "stale_snapshot", sample.Superseded)
	}
	accountedRequeued := nonNegative(sample.VersionConflicts)
	if remaining := nonNegative(sample.Requeued) - accountedRequeued; remaining > 0 {
		m.observeActiveFlushRows(result, "requeued", activeFlushFailureReason(sample.Result, sample.FailureStage), remaining)
	}
	m.observeActiveFlushStage(result, "lane_wait", sample.LaneWaitDuration)
	m.observeActiveFlushStage(result, "select", sample.SelectDuration)
	if sample.Selected > 0 {
		m.observeActiveFlushStage(result, "filter", sample.FilterDuration)
	}
	if sample.Persisted > 0 || sample.FailureStage == "persist" {
		m.observeActiveFlushStage(result, "persist", sample.PersistDuration)
	}
	if result == "ok" && sample.Selected > 0 {
		m.observeActiveFlushStage(result, "clear", sample.ClearDuration)
	}
	m.activeFlushDuration.WithLabelValues(result).Observe(nonNegativeMetricDuration(sample.Duration).Seconds())
}

func (m *ConversationMetrics) observeActiveFlushRows(result, stage, reason string, rows int) {
	rows = nonNegative(rows)
	stage = conversationActiveFlushRowsKind(stage)
	reason = conversationActiveFlushReason(reason)
	m.activeFlushRows.WithLabelValues(result, stage).Observe(float64(rows))
	if rows > 0 {
		m.activeFlushRowsTotal.WithLabelValues(result, stage, reason).Add(float64(rows))
	}
}

func (m *ConversationMetrics) observeActiveFlushStage(result, stage string, dur time.Duration) {
	m.activeFlushStageDuration.WithLabelValues(result, conversationActiveFlushStage(stage)).Observe(nonNegativeMetricDuration(dur).Seconds())
}

// ObserveActivePressure records one pressure-drain event and optional wakeup wait.
func (m *ConversationMetrics) ObserveActivePressure(event string, wakeupWait time.Duration) {
	if m == nil {
		return
	}
	event = conversationActivePressureEvent(event)
	m.activePressureEvents.WithLabelValues(event).Inc()
	m.activePressureLastEvent.WithLabelValues(event).SetToCurrentTime()
	if event == "signal_received" {
		m.activePressureWakeupWait.Observe(nonNegativeMetricDuration(wakeupWait).Seconds())
	}
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
	case "selected", "persisted", "flushed", "skipped", "cleared", "requeued", "superseded":
		return kind
	default:
		return "other"
	}
}

func conversationActiveMutationEvent(event string) string {
	switch event {
	case "became_dirty", "dirty_updated", "unchanged":
		return event
	default:
		return "other"
	}
}

func conversationActiveFlushReason(reason string) string {
	switch reason {
	case "none", "active_cooldown", "version_conflict", "filter_error", "persist_error", "timeout", "stale_snapshot":
		return reason
	default:
		return "other"
	}
}

func conversationActiveFlushStage(stage string) string {
	switch stage {
	case "lane_wait", "select", "filter", "persist", "clear":
		return stage
	default:
		return "other"
	}
}

func activeFlushFailureReason(result, stage string) string {
	if result == "timeout" {
		return "timeout"
	}
	if stage == "filter" {
		return "filter_error"
	}
	if stage == "persist" {
		return "persist_error"
	}
	return "other"
}

func conversationActivePressureEvent(event string) string {
	switch event {
	case "start_high_watermark", "start_hard_limit", "signal_sent", "signal_coalesced", "signal_received", "requeue_progress", "requeue_no_progress", "stop_low_watermark", "pause_error", "pause_timeout":
		return event
	default:
		return "other"
	}
}

func nonNegativeMetricDuration(d time.Duration) time.Duration {
	if d < 0 {
		return 0
	}
	return d
}

func nonNegative(v int) int {
	if v < 0 {
		return 0
	}
	return v
}
