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
	writeBatches      prometheus.Counter
	writeFrames       prometheus.Counter
	writePayloadBytes prometheus.Counter
	writeSingleFrames prometheus.Counter
	writeFrameLimit   prometheus.Counter
	poolActive        *prometheus.GaugeVec
	poolIdle          *prometheus.GaugeVec
	poolMu            sync.Mutex
	poolPeers         map[string]struct{}
	// Handle caches avoid repeated Vec label lookups on stable transport hot paths.
	rpcDurationHandles       sync.Map
	rpcTotalHandles          sync.Map
	rpcClientDurationHandles sync.Map
	rpcClientTotalHandles    sync.Map
	rpcInflightHandles       sync.Map
	enqueueTotalHandles      sync.Map
	dialDurationHandles      sync.Map
	dialTotalHandles         sync.Map
	sentBytesHandles         sync.Map
	receivedBytesHandles     sync.Map
	poolActiveHandles        sync.Map
	poolIdleHandles          sync.Map
}

type transportRPCResultKey struct {
	service string
	result  string
}

type transportRPCClientServiceKey struct {
	targetNode string
	service    string
}

type transportRPCClientResultKey struct {
	targetNode string
	service    string
	result     string
}

type transportEnqueueResultKey struct {
	targetNode string
	kind       string
	result     string
}

type transportDialResultKey struct {
	targetNode string
	result     string
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
		writeBatches: prometheus.NewCounter(prometheus.CounterOpts{
			Name:        "wukongim_transport_write_batches_total",
			Help:        "Total observed successful transport write batches.",
			ConstLabels: labels,
		}),
		writeFrames: prometheus.NewCounter(prometheus.CounterOpts{
			Name:        "wukongim_transport_write_frames_total",
			Help:        "Total transport frames included in observed successful write batches.",
			ConstLabels: labels,
		}),
		writePayloadBytes: prometheus.NewCounter(prometheus.CounterOpts{
			Name:        "wukongim_transport_write_payload_bytes_total",
			Help:        "Total payload bytes included in observed successful transport write batches.",
			ConstLabels: labels,
		}),
		writeSingleFrames: prometheus.NewCounter(prometheus.CounterOpts{
			Name:        "wukongim_transport_write_single_frame_batches_total",
			Help:        "Total observed successful transport write batches containing one frame.",
			ConstLabels: labels,
		}),
		writeFrameLimit: prometheus.NewCounter(prometheus.CounterOpts{
			Name:        "wukongim_transport_write_frame_limit_batches_total",
			Help:        "Total observed successful transport write batches that reached the configured frame limit.",
			ConstLabels: labels,
		}),
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
		m.writeBatches,
		m.writeFrames,
		m.writePayloadBytes,
		m.writeSingleFrames,
		m.writeFrameLimit,
		m.poolActive,
		m.poolIdle,
	)

	return m
}

func (m *TransportMetrics) ObserveRPC(service, result string, dur time.Duration) {
	if m == nil {
		return
	}
	m.rpcDurationHandle(service).Observe(dur.Seconds())
	m.rpcTotalHandle(service, result).Inc()
}

func (m *TransportMetrics) ObserveRPCClient(targetNode, service, result string, dur time.Duration) {
	if m == nil {
		return
	}
	m.rpcClientDurationHandle(targetNode, service).Observe(dur.Seconds())
	m.rpcClientTotalHandle(targetNode, service, result).Inc()
}

func (m *TransportMetrics) SetRPCInflight(targetNode, service string, count int) {
	if m == nil {
		return
	}
	m.rpcInflightHandle(targetNode, service).Set(float64(count))
}

func (m *TransportMetrics) ObserveEnqueue(targetNode, kind, result string) {
	if m == nil {
		return
	}
	m.enqueueTotalHandle(targetNode, kind, result).Inc()
}

func (m *TransportMetrics) ObserveDial(targetNode, result string, dur time.Duration) {
	if m == nil {
		return
	}
	m.dialDurationHandle(targetNode).Observe(dur.Seconds())
	m.dialTotalHandle(targetNode, result).Inc()
}

func (m *TransportMetrics) ObserveSentBytes(msgType string, bytes int) {
	if m == nil {
		return
	}
	m.sentBytesHandle(msgType).Add(float64(bytes))
}

func (m *TransportMetrics) ObserveReceivedBytes(msgType string, bytes int) {
	if m == nil {
		return
	}
	m.receivedBytesHandle(msgType).Add(float64(bytes))
}

// ObserveWriteBatch records the shape of one observed successful transport writev call.
func (m *TransportMetrics) ObserveWriteBatch(frames, payloadBytes, frameLimit int) {
	if m == nil || frames <= 0 {
		return
	}
	if payloadBytes < 0 {
		payloadBytes = 0
	}
	m.writeBatches.Inc()
	m.writeFrames.Add(float64(frames))
	m.writePayloadBytes.Add(float64(payloadBytes))
	if frames == 1 {
		m.writeSingleFrames.Inc()
	}
	if frameLimit > 0 && frames >= frameLimit {
		m.writeFrameLimit.Inc()
	}
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
		m.poolActiveHandle(peer).Set(float64(active))
	}
	for peer, idle := range idleByPeer {
		currentPeers[peer] = struct{}{}
		m.poolIdleHandle(peer).Set(float64(idle))
	}

	for peer := range currentPeers {
		if _, ok := activeByPeer[peer]; !ok {
			m.poolActiveHandle(peer).Set(0)
		}
		if _, ok := idleByPeer[peer]; !ok {
			m.poolIdleHandle(peer).Set(0)
		}
	}

	for peer := range m.poolPeers {
		if _, ok := currentPeers[peer]; ok {
			continue
		}
		m.poolActiveHandle(peer).Set(0)
		m.poolIdleHandle(peer).Set(0)
		delete(m.poolPeers, peer)
	}
	for peer := range currentPeers {
		m.poolPeers[peer] = struct{}{}
	}
}

