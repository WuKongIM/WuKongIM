package node

import (
	"context"
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/contracts/channelappend"
	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
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

func TestManagerMessageRetentionRPCPreservesStableLeaderAndAvailabilityErrors(t *testing.T) {
	tests := []struct {
		name        string
		providerErr error
		wantErr     error
	}{
		{name: "caller canceled", providerErr: context.Canceled, wantErr: context.Canceled},
		{name: "caller deadline", providerErr: context.DeadlineExceeded, wantErr: context.DeadlineExceeded},
		{name: "not leader", providerErr: channelappend.ErrNotLeader, wantErr: channelappend.ErrNotLeader},
		{name: "stale route", providerErr: channelappend.ErrStaleRoute, wantErr: channelappend.ErrStaleRoute},
		{name: "route not ready", providerErr: channelappend.ErrRouteNotReady, wantErr: channelappend.ErrRouteNotReady},
		{name: "channel missing", providerErr: channelappend.ErrChannelNotFound, wantErr: metadb.ErrNotFound},
		{name: "invalid request", providerErr: metadb.ErrInvalidArgument, wantErr: managementusecase.ErrMessageRetentionUnavailable},
		{name: "operator unavailable", providerErr: managementusecase.ErrMessageRetentionUnavailable, wantErr: managementusecase.ErrMessageRetentionUnavailable},
	}

	request := managementusecase.AdvanceMessageRetentionRequest{
		ChannelID: "room-fenced", ChannelType: 2, ThroughSeq: 91, DryRun: true,
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &fakeManagerMessageRetentionService{
				result: managementusecase.AdvanceMessageRetentionResponse{
					ChannelID: "must-not-be-accepted", AdvancedThroughSeq: 90,
				},
				err: tt.providerErr,
			}
			adapter := New(Options{ManagerMessageRetention: service})
			node := &fakeManagerMessageRetentionRPCNode{handler: adapter.HandleManagerMessageRetentionRPC}

			got, err := NewClient(node).AdvanceManagerMessageRetention(context.Background(), 3, request)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("AdvanceManagerMessageRetention() error = %v, want %v", err, tt.wantErr)
			}
			if got != (managementusecase.AdvanceMessageRetentionResponse{}) {
				t.Fatalf("failed retention result = %#v, want zero response", got)
			}
			if service.req != request {
				t.Fatalf("provider request = %#v, want exact fenced request %#v", service.req, request)
			}
			if node.nodeID != 3 || node.serviceID != ManagerMessageRetentionRPCServiceID {
				t.Fatalf("RPC target = node:%d service:%d", node.nodeID, node.serviceID)
			}
		})
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
