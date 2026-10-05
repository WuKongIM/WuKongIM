package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"time"
)

type sendPermissionMetrics struct {
	counts   *prometheus.CounterVec
	stages   *prometheus.HistogramVec
	rejected *prometheus.CounterVec
	inflight prometheus.Gauge
	owned    *prometheus.GaugeVec
}

func newSendPermissionMetrics(reg prometheus.Registerer, labels prometheus.Labels) *sendPermissionMetrics {
	m := &sendPermissionMetrics{
		counts:   prometheus.NewCounterVec(prometheus.CounterOpts{Name: "wukongim_message_permission_counts_total", Help: "Permission plan, transport and fact counts by fixed kind.", ConstLabels: labels}, []string{"kind"}),
		stages:   prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "wukongim_message_permission_duration_seconds", Help: "Permission stage latency, separating quorum barriers from transport and storage.", ConstLabels: labels, Buckets: gatewayFrameDurationBuckets}, []string{"stage", "result"}),
		rejected: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "wukongim_message_send_ban_rejections_total", Help: "Message admissions rejected by a user or source channel send ban.", ConstLabels: labels}, []string{"scope"}),
		inflight: prometheus.NewGauge(prometheus.GaugeOpts{Name: "wukongim_message_permission_inflight", Help: "Currently admitted node permission envelopes.", ConstLabels: labels}),
	}
	m.owned = prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "wukongim_message_permission_cohort_owned", Help: "Owned ingress permission calls, cohorts and conservative retained-memory credits (not allocator bytes).", ConstLabels: labels}, []string{"kind"})
	for _, kind := range []string{"calls", "cohorts", "budget_bytes"} {
		m.owned.WithLabelValues(kind)
	}
	reg.MustRegister(m.counts, m.stages, m.rejected, m.inflight, m.owned)
	return m
}

// ObserveSendPermissionCount records only fixed dimensions, never entity IDs.
func (m *MessageMetrics) ObserveSendPermissionCount(kind string, n int) {
	if m == nil || m.sendPermission == nil || n < 0 {
		return
	}
	switch kind {
	case "messages", "users", "channels", "facts_before", "facts", "slot_groups", "node_envelopes", "local_envelopes", "request_bytes", "response_bytes", "cohorts", "cohort_requests", "cohort_facts", "cohort_busy":
	default:
		kind = "unknown"
	}
	m.sendPermission.counts.WithLabelValues(kind).Add(float64(n))
}

// ObserveSendPermissionStage separates fresh-read work from end-to-end SEND time.
func (m *MessageMetrics) ObserveSendPermissionStage(stage, result string, d time.Duration) {
	if m == nil || m.sendPermission == nil {
		return
	}
	switch stage {
	case "plan", "route", "rpc", "barrier", "snapshot", "evaluate", "admission":
	default:
		stage = "unknown"
	}
	switch result {
	case "ok", "unavailable", "stale_route", "busy", "invalid":
	default:
		result = "unknown"
	}
	m.sendPermission.stages.WithLabelValues(stage, result).Observe(d.Seconds())
}

// ObserveSendPermissionInflight balances admission and completion without queues.
func (m *MessageMetrics) ObserveSendPermissionInflight(delta int) {
	if m != nil && m.sendPermission != nil {
		m.sendPermission.inflight.Add(float64(delta))
	}
}

// ObserveSendBanRejection reports the responsible policy scope without identities.
func (m *MessageMetrics) ObserveSendBanRejection(scope string, n int) {
	if m == nil || m.sendPermission == nil || n < 0 {
		return
	}
	if scope != "user" && scope != "channel" {
		scope = "unknown"
	}
	m.sendPermission.rejected.WithLabelValues(scope).Add(float64(n))
}

// ObserveSendPermissionCohortOwned balances ownership until workers join and
// aligned results transfer to their caller. Identity values never become labels.
func (m *MessageMetrics) ObserveSendPermissionCohortOwned(kind string, delta int) {
	if m == nil || m.sendPermission == nil {
		return
	}
	switch kind {
	case "calls", "cohorts", "budget_bytes":
	default:
		kind = "unknown"
	}
	m.sendPermission.owned.WithLabelValues(kind).Add(float64(delta))
}
