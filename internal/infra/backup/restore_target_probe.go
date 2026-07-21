package backup

import (
	"context"
	"fmt"
	"sort"
	"strings"

	clusterpkg "github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
)

// RestoreTargetClusterNode exposes the local node and Controller membership
// evidence required to prove a successor cluster is semantically empty.
type RestoreTargetClusterNode interface {
	NodeID() uint64
	LocalControlSnapshot(context.Context) (control.Snapshot, error)
	InspectLocalRestoreTarget(context.Context) (clusterpkg.RestoreTargetLocalState, error)
}

// RemoteRestoreTargetClient inspects storage on another current cluster node.
type RemoteRestoreTargetClient interface {
	InspectBackupRestoreTarget(context.Context, uint64) (clusterpkg.RestoreTargetLocalState, error)
}

// ClusterRestoreTargetProbeOptions configures cluster-wide target inspection.
type ClusterRestoreTargetProbeOptions struct {
	Node          RestoreTargetClusterNode
	Remote        RemoteRestoreTargetClient
	ClusterID     string
	Generation    string
	HashSlotCount uint16
}

// ClusterRestoreTargetProbe requires consistent empty evidence from every
// non-removed node in the current Controller membership.
type ClusterRestoreTargetProbe struct {
	node          RestoreTargetClusterNode
	remote        RemoteRestoreTargetClient
	clusterID     string
	generation    string
	hashSlotCount uint16
}

// NewClusterRestoreTargetProbe creates a fail-closed cluster target probe.
func NewClusterRestoreTargetProbe(options ClusterRestoreTargetProbeOptions) (*ClusterRestoreTargetProbe, error) {
	if options.Node == nil || options.Remote == nil || options.Node.NodeID() == 0 || strings.TrimSpace(options.ClusterID) == "" ||
		strings.TrimSpace(options.Generation) == "" || options.HashSlotCount == 0 {
		return nil, fmt.Errorf("backup restore target probe: invalid options")
	}
	return &ClusterRestoreTargetProbe{
		node: options.Node, remote: options.Remote, clusterID: strings.TrimSpace(options.ClusterID),
		generation: strings.TrimSpace(options.Generation), hashSlotCount: options.HashSlotCount,
	}, nil
}

// InspectRestoreTarget checks the current membership and all node-local stores.
func (p *ClusterRestoreTargetProbe) InspectRestoreTarget(ctx context.Context) (RestoreTargetState, error) {
	if p == nil {
		return RestoreTargetState{}, fmt.Errorf("backup restore target probe: probe is required")
	}
	snapshot, err := p.node.LocalControlSnapshot(ctx)
	if err != nil {
		return RestoreTargetState{}, err
	}
	if snapshot.ClusterID != p.clusterID || snapshot.HashSlots.Count != p.hashSlotCount {
		return RestoreTargetState{}, fmt.Errorf("backup restore target probe: Controller identity or hash-slot table mismatch")
	}
	nodeIDs := make([]uint64, 0, len(snapshot.Nodes))
	seen := make(map[uint64]struct{}, len(snapshot.Nodes))
	for _, node := range snapshot.Nodes {
		if node.JoinState == control.NodeJoinStateRemoved {
			continue
		}
		if node.NodeID == 0 {
			return RestoreTargetState{}, fmt.Errorf("backup restore target probe: current node identity is invalid")
		}
		if _, exists := seen[node.NodeID]; exists {
			return RestoreTargetState{}, fmt.Errorf("backup restore target probe: duplicate current node identity")
		}
		seen[node.NodeID] = struct{}{}
		nodeIDs = append(nodeIDs, node.NodeID)
	}
	if len(nodeIDs) == 0 {
		return RestoreTargetState{}, fmt.Errorf("backup restore target probe: current membership is empty")
	}
	if _, exists := seen[p.node.NodeID()]; !exists {
		return RestoreTargetState{}, fmt.Errorf("backup restore target probe: local node is outside current membership")
	}
	sort.Slice(nodeIDs, func(left, right int) bool { return nodeIDs[left] < nodeIDs[right] })
	empty := true
	for _, nodeID := range nodeIDs {
		var local clusterpkg.RestoreTargetLocalState
		if nodeID == p.node.NodeID() {
			local, err = p.node.InspectLocalRestoreTarget(ctx)
		} else {
			local, err = p.remote.InspectBackupRestoreTarget(ctx, nodeID)
		}
		if err != nil {
			return RestoreTargetState{}, fmt.Errorf("backup restore target probe: inspect node %d: %w", nodeID, err)
		}
		if local.NodeID != nodeID || local.Empty != (local.MetadataEmpty && local.MessagesEmpty) {
			return RestoreTargetState{}, fmt.Errorf("backup restore target probe: invalid evidence from node %d", nodeID)
		}
		if !local.Empty {
			empty = false
		}
	}
	return RestoreTargetState{ClusterID: p.clusterID, Generation: p.generation, HashSlotCount: p.hashSlotCount, Empty: empty}, nil
}

var _ RestoreTargetProbe = (*ClusterRestoreTargetProbe)(nil)
