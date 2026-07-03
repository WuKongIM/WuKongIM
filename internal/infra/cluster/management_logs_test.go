package cluster

import (
	"context"
	"testing"

	accessnode "github.com/WuKongIM/WuKongIM/internal/access/node"
	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	"github.com/WuKongIM/WuKongIM/pkg/cluster"
)

func TestManagementLogReaderUsesLocalControllerLogs(t *testing.T) {
	node := &fakeManagementLogNode{
		nodeID: 1,
		controller: cluster.ControllerLogEntries{
			NodeID:       1,
			FirstIndex:   1,
			LastIndex:    4,
			CommitIndex:  4,
			AppliedIndex: 3,
			NextCursor:   3,
			Items: []cluster.LogEntry{{
				Index:        4,
				Term:         2,
				Type:         "normal",
				CreatedAtMS:  1781754611123,
				DecodeStatus: "ok",
				DecodedType:  "init_cluster_state",
				Decoded:      map[string]any{"command": "init_cluster_state"},
			}},
		},
	}
	reader := NewManagementLogReader(node)

	got, err := reader.ControllerLogEntries(context.Background(), managementusecase.ListControllerLogEntriesRequest{
		NodeID: 1,
		Limit:  2,
		Cursor: 5,
	})
	if err != nil {
		t.Fatalf("ControllerLogEntries() error = %v", err)
	}

	if got.NodeID != 1 || got.NextCursor != 3 || len(got.Items) != 1 || got.Items[0].DecodedType != "init_cluster_state" || got.Items[0].CreatedAtMS != 1781754611123 {
		t.Fatalf("controller page = %#v, want local decoded page", got)
	}
	if node.localControllerOpts != (cluster.LogEntriesOptions{Limit: 2, Cursor: 5}) {
		t.Fatalf("local opts = %#v, want limit 2 cursor 5", node.localControllerOpts)
	}
	if node.calledServiceID != 0 {
		t.Fatalf("called service id = %d, want no remote rpc", node.calledServiceID)
	}
}

func TestManagementLogReaderRoutesRemoteSlotLogs(t *testing.T) {
	service := &fakeRemoteManagerLogService{
		slot: managementusecase.SlotLogEntriesResponse{
			NodeID:       2,
			SlotID:       9,
			FirstIndex:   1,
			LastIndex:    4,
			CommitIndex:  4,
			AppliedIndex: 3,
			Items: []managementusecase.SlotLogEntry{{
				Index:       4,
				Term:        2,
				Type:        "normal",
				DecodedType: "noop",
			}},
		},
	}
	adapter := accessnode.New(accessnode.Options{ManagerLogs: service})
	node := &fakeManagementLogNode{
		nodeID:  1,
		handler: adapter.HandleManagerLogRPC,
	}
	reader := NewManagementLogReader(node)

	got, err := reader.SlotLogEntries(context.Background(), managementusecase.ListSlotLogEntriesRequest{
		NodeID: 2,
		SlotID: 9,
		Limit:  2,
		Cursor: 5,
	})
	if err != nil {
		t.Fatalf("SlotLogEntries() error = %v", err)
	}

	if got.NodeID != 2 || got.SlotID != 9 || len(got.Items) != 1 || got.Items[0].DecodedType != "noop" {
		t.Fatalf("slot page = %#v, want remote decoded page", got)
	}
	if service.slotReq != (managementusecase.ListSlotLogEntriesRequest{NodeID: 2, SlotID: 9, Limit: 2, Cursor: 5}) {
		t.Fatalf("remote slot request = %#v, want node 2 slot 9 limit 2 cursor 5", service.slotReq)
	}
	if node.calledNodeID != 2 || node.calledServiceID != accessnode.ManagerLogRPCServiceID {
		t.Fatalf("rpc target = node:%d service:%d, want node 2 service %d", node.calledNodeID, node.calledServiceID, accessnode.ManagerLogRPCServiceID)
	}
}

type fakeManagementLogNode struct {
	nodeID              uint64
	calledNodeID        uint64
	calledServiceID     uint8
	handler             func(context.Context, []byte) ([]byte, error)
	localControllerOpts cluster.LogEntriesOptions
	localSlotID         uint32
	localSlotOpts       cluster.LogEntriesOptions
	controller          cluster.ControllerLogEntries
	slot                cluster.SlotLogEntries
}

func (f *fakeManagementLogNode) NodeID() uint64 { return f.nodeID }

func (f *fakeManagementLogNode) CallRPC(ctx context.Context, nodeID uint64, serviceID uint8, payload []byte) ([]byte, error) {
	f.calledNodeID = nodeID
	f.calledServiceID = serviceID
	return f.handler(ctx, payload)
}

func (f *fakeManagementLogNode) LocalControllerLogEntries(_ context.Context, opts cluster.LogEntriesOptions) (cluster.ControllerLogEntries, error) {
	f.localControllerOpts = opts
	return f.controller, nil
}

func (f *fakeManagementLogNode) LocalSlotLogEntries(_ context.Context, slotID uint32, opts cluster.LogEntriesOptions) (cluster.SlotLogEntries, error) {
	f.localSlotID = slotID
	f.localSlotOpts = opts
	return f.slot, nil
}

type fakeRemoteManagerLogService struct {
	controllerReq managementusecase.ListControllerLogEntriesRequest
	slotReq       managementusecase.ListSlotLogEntriesRequest
	controller    managementusecase.ControllerLogEntriesResponse
	slot          managementusecase.SlotLogEntriesResponse
}

func (f *fakeRemoteManagerLogService) ControllerLogEntries(_ context.Context, req managementusecase.ListControllerLogEntriesRequest) (managementusecase.ControllerLogEntriesResponse, error) {
	f.controllerReq = req
	return f.controller, nil
}

func (f *fakeRemoteManagerLogService) SlotLogEntries(_ context.Context, req managementusecase.ListSlotLogEntriesRequest) (managementusecase.SlotLogEntriesResponse, error) {
	f.slotReq = req
	return f.slot, nil
}
