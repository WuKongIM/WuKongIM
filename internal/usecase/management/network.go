package management

import (
	"context"
	"sort"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/transport"
)

const (
	networkSourceOK          = "ok"
	networkSourceUnavailable = "unavailable"
	networkSummaryViewLocal  = "local_node"
	networkTrafficScopeLocal = "local_total_by_msg_type"
	networkEventLimit        = 50
)

// NetworkSnapshotReader reads local node transport observations for manager aggregation.
type NetworkSnapshotReader interface {
	NetworkSnapshot(now time.Time) NetworkObservationSnapshot
}

// NetworkObservationSnapshot is a copy-safe local collector snapshot.
type NetworkObservationSnapshot struct {
	// LocalCollectorAvailable reports whether the local collector supplied the snapshot.
	LocalCollectorAvailable bool
	// History contains aligned local collector time-series aggregates.
	History NetworkHistory
	// Traffic contains local transport byte counters grouped by message type.
	Traffic NetworkTraffic
	// DataPlanePools contains local channel data-plane pool counters by peer.
	DataPlanePools []NetworkPoolPeerStats
	// PeerErrors contains non-RPC peer error counters collected locally.
	PeerErrors map[uint64]NetworkPeerErrors
	// Services contains local RPC counters grouped by service and peer.
	Services []NetworkRPCService
	// ChannelReplication contains local channel replication network details.
	ChannelReplication NetworkChannelReplication
	// Discovery contains local network discovery and dial configuration.
	Discovery NetworkDiscovery
	// Events contains recent local network observations.
	Events []NetworkEvent
}

// NetworkPoolPeerStats contains pool counters for one remote peer.
type NetworkPoolPeerStats struct {
	// NodeID identifies the remote cluster peer.
	NodeID uint64
	// Active counts active connections in the pool.
	Active int
	// Idle counts idle connections in the pool.
	Idle int
}

// NetworkSummary is the manager-facing local node network observation snapshot.
type NetworkSummary struct {
	// GeneratedAt is the UTC timestamp when the summary was assembled.
	GeneratedAt time.Time
	// Scope describes the current summary view.
	Scope NetworkScope
	// SourceStatus reports which local and controller sources were available.
	SourceStatus NetworkSourceStatus
	// History contains local collector time-series aggregates for chart views.
	History NetworkHistory
	// Headline contains top-level counters for the network page.
	Headline NetworkHeadline
	// Traffic contains local transport traffic counters.
	Traffic NetworkTraffic
	// Peers contains per-remote-node local network observations.
	Peers []NetworkPeer
	// Services contains per-RPC-service local observations.
	Services []NetworkRPCService
	// ChannelReplication contains channel data-plane network observations.
	ChannelReplication NetworkChannelReplication
	// Discovery contains network discovery and transport configuration.
	Discovery NetworkDiscovery
	// Events contains recent local network events, newest first.
	Events []NetworkEvent
}

// NetworkHistory contains local collector time-series aggregates for manager charts.
type NetworkHistory struct {
	// Window is the recent interval covered by the local collector.
	Window time.Duration
	// Step is the chart aggregation interval between points.
	Step time.Duration
	// Traffic contains traffic byte totals grouped by step.
	Traffic []NetworkTrafficHistoryPoint
	// RPC contains RPC result totals grouped by step.
	RPC []NetworkRPCHistoryPoint
	// Errors contains network error totals grouped by step.
	Errors []NetworkErrorHistoryPoint
}

// NetworkTrafficHistoryPoint contains traffic totals for one chart step.
type NetworkTrafficHistoryPoint struct {
	// At is the UTC start time of the chart step.
	At time.Time
	// TXBytes counts transmitted bytes in the chart step.
	TXBytes int64
	// RXBytes counts received bytes in the chart step.
	RXBytes int64
}

// NetworkRPCHistoryPoint contains RPC result totals for one chart step.
type NetworkRPCHistoryPoint struct {
	// At is the UTC start time of the chart step.
	At time.Time
	// Calls counts completed RPC calls in the chart step.
	Calls int
	// Success counts successful RPC calls in the chart step.
	Success int
	// Errors counts abnormal RPC outcomes in the chart step.
	Errors int
	// ExpectedTimeouts counts neutral service-level timeout outcomes in the chart step.
	ExpectedTimeouts int
}

