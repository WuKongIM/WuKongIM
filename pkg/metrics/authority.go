package metrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// AuthorityMetrics exposes the internalv2 UID-authority SEND pipeline metrics.
type AuthorityMetrics struct {
	senderRouteTotal                 *prometheus.CounterVec
	recipientQueueTotal              *prometheus.CounterVec
	recipientDispatchTotal           *prometheus.CounterVec
	recipientDispatchDurationSeconds *prometheus.HistogramVec
}

func newAuthorityMetrics(registry prometheus.Registerer, labels prometheus.Labels) *AuthorityMetrics {
	m := &AuthorityMetrics{
		senderRouteTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_authority_sender_route_total",
			Help:        "Sender UID-authority routing decisions by normalized result.",
			ConstLabels: labels,
		}, []string{"result"}),
		recipientQueueTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_authority_recipient_queue_total",
			Help:        "Recipient committed-worker admission attempts by normalized result.",
			ConstLabels: labels,
		}, []string{"result"}),
		recipientDispatchTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_authority_recipient_dispatch_total",
			Help:        "Recipient authority dispatch attempts by phase and normalized result.",
			ConstLabels: labels,
		}, []string{"phase", "result"}),
		recipientDispatchDurationSeconds: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_authority_recipient_dispatch_duration_seconds",
			Help:        "Recipient authority dispatch latency in seconds by phase and normalized result.",
			ConstLabels: labels,
			Buckets:     gatewayFrameDurationBuckets,
		}, []string{"phase", "result"}),
	}
	registry.MustRegister(
		m.senderRouteTotal,
		m.recipientQueueTotal,
		m.recipientDispatchTotal,
		m.recipientDispatchDurationSeconds,
	)
	return m
}

// ObserveSenderRoute records one sender UID-authority route decision.
func (m *AuthorityMetrics) ObserveSenderRoute(result string) {
	if m == nil {
		return
	}
	m.senderRouteTotal.WithLabelValues(authorityResult(result)).Inc()
}

// ObserveRecipientQueue records one committed-worker queue admission outcome.
func (m *AuthorityMetrics) ObserveRecipientQueue(result string) {
	if m == nil {
		return
	}
	m.recipientQueueTotal.WithLabelValues(authorityQueueResult(result)).Inc()
}

// ObserveRecipientDispatch records one recipient authority dispatch phase.
func (m *AuthorityMetrics) ObserveRecipientDispatch(phase, result string, dur time.Duration) {
	if m == nil {
		return
	}
	phaseLabel := authorityDispatchPhase(phase)
	resultLabel := authorityResult(result)
	m.recipientDispatchTotal.WithLabelValues(phaseLabel, resultLabel).Inc()
	m.recipientDispatchDurationSeconds.WithLabelValues(phaseLabel, resultLabel).Observe(dur.Seconds())
}

func authorityDispatchPhase(phase string) string {
	switch phase {
	case "worker", "conversation", "delivery", "sender_rpc":
		return phase
	default:
		return "other"
	}
}

func authorityQueueResult(result string) string {
	switch result {
	case "accepted", "full", "closed", "sync_ok", "sync_error":
		return result
	default:
		return authorityResult(result)
	}
}

func authorityResult(result string) string {
	switch result {
	case "ok", "error", "local", "remote", "accepted", "full", "closed", "route_not_ready", "stale_route", "not_leader", "timeout", "auth_fail", "invalid", "cache_pressure":
		return result
	default:
		return "other"
	}
}
