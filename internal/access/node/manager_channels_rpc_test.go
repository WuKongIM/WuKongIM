package node

import (
	"context"
	"testing"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
)

func TestManagerChannelRPCListsBusinessChannels(t *testing.T) {
	service := &fakeManagerChannelService{
		page: managementusecase.ListBusinessChannelsResponse{
			Items: []managementusecase.BusinessChannelListItem{{
				ChannelID:                 "g1",
				ChannelType:               2,
				SlotID:                    9,
				HashSlot:                  3,
				SendBan:                   true,
				SubscriberMutationVersion: 7,
			}},
			HasMore: true,
			NextCursor: managementusecase.ChannelListCursor{
				SlotID:      9,
				ChannelID:   "g1",
				ChannelType: 2,
				TypeFilter:  2,
			},
		},
	}
	adapter := New(Options{ManagerChannels: service})
	body, err := encodeManagerChannelRequest(managerChannelRPCRequest{
		NodeID:     2,
		Limit:      50,
		TypeFilter: 2,
		Keyword:    "g",
		Cursor: managementusecase.ChannelListCursor{
			SlotID:      8,
			ChannelID:   "prev",
			ChannelType: 2,
			TypeFilter:  2,
		},
	})
	if err != nil {
		t.Fatalf("encodeManagerChannelRequest() error = %v", err)
	}

	respBody, err := adapter.HandleManagerChannelRPC(context.Background(), body)
	if err != nil {
		t.Fatalf("HandleManagerChannelRPC() error = %v", err)
	}
	resp, err := decodeManagerChannelResponse(respBody)
	if err != nil {
		t.Fatalf("decodeManagerChannelResponse() error = %v", err)
	}

	if resp.Status != rpcStatusOK || len(resp.Page.Items) != 1 || resp.Page.Items[0].ChannelID != "g1" || !resp.Page.HasMore {
		t.Fatalf("response = %#v, want ok channel page", resp)
	}
	if service.req.NodeID != 2 || service.req.Limit != 50 || service.req.TypeFilter != 2 || service.req.Keyword != "g" || service.req.Cursor.ChannelID != "prev" {
		t.Fatalf("request = %#v, want node 2 type 2 keyword g cursor prev", service.req)
	}
}

func TestManagerChannelRPCClientListsBusinessChannels(t *testing.T) {
	service := &fakeManagerChannelService{
		page: managementusecase.ListBusinessChannelsResponse{
			Items: []managementusecase.BusinessChannelListItem{{ChannelID: "remote", ChannelType: 1}},
		},
	}
	adapter := New(Options{ManagerChannels: service})
	node := &fakeManagerChannelRPCNode{handler: adapter.HandleManagerChannelRPC}
	client := NewClient(node)

	got, err := client.ListManagerBusinessChannels(context.Background(), managementusecase.ListBusinessChannelsRequest{
		NodeID: 2,
		Limit:  50,
	})
	if err != nil {
		t.Fatalf("ListManagerBusinessChannels() error = %v", err)
	}

	if len(got.Items) != 1 || got.Items[0].ChannelID != "remote" {
		t.Fatalf("page = %#v, want remote channel", got)
	}
	if node.nodeID != 2 || node.serviceID != ManagerChannelRPCServiceID {
		t.Fatalf("rpc target = node:%d service:%d, want node 2 service %d", node.nodeID, node.serviceID, ManagerChannelRPCServiceID)
	}
}

type fakeManagerChannelService struct {
	req  managementusecase.ListBusinessChannelsRequest
	page managementusecase.ListBusinessChannelsResponse
	err  error
}

func (f *fakeManagerChannelService) ListBusinessChannels(_ context.Context, req managementusecase.ListBusinessChannelsRequest) (managementusecase.ListBusinessChannelsResponse, error) {
	f.req = req
	return f.page, f.err
}

type fakeManagerChannelRPCNode struct {
	handler   func(context.Context, []byte) ([]byte, error)
	nodeID    uint64
	serviceID uint8
}

func (f *fakeManagerChannelRPCNode) CallRPC(ctx context.Context, nodeID uint64, serviceID uint8, payload []byte) ([]byte, error) {
	f.nodeID = nodeID
	f.serviceID = serviceID
	return f.handler(ctx, payload)
}