func (m *TransportMetrics) rpcDurationHandle(service string) prometheus.Observer {
	if value, ok := m.rpcDurationHandles.Load(service); ok {
		return value.(prometheus.Observer)
	}
	handle := m.rpcDuration.WithLabelValues(service)
	value, _ := m.rpcDurationHandles.LoadOrStore(service, handle)
	return value.(prometheus.Observer)
}

func (m *TransportMetrics) rpcTotalHandle(service, result string) prometheus.Counter {
	key := transportRPCResultKey{service: service, result: result}
	if value, ok := m.rpcTotalHandles.Load(key); ok {
		return value.(prometheus.Counter)
	}
	handle := m.rpcTotal.WithLabelValues(service, result)
	value, _ := m.rpcTotalHandles.LoadOrStore(key, handle)
	return value.(prometheus.Counter)
}

func (m *TransportMetrics) rpcClientDurationHandle(targetNode, service string) prometheus.Observer {
	key := transportRPCClientServiceKey{targetNode: targetNode, service: service}
	if value, ok := m.rpcClientDurationHandles.Load(key); ok {
		return value.(prometheus.Observer)
	}
	handle := m.rpcClientDuration.WithLabelValues(targetNode, service)
	value, _ := m.rpcClientDurationHandles.LoadOrStore(key, handle)
	return value.(prometheus.Observer)
}

func (m *TransportMetrics) rpcClientTotalHandle(targetNode, service, result string) prometheus.Counter {
	key := transportRPCClientResultKey{targetNode: targetNode, service: service, result: result}
	if value, ok := m.rpcClientTotalHandles.Load(key); ok {
		return value.(prometheus.Counter)
	}
	handle := m.rpcClientTotal.WithLabelValues(targetNode, service, result)
	value, _ := m.rpcClientTotalHandles.LoadOrStore(key, handle)
	return value.(prometheus.Counter)
}

func (m *TransportMetrics) rpcInflightHandle(targetNode, service string) prometheus.Gauge {
	key := transportRPCClientServiceKey{targetNode: targetNode, service: service}
	if value, ok := m.rpcInflightHandles.Load(key); ok {
		return value.(prometheus.Gauge)
	}
	handle := m.rpcInflight.WithLabelValues(targetNode, service)
	value, _ := m.rpcInflightHandles.LoadOrStore(key, handle)
	return value.(prometheus.Gauge)
}

func (m *TransportMetrics) enqueueTotalHandle(targetNode, kind, result string) prometheus.Counter {
	key := transportEnqueueResultKey{targetNode: targetNode, kind: kind, result: result}
	if value, ok := m.enqueueTotalHandles.Load(key); ok {
		return value.(prometheus.Counter)
	}
	handle := m.enqueueTotal.WithLabelValues(targetNode, kind, result)
	value, _ := m.enqueueTotalHandles.LoadOrStore(key, handle)
	return value.(prometheus.Counter)
}

func (m *TransportMetrics) dialDurationHandle(targetNode string) prometheus.Observer {
	if value, ok := m.dialDurationHandles.Load(targetNode); ok {
		return value.(prometheus.Observer)
	}
	handle := m.dialDuration.WithLabelValues(targetNode)
	value, _ := m.dialDurationHandles.LoadOrStore(targetNode, handle)
	return value.(prometheus.Observer)
}

func (m *TransportMetrics) dialTotalHandle(targetNode, result string) prometheus.Counter {
	key := transportDialResultKey{targetNode: targetNode, result: result}
	if value, ok := m.dialTotalHandles.Load(key); ok {
		return value.(prometheus.Counter)
	}
	handle := m.dialTotal.WithLabelValues(targetNode, result)
	value, _ := m.dialTotalHandles.LoadOrStore(key, handle)
	return value.(prometheus.Counter)
}

func (m *TransportMetrics) sentBytesHandle(msgType string) prometheus.Counter {
	if value, ok := m.sentBytesHandles.Load(msgType); ok {
		return value.(prometheus.Counter)
	}
	handle := m.sentBytes.WithLabelValues(msgType)
	value, _ := m.sentBytesHandles.LoadOrStore(msgType, handle)
	return value.(prometheus.Counter)
}

func (m *TransportMetrics) receivedBytesHandle(msgType string) prometheus.Counter {
	if value, ok := m.receivedBytesHandles.Load(msgType); ok {
		return value.(prometheus.Counter)
	}
	handle := m.receivedBytes.WithLabelValues(msgType)
	value, _ := m.receivedBytesHandles.LoadOrStore(msgType, handle)
	return value.(prometheus.Counter)
}

func (m *TransportMetrics) poolActiveHandle(peer string) prometheus.Gauge {
	if value, ok := m.poolActiveHandles.Load(peer); ok {
		return value.(prometheus.Gauge)
	}
	handle := m.poolActive.WithLabelValues(peer)
	value, _ := m.poolActiveHandles.LoadOrStore(peer, handle)
	return value.(prometheus.Gauge)
}

func (m *TransportMetrics) poolIdleHandle(peer string) prometheus.Gauge {
	if value, ok := m.poolIdleHandles.Load(peer); ok {
		return value.(prometheus.Gauge)
	}
	handle := m.poolIdle.WithLabelValues(peer)
	value, _ := m.poolIdleHandles.LoadOrStore(peer, handle)
	return value.(prometheus.Gauge)
}
