package metrics

import (
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// DeliverySnapshot captures the latest gauge values owned by DeliveryMetrics.
type DeliverySnapshot struct {
	// ActorInflightRoutes is the latest number of routes currently held by delivery actors.
	ActorInflightRoutes int64
	// AckBindings is the latest number of delivery acknowledgement bindings.
	AckBindings int64
}

// DeliveryMetrics exposes delivery queue, fanout, route resolution, push RPC, actor, and expiry metrics.
type DeliveryMetrics struct {
	resolveDuration            *prometheus.HistogramVec
	resolvePagesTotal          *prometheus.CounterVec
	resolveRoutesTotal         *prometheus.CounterVec
	pushRPCTotal               *prometheus.CounterVec
	pushRPCDuration            *prometheus.HistogramVec
	pushRPCRoutesTotal         *prometheus.CounterVec
	eventQueueTotal            *prometheus.CounterVec
	errorsTotal                *prometheus.CounterVec
	fanoutTaskTotal            *prometheus.CounterVec
	fanoutTaskDuration         *prometheus.HistogramVec
	retryTotal                 *prometheus.CounterVec
	retryQueueDepth            prometheus.Gauge
	actorInflight              prometheus.Gauge
	ackBindings                prometheus.Gauge
	routeExpiredTotal          *prometheus.CounterVec
	recipientQueueDepth        prometheus.Gauge
	recipientQueueCapacity     prometheus.Gauge
	recipientAdmissionTotal    *prometheus.CounterVec
	recipientAdmissionWait     *prometheus.HistogramVec
	recipientProcessTotal      *prometheus.CounterVec
	recipientProcessDuration   *prometheus.HistogramVec
	recipientProcessRecipients *prometheus.HistogramVec
	mu                         sync.Mutex
	actorInflightV             int64
	ackBindingsV               int64
}

func newDeliveryMetrics(registry prometheus.Registerer, labels prometheus.Labels) *DeliveryMetrics {
	m := &DeliveryMetrics{
		resolveDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_delivery_resolve_duration_seconds",
			Help:        "Delivery route resolution latency in seconds.",
			ConstLabels: labels,
			Buckets:     gatewayFrameDurationBuckets,
		}, []string{"channel_type", "result"}),
		resolvePagesTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_delivery_resolve_pages_total",
			Help:        "Total number of pages read during delivery route resolution.",
			ConstLabels: labels,
		}, []string{"channel_type", "result"}),
		resolveRoutesTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_delivery_resolve_routes_total",
			Help:        "Total number of routes resolved for delivery.",
			ConstLabels: labels,
		}, []string{"channel_type", "result"}),
		pushRPCTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_delivery_push_rpc_total",
			Help:        "Total number of delivery push RPC attempts.",
			ConstLabels: labels,
		}, []string{"target_node", "result"}),
		pushRPCDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_delivery_push_rpc_duration_seconds",
			Help:        "Delivery push RPC latency in seconds.",
			ConstLabels: labels,
			Buckets:     gatewayFrameDurationBuckets,
		}, []string{"target_node", "result"}),
		pushRPCRoutesTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_delivery_push_rpc_routes_total",
			Help:        "Total number of routes sent by delivery push RPC.",
			ConstLabels: labels,
		}, []string{"target_node", "result"}),
		eventQueueTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_delivery_event_queue_total",
			Help:        "Total number of delivery committed-event queue submit attempts.",
			ConstLabels: labels,
		}, []string{"result"}),
		errorsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_delivery_errors_total",
			Help:        "Total number of normalized delivery errors.",
			ConstLabels: labels,
		}, []string{"class"}),
		fanoutTaskTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_delivery_fanout_task_total",
			Help:        "Total number of delivery fanout task routing attempts.",
			ConstLabels: labels,
		}, []string{"target_node", "result"}),
		fanoutTaskDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_delivery_fanout_task_duration_seconds",
			Help:        "Delivery fanout task routing latency in seconds.",
			ConstLabels: labels,
			Buckets:     gatewayFrameDurationBuckets,
		}, []string{"target_node", "result"}),
		retryTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_delivery_retry_total",
			Help:        "Total number of delivery retry scheduler events.",
			ConstLabels: labels,
		}, []string{"event", "result"}),
		retryQueueDepth: prometheus.NewGauge(prometheus.GaugeOpts{
			Name:        "wukongim_delivery_retry_queue_depth",
			Help:        "Current number of fanout tasks waiting in the delivery retry queue.",
			ConstLabels: labels,
		}),
		actorInflight: prometheus.NewGauge(prometheus.GaugeOpts{
			Name:        "wukongim_delivery_actor_inflight_routes",
			Help:        "Current number of in-flight routes held by delivery actors.",
			ConstLabels: labels,
		}),
		ackBindings: prometheus.NewGauge(prometheus.GaugeOpts{
			Name:        "wukongim_delivery_ack_bindings",
			Help:        "Current number of delivery acknowledgement bindings.",
			ConstLabels: labels,
		}),
		routeExpiredTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_delivery_route_expired_total",
			Help:        "Total number of delivery routes expired before completion.",
			ConstLabels: labels,
		}, []string{"channel_type"}),
		recipientQueueDepth: prometheus.NewGauge(prometheus.GaugeOpts{
			Name:        "wukongim_delivery_recipient_worker_queue_depth",
			Help:        "Current queued recipient-authority delivery batches.",
			ConstLabels: labels,
		}),
		recipientQueueCapacity: prometheus.NewGauge(prometheus.GaugeOpts{
			Name:        "wukongim_delivery_recipient_worker_queue_capacity",
			Help:        "Configured recipient-authority delivery worker queue capacity.",
			ConstLabels: labels,
		}),
		recipientAdmissionTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_delivery_recipient_worker_admission_total",
			Help:        "Total recipient delivery worker enqueue attempts by normalized result.",
			ConstLabels: labels,
		}, []string{"result"}),
		recipientAdmissionWait: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_delivery_recipient_worker_admission_wait_seconds",
			Help:        "Recipient delivery worker enqueue wait time in seconds.",
			ConstLabels: labels,
			Buckets:     gatewayFrameDurationBuckets,
		}, []string{"result"}),
		recipientProcessTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_delivery_recipient_worker_process_total",
			Help:        "Total recipient delivery worker processing attempts by normalized result.",
			ConstLabels: labels,
		}, []string{"result"}),
		recipientProcessDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_delivery_recipient_worker_process_duration_seconds",
			Help:        "Recipient delivery worker processing latency in seconds.",
			ConstLabels: labels,
			Buckets:     gatewayFrameDurationBuckets,
		}, []string{"result"}),
		recipientProcessRecipients: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_delivery_recipient_worker_process_recipients",
			Help:        "Recipients processed by each recipient delivery worker batch.",
			ConstLabels: labels,
			Buckets:     conversationListSizeBuckets,
		}, []string{"result"}),
	}

	registry.MustRegister(
		m.resolveDuration,
		m.resolvePagesTotal,
		m.resolveRoutesTotal,
		m.pushRPCTotal,
		m.pushRPCDuration,
		m.pushRPCRoutesTotal,
		m.eventQueueTotal,
		m.errorsTotal,
		m.fanoutTaskTotal,
		m.fanoutTaskDuration,
		m.retryTotal,
		m.retryQueueDepth,
		m.actorInflight,
		m.ackBindings,
		m.routeExpiredTotal,
		m.recipientQueueDepth,
		m.recipientQueueCapacity,
		m.recipientAdmissionTotal,
		m.recipientAdmissionWait,
		m.recipientProcessTotal,
		m.recipientProcessDuration,
		m.recipientProcessRecipients,
	)

	return m
}

