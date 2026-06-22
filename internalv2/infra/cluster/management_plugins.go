package cluster

import (
	"context"

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

// NewManagementPluginReader creates a cluster-routed manager plugin reader.
func NewManagementPluginReader(node ManagementPluginNode) *ManagementPluginReader {
	return &ManagementPluginReader{remote: accessnode.NewClient(node)}
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
