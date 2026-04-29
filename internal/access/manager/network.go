package manager

import (
	"net/http"
	"time"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	"github.com/gin-gonic/gin"
)

// NetworkSummaryResponse is the manager network summary response body.
type NetworkSummaryResponse struct {
	// GeneratedAt is the UTC timestamp when the summary was assembled.
	GeneratedAt time.Time `json:"generated_at"`
	// Scope describes the local-node network summary scope.
	Scope NetworkScopeDTO `json:"scope"`
	// SourceStatus reports source availability for the summary.
	SourceStatus NetworkSourceStatusDTO `json:"source_status"`
	// Headline contains top-level network counters.
	Headline NetworkHeadlineDTO `json:"headline"`
	// Traffic contains local transport traffic counters.
	Traffic NetworkTrafficDTO `json:"traffic"`
	// Peers contains per-remote-node local network observations.
	Peers []NetworkPeerDTO `json:"peers"`
	// Services contains per-RPC-service local observations.
	Services []NetworkRPCServiceDTO `json:"services"`
	// ChannelReplication contains channel data-plane network observations.
	ChannelReplication NetworkChannelReplicationDTO `json:"channel_replication"`
	// Discovery contains network discovery and transport configuration.
	Discovery NetworkDiscoveryDTO `json:"discovery"`
	// Events contains recent local network events.
	Events []NetworkEventDTO `json:"events"`
}

// NetworkScopeDTO describes which node and controller context produced a summary.
type NetworkScopeDTO struct {
	// View is the manager-facing view name.
	View string `json:"view"`
	// LocalNodeID identifies the node that owns the local observations.
	LocalNodeID uint64 `json:"local_node_id"`
	// ControllerLeaderID identifies the observed controller leader when available.
	ControllerLeaderID uint64 `json:"controller_leader_id"`
}

// NetworkSourceStatusDTO reports source availability for the network summary.
type NetworkSourceStatusDTO struct {
	// LocalCollector is ok when the local collector supplied observations.
	LocalCollector string `json:"local_collector"`
	// ControllerContext is ok when controller node context was read successfully.
	ControllerContext string `json:"controller_context"`
	// RuntimeViews is ok when controller runtime views were read successfully.
	RuntimeViews string `json:"runtime_views"`
	// Errors contains source read errors keyed by source name.
	Errors map[string]string `json:"errors"`
}

// NetworkHeadlineDTO contains top-level counters for the manager network page.
type NetworkHeadlineDTO struct {
	// RemotePeers counts remote peers seen by cluster or local observations.
	RemotePeers int `json:"remote_peers"`
	// AliveNodes counts controller nodes in alive status.
	AliveNodes int `json:"alive_nodes"`
	// SuspectNodes counts controller nodes in suspect status.
	SuspectNodes int `json:"suspect_nodes"`
	// DeadNodes counts controller nodes in dead status.
	DeadNodes int `json:"dead_nodes"`
	// DrainingNodes counts controller nodes in draining status.
	DrainingNodes int `json:"draining_nodes"`
	// PoolActive totals active cluster and data-plane pool connections.
	PoolActive int `json:"pool_active"`
	// PoolIdle totals idle cluster and data-plane pool connections.
	PoolIdle int `json:"pool_idle"`
	// RPCInflight totals local outbound RPC calls currently in flight.
	RPCInflight int `json:"rpc_inflight"`
	// DialErrors1m counts recent dial errors.
	DialErrors1m int `json:"dial_errors_1m"`
	// QueueFull1m counts recent queue-full outcomes.
	QueueFull1m int `json:"queue_full_1m"`
	// Timeouts1m counts abnormal recent RPC timeouts.
	Timeouts1m int `json:"timeouts_1m"`
	// StaleObservations counts stale controller runtime views.
	StaleObservations int `json:"stale_observations"`
}

// NetworkTrafficDTO contains local transport traffic counters.
type NetworkTrafficDTO struct {
	// Scope identifies the traffic aggregation scope.
	Scope string `json:"scope"`
	// TXBytes1m counts transmitted bytes in the recent one-minute window.
	TXBytes1m int64 `json:"tx_bytes_1m"`
	// RXBytes1m counts received bytes in the recent one-minute window.
	RXBytes1m int64 `json:"rx_bytes_1m"`
	// TXBps is the current transmit byte rate.
	TXBps float64 `json:"tx_bps"`
	// RXBps is the current receive byte rate.
	RXBps float64 `json:"rx_bps"`
	// PeerBreakdownAvailable reports whether traffic can be attributed to peers.
	PeerBreakdownAvailable bool `json:"peer_breakdown_available"`
	// ByMessageType breaks traffic down by direction and message type.
	ByMessageType []NetworkTrafficMessageTypeDTO `json:"by_message_type"`
}