// NetworkErrorHistoryPoint contains network error totals for one chart step.
type NetworkErrorHistoryPoint struct {
	// At is the UTC start time of the chart step.
	At time.Time
	// DialErrors counts dial failures in the chart step.
	DialErrors int
	// QueueFull counts enqueue and RPC queue-full outcomes in the chart step.
	QueueFull int
	// Timeouts counts abnormal RPC timeouts in the chart step.
	Timeouts int
	// RemoteErrors counts remote RPC errors in the chart step.
	RemoteErrors int
}

// NetworkScope describes which node and controller context produced a summary.
type NetworkScope struct {
	// View is the manager-facing view name; the first release is local_node.
	View string
	// LocalNodeID identifies the node that owns the local observations.
	LocalNodeID uint64
	// ControllerLeaderID identifies the observed controller leader when available.
	ControllerLeaderID uint64
}

// NetworkSourceStatus reports source availability for the network summary.
type NetworkSourceStatus struct {
	// LocalCollector is ok when the local collector supplied observations.
	LocalCollector string
	// ControllerContext is ok when controller node context was read successfully.
	ControllerContext string
	// RuntimeViews is ok when controller runtime views were read successfully.
	RuntimeViews string
	// Errors contains source read errors keyed by source name.
	Errors map[string]string
}

// NetworkHeadline contains top-level counters for the manager network page.
type NetworkHeadline struct {
	// RemotePeers counts remote peers seen by cluster or local observations.
	RemotePeers int
	// AliveNodes counts controller nodes in alive status.
	AliveNodes int
	// SuspectNodes counts controller nodes in suspect status.
	SuspectNodes int
	// DeadNodes counts controller nodes in dead status.
	DeadNodes int
	// DrainingNodes counts controller nodes in draining status.
	DrainingNodes int
	// PoolActive totals active cluster and data-plane pool connections.
	PoolActive int
	// PoolIdle totals idle cluster and data-plane pool connections.
	PoolIdle int
	// RPCInflight totals local outbound RPC calls currently in flight.
	RPCInflight int
	// DialErrors1m counts recent dial errors.
	DialErrors1m int
	// QueueFull1m counts recent queue-full outcomes.
	QueueFull1m int
	// Timeouts1m counts abnormal recent RPC timeouts.
	Timeouts1m int
	// StaleObservations counts stale controller runtime views.
	StaleObservations int
}

// NetworkTraffic contains local transport traffic counters.
type NetworkTraffic struct {
	// Scope identifies the traffic aggregation scope.
	Scope string
	// TXBytes1m counts transmitted bytes in the recent one-minute window.
	TXBytes1m int64
	// RXBytes1m counts received bytes in the recent one-minute window.
	RXBytes1m int64
	// TXBps is the current transmit byte rate.
	TXBps float64
	// RXBps is the current receive byte rate.
	RXBps float64
	// PeerBreakdownAvailable reports whether traffic can be attributed to peers.
	PeerBreakdownAvailable bool
	// ByMessageType breaks traffic down by direction and message type.
	ByMessageType []NetworkTrafficMessageType
}

// NetworkTrafficMessageType contains traffic counters for one message type.
type NetworkTrafficMessageType struct {
	// Direction is tx or rx.
	Direction string
	// MessageType identifies the transport message type.
	MessageType string
	// Bytes1m counts bytes in the recent one-minute window.
	Bytes1m int64
	// Bps is the current byte rate.
	Bps float64
}

// NetworkPeer contains local network observations for one remote peer.
type NetworkPeer struct {
	// NodeID identifies the remote cluster node.
	NodeID uint64
	// Name is the controller node name when known.
	Name string
	// Addr is the controller node RPC address when known.
	Addr string
	// Health is the controller health status when known.
	Health string
	// LastHeartbeatAt is the latest controller heartbeat for this peer.
	LastHeartbeatAt time.Time
	// Pools contains cluster and data-plane pool counters.
	Pools NetworkPeerPools
	// RPC contains aggregate RPC counters for this peer.
	RPC NetworkPeerRPC
	// Errors contains recent peer error counters.
	Errors NetworkPeerErrors
}

