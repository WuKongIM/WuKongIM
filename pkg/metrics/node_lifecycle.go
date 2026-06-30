package metrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	nodeLifecycleJoinStates = map[string]struct{}{
		"active":  {},
		"joining": {},
		"leaving": {},
		"removed": {},
	}
	nodeLifecycleStatuses = map[string]struct{}{
		"alive":   {},
		"suspect": {},
		"down":    {},
	}
	nodeHealthFreshnesses = map[string]struct{}{
		"fresh":   {},
		"stale":   {},
		"missing": {},
	}
	nodeLifecycleOperations = map[string]struct{}{
		"join":            {},
		"activate":        {},
		"mark_leaving":    {},
		"mark_removed":    {},
		"onboarding":      {},
		"scale_in":        {},
		"scale_in_start":  {},
		"scale_in_drain":  {},
		"scale_in_remove": {},
	}
	nodeLifecycleResults = map[string]struct{}{
		"ok":          {},
		"fail":        {},
		"error":       {},
		"conflict":    {},
		"invalid":     {},
		"noop":        {},
		"not_ready":   {},
		"timeout":     {},
		"unavailable": {},
		"unsafe":      {},
	}
	nodeOnboardingTaskStates = map[string]struct{}{
		"pending": {},
		"running": {},
		"failed":  {},
	}
	nodeScaleInBlockerReasons = map[string]struct{}{
		"target_health_missing":               {},
		"target_health_stale":                 {},
		"target_health_not_alive":             {},
		"target_runtime_not_ready":            {},
		"eligible_node_health_missing":        {},
		"eligible_node_health_stale":          {},
		"eligible_node_health_not_alive":      {},
		"eligible_node_health_revision_stale": {},
		"eligible_node_runtime_not_ready":     {},
	}
	slotReplicaMoveResults = map[string]struct{}{
		"ok":       {},
		"fail":     {},
		"conflict": {},
		"timeout":  {},
		"canceled": {},
	}
	slotReplicaMoveFailureReasons = map[string]struct{}{
		"proposal_rejected": {},
		"control_conflict":  {},
		"runtime_error":     {},
		"timeout":           {},
		"unsafe":            {},
	}
	slotReplicaMoveDurationBuckets = []float64{0.1, 0.5, 1, 2.5, 5, 10, 30, 60, 120, 300}
)

// NodeLifecycleKey identifies current node lifecycle count buckets.
type NodeLifecycleKey struct {
	JoinState string
	Status    string
}

// NodeHealthFreshnessKey identifies current health freshness count buckets.
type NodeHealthFreshnessKey struct {
	Freshness string
	Status    string
}

// NodeLifecycleMetrics exposes low-cardinality dynamic node lifecycle evidence.
type NodeLifecycleMetrics struct {
	lifecycleNodes     *prometheus.GaugeVec
	healthFreshness    *prometheus.GaugeVec
	healthReportAgeMax *prometheus.GaugeVec
	lifecycleAttempts  *prometheus.CounterVec
	onboardingTasks    *prometheus.GaugeVec
	scaleInBlockers    *prometheus.CounterVec
	slotMoveDuration   *prometheus.HistogramVec
	slotMoveFailures   *prometheus.CounterVec
	membershipRevision prometheus.Gauge
}

func newNodeLifecycleMetrics(registry prometheus.Registerer, labels prometheus.Labels) *NodeLifecycleMetrics {
	m := &NodeLifecycleMetrics{
		lifecycleNodes: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_node_lifecycle_nodes",
			Help:        "Current node count grouped by durable join state and status.",
			ConstLabels: labels,
		}, []string{"join_state", "status"}),
		healthFreshness: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_node_health_freshness_nodes",
			Help:        "Current node count grouped by health freshness and health status.",
			ConstLabels: labels,
		}, []string{"freshness", "status"}),
		healthReportAgeMax: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_node_health_report_age_seconds",
			Help:        "Maximum current health report age in seconds grouped by freshness and status.",
			ConstLabels: labels,
		}, []string{"freshness", "status"}),
		lifecycleAttempts: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_node_lifecycle_attempts_total",
			Help:        "Total node lifecycle operation attempts grouped by operation and result.",
			ConstLabels: labels,
		}, []string{"operation", "result"}),
		onboardingTasks: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_node_onboarding_tasks",
			Help:        "Current Slot onboarding task count grouped by task state.",
			ConstLabels: labels,
		}, []string{"state"}),
		scaleInBlockers: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_node_scale_in_blockers_total",
			Help:        "Total observed scale-in blockers grouped by bounded reason.",
			ConstLabels: labels,
		}, []string{"reason"}),
		slotMoveDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_slot_replica_move_duration_seconds",
			Help:        "Slot replica move duration in seconds grouped by result.",
			ConstLabels: labels,
			Buckets:     slotReplicaMoveDurationBuckets,
		}, []string{"result"}),
		slotMoveFailures: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_slot_replica_move_failures_total",
			Help:        "Total Slot replica move failures grouped by bounded reason.",
			ConstLabels: labels,
		}, []string{"reason"}),
		membershipRevision: prometheus.NewGauge(prometheus.GaugeOpts{
			Name:        "wukongim_discovery_membership_revision",
			Help:        "Latest locally visible dynamic-node membership revision.",
			ConstLabels: labels,
		}),
	}

	registry.MustRegister(
		m.lifecycleNodes,
		m.healthFreshness,
		m.healthReportAgeMax,
		m.lifecycleAttempts,
		m.onboardingTasks,
		m.scaleInBlockers,
		m.slotMoveDuration,
		m.slotMoveFailures,
		m.membershipRevision,
	)

	return m
}

