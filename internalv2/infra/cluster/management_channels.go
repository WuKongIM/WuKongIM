package cluster

import (
	"context"

	accessnode "github.com/WuKongIM/WuKongIM/internalv2/access/node"
	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
)

// ManagementChannelNode exposes cluster node RPC for manager channel list reads.
type ManagementChannelNode interface {
	// NodeID returns the local cluster node ID.
	NodeID() uint64
	// CallRPC invokes one typed node RPC service on a peer node.
	CallRPC(context.Context, uint64, uint8, []byte) ([]byte, error)
}

// ManagementChannelReader routes manager channel list reads to selected nodes.
type ManagementChannelReader struct {
	node   ManagementChannelNode
	remote *accessnode.Client
}

// NewManagementChannelReader creates a cluster-routed manager channel reader.
func NewManagementChannelReader(node ManagementChannelNode) *ManagementChannelReader {
	return &ManagementChannelReader{
		node:   node,
		remote: accessnode.NewClient(node),
	}
}

// NodeBusinessChannels reads one node's manager channel list page.
func (r *ManagementChannelReader) NodeBusinessChannels(ctx context.Context, req managementusecase.ListBusinessChannelsRequest) (managementusecase.ListBusinessChannelsResponse, error) {
	if r == nil || r.node == nil || r.remote == nil {
		return managementusecase.ListBusinessChannelsResponse{}, managementusecase.ErrBusinessChannelReaderUnavailable
	}
	return r.remote.ListManagerBusinessChannels(ctx, req)
}
