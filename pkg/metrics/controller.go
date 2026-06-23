package metrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	controllerTaskKinds        = []string{"bootstrap", "repair", "rebalance", "leader_transfer"}
	controllerTaskResults      = []string{"ok", "fail", "timeout", "safety_check"}
	controllerMigrationResults = []string{"ok", "fail", "abort"}
)

type ControllerMetrics struct {
	decisionsTotal   *prometheus.CounterVec
	decisionDuration prometheus.Histogram
	tasksActive      *prometheus.GaugeVec
	tasksFailed      *prometheus.GaugeVec
	tasksCompleted   *prometheus.CounterVec
	migrationsActive prometheus.Gauge
	migrationsTotal  *prometheus.CounterVec
	nodesAlive       prometheus.Gauge
	nodesSuspect     prometheus.Gauge
	nodesDead        prometheus.Gauge
	slotLeaderSkew   prometheus.Gauge
	stateRevision    prometheus.Gauge
	leaderPresent    prometheus.Gauge
	applyGap         *prometheus.GaugeVec
	raftStepDepth    prometheus.Gauge
	raftStepCapacity prometheus.Gauge
	raftStepEnqueue  *prometheus.HistogramVec
}

func newControllerMetrics(registry prometheus.Registerer, labels prometheus.Labels) *ControllerMetrics {
	m := &ControllerMetrics{
		decisionsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_controller_decisions_total",
			Help:        "Total number of controller scheduling decisions.",
			ConstLabels: labels,
		}, []string{"type"}),
		decisionDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:        "wukongim_controller_decision_duration_seconds",
			Help:        "Controller scheduling decision latency in seconds.",
			ConstLabels: labels,
			Buckets:     gatewayFrameDurationBuckets,
		}),
		tasksActive: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_controller_tasks_active",
			Help:        "Number of active controller tasks grouped by type.",
			ConstLabels: labels,
		}, []string{"type"}),
		tasksFailed: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_controller_tasks_failed",
			Help:        "Number of active failed controller tasks grouped by type.",
			ConstLabels: labels,
		}, []string{"type"}),
		tasksCompleted: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_controller_tasks_completed_total",
			Help:        "Total number of completed controller tasks.",
			ConstLabels: labels,
		}, []string{"type", "result"}),
		migrationsActive: prometheus.NewGauge(prometheus.GaugeOpts{
			Name:        "wukongim_controller_hashslot_migrations_active",
			Help:        "Number of active hash slot migrations tracked by the controller.",
			ConstLabels: labels,
		}),
		migrationsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_controller_hashslot_migrations_total",
			Help:        "Total number of completed hash slot migrations.",
			ConstLabels: labels,
		}, []string{"result"}),
		nodesAlive: prometheus.NewGauge(prometheus.GaugeOpts{
			Name:        "wukongim_controller_nodes_alive",
			Help:        "Number of alive controller-tracked nodes.",
			ConstLabels: labels,
		}),
		nodesSuspect: prometheus.NewGauge(prometheus.GaugeOpts{
			Name:        "wukongim_controller_nodes_suspect",
			Help:        "Number of suspect controller-tracked nodes.",
			ConstLabels: labels,
		}),
		nodesDead: prometheus.NewGauge(prometheus.GaugeOpts{
			Name:        "wukongim_controller_nodes_dead",
			Help:        "Number of dead controller-tracked nodes.",
			ConstLabels: labels,
		}),
		slotLeaderSkew: prometheus.NewGauge(prometheus.GaugeOpts{
			Name:        "wukongim_controller_slot_leader_skew",
			Help:        "Max-minus-min Slot leader count skew across active data nodes.",
			ConstLabels: labels,
		}),
		stateRevision: prometheus.NewGauge(prometheus.GaugeOpts{
			Name:        "wukongim_controller_state_revision",
			Help:        "Latest locally visible ControllerV2 cluster-state revision.",
			ConstLabels: labels,
		}),
		leaderPresent: prometheus.NewGauge(prometheus.GaugeOpts{
			Name:        "wukongim_controller_leader_present",
			Help:        "Reports 1 when the local ControllerV2 snapshot has a known leader, otherwise 0.",
			ConstLabels: labels,
		}),
		applyGap: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_controller_apply_gap",
			Help:        "ControllerV2 committed-to-applied Raft log gap.",
			ConstLabels: labels,
		}, []string{"state"}),
		raftStepDepth: prometheus.NewGauge(prometheus.GaugeOpts{
			Name:        "wukongim_controller_raft_step_queue_depth",
			Help:        "Number of pending inbound ControllerV2 Raft Step messages.",
			ConstLabels: labels,
		}),
		raftStepCapacity: prometheus.NewGauge(prometheus.GaugeOpts{
			Name:        "wukongim_controller_raft_step_queue_capacity",
			Help:        "Capacity of the inbound ControllerV2 Raft Step message queue.",
			ConstLabels: labels,
		}),
		raftStepEnqueue: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_controller_raft_step_enqueue_duration_seconds",
			Help:        "Elapsed time to enqueue an inbound ControllerV2 Raft Step message.",
			ConstLabels: labels,
			Buckets:     gatewayFrameDurationBuckets,
		}, []string{"result"}),
	}

	registry.MustRegister(
		m.decisionsTotal,
		m.decisionDuration,
		m.tasksActive,
		m.tasksFailed,
		m.tasksCompleted,
		m.migrationsActive,
		m.migrationsTotal,
		m.nodesAlive,
		m.nodesSuspect,
		m.nodesDead,
		m.slotLeaderSkew,
		m.stateRevision,
		m.leaderPresent,
		m.applyGap,
		m.raftStepDepth,
		m.raftStepCapacity,
		m.raftStepEnqueue,
	)

	for _, kind := range controllerTaskKinds {
		m.decisionsTotal.WithLabelValues(kind)
		m.tasksActive.WithLabelValues(kind).Set(0)
		m.tasksFailed.WithLabelValues(kind).Set(0)
		for _, result := range controllerTaskResults {
			m.tasksCompleted.WithLabelValues(kind, result)
		}
	}
	m.migrationsActive.Set(0)
	m.slotLeaderSkew.Set(0)
	for _, result := range controllerMigrationResults {
		m.migrationsTotal.WithLabelValues(result)
	}

	return m
}

