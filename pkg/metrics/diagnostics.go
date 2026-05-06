package metrics

import "github.com/prometheus/client_golang/prometheus"

// DiagnosticsMetrics exposes low-cardinality counters and gauges for diagnostics events.
type DiagnosticsMetrics struct {
	eventsRecorded *prometheus.CounterVec
	eventsDropped  *prometheus.CounterVec
	bufferUsage    prometheus.Gauge
}

func newDiagnosticsMetrics(registry prometheus.Registerer, labels prometheus.Labels) *DiagnosticsMetrics {
	m := &DiagnosticsMetrics{
		eventsRecorded: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_diagnostics_events_recorded_total",
			Help:        "Total number of diagnostics events recorded by stage and result.",
			ConstLabels: labels,
		}, []string{"stage", "result"}),
		eventsDropped: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_diagnostics_events_dropped_total",
			Help:        "Total number of diagnostics events dropped by low-cardinality reason.",
			ConstLabels: labels,
		}, []string{"reason"}),
		bufferUsage: prometheus.NewGauge(prometheus.GaugeOpts{
			Name:        "wukongim_diagnostics_buffer_usage_ratio",
			Help:        "Current diagnostics ring buffer usage ratio from 0 to 1.",
			ConstLabels: labels,
		}),
	}

	registry.MustRegister(m.eventsRecorded, m.eventsDropped, m.bufferUsage)
	return m
}

// RecordEvent increments the diagnostics event counter for a stable stage and result.
func (m *DiagnosticsMetrics) RecordEvent(stage, result string) {
	if m == nil {
		return
	}
	m.eventsRecorded.WithLabelValues(stage, result).Inc()
}

// RecordDropped increments the diagnostics drop counter for a low-cardinality reason.
func (m *DiagnosticsMetrics) RecordDropped(reason string) {
	if m == nil {
		return
	}
	m.eventsDropped.WithLabelValues(reason).Inc()
}

// SetBufferUsageRatio sets the current diagnostics ring buffer usage ratio.
func (m *DiagnosticsMetrics) SetBufferUsageRatio(v float64) {
	if m == nil {
		return
	}
	m.bufferUsage.Set(v)
}
