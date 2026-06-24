package cluster

import (
	"context"
	"testing"

	accessnode "github.com/WuKongIM/WuKongIM/internalv2/access/node"
	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
)

func TestNodeLifecycleClientForwardsJoinRPC(t *testing.T) {
	service := &fakeNodeLifecycleManager{
		response: managementusecase.JoinNodeResponse{
			Created:   true,
			NodeID:    4,
			Addr:      "10.0.0.4:11110",
			JoinState: "joining",
			Revision:  12,
		},
	}
	adapter := accessnode.New(accessnode.Options{
		NodeLifecycle:          service,
		NodeLifecycleClusterID: "cluster-a",
		NodeLifecycleJoinToken: "join-secret",
	})
	node := &fakeNodeLifecycleNode{handler: adapter.HandleNodeLifecycleRPC}
	client := NewNodeLifecycleClient(node)

	req := accessnode.NodeJoinRequest{
		NodeID:         4,
		AdvertiseAddr:  "10.0.0.4:11110",
		ClusterID:      "cluster-a",
		JoinToken:      "join-secret",
		CapacityWeight: 7,
	}
	got, err := client.JoinNode(context.Background(), 1, req)
	if err != nil {
		t.Fatalf("JoinNode() error = %v", err)
	}

	if got != service.response {
		t.Fatalf("JoinNode() = %#v, want %#v", got, service.response)
	}
	if node.nodeID != 1 || node.serviceID != accessnode.NodeLifecycleRPCServiceID {
		t.Fatalf("rpc target = node:%d service:%d, want node 1 service %d", node.nodeID, node.serviceID, accessnode.NodeLifecycleRPCServiceID)
	}
}

type fakeNodeLifecycleManager struct {
	request  managementusecase.JoinNodeRequest
	response managementusecase.JoinNodeResponse
}

func (f *fakeNodeLifecycleManager) JoinNode(_ context.Context, req managementusecase.JoinNodeRequest) (managementusecase.JoinNodeResponse, error) {
	f.request = req
	return f.response, nil
}

type fakeNodeLifecycleNode struct {
	handler   func(context.Context, []byte) ([]byte, error)
	nodeID    uint64
	serviceID uint8
}

func (f *fakeNodeLifecycleNode) NodeID() uint64 { return 99 }

func (f *fakeNodeLifecycleNode) CallRPC(ctx context.Context, nodeID uint64, serviceID uint8, payload []byte) ([]byte, error) {
	f.nodeID = nodeID
	f.serviceID = serviceID
	return f.handler(ctx, payload)
}