// NetworkTrafficMessageTypeDTO contains traffic counters for one message type.
type NetworkTrafficMessageTypeDTO struct {
	// Direction is tx or rx.
	Direction string `json:"direction"`
	// MessageType identifies the transport message type.
	MessageType string `json:"message_type"`
	// Bytes1m counts bytes in the recent one-minute window.
	Bytes1m int64 `json:"bytes_1m"`
	// Bps is the current byte rate.
	Bps float64 `json:"bps"`
}

// NetworkPeerDTO contains local network observations for one remote peer.
type NetworkPeerDTO struct {
	// NodeID identifies the remote cluster node.
	NodeID uint64 `json:"node_id"`
	// Name is the controller node name when known.
	Name string `json:"name"`
	// Addr is the controller node RPC address when known.
	Addr string `json:"addr"`
	// Health is the controller health status when known.
	Health string `json:"health"`
	// LastHeartbeatAt is the latest controller heartbeat for this peer.
	LastHeartbeatAt time.Time `json:"last_heartbeat_at"`
	// Pools contains cluster and data-plane pool counters.
	Pools NetworkPeerPoolsDTO `json:"pools"`
	// RPC contains aggregate RPC counters for this peer.
	RPC NetworkPeerRPCDTO `json:"rpc"`
	// Errors contains recent peer error counters.
	Errors NetworkPeerErrorsDTO `json:"errors"`
}

// NetworkPoolStatsDTO contains active and idle pool counters.
type NetworkPoolStatsDTO struct {
	// Active counts active connections in the pool.
	Active int `json:"active"`
	// Idle counts idle connections in the pool.
	Idle int `json:"idle"`
}

// NetworkPeerPoolsDTO separates cluster and data-plane pool counters.
type NetworkPeerPoolsDTO struct {
	// Cluster contains cluster transport pool counters.
	Cluster NetworkPoolStatsDTO `json:"cluster"`
	// DataPlane contains channel data-plane transport pool counters.
	DataPlane NetworkPoolStatsDTO `json:"data_plane"`
}

// NetworkPeerRPCDTO contains aggregate RPC counters for one peer.
type NetworkPeerRPCDTO struct {
	// Inflight counts RPC calls currently in flight.
	Inflight int `json:"inflight"`
	// Calls1m counts completed RPC calls in the recent one-minute window.
	Calls1m int `json:"calls_1m"`
	// P95Ms is the largest observed service p95 latency for the peer.
	P95Ms float64 `json:"p95_ms"`
	// SuccessRate is null when no completed calls are available.
	SuccessRate *float64 `json:"success_rate"`
}

// NetworkPeerErrorsDTO contains recent peer error counters.
type NetworkPeerErrorsDTO struct {
	// DialError1m counts recent dial failures.
	DialError1m int `json:"dial_error_1m"`
	// QueueFull1m counts recent queue-full outcomes.
	QueueFull1m int `json:"queue_full_1m"`
	// Timeout1m excludes expected channel_long_poll_fetch wait timeouts.
	Timeout1m int `json:"timeout_1m"`
	// RemoteError1m counts recent remote RPC errors.
	RemoteError1m int `json:"remote_error_1m"`
}