// ObserveResolve records delivery route resolution latency and resolved work sizes.
func (m *DeliveryMetrics) ObserveResolve(channelType, result string, dur time.Duration, pages, routes int) {
	if m == nil {
		return
	}
	m.resolveDuration.WithLabelValues(channelType, result).Observe(dur.Seconds())
	m.resolvePagesTotal.WithLabelValues(channelType, result).Add(float64(pages))
	m.resolveRoutesTotal.WithLabelValues(channelType, result).Add(float64(routes))
}

// ObservePushRPC records a delivery push RPC attempt and the number of routes sent.
func (m *DeliveryMetrics) ObservePushRPC(targetNode, result string, dur time.Duration, routes int) {
	if m == nil {
		return
	}
	m.pushRPCTotal.WithLabelValues(targetNode, result).Inc()
	m.pushRPCDuration.WithLabelValues(targetNode, result).Observe(dur.Seconds())
	m.pushRPCRoutesTotal.WithLabelValues(targetNode, result).Add(float64(routes))
}

// ObserveEventQueue records a committed-event queue submit result.
func (m *DeliveryMetrics) ObserveEventQueue(result string) {
	if m == nil {
		return
	}
	m.eventQueueTotal.WithLabelValues(normalizeDeliveryLabel(result, "unknown")).Inc()
}

