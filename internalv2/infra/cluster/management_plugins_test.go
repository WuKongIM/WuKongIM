package cluster

import (
	"context"
	"net/http"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/usecase/plugin/pluginproto"
	accessnode "github.com/WuKongIM/WuKongIM/internalv2/access/node"
	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
)

func TestManagementPluginReaderRoutesRemotePluginReads(t *testing.T) {
	service := &fakeManagementPluginService{
		list: managementusecase.NodePluginList{
			NodeID: 2,
			Plugins: []managementusecase.Plugin{{
				NodeID: 2,
				No:     "wk.persist",
				Status: "running",
			}},
		},
		plugin: managementusecase.Plugin{NodeID: 2, No: "wk.persist", Status: "running"},
	}
	adapter := accessnode.New(accessnode.Options{ManagerPlugins: service})
	node := &fakeManagementPluginNode{handler: adapter.HandleManagerPluginRPC}
	reader := NewManagementPluginReader(node)

	list, err := reader.NodePlugins(context.Background(), 2)
	if err != nil {
		t.Fatalf("NodePlugins() error = %v", err)
	}
	if len(list) != 1 || list[0].No != "wk.persist" {
		t.Fatalf("plugins = %#v, want wk.persist", list)
	}
	if node.nodeID != 2 || node.serviceID != accessnode.ManagerPluginRPCServiceID {
		t.Fatalf("rpc target = node:%d service:%d, want node 2 service %d", node.nodeID, node.serviceID, accessnode.ManagerPluginRPCServiceID)
	}

	detail, err := reader.NodePlugin(context.Background(), 2, "wk.persist")
	if err != nil {
		t.Fatalf("NodePlugin() error = %v", err)
	}
	if detail.No != "wk.persist" || service.detailPluginNo != "wk.persist" {
		t.Fatalf("plugin = %#v service plugin=%q, want wk.persist", detail, service.detailPluginNo)
	}
}

func TestPluginHTTPForwarderRoutesRemotePluginHTTP(t *testing.T) {
	routes := &fakePluginHTTPRoutes{
		resp: &pluginproto.HttpResponse{
			Status:  http.StatusCreated,
			Headers: map[string]string{"X-Plugin": "ok"},
			Body:    []byte("forwarded"),
		},
	}
	adapter := accessnode.New(accessnode.Options{PluginHTTPRoutes: routes})
	node := &fakeManagementPluginNode{handler: adapter.HandleManagerPluginRPC}
	forwarder := NewPluginHTTPForwarder(node)

	resp, err := forwarder.ForwardPluginHTTP(context.Background(), 3, &pluginproto.ForwardHttpReq{
		PluginNo: "wk.http",
		ToNodeId: 3,
		Request: &pluginproto.HttpRequest{
			Method: "POST",
			Path:   "/echo",
			Body:   []byte("payload"),
		},
	})
	if err != nil {
		t.Fatalf("ForwardPluginHTTP() error = %v", err)
	}
	if node.nodeID != 3 || node.serviceID != accessnode.ManagerPluginRPCServiceID {
		t.Fatalf("rpc target = node:%d service:%d, want node 3 service %d", node.nodeID, node.serviceID, accessnode.ManagerPluginRPCServiceID)
	}
	if routes.pluginNo != "wk.http" || routes.req.GetPath() != "/echo" || string(routes.req.GetBody()) != "payload" {
		t.Fatalf("route call = plugin:%q req:%#v", routes.pluginNo, routes.req)
	}
	if resp.GetStatus() != http.StatusCreated || string(resp.GetBody()) != "forwarded" || resp.GetHeaders()["X-Plugin"] != "ok" {
		t.Fatalf("response = %#v, want remote plugin response", resp)
	}
}

func TestPluginHTTPForwarderRequiresNode(t *testing.T) {
	forwarder := NewPluginHTTPForwarder(nil)

	_, err := forwarder.ForwardPluginHTTP(context.Background(), 2, &pluginproto.ForwardHttpReq{})

	if err == nil {
		t.Fatal("ForwardPluginHTTP() error = nil, want unavailable")
	}
}

type fakeManagementPluginService struct {
	listNodeID     uint64
	detailNodeID   uint64
	detailPluginNo string
	list           managementusecase.NodePluginList
	plugin         managementusecase.Plugin
	err            error
}

func (f *fakeManagementPluginService) ListNodePlugins(_ context.Context, nodeID uint64) (managementusecase.NodePluginList, error) {
	f.listNodeID = nodeID
	return f.list, f.err
}

func (f *fakeManagementPluginService) GetNodePlugin(_ context.Context, nodeID uint64, pluginNo string) (managementusecase.Plugin, error) {
	f.detailNodeID = nodeID
	f.detailPluginNo = pluginNo
	return f.plugin, f.err
}

type fakeManagementPluginNode struct {
	handler   func(context.Context, []byte) ([]byte, error)
	nodeID    uint64
	serviceID uint8
}

func (f *fakeManagementPluginNode) NodeID() uint64 { return 1 }

func (f *fakeManagementPluginNode) CallRPC(ctx context.Context, nodeID uint64, serviceID uint8, payload []byte) ([]byte, error) {
	f.nodeID = nodeID
	f.serviceID = serviceID
	return f.handler(ctx, payload)
}

type fakePluginHTTPRoutes struct {
	pluginNo string
	req      *pluginproto.HttpRequest
	resp     *pluginproto.HttpResponse
	err      error
}

func (f *fakePluginHTTPRoutes) Route(_ context.Context, pluginNo string, req *pluginproto.HttpRequest) (*pluginproto.HttpResponse, error) {
	f.pluginNo = pluginNo
	f.req = req
	return f.resp, f.err
}
