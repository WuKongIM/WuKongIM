package metrics

import (
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

var conversationListSizeBuckets = []float64{0, 1, 2, 4, 8, 16, 32, 64, 128, 256, 512, 1000, 2000, 5000}

// ConversationMetrics exposes conversation list read latency and page-shape metrics.
type ConversationMetrics struct {
	listTotal           *prometheus.CounterVec
	listDuration        *prometheus.HistogramVec
	listReturnedItems   *prometheus.HistogramVec
	listLastMessageHits *prometheus.HistogramVec
}

func newConversationMetrics(registry prometheus.Registerer, labels prometheus.Labels) *ConversationMetrics {
	m := &ConversationMetrics{
		listTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_conversation_list_total",
			Help:        "Total number of conversation list requests.",
			ConstLabels: labels,
		}, []string{"result", "more"}),
		listDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_conversation_list_duration_seconds",
			Help:        "Conversation list request latency in seconds.",
			ConstLabels: labels,
			Buckets:     gatewayFrameDurationBuckets,
		}, []string{"result", "more"}),
		listReturnedItems: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_conversation_list_returned_items",
			Help:        "Conversation rows returned by conversation list requests.",
			ConstLabels: labels,
			Buckets:     conversationListSizeBuckets,
		}, []string{"result", "more"}),
		listLastMessageHits: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_conversation_list_last_message_hits",
			Help:        "Returned conversation rows with a visible durable last message.",
			ConstLabels: labels,
			Buckets:     conversationListSizeBuckets,
		}, []string{"result", "more"}),
	}

	registry.MustRegister(
		m.listTotal,
		m.listDuration,
		m.listReturnedItems,
		m.listLastMessageHits,
	)

	return m
}

// ObserveList records one conversation list request result and page shape.
func (m *ConversationMetrics) ObserveList(result string, more bool, dur time.Duration, returnedItems, lastMessageHits int) {
	if m == nil {
		return
	}
	if result == "" {
		result = "unknown"
	}
	moreLabel := strconv.FormatBool(more)
	m.listTotal.WithLabelValues(result, moreLabel).Inc()
	m.listDuration.WithLabelValues(result, moreLabel).Observe(dur.Seconds())
	m.listReturnedItems.WithLabelValues(result, moreLabel).Observe(float64(nonNegative(returnedItems)))
	m.listLastMessageHits.WithLabelValues(result, moreLabel).Observe(float64(nonNegative(lastMessageHits)))
}

func nonNegative(v int) int {
	if v < 0 {
		return 0
	}
	return v
}
