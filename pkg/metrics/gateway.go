package metrics

import (
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

var gatewayFrameDurationBuckets = []float64{0.0005, 0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5}

type GatewaySnapshot struct {
	ActiveConnections map[string]int64
}

type GatewayMetrics struct {
	connectionsActive        *prometheus.GaugeVec
	connectionsTotal         *prometheus.CounterVec
	authTotal                *prometheus.CounterVec
	authDuration             prometheus.Histogram
	messagesReceivedTotal    *prometheus.CounterVec
	messagesReceivedBytes    *prometheus.CounterVec
	messagesDeliveredTotal   *prometheus.CounterVec
	messagesDeliveredBytes   *prometheus.CounterVec
	frameHandleDuration      *prometheus.HistogramVec
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
		authTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_gateway_auth_total",
			Help:        "Total number of gateway authentication attempts.",
			ConstLabels: labels,
		}, []string{"status"}),
		authDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:        "wukongim_gateway_auth_duration_seconds",
			Help:        "Gateway authentication latency in seconds.",
			ConstLabels: labels,
			Buckets:     gatewayFrameDurationBuckets,
		}),
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
		frameHandleDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_gateway_frame_handle_duration_seconds",
			Help:        "Gateway frame handling latency in seconds.",
			ConstLabels: labels,
			Buckets:     gatewayFrameDurationBuckets,
		}, []string{"frame_type"}),
		activeConnectionsByProto: make(map[string]int64),
	}

	registry.MustRegister(
		m.connectionsActive,
		m.connectionsTotal,
		m.authTotal,
		m.authDuration,
		m.messagesReceivedTotal,
		m.messagesReceivedBytes,
		m.messagesDeliveredTotal,
		m.messagesDeliveredBytes,
		m.frameHandleDuration,
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

func (m *GatewayMetrics) Auth(status string, dur time.Duration) {
	if m == nil {
		return
	}
	m.authTotal.WithLabelValues(status).Inc()
	m.authDuration.Observe(dur.Seconds())
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

func (m *GatewayMetrics) FrameHandled(frameType string, dur time.Duration) {
	if m == nil {
		return
	}
	m.frameHandleDuration.WithLabelValues(frameType).Observe(dur.Seconds())
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
