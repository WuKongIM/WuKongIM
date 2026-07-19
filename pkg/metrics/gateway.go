package metrics

import (
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

var gatewayFrameDurationBuckets = []float64{0.0005, 0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30}
var gatewayAsyncSendBatchRecordBuckets = []float64{1, 2, 4, 8, 16, 32, 64, 128, 256, 512, 1024}
var gatewayAsyncSendBatchByteBuckets = []float64{64, 256, 1024, 4096, 16384, 65536, 262144, 524288, 1048576, 4194304}

type GatewaySnapshot struct {
	ActiveConnections map[string]int64
}

type GatewayMetrics struct {
	connectionsActive        *prometheus.GaugeVec
	connectionsTotal         *prometheus.CounterVec
	connectionClosesTotal    *prometheus.CounterVec
	authTotal                *prometheus.CounterVec
	authDuration             *prometheus.HistogramVec
	messagesReceivedTotal    *prometheus.CounterVec
	messagesReceivedBytes    *prometheus.CounterVec
	messagesDeliveredTotal   *prometheus.CounterVec
	messagesDeliveredBytes   *prometheus.CounterVec
	sendacksTotal            *prometheus.CounterVec
	frameHandleDuration      *prometheus.HistogramVec
	asyncSendQueueDepth      prometheus.Gauge
	asyncSendQueueCapacity   prometheus.Gauge
	asyncSendDispatchWait    *prometheus.HistogramVec
	asyncSendBatchRecords    prometheus.Histogram
	asyncSendBatchBytes      prometheus.Histogram
	asyncSendBatchWait       prometheus.Histogram
	mu                       sync.Mutex
	activeConnectionsByProto map[string]int64
}

func newGatewayMetrics(registry prometheus.Registerer, labels prometheus.Labels) *GatewayMetrics {
	m := &GatewayMetrics{
		connectionsActive: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_gateway_connections_active",
			Help:        "Number of active gateway connections.",
			ConstLabels: labels,
		}, []string{"protocol"}),
		connectionsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_gateway_connections_total",
			Help:        "Total number of gateway connections by lifecycle event.",
			ConstLabels: labels,
		}, []string{"protocol", "event"}),
		connectionClosesTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_gateway_connection_closes_total",
			Help:        "Total number of gateway connection closes by reason.",
			ConstLabels: labels,
		}, []string{"protocol", "reason"}),
		authTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_gateway_auth_total",
			Help:        "Total number of gateway authentication attempts.",
			ConstLabels: labels,
		}, []string{"status", "failure"}),
		authDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_gateway_auth_duration_seconds",
			Help:        "Gateway authentication latency in seconds.",
			ConstLabels: labels,
			Buckets:     gatewayFrameDurationBuckets,
		}, []string{"status", "failure"}),
		messagesReceivedTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_gateway_messages_received_total",
			Help:        "Total number of client messages received by the gateway.",
			ConstLabels: labels,
		}, []string{"protocol"}),
		messagesReceivedBytes: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_gateway_messages_received_bytes_total",
			Help:        "Total payload bytes received from clients by the gateway.",
			ConstLabels: labels,
		}, []string{"protocol"}),
		messagesDeliveredTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_gateway_messages_delivered_total",
			Help:        "Total number of messages delivered to gateway clients.",
			ConstLabels: labels,
		}, []string{"protocol"}),
		messagesDeliveredBytes: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_gateway_messages_delivered_bytes_total",
			Help:        "Total payload bytes delivered to gateway clients.",
			ConstLabels: labels,
		}, []string{"protocol"}),
		sendacksTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_gateway_sendacks_total",
			Help:        "Total number of SEND acknowledgements written by result reason, source, and error class.",
			ConstLabels: labels,
		}, []string{"reason", "source", "class"}),
		frameHandleDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_gateway_frame_handle_duration_seconds",
			Help:        "Gateway frame handling latency in seconds.",
			ConstLabels: labels,
			Buckets:     gatewayFrameDurationBuckets,
		}, []string{"frame_type"}),
		asyncSendQueueDepth: prometheus.NewGauge(prometheus.GaugeOpts{
			Name:        "wukongim_gateway_async_send_queue_depth",
			Help:        "Number of SEND frames currently queued for asynchronous gateway dispatch.",
			ConstLabels: labels,
		}),
		asyncSendQueueCapacity: prometheus.NewGauge(prometheus.GaugeOpts{
			Name:        "wukongim_gateway_async_send_queue_capacity",
			Help:        "Total capacity of the asynchronous gateway SEND dispatch queue.",
			ConstLabels: labels,
		}),
		asyncSendDispatchWait: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_gateway_async_send_dispatch_wait_duration_seconds",
			Help:        "Time a SEND frame waits in the asynchronous gateway dispatch queue before handling.",
			ConstLabels: labels,
			Buckets:     gatewayFrameDurationBuckets,
		}, []string{"protocol"}),
		asyncSendBatchRecords: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:        "wukongim_gateway_async_send_batch_records",
			Help:        "Number of SEND frames collected into each asynchronous gateway dispatch batch.",
			ConstLabels: labels,
			Buckets:     gatewayAsyncSendBatchRecordBuckets,
		}),
		asyncSendBatchBytes: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:        "wukongim_gateway_async_send_batch_bytes",
			Help:        "Payload bytes collected into each asynchronous gateway dispatch batch.",
			ConstLabels: labels,
			Buckets:     gatewayAsyncSendBatchByteBuckets,
		}),
		asyncSendBatchWait: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:        "wukongim_gateway_async_send_batch_wait_duration_seconds",
			Help:        "Elapsed time from the first queued SEND frame to asynchronous gateway batch dispatch.",
			ConstLabels: labels,
			Buckets:     gatewayFrameDurationBuckets,
		}),
		activeConnectionsByProto: make(map[string]int64),
	}

	registry.MustRegister(
		m.connectionsActive,
		m.connectionsTotal,
		m.connectionClosesTotal,
		m.authTotal,
		m.authDuration,
		m.messagesReceivedTotal,
		m.messagesReceivedBytes,
		m.messagesDeliveredTotal,
		m.messagesDeliveredBytes,
		m.sendacksTotal,
		m.frameHandleDuration,
		m.asyncSendQueueDepth,
		m.asyncSendQueueCapacity,
		m.asyncSendDispatchWait,
		m.asyncSendBatchRecords,
		m.asyncSendBatchBytes,
		m.asyncSendBatchWait,
	)

	return m
}

