package metrics

import "github.com/prometheus/client_golang/prometheus"

// AntsPoolMetrics exposes occupancy gauges for pools backed by github.com/panjf2000/ants/v2.
type AntsPoolMetrics struct {
	running     *prometheus.GaugeVec
	capacity    *prometheus.GaugeVec
	waiting     *prometheus.GaugeVec
	utilization *prometheus.GaugeVec
}

func newAntsPoolMetrics(registry prometheus.Registerer, labels prometheus.Labels) *AntsPoolMetrics {
	m := &AntsPoolMetrics{
		running: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_ants_pool_running",
			Help:        "Current running workers in ants/v2 pools.",
			ConstLabels: labels,
		}, []string{"component", "pool"}),
		capacity: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_ants_pool_capacity",
			Help:        "Configured worker capacity for ants/v2 pools.",
			ConstLabels: labels,
		}, []string{"component", "pool"}),
		waiting: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_ants_pool_waiting",
			Help:        "Current blocked submissions waiting on ants/v2 pools.",
			ConstLabels: labels,
		}, []string{"component", "pool"}),
		utilization: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_ants_pool_utilization",
			Help:        "Current running-to-capacity ratio for ants/v2 pools.",
			ConstLabels: labels,
		}, []string{"component", "pool"}),
	}

	registry.MustRegister(
		m.running,
		m.capacity,
		m.waiting,
		m.utilization,
	)

	return m
}

// SetUsage records current ants/v2 pool occupancy.
func (m *AntsPoolMetrics) SetUsage(component, pool string, running int, capacity int, waiting int) {
	if m == nil {
		return
	}
	running = clampRuntimePressureInt(running)
	capacity = clampRuntimePressureInt(capacity)
	waiting = clampRuntimePressureInt(waiting)
	utilization := 0.0
	if capacity > 0 {
		utilization = float64(running) / float64(capacity)
	}
	labelValues := []string{
		normalizeRuntimePressureLabel(component),
		normalizeRuntimePressureLabel(pool),
	}
	m.running.WithLabelValues(labelValues...).Set(float64(running))
	m.capacity.WithLabelValues(labelValues...).Set(float64(capacity))
	m.waiting.WithLabelValues(labelValues...).Set(float64(waiting))
	m.utilization.WithLabelValues(labelValues...).Set(utilization)
}
