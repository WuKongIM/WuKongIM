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
	// networkObservabilityBucketSize is the manager collector precision; buckets bound memory and may over-retain by at most one bucket.
	networkObservabilityBucketSize    = 100 * time.Millisecond
	networkObservabilityPruneInterval = time.Second
	networkObservabilityMaxDurations  = 256
	networkSampleDirectionTX          = "tx"
	networkSampleDirectionRX          = "rx"
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

// networkObservability records local transport hook aggregates for manager snapshots.
type networkObservability struct {
	mu sync.Mutex

	cfg networkObservabilityConfig
	// trafficBuckets stores byte totals per bounded time bucket, direction, and message type.
	trafficBuckets map[networkTrafficBucketKey]int64
	// dialBuckets stores dial outcome counts per bounded time bucket and peer.
	dialBuckets map[networkDialBucketKey]int
	// enqueueBuckets stores enqueue outcome counts per bounded time bucket, peer, and queue kind.
	enqueueBuckets map[networkEnqueueBucketKey]int
	// rpcBuckets stores RPC outcome counts and bounded latency samples per time bucket.
	rpcBuckets map[networkRPCBucketKey]networkRPCAggregate
	// events stores bounded recent warning and status events.
	events []managementusecase.NetworkEvent
	// inflight stores the latest in-flight RPC gauge per peer and service.
	inflight map[networkRPCKey]networkInflightGauge
	// lastPruneAt bounds write-path pruning cost under high-volume transport hooks.
	lastPruneAt time.Time
}

type networkTrafficBucketKey struct {
	bucket    time.Time
	direction string
	msgType   uint8
}

type networkDialBucketKey struct {
	bucket     time.Time
	targetNode uint64
	result     string
}

type networkEnqueueBucketKey struct {
	bucket     time.Time
	targetNode uint64
	kind       string
	result     string
}

type networkRPCBucketKey struct {
	bucket     time.Time
	targetNode uint64
	serviceID  uint8
	result     string
}

type networkRPCAggregate struct {
	count      int
	lastSeenAt time.Time
	durations  []time.Duration
}

