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

	got, err := reader.NodeConnections(context.Background(), 2, 100)
	if err != nil {
		t.Fatalf("NodeConnections() error = %v", err)
	}

	if !sameManagementConnections(got, service.connections) {
		t.Fatalf("connections = %#v, want %#v", got, service.connections)
	}
	if service.listReq != (managementusecase.ListConnectionsRequest{NodeID: 2, Limit: 100}) {
		t.Fatalf("list request = %#v, want node 2 limit 100", service.listReq)
	}
	if node.calledNodeID != 2 || node.calledServiceID != accessnode.ManagerConnectionRPCServiceID {
		t.Fatalf("rpc target = node:%d service:%d, want node 2 service %d", node.calledNodeID, node.calledServiceID, accessnode.ManagerConnectionRPCServiceID)
	}
}

func TestManagementConnectionReaderRoutesRuntimeSummary(t *testing.T) {
	service := &fakeManagerConnectionService{
		runtime: managementusecase.NodeRuntimeSummary{
			NodeID:               2,
			ActiveOnline:         3,
			GatewaySessions:      4,
			PendingActivations:   1,
			SessionsByListener:   map[string]int{"tcp": 4},
			AcceptingNewSessions: true,
		},
	}
	adapter := accessnode.New(accessnode.Options{ManagerConnections: service})
	node := &fakeManagementConnectionNode{
		nodeID:  1,
		handler: adapter.HandleManagerConnectionRPC,
	}
	reader := NewManagementConnectionReader(node)

	got, err := reader.NodeRuntimeSummary(context.Background(), 2)
	if err != nil {
		t.Fatalf("NodeRuntimeSummary() error = %v", err)
	}

	if got.NodeID != 2 || got.ActiveOnline != 3 || got.GatewaySessions != 4 ||
		got.PendingActivations != 1 || got.SessionsByListener["tcp"] != 4 || !got.AcceptingNewSessions || got.Unknown {
		t.Fatalf("runtime summary = %#v, want concrete summary", got)
	}
	if node.calledNodeID != 2 || node.calledServiceID != accessnode.ManagerConnectionRPCServiceID {
		t.Fatalf("rpc target = node:%d service:%d, want node 2 service %d", node.calledNodeID, node.calledServiceID, accessnode.ManagerConnectionRPCServiceID)
	}
}

func TestManagementConnectionReaderRoutesDrainMode(t *testing.T) {
	service := &fakeManagerConnectionService{
		runtime: managementusecase.NodeRuntimeSummary{
			NodeID: 4, Draining: true, AcceptingNewSessions: false, PendingActivations: 2,
		},
	}
	adapter := accessnode.New(accessnode.Options{ManagerConnections: service})
	node := &fakeManagementConnectionNode{
		nodeID:  1,
		handler: adapter.HandleManagerConnectionRPC,
	}
	reader := NewManagementConnectionReader(node)

	got, err := reader.SetNodeDrainMode(context.Background(), 4, true)
	if err != nil {
		t.Fatalf("SetNodeDrainMode() error = %v", err)
	}

	if service.drainReq != (managementusecase.SetNodeDrainModeRequest{NodeID: 4, Draining: true}) ||
		!got.Draining || got.PendingActivations != 2 {
		t.Fatalf("service=%#v summary=%#v, want remote drain summary", service, got)
	}
	if node.calledNodeID != 4 || node.calledServiceID != accessnode.ManagerConnectionRPCServiceID {
		t.Fatalf("rpc target = node:%d service:%d, want node 4 service %d", node.calledNodeID, node.calledServiceID, accessnode.ManagerConnectionRPCServiceID)
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
	listReq     managementusecase.ListConnectionsRequest
	drainReq    managementusecase.SetNodeDrainModeRequest
	detail      managementusecase.ConnectionDetail
	runtime     managementusecase.NodeRuntimeSummary
}

func (f *fakeManagerConnectionService) ListConnections(_ context.Context, req managementusecase.ListConnectionsRequest) ([]managementusecase.Connection, error) {
	f.listReq = req
	return append([]managementusecase.Connection(nil), f.connections...), nil
}

func (f *fakeManagerConnectionService) GetConnection(context.Context, managementusecase.GetConnectionRequest) (managementusecase.ConnectionDetail, error) {
	return f.detail, nil
}

func (f *fakeManagerConnectionService) NodeRuntimeSummary(context.Context, uint64) (managementusecase.NodeRuntimeSummary, error) {
	return f.runtime, nil
}

func (f *fakeManagerConnectionService) SetNodeDrainMode(_ context.Context, req managementusecase.SetNodeDrainModeRequest) (managementusecase.SetNodeDrainModeResponse, error) {
	f.drainReq = req
	return managementusecase.SetNodeDrainModeResponse{
		NodeID:               f.runtime.NodeID,
		Draining:             f.runtime.Draining,
		AcceptingNewSessions: f.runtime.AcceptingNewSessions,
		GatewaySessions:      f.runtime.GatewaySessions,
		ActiveOnline:         f.runtime.ActiveOnline,
		ClosingOnline:        f.runtime.ClosingOnline,
		TotalOnline:          f.runtime.TotalOnline,
		PendingActivations:   f.runtime.PendingActivations,
		Unknown:              f.runtime.Unknown,
	}, nil
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
