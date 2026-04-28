package management

import (
	"context"
	"sort"
	"time"

	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
)

// NodeList is the manager-facing node inventory snapshot.
type NodeList struct {
	// GeneratedAt records when this inventory snapshot was built.
	GeneratedAt time.Time
	// ControllerLeaderID is the Controller Raft leader known to this node.
	ControllerLeaderID uint64
	// Items contains ordered node inventory rows.
	Items []Node
}

// Node is the manager-facing node DTO.
type Node struct {
	// NodeID is the node identifier.
	NodeID uint64
	// Name is the operator-facing node name persisted in controller metadata.
	Name string
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
	// Membership contains durable membership role and lifecycle state.
	Membership NodeMembership
	// Health contains observed node health and operator state.
	Health NodeHealth
	// Controller contains Controller Raft role and voter context.
	Controller NodeController
	// Slots contains lightweight Slot placement counts.
	Slots NodeSlotSummary
	// Runtime contains node-local online and gateway counters.
	Runtime NodeRuntimeSummary
	// Actions contains backend business capability hints for UI actions.
	Actions NodeActions
}

// NodeMembership describes durable cluster membership for one node.
type NodeMembership struct {
	// Role is the durable cluster membership role, such as data or controller_voter.
	Role string
	// JoinState is the durable membership lifecycle state.
	JoinState string
	// Schedulable reports whether the planner may place data replicas on this node.
	Schedulable bool
}

// NodeHealth describes observed node health and operator state.
type NodeHealth struct {
	// Status is the manager-facing health or operator state.
	Status string
	// LastHeartbeatAt is the latest controller heartbeat timestamp.
	LastHeartbeatAt time.Time
}

// NodeController describes this node's Controller Raft perspective.
type NodeController struct {
	// Role is leader, follower, or none.
	Role string
	// Voter reports whether this node is a configured Controller voter.
	Voter bool
	// LeaderID is the current Controller Raft leader known locally.
	LeaderID uint64
}

// NodeSlotSummary contains lightweight Slot placement counts for one node.
type NodeSlotSummary struct {
	// ReplicaCount is the number of observed Slot replicas hosted by the node.
	ReplicaCount int
	// LeaderCount is the number of observed Slots led by the node.
	LeaderCount int
	// FollowerCount is the number of observed non-leader Slot replicas hosted by the node.
	FollowerCount int
	// QuorumLostCount is the number of hosted Slots whose runtime view lacks quorum.
	QuorumLostCount int
	// UnreportedCount is the number of expected hosted Slots missing runtime observation.
	UnreportedCount int
}

// NodeActions contains backend business capability hints for UI actions.
type NodeActions struct {
	// CanDrain reports whether the node can be marked draining.
	CanDrain bool
	// CanResume reports whether the node can be resumed from draining.
	CanResume bool
	// CanScaleIn reports whether the data-node scale-in flow can be considered.
	CanScaleIn bool
	// CanOnboard reports whether the node can be considered for explicit resource allocation.
	CanOnboard bool
}

// ListNodes returns the manager node list DTOs ordered by node ID.
func (a *App) ListNodes(ctx context.Context) (NodeList, error) {
	clusterNodes, slotSummary, controllerLeaderID, err := a.loadNodeSnapshot(ctx)
	if err != nil {
		return NodeList{}, err
	}
	nodes := make([]Node, 0, len(clusterNodes))
	for _, clusterNode := range clusterNodes {
		nodes = append(nodes, a.managerNode(ctx, clusterNode, controllerLeaderID, slotSummary))
	}

	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i].NodeID < nodes[j].NodeID
	})
	return NodeList{
		GeneratedAt:        a.now(),
		ControllerLeaderID: controllerLeaderID,
		Items:              nodes,
	}, nil
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

func managerNodeRole(role controllermeta.NodeRole) string {
	switch role {
	case controllermeta.NodeRoleData:
		return "data"
	case controllermeta.NodeRoleControllerVoter:
		return "controller_voter"
	default:
		return "unknown"
	}
}

func managerNodeJoinState(state controllermeta.NodeJoinState) string {
	switch state {
	case controllermeta.NodeJoinStateJoining:
		return "joining"
	case controllermeta.NodeJoinStateActive:
		return "active"
	case controllermeta.NodeJoinStateRejected:
		return "rejected"
	default:
		return "unknown"
	}
}

func managerNodeSchedulable(node controllermeta.ClusterNode) bool {
	return node.Role == controllermeta.NodeRoleData &&
		node.JoinState == controllermeta.NodeJoinStateActive &&
		node.Status == controllermeta.NodeStatusAlive
}

type nodeSlotSummary struct {
	slotCountByNode       map[uint64]int
	leaderSlotCountByNode map[uint64]int
	followerCountByNode   map[uint64]int
	quorumLostByNode      map[uint64]int
	unreportedByNode      map[uint64]int
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
	controllerLeaderID := a.cluster.ControllerLeaderID()
	summary := summarizeNodeSlots(views)
	return clusterNodes, summary, controllerLeaderID, nil
}

