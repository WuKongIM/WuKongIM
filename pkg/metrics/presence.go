package metrics

import (
	"strconv"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// PresenceMetrics exposes bounded authority expiry and owner touch-flush metrics.
type PresenceMetrics struct {
	expiryTotal            *prometheus.CounterVec
	expiryDuration         *prometheus.HistogramVec
	expiryDueBuckets       prometheus.Gauge
	expiryExaminedRoutes   prometheus.Gauge
	expiredRoutes          prometheus.Gauge
	expiryIndexRoutes      prometheus.Gauge
	expiryIndexBuckets     prometheus.Gauge
	touchFlushTotal        *prometheus.CounterVec
	touchFlushDuration     *prometheus.HistogramVec
	touchFlushRoutes       *prometheus.CounterVec
	touchFlushChunks       prometheus.Counter
	touchFlushTargetGroups prometheus.Counter
	endpointLookupTotal    *prometheus.CounterVec
	endpointLookupItems    *prometheus.CounterVec
	endpointLookupGroups   *prometheus.CounterVec
	endpointLookupDuration *prometheus.HistogramVec
	endpointLookupSeries   [4][10][2]presenceEndpointLookupSeries
}

type presenceEndpointLookupSeries struct {
	once     sync.Once
	total    prometheus.Counter
	items    prometheus.Counter
	groups   prometheus.Counter
	duration prometheus.Observer
}

func newPresenceMetrics(registry prometheus.Registerer, labels prometheus.Labels) *PresenceMetrics {
	m := &PresenceMetrics{
		expiryTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_presence_expiry_total",
			Help:        "Total indexed presence expiry passes by result.",
			ConstLabels: labels,
		}, []string{"result"}),
		expiryDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_presence_expiry_duration_seconds",
			Help:        "Indexed presence expiry pass duration in seconds.",
			ConstLabels: labels,
			Buckets:     runtimePressureDurationBuckets,
		}, []string{"result"}),
		expiryDueBuckets: prometheus.NewGauge(prometheus.GaugeOpts{
			Name:        "wukongim_presence_expiry_due_buckets",
			Help:        "Due expiry buckets examined by the latest presence expiry pass.",
			ConstLabels: labels,
		}),
		expiryExaminedRoutes: prometheus.NewGauge(prometheus.GaugeOpts{
			Name:        "wukongim_presence_expiry_examined_routes",
			Help:        "Route identities examined by the latest presence expiry pass.",
			ConstLabels: labels,
		}),
		expiredRoutes: prometheus.NewGauge(prometheus.GaugeOpts{
			Name:        "wukongim_presence_expired_routes",
			Help:        "Routes removed by the latest presence expiry pass.",
			ConstLabels: labels,
		}),
		expiryIndexRoutes: prometheus.NewGauge(prometheus.GaugeOpts{
			Name:        "wukongim_presence_expiry_index_routes",
			Help:        "Routes retained in the presence expiry index after the latest pass.",
			ConstLabels: labels,
		}),
		expiryIndexBuckets: prometheus.NewGauge(prometheus.GaugeOpts{
			Name:        "wukongim_presence_expiry_index_buckets",
			Help:        "Buckets retained in the presence expiry index after the latest pass.",
			ConstLabels: labels,
		}),
		touchFlushTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_presence_touch_flush_total",
			Help:        "Total bounded presence touch flushes by result and budget exhaustion.",
			ConstLabels: labels,
		}, []string{"result", "budget_reached"}),
		touchFlushDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_presence_touch_flush_duration_seconds",
			Help:        "Bounded presence touch flush duration in seconds.",
			ConstLabels: labels,
			Buckets:     runtimePressureDurationBuckets,
		}, []string{"result", "budget_reached"}),
		touchFlushRoutes: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_presence_touch_flush_routes",
			Help:        "Total presence touch routes processed at each bounded flush stage.",
			ConstLabels: labels,
		}, []string{"stage"}),
		touchFlushChunks: prometheus.NewCounter(prometheus.CounterOpts{
			Name:        "wukongim_presence_touch_flush_chunks",
			Help:        "Total chunks processed by bounded presence touch flushes.",
			ConstLabels: labels,
		}),
		touchFlushTargetGroups: prometheus.NewCounter(prometheus.CounterOpts{
			Name:        "wukongim_presence_touch_flush_target_groups",
			Help:        "Total authority target groups processed by bounded presence touch flushes.",
			ConstLabels: labels,
		}),
		endpointLookupTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_presence_endpoint_lookup_total",
			Help:        "Total exact-target presence endpoint batch lookups by bounded path, outcome, and retry state.",
			ConstLabels: labels,
		}, []string{"path", "outcome", "stale_retry"}),
		endpointLookupItems: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_presence_endpoint_lookup_items_total",
			Help:        "Total UID items handled by exact-target presence endpoint batch lookups.",
			ConstLabels: labels,
		}, []string{"path", "outcome", "stale_retry"}),
		endpointLookupGroups: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_presence_endpoint_lookup_groups_total",
			Help:        "Total exact target groups handled by presence endpoint batch lookups.",
			ConstLabels: labels,
		}, []string{"path", "outcome", "stale_retry"}),
		endpointLookupDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_presence_endpoint_lookup_duration_seconds",
			Help:        "Exact-target presence endpoint batch lookup latency in seconds.",
			ConstLabels: labels,
			Buckets:     runtimePressureDurationBuckets,
		}, []string{"path", "outcome", "stale_retry"}),
	}

	registry.MustRegister(
		m.expiryTotal,
		m.expiryDuration,
		m.expiryDueBuckets,
		m.expiryExaminedRoutes,
		m.expiredRoutes,
		m.expiryIndexRoutes,
		m.expiryIndexBuckets,
		m.touchFlushTotal,
		m.touchFlushDuration,
		m.touchFlushRoutes,
		m.touchFlushChunks,
		m.touchFlushTargetGroups,
		m.endpointLookupTotal,
		m.endpointLookupItems,
		m.endpointLookupGroups,
		m.endpointLookupDuration,
	)

	return m
}

