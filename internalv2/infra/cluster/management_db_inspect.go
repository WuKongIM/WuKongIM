package cluster

import (
	"context"

	accessnode "github.com/WuKongIM/WuKongIM/internalv2/access/node"
	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
)

// ManagementDBInspectNode exposes clusterv2 node RPC for manager DB inspect reads.
type ManagementDBInspectNode interface {
	// NodeID returns the local cluster node ID.
	NodeID() uint64
	// CallRPC invokes one typed node RPC service on a peer node.
	CallRPC(context.Context, uint64, uint8, []byte) ([]byte, error)
}

// ManagementDBInspectReader routes manager DB inspect queries to selected nodes.
type ManagementDBInspectReader struct {
	node   ManagementDBInspectNode
	remote *accessnode.Client
}

// NewManagementDBInspectReader creates a cluster-routed manager DB inspect reader.
func NewManagementDBInspectReader(node ManagementDBInspectNode) *ManagementDBInspectReader {
	return &ManagementDBInspectReader{
		node:   node,
		remote: accessnode.NewClient(node),
	}
}

// NodeDBInspectQuery runs a read-only DB inspect query on one node.
func (r *ManagementDBInspectReader) NodeDBInspectQuery(ctx context.Context, req managementusecase.DBInspectQueryRequest) (managementusecase.DBInspectQueryResponse, error) {
	if r == nil || r.node == nil || r.remote == nil {
		return managementusecase.DBInspectQueryResponse{}, managementusecase.ErrDBInspectUnavailable
	}
	return r.remote.NodeDBInspectQuery(ctx, req)
}
