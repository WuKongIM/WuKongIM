package cluster

import (
	"context"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
)

func TestManagementSnapshotReaderPreservesLocalNodeAndRevision(t *testing.T) {
	t.Parallel()

	node := &contractManagementNode{nodeID: 7, snapshot: control.Snapshot{ClusterID: "cluster-a", Revision: 19}}
	reader := NewManagementSnapshotReader(node)
	if reader.NodeID() != 7 {
		t.Fatalf("NodeID() = %d, want 7", reader.NodeID())
	}
	snapshot, err := reader.LocalControlSnapshot(context.Background())
	if err != nil || snapshot.ClusterID != "cluster-a" || snapshot.Revision != 19 {
		t.Fatalf("LocalControlSnapshot() = %#v err=%v", snapshot, err)
	}

	var nilReader *ManagementSnapshotReader
	if nilReader.NodeID() != 0 {
		t.Fatalf("nil NodeID() = %d, want 0", nilReader.NodeID())
	}
	if snapshot, err := nilReader.LocalControlSnapshot(context.Background()); err != nil || snapshot.ClusterID != "" || snapshot.Revision != 0 || len(snapshot.Nodes) != 0 {
		t.Fatalf("nil LocalControlSnapshot() = %#v err=%v", snapshot, err)
	}
}

type contractManagementNode struct {
	nodeID   uint64
	snapshot control.Snapshot
}

func (n *contractManagementNode) NodeID() uint64 { return n.nodeID }

func (n *contractManagementNode) LocalControlSnapshot(context.Context) (control.Snapshot, error) {
	return n.snapshot, nil
}
