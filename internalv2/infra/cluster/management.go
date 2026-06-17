package cluster

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/control"
)

// ManagementNode exposes clusterv2 control state for manager read models.
type ManagementNode interface {
	// NodeID returns the local cluster node ID.
	NodeID() uint64
	// LocalControlSnapshot returns the latest locally visible control snapshot.
	LocalControlSnapshot(context.Context) (control.Snapshot, error)
}

// ManagementSnapshotReader adapts clusterv2 control state to the management usecase port.
type ManagementSnapshotReader struct {
	node ManagementNode
}

// NewManagementSnapshotReader creates a manager control snapshot reader.
func NewManagementSnapshotReader(node ManagementNode) *ManagementSnapshotReader {
	return &ManagementSnapshotReader{node: node}
}

// NodeID returns the local cluster node ID.
func (r *ManagementSnapshotReader) NodeID() uint64 {
	if r == nil || r.node == nil {
		return 0
	}
	return r.node.NodeID()
}

// LocalControlSnapshot returns the latest locally visible control snapshot.
func (r *ManagementSnapshotReader) LocalControlSnapshot(ctx context.Context) (control.Snapshot, error) {
	if r == nil || r.node == nil {
		return control.Snapshot{}, nil
	}
	return r.node.LocalControlSnapshot(ctx)
}