// NetworkRPCServiceDTO contains local RPC observations for one service and target peer.
type NetworkRPCServiceDTO struct {
	// ServiceID is the transport RPC service identifier.
	ServiceID uint8 `json:"service_id"`
	// Service is the stable service name shown by manager pages.
	Service string `json:"service"`
	// Group is the stable service group shown by manager pages.
	Group string `json:"group"`
	// TargetNode identifies the remote RPC target node.
	TargetNode uint64 `json:"target_node"`
	// Inflight counts RPC calls currently in flight.
	Inflight int `json:"inflight"`
	// Calls1m counts completed calls in the recent one-minute window.
	Calls1m int `json:"calls_1m"`
	// Success1m counts successful completed calls in the recent one-minute window.
	Success1m int `json:"success_1m"`
	// ExpectedTimeout1m counts normal service-level timeouts.
	ExpectedTimeout1m int `json:"expected_timeout_1m"`
	// Timeout1m counts abnormal RPC timeouts.
	Timeout1m int `json:"timeout_1m"`
	// QueueFull1m counts queue-full outcomes.
	QueueFull1m int `json:"queue_full_1m"`
	// RemoteError1m counts remote RPC errors.
	RemoteError1m int `json:"remote_error_1m"`
	// OtherError1m counts other non-success outcomes.
	OtherError1m int `json:"other_error_1m"`
	// P50Ms is the median observed latency in milliseconds.
	P50Ms float64 `json:"p50_ms"`
	// P95Ms is the p95 observed latency in milliseconds.
	P95Ms float64 `json:"p95_ms"`
	// P99Ms is the p99 observed latency in milliseconds.
	P99Ms float64 `json:"p99_ms"`
	// LastSeenAt records the latest completed observation for this service.
	LastSeenAt time.Time `json:"last_seen_at"`
}

// NetworkChannelReplicationDTO contains channel data-plane network observations.
type NetworkChannelReplicationDTO struct {
	// Pool totals channel data-plane pool counters across peers.
	Pool NetworkPoolStatsDTO `json:"pool"`
	// Services contains channel replication RPC service summaries.
	Services []NetworkRPCServiceDTO `json:"services"`
	// LongPoll contains long-poll fetch configuration.
	LongPoll NetworkLongPollConfigDTO `json:"long_poll"`
	// LongPollTimeouts1m counts expected long-poll wait expirations.
	LongPollTimeouts1m int `json:"long_poll_timeouts_1m"`
	// DataPlaneRPCTimeoutMs is the configured data-plane RPC timeout in milliseconds.
	DataPlaneRPCTimeoutMs int64 `json:"data_plane_rpc_timeout_ms"`
}

// NetworkLongPollConfigDTO contains channel long-poll fetch limits.
type NetworkLongPollConfigDTO struct {
	// LaneCount is the number of long-poll lanes.
	LaneCount int `json:"lane_count"`
	// MaxWaitMs is the maximum long-poll wait duration in milliseconds.
	MaxWaitMs int64 `json:"max_wait_ms"`
	// MaxBytes is the maximum bytes returned by one long-poll fetch.
	MaxBytes int `json:"max_bytes"`
	// MaxChannels is the maximum channels included in one long-poll fetch.
	MaxChannels int `json:"max_channels"`
}

// NetworkDiscoveryDTO contains transport discovery and dial configuration.
type NetworkDiscoveryDTO struct {
	// ListenAddr is the local transport listen address.
	ListenAddr string `json:"listen_addr"`
	// AdvertiseAddr is the advertised transport address.
	AdvertiseAddr string `json:"advertise_addr"`
	// Seeds contains configured seed addresses.
	Seeds []string `json:"seeds"`
	// StaticNodes contains statically configured nodes.
	StaticNodes []NetworkDiscoveryNodeDTO `json:"static_nodes"`
	// PoolSize is the cluster transport pool size.
	PoolSize int `json:"pool_size"`
	// DataPlanePoolSize is the channel data-plane transport pool size.
	DataPlanePoolSize int `json:"data_plane_pool_size"`
	// DialTimeoutMs is the configured transport dial timeout in milliseconds.
	DialTimeoutMs int64 `json:"dial_timeout_ms"`
	// ControllerObservationIntervalMs is the configured controller observation interval in milliseconds.
	ControllerObservationIntervalMs int64 `json:"controller_observation_interval_ms"`
}

// NetworkDiscoveryNodeDTO contains one static discovery node.
type NetworkDiscoveryNodeDTO struct {
	// NodeID identifies the static cluster node.
	NodeID uint64 `json:"node_id"`
	// Addr is the configured address for the static node.
	Addr string `json:"addr"`
}

// NetworkEventDTO contains one recent local network event.
type NetworkEventDTO struct {
	// At records when the event happened.
	At time.Time `json:"at"`
	// Severity is info, warn, or error.
	Severity string `json:"severity"`
	// Kind identifies the stable event category.
	Kind string `json:"kind"`
	// TargetNode identifies the remote node involved in the event.
	TargetNode uint64 `json:"target_node"`
	// Service names the RPC service involved in the event when applicable.
	Service string `json:"service"`
	// Message is a short stable manager-facing description.
	Message string `json:"message"`
}