func (m *ControllerMetrics) ObserveDecision(kind string, dur time.Duration) {
	if m == nil {
		return
	}
	m.decisionsTotal.WithLabelValues(kind).Inc()
	m.decisionDuration.Observe(dur.Seconds())
}

func (m *ControllerMetrics) ObserveTaskCompleted(kind, result string) {
	if m == nil {
		return
	}
	m.tasksCompleted.WithLabelValues(kind, result).Inc()
}

func (m *ControllerMetrics) SetMigrationsActive(count int) {
	if m == nil {
		return
	}
	m.migrationsActive.Set(float64(count))
}

func (m *ControllerMetrics) ObserveMigrationCompleted(result string) {
	if m == nil {
		return
	}
	m.migrationsTotal.WithLabelValues(result).Inc()
}

func (m *ControllerMetrics) SetNodeCounts(alive, suspect, dead int) {
	if m == nil {
		return
	}
	m.nodesAlive.Set(float64(alive))
	m.nodesSuspect.Set(float64(suspect))
	m.nodesDead.Set(float64(dead))
}

func (m *ControllerMetrics) SetTaskActive(counts map[string]int) {
	if m == nil {
		return
	}
	for _, kind := range controllerTaskKinds {
		m.tasksActive.WithLabelValues(kind).Set(float64(counts[kind]))
	}
}

func (m *ControllerMetrics) SetTaskFailed(counts map[string]int) {
	if m == nil {
		return
	}
	for _, kind := range controllerTaskKinds {
		m.tasksFailed.WithLabelValues(kind).Set(float64(counts[kind]))
	}
}

func (m *ControllerMetrics) SetSlotLeaderSkew(skew int) {
	if m == nil {
		return
	}
	m.slotLeaderSkew.Set(float64(skew))
}

func (m *ControllerMetrics) SetStateRevision(revision uint64) {
	if m == nil {
		return
	}
	m.stateRevision.Set(float64(revision))
}

func (m *ControllerMetrics) SetLeaderPresent(present bool) {
	if m == nil {
		return
	}
	if present {
		m.leaderPresent.Set(1)
		return
	}
	m.leaderPresent.Set(0)
}

// SetApplyGap records the current ControllerV2 committed-to-applied Raft log gap.
func (m *ControllerMetrics) SetApplyGap(gap uint64) {
	if m == nil {
		return
	}
	m.applyGap.WithLabelValues("current").Set(float64(gap))
}

func (m *ControllerMetrics) SetControllerRaftStepQueue(depth int, capacity int) {
	if m == nil {
		return
	}
	m.raftStepDepth.Set(float64(depth))
	m.raftStepCapacity.Set(float64(capacity))
}

func (m *ControllerMetrics) ObserveControllerRaftStepEnqueue(result string, d time.Duration) {
	if m == nil {
		return
	}
	m.raftStepEnqueue.WithLabelValues(result).Observe(d.Seconds())
}
