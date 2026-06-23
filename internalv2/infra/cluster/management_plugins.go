package cluster

import (
	"context"
	"encoding/json"

	"github.com/WuKongIM/WuKongIM/internal/usecase/plugin/pluginproto"
	accessnode "github.com/WuKongIM/WuKongIM/internalv2/access/node"
	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
)

// ManagementPluginNode exposes clusterv2 node RPC for manager plugin reads.
type ManagementPluginNode interface {
	// NodeID returns the local cluster node ID.
	NodeID() uint64
	// CallRPC invokes one typed node RPC service on a peer node.
	CallRPC(context.Context, uint64, uint8, []byte) ([]byte, error)
}

// ManagementPluginReader routes manager plugin inventory reads to selected nodes.
type ManagementPluginReader struct {
	remote *accessnode.Client
}

// PluginHTTPForwarder routes plugin host HTTP calls to selected nodes.
type PluginHTTPForwarder struct {
	remote *accessnode.Client
}

// NewManagementPluginReader creates a cluster-routed manager plugin reader.
func NewManagementPluginReader(node ManagementPluginNode) *ManagementPluginReader {
	return &ManagementPluginReader{remote: accessnode.NewClient(node)}
}

// NewPluginHTTPForwarder creates a cluster-routed plugin HTTP forwarder.
func NewPluginHTTPForwarder(node ManagementPluginNode) *PluginHTTPForwarder {
	if node == nil {
		return &PluginHTTPForwarder{}
	}
	return &PluginHTTPForwarder{remote: accessnode.NewClient(node)}
}

// NodePlugins reads plugin inventory from one selected node.
func (r *ManagementPluginReader) NodePlugins(ctx context.Context, nodeID uint64) ([]managementusecase.Plugin, error) {
	if r == nil || r.remote == nil {
		return nil, managementusecase.ErrPluginNodeUnavailable
	}
	return r.remote.ListManagerPlugins(ctx, nodeID)
}

// NodePlugin reads one plugin detail from one selected node.
func (r *ManagementPluginReader) NodePlugin(ctx context.Context, nodeID uint64, pluginNo string) (managementusecase.Plugin, error) {
	if r == nil || r.remote == nil {
		return managementusecase.Plugin{}, managementusecase.ErrPluginNodeUnavailable
	}
	return r.remote.GetManagerPlugin(ctx, nodeID, pluginNo)
}

// UpdateNodePluginConfig persists desired config on one selected node.
func (r *ManagementPluginReader) UpdateNodePluginConfig(ctx context.Context, nodeID uint64, pluginNo string, config json.RawMessage) (managementusecase.Plugin, error) {
	if r == nil || r.remote == nil {
		return managementusecase.Plugin{}, managementusecase.ErrPluginNodeUnavailable
	}
	return r.remote.UpdateManagerPluginConfig(ctx, nodeID, pluginNo, config)
}

// RestartNodePlugin restarts one plugin process on one selected node.
func (r *ManagementPluginReader) RestartNodePlugin(ctx context.Context, nodeID uint64, pluginNo string) (managementusecase.Plugin, error) {
	if r == nil || r.remote == nil {
		return managementusecase.Plugin{}, managementusecase.ErrPluginNodeUnavailable
	}
	return r.remote.RestartManagerPlugin(ctx, nodeID, pluginNo)
}

// UninstallNodePlugin disables and removes one plugin on one selected node.
func (r *ManagementPluginReader) UninstallNodePlugin(ctx context.Context, nodeID uint64, pluginNo string) error {
	if r == nil || r.remote == nil {
		return managementusecase.ErrPluginNodeUnavailable
	}
	return r.remote.UninstallManagerPlugin(ctx, nodeID, pluginNo)
}

// ForwardPluginHTTP invokes one plugin HTTP route on one selected node.
func (f *PluginHTTPForwarder) ForwardPluginHTTP(ctx context.Context, nodeID uint64, req *pluginproto.ForwardHttpReq) (*pluginproto.HttpResponse, error) {
	if f == nil || f.remote == nil {
		return nil, managementusecase.ErrPluginNodeUnavailable
	}
	return f.remote.ForwardPluginHTTP(ctx, nodeID, req)
}
