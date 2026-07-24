package metrics

import (
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

var opsMCPToolLabels = map[string]struct{}{
	"cluster_health": {}, "node_inspect": {}, "slot_inspect": {},
	"channel_runtime_inspect": {}, "controller_tasks_query": {}, "metrics_query_range": {},
	"logs_search": {}, "logs_context": {}, "diagnostics_query": {},
	"config_read_redacted": {}, "backup_inspect": {}, "pprof_analyze": {},
}

// OpsMCPMetrics exposes low-cardinality request and concurrency evidence.
type OpsMCPMetrics struct {
	requests     *prometheus.CounterVec
	duration     *prometheus.HistogramVec
	active       *prometheus.GaugeVec
	authFailures prometheus.Counter
}

func newOpsMCPMetrics(registry prometheus.Registerer, labels prometheus.Labels) *OpsMCPMetrics {
	metrics := &OpsMCPMetrics{
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_ops_mcp_requests_total",
			Help:        "Operations MCP tool calls and admission rejections by bounded tool and result.",
			ConstLabels: labels,
		}, []string{"tool", "result"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_ops_mcp_request_duration_seconds",
			Help:        "Operations MCP admitted tool-call duration by bounded tool and result.",
			ConstLabels: labels,
			Buckets:     []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 45},
		}, []string{"tool", "result"}),
		active: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_ops_mcp_requests_active",
			Help:        "Current admitted Operations MCP tool calls by bounded tool.",
			ConstLabels: labels,
		}, []string{"tool"}),
		authFailures: prometheus.NewCounter(prometheus.CounterOpts{
			Name:        "wukongim_ops_mcp_auth_failures_total",
			Help:        "Operations MCP bearer authentication failures observed by this Manager node.",
			ConstLabels: labels,
		}),
	}
	registry.MustRegister(metrics.requests, metrics.duration, metrics.active, metrics.authFailures)
	return metrics
}

// CallStarted records one admitted tool call.
func (m *OpsMCPMetrics) CallStarted(tool string) {
	if m == nil {
		return
	}
	m.active.WithLabelValues(normalizeOpsMCPTool(tool)).Inc()
}

// CallFinished records one completed admitted tool call.
func (m *OpsMCPMetrics) CallFinished(tool, result string, duration time.Duration) {
	if m == nil {
		return
	}
	tool = normalizeOpsMCPTool(tool)
	result = normalizeOpsMCPResult(result)
	if duration < 0 {
		duration = 0
	}
	m.active.WithLabelValues(tool).Dec()
	m.requests.WithLabelValues(tool, result).Inc()
	m.duration.WithLabelValues(tool, result).Observe(duration.Seconds())
}

// CallRejected records one call rejected before execution.
func (m *OpsMCPMetrics) CallRejected(tool, result string) {
	if m == nil {
		return
	}
	m.requests.WithLabelValues(normalizeOpsMCPTool(tool), normalizeOpsMCPResult(result)).Inc()
}

// AuthenticationFailed records one failed bearer authentication.
func (m *OpsMCPMetrics) AuthenticationFailed() {
	if m == nil {
		return
	}
	m.authFailures.Inc()
}

func normalizeOpsMCPTool(tool string) string {
	tool = strings.TrimSpace(tool)
	if _, ok := opsMCPToolLabels[tool]; !ok {
		return "unknown"
	}
	return tool
}

func normalizeOpsMCPResult(result string) string {
	switch strings.TrimSpace(result) {
	case "ok", "error", "rate_limited", "concurrency_limited":
		return strings.TrimSpace(result)
	default:
		return "error"
	}
}
