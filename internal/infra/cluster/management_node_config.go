package cluster

import (
	"context"

	accessnode "github.com/WuKongIM/WuKongIM/internal/access/node"
	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
)

// ManagementNodeConfigRPCNode exposes cluster node RPC for manager node config reads.
type ManagementNodeConfigRPCNode interface {
	// NodeID returns the local cluster node ID.
	NodeID() uint64
	// CallRPC invokes one typed node RPC service on a peer node.
	CallRPC(context.Context, uint64, uint8, []byte) ([]byte, error)
}

// LocalNodeConfigReader reads this node's redacted effective startup config.
type LocalNodeConfigReader interface {
	// NodeConfigSnapshot returns this node's allowlisted effective startup config.
	NodeConfigSnapshot(context.Context, uint64) (managementusecase.NodeConfigSnapshot, error)
}

// ManagementNodeConfigReader routes node config reads to local or remote nodes.
type ManagementNodeConfigReader struct {
	// node is the local cluster node RPC facade.
	node ManagementNodeConfigRPCNode
	// local reads effective startup config from this node.
	local LocalNodeConfigReader
	// remote forwards effective startup config reads to peer nodes.
	remote *accessnode.Client
}

// NewManagementNodeConfigReader creates a selected-node config reader.
func NewManagementNodeConfigReader(node ManagementNodeConfigRPCNode, local LocalNodeConfigReader) *ManagementNodeConfigReader {
	return &ManagementNodeConfigReader{
		node:   node,
		local:  local,
		remote: accessnode.NewClient(node),
	}
}

// NodeConfigSnapshot returns one selected node's redacted effective startup config.
func (r *ManagementNodeConfigReader) NodeConfigSnapshot(ctx context.Context, nodeID uint64) (managementusecase.NodeConfigSnapshot, error) {
	if r == nil {
		return managementusecase.NodeConfigSnapshot{}, managementusecase.ErrNodeConfigUnavailable
	}
	if r.isLocal(nodeID) {
		if r.local == nil {
			return managementusecase.NodeConfigSnapshot{}, managementusecase.ErrNodeConfigUnavailable
		}
		return r.local.NodeConfigSnapshot(ctx, nodeID)
	}
	if r.remote == nil {
		return managementusecase.NodeConfigSnapshot{}, managementusecase.ErrNodeConfigUnavailable
	}
	return r.remote.GetManagerNodeConfig(ctx, nodeID)
}

func (r *ManagementNodeConfigReader) isLocal(nodeID uint64) bool {
	return r.node == nil || nodeID == r.node.NodeID()
}
