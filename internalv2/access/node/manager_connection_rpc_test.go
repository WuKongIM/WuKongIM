package node

import (
	"context"
	"testing"
	"time"

	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
)

func TestManagerConnectionRPCListsConnections(t *testing.T) {
	service := &fakeManagerConnectionService{
		connections: []managementusecase.Connection{{
			NodeID: 2, SessionID: 101, UID: "u1", DeviceID: "d1",
			DeviceFlag: "app", DeviceLevel: "master", SlotID: 9, State: "active",
			Listener: "tcp", ConnectedAt: time.Unix(1713859200, 0).UTC(),
			RemoteAddr: "10.0.0.1:5000", LocalAddr: "127.0.0.1:7000",
		}},
	}
	adapter := New(Options{ManagerConnections: service})
	req := managerConnectionRPCRequest{Op: managerConnectionOpList, NodeID: 2}
	body, err := encodeManagerConnectionRequest(req)
	if err != nil {
		t.Fatalf("encodeManagerConnectionRequest() error = %v", err)
	}

	respBody, err := adapter.HandleManagerConnectionRPC(context.Background(), body)
	if err != nil {
		t.Fatalf("HandleManagerConnectionRPC() error = %v", err)
	}
	resp, err := decodeManagerConnectionResponse(respBody)
	if err != nil {
		t.Fatalf("decodeManagerConnectionResponse() error = %v", err)
	}

	if resp.Status != rpcStatusOK || len(resp.Connections) != 1 || resp.Connections[0].SessionID != 101 {
		t.Fatalf("response = %#v, want one ok connection", resp)
	}
	if service.listReq != (managementusecase.ListConnectionsRequest{NodeID: 2}) {
		t.Fatalf("list request = %#v, want node 2", service.listReq)
	}
}

func TestManagerConnectionRPCClientGetsConnection(t *testing.T) {
	service := &fakeManagerConnectionService{
		detail: managementusecase.ConnectionDetail{NodeID: 2, SessionID: 202, UID: "u2"},
	}
	adapter := New(Options{ManagerConnections: service})
	node := &fakeManagerConnectionRPCNode{handler: adapter.HandleManagerConnectionRPC}
	client := NewClient(node)

	got, err := client.GetManagerConnection(context.Background(), 2, 202)
	if err != nil {
		t.Fatalf("GetManagerConnection() error = %v", err)
	}

	if got != service.detail {
		t.Fatalf("detail = %#v, want %#v", got, service.detail)
	}
	if node.nodeID != 2 || node.serviceID != ManagerConnectionRPCServiceID {
		t.Fatalf("rpc target = node:%d service:%d, want node 2 service %d", node.nodeID, node.serviceID, ManagerConnectionRPCServiceID)
	}
}

type fakeManagerConnectionService struct {
	listReq     managementusecase.ListConnectionsRequest
	detailReq   managementusecase.GetConnectionRequest
	connections []managementusecase.Connection
	detail      managementusecase.ConnectionDetail
}

func (f *fakeManagerConnectionService) ListConnections(_ context.Context, req managementusecase.ListConnectionsRequest) ([]managementusecase.Connection, error) {
	f.listReq = req
	return append([]managementusecase.Connection(nil), f.connections...), nil
}

func (f *fakeManagerConnectionService) GetConnection(_ context.Context, req managementusecase.GetConnectionRequest) (managementusecase.ConnectionDetail, error) {
	f.detailReq = req
	return f.detail, nil
}

type fakeManagerConnectionRPCNode struct {
	handler   func(context.Context, []byte) ([]byte, error)
	nodeID    uint64
	serviceID uint8
}

func (f *fakeManagerConnectionRPCNode) CallRPC(ctx context.Context, nodeID uint64, serviceID uint8, payload []byte) ([]byte, error) {
	f.nodeID = nodeID
	f.serviceID = serviceID
	return f.handler(ctx, payload)
}
