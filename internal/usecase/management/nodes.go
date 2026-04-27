package management

import (
	"context"
	"sort"
	"time"

	raftcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
)

// Node is the manager-facing node DTO.
type Node struct {
	// NodeID is the node identifier.
	NodeID uint64
	// Addr is the cluster listen address of the node.
	Addr string
	// Status is the manager-facing node status string.
	Status string
	// LastHeartbeatAt is the latest controller heartbeat timestamp.
	LastHeartbeatAt time.Time
	// ControllerRole is the controller role summary for the node.
	ControllerRole string
	// SlotCount is the number of observed slot peers hosted by the node.
	SlotCount int
	// LeaderSlotCount is the number of observed slots led by the node.
	LeaderSlotCount int
	// IsLocal reports whether the node is the current process node.
	IsLocal bool
	// CapacityWeight is the configured controller capacity weight.
	CapacityWeight int
	// DistributedLog contains manager-facing Raft log health summaries for this node.
	DistributedLog NodeDistributedLog
}

// NodeDistributedLog contains controller and Slot Raft log health summaries for one node.
type NodeDistributedLog struct {
	// Controller contains controller Raft placement and leadership context.
	Controller NodeControllerLog
	// Slots contains Slot Raft log health aggregated for Slots hosted by the node.
	Slots NodeSlotLogHealth
}

// NodeControllerLog describes this node's controller Raft membership perspective.
type NodeControllerLog struct {
	// Role is the current controller role summary for the node.
	Role string
	// LeaderID is the current controller Raft leader known locally.
	LeaderID uint64
	// Voter reports whether this node is configured as a controller voter.
	Voter bool
}

// NodeSlotLogHealth aggregates distributed Slot Raft log health for one node.
type NodeSlotLogHealth struct {
	// ReplicaCount is the number of observed Slot Raft replicas hosted by the node.
	ReplicaCount int
	// LeaderCount is the number of observed Slot Raft leaders hosted by the node.
	LeaderCount int
	// FollowerCount is the number of observed Slot Raft followers hosted by the node.
	FollowerCount int
	// MaxCommitLag is the largest gap between a Slot leader commit index and this node's commit index.
	MaxCommitLag uint64
	// MaxApplyGap is the largest gap between this node's commit index and applied index.
	MaxApplyGap uint64
	// UnavailableCount is the number of hosted Slots whose local log watermark could not be read.
	UnavailableCount int
	// UnhealthyCount is the number of hosted Slots that are unavailable, quorum-lost, or lagging.
	UnhealthyCount int
	// Samples contains ordered Slot log samples that explain non-healthy state.
	Samples []NodeSlotLogSample
}

// NodeSlotLogSample is a sampled per-Slot Raft log health row for node details and list drilldown.
type NodeSlotLogSample struct {
	// SlotID is the physical Slot identity for the sampled log row.
	SlotID uint32
	// Role is this node's role in the Slot Raft group.
	Role string
	// LeaderID is the Slot Raft leader reported by the runtime view.
	LeaderID uint64
	// CommitIndex is this node's local committed Raft log index for the Slot.
	CommitIndex uint64
	// AppliedIndex is this node's local applied Raft log index for the Slot.
	AppliedIndex uint64
	// LeaderCommitIndex is the Slot leader's committed Raft log index when available.
	LeaderCommitIndex uint64
	// CommitLag is LeaderCommitIndex minus CommitIndex when both are known.
	CommitLag uint64
	// ApplyGap is CommitIndex minus AppliedIndex when both are known.
	ApplyGap uint64
	// Quorum is a manager-facing quorum summary: healthy or lost.
	Quorum string
	// Status is a manager-facing log health summary for this Slot sample.
	Status string
}

// ListNodes returns the manager node list DTOs ordered by node ID.
func (a *App) ListNodes(ctx context.Context) ([]Node, error) {
	clusterNodes, slotSummary, controllerLeaderID, err := a.loadNodeSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	nodes := make([]Node, 0, len(clusterNodes))
	for _, clusterNode := range clusterNodes {
		nodes = append(nodes, a.managerNode(clusterNode, controllerLeaderID, slotSummary))
	}

	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i].NodeID < nodes[j].NodeID
	})
	return nodes, nil
}

func (a *App) controllerRole(nodeID, controllerLeaderID uint64) string {
	if nodeID != 0 && nodeID == controllerLeaderID {
		return "leader"
	}
	if _, ok := a.controllerPeerIDs[nodeID]; ok {
		return "follower"
	}
	return "none"
}

func managerNodeStatus(status controllermeta.NodeStatus) string {
	switch status {
	case controllermeta.NodeStatusAlive:
		return "alive"
	case controllermeta.NodeStatusSuspect:
		return "suspect"
	case controllermeta.NodeStatusDead:
		return "dead"
	case controllermeta.NodeStatusDraining:
		return "draining"
	default:
		return "unknown"
	}
}

