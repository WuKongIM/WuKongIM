package metrics

import (
	"strconv"
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
