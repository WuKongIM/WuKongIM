package metrics

import (
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/golang/protobuf/proto"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	dto "github.com/prometheus/client_model/go"
)

type Registry struct {
	registry *prometheus.Registry

	Gateway         *GatewayMetrics
	Channel         *ChannelMetrics
	ChannelAppend   *ChannelAppendMetrics
	ChannelRuntime  *ChannelRuntimeMetrics
	Slot            *SlotMetrics
	Controller      *ControllerMetrics
	Transport       *TransportMetrics
	Storage         *StorageMetrics
	Message         *MessageMetrics
	Conversation    *ConversationMetrics
	Delivery        *DeliveryMetrics
	Presence        *PresenceMetrics
	Plugin          *PluginMetrics
	Diagnostics     *DiagnosticsMetrics
	RuntimePressure *RuntimePressureMetrics
	AntsPool        *AntsPoolMetrics
	NodeResource    *NodeResourceMetrics
	NodeLifecycle   *NodeLifecycleMetrics
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
		registry:        registry,
		Gateway:         newGatewayMetrics(registry, labels),
		Channel:         newChannelMetrics(registry, labels),
		ChannelAppend:   newChannelAppendMetrics(registry, labels),
		ChannelRuntime:  newChannelRuntimeMetrics(registry, labels),
		Slot:            newSlotMetrics(registry, labels),
		Controller:      newControllerMetrics(registry, labels),
		Transport:       newTransportMetrics(registry, labels),
		Storage:         newStorageMetrics(registry, labels),
		Message:         newMessageMetrics(registry, labels),
		Conversation:    newConversationMetrics(registry, labels),
		Delivery:        newDeliveryMetrics(registry, labels),
		Presence:        newPresenceMetrics(registry, labels),
		Plugin:          newPluginMetrics(registry, labels),
		Diagnostics:     newDiagnosticsMetrics(registry, labels),
		RuntimePressure: newRuntimePressureMetrics(registry, labels),
		AntsPool:        newAntsPoolMetrics(registry, labels),
		NodeResource:    newNodeResourceMetrics(registry, labels),
		NodeLifecycle:   newNodeLifecycleMetrics(registry, labels),
	}
}

func (r *Registry) Gather() ([]*dto.MetricFamily, error) {
	if r == nil || r.registry == nil {
		return nil, nil
	}
	families, err := r.registry.Gather()
	return appendChannelRuntimePromotedFamilies(families), err
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
	return promhttp.HandlerFor(channelRuntimeAliasGatherer{base: r.registry}, promhttp.HandlerOpts{})
}

func (r *Registry) ChannelExecutionMetrics() *ChannelMetrics {
	if r == nil {
		return nil
	}
	return r.Channel
}

type channelRuntimeAliasGatherer struct {
	base prometheus.Gatherer
}

// Gather exposes promoted Channel runtime metric names at scrape time while reusing the legacy collectors.
func (g channelRuntimeAliasGatherer) Gather() ([]*dto.MetricFamily, error) {
	if g.base == nil {
		return nil, nil
	}
	families, err := g.base.Gather()
	return appendChannelRuntimePromotedFamilies(families), err
}

// appendChannelRuntimePromotedFamilies clones legacy Channel runtime families with promoted names.
func appendChannelRuntimePromotedFamilies(families []*dto.MetricFamily) []*dto.MetricFamily {
	if len(families) == 0 {
		return families
	}
	names := make(map[string]struct{}, len(families))
	for _, family := range families {
		if family == nil {
			continue
		}
		names[family.GetName()] = struct{}{}
	}
	out := make([]*dto.MetricFamily, 0, len(families)*2)
	out = append(out, families...)
	for _, family := range families {
		if family == nil {
			continue
		}
		name := family.GetName()
		if !strings.HasPrefix(name, "wukongim_channelv2_") {
			continue
		}
		promotedName := "wukongim_channel_" + strings.TrimPrefix(name, "wukongim_channelv2_")
		if _, exists := names[promotedName]; exists {
			continue
		}
		alias, ok := proto.Clone(family).(*dto.MetricFamily)
		if !ok || alias == nil {
			continue
		}
		alias.Name = proto.String(promotedName)
		out = append(out, alias)
		names[promotedName] = struct{}{}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].GetName() < out[j].GetName()
	})
	return out
}
