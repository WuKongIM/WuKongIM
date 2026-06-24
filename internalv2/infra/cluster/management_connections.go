package cluster

import (
	"context"

	accessnode "github.com/WuKongIM/WuKongIM/internalv2/access/node"
	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
)

// ManagementConnectionNode exposes clusterv2 node RPC for manager connection inventory reads.
type ManagementConnectionNode interface {
	// NodeID returns the local cluster node ID.
	NodeID() uint64
	// CallRPC invokes one typed node RPC service on a peer node.
	CallRPC(context.Context, uint64, uint8, []byte) ([]byte, error)
}

// ManagementConnectionReader routes manager connection inventory reads to owner nodes.
type ManagementConnectionReader struct {
	node   ManagementConnectionNode
	remote *accessnode.Client
}

// NewManagementConnectionReader creates a cluster-routed manager connection reader.
func NewManagementConnectionReader(node ManagementConnectionNode) *ManagementConnectionReader {
	return &ManagementConnectionReader{
		node:   node,
		remote: accessnode.NewClient(node),
	}
}

// NodeConnections reads active connections from one owner node.
func (r *ManagementConnectionReader) NodeConnections(ctx context.Context, nodeID uint64) ([]managementusecase.Connection, error) {
	if r == nil || r.remote == nil {
		return nil, managementusecase.ErrConnectionReaderUnavailable
	}
	return r.remote.ListManagerConnections(ctx, nodeID)
}

// NodeConnection reads one connection detail from one owner node.
func (r *ManagementConnectionReader) NodeConnection(ctx context.Context, nodeID, sessionID uint64) (managementusecase.ConnectionDetail, error) {
	if r == nil || r.remote == nil {
		return managementusecase.ConnectionDetail{}, managementusecase.ErrConnectionReaderUnavailable
	}
	return r.remote.GetManagerConnection(ctx, nodeID, sessionID)
}

// NodeRuntimeSummary reads aggregate runtime counters from one owner node.
func (r *ManagementConnectionReader) NodeRuntimeSummary(ctx context.Context, nodeID uint64) (managementusecase.NodeRuntimeSummary, error) {
	if r == nil || r.remote == nil {
		return managementusecase.NodeRuntimeSummary{}, managementusecase.ErrConnectionReaderUnavailable
	}
	return r.remote.GetManagerRuntimeSummary(ctx, nodeID)
}