type nodeSlotSummary struct {
	slotCountByNode       map[uint64]int
	leaderSlotCountByNode map[uint64]int
	hostedSlotIDsByNode   map[uint64][]uint32
	leaderSlotIDsByNode   map[uint64][]uint32
	distributedLogByNode  map[uint64]NodeDistributedLog
}

func (a *App) loadNodeSnapshot(ctx context.Context) ([]controllermeta.ClusterNode, nodeSlotSummary, uint64, error) {
	if a == nil || a.cluster == nil {
		return nil, nodeSlotSummary{}, 0, nil
	}

	clusterNodes, err := a.cluster.ListNodesStrict(ctx)
	if err != nil {
		return nil, nodeSlotSummary{}, 0, err
	}
	views, err := a.cluster.ListObservedRuntimeViewsStrict(ctx)
	if err != nil {
		return nil, nodeSlotSummary{}, 0, err
	}
	controllerLeaderID := a.cluster.ControllerLeaderID()
	summary := summarizeNodeSlots(views)
	summary.distributedLogByNode = a.summarizeDistributedLog(ctx, views, controllerLeaderID)
	return clusterNodes, summary, controllerLeaderID, nil
}

func summarizeNodeSlots(views []controllermeta.SlotRuntimeView) nodeSlotSummary {
	summary := nodeSlotSummary{
		slotCountByNode:       make(map[uint64]int),
		leaderSlotCountByNode: make(map[uint64]int),
		hostedSlotIDsByNode:   make(map[uint64][]uint32),
		leaderSlotIDsByNode:   make(map[uint64][]uint32),
		distributedLogByNode:  make(map[uint64]NodeDistributedLog),
	}
	for _, view := range views {
		for _, peer := range view.CurrentPeers {
			summary.slotCountByNode[peer]++
			summary.hostedSlotIDsByNode[peer] = append(summary.hostedSlotIDsByNode[peer], view.SlotID)
		}
		if view.LeaderID != 0 {
			summary.leaderSlotCountByNode[view.LeaderID]++
			summary.leaderSlotIDsByNode[view.LeaderID] = append(summary.leaderSlotIDsByNode[view.LeaderID], view.SlotID)
		}
	}
	for nodeID := range summary.hostedSlotIDsByNode {
		sort.Slice(summary.hostedSlotIDsByNode[nodeID], func(i, j int) bool {
			return summary.hostedSlotIDsByNode[nodeID][i] < summary.hostedSlotIDsByNode[nodeID][j]
		})
	}
	for nodeID := range summary.leaderSlotIDsByNode {
		sort.Slice(summary.leaderSlotIDsByNode[nodeID], func(i, j int) bool {
			return summary.leaderSlotIDsByNode[nodeID][i] < summary.leaderSlotIDsByNode[nodeID][j]
		})
	}
	return summary
}

const maxNodeSlotLogSamples = 8

type nodeSlotLogStatusKey struct {
	nodeID uint64
	slotID uint32
}

type nodeSlotLogStatusResult struct {
	status raftcluster.SlotLogStatus
	err    error
}

type slotLogStatusReader interface {
	SlotLogStatusOnNode(ctx context.Context, nodeID uint64, slotID uint32) (raftcluster.SlotLogStatus, error)
}

func (a *App) summarizeDistributedLog(ctx context.Context, views []controllermeta.SlotRuntimeView, controllerLeaderID uint64) map[uint64]NodeDistributedLog {
	out := make(map[uint64]NodeDistributedLog)
	if a == nil || a.cluster == nil {
		return out
	}
	statuses := a.loadSlotLogStatuses(ctx, views)
	for _, view := range views {
		for _, nodeID := range view.CurrentPeers {
			log := out[nodeID]
			log.Controller = NodeControllerLog{
				Role:     a.controllerRole(nodeID, controllerLeaderID),
				LeaderID: controllerLeaderID,
				Voter:    a.isControllerPeer(nodeID),
			}
			log.Slots.ReplicaCount++
			if view.LeaderID == nodeID {
				log.Slots.LeaderCount++
			} else {
				log.Slots.FollowerCount++
			}

			sample := buildNodeSlotLogSample(nodeID, view, statuses)
			if sample.Status == "unavailable" {
				log.Slots.UnavailableCount++
			}
			if sample.Status != "healthy" {
				log.Slots.UnhealthyCount++
				log.Slots.Samples = append(log.Slots.Samples, sample)
			}
			if sample.CommitLag > log.Slots.MaxCommitLag {
				log.Slots.MaxCommitLag = sample.CommitLag
			}
			if sample.ApplyGap > log.Slots.MaxApplyGap {
				log.Slots.MaxApplyGap = sample.ApplyGap
			}
			out[nodeID] = log
		}
	}
	for nodeID, log := range out {
		sortNodeSlotLogSamples(log.Slots.Samples)
		if len(log.Slots.Samples) > maxNodeSlotLogSamples {
			log.Slots.Samples = append([]NodeSlotLogSample(nil), log.Slots.Samples[:maxNodeSlotLogSamples]...)
		}
		out[nodeID] = log
	}
	return out
}

