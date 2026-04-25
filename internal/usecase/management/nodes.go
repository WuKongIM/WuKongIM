package management

import (
	"context"
	"sort"
	"time"

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
	return clusterNodes, summarizeNodeSlots(views), a.cluster.ControllerLeaderID(), nil
}

func summarizeNodeSlots(views []controllermeta.SlotRuntimeView) nodeSlotSummary {
	summary := nodeSlotSummary{
		slotCountByNode:       make(map[uint64]int),
		leaderSlotCountByNode: make(map[uint64]int),
		hostedSlotIDsByNode:   make(map[uint64][]uint32),
		leaderSlotIDsByNode:   make(map[uint64][]uint32),
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

func (a *App) managerNode(clusterNode controllermeta.ClusterNode, controllerLeaderID uint64, slotSummary nodeSlotSummary) Node {
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
	}
}
