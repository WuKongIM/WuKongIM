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
	replicaLag      *prometheus.GaugeVec
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
		replicaLag: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_slot_replica_lag_seconds",
			Help:        "Current Slot replica lag in seconds grouped by physical slot and replica node.",
			ConstLabels: labels,
		}, []string{"slot_id", "replica_node"}),
	}

	registry.MustRegister(
		m.proposalsTotal,
		m.applyDuration,
		m.leaderElections,
		m.replicaLag,
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

// SetReplicaLag records the current lag for one bounded Slot replica.
func (m *SlotMetrics) SetReplicaLag(slotID uint32, replicaNode uint64, lag time.Duration) {
	if m == nil {
		return
	}
	m.replicaLag.WithLabelValues(
		strconv.FormatUint(uint64(slotID), 10),
		strconv.FormatUint(replicaNode, 10),
	).Set(lag.Seconds())
}
