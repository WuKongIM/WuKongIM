package metrics

import (
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

var conversationListSizeBuckets = []float64{0, 1, 2, 4, 8, 16, 32, 64, 128, 256, 512, 1000, 2000, 5000}

// ConversationMetrics exposes conversation list read latency and page-shape metrics.
type ConversationMetrics struct {
	listTotal              *prometheus.CounterVec
	listDuration           *prometheus.HistogramVec
	listScannedMemberships *prometheus.HistogramVec
	listReturnedItems      *prometheus.HistogramVec
}

func newConversationMetrics(registry prometheus.Registerer, labels prometheus.Labels) *ConversationMetrics {
	m := &ConversationMetrics{
		listTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_conversation_list_total",
			Help:        "Total number of conversation list requests.",
			ConstLabels: labels,
		}, []string{"result", "truncated"}),
		listDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_conversation_list_duration_seconds",
			Help:        "Conversation list request latency in seconds.",
			ConstLabels: labels,
			Buckets:     gatewayFrameDurationBuckets,
		}, []string{"result", "truncated"}),
		listScannedMemberships: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_conversation_list_scanned_memberships",
			Help:        "Membership rows scanned by conversation list requests.",
			ConstLabels: labels,
			Buckets:     conversationListSizeBuckets,
		}, []string{"result", "truncated"}),
		listReturnedItems: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_conversation_list_returned_items",
			Help:        "Conversation rows returned by conversation list requests.",
			ConstLabels: labels,
			Buckets:     conversationListSizeBuckets,
		}, []string{"result", "truncated"}),
	}

	registry.MustRegister(
		m.listTotal,
		m.listDuration,
		m.listScannedMemberships,
		m.listReturnedItems,
	)

	return m
}

// ObserveList records one conversation list request result and page shape.
func (m *ConversationMetrics) ObserveList(result string, truncated bool, dur time.Duration, scannedMemberships, returnedItems int) {
	if m == nil {
		return
	}
	if result == "" {
		result = "unknown"
	}
	truncatedLabel := strconv.FormatBool(truncated)
	m.listTotal.WithLabelValues(result, truncatedLabel).Inc()
	m.listDuration.WithLabelValues(result, truncatedLabel).Observe(dur.Seconds())
	m.listScannedMemberships.WithLabelValues(result, truncatedLabel).Observe(float64(nonNegative(scannedMemberships)))
	m.listReturnedItems.WithLabelValues(result, truncatedLabel).Observe(float64(nonNegative(returnedItems)))
}

func nonNegative(v int) int {
	if v < 0 {
		return 0
	}
	return v
}
