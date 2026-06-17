package cluster

import (
	"context"
	"testing"

	accessnode "github.com/WuKongIM/WuKongIM/internalv2/access/node"
	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
)

func TestManagementChannelReaderRoutesRemoteBusinessChannelPage(t *testing.T) {
	service := &fakeManagementChannelService{
		page: managementusecase.ListBusinessChannelsResponse{
			Items: []managementusecase.BusinessChannelListItem{{
				ChannelID:   "remote",
				ChannelType: 2,
				SlotID:      9,
				HashSlot:    3,
			}},
		},
	}
	adapter := accessnode.New(accessnode.Options{ManagerChannels: service})
	node := &fakeManagementChannelNode{handler: adapter.HandleManagerChannelRPC}
	reader := NewManagementChannelReader(node)

	got, err := reader.NodeBusinessChannels(context.Background(), managementusecase.ListBusinessChannelsRequest{
		NodeID: 2,
		Limit:  50,
	})
	if err != nil {
		t.Fatalf("NodeBusinessChannels() error = %v", err)
	}

	if len(got.Items) != 1 || got.Items[0].ChannelID != "remote" {
		t.Fatalf("page = %#v, want remote channel", got)
	}
	if node.nodeID != 2 || node.serviceID != accessnode.ManagerChannelRPCServiceID {
		t.Fatalf("rpc target = node:%d service:%d, want node 2 service %d", node.nodeID, node.serviceID, accessnode.ManagerChannelRPCServiceID)
	}
}

type fakeManagementChannelService struct {
	req  managementusecase.ListBusinessChannelsRequest
	page managementusecase.ListBusinessChannelsResponse
	err  error
}

func (f *fakeManagementChannelService) ListBusinessChannels(_ context.Context, req managementusecase.ListBusinessChannelsRequest) (managementusecase.ListBusinessChannelsResponse, error) {
	f.req = req
	return f.page, f.err
}

type fakeManagementChannelNode struct {
	handler   func(context.Context, []byte) ([]byte, error)
	nodeID    uint64
	serviceID uint8
}

func (f *fakeManagementChannelNode) NodeID() uint64 { return 1 }

func (f *fakeManagementChannelNode) CallRPC(ctx context.Context, nodeID uint64, serviceID uint8, payload []byte) ([]byte, error) {
	f.nodeID = nodeID
	f.serviceID = serviceID
	return f.handler(ctx, payload)
}
