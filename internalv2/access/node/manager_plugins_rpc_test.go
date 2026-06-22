package node

import (
	"context"
	"testing"
	"time"

	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
	pluginusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/plugin"
)

func TestManagerPluginRPCListsNodePlugins(t *testing.T) {
	lastSeen := time.Unix(1713859200, 0).UTC()
	service := &fakeManagerPluginService{
		list: managementusecase.NodePluginList{
			NodeID: 2,
			Plugins: []managementusecase.Plugin{{
				NodeID: 2, No: "wk.persist", Name: "Persist", Version: "v1",
				Methods:  []pluginusecase.Method{pluginusecase.MethodPersistAfter},
				Priority: 9, PersistAfterSync: true, ReplySync: true,
				Status: "running", Enabled: true, PID: 101, LastSeenAt: lastSeen,
				LastError: "last warning",
			}},
		},
	}
	adapter := New(Options{ManagerPlugins: service})
	body, err := encodeManagerPluginRequest(managerPluginRPCRequest{Op: managerPluginOpList, NodeID: 2})
	if err != nil {
		t.Fatalf("encodeManagerPluginRequest() error = %v", err)
	}

	respBody, err := adapter.HandleManagerPluginRPC(context.Background(), body)
	if err != nil {
		t.Fatalf("HandleManagerPluginRPC() error = %v", err)
	}
	resp, err := decodeManagerPluginResponse(respBody)
	if err != nil {
		t.Fatalf("decodeManagerPluginResponse() error = %v", err)
	}

	if resp.Status != rpcStatusOK || len(resp.Plugins) != 1 || resp.Plugins[0].No != "wk.persist" || !resp.Plugins[0].LastSeenAt.Equal(lastSeen) {
		t.Fatalf("response = %#v, want ok plugin list", resp)
	}
	if service.listNodeID != 2 {
		t.Fatalf("service list node = %d, want 2", service.listNodeID)
	}
}

func TestManagerPluginRPCClientGetsNodePlugin(t *testing.T) {
	service := &fakeManagerPluginService{
		plugin: managementusecase.Plugin{NodeID: 2, No: "wk.persist", Status: "running", Enabled: true},
	}
	adapter := New(Options{ManagerPlugins: service})
	node := &fakeManagerPluginRPCNode{handler: adapter.HandleManagerPluginRPC}
	client := NewClient(node)

	got, err := client.GetManagerPlugin(context.Background(), 2, "wk.persist")
	if err != nil {
		t.Fatalf("GetManagerPlugin() error = %v", err)
	}

	if got.No != "wk.persist" || got.NodeID != 2 {
		t.Fatalf("plugin = %#v, want wk.persist on node 2", got)
	}
	if node.nodeID != 2 || node.serviceID != ManagerPluginRPCServiceID {
		t.Fatalf("rpc target = node:%d service:%d, want node 2 service %d", node.nodeID, node.serviceID, ManagerPluginRPCServiceID)
	}
	if service.detailNodeID != 2 || service.detailPluginNo != "wk.persist" {
		t.Fatalf("service detail target = node:%d plugin:%q, want node 2 wk.persist", service.detailNodeID, service.detailPluginNo)
	}
}

type fakeManagerPluginService struct {
	listNodeID     uint64
	detailNodeID   uint64
	detailPluginNo string
	list           managementusecase.NodePluginList
	plugin         managementusecase.Plugin
	err            error
}

func (f *fakeManagerPluginService) ListNodePlugins(_ context.Context, nodeID uint64) (managementusecase.NodePluginList, error) {
	f.listNodeID = nodeID
	return f.list, f.err
}

func (f *fakeManagerPluginService) GetNodePlugin(_ context.Context, nodeID uint64, pluginNo string) (managementusecase.Plugin, error) {
	f.detailNodeID = nodeID
	f.detailPluginNo = pluginNo
	return f.plugin, f.err
}

type fakeManagerPluginRPCNode struct {
	handler   func(context.Context, []byte) ([]byte, error)
	nodeID    uint64
	serviceID uint8
}

func (f *fakeManagerPluginRPCNode) CallRPC(ctx context.Context, nodeID uint64, serviceID uint8, payload []byte) ([]byte, error) {
	f.nodeID = nodeID
	f.serviceID = serviceID
	return f.handler(ctx, payload)
}