// NetworkPoolStats contains active and idle pool counters.
type NetworkPoolStats struct {
	// Active counts active connections in the pool.
	Active int
	// Idle counts idle connections in the pool.
	Idle int
}

// NetworkPeerPools separates cluster and data-plane pool counters.
type NetworkPeerPools struct {
	// Cluster contains cluster transport pool counters.
	Cluster NetworkPoolStats
	// DataPlane contains channel data-plane transport pool counters.
	DataPlane NetworkPoolStats
}

// NetworkPeerRPC contains aggregate RPC counters for one peer.
type NetworkPeerRPC struct {
	// Inflight counts RPC calls currently in flight.
	Inflight int
	// Calls1m counts completed RPC calls in the recent one-minute window.
	Calls1m int
	// P95Ms is the largest observed service p95 latency for the peer.
	P95Ms float64
	// SuccessRate is nil when no completed calls are available.
	SuccessRate *float64
}

// NetworkPeerErrors contains recent peer error counters.
type NetworkPeerErrors struct {
	// DialError1m counts recent dial failures.
	DialError1m int
	// QueueFull1m counts recent queue-full outcomes.
	QueueFull1m int
	// Timeout1m excludes expected channel_long_poll_fetch wait timeouts.
	Timeout1m int
	// RemoteError1m counts recent remote RPC errors.
	RemoteError1m int
}

// NetworkRPCService contains local RPC observations for one service and target peer.
type NetworkRPCService struct {
	// ServiceID is the transport RPC service identifier.
	ServiceID uint8
	// Service is the stable service name shown by manager pages.
	Service string
	// Group is the stable service group shown by manager pages.
	Group string
	// TargetNode identifies the remote RPC target node.
	TargetNode uint64
	// Inflight counts RPC calls currently in flight.
	Inflight int
	// Calls1m counts completed calls in the recent one-minute window.
	Calls1m int
	// Success1m counts successful completed calls in the recent one-minute window.
	Success1m int
	// ExpectedTimeout1m counts normal service-level timeouts, such as long-poll wait expiry.
	ExpectedTimeout1m int
	// Timeout1m counts abnormal RPC timeouts.
	Timeout1m int
	// QueueFull1m counts queue-full outcomes.
	QueueFull1m int
	// RemoteError1m counts remote RPC errors.
	RemoteError1m int
	// OtherError1m counts other non-success outcomes.
	OtherError1m int
	// P50Ms is the median observed latency in milliseconds.
	P50Ms float64
	// P95Ms is the p95 observed latency in milliseconds.
	P95Ms float64
	// P99Ms is the p99 observed latency in milliseconds.
	P99Ms float64
	// LastSeenAt records the latest completed observation for this service.
	LastSeenAt time.Time
}

// NetworkChannelReplication contains channel data-plane network observations.
type NetworkChannelReplication struct {
	// Pool totals channel data-plane pool counters across peers.
	Pool NetworkPoolStats
	// Services contains channel replication RPC service summaries.
	Services []NetworkRPCService
	// LongPollConfig contains long-poll fetch configuration.
	LongPollConfig NetworkLongPollConfig
	// LongPollTimeouts1m counts expected long-poll wait expirations.
	LongPollTimeouts1m int
	// DataPlaneRPCTimeout is the configured data-plane RPC timeout.
	DataPlaneRPCTimeout time.Duration
}

// NetworkLongPollConfig contains channel long-poll fetch limits.
type NetworkLongPollConfig struct {
	// LaneCount is the number of long-poll lanes.
	LaneCount int
	// MaxWait is the maximum long-poll wait duration.
	MaxWait time.Duration
	// MaxBytes is the maximum bytes returned by one long-poll fetch.
	MaxBytes int
	// MaxChannels is the maximum channels included in one long-poll fetch.
	MaxChannels int
}

// NetworkDiscovery contains transport discovery and dial configuration.
type NetworkDiscovery struct {
	// ListenAddr is the local transport listen address.
	ListenAddr string
	// AdvertiseAddr is the advertised transport address.
	AdvertiseAddr string
	// Seeds contains configured seed addresses.
	Seeds []string
	// StaticNodes contains statically configured nodes.
	StaticNodes []NetworkDiscoveryNode
	// PoolSize is the cluster transport pool size.
	PoolSize int
	// DataPlanePoolSize is the channel data-plane transport pool size.
	DataPlanePoolSize int
	// DialTimeout is the configured transport dial timeout.
	DialTimeout time.Duration
	// ControllerObservationInterval is the configured controller observation interval.
	ControllerObservationInterval time.Duration
}