// ObserveError records a normalized delivery error class.
func (m *DeliveryMetrics) ObserveError(class string) {
	if m == nil {
		return
	}
	m.errorsTotal.WithLabelValues(normalizeDeliveryLabel(class, "unknown")).Inc()
}

// ObserveFanoutTask records one fanout task routing attempt.
func (m *DeliveryMetrics) ObserveFanoutTask(targetNode, result string, dur time.Duration) {
	if m == nil {
		return
	}
	targetNode = normalizeDeliveryLabel(targetNode, "0")
	result = normalizeDeliveryLabel(result, "unknown")
	m.fanoutTaskTotal.WithLabelValues(targetNode, result).Inc()
	m.fanoutTaskDuration.WithLabelValues(targetNode, result).Observe(dur.Seconds())
}

// ObserveRetry records one delivery retry scheduler event.
func (m *DeliveryMetrics) ObserveRetry(event, result string) {
	if m == nil {
		return
	}
	event = normalizeDeliveryLabel(event, "unknown")
	result = normalizeDeliveryLabel(result, "unknown")
	m.retryTotal.WithLabelValues(event, result).Inc()
}

// SetRetryQueueDepth sets the current delivery retry queue depth.
func (m *DeliveryMetrics) SetRetryQueueDepth(depth int) {
	if m == nil {
		return
	}
	m.retryQueueDepth.Set(float64(depth))
}

// SetActorInflightRoutes sets the current number of in-flight delivery actor routes.
func (m *DeliveryMetrics) SetActorInflightRoutes(v int) {
	if m == nil {
		return
	}
	m.actorInflight.Set(float64(v))
	m.mu.Lock()
	m.actorInflightV = int64(v)
	m.mu.Unlock()
}

// SetAckBindings sets the current number of delivery acknowledgement bindings.
func (m *DeliveryMetrics) SetAckBindings(v int) {
	if m == nil {
		return
	}
	m.ackBindings.Set(float64(v))
	m.mu.Lock()
	m.ackBindingsV = int64(v)
	m.mu.Unlock()
}

// ObserveRouteExpired records an expired delivery route.
func (m *DeliveryMetrics) ObserveRouteExpired(channelType string) {
	if m == nil {
		return
	}
	m.routeExpiredTotal.WithLabelValues(channelType).Inc()
}

// SetRecipientWorkerQueue sets the current recipient delivery worker queue pressure.
func (m *DeliveryMetrics) SetRecipientWorkerQueue(depth, capacity int) {
	if m == nil {
		return
	}
	m.recipientQueueDepth.Set(float64(nonNegative(depth)))
	m.recipientQueueCapacity.Set(float64(nonNegative(capacity)))
}

// ObserveRecipientWorkerAdmission records one recipient delivery worker enqueue attempt.
func (m *DeliveryMetrics) ObserveRecipientWorkerAdmission(result string, dur time.Duration) {
	if m == nil {
		return
	}
	result = normalizeDeliveryLabel(result, "unknown")
	if dur < 0 {
		dur = 0
	}
	m.recipientAdmissionTotal.WithLabelValues(result).Inc()
	m.recipientAdmissionWait.WithLabelValues(result).Observe(dur.Seconds())
}

// ObserveRecipientWorkerProcess records one recipient delivery worker processing attempt.
func (m *DeliveryMetrics) ObserveRecipientWorkerProcess(result string, recipients int, dur time.Duration) {
	if m == nil {
		return
	}
	result = normalizeDeliveryLabel(result, "unknown")
	if dur < 0 {
		dur = 0
	}
	m.recipientProcessTotal.WithLabelValues(result).Inc()
	m.recipientProcessDuration.WithLabelValues(result).Observe(dur.Seconds())
	m.recipientProcessRecipients.WithLabelValues(result).Observe(float64(nonNegative(recipients)))
}

// Snapshot returns the latest delivery gauge values.
func (m *DeliveryMetrics) Snapshot() DeliverySnapshot {
	if m == nil {
		return DeliverySnapshot{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return DeliverySnapshot{
		ActorInflightRoutes: m.actorInflightV,
		AckBindings:         m.ackBindingsV,
	}
}

func normalizeDeliveryLabel(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
