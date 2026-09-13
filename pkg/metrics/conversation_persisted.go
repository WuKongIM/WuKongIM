package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"time"
)

// persistedReadMetrics reports node-local batches, never per-Channel series.
// There is no serving-node waiting queue; occupancy samples include all callers.
type persistedReadMetrics struct {
	admission *prometheus.CounterVec
	inflight  *prometheus.GaugeVec
	limit     prometheus.Gauge
	occupancy *prometheus.HistogramVec
	duration  *prometheus.HistogramVec
	items     *prometheus.HistogramVec
}

func newPersistedReadMetrics(reg prometheus.Registerer, labels prometheus.Labels) *persistedReadMetrics {
	m := &persistedReadMetrics{
		admission: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "wukongim_conversation_persisted_admission_total", Help: "Serving-node persisted read admissions and immediate refusals.", ConstLabels: labels}, []string{"kind", "result"}),
		inflight:  prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "wukongim_conversation_persisted_inflight", Help: "Currently admitted persisted read batches by kind.", ConstLabels: labels}, []string{"kind"}),
		limit:     prometheus.NewGauge(prometheus.GaugeOpts{Name: "wukongim_conversation_persisted_limit", Help: "Shared serving-node persisted read batch limit, observed at admission.", ConstLabels: labels}),
		occupancy: prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "wukongim_conversation_persisted_occupancy", Help: "Shared occupied batch slots sampled at each admission attempt; concurrent completions can change the sample.", ConstLabels: labels, Buckets: []float64{0, 1, 2, 4, 8, 12, 14, 15, 16}}, []string{"kind", "result"}),
		duration:  prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "wukongim_conversation_persisted_hold_seconds", Help: "Wall time holding a serving-node persisted read slot; excludes pre-admission routing and RPC.", ConstLabels: labels, Buckets: gatewayFrameDurationBuckets}, []string{"kind", "result"}),
		items:     prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "wukongim_conversation_persisted_batch_items", Help: "Requested items in each admitted persisted read batch.", ConstLabels: labels, Buckets: conversationListSizeBuckets}, []string{"kind"}),
	}
	for _, kind := range []string{"heads", "recents", "other"} {
		m.inflight.WithLabelValues(kind)
		m.items.WithLabelValues(kind)
		for _, result := range []string{"accepted", "rejected"} {
			m.admission.WithLabelValues(kind, result)
			m.occupancy.WithLabelValues(kind, result)
		}
		for _, result := range []string{"ok", "error", "byte_budget"} {
			m.duration.WithLabelValues(kind, result)
		}
	}
	reg.MustRegister(m.admission, m.inflight, m.limit, m.occupancy, m.duration, m.items)
	return m
}
func persistedReadKind(kind string) string {
	switch kind {
	case "heads", "recents":
		return kind
	default:
		return "other"
	}
}

// ObservePersistedReadAdmission records the exact admission decision, not an inferred queue overflow.
func (m *ConversationMetrics) ObservePersistedReadAdmission(kind string, accepted bool, inUse, limit int) {
	if m == nil || m.persisted == nil {
		return
	}
	kind = persistedReadKind(kind)
	result := "rejected"
	if accepted {
		result = "accepted"
		m.persisted.inflight.WithLabelValues(kind).Inc()
	}
	m.persisted.admission.WithLabelValues(kind, result).Inc()
	m.persisted.occupancy.WithLabelValues(kind, result).Observe(float64(nonNegative(inUse)))
	m.persisted.limit.Set(float64(nonNegative(limit)))
}

// ObservePersistedReadCompletion must be called exactly once for each accepted batch.
func (m *ConversationMetrics) ObservePersistedReadCompletion(kind, result string, items int, duration time.Duration) {
	if m == nil || m.persisted == nil {
		return
	}
	kind = persistedReadKind(kind)
	switch result {
	case "ok", "byte_budget":
	default:
		result = "error"
	}
	m.persisted.inflight.WithLabelValues(kind).Dec()
	m.persisted.duration.WithLabelValues(kind, result).Observe(nonNegativeConversationDuration(duration).Seconds())
	m.persisted.items.WithLabelValues(kind).Observe(float64(nonNegative(items)))
}
