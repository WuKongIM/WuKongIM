package metrics

import (
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// PluginMetrics exposes internal plugin hook admission and invocation metrics.
type PluginMetrics struct {
	enqueueTotal   *prometheus.CounterVec
	enqueueWait    *prometheus.HistogramVec
	invokeTotal    *prometheus.CounterVec
	invokeDuration *prometheus.HistogramVec
}

func newPluginMetrics(registry prometheus.Registerer, labels prometheus.Labels) *PluginMetrics {
	m := &PluginMetrics{
		enqueueTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_plugin_hook_enqueue_total",
			Help:        "Total internal plugin hook enqueue attempts by method and result.",
			ConstLabels: labels,
		}, []string{"method", "result"}),
		enqueueWait: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_plugin_hook_enqueue_wait_seconds",
			Help:        "Internal plugin hook enqueue wait time in seconds.",
			ConstLabels: labels,
			Buckets:     gatewayFrameDurationBuckets,
		}, []string{"method", "result"}),
		invokeTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_plugin_hook_invoke_total",
			Help:        "Total internal plugin hook invocation attempts by method and result.",
			ConstLabels: labels,
		}, []string{"method", "result"}),
		invokeDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_plugin_hook_invoke_duration_seconds",
			Help:        "Internal plugin hook invocation latency in seconds.",
			ConstLabels: labels,
			Buckets:     gatewayFrameDurationBuckets,
		}, []string{"method", "result"}),
	}
	registry.MustRegister(
		m.enqueueTotal,
		m.enqueueWait,
		m.invokeTotal,
		m.invokeDuration,
	)
	return m
}

// ObserveHookEnqueue records one plugin hook enqueue attempt.
func (m *PluginMetrics) ObserveHookEnqueue(method, result string, wait time.Duration) {
	if m == nil {
		return
	}
	method = normalizePluginMetricLabel(method, "unknown")
	result = normalizePluginMetricLabel(result, "unknown")
	m.enqueueTotal.WithLabelValues(method, result).Inc()
	m.enqueueWait.WithLabelValues(method, result).Observe(wait.Seconds())
}

// ObserveHookInvoke records one plugin hook invocation attempt.
func (m *PluginMetrics) ObserveHookInvoke(method, result string, d time.Duration) {
	if m == nil {
		return
	}
	method = normalizePluginMetricLabel(method, "unknown")
	result = normalizePluginMetricLabel(result, "unknown")
	m.invokeTotal.WithLabelValues(method, result).Inc()
	m.invokeDuration.WithLabelValues(method, result).Observe(d.Seconds())
}

func normalizePluginMetricLabel(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}
