package app

import (
	"fmt"
	"sort"
	"sync"
	"time"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	raftcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/WuKongIM/WuKongIM/pkg/transport"
)

const (
	defaultNetworkObservabilityWindow    = time.Minute
	defaultNetworkObservabilityMaxEvents = 50
	networkSampleDirectionTX             = "tx"
	networkSampleDirectionRX             = "rx"
)

// networkObservabilityConfig configures local app-level network observations.
type networkObservabilityConfig struct {
	// LocalNodeID identifies the node that owns the collector.
	LocalNodeID uint64
	// LocalNodeName is the human-readable local node name.
	LocalNodeName string
	// Window bounds the recent sample interval used by manager counters.
	Window time.Duration
	// MaxEvents limits recent warning events returned in snapshots.
	MaxEvents int
	// ListenAddr is the local cluster transport listen address.
	ListenAddr string
	// AdvertiseAddr is the cluster transport address advertised to peers.
	AdvertiseAddr string
	// Seeds contains dynamic join seed addresses.
	Seeds []string
	// StaticNodes contains configured static cluster peers.
	StaticNodes []NodeConfigRef
	// PoolSize is the cluster transport pool size.
	PoolSize int
	// DataPlanePoolSize is the channel data-plane transport pool size.
	DataPlanePoolSize int
	// DialTimeout is the configured peer dial timeout.
	DialTimeout time.Duration
	// ControllerObservationInterval is the controller runtime observation interval.
	ControllerObservationInterval time.Duration
	// DataPlaneRPCTimeout is the configured channel data-plane RPC timeout.
	DataPlaneRPCTimeout time.Duration
	// LongPollLaneCount is the number of channel fetch long-poll lanes.
	LongPollLaneCount int
	// LongPollMaxWait is the maximum wait for one channel fetch long-poll request.
	LongPollMaxWait time.Duration
	// LongPollMaxBytes is the maximum response bytes for one long-poll fetch.
	LongPollMaxBytes int
	// LongPollMaxChannels is the maximum channels served by one long-poll fetch.
	LongPollMaxChannels int
	// DataPlanePoolStats reads current channel data-plane pool counters.
	DataPlanePoolStats func() []transport.PoolPeerStats
	// Now returns the observation timestamp used when hooks record samples.
	Now func() time.Time
}

// networkObservability records local transport hook samples for manager snapshots.
type networkObservability struct {
	mu sync.Mutex

	cfg      networkObservabilityConfig
	traffic  []networkTrafficSample
	dials    []networkDialSample
	enqueues []networkEnqueueSample
	rpcs     []networkRPCSample
	events   []managementusecase.NetworkEvent
	inflight map[networkRPCKey]int
}

type networkTrafficSample struct {
	at        time.Time
	direction string
	msgType   uint8
	bytes     int
}

type networkDialSample struct {
	at         time.Time
	targetNode uint64
	result     string
	duration   time.Duration
}

type networkEnqueueSample struct {
	at         time.Time
	targetNode uint64
	kind       string
	result     string
}

type networkRPCSample struct {
	at         time.Time
	targetNode uint64
	serviceID  uint8
	result     string
	duration   time.Duration
}

type networkRPCKey struct {
	targetNode uint64
	serviceID  uint8
}

