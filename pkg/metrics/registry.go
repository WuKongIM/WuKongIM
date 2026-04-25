package metrics

import (
	"net/http"
	"strconv"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	dto "github.com/prometheus/client_model/go"
)

type Registry struct {
	registry *prometheus.Registry

	Gateway    *GatewayMetrics
	Channel    *ChannelMetrics
	Slot       *SlotMetrics
	Controller *ControllerMetrics
	Transport  *TransportMetrics
	Storage    *StorageMetrics
}

func New(nodeID uint64, nodeName string) *Registry {
	registry := prometheus.NewRegistry()
	registry.MustRegister(collectors.NewGoCollector())
	registry.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))

	labels := prometheus.Labels{
		"node_id":   strconv.FormatUint(nodeID, 10),
		"node_name": nodeName,
	}

	return &Registry{
		registry:   registry,
		Gateway:    newGatewayMetrics(registry, labels),
		Channel:    newChannelMetrics(registry, labels),
		Slot:       newSlotMetrics(registry, labels),
		Controller: newControllerMetrics(registry, labels),
		Transport:  newTransportMetrics(registry, labels),
		Storage:    newStorageMetrics(registry, labels),
	}
}

func (r *Registry) Gather() ([]*dto.MetricFamily, error) {
	if r == nil || r.registry == nil {
		return nil, nil
	}
	return r.registry.Gather()
}

func (r *Registry) PrometheusRegistry() *prometheus.Registry {
	if r == nil {
		return nil
	}
	return r.registry
}

func (r *Registry) Handler() http.Handler {
	if r == nil || r.registry == nil {
		return promhttp.Handler()
	}
	return promhttp.HandlerFor(r.registry, promhttp.HandlerOpts{})
}