// ObserveExpiry records one indexed presence expiry pass and its latest bounded work counts.
func (m *PresenceMetrics) ObserveExpiry(result string, dur time.Duration, dueBuckets, examined, expired, indexRoutes, indexBuckets int) {
	if m == nil {
		return
	}
	m.expiryTotal.WithLabelValues(result).Inc()
	m.expiryDuration.WithLabelValues(result).Observe(dur.Seconds())
	m.expiryDueBuckets.Set(float64(dueBuckets))
	m.expiryExaminedRoutes.Set(float64(examined))
	m.expiredRoutes.Set(float64(expired))
	m.expiryIndexRoutes.Set(float64(indexRoutes))
	m.expiryIndexBuckets.Set(float64(indexBuckets))
}

// ObserveTouchFlush records one bounded owner touch flush and cumulative work counts.
func (m *PresenceMetrics) ObserveTouchFlush(result string, dur time.Duration, drained, resolved, sent, requeued, chunks, targetGroups int, budgetReached bool) {
	if m == nil {
		return
	}
	budgetLabel := strconv.FormatBool(budgetReached)
	m.touchFlushTotal.WithLabelValues(result, budgetLabel).Inc()
	m.touchFlushDuration.WithLabelValues(result, budgetLabel).Observe(dur.Seconds())
	m.touchFlushRoutes.WithLabelValues("drained").Add(float64(drained))
	m.touchFlushRoutes.WithLabelValues("resolved").Add(float64(resolved))
	m.touchFlushRoutes.WithLabelValues("sent").Add(float64(sent))
	m.touchFlushRoutes.WithLabelValues("requeued").Add(float64(requeued))
	m.touchFlushChunks.Add(float64(chunks))
	m.touchFlushTargetGroups.Add(float64(targetGroups))
}

// ObserveEndpointLookup records one local, remote, or legacy exact-target lookup batch.
func (m *PresenceMetrics) ObserveEndpointLookup(path, outcome string, staleRetry bool, dur time.Duration, items, groups int) {
	if m == nil {
		return
	}
	path = normalizePresenceEndpointLookupPath(path)
	outcome = normalizePresenceEndpointLookupOutcome(outcome)
	retryIndex := 0
	if staleRetry {
		retryIndex = 1
	}
	series := &m.endpointLookupSeries[presenceEndpointLookupPathIndex(path)][presenceEndpointLookupOutcomeIndex(outcome)][retryIndex]
	series.once.Do(func() {
		retryLabel := strconv.FormatBool(staleRetry)
		series.total = m.endpointLookupTotal.WithLabelValues(path, outcome, retryLabel)
		series.items = m.endpointLookupItems.WithLabelValues(path, outcome, retryLabel)
		series.groups = m.endpointLookupGroups.WithLabelValues(path, outcome, retryLabel)
		series.duration = m.endpointLookupDuration.WithLabelValues(path, outcome, retryLabel)
	})
	if dur < 0 {
		dur = 0
	}
	series.total.Inc()
	series.items.Add(float64(nonNegative(items)))
	series.groups.Add(float64(nonNegative(groups)))
	series.duration.Observe(dur.Seconds())
}

func normalizePresenceEndpointLookupPath(path string) string {
	switch path {
	case "local_bulk", "remote_bulk", "legacy_fallback":
		return path
	default:
		return "unknown"
	}
}

func normalizePresenceEndpointLookupOutcome(outcome string) string {
	switch outcome {
	case "ok", "partial", "route_not_ready", "stale_route", "not_leader", "canceled", "deadline", "panic", "error":
		return outcome
	default:
		return "unknown"
	}
}

func presenceEndpointLookupPathIndex(path string) int {
	switch path {
	case "local_bulk":
		return 0
	case "remote_bulk":
		return 1
	case "legacy_fallback":
		return 2
	default:
		return 3
	}
}

func presenceEndpointLookupOutcomeIndex(outcome string) int {
	switch outcome {
	case "ok":
		return 0
	case "partial":
		return 1
	case "route_not_ready":
		return 2
	case "stale_route":
		return 3
	case "not_leader":
		return 4
	case "canceled":
		return 5
	case "deadline":
		return 6
	case "panic":
		return 7
	case "error":
		return 8
	default:
		return 9
	}
}