func newNetworkObservability(cfg networkObservabilityConfig) *networkObservability {
	if cfg.Window <= 0 {
		cfg.Window = defaultNetworkObservabilityWindow
	}
	if cfg.MaxEvents <= 0 {
		cfg.MaxEvents = defaultNetworkObservabilityMaxEvents
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	cfg.Seeds = append([]string(nil), cfg.Seeds...)
	cfg.StaticNodes = append([]NodeConfigRef(nil), cfg.StaticNodes...)
	return &networkObservability{cfg: cfg, inflight: map[networkRPCKey]int{}}
}

// TransportHooks returns callbacks that record local transport traffic and RPC outcomes.
func (o *networkObservability) TransportHooks() transport.ObserverHooks {
	if o == nil {
		return transport.ObserverHooks{}
	}
	return transport.ObserverHooks{
		OnSend: func(msgType uint8, bytes int) {
			o.recordTraffic(msgType, bytes, networkSampleDirectionTX)
		},
		OnReceive: func(msgType uint8, bytes int) {
			o.recordTraffic(msgType, bytes, networkSampleDirectionRX)
		},
		OnDial: func(event transport.DialEvent) {
			o.recordDial(event)
		},
		OnEnqueue: func(event transport.EnqueueEvent) {
			o.recordEnqueue(event)
		},
		OnRPCClient: func(event transport.RPCClientEvent) {
			o.recordRPCClient(event)
		},
	}
}

// ClusterHooks returns callbacks that add recent cluster status events to the network view.
func (o *networkObservability) ClusterHooks() raftcluster.ObserverHooks {
	if o == nil {
		return raftcluster.ObserverHooks{}
	}
	return raftcluster.ObserverHooks{
		OnNodeStatusChange: func(nodeID uint64, from, to controllermeta.NodeStatus) {
			if from == to {
				return
			}
			o.appendEvent(managementusecase.NetworkEvent{
				At:         o.now(),
				Severity:   "warn",
				Kind:       "node_status_change",
				TargetNode: nodeID,
				Message:    fmt.Sprintf("node %d changed status", nodeID),
			})
		},
		OnLeaderChange: func(slotID uint32, _, to multiraft.NodeID) {
			if to == 0 {
				return
			}
			o.appendEvent(managementusecase.NetworkEvent{
				At:         o.now(),
				Severity:   "info",
				Kind:       "slot_leader_change",
				TargetNode: uint64(to),
				Message:    fmt.Sprintf("slot %d leader changed", slotID),
			})
		},
	}
}

// NetworkSnapshot returns a copy-safe local network observation snapshot for the requested time.
func (o *networkObservability) NetworkSnapshot(now time.Time) managementusecase.NetworkObservationSnapshot {
	if o == nil {
		return managementusecase.NetworkObservationSnapshot{}
	}
	o.mu.Lock()
	defer o.mu.Unlock()

	cutoff := now.Add(-o.cfg.Window)
	o.pruneLocked(cutoff)

	snap := managementusecase.NetworkObservationSnapshot{
		LocalCollectorAvailable: true,
		PeerErrors:              map[uint64]managementusecase.NetworkPeerErrors{},
		Discovery:               o.discoverySnapshotLocked(),
		ChannelReplication: managementusecase.NetworkChannelReplication{
			LongPollConfig: managementusecase.NetworkLongPollConfig{
				LaneCount:   o.cfg.LongPollLaneCount,
				MaxWait:     o.cfg.LongPollMaxWait,
				MaxBytes:    o.cfg.LongPollMaxBytes,
				MaxChannels: o.cfg.LongPollMaxChannels,
			},
			DataPlaneRPCTimeout: o.cfg.DataPlaneRPCTimeout,
		},
	}
	snap.Traffic.Scope = "local_total_by_msg_type"
	snap.Traffic.PeerBreakdownAvailable = false

	trafficByType := map[string]managementusecase.NetworkTrafficMessageType{}
	for _, sample := range o.traffic {
		if sample.direction == networkSampleDirectionTX {
			snap.Traffic.TXBytes1m += int64(sample.bytes)
		} else {
			snap.Traffic.RXBytes1m += int64(sample.bytes)
		}
		key := sample.direction + ":" + transportMsgType(sample.msgType)
		entry := trafficByType[key]
		entry.Direction = sample.direction
		entry.MessageType = transportMsgType(sample.msgType)
		entry.Bytes1m += int64(sample.bytes)
		trafficByType[key] = entry
	}
	windowSeconds := o.cfg.Window.Seconds()
	if windowSeconds > 0 {
		snap.Traffic.TXBps = float64(snap.Traffic.TXBytes1m) / windowSeconds
		snap.Traffic.RXBps = float64(snap.Traffic.RXBytes1m) / windowSeconds
	}
	for _, entry := range trafficByType {
		if windowSeconds > 0 {
			entry.Bps = float64(entry.Bytes1m) / windowSeconds
		}
		snap.Traffic.ByMessageType = append(snap.Traffic.ByMessageType, entry)
	}
	sort.Slice(snap.Traffic.ByMessageType, func(i, j int) bool {
		if snap.Traffic.ByMessageType[i].Direction != snap.Traffic.ByMessageType[j].Direction {
			return snap.Traffic.ByMessageType[i].Direction < snap.Traffic.ByMessageType[j].Direction
		}
		return snap.Traffic.ByMessageType[i].MessageType < snap.Traffic.ByMessageType[j].MessageType
	})

	for _, sample := range o.dials {
		if sample.result != "dial_error" {
			continue
		}
		errs := snap.PeerErrors[sample.targetNode]
		errs.DialError1m++
		snap.PeerErrors[sample.targetNode] = errs
	}
	for _, sample := range o.enqueues {
		if sample.result != "queue_full" {
			continue
		}
		errs := snap.PeerErrors[sample.targetNode]
		errs.QueueFull1m++
		snap.PeerErrors[sample.targetNode] = errs
	}

	services := map[networkRPCKey]*networkServiceAccumulator{}
	for key, inflight := range o.inflight {
		acc := ensureNetworkServiceAccumulator(services, key)
		acc.service.Inflight = inflight
	}
	for _, sample := range o.rpcs {
		key := networkRPCKey{targetNode: sample.targetNode, serviceID: sample.serviceID}
		acc := ensureNetworkServiceAccumulator(services, key)
		acc.service.Calls1m++
		acc.service.LastSeenAt = sample.at
		acc.durations = append(acc.durations, sample.duration)
		switch sample.result {
		case "ok":
			acc.service.Success1m++
		case "timeout":
			if networkRPCExpectedTimeout(sample.serviceID, sample.result) {
				acc.service.ExpectedTimeout1m++
			} else {
				acc.service.Timeout1m++
				errs := snap.PeerErrors[sample.targetNode]
				errs.Timeout1m++
				snap.PeerErrors[sample.targetNode] = errs
			}
		case "queue_full":
			acc.service.QueueFull1m++
		case "remote_error":
			acc.service.RemoteError1m++
			errs := snap.PeerErrors[sample.targetNode]
			errs.RemoteError1m++
			snap.PeerErrors[sample.targetNode] = errs
		default:
			acc.service.OtherError1m++
		}
	}
	for _, acc := range services {
		if len(acc.durations) > 0 {
			acc.service.P50Ms = durationPercentileMs(acc.durations, 0.50)
			acc.service.P95Ms = durationPercentileMs(acc.durations, 0.95)
			acc.service.P99Ms = durationPercentileMs(acc.durations, 0.99)
		}
		snap.Services = append(snap.Services, acc.service)
		if acc.service.Group == "channel_data_plane" || acc.service.Service == "channel_append" {
			snap.ChannelReplication.Services = append(snap.ChannelReplication.Services, acc.service)
		}
	}
	sortNetworkServices(snap.Services)
	sortNetworkServices(snap.ChannelReplication.Services)

	for _, stat := range o.dataPlanePoolStatsLocked() {
		snap.DataPlanePools = append(snap.DataPlanePools, managementusecase.NetworkPoolPeerStats{
			NodeID: uint64(stat.NodeID),
			Active: stat.Active,
			Idle:   stat.Idle,
		})
	}
	sort.Slice(snap.DataPlanePools, func(i, j int) bool { return snap.DataPlanePools[i].NodeID < snap.DataPlanePools[j].NodeID })

	snap.Events = append([]managementusecase.NetworkEvent(nil), o.events...)
	sort.Slice(snap.Events, func(i, j int) bool { return snap.Events[i].At.After(snap.Events[j].At) })
	if len(snap.Events) > o.cfg.MaxEvents {
		snap.Events = snap.Events[:o.cfg.MaxEvents]
	}
	return snap
}

type networkServiceAccumulator struct {
	service   managementusecase.NetworkRPCService
	durations []time.Duration
}

func ensureNetworkServiceAccumulator(services map[networkRPCKey]*networkServiceAccumulator, key networkRPCKey) *networkServiceAccumulator {
	if acc := services[key]; acc != nil {
		return acc
	}
	service := transportRPCServiceName(key.serviceID)
	acc := &networkServiceAccumulator{service: managementusecase.NetworkRPCService{
		ServiceID:  key.serviceID,
		Service:    service,
		Group:      transportRPCServiceGroup(service),
		TargetNode: key.targetNode,
	}}
	services[key] = acc
	return acc
}

func (o *networkObservability) recordTraffic(msgType uint8, bytes int, direction string) {
	if o == nil || bytes <= 0 {
		return
	}
	o.mu.Lock()
	o.traffic = append(o.traffic, networkTrafficSample{at: o.now(), direction: direction, msgType: msgType, bytes: bytes})
	o.mu.Unlock()
}

func (o *networkObservability) recordDial(event transport.DialEvent) {
	if o == nil {
		return
	}
	sample := networkDialSample{at: o.now(), targetNode: uint64(event.TargetNode), result: event.Result, duration: event.Duration}
	o.mu.Lock()
	o.dials = append(o.dials, sample)
	if event.Result == "dial_error" {
		o.events = append(o.events, managementusecase.NetworkEvent{At: sample.at, Severity: "error", Kind: "dial_error", TargetNode: sample.targetNode, Message: fmt.Sprintf("dial to node %d failed", sample.targetNode)})
	}
	o.mu.Unlock()
}

func (o *networkObservability) recordEnqueue(event transport.EnqueueEvent) {
	if o == nil {
		return
	}
	sample := networkEnqueueSample{at: o.now(), targetNode: uint64(event.TargetNode), kind: event.Kind, result: event.Result}
	o.mu.Lock()
	o.enqueues = append(o.enqueues, sample)
	if event.Result == "queue_full" {
		message := fmt.Sprintf("%s queue full for node %d", event.Kind, sample.targetNode)
		if event.Kind == "rpc" || event.Kind == "" {
			message = fmt.Sprintf("rpc queue full for node %d", sample.targetNode)
		}
		o.events = append(o.events, managementusecase.NetworkEvent{At: sample.at, Severity: "warn", Kind: "queue_full", TargetNode: sample.targetNode, Message: message})
	}
	o.mu.Unlock()
}

func (o *networkObservability) recordRPCClient(event transport.RPCClientEvent) {
	if o == nil {
		return
	}
	at := o.now()
	key := networkRPCKey{targetNode: uint64(event.TargetNode), serviceID: event.ServiceID}
	o.mu.Lock()
	o.inflight[key] = event.Inflight
	if event.Result != "" {
		o.rpcs = append(o.rpcs, networkRPCSample{at: at, targetNode: key.targetNode, serviceID: event.ServiceID, result: event.Result, duration: event.Duration})
		if event.Result == "timeout" && !networkRPCExpectedTimeout(event.ServiceID, event.Result) {
			service := transportRPCServiceName(event.ServiceID)
			o.events = append(o.events, managementusecase.NetworkEvent{At: at, Severity: "warn", Kind: "rpc_timeout", TargetNode: key.targetNode, Service: service, Message: fmt.Sprintf("rpc timeout for %s on node %d", service, key.targetNode)})
		} else if event.Result == "remote_error" {
			service := transportRPCServiceName(event.ServiceID)
			o.events = append(o.events, managementusecase.NetworkEvent{At: at, Severity: "warn", Kind: "rpc_remote_error", TargetNode: key.targetNode, Service: service, Message: fmt.Sprintf("rpc remote error for %s on node %d", service, key.targetNode)})
		}
	}
	o.mu.Unlock()
}

func (o *networkObservability) appendEvent(event managementusecase.NetworkEvent) {
	if o == nil {
		return
	}
	o.mu.Lock()
	o.events = append(o.events, event)
	o.mu.Unlock()
}

func (o *networkObservability) now() time.Time {
	return o.cfg.Now().UTC()
}

func (o *networkObservability) pruneLocked(cutoff time.Time) {
	o.traffic = pruneTrafficSamples(o.traffic, cutoff)
	o.dials = pruneDialSamples(o.dials, cutoff)
	o.enqueues = pruneEnqueueSamples(o.enqueues, cutoff)
	o.rpcs = pruneRPCSamples(o.rpcs, cutoff)
	o.events = pruneNetworkEvents(o.events, cutoff)
}

func (o *networkObservability) discoverySnapshotLocked() managementusecase.NetworkDiscovery {
	out := managementusecase.NetworkDiscovery{
		ListenAddr:                    o.cfg.ListenAddr,
		AdvertiseAddr:                 o.cfg.AdvertiseAddr,
		Seeds:                         append([]string(nil), o.cfg.Seeds...),
		PoolSize:                      o.cfg.PoolSize,
		DataPlanePoolSize:             o.cfg.DataPlanePoolSize,
		DialTimeout:                   o.cfg.DialTimeout,
		ControllerObservationInterval: o.cfg.ControllerObservationInterval,
	}
	out.StaticNodes = make([]managementusecase.NetworkDiscoveryNode, 0, len(o.cfg.StaticNodes))
	for _, node := range o.cfg.StaticNodes {
		out.StaticNodes = append(out.StaticNodes, managementusecase.NetworkDiscoveryNode{NodeID: node.ID, Addr: node.Addr})
	}
	return out
}

func (o *networkObservability) dataPlanePoolStatsLocked() []transport.PoolPeerStats {
	if o.cfg.DataPlanePoolStats == nil {
		return nil
	}
	return append([]transport.PoolPeerStats(nil), o.cfg.DataPlanePoolStats()...)
}

func pruneTrafficSamples(samples []networkTrafficSample, cutoff time.Time) []networkTrafficSample {
	out := samples[:0]
	for _, sample := range samples {
		if !sample.at.Before(cutoff) {
			out = append(out, sample)
		}
	}
	return out
}

func pruneDialSamples(samples []networkDialSample, cutoff time.Time) []networkDialSample {
	out := samples[:0]
	for _, sample := range samples {
		if !sample.at.Before(cutoff) {
			out = append(out, sample)
		}
	}
	return out
}

func pruneEnqueueSamples(samples []networkEnqueueSample, cutoff time.Time) []networkEnqueueSample {
	out := samples[:0]
	for _, sample := range samples {
		if !sample.at.Before(cutoff) {
			out = append(out, sample)
		}
	}
	return out
}

func pruneRPCSamples(samples []networkRPCSample, cutoff time.Time) []networkRPCSample {
	out := samples[:0]
	for _, sample := range samples {
		if !sample.at.Before(cutoff) {
			out = append(out, sample)
		}
	}
	return out
}

func pruneNetworkEvents(events []managementusecase.NetworkEvent, cutoff time.Time) []managementusecase.NetworkEvent {
	out := events[:0]
	for _, event := range events {
		if !event.At.Before(cutoff) {
			out = append(out, event)
		}
	}
	return out
}

func networkRPCExpectedTimeout(serviceID uint8, result string) bool {
	return result == "timeout" && transportRPCServiceName(serviceID) == "channel_long_poll_fetch"
}

func transportRPCServiceGroup(service string) string {
	switch service {
	case "controller":
		return "controller"
	case "managed_slot":
		return "slot"
	case "channel_fetch", "channel_reconcile_probe", "channel_long_poll_fetch":
		return "channel_data_plane"
	case "forward":
		return "cluster"
	default:
		return "usecase"
	}
}

func durationPercentileMs(values []time.Duration, quantile float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]time.Duration(nil), values...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	idx := int(quantile*float64(len(sorted))+0.999999999) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return float64(sorted[idx]) / float64(time.Millisecond)
}

func sortNetworkServices(services []managementusecase.NetworkRPCService) {
	sort.Slice(services, func(i, j int) bool {
		if services[i].Group != services[j].Group {
			return services[i].Group < services[j].Group
		}
		if services[i].Service != services[j].Service {
			return services[i].Service < services[j].Service
		}
		return services[i].TargetNode < services[j].TargetNode
	})
}