func (m *GatewayMetrics) ConnectionOpened(protocol string) {
	if m == nil {
		return
	}
	m.connectionsActive.WithLabelValues(protocol).Inc()
	m.connectionsTotal.WithLabelValues(protocol, "open").Inc()
	m.mu.Lock()
	m.activeConnectionsByProto[protocol]++
	m.mu.Unlock()
}

func (m *GatewayMetrics) ConnectionClosed(protocol string) {
	if m == nil {
		return
	}
	m.connectionsActive.WithLabelValues(protocol).Dec()
	m.connectionsTotal.WithLabelValues(protocol, "close").Inc()
	m.mu.Lock()
	if m.activeConnectionsByProto[protocol] > 0 {
		m.activeConnectionsByProto[protocol]--
	}
	m.mu.Unlock()
}

func (m *GatewayMetrics) ConnectionClosedReason(protocol, reason string) {
	if m == nil {
		return
	}
	if reason == "" {
		reason = "unknown"
	}
	m.connectionClosesTotal.WithLabelValues(protocol, reason).Inc()
}

func (m *GatewayMetrics) Auth(status, failure string, dur time.Duration) {
	if m == nil {
		return
	}
	status = normalizeGatewayAuthStatus(status)
	failure = normalizeGatewayAuthFailure(status, failure)
	m.authTotal.WithLabelValues(status, failure).Inc()
	m.authDuration.WithLabelValues(status, failure).Observe(dur.Seconds())
}

func normalizeGatewayAuthStatus(status string) string {
	switch status {
	case "ok", "fail":
		return status
	default:
		return "unknown"
	}
}

