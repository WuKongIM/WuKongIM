package management

import (
	"context"

	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
)

// NodeDetail is the manager-facing node detail DTO.
type NodeDetail struct {
	Node
	// Slots contains hosted slot placement details for the node.
	Slots NodeSlots
}

// NodeSlots contains manager-facing hosted slot identifiers for a node.
type NodeSlots struct {
	// HostedIDs is the ordered list of slots currently hosted by the node.
	HostedIDs []uint32
	// LeaderIDs is the ordered list of slots currently led by the node.
	LeaderIDs []uint32
}

// GetNode returns one manager node detail DTO.
func (a *App) GetNode(ctx context.Context, nodeID uint64) (NodeDetail, error) {
	clusterNodes, slotSummary, controllerLeaderID, err := a.loadNodeSnapshot(ctx)
	if err != nil {
		return NodeDetail{}, err
	}

	for _, clusterNode := range clusterNodes {
		if clusterNode.NodeID != nodeID {
			continue
		}
		return NodeDetail{
			Node: a.managerNode(clusterNode, controllerLeaderID, slotSummary),
			Slots: NodeSlots{
				HostedIDs: append([]uint32(nil), slotSummary.hostedSlotIDsByNode[nodeID]...),
				LeaderIDs: append([]uint32(nil), slotSummary.leaderSlotIDsByNode[nodeID]...),
			},
		}, nil
	}
	return NodeDetail{}, controllermeta.ErrNotFound
}