type networkInflightGauge struct {
	count      int
	lastSeenAt time.Time
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
	return &networkObservability{
		cfg:            cfg,
		trafficBuckets: map[networkTrafficBucketKey]int64{},
		dialBuckets:    map[networkDialBucketKey]int{},
		enqueueBuckets: map[networkEnqueueBucketKey]int{},
		rpcBuckets:     map[networkRPCBucketKey]networkRPCAggregate{},
		inflight:       map[networkRPCKey]networkInflightGauge{},
	}
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

	cutoff := now.Add(-o.cfg.Window)
	o.pruneLocked(cutoff)
	dataPlanePoolStats := o.cfg.DataPlanePoolStats

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
	for key, bytes := range o.trafficBuckets {
		if key.direction == networkSampleDirectionTX {
			snap.Traffic.TXBytes1m += bytes
		} else {
			snap.Traffic.RXBytes1m += bytes
		}
		typeKey := key.direction + ":" + transportMsgType(key.msgType)
		entry := trafficByType[typeKey]
		entry.Direction = key.direction
		entry.MessageType = transportMsgType(key.msgType)
		entry.Bytes1m += bytes
		trafficByType[typeKey] = entry
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

	for key, count := range o.dialBuckets {
		if key.result != "dial_error" {
			continue
		}
		errs := snap.PeerErrors[key.targetNode]
		errs.DialError1m += count
		snap.PeerErrors[key.targetNode] = errs
	}
	for key, count := range o.enqueueBuckets {
		if key.result != "queue_full" {
			continue
		}
		errs := snap.PeerErrors[key.targetNode]
		errs.QueueFull1m += count
		snap.PeerErrors[key.targetNode] = errs
	}

	services := map[networkRPCKey]*networkServiceAccumulator{}
	for key, inflight := range o.inflight {
		acc := ensureNetworkServiceAccumulator(services, key)
		acc.service.Inflight = inflight.count
	}
	for key, aggregate := range o.rpcBuckets {
		count := aggregate.count
		if count == 0 {
			continue
		}
		serviceKey := networkRPCKey{targetNode: key.targetNode, serviceID: key.serviceID}
		acc := ensureNetworkServiceAccumulator(services, serviceKey)
		acc.service.Calls1m += count
		if aggregate.lastSeenAt.After(acc.service.LastSeenAt) {
			acc.service.LastSeenAt = aggregate.lastSeenAt
		}
		acc.durations = append(acc.durations, aggregate.durations...)
		switch key.result {
		case "ok":
			acc.service.Success1m += count
		case "timeout":
			acc.service.Timeout1m += count
		case "queue_full":
			acc.service.QueueFull1m += count
		case "remote_error":
			acc.service.RemoteError1m += count
		default:
			acc.service.OtherError1m += count
		}
	}
	for _, acc := range services {
		if len(acc.durations) > 0 {
			acc.service.P50Ms = durationPercentileMs(acc.durations, 0.50)
			acc.service.P95Ms = durationPercentileMs(acc.durations, 0.95)
			acc.service.P99Ms = durationPercentileMs(acc.durations, 0.99)
		}
		snap.Services = append(snap.Services, acc.service)
	}
	sortNetworkServices(snap.Services)

	snap.Events = append([]managementusecase.NetworkEvent(nil), o.events...)
	sort.Slice(snap.Events, func(i, j int) bool { return snap.Events[i].At.After(snap.Events[j].At) })
	if len(snap.Events) > o.cfg.MaxEvents {
		snap.Events = snap.Events[:o.cfg.MaxEvents]
	}
	o.mu.Unlock()

	if dataPlanePoolStats != nil {
		for _, stat := range dataPlanePoolStats() {
			snap.DataPlanePools = append(snap.DataPlanePools, managementusecase.NetworkPoolPeerStats{
				NodeID: uint64(stat.NodeID),
				Active: stat.Active,
				Idle:   stat.Idle,
			})
		}
	}
	sort.Slice(snap.DataPlanePools, func(i, j int) bool { return snap.DataPlanePools[i].NodeID < snap.DataPlanePools[j].NodeID })
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
	at := o.now()
	o.mu.Lock()
	key := networkTrafficBucketKey{bucket: networkObservabilityBucket(at), direction: direction, msgType: msgType}
	o.trafficBuckets[key] += int64(bytes)
	o.pruneForWriteLocked(at)
	o.mu.Unlock()
}

func (o *networkObservability) recordDial(event transport.DialEvent) {
	if o == nil {
		return
	}
	at := o.now()
	targetNode := uint64(event.TargetNode)
	o.mu.Lock()
	key := networkDialBucketKey{bucket: networkObservabilityBucket(at), targetNode: targetNode, result: event.Result}
	o.dialBuckets[key]++
	if event.Result == "dial_error" {
		o.events = append(o.events, managementusecase.NetworkEvent{At: at, Severity: "error", Kind: "dial_error", TargetNode: targetNode, Message: fmt.Sprintf("dial to node %d failed", targetNode)})
	}
	o.pruneForWriteLocked(at)
	o.mu.Unlock()
}

func (o *networkObservability) recordEnqueue(event transport.EnqueueEvent) {
	if o == nil {
		return
	}
	at := o.now()
	targetNode := uint64(event.TargetNode)
	o.mu.Lock()
	key := networkEnqueueBucketKey{bucket: networkObservabilityBucket(at), targetNode: targetNode, kind: event.Kind, result: event.Result}
	o.enqueueBuckets[key]++
	if event.Result == "queue_full" {
		message := fmt.Sprintf("%s queue full for node %d", event.Kind, targetNode)
		if event.Kind == "rpc" || event.Kind == "" {
			message = fmt.Sprintf("rpc queue full for node %d", targetNode)
		}
		o.events = append(o.events, managementusecase.NetworkEvent{At: at, Severity: "warn", Kind: "queue_full", TargetNode: targetNode, Message: message})
	}
	o.pruneForWriteLocked(at)
	o.mu.Unlock()
}

func (o *networkObservability) recordRPCClient(event transport.RPCClientEvent) {
	if o == nil {
		return
	}
	at := o.now()
	key := networkRPCKey{targetNode: uint64(event.TargetNode), serviceID: event.ServiceID}
	o.mu.Lock()
	if event.Inflight > 0 {
		o.inflight[key] = networkInflightGauge{count: event.Inflight, lastSeenAt: at}
	} else {
		delete(o.inflight, key)
	}
	if event.Result != "" {
		bucketKey := networkRPCBucketKey{bucket: networkObservabilityBucket(at), targetNode: key.targetNode, serviceID: event.ServiceID, result: event.Result}
		aggregate := o.rpcBuckets[bucketKey]
		aggregate.count++
		aggregate.lastSeenAt = at
		if len(aggregate.durations) < networkObservabilityMaxDurations {
			aggregate.durations = append(aggregate.durations, event.Duration)
		}
		o.rpcBuckets[bucketKey] = aggregate
		if event.Result == "timeout" {
			service := transportRPCServiceName(event.ServiceID)
			o.events = append(o.events, managementusecase.NetworkEvent{At: at, Severity: "warn", Kind: "rpc_timeout", TargetNode: key.targetNode, Service: service, Message: fmt.Sprintf("rpc timeout for %s on node %d", service, key.targetNode)})
		} else if event.Result == "remote_error" {
			service := transportRPCServiceName(event.ServiceID)
			o.events = append(o.events, managementusecase.NetworkEvent{At: at, Severity: "warn", Kind: "rpc_remote_error", TargetNode: key.targetNode, Service: service, Message: fmt.Sprintf("rpc remote error for %s on node %d", service, key.targetNode)})
		}
	}
	o.pruneForWriteLocked(at)
	o.mu.Unlock()
}

func (o *networkObservability) appendEvent(event managementusecase.NetworkEvent) {
	if o == nil {
		return
	}
	o.mu.Lock()
	o.events = append(o.events, event)
	o.pruneForWriteLocked(event.At)
	o.mu.Unlock()
}

func (o *networkObservability) now() time.Time {
	return o.cfg.Now().UTC()
}

func (o *networkObservability) pruneLocked(cutoff time.Time) {
	o.pruneTrafficBucketsLocked(cutoff)
	o.pruneDialBucketsLocked(cutoff)
	o.pruneEnqueueBucketsLocked(cutoff)
	o.pruneRPCBucketsLocked(cutoff)
	o.pruneInflightLocked(cutoff)
	o.events = pruneNetworkEvents(o.events, cutoff)
}

func (o *networkObservability) pruneForWriteLocked(now time.Time) {
	if !o.shouldPruneForWriteLocked(now) {
		o.events = capNetworkEvents(o.events, o.cfg.MaxEvents)
		return
	}
	o.lastPruneAt = now
	o.pruneLocked(now.Add(-o.cfg.Window))
	o.events = capNetworkEvents(o.events, o.cfg.MaxEvents)
}

func (o *networkObservability) shouldPruneForWriteLocked(now time.Time) bool {
	if o.lastPruneAt.IsZero() || now.Before(o.lastPruneAt) {
		return true
	}
	return now.Sub(o.lastPruneAt) >= networkObservabilityPruneInterval
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

func pruneNetworkEvents(events []managementusecase.NetworkEvent, cutoff time.Time) []managementusecase.NetworkEvent {
	out := events[:0]
	for _, event := range events {
		if !event.At.Before(cutoff) {
			out = append(out, event)
		}
	}
	return out
}

func capNetworkEvents(events []managementusecase.NetworkEvent, limit int) []managementusecase.NetworkEvent {
	if limit <= 0 || len(events) <= limit {
		return events
	}
	return events[len(events)-limit:]
}

func (o *networkObservability) pruneTrafficBucketsLocked(cutoff time.Time) {
	for key := range o.trafficBuckets {
		if networkObservabilityBucketExpired(key.bucket, cutoff) {
			delete(o.trafficBuckets, key)
		}
	}
}

func (o *networkObservability) pruneDialBucketsLocked(cutoff time.Time) {
	for key := range o.dialBuckets {
		if networkObservabilityBucketExpired(key.bucket, cutoff) {
			delete(o.dialBuckets, key)
		}
	}
}

func (o *networkObservability) pruneEnqueueBucketsLocked(cutoff time.Time) {
	for key := range o.enqueueBuckets {
		if networkObservabilityBucketExpired(key.bucket, cutoff) {
			delete(o.enqueueBuckets, key)
		}
	}
}

func (o *networkObservability) pruneRPCBucketsLocked(cutoff time.Time) {
	for key := range o.rpcBuckets {
		if networkObservabilityBucketExpired(key.bucket, cutoff) {
			delete(o.rpcBuckets, key)
		}
	}
}

func (o *networkObservability) pruneInflightLocked(cutoff time.Time) {
	for key, gauge := range o.inflight {
		if gauge.lastSeenAt.Before(cutoff) {
			delete(o.inflight, key)
		}
	}
}

func networkObservabilityBucket(at time.Time) time.Time {
	return at.Truncate(networkObservabilityBucketSize)
}

func networkObservabilityBucketExpired(bucket, cutoff time.Time) bool {
	return !bucket.Add(networkObservabilityBucketSize).After(cutoff)
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