// NetworkDiscoveryNode contains one static discovery node.
type NetworkDiscoveryNode struct {
	// NodeID identifies the static cluster node.
	NodeID uint64
	// Addr is the configured address for the static node.
	Addr string
}

// NetworkEvent contains one recent local network event.
type NetworkEvent struct {
	// At records when the event happened.
	At time.Time
	// Severity is info, warn, or error.
	Severity string
	// Kind identifies the stable event category.
	Kind string
	// TargetNode identifies the remote node involved in the event.
	TargetNode uint64
	// Service names the RPC service involved in the event when applicable.
	Service string
	// Message is a short stable manager-facing description.
	Message string
}

// ListNetworkSummary returns the local-node cluster network summary for manager pages.
func (a *App) ListNetworkSummary(ctx context.Context) (NetworkSummary, error) {
	now := a.now().UTC()
	summary := NetworkSummary{
		GeneratedAt: now,
		Scope: NetworkScope{
			View:        networkSummaryViewLocal,
			LocalNodeID: a.localNodeID,
		},
		SourceStatus: NetworkSourceStatus{
			LocalCollector:    networkSourceOK,
			ControllerContext: networkSourceOK,
			RuntimeViews:      networkSourceOK,
			Errors:            map[string]string{},
		},
	}
	if a.cluster != nil {
		summary.Scope.ControllerLeaderID = a.cluster.ControllerLeaderID()
	}

	snapshot := NetworkObservationSnapshot{}
	if a.network != nil {
		snapshot = a.network.NetworkSnapshot(now)
		if !snapshot.LocalCollectorAvailable {
			summary.SourceStatus.LocalCollector = networkSourceUnavailable
		}
	} else {
		summary.SourceStatus.LocalCollector = networkSourceUnavailable
	}

	nodes := []clusterNodeForNetwork{}
	if a.cluster != nil {
		readNodes, err := a.cluster.ListNodesStrict(ctx)
		if err != nil {
			summary.SourceStatus.ControllerContext = networkSourceUnavailable
			summary.SourceStatus.Errors["nodes"] = err.Error()
		} else {
			for _, node := range readNodes {
				nodes = append(nodes, clusterNodeForNetwork{nodeID: node.NodeID, name: node.Name, addr: node.Addr, health: managerNodeStatus(node.Status), lastHeartbeatAt: node.LastHeartbeatAt})
				switch managerNodeStatus(node.Status) {
				case "alive":
					summary.Headline.AliveNodes++
				case "suspect":
					summary.Headline.SuspectNodes++
				case "dead":
					summary.Headline.DeadNodes++
				case "draining":
					summary.Headline.DrainingNodes++
				}
			}
		}

		views, err := a.cluster.ListObservedRuntimeViewsStrict(ctx)
		if err != nil {
			summary.SourceStatus.RuntimeViews = networkSourceUnavailable
			summary.SourceStatus.Errors["runtime_views"] = err.Error()
		} else {
			for _, view := range views {
				if now.Sub(view.LastReportAt) > a.scaleInRuntimeViewMaxAge {
					summary.Headline.StaleObservations++
				}
			}
		}
	} else {
		summary.SourceStatus.ControllerContext = networkSourceUnavailable
		summary.SourceStatus.RuntimeViews = networkSourceUnavailable
	}

	summary.History = copyNetworkHistory(snapshot.History)
	summary.Traffic = copyNetworkTraffic(snapshot.Traffic)
	if summary.Traffic.Scope == "" {
		summary.Traffic.Scope = networkTrafficScopeLocal
	}
	summary.Traffic.PeerBreakdownAvailable = false
	summary.Discovery = copyNetworkDiscovery(snapshot.Discovery)
	summary.ChannelReplication = copyNetworkChannelReplication(snapshot.ChannelReplication)

	peers := map[uint64]*NetworkPeer{}
	ensurePeer := func(nodeID uint64) *NetworkPeer {
		if nodeID == 0 || nodeID == a.localNodeID {
			return nil
		}
		if peer := peers[nodeID]; peer != nil {
			return peer
		}
		peer := &NetworkPeer{NodeID: nodeID, Health: "unknown"}
		peers[nodeID] = peer
		return peer
	}

	for _, node := range nodes {
		peer := ensurePeer(node.nodeID)
		if peer == nil {
			continue
		}
		peer.Name = node.name
		peer.Addr = node.addr
		peer.Health = node.health
		peer.LastHeartbeatAt = node.lastHeartbeatAt
	}

	clusterPools := []transport.PoolPeerStats(nil)
	if a.cluster != nil {
		clusterPools = a.cluster.TransportPoolStats()
	}
	for _, stat := range clusterPools {
		peer := ensurePeer(uint64(stat.NodeID))
		if peer == nil {
			continue
		}
		peer.Pools.Cluster.Active += stat.Active
		peer.Pools.Cluster.Idle += stat.Idle
		summary.Headline.PoolActive += stat.Active
		summary.Headline.PoolIdle += stat.Idle
	}

	for _, stat := range snapshot.DataPlanePools {
		peer := ensurePeer(stat.NodeID)
		if peer == nil {
			continue
		}
		peer.Pools.DataPlane.Active += stat.Active
		peer.Pools.DataPlane.Idle += stat.Idle
		summary.ChannelReplication.Pool.Active += stat.Active
		summary.ChannelReplication.Pool.Idle += stat.Idle
		summary.Headline.PoolActive += stat.Active
		summary.Headline.PoolIdle += stat.Idle
	}

	for nodeID, errs := range snapshot.PeerErrors {
		peer := ensurePeer(nodeID)
		if peer == nil {
			continue
		}
		peer.Errors.DialError1m += errs.DialError1m
		peer.Errors.QueueFull1m += errs.QueueFull1m
		peer.Errors.Timeout1m += errs.Timeout1m
		peer.Errors.RemoteError1m += errs.RemoteError1m
	}

	summary.Services = normalizeNetworkServices(snapshot.Services)
	peerSuccesses := map[uint64]int{}
	for _, service := range summary.Services {
		rateCalls := service.Calls1m - service.ExpectedTimeout1m
		if rateCalls < 0 {
			rateCalls = 0
		}
		peer := ensurePeer(service.TargetNode)
		if peer != nil {
			peer.RPC.Inflight += service.Inflight
			peer.RPC.Calls1m += rateCalls
			peerSuccesses[service.TargetNode] += service.Success1m
			if service.P95Ms > peer.RPC.P95Ms {
				peer.RPC.P95Ms = service.P95Ms
			}
			peer.Errors.QueueFull1m += service.QueueFull1m
			peer.Errors.Timeout1m += service.Timeout1m
			peer.Errors.RemoteError1m += service.RemoteError1m
		}
		summary.Headline.RPCInflight += service.Inflight
		if networkServiceIsChannelReplication(service) {
			summary.ChannelReplication.Services = append(summary.ChannelReplication.Services, service)
		}
		summary.ChannelReplication.LongPollTimeouts1m += expectedLongPollTimeouts(service)
	}

	for _, event := range snapshot.Events {
		ensurePeer(event.TargetNode)
	}

	summary.Events = copyNetworkEvents(snapshot.Events)
	sort.Slice(summary.Events, func(i, j int) bool { return summary.Events[i].At.After(summary.Events[j].At) })
	if len(summary.Events) > networkEventLimit {
		summary.Events = summary.Events[:networkEventLimit]
	}

	summary.Peers = make([]NetworkPeer, 0, len(peers))
	for _, peer := range peers {
		if peer.RPC.Calls1m > 0 {
			rate := float64(peerSuccesses[peer.NodeID]) / float64(peer.RPC.Calls1m)
			peer.RPC.SuccessRate = &rate
		}
		summary.Headline.DialErrors1m += peer.Errors.DialError1m
		summary.Headline.QueueFull1m += peer.Errors.QueueFull1m
		summary.Headline.Timeouts1m += peer.Errors.Timeout1m
		summary.Peers = append(summary.Peers, *peer)
	}
	sort.Slice(summary.Peers, func(i, j int) bool { return summary.Peers[i].NodeID < summary.Peers[j].NodeID })
	summary.Headline.RemotePeers = len(summary.Peers)

	sort.Slice(summary.Services, func(i, j int) bool {
		if summary.Services[i].Group != summary.Services[j].Group {
			return summary.Services[i].Group < summary.Services[j].Group
		}
		if summary.Services[i].Service != summary.Services[j].Service {
			return summary.Services[i].Service < summary.Services[j].Service
		}
		return summary.Services[i].TargetNode < summary.Services[j].TargetNode
	})
	sort.Slice(summary.ChannelReplication.Services, func(i, j int) bool {
		if summary.ChannelReplication.Services[i].Group != summary.ChannelReplication.Services[j].Group {
			return summary.ChannelReplication.Services[i].Group < summary.ChannelReplication.Services[j].Group
		}
		if summary.ChannelReplication.Services[i].Service != summary.ChannelReplication.Services[j].Service {
			return summary.ChannelReplication.Services[i].Service < summary.ChannelReplication.Services[j].Service
		}
		return summary.ChannelReplication.Services[i].TargetNode < summary.ChannelReplication.Services[j].TargetNode
	})

	return summary, nil
}

