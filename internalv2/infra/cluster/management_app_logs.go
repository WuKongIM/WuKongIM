package cluster

import (
	"context"

	accessnode "github.com/WuKongIM/WuKongIM/internalv2/access/node"
	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
)

// ManagementApplicationLogRPCNode exposes clusterv2 node RPC for manager application log reads.
type ManagementApplicationLogRPCNode interface {
	// NodeID returns the local cluster node ID.
	NodeID() uint64
	// CallRPC invokes one typed node RPC service on a peer node.
	CallRPC(context.Context, uint64, uint8, []byte) ([]byte, error)
}

// LocalApplicationLogReader reads ordinary application logs from this node.
type LocalApplicationLogReader interface {
	// ApplicationLogSources returns the ordinary application log sources available on this node.
	ApplicationLogSources(context.Context, managementusecase.ApplicationLogSourcesRequest) (managementusecase.ApplicationLogSourcesResponse, error)
	// ApplicationLogEntries returns one page from a local ordinary application log source.
	ApplicationLogEntries(context.Context, managementusecase.ApplicationLogEntriesRequest) (managementusecase.ApplicationLogEntriesResponse, error)
}

// ManagementApplicationLogReader routes ordinary application log reads to selected nodes.
type ManagementApplicationLogReader struct {
	// node is the local cluster node RPC facade.
	node ManagementApplicationLogRPCNode
	// local reads ordinary application logs from this node.
	local LocalApplicationLogReader
	// remote forwards ordinary application log reads to peer nodes.
	remote *accessnode.Client
}

// NewManagementApplicationLogReader creates a selected-node ordinary application log reader.
func NewManagementApplicationLogReader(node ManagementApplicationLogRPCNode, local LocalApplicationLogReader) *ManagementApplicationLogReader {
	return &ManagementApplicationLogReader{
		node:   node,
		local:  local,
		remote: accessnode.NewClient(node),
	}
}

// ApplicationLogSources returns ordinary application log sources for the selected node.
func (r *ManagementApplicationLogReader) ApplicationLogSources(ctx context.Context, req managementusecase.ApplicationLogSourcesRequest) (managementusecase.ApplicationLogSourcesResponse, error) {
	if r == nil {
		return managementusecase.ApplicationLogSourcesResponse{}, managementusecase.ErrApplicationLogReaderUnavailable
	}
	if r.isLocal(req.NodeID) {
		if r.local == nil {
			return managementusecase.ApplicationLogSourcesResponse{}, managementusecase.ErrApplicationLogReaderUnavailable
		}
		return r.local.ApplicationLogSources(ctx, req)
	}
	if r.remote == nil {
		return managementusecase.ApplicationLogSourcesResponse{}, managementusecase.ErrApplicationLogReaderUnavailable
	}
	return r.remote.GetManagerApplicationLogSources(ctx, req)
}

// ApplicationLogEntries returns one ordinary application log page for the selected node.
func (r *ManagementApplicationLogReader) ApplicationLogEntries(ctx context.Context, req managementusecase.ApplicationLogEntriesRequest) (managementusecase.ApplicationLogEntriesResponse, error) {
	if r == nil {
		return managementusecase.ApplicationLogEntriesResponse{}, managementusecase.ErrApplicationLogReaderUnavailable
	}
	if r.isLocal(req.NodeID) {
		if r.local == nil {
			return managementusecase.ApplicationLogEntriesResponse{}, managementusecase.ErrApplicationLogReaderUnavailable
		}
		return r.local.ApplicationLogEntries(ctx, req)
	}
	if r.remote == nil {
		return managementusecase.ApplicationLogEntriesResponse{}, managementusecase.ErrApplicationLogReaderUnavailable
	}
	return r.remote.GetManagerApplicationLogEntries(ctx, req)
}

func (r *ManagementApplicationLogReader) isLocal(nodeID uint64) bool {
	return nodeID == 0 || r.node == nil || nodeID == r.node.NodeID()
}
