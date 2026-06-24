package cluster

import (
	"context"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/clusterv2"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/control"
)

var _ ManagementNodeLifecycleNode = (*clusterv2.Node)(nil)

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
}

type fakeManagementNodeLifecycleNode struct {
	joinRequest     control.JoinNodeRequest
	joinResult      control.JoinNodeResult
	activateRequest control.ActivateNodeRequest
	activateResult  control.ActivateNodeResult
}

func (f *fakeManagementNodeLifecycleNode) JoinNode(_ context.Context, req control.JoinNodeRequest) (control.JoinNodeResult, error) {
	f.joinRequest = req
	return f.joinResult, nil
}

func (f *fakeManagementNodeLifecycleNode) ActivateNode(_ context.Context, req control.ActivateNodeRequest) (control.ActivateNodeResult, error) {
	f.activateRequest = req
	return f.activateResult, nil
}