func normalizeGatewayAuthFailure(status, failure string) string {
	if status == "ok" {
		return "none"
	}
	switch failure {
	case "protocol_violation",
		"authenticator_error",
		"activation_error",
		"activation_route_not_ready",
		"activation_not_leader",
		"activation_stale_route",
		"activation_context_canceled",
		"activation_context_deadline",
		"connack_auth_fail",
		"connack_ban",
		"connack_client_key_empty",
		"connack_rate_limit",
		"connack_system_error",
		"connack_protocol_upgrade_required",
		"connack_non_success",
		"connack_write_error":
		return failure
	default:
		return "unknown"
	}
}

func (m *GatewayMetrics) MessageReceived(protocol string, bytes int) {
	if m == nil {
		return
	}
	m.messagesReceivedTotal.WithLabelValues(protocol).Inc()
	m.messagesReceivedBytes.WithLabelValues(protocol).Add(float64(bytes))
}

func (m *GatewayMetrics) MessageDelivered(protocol string, bytes int) {
	if m == nil {
		return
	}
	m.messagesDeliveredTotal.WithLabelValues(protocol).Inc()
	m.messagesDeliveredBytes.WithLabelValues(protocol).Add(float64(bytes))
}

// Sendack records one SEND acknowledgement by low-cardinality reason, source, and error class.
func (m *GatewayMetrics) Sendack(reason, source string, class string) {
	if m == nil {
		return
	}
	m.sendacksTotal.WithLabelValues(normalizeGatewaySendackReason(reason), normalizeGatewaySendackSource(source), normalizeGatewaySendackClass(class)).Inc()
}

func normalizeGatewaySendackReason(reason string) string {
	switch reason {
	case "success",
		"invalid_request",
		"auth_fail",
		"channel_not_exist",
		"node_not_match",
		"system_error",
		"unsupported":
		return reason
	default:
		return "unknown"
	}
}

func normalizeGatewaySendackSource(source string) string {
	switch source {
	case "single_result",
		"single_error",
		"single_missing_request_context",
		"batch_precheck",
		"batch_missing_request_context",
		"batch_result",
		"batch_result_error",
		"batch_fallback_result",
		"batch_fallback_error":
		return source
	default:
		return "unknown"
	}
}

func normalizeGatewaySendackClass(class string) string {
	switch class {
	case "none",
		"unauthenticated",
		"missing_request_context",
		"channel_not_found",
		"not_leader",
		"stale_route",
		"route_not_ready",
		"invalid_request",
		"canceled",
		"timeout",
		"other":
		return class
	default:
		return "unknown"
	}
}

func (m *GatewayMetrics) FrameHandled(frameType string, dur time.Duration) {
	if m == nil {
		return
	}
	m.frameHandleDuration.WithLabelValues(frameType).Observe(dur.Seconds())
}

func (m *GatewayMetrics) SetAsyncSendQueue(depth, capacity int) {
	if m == nil {
		return
	}
	m.asyncSendQueueDepth.Set(float64(depth))
	m.asyncSendQueueCapacity.Set(float64(capacity))
}

func (m *GatewayMetrics) ObserveAsyncSendDispatchWait(protocol string, dur time.Duration) {
	if m == nil {
		return
	}
	m.asyncSendDispatchWait.WithLabelValues(protocol).Observe(dur.Seconds())
}

func (m *GatewayMetrics) ObserveAsyncSendBatch(records, bytes int, wait time.Duration) {
	if m == nil {
		return
	}
	m.asyncSendBatchRecords.Observe(float64(records))
	m.asyncSendBatchBytes.Observe(float64(bytes))
	m.asyncSendBatchWait.Observe(wait.Seconds())
}

func (m *GatewayMetrics) Snapshot() GatewaySnapshot {
	if m == nil {
		return GatewaySnapshot{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	out := GatewaySnapshot{ActiveConnections: make(map[string]int64, len(m.activeConnectionsByProto))}
	for protocol, count := range m.activeConnectionsByProto {
		out.ActiveConnections[protocol] = count
	}
	return out
}