func (a *App) loadSlotLogStatuses(ctx context.Context, views []controllermeta.SlotRuntimeView) map[nodeSlotLogStatusKey]nodeSlotLogStatusResult {
	out := make(map[nodeSlotLogStatusKey]nodeSlotLogStatusResult)
	reader, ok := a.cluster.(slotLogStatusReader)
	if !ok {
		return out
	}
	for _, view := range views {
		nodeIDs := append([]uint64(nil), view.CurrentPeers...)
		if view.LeaderID != 0 {
			nodeIDs = append(nodeIDs, view.LeaderID)
		}
		for _, nodeID := range nodeIDs {
			key := nodeSlotLogStatusKey{nodeID: nodeID, slotID: view.SlotID}
			if _, exists := out[key]; exists {
				continue
			}
			status, err := reader.SlotLogStatusOnNode(ctx, nodeID, view.SlotID)
			out[key] = nodeSlotLogStatusResult{status: status, err: err}
		}
	}
	return out
}

func buildNodeSlotLogSample(nodeID uint64, view controllermeta.SlotRuntimeView, statuses map[nodeSlotLogStatusKey]nodeSlotLogStatusResult) NodeSlotLogSample {
	role := "follower"
	if view.LeaderID == nodeID {
		role = "leader"
	} else if view.LeaderID == 0 {
		role = "unknown"
	}
	sample := NodeSlotLogSample{
		SlotID:   view.SlotID,
		Role:     role,
		LeaderID: view.LeaderID,
		Quorum:   "healthy",
		Status:   "healthy",
	}
	if !view.HasQuorum {
		sample.Quorum = "lost"
	}

	local, ok := statuses[nodeSlotLogStatusKey{nodeID: nodeID, slotID: view.SlotID}]
	if !ok || local.err != nil {
		sample.Status = "unavailable"
		return sample
	}
	sample.CommitIndex = local.status.CommitIndex
	sample.AppliedIndex = local.status.AppliedIndex
	sample.ApplyGap = subtractUint64(local.status.CommitIndex, local.status.AppliedIndex)

	if view.LeaderID != 0 {
		leader, ok := statuses[nodeSlotLogStatusKey{nodeID: view.LeaderID, slotID: view.SlotID}]
		if ok && leader.err == nil {
			sample.LeaderCommitIndex = leader.status.CommitIndex
			sample.CommitLag = subtractUint64(leader.status.CommitIndex, local.status.CommitIndex)
		}
	}
	switch {
	case sample.Quorum == "lost":
		sample.Status = "quorum_lost"
	case sample.CommitLag > 0 || sample.ApplyGap > 0:
		sample.Status = "lagging"
	default:
		sample.Status = "healthy"
	}
	return sample
}

func sortNodeSlotLogSamples(samples []NodeSlotLogSample) {
	sort.Slice(samples, func(i, j int) bool {
		if samples[i].Status != samples[j].Status {
			return nodeSlotLogStatusRank(samples[i].Status) < nodeSlotLogStatusRank(samples[j].Status)
		}
		if samples[i].CommitLag != samples[j].CommitLag {
			return samples[i].CommitLag > samples[j].CommitLag
		}
		if samples[i].ApplyGap != samples[j].ApplyGap {
			return samples[i].ApplyGap > samples[j].ApplyGap
		}
		return samples[i].SlotID < samples[j].SlotID
	})
}

func nodeSlotLogStatusRank(status string) int {
	switch status {
	case "unavailable":
		return 0
	case "quorum_lost":
		return 1
	case "lagging":
		return 2
	default:
		return 3
	}
}

func subtractUint64(left, right uint64) uint64 {
	if left <= right {
		return 0
	}
	return left - right
}

func (a *App) isControllerPeer(nodeID uint64) bool {
	if a == nil {
		return false
	}
	_, ok := a.controllerPeerIDs[nodeID]
	return ok
}

func (a *App) managerNode(clusterNode controllermeta.ClusterNode, controllerLeaderID uint64, slotSummary nodeSlotSummary) Node {
	distributedLog := slotSummary.distributedLogByNode[clusterNode.NodeID]
	if distributedLog.Controller.Role == "" {
		distributedLog.Controller = NodeControllerLog{
			Role:     a.controllerRole(clusterNode.NodeID, controllerLeaderID),
			LeaderID: controllerLeaderID,
			Voter:    a.isControllerPeer(clusterNode.NodeID),
		}
	}
	return Node{
		NodeID:          clusterNode.NodeID,
		Addr:            clusterNode.Addr,
		Status:          managerNodeStatus(clusterNode.Status),
		LastHeartbeatAt: clusterNode.LastHeartbeatAt,
		ControllerRole:  a.controllerRole(clusterNode.NodeID, controllerLeaderID),
		SlotCount:       slotSummary.slotCountByNode[clusterNode.NodeID],
		LeaderSlotCount: slotSummary.leaderSlotCountByNode[clusterNode.NodeID],
		IsLocal:         clusterNode.NodeID == a.localNodeID,
		CapacityWeight:  clusterNode.CapacityWeight,
		DistributedLog:  distributedLog,
	}
}
