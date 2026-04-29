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

// DeliveryMetrics exposes route resolution, push RPC, actor, and expiry metrics.
type DeliveryMetrics struct {
	resolveDuration    *prometheus.HistogramVec
	resolvePagesTotal  *prometheus.CounterVec
	resolveRoutesTotal *prometheus.CounterVec
	pushRPCTotal       *prometheus.CounterVec
	pushRPCDuration    *prometheus.HistogramVec
	pushRPCRoutesTotal *prometheus.CounterVec
	actorInflight      prometheus.Gauge
	ackBindings        prometheus.Gauge
	routeExpiredTotal  *prometheus.CounterVec
	mu                 sync.Mutex
	actorInflightV     int64
	ackBindingsV       int64
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
	}

	registry.MustRegister(
		m.resolveDuration,
		m.resolvePagesTotal,
		m.resolveRoutesTotal,
		m.pushRPCTotal,
		m.pushRPCDuration,
		m.pushRPCRoutesTotal,
		m.actorInflight,
		m.ackBindings,
		m.routeExpiredTotal,
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
