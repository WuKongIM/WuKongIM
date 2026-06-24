package cluster

import (
	"context"

	accessnode "github.com/WuKongIM/WuKongIM/internalv2/access/node"
	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
)

// NodeLifecycleNode exposes clusterv2 node RPC for seed join and readiness probes.
type NodeLifecycleNode interface {
	// NodeID returns the local cluster node ID.
	NodeID() uint64
	// CallRPC invokes one typed node RPC service on a peer node.
	CallRPC(context.Context, uint64, uint8, []byte) ([]byte, error)
}

// NodeLifecycleClient routes seed join and readiness requests over node RPC.
type NodeLifecycleClient struct {
	remote *accessnode.Client
}

// NewNodeLifecycleClient creates a cluster-routed node lifecycle client.
func NewNodeLifecycleClient(node NodeLifecycleNode) *NodeLifecycleClient {
	return &NodeLifecycleClient{remote: accessnode.NewClient(node)}
}

// JoinNode asks one seed node to submit this node's join intent.
func (c *NodeLifecycleClient) JoinNode(ctx context.Context, seedNodeID uint64, req accessnode.NodeJoinRequest) (managementusecase.JoinNodeResponse, error) {
	if c == nil || c.remote == nil {
		return managementusecase.JoinNodeResponse{}, managementusecase.ErrNodeLifecycleUnavailable
	}
	return c.remote.JoinNode(ctx, seedNodeID, req)
}

// NodeReadiness probes one node's app-local startup readiness.
func (c *NodeLifecycleClient) NodeReadiness(ctx context.Context, nodeID uint64, req accessnode.NodeReadinessRequest) (accessnode.NodeReadinessResponse, error) {
	if c == nil || c.remote == nil {
		return accessnode.NodeReadinessResponse{}, managementusecase.ErrNodeLifecycleUnavailable
	}
	return c.remote.NodeReadiness(ctx, nodeID, req)
}
