package cluster

import (
	"context"
	"testing"

	accessnode "github.com/WuKongIM/WuKongIM/internalv2/access/node"
	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
)

func TestManagementConnectionReaderRoutesRemoteList(t *testing.T) {
	service := &fakeManagerConnectionService{
		connections: []managementusecase.Connection{{NodeID: 2, SessionID: 101, UID: "u1"}},
	}
	adapter := accessnode.New(accessnode.Options{ManagerConnections: service})
	node := &fakeManagementConnectionNode{
		nodeID:  1,
		handler: adapter.HandleManagerConnectionRPC,
	}
	reader := NewManagementConnectionReader(node)

	got, err := reader.NodeConnections(context.Background(), 2)
	if err != nil {
		t.Fatalf("NodeConnections() error = %v", err)
	}

	if !sameManagementConnections(got, service.connections) {
		t.Fatalf("connections = %#v, want %#v", got, service.connections)
	}
	if node.calledNodeID != 2 || node.calledServiceID != accessnode.ManagerConnectionRPCServiceID {
		t.Fatalf("rpc target = node:%d service:%d, want node 2 service %d", node.calledNodeID, node.calledServiceID, accessnode.ManagerConnectionRPCServiceID)
	}
}

type fakeManagementConnectionNode struct {
	nodeID          uint64
	calledNodeID    uint64
	calledServiceID uint8
	handler         func(context.Context, []byte) ([]byte, error)
}

func (f *fakeManagementConnectionNode) NodeID() uint64 { return f.nodeID }

func (f *fakeManagementConnectionNode) CallRPC(ctx context.Context, nodeID uint64, serviceID uint8, payload []byte) ([]byte, error) {
	f.calledNodeID = nodeID
	f.calledServiceID = serviceID
	return f.handler(ctx, payload)
}

type fakeManagerConnectionService struct {
	connections []managementusecase.Connection
	detail      managementusecase.ConnectionDetail
}

func (f *fakeManagerConnectionService) ListConnections(context.Context, managementusecase.ListConnectionsRequest) ([]managementusecase.Connection, error) {
	return append([]managementusecase.Connection(nil), f.connections...), nil
}

func (f *fakeManagerConnectionService) GetConnection(context.Context, managementusecase.GetConnectionRequest) (managementusecase.ConnectionDetail, error) {
	return f.detail, nil
}

func sameManagementConnections(left, right []managementusecase.Connection) bool {
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
