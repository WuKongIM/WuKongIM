package node

import (
	"context"
	"testing"

	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
)

func TestManagerLogRPCReadsControllerLogs(t *testing.T) {
	service := &fakeManagerLogService{
		controller: managementusecase.ControllerLogEntriesResponse{
			NodeID:       2,
			FirstIndex:   1,
			LastIndex:    4,
			CommitIndex:  4,
			AppliedIndex: 3,
			NextCursor:   3,
			Items: []managementusecase.ControllerLogEntry{{
				Index:        4,
				Term:         2,
				Type:         "normal",
				DecodeStatus: "ok",
				DecodedType:  "init_cluster_state",
				Decoded:      map[string]any{"command": "init_cluster_state"},
			}},
		},
	}
	adapter := New(Options{ManagerLogs: service})
	body, err := encodeManagerLogRequest(managerLogRPCRequest{Op: managerLogOpController, NodeID: 2, Limit: 2, Cursor: 5})
	if err != nil {
		t.Fatalf("encodeManagerLogRequest() error = %v", err)
	}

	respBody, err := adapter.HandleManagerLogRPC(context.Background(), body)
	if err != nil {
		t.Fatalf("HandleManagerLogRPC() error = %v", err)
	}
	resp, err := decodeManagerLogResponse(respBody)
	if err != nil {
		t.Fatalf("decodeManagerLogResponse() error = %v", err)
	}

	if resp.Status != rpcStatusOK || len(resp.Controller.Items) != 1 || resp.Controller.Items[0].DecodedType != "init_cluster_state" {
		t.Fatalf("response = %#v, want ok controller log page", resp)
	}
	if service.controllerReq != (managementusecase.ListControllerLogEntriesRequest{NodeID: 2, Limit: 2, Cursor: 5}) {
		t.Fatalf("controller request = %#v, want node 2 limit 2 cursor 5", service.controllerReq)
	}
}

func TestManagerLogRPCClientReadsSlotLogs(t *testing.T) {
	service := &fakeManagerLogService{
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
	adapter := New(Options{ManagerLogs: service})
	node := &fakeManagerLogRPCNode{handler: adapter.HandleManagerLogRPC}
	client := NewClient(node)

	got, err := client.GetManagerSlotLogEntries(context.Background(), managementusecase.ListSlotLogEntriesRequest{
		NodeID: 2,
		SlotID: 9,
		Limit:  2,
		Cursor: 5,
	})
	if err != nil {
		t.Fatalf("GetManagerSlotLogEntries() error = %v", err)
	}

	if got.SlotID != 9 || len(got.Items) != 1 || got.Items[0].DecodedType != "noop" {
		t.Fatalf("slot page = %#v, want one noop entry", got)
	}
	if node.nodeID != 2 || node.serviceID != ManagerLogRPCServiceID {
		t.Fatalf("rpc target = node:%d service:%d, want node 2 service %d", node.nodeID, node.serviceID, ManagerLogRPCServiceID)
	}
}

type fakeManagerLogService struct {
	controllerReq managementusecase.ListControllerLogEntriesRequest
	slotReq       managementusecase.ListSlotLogEntriesRequest
	controller    managementusecase.ControllerLogEntriesResponse
	slot          managementusecase.SlotLogEntriesResponse
}

func (f *fakeManagerLogService) ControllerLogEntries(_ context.Context, req managementusecase.ListControllerLogEntriesRequest) (managementusecase.ControllerLogEntriesResponse, error) {
	f.controllerReq = req
	return f.controller, nil
}

func (f *fakeManagerLogService) SlotLogEntries(_ context.Context, req managementusecase.ListSlotLogEntriesRequest) (managementusecase.SlotLogEntriesResponse, error) {
	f.slotReq = req
	return f.slot, nil
}

type fakeManagerLogRPCNode struct {
	handler   func(context.Context, []byte) ([]byte, error)
	nodeID    uint64
	serviceID uint8
}

func (f *fakeManagerLogRPCNode) CallRPC(ctx context.Context, nodeID uint64, serviceID uint8, payload []byte) ([]byte, error) {
	f.nodeID = nodeID
	f.serviceID = serviceID
	return f.handler(ctx, payload)
}