func (s *Server) handleNetworkSummary(c *gin.Context) {
	if s.management == nil {
		jsonError(c, http.StatusServiceUnavailable, "service_unavailable", "management not configured")
		return
	}
	summary, err := s.management.ListNetworkSummary(c.Request.Context())
	if err != nil {
		jsonError(c, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	c.JSON(http.StatusOK, networkSummaryResponse(summary))
}

func networkSummaryResponse(item managementusecase.NetworkSummary) NetworkSummaryResponse {
	return NetworkSummaryResponse{
		GeneratedAt: item.GeneratedAt,
		Scope: NetworkScopeDTO{
			View:               item.Scope.View,
			LocalNodeID:        item.Scope.LocalNodeID,
			ControllerLeaderID: item.Scope.ControllerLeaderID,
		},
		SourceStatus: NetworkSourceStatusDTO{
			LocalCollector:    item.SourceStatus.LocalCollector,
			ControllerContext: item.SourceStatus.ControllerContext,
			RuntimeViews:      item.SourceStatus.RuntimeViews,
			Errors:            stringMapDTO(item.SourceStatus.Errors),
		},
		Headline: NetworkHeadlineDTO{
			RemotePeers:       item.Headline.RemotePeers,
			AliveNodes:        item.Headline.AliveNodes,
			SuspectNodes:      item.Headline.SuspectNodes,
			DeadNodes:         item.Headline.DeadNodes,
			DrainingNodes:     item.Headline.DrainingNodes,
			PoolActive:        item.Headline.PoolActive,
			PoolIdle:          item.Headline.PoolIdle,
			RPCInflight:       item.Headline.RPCInflight,
			DialErrors1m:      item.Headline.DialErrors1m,
			QueueFull1m:       item.Headline.QueueFull1m,
			Timeouts1m:        item.Headline.Timeouts1m,
			StaleObservations: item.Headline.StaleObservations,
		},
		Traffic:            networkTrafficDTO(item.Traffic),
		Peers:              networkPeerDTOs(item.Peers),
		Services:           networkRPCServiceDTOs(item.Services),
		ChannelReplication: networkChannelReplicationDTO(item.ChannelReplication),
		Discovery:          networkDiscoveryDTO(item.Discovery),
		Events:             networkEventDTOs(item.Events),
	}
}

func networkTrafficDTO(item managementusecase.NetworkTraffic) NetworkTrafficDTO {
	return NetworkTrafficDTO{
		Scope:                  item.Scope,
		TXBytes1m:              item.TXBytes1m,
		RXBytes1m:              item.RXBytes1m,
		TXBps:                  item.TXBps,
		RXBps:                  item.RXBps,
		PeerBreakdownAvailable: item.PeerBreakdownAvailable,
		ByMessageType:          networkTrafficMessageTypeDTOs(item.ByMessageType),
	}
}

func networkTrafficMessageTypeDTOs(items []managementusecase.NetworkTrafficMessageType) []NetworkTrafficMessageTypeDTO {
	out := make([]NetworkTrafficMessageTypeDTO, 0, len(items))
	for _, item := range items {
		out = append(out, NetworkTrafficMessageTypeDTO{
			Direction:   item.Direction,
			MessageType: item.MessageType,
			Bytes1m:     item.Bytes1m,
			Bps:         item.Bps,
		})
	}
	return out
}

func networkPeerDTOs(items []managementusecase.NetworkPeer) []NetworkPeerDTO {
	out := make([]NetworkPeerDTO, 0, len(items))
	for _, item := range items {
		out = append(out, NetworkPeerDTO{
			NodeID:          item.NodeID,
			Name:            item.Name,
			Addr:            item.Addr,
			Health:          item.Health,
			LastHeartbeatAt: item.LastHeartbeatAt,
			Pools: NetworkPeerPoolsDTO{
				Cluster:   networkPoolStatsDTO(item.Pools.Cluster),
				DataPlane: networkPoolStatsDTO(item.Pools.DataPlane),
			},
			RPC: NetworkPeerRPCDTO{
				Inflight:    item.RPC.Inflight,
				Calls1m:     item.RPC.Calls1m,
				P95Ms:       item.RPC.P95Ms,
				SuccessRate: item.RPC.SuccessRate,
			},
			Errors: NetworkPeerErrorsDTO{
				DialError1m:   item.Errors.DialError1m,
				QueueFull1m:   item.Errors.QueueFull1m,
				Timeout1m:     item.Errors.Timeout1m,
				RemoteError1m: item.Errors.RemoteError1m,
			},
		})
	}
	return out
}

func networkPoolStatsDTO(item managementusecase.NetworkPoolStats) NetworkPoolStatsDTO {
	return NetworkPoolStatsDTO{Active: item.Active, Idle: item.Idle}
}

func networkRPCServiceDTOs(items []managementusecase.NetworkRPCService) []NetworkRPCServiceDTO {
	out := make([]NetworkRPCServiceDTO, 0, len(items))
	for _, item := range items {
		out = append(out, NetworkRPCServiceDTO{
			ServiceID:         item.ServiceID,
			Service:           item.Service,
			Group:             item.Group,
			TargetNode:        item.TargetNode,
			Inflight:          item.Inflight,
			Calls1m:           item.Calls1m,
			Success1m:         item.Success1m,
			ExpectedTimeout1m: item.ExpectedTimeout1m,
			Timeout1m:         item.Timeout1m,
			QueueFull1m:       item.QueueFull1m,
			RemoteError1m:     item.RemoteError1m,
			OtherError1m:      item.OtherError1m,
			P50Ms:             item.P50Ms,
			P95Ms:             item.P95Ms,
			P99Ms:             item.P99Ms,
			LastSeenAt:        item.LastSeenAt,
		})
	}
	return out
}

func networkChannelReplicationDTO(item managementusecase.NetworkChannelReplication) NetworkChannelReplicationDTO {
	return NetworkChannelReplicationDTO{
		Pool:     networkPoolStatsDTO(item.Pool),
		Services: networkRPCServiceDTOs(item.Services),
		LongPoll: NetworkLongPollConfigDTO{
			LaneCount:   item.LongPollConfig.LaneCount,
			MaxWaitMs:   durationMillis(item.LongPollConfig.MaxWait),
			MaxBytes:    item.LongPollConfig.MaxBytes,
			MaxChannels: item.LongPollConfig.MaxChannels,
		},
		LongPollTimeouts1m:    item.LongPollTimeouts1m,
		DataPlaneRPCTimeoutMs: durationMillis(item.DataPlaneRPCTimeout),
	}
}

func networkDiscoveryDTO(item managementusecase.NetworkDiscovery) NetworkDiscoveryDTO {
	return NetworkDiscoveryDTO{
		ListenAddr:                      item.ListenAddr,
		AdvertiseAddr:                   item.AdvertiseAddr,
		Seeds:                           stringSliceDTO(item.Seeds),
		StaticNodes:                     networkDiscoveryNodeDTOs(item.StaticNodes),
		PoolSize:                        item.PoolSize,
		DataPlanePoolSize:               item.DataPlanePoolSize,
		DialTimeoutMs:                   durationMillis(item.DialTimeout),
		ControllerObservationIntervalMs: durationMillis(item.ControllerObservationInterval),
	}
}

func networkDiscoveryNodeDTOs(items []managementusecase.NetworkDiscoveryNode) []NetworkDiscoveryNodeDTO {
	out := make([]NetworkDiscoveryNodeDTO, 0, len(items))
	for _, item := range items {
		out = append(out, NetworkDiscoveryNodeDTO{NodeID: item.NodeID, Addr: item.Addr})
	}
	return out
}

func networkEventDTOs(items []managementusecase.NetworkEvent) []NetworkEventDTO {
	out := make([]NetworkEventDTO, 0, len(items))
	for _, item := range items {
		out = append(out, NetworkEventDTO{
			At:         item.At,
			Severity:   item.Severity,
			Kind:       item.Kind,
			TargetNode: item.TargetNode,
			Service:    item.Service,
			Message:    item.Message,
		})
	}
	return out
}

func stringSliceDTO(items []string) []string {
	return append([]string{}, items...)
}

func stringMapDTO(items map[string]string) map[string]string {
	out := make(map[string]string, len(items))
	for key, value := range items {
		out[key] = value
	}
	return out
}

func durationMillis(d time.Duration) int64 {
	return int64(d / time.Millisecond)
}
