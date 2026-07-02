package management

import (
	"context"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/control"
)

func TestPlanNodeSlotMoveOutAllowsActiveControllerVoterDataNode(t *testing.T) {
	snap := scaleInSnapshot(17)
	snap.Nodes = append(snap.Nodes, scaleInHealthNode(4, []control.Role{control.RoleData}, control.NodeJoinStateActive, snap.Revision))
	app := New(Options{Cluster: fakeNodeSnapshotReader{snapshot: snap}})

	got, err := app.PlanNodeSlotMoveOut(context.Background(), NodeSlotMoveOutPlanRequest{NodeID: 1, MaxSlotMoves: 1})
	if err != nil {
		t.Fatalf("PlanNodeSlotMoveOut() error = %v", err)
	}

	if got.NodeID != 1 || got.StateRevision != 17 || got.BlockedByStatus {
		t.Fatalf("plan identity = %#v, want source node 1 revision 17 unblocked", got)
	}
	if len(got.Candidates) != 1 {
		t.Fatalf("candidates = %#v, want one bounded move-out candidate", got.Candidates)
	}
	candidate := got.Candidates[0]
	if candidate.SlotID != 1 || candidate.SourceNodeID != 1 || candidate.TargetNodeID != 4 || candidate.ConfigEpoch != 7 {
		t.Fatalf("candidate = %#v, want slot 1 source 1 target 4 epoch 7", candidate)
	}
	if !sameUint64Slice(candidate.TargetPeers, []uint64{4, 2, 3}) {
		t.Fatalf("target peers = %v, want [4 2 3]", candidate.TargetPeers)
	}
}

func TestAdvanceNodeSlotMoveOutCreatesMoveWithoutLeavingState(t *testing.T) {
	snap := scaleInSnapshot(17)
	snap.Nodes = append(snap.Nodes, scaleInHealthNode(4, []control.Role{control.RoleData}, control.NodeJoinStateActive, snap.Revision))
	writer := &fakeSlotReplicaMoveWriter{result: control.SlotReplicaMoveResult{Created: true}}
	app := New(Options{
		Cluster:         fakeNodeSnapshotReader{snapshot: snap},
		SlotReplicaMove: writer,
	})

	got, err := app.AdvanceNodeSlotMoveOut(context.Background(), NodeSlotMoveOutAdvanceRequest{NodeID: 1, MaxSlotMoves: 1})
	if err != nil {
		t.Fatalf("AdvanceNodeSlotMoveOut() error = %v", err)
	}

	if got.Created != 1 || len(writer.requests) != 1 {
		t.Fatalf("advance = %#v requests=%#v, want one created move-out task", got, writer.requests)
	}
	req := writer.requests[0]
	if req.SlotID != 1 || req.SourceNode != 1 || req.TargetNode != 4 || req.StateRevision != 17 || req.ConfigEpoch != 7 {
		t.Fatalf("move request = %#v, want slot 1 source 1 target 4 revision 17 epoch 7", req)
	}
	if !sameUint64Slice(req.TargetPeers, []uint64{4, 2, 3}) {
		t.Fatalf("target peers = %v, want [4 2 3]", req.TargetPeers)
	}
}

func TestAdvanceNodeSlotMoveOutRefreshesOriginalCandidateSlot(t *testing.T) {
	snap := scaleInSnapshot(17)
	snap.Nodes = append(snap.Nodes, scaleInHealthNode(4, []control.Role{control.RoleData}, control.NodeJoinStateActive, snap.Revision))
	snap.Slots = append(snap.Slots, control.SlotAssignment{SlotID: 2, DesiredPeers: []uint64{1, 2, 3}, ConfigEpoch: 8})
	writer := &fakeSlotReplicaMoveWriter{result: control.SlotReplicaMoveResult{Created: true}}
	app := New(Options{
		Cluster:         fakeNodeSnapshotReader{snapshot: snap},
		SlotReplicaMove: writer,
	})

	got, err := app.AdvanceNodeSlotMoveOut(context.Background(), NodeSlotMoveOutAdvanceRequest{NodeID: 1, MaxSlotMoves: 2})
	if err != nil {
		t.Fatalf("AdvanceNodeSlotMoveOut() error = %v", err)
	}

	if got.Created != 2 || len(writer.requests) != 2 {
		t.Fatalf("advance = %#v requests=%#v, want two move tasks", got, writer.requests)
	}
	if writer.requests[0].SlotID != 1 || writer.requests[1].SlotID != 2 || writer.requests[1].ConfigEpoch != 8 {
		t.Fatalf("requests = %#v, want refreshed original candidate slots 1 and 2", writer.requests)
	}
}
