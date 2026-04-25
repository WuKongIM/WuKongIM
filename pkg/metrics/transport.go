package metrics

import (
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

type TransportMetrics struct {
	rpcDuration       *prometheus.HistogramVec
	rpcTotal          *prometheus.CounterVec
	rpcClientDuration *prometheus.HistogramVec
	rpcClientTotal    *prometheus.CounterVec
	rpcInflight       *prometheus.GaugeVec
	enqueueTotal      *prometheus.CounterVec
	dialDuration      *prometheus.HistogramVec
	dialTotal         *prometheus.CounterVec
	sentBytes         *prometheus.CounterVec
	receivedBytes     *prometheus.CounterVec
	poolActive        *prometheus.GaugeVec
	poolIdle          *prometheus.GaugeVec
	poolMu            sync.Mutex
	poolPeers         map[string]struct{}
}

func newTransportMetrics(registry prometheus.Registerer, labels prometheus.Labels) *TransportMetrics {
	m := &TransportMetrics{
		rpcDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_transport_rpc_duration_seconds",
			Help:        "Transport RPC latency in seconds.",
			ConstLabels: labels,
			Buckets:     gatewayFrameDurationBuckets,
		}, []string{"service"}),
		rpcTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_transport_rpc_total",
			Help:        "Total number of transport RPC calls.",
			ConstLabels: labels,
		}, []string{"service", "result"}),
		rpcClientDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_transport_rpc_client_duration_seconds",
			Help:        "Transport RPC client latency in seconds grouped by target node and service.",
			ConstLabels: labels,
			Buckets:     gatewayFrameDurationBuckets,
		}, []string{"target_node", "service"}),
		rpcClientTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_transport_rpc_client_total",
			Help:        "Total number of transport RPC client calls grouped by target node and service.",
			ConstLabels: labels,
		}, []string{"target_node", "service", "result"}),
		rpcInflight: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_transport_rpc_inflight",
			Help:        "Current number of in-flight transport RPC client calls grouped by target node and service.",
			ConstLabels: labels,
		}, []string{"target_node", "service"}),
		enqueueTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_transport_enqueue_total",
			Help:        "Total number of transport enqueue attempts grouped by target node and queue kind.",
			ConstLabels: labels,
		}, []string{"target_node", "kind", "result"}),
		dialDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "wukongim_transport_dial_duration_seconds",
			Help:        "Transport dial latency in seconds grouped by target node.",
			ConstLabels: labels,
			Buckets:     gatewayFrameDurationBuckets,
		}, []string{"target_node"}),
		dialTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_transport_dial_total",
			Help:        "Total number of transport dial attempts grouped by target node.",
			ConstLabels: labels,
		}, []string{"target_node", "result"}),
		sentBytes: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_transport_sent_bytes_total",
			Help:        "Total outbound transport payload bytes.",
			ConstLabels: labels,
		}, []string{"msg_type"}),
		receivedBytes: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "wukongim_transport_received_bytes_total",
			Help:        "Total inbound transport payload bytes.",
			ConstLabels: labels,
		}, []string{"msg_type"}),
		poolActive: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_transport_connections_pool_active",
			Help:        "Number of active transport pool connections grouped by peer node.",
			ConstLabels: labels,
		}, []string{"peer_node"}),
		poolIdle: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "wukongim_transport_connections_pool_idle",
			Help:        "Number of idle transport pool connections grouped by peer node.",
			ConstLabels: labels,
		}, []string{"peer_node"}),
		poolPeers: make(map[string]struct{}),
	}

	registry.MustRegister(
		m.rpcDuration,
		m.rpcTotal,
		m.rpcClientDuration,
		m.rpcClientTotal,
		m.rpcInflight,
		m.enqueueTotal,
		m.dialDuration,
		m.dialTotal,
		m.sentBytes,
		m.receivedBytes,
		m.poolActive,
		m.poolIdle,
	)

	return m
}

func (m *TransportMetrics) ObserveRPC(service, result string, dur time.Duration) {
	if m == nil {
		return
	}
	m.rpcDuration.WithLabelValues(service).Observe(dur.Seconds())
	m.rpcTotal.WithLabelValues(service, result).Inc()
}

func (m *TransportMetrics) ObserveRPCClient(targetNode, service, result string, dur time.Duration) {
	if m == nil {
		return
	}
	m.rpcClientDuration.WithLabelValues(targetNode, service).Observe(dur.Seconds())
	m.rpcClientTotal.WithLabelValues(targetNode, service, result).Inc()
}

func (m *TransportMetrics) SetRPCInflight(targetNode, service string, count int) {
	if m == nil {
		return
	}
	m.rpcInflight.WithLabelValues(targetNode, service).Set(float64(count))
}

func (m *TransportMetrics) ObserveEnqueue(targetNode, kind, result string) {
	if m == nil {
		return
	}
	m.enqueueTotal.WithLabelValues(targetNode, kind, result).Inc()
}

func (m *TransportMetrics) ObserveDial(targetNode, result string, dur time.Duration) {
	if m == nil {
		return
	}
	m.dialDuration.WithLabelValues(targetNode).Observe(dur.Seconds())
	m.dialTotal.WithLabelValues(targetNode, result).Inc()
}

func (m *TransportMetrics) ObserveSentBytes(msgType string, bytes int) {
	if m == nil {
		return
	}
	m.sentBytes.WithLabelValues(msgType).Add(float64(bytes))
}

func (m *TransportMetrics) ObserveReceivedBytes(msgType string, bytes int) {
	if m == nil {
		return
	}
	m.receivedBytes.WithLabelValues(msgType).Add(float64(bytes))
}

func (m *TransportMetrics) SetPoolConnections(activeByPeer, idleByPeer map[string]int) {
	if m == nil {
		return
	}

	m.poolMu.Lock()
	defer m.poolMu.Unlock()

	currentPeers := make(map[string]struct{}, len(activeByPeer)+len(idleByPeer))
	for peer, active := range activeByPeer {
		currentPeers[peer] = struct{}{}
		m.poolActive.WithLabelValues(peer).Set(float64(active))
	}
	for peer, idle := range idleByPeer {
		currentPeers[peer] = struct{}{}
		m.poolIdle.WithLabelValues(peer).Set(float64(idle))
	}

	for peer := range currentPeers {
		if _, ok := activeByPeer[peer]; !ok {
			m.poolActive.WithLabelValues(peer).Set(0)
		}
		if _, ok := idleByPeer[peer]; !ok {
			m.poolIdle.WithLabelValues(peer).Set(0)
		}
	}

	for peer := range m.poolPeers {
		if _, ok := currentPeers[peer]; ok {
			continue
		}
		m.poolActive.WithLabelValues(peer).Set(0)
		m.poolIdle.WithLabelValues(peer).Set(0)
		delete(m.poolPeers, peer)
	}
	for peer := range currentPeers {
		m.poolPeers[peer] = struct{}{}
	}
}
