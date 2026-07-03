package node

import (
	"context"
	"testing"
	"time"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
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
	req := managerConnectionRPCRequest{Op: managerConnectionOpList, NodeID: 2, Limit: 100}
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
	if service.listReq != (managementusecase.ListConnectionsRequest{NodeID: 2, Limit: 100}) {
		t.Fatalf("list request = %#v, want node 2 limit 100", service.listReq)
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

func TestManagerConnectionRPCClientGetsRuntimeSummary(t *testing.T) {
	service := &fakeManagerConnectionService{
		runtime: managementusecase.NodeRuntimeSummary{
			NodeID:               2,
			ActiveOnline:         7,
			ClosingOnline:        1,
			TotalOnline:          8,
			GatewaySessions:      9,
			PendingActivations:   3,
			SessionsByListener:   map[string]int{"tcp": 9},
			AcceptingNewSessions: false,
			Draining:             true,
		},
	}
	adapter := New(Options{ManagerConnections: service})
	node := &fakeManagerConnectionRPCNode{handler: adapter.HandleManagerConnectionRPC}
	client := NewClient(node)

	got, err := client.GetManagerRuntimeSummary(context.Background(), 2)
	if err != nil {
		t.Fatalf("GetManagerRuntimeSummary() error = %v", err)
	}

	if got.NodeID != 2 || got.ActiveOnline != 7 || got.ClosingOnline != 1 || got.TotalOnline != 8 ||
		got.GatewaySessions != 9 || got.PendingActivations != 3 || got.SessionsByListener["tcp"] != 9 ||
		got.AcceptingNewSessions || !got.Draining || got.Unknown {
		t.Fatalf("runtime summary = %#v, want concrete node 2 runtime summary", got)
	}
	if service.runtimeNodeID != 2 {
		t.Fatalf("runtime request node id = %d, want 2", service.runtimeNodeID)
	}
	if node.nodeID != 2 || node.serviceID != ManagerConnectionRPCServiceID {
		t.Fatalf("rpc target = node:%d service:%d, want node 2 service %d", node.nodeID, node.serviceID, ManagerConnectionRPCServiceID)
	}
}

func TestManagerConnectionRPCSetDrainMode(t *testing.T) {
	service := &fakeManagerConnectionService{runtime: managementusecase.NodeRuntimeSummary{
		NodeID: 4, Draining: true, AcceptingNewSessions: false, PendingActivations: 1,
	}}
	adapter := New(Options{ManagerConnections: service})
	body, err := encodeManagerConnectionRequest(managerConnectionRPCRequest{Op: managerConnectionOpSetDrainMode, NodeID: 4, Draining: true})
	if err != nil {
		t.Fatalf("encode request error = %v", err)
	}

	respBody, err := adapter.HandleManagerConnectionRPC(context.Background(), body)
	if err != nil {
		t.Fatalf("HandleManagerConnectionRPC() error = %v", err)
	}
	resp, err := decodeManagerConnectionResponse(respBody)
	if err != nil {
		t.Fatalf("decode response error = %v", err)
	}
	if service.drainReq != (managementusecase.SetNodeDrainModeRequest{NodeID: 4, Draining: true}) ||
		!resp.Summary.Draining || resp.Summary.PendingActivations != 1 {
		t.Fatalf("service=%#v response=%#v, want drain summary", service, resp.Summary)
	}
}

func TestManagerConnectionRPCClientMapsDrainUnavailable(t *testing.T) {
	service := &fakeManagerConnectionService{err: managementusecase.ErrNodeScaleInUnavailable}
	adapter := New(Options{ManagerConnections: service})
	node := &fakeManagerConnectionRPCNode{handler: adapter.HandleManagerConnectionRPC}
	client := NewClient(node)

	_, err := client.SetManagerDrainMode(context.Background(), 4, true)
	if err != managementusecase.ErrNodeScaleInUnavailable {
		t.Fatalf("SetManagerDrainMode() error = %v, want ErrNodeScaleInUnavailable", err)
	}
}

func TestManagerConnectionRPCClientMapsDrainInvalidArgument(t *testing.T) {
	service := &fakeManagerConnectionService{err: metadb.ErrInvalidArgument}
	adapter := New(Options{ManagerConnections: service})
	node := &fakeManagerConnectionRPCNode{handler: adapter.HandleManagerConnectionRPC}
	client := NewClient(node)

	_, err := client.SetManagerDrainMode(context.Background(), 4, true)
	if err != metadb.ErrInvalidArgument {
		t.Fatalf("SetManagerDrainMode() error = %v, want ErrInvalidArgument", err)
	}
}

type fakeManagerConnectionService struct {
	listReq       managementusecase.ListConnectionsRequest
	detailReq     managementusecase.GetConnectionRequest
	runtimeNodeID uint64
	drainReq      managementusecase.SetNodeDrainModeRequest
	connections   []managementusecase.Connection
	detail        managementusecase.ConnectionDetail
	runtime       managementusecase.NodeRuntimeSummary
	err           error
}

func (f *fakeManagerConnectionService) ListConnections(_ context.Context, req managementusecase.ListConnectionsRequest) ([]managementusecase.Connection, error) {
	f.listReq = req
	return append([]managementusecase.Connection(nil), f.connections...), nil
}

func (f *fakeManagerConnectionService) GetConnection(_ context.Context, req managementusecase.GetConnectionRequest) (managementusecase.ConnectionDetail, error) {
	f.detailReq = req
	return f.detail, nil
}

func (f *fakeManagerConnectionService) NodeRuntimeSummary(_ context.Context, nodeID uint64) (managementusecase.NodeRuntimeSummary, error) {
	f.runtimeNodeID = nodeID
	return f.runtime, nil
}

func (f *fakeManagerConnectionService) SetNodeDrainMode(_ context.Context, req managementusecase.SetNodeDrainModeRequest) (managementusecase.SetNodeDrainModeResponse, error) {
	if f.err != nil {
		return managementusecase.SetNodeDrainModeResponse{}, f.err
	}
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