type clusterNodeForNetwork struct {
	nodeID          uint64
	name            string
	addr            string
	health          string
	lastHeartbeatAt time.Time
}

func normalizeNetworkServices(in []NetworkRPCService) []NetworkRPCService {
	out := make([]NetworkRPCService, 0, len(in))
	for _, service := range in {
		service = normalizeNetworkServiceIdentity(service)
		out = append(out, service)
	}
	return out
}

func normalizeNetworkServiceIdentity(service NetworkRPCService) NetworkRPCService {
	if service.Service == "" {
		service.Service = networkRPCServiceName(service.ServiceID)
	}
	if service.Group == "" {
		service.Group = networkRPCServiceGroup(service.Service)
	}
	return service
}

func expectedLongPollTimeouts(service NetworkRPCService) int {
	if service.Service != "channel_long_poll_fetch" {
		return 0
	}
	return service.ExpectedTimeout1m
}

func networkServiceIsChannelReplication(service NetworkRPCService) bool {
	return service.Group == "channel_data_plane" || service.Service == "channel_append"
}

func networkRPCServiceName(serviceID uint8) string {
	switch serviceID {
	case 1:
		return "forward"
	case 5:
		return "presence"
	case 6:
		return "delivery_submit"
	case 7:
		return "delivery_push"
	case 8:
		return "delivery_ack"
	case 9:
		return "delivery_offline"
	case 13:
		return "conversation_facts"
	case 14:
		return "controller"
	case 20:
		return "managed_slot"
	case 30:
		return "channel_fetch"
	case 33:
		return "channel_append"
	case 34:
		return "channel_reconcile_probe"
	case 35:
		return "channel_long_poll_fetch"
	case 36:
		return "channel_messages"
	case 37:
		return "channel_leader_repair"
	case 38:
		return "channel_leader_evaluate"
	case 39:
		return "runtime_summary"
	default:
		return "service_" + itoaUint8(serviceID)
	}
}