// SetLifecycleNodes records the latest lifecycle bucket counts.
func (m *NodeLifecycleMetrics) SetLifecycleNodes(counts map[NodeLifecycleKey]int) {
	if m == nil {
		return
	}
	m.lifecycleNodes.Reset()
	normalized := make(map[NodeLifecycleKey]int, len(counts))
	for key, count := range counts {
		normalized[NodeLifecycleKey{
			JoinState: normalizeBoundedMetricLabel(key.JoinState, nodeLifecycleJoinStates),
			Status:    normalizeBoundedMetricLabel(key.Status, nodeLifecycleStatuses),
		}] += count
	}
	for key, count := range normalized {
		m.lifecycleNodes.WithLabelValues(
			key.JoinState,
			key.Status,
		).Set(float64(count))
	}
}

// SetHealthFreshnessNodes records the latest health freshness bucket counts.
func (m *NodeLifecycleMetrics) SetHealthFreshnessNodes(counts map[NodeHealthFreshnessKey]int) {
	if m == nil {
		return
	}
	m.healthFreshness.Reset()
	normalized := make(map[NodeHealthFreshnessKey]int, len(counts))
	for key, count := range counts {
		normalized[NodeHealthFreshnessKey{
			Freshness: normalizeBoundedMetricLabel(key.Freshness, nodeHealthFreshnesses),
			Status:    normalizeBoundedMetricLabel(key.Status, nodeLifecycleStatuses),
		}] += count
	}
	for key, count := range normalized {
		m.healthFreshness.WithLabelValues(
			key.Freshness,
			key.Status,
		).Set(float64(count))
	}
}

// SetHealthReportAgeMax records the latest max health report age per freshness bucket.
func (m *NodeLifecycleMetrics) SetHealthReportAgeMax(ages map[NodeHealthFreshnessKey]float64) {
	if m == nil {
		return
	}
	m.healthReportAgeMax.Reset()
	normalized := make(map[NodeHealthFreshnessKey]float64, len(ages))
	for key, age := range ages {
		normalizedKey := NodeHealthFreshnessKey{
			Freshness: normalizeBoundedMetricLabel(key.Freshness, nodeHealthFreshnesses),
			Status:    normalizeBoundedMetricLabel(key.Status, nodeLifecycleStatuses),
		}
		if existing, ok := normalized[normalizedKey]; !ok || age > existing {
			normalized[normalizedKey] = age
		}
	}
	for key, age := range normalized {
		m.healthReportAgeMax.WithLabelValues(
			key.Freshness,
			key.Status,
		).Set(age)
	}
}

// ObserveLifecycleAttempt increments one lifecycle operation result.
func (m *NodeLifecycleMetrics) ObserveLifecycleAttempt(operation, result string) {
	if m == nil {
		return
	}
	m.lifecycleAttempts.WithLabelValues(
		normalizeBoundedMetricLabel(operation, nodeLifecycleOperations),
		normalizeBoundedMetricLabel(result, nodeLifecycleResults),
	).Inc()
}

// ObserveScaleInBlocker increments one bounded scale-in blocker reason.
func (m *NodeLifecycleMetrics) ObserveScaleInBlocker(reason string) {
	if m == nil {
		return
	}
	m.scaleInBlockers.WithLabelValues(normalizeBoundedMetricLabel(reason, nodeScaleInBlockerReasons)).Inc()
}

// SetOnboardingTasks records the latest Slot onboarding task counts.
func (m *NodeLifecycleMetrics) SetOnboardingTasks(counts map[string]int) {
	if m == nil {
		return
	}
	m.onboardingTasks.Reset()
	normalized := make(map[string]int, len(counts))
	for state, count := range counts {
		normalized[normalizeBoundedMetricLabel(state, nodeOnboardingTaskStates)] += count
	}
	for state, count := range normalized {
		m.onboardingTasks.WithLabelValues(state).Set(float64(count))
	}
}

// ObserveSlotReplicaMove records one completed Slot replica move duration.
func (m *NodeLifecycleMetrics) ObserveSlotReplicaMove(result string, d time.Duration) {
	if m == nil {
		return
	}
	if d < 0 {
		d = 0
	}
	m.slotMoveDuration.WithLabelValues(normalizeBoundedMetricLabel(result, slotReplicaMoveResults)).Observe(d.Seconds())
}

// ObserveSlotReplicaMoveFailure increments one bounded Slot replica move failure reason.
func (m *NodeLifecycleMetrics) ObserveSlotReplicaMoveFailure(reason string) {
	if m == nil {
		return
	}
	m.slotMoveFailures.WithLabelValues(normalizeBoundedMetricLabel(reason, slotReplicaMoveFailureReasons)).Inc()
}

// SetDiscoveryMembershipRevision records the latest locally visible membership revision.
func (m *NodeLifecycleMetrics) SetDiscoveryMembershipRevision(revision uint64) {
	if m == nil {
		return
	}
	m.membershipRevision.Set(float64(revision))
}

func normalizeBoundedMetricLabel(value string, allowed map[string]struct{}) string {
	if value == "" {
		return "unknown"
	}
	if _, ok := allowed[value]; !ok {
		return "other"
	}
	return value
}
