package cluster

import (
	"context"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
)

var _ ManagementNodeLifecycleNode = (*cluster.Node)(nil)

func TestManagementNodeLifecycleAdapterUsesControlWriter(t *testing.T) {
	node := &fakeManagementNodeLifecycleNode{
		joinResult: control.JoinNodeResult{
			Created: true,
			Node:    control.Node{NodeID: 4, Addr: "10.0.0.4:11110", JoinState: control.NodeJoinStateJoining},
		},
		activateResult: control.ActivateNodeResult{
			Changed: true,
			Node:    control.Node{NodeID: 4, Addr: "10.0.0.4:11110", JoinState: control.NodeJoinStateActive},
		},
		leavingResult: control.MarkNodeLeavingResult{
			Changed: true,
			Node:    control.Node{NodeID: 4, Addr: "10.0.0.4:11110", JoinState: control.NodeJoinStateLeaving},
		},
		removedResult: control.MarkNodeRemovedResult{
			Changed: true,
			Node:    control.Node{NodeID: 4, Addr: "10.0.0.4:11110", JoinState: control.NodeJoinStateRemoved},
		},
	}
	adapter := NewManagementNodeLifecycleAdapter(node)

	join, err := adapter.JoinNode(context.Background(), control.JoinNodeRequest{NodeID: 4, Addr: "10.0.0.4:11110"})
	if err != nil {
		t.Fatalf("JoinNode() error = %v", err)
	}
	if !join.Created || node.joinRequest.NodeID != 4 {
		t.Fatalf("JoinNode() = %#v request=%#v, want created node 4", join, node.joinRequest)
	}

	activate, err := adapter.ActivateNode(context.Background(), control.ActivateNodeRequest{NodeID: 4})
	if err != nil {
		t.Fatalf("ActivateNode() error = %v", err)
	}
	if !activate.Changed || node.activateRequest.NodeID != 4 {
		t.Fatalf("ActivateNode() = %#v request=%#v, want changed node 4", activate, node.activateRequest)
	}

	leaving, err := adapter.MarkNodeLeaving(context.Background(), control.MarkNodeLeavingRequest{NodeID: 4})
	if err != nil {
		t.Fatalf("MarkNodeLeaving() error = %v", err)
	}
	if !leaving.Changed || node.leavingRequest.NodeID != 4 || leaving.Node.JoinState != control.NodeJoinStateLeaving {
		t.Fatalf("MarkNodeLeaving() = %#v request=%#v, want changed leaving node 4", leaving, node.leavingRequest)
	}

	removed, err := adapter.MarkNodeRemoved(context.Background(), control.MarkNodeRemovedRequest{NodeID: 4})
	if err != nil {
		t.Fatalf("MarkNodeRemoved() error = %v", err)
	}
	if !removed.Changed || node.removedRequest.NodeID != 4 || removed.Node.JoinState != control.NodeJoinStateRemoved {
		t.Fatalf("MarkNodeRemoved() = %#v request=%#v, want changed removed node 4", removed, node.removedRequest)
	}

	promote, err := adapter.PromoteControllerVoter(context.Background(), control.PromoteControllerVoterRequest{
		NodeID:              4,
		ExpectedRevision:    9,
		ExpectedVoters:      []uint64{1, 2},
		ObservedConfigIndex: 77,
		ObservedVoters:      []uint64{1, 2, 4},
	})
	if err != nil {
		t.Fatalf("PromoteControllerVoter() error = %v", err)
	}
	if !promote.Changed ||
		node.promoteRequest.NodeID != 4 ||
		node.promoteRequest.ExpectedRevision != 9 ||
		!sameUint64s(node.promoteRequest.ExpectedVoters, []uint64{1, 2}) ||
		node.promoteRequest.ObservedConfigIndex != 77 ||
		!sameUint64s(node.promoteRequest.ObservedVoters, []uint64{1, 2, 4}) {
		t.Fatalf("PromoteControllerVoter() = %#v request=%#v, want proof request passed through", promote, node.promoteRequest)
	}
}

type fakeManagementNodeLifecycleNode struct {
	joinRequest     control.JoinNodeRequest
	joinResult      control.JoinNodeResult
	activateRequest control.ActivateNodeRequest
	activateResult  control.ActivateNodeResult
	leavingRequest  control.MarkNodeLeavingRequest
	leavingResult   control.MarkNodeLeavingResult
	removedRequest  control.MarkNodeRemovedRequest
	removedResult   control.MarkNodeRemovedResult
	promoteRequest  control.PromoteControllerVoterRequest
	promoteResult   control.PromoteControllerVoterResult
}

func (f *fakeManagementNodeLifecycleNode) JoinNode(_ context.Context, req control.JoinNodeRequest) (control.JoinNodeResult, error) {
	f.joinRequest = req
	return f.joinResult, nil
}

func (f *fakeManagementNodeLifecycleNode) ActivateNode(_ context.Context, req control.ActivateNodeRequest) (control.ActivateNodeResult, error) {
	f.activateRequest = req
	return f.activateResult, nil
}

func (f *fakeManagementNodeLifecycleNode) MarkNodeLeaving(_ context.Context, req control.MarkNodeLeavingRequest) (control.MarkNodeLeavingResult, error) {
	f.leavingRequest = req
	return f.leavingResult, nil
}

func (f *fakeManagementNodeLifecycleNode) MarkNodeRemoved(_ context.Context, req control.MarkNodeRemovedRequest) (control.MarkNodeRemovedResult, error) {
	f.removedRequest = req
	return f.removedResult, nil
}

func (f *fakeManagementNodeLifecycleNode) PromoteControllerVoter(_ context.Context, req control.PromoteControllerVoterRequest) (control.PromoteControllerVoterResult, error) {
	f.promoteRequest = req
	if f.promoteResult.Node.NodeID == 0 {
		f.promoteResult = control.PromoteControllerVoterResult{Changed: true, Node: control.Node{NodeID: req.NodeID}, Revision: req.ExpectedRevision + 1}
	}
	return f.promoteResult, nil
}

func sameUint64s(left, right []uint64) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
