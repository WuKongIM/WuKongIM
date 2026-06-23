package node

import (
	"context"
	"testing"

	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
)

func TestManagerMessageRetentionRPCAdvancesBoundary(t *testing.T) {
	service := &fakeManagerMessageRetentionService{
		result: managementusecase.AdvanceMessageRetentionResponse{
			ChannelID: "room-1", ChannelType: 2,
			RequestedThroughSeq: 10, AdvancedThroughSeq: 8, MinAvailableSeq: 9,
			Status: managementusecase.MessageRetentionStatusAdvanced,
		},
	}
	adapter := New(Options{ManagerMessageRetention: service})
	body, err := encodeManagerMessageRetentionRequest(managerMessageRetentionRPCRequest{
		Request: managementusecase.AdvanceMessageRetentionRequest{
			ChannelID: "room-1", ChannelType: 2, ThroughSeq: 10, DryRun: true,
		},
	})
	if err != nil {
		t.Fatalf("encodeManagerMessageRetentionRequest() error = %v", err)
	}

	respBody, err := adapter.HandleManagerMessageRetentionRPC(context.Background(), body)
	if err != nil {
		t.Fatalf("HandleManagerMessageRetentionRPC() error = %v", err)
	}
	resp, err := decodeManagerMessageRetentionResponse(respBody)
	if err != nil {
		t.Fatalf("decodeManagerMessageRetentionResponse() error = %v", err)
	}

	if resp.Status != rpcStatusOK || resp.Result.Status != managementusecase.MessageRetentionStatusAdvanced || resp.Result.AdvancedThroughSeq != 8 {
		t.Fatalf("response = %#v, want ok advanced result", resp)
	}
	if service.req.ChannelID != "room-1" || service.req.ChannelType != 2 || service.req.ThroughSeq != 10 || !service.req.DryRun {
		t.Fatalf("request = %#v, want room-1/2 through 10 dry-run", service.req)
	}
}

func TestManagerMessageRetentionRPCClientAdvancesBoundary(t *testing.T) {
	service := &fakeManagerMessageRetentionService{
		result: managementusecase.AdvanceMessageRetentionResponse{
			ChannelID: "remote", ChannelType: 2,
			RequestedThroughSeq: 10, AdvancedThroughSeq: 8, MinAvailableSeq: 9,
			Status: managementusecase.MessageRetentionStatusAdvanced,
		},
	}
	adapter := New(Options{ManagerMessageRetention: service})
	node := &fakeManagerMessageRetentionRPCNode{handler: adapter.HandleManagerMessageRetentionRPC}
	client := NewClient(node)

	got, err := client.AdvanceManagerMessageRetention(context.Background(), 3, managementusecase.AdvanceMessageRetentionRequest{
		ChannelID: "remote", ChannelType: 2, ThroughSeq: 10,
	})
	if err != nil {
		t.Fatalf("AdvanceManagerMessageRetention() error = %v", err)
	}

	if got.AdvancedThroughSeq != 8 || got.MinAvailableSeq != 9 {
		t.Fatalf("response = %#v, want advanced through 8", got)
	}
	if node.nodeID != 3 || node.serviceID != ManagerMessageRetentionRPCServiceID {
		t.Fatalf("rpc target = node:%d service:%d, want node 3 service %d", node.nodeID, node.serviceID, ManagerMessageRetentionRPCServiceID)
	}
}

type fakeManagerMessageRetentionService struct {
	req    managementusecase.AdvanceMessageRetentionRequest
	result managementusecase.AdvanceMessageRetentionResponse
	err    error
}

func (f *fakeManagerMessageRetentionService) AdvanceMessageRetention(_ context.Context, req managementusecase.AdvanceMessageRetentionRequest) (managementusecase.AdvanceMessageRetentionResponse, error) {
	f.req = req
	return f.result, f.err
}

type fakeManagerMessageRetentionRPCNode struct {
	handler   func(context.Context, []byte) ([]byte, error)
	nodeID    uint64
	serviceID uint8
}

func (f *fakeManagerMessageRetentionRPCNode) CallRPC(ctx context.Context, nodeID uint64, serviceID uint8, payload []byte) ([]byte, error) {
	f.nodeID = nodeID
	f.serviceID = serviceID
	return f.handler(ctx, payload)
}
