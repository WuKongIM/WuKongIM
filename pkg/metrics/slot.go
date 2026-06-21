package metrics

import (
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

type SlotMetrics struct {
	proposalsTotal    *prometheus.CounterVec
	proposalAdmission *prometheus.CounterVec
	applyDuration     *prometheus.HistogramVec
	applyGap          *prometheus.GaugeVec
	leaderElections   prometheus.Counter
	leaderChanges     *prometheus.CounterVec
	replicaLag        *prometheus.GaugeVec
}

func newSlotMetrics(registry prometheus.Registerer, labels prometheus.Labels) *SlotMetrics {
	m := &SlotMetrics{
		proposalsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_slot_proposals_total",
			Help:        "Total number of slot proposal operations.",
			ConstLabels: labels,
		}, []string{"slot_id"}),
		proposalAdmission: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_slot_proposal_admission_total",
			Help:        "Total Slot proposal admission decisions by class and result.",
			ConstLabels: labels,
		}, []string{"class", "result"}),
		applyDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_slot_apply_duration_seconds",
			Help:        "Slot proposal apply latency in seconds.",
			ConstLabels: labels,
			Buckets:     gatewayFrameDurationBuckets,
		}, []string{"slot_id"}),
		applyGap: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_slot_apply_gap",
			Help:        "Slot committed-to-applied Raft log gap.",
			ConstLabels: labels,
		}, []string{"slot_id"}),
		leaderElections: prometheus.NewCounter(prometheus.CounterOpts{
			Name:        "wukongim_slot_leader_elections_total",
			Help:        "Total number of observed slot leader changes.",
			ConstLabels: labels,
		}),
		leaderChanges: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_slot_leader_changes_total",
			Help:        "Total number of observed slot leader changes grouped by physical slot and cause.",
			ConstLabels: labels,
		}, []string{"slot_id", "cause"}),
		replicaLag: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_slot_replica_lag_seconds",
			Help:        "Current Slot replica lag in seconds grouped by physical slot and replica node.",
			ConstLabels: labels,
		}, []string{"slot_id", "replica_node"}),
	}

	registry.MustRegister(
		m.proposalsTotal,
		m.proposalAdmission,
		m.applyDuration,
		m.applyGap,
		m.leaderElections,
		m.leaderChanges,
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

// ObserveProposalAdmission records one Slot proposal admission decision.
func (m *SlotMetrics) ObserveProposalAdmission(class string, result string) {
	if m == nil {
		return
	}
	m.proposalAdmission.WithLabelValues(class, result).Inc()
}

func (m *SlotMetrics) ObserveLeaderChange(slotID uint32) {
	if m == nil {
		return
	}
	m.leaderElections.Inc()
	m.ObserveLeaderChangeCause(slotID, "election")
}

// ObserveLeaderChangeCause records a Slot leader change without affecting the
// legacy election-only aggregate used by the stability monitor.
func (m *SlotMetrics) ObserveLeaderChangeCause(slotID uint32, cause string) {
	if m == nil {
		return
	}
	if cause == "" {
		cause = "unknown"
	}
	m.leaderChanges.WithLabelValues(strconv.FormatUint(uint64(slotID), 10), cause).Inc()
}

// SetApplyGap records the committed-to-applied Raft log gap for one Slot.
func (m *SlotMetrics) SetApplyGap(slotID uint32, gap uint64) {
	if m == nil {
		return
	}
	m.applyGap.WithLabelValues(strconv.FormatUint(uint64(slotID), 10)).Set(float64(gap))
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