func summarizeNodeSlots(views []controllermeta.SlotRuntimeView) nodeSlotSummary {
	summary := nodeSlotSummary{
		slotCountByNode:       make(map[uint64]int),
		leaderSlotCountByNode: make(map[uint64]int),
		followerCountByNode:   make(map[uint64]int),
		quorumLostByNode:      make(map[uint64]int),
		unreportedByNode:      make(map[uint64]int),
		hostedSlotIDsByNode:   make(map[uint64][]uint32),
		leaderSlotIDsByNode:   make(map[uint64][]uint32),
	}
	for _, view := range views {
		for _, peer := range view.CurrentPeers {
			summary.slotCountByNode[peer]++
			summary.hostedSlotIDsByNode[peer] = append(summary.hostedSlotIDsByNode[peer], view.SlotID)
			if !view.HasQuorum {
				summary.quorumLostByNode[peer]++
			}
			if view.LeaderID != peer {
				summary.followerCountByNode[peer]++
			}
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

func (a *App) isControllerPeer(nodeID uint64) bool {
	if a == nil {
		return false
	}
	_, ok := a.controllerPeerIDs[nodeID]
	return ok
}

func (a *App) managerNode(ctx context.Context, clusterNode controllermeta.ClusterNode, controllerLeaderID uint64, slotSummary nodeSlotSummary) Node {
	controllerRole := a.controllerRole(clusterNode.NodeID, controllerLeaderID)
	healthStatus := managerNodeStatus(clusterNode.Status)
	slotCount := slotSummary.slotCountByNode[clusterNode.NodeID]
	leaderSlotCount := slotSummary.leaderSlotCountByNode[clusterNode.NodeID]
	followerSlotCount := slotSummary.followerCountByNode[clusterNode.NodeID]
	if followerSlotCount == 0 && slotCount > leaderSlotCount {
		followerSlotCount = slotCount - leaderSlotCount
	}
	if followerSlotCount < 0 {
		followerSlotCount = 0
	}
	runtime := a.nodeRuntimeSummary(ctx, clusterNode.NodeID)
	return Node{
		NodeID:          clusterNode.NodeID,
		Name:            clusterNode.Name,
		Addr:            clusterNode.Addr,
		Status:          healthStatus,
		LastHeartbeatAt: clusterNode.LastHeartbeatAt,
		ControllerRole:  controllerRole,
		SlotCount:       slotCount,
		LeaderSlotCount: leaderSlotCount,
		IsLocal:         clusterNode.NodeID == a.localNodeID,
		CapacityWeight:  clusterNode.CapacityWeight,
		Membership: NodeMembership{
			Role:        managerNodeRole(clusterNode.Role),
			JoinState:   managerNodeJoinState(clusterNode.JoinState),
			Schedulable: managerNodeSchedulable(clusterNode),
		},
		Health: NodeHealth{
			Status:          healthStatus,
			LastHeartbeatAt: clusterNode.LastHeartbeatAt,
		},
		Controller: NodeController{
			Role:     controllerRole,
			Voter:    a.isControllerPeer(clusterNode.NodeID),
			LeaderID: controllerLeaderID,
		},
		Slots: NodeSlotSummary{
			ReplicaCount:    slotCount,
			LeaderCount:     leaderSlotCount,
			FollowerCount:   followerSlotCount,
			QuorumLostCount: slotSummary.quorumLostByNode[clusterNode.NodeID],
			UnreportedCount: slotSummary.unreportedByNode[clusterNode.NodeID],
		},
		Runtime: runtime,
		Actions: NodeActions{
			CanDrain:   clusterNode.Status != controllermeta.NodeStatusDraining && clusterNode.Status != controllermeta.NodeStatusDead,
			CanResume:  clusterNode.Status == controllermeta.NodeStatusDraining,
			CanScaleIn: clusterNode.Role == controllermeta.NodeRoleData && clusterNode.JoinState == controllermeta.NodeJoinStateActive && !a.isControllerPeer(clusterNode.NodeID),
			CanOnboard: clusterNode.Role == controllermeta.NodeRoleData && clusterNode.JoinState == controllermeta.NodeJoinStateActive && clusterNode.Status == controllermeta.NodeStatusAlive && slotCount == 0,
		},
	}
}

func (a *App) nodeRuntimeSummary(ctx context.Context, nodeID uint64) NodeRuntimeSummary {
	if a == nil || a.runtimeSummary == nil {
		return NodeRuntimeSummary{NodeID: nodeID, Unknown: true}
	}
	summary, err := a.runtimeSummary.NodeRuntimeSummary(ctx, nodeID)
	if err != nil {
		return NodeRuntimeSummary{NodeID: nodeID, Unknown: true}
	}
	if summary.NodeID == 0 {
		summary.NodeID = nodeID
	}
	if summary.SessionsByListener == nil {
		summary.SessionsByListener = map[string]int{}
	}
	return summary
}
