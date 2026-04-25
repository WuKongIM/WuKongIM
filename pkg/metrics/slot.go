package metrics

import (
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

type SlotMetrics struct {
	proposalsTotal  *prometheus.CounterVec
	applyDuration   *prometheus.HistogramVec
	leaderElections prometheus.Counter
}

func newSlotMetrics(registry prometheus.Registerer, labels prometheus.Labels) *SlotMetrics {
	m := &SlotMetrics{
		proposalsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_slot_proposals_total",
			Help:        "Total number of slot proposal operations.",
			ConstLabels: labels,
		}, []string{"slot_id"}),
		applyDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_slot_apply_duration_seconds",
			Help:        "Slot proposal apply latency in seconds.",
			ConstLabels: labels,
			Buckets:     gatewayFrameDurationBuckets,
		}, []string{"slot_id"}),
		leaderElections: prometheus.NewCounter(prometheus.CounterOpts{
			Name:        "wukongim_slot_leader_elections_total",
			Help:        "Total number of observed slot leader changes.",
			ConstLabels: labels,
		}),
	}

	registry.MustRegister(
		m.proposalsTotal,
		m.applyDuration,
		m.leaderElections,
	)

	return m
}

func (m *SlotMetrics) ObserveProposal(slotID uint32, dur time.Duration) {
	if m == nil {
		return
	}
	slotLabel := strconv.FormatUint(uint64(slotID), 10)
	m.proposalsTotal.WithLabelValues(slotLabel).Inc()
	m.applyDuration.WithLabelValues(slotLabel).Observe(dur.Seconds())
}

func (m *SlotMetrics) ObserveLeaderChange(_ uint32) {
	if m == nil {
		return
	}
	m.leaderElections.Inc()
}
