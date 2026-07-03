package management

import (
	"context"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

func TestSetNodeDrainModeDelegatesToWriter(t *testing.T) {
	snap := scaleInReadyNoSlotReplicaSnapshot()
	writer := &gatewayDrainWriterStub{summary: NodeRuntimeSummary{
		NodeID: 4, Draining: true, AcceptingNewSessions: false, GatewaySessions: 2, ActiveOnline: 1, PendingActivations: 1,
	}}
	app := New(Options{Cluster: fakeNodeSnapshotReader{snapshot: snap}, GatewayDrain: writer})

	resp, err := app.SetNodeDrainMode(context.Background(), SetNodeDrainModeRequest{NodeID: 4, Draining: true})
	if err != nil {
		t.Fatalf("SetNodeDrainMode() error = %v", err)
	}
	if writer.nodeID != 4 || !writer.draining {
		t.Fatalf("writer node=%d draining=%v, want node 4 draining", writer.nodeID, writer.draining)
	}
	if !resp.Draining || resp.AcceptingNewSessions || resp.GatewaySessions != 2 || resp.ActiveOnline != 1 || resp.PendingActivations != 1 {
		t.Fatalf("response = %#v, want mapped drain summary", resp)
	}
}

func TestSetNodeDrainModeRejectsInvalidInputAndMissingWriter(t *testing.T) {
	if _, err := New(Options{GatewayDrain: &gatewayDrainWriterStub{}}).SetNodeDrainMode(context.Background(), SetNodeDrainModeRequest{}); !errors.Is(err, metadb.ErrInvalidArgument) {
		t.Fatalf("SetNodeDrainMode(invalid) error = %v, want ErrInvalidArgument", err)
	}
	if _, err := New(Options{}).SetNodeDrainMode(context.Background(), SetNodeDrainModeRequest{NodeID: 4, Draining: true}); !errors.Is(err, ErrNodeScaleInUnavailable) {
		t.Fatalf("SetNodeDrainMode(missing writer) error = %v, want ErrNodeScaleInUnavailable", err)
	}
}

func TestSetNodeDrainModeRequiresLeavingDataNode(t *testing.T) {
	tests := []struct {
		name string
		node control.Node
		want error
	}{
		{
			name: "active data node",
			node: control.Node{NodeID: 4, Roles: []control.Role{control.RoleData}, Status: control.NodeAlive, JoinState: control.NodeJoinStateActive},
			want: ErrNodeLifecycleConflict,
		},
		{
			name: "controller node",
			node: control.Node{NodeID: 4, Roles: []control.Role{control.RoleController}, Status: control.NodeAlive, JoinState: control.NodeJoinStateLeaving},
			want: ErrNodeLifecycleConflict,
		},
		{
			name: "role-less leaving node",
			node: control.Node{NodeID: 4, Status: control.NodeAlive, JoinState: control.NodeJoinStateLeaving},
			want: ErrNodeLifecycleConflict,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			snap := scaleInReadyNoSlotReplicaSnapshot()
			snap.Nodes[3] = tt.node
			writer := &gatewayDrainWriterStub{}
			app := New(Options{Cluster: fakeNodeSnapshotReader{snapshot: snap}, GatewayDrain: writer})

			_, err := app.SetNodeDrainMode(context.Background(), SetNodeDrainModeRequest{NodeID: 4, Draining: true})
			if !errors.Is(err, tt.want) {
				t.Fatalf("SetNodeDrainMode() error = %v, want %v", err, tt.want)
			}
			if writer.nodeID != 0 {
				t.Fatalf("writer node id = %d, want not called", writer.nodeID)
			}
		})
	}
}

func TestSetNodeDrainModeRejectsMissingTargetNode(t *testing.T) {
	writer := &gatewayDrainWriterStub{}
	app := New(Options{Cluster: fakeNodeSnapshotReader{snapshot: scaleInReadyNoSlotReplicaSnapshot()}, GatewayDrain: writer})

	_, err := app.SetNodeDrainMode(context.Background(), SetNodeDrainModeRequest{NodeID: 99, Draining: true})
	if !errors.Is(err, ErrNodeLifecycleNotFound) {
		t.Fatalf("SetNodeDrainMode(missing) error = %v, want %v", err, ErrNodeLifecycleNotFound)
	}
	if writer.nodeID != 0 {
		t.Fatalf("writer node id = %d, want not called", writer.nodeID)
	}
}

type gatewayDrainWriterStub struct {
	nodeID   uint64
	draining bool
	summary  NodeRuntimeSummary
	err      error
}

func (w *gatewayDrainWriterStub) SetNodeDrainMode(_ context.Context, nodeID uint64, draining bool) (NodeRuntimeSummary, error) {
	w.nodeID = nodeID
	w.draining = draining
	return w.summary, w.err
}
