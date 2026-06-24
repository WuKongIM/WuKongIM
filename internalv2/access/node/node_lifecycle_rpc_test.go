package node

import (
	"context"
	"testing"

	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
)

func TestNodeLifecycleRPCJoinForwardsTokenAndClusterID(t *testing.T) {
	service := &fakeNodeLifecycleService{
		joinResponse: managementusecase.JoinNodeResponse{
			Created:   true,
			NodeID:    4,
			Addr:      "10.0.0.4:11110",
			JoinState: "joining",
			Revision:  12,
		},
	}
	adapter := New(Options{
		NodeLifecycle:          service,
		NodeLifecycleClusterID: "cluster-a",
		NodeLifecycleJoinToken: "join-secret",
	})
	node := &fakeNodeLifecycleRPCNode{handler: adapter.HandleNodeLifecycleRPC}
	client := NewClient(node)

	req := NodeJoinRequest{
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

	if got != service.joinResponse {
		t.Fatalf("JoinNode() = %#v, want %#v", got, service.joinResponse)
	}
	if node.nodeID != 1 || node.serviceID != NodeLifecycleRPCServiceID {
		t.Fatalf("rpc target = node:%d service:%d, want node 1 service %d", node.nodeID, node.serviceID, NodeLifecycleRPCServiceID)
	}
	wireReq, err := decodeNodeLifecycleRequest(node.payload)
	if err != nil {
		t.Fatalf("decodeNodeLifecycleRequest() error = %v", err)
	}
	if wireReq.Op != nodeLifecycleOpJoin || wireReq.Join != req {
		t.Fatalf("wire request = %#v, want join %#v", wireReq, req)
	}
	wantServiceReq := managementusecase.JoinNodeRequest{
		NodeID:         4,
		Addr:           "10.0.0.4:11110",
		CapacityWeight: 7,
	}
	if service.joinRequest != wantServiceReq {
		t.Fatalf("service request = %#v, want %#v", service.joinRequest, wantServiceReq)
	}
}

type fakeNodeLifecycleService struct {
	joinRequest  managementusecase.JoinNodeRequest
	joinResponse managementusecase.JoinNodeResponse
}

func (f *fakeNodeLifecycleService) JoinNode(_ context.Context, req managementusecase.JoinNodeRequest) (managementusecase.JoinNodeResponse, error) {
	f.joinRequest = req
	return f.joinResponse, nil
}

type fakeNodeLifecycleRPCNode struct {
	handler   func(context.Context, []byte) ([]byte, error)
	nodeID    uint64
	serviceID uint8
	payload   []byte
}

func (f *fakeNodeLifecycleRPCNode) CallRPC(ctx context.Context, nodeID uint64, serviceID uint8, payload []byte) ([]byte, error) {
	f.nodeID = nodeID
	f.serviceID = serviceID
	f.payload = append([]byte(nil), payload...)
	return f.handler(ctx, payload)
}