func networkRPCServiceGroup(service string) string {
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

func itoaUint8(v uint8) string {
	if v == 0 {
		return "0"
	}
	buf := [3]byte{}
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}

func copyNetworkTraffic(in NetworkTraffic) NetworkTraffic {
	in.ByMessageType = append([]NetworkTrafficMessageType(nil), in.ByMessageType...)
	return in
}

func copyNetworkHistory(in NetworkHistory) NetworkHistory {
	in.Traffic = append([]NetworkTrafficHistoryPoint(nil), in.Traffic...)
	in.RPC = append([]NetworkRPCHistoryPoint(nil), in.RPC...)
	in.Errors = append([]NetworkErrorHistoryPoint(nil), in.Errors...)
	return in
}

func copyNetworkDiscovery(in NetworkDiscovery) NetworkDiscovery {
	in.Seeds = append([]string(nil), in.Seeds...)
	in.StaticNodes = append([]NetworkDiscoveryNode(nil), in.StaticNodes...)
	return in
}

func copyNetworkChannelReplication(in NetworkChannelReplication) NetworkChannelReplication {
	in.Services = normalizeNetworkServices(in.Services)
	return in
}

func copyNetworkEvents(in []NetworkEvent) []NetworkEvent {
	return append([]NetworkEvent(nil), in...)
}
