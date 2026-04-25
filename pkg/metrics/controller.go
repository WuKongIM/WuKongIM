package metrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	controllerTaskKinds        = []string{"bootstrap", "repair", "rebalance"}
	controllerTaskResults      = []string{"ok", "fail", "timeout"}
	controllerMigrationResults = []string{"ok", "fail", "abort"}
)

type ControllerMetrics struct {
	decisionsTotal   *prometheus.CounterVec
	decisionDuration prometheus.Histogram
	tasksActive      *prometheus.GaugeVec
	tasksCompleted   *prometheus.CounterVec
	migrationsActive prometheus.Gauge
	migrationsTotal  *prometheus.CounterVec
	nodesAlive       prometheus.Gauge
	nodesSuspect     prometheus.Gauge
	nodesDead        prometheus.Gauge
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
	}

	registry.MustRegister(
		m.decisionsTotal,
		m.decisionDuration,
		m.tasksActive,
		m.tasksCompleted,
		m.migrationsActive,
		m.migrationsTotal,
		m.nodesAlive,
		m.nodesSuspect,
		m.nodesDead,
	)

	for _, kind := range controllerTaskKinds {
		m.decisionsTotal.WithLabelValues(kind)
		m.tasksActive.WithLabelValues(kind).Set(0)
		for _, result := range controllerTaskResults {
			m.tasksCompleted.WithLabelValues(kind, result)
		}
	}
	m.migrationsActive.Set(0)
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
