package cluster

import (
	"context"
	"strings"

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
	remote    *accessnode.Client
	clusterID string
}

// NewNodeLifecycleClient creates a cluster-routed node lifecycle client.
func NewNodeLifecycleClient(node NodeLifecycleNode, clusterID ...string) *NodeLifecycleClient {
	expectedClusterID := ""
	if len(clusterID) > 0 {
		expectedClusterID = strings.TrimSpace(clusterID[0])
	}
	return &NodeLifecycleClient{remote: accessnode.NewClient(node), clusterID: expectedClusterID}
}

// JoinNode asks one seed node to submit this node's join intent.
func (c *NodeLifecycleClient) JoinNode(ctx context.Context, seedNodeID uint64, req accessnode.NodeJoinRequest) (managementusecase.JoinNodeResponse, error) {
	if c == nil || c.remote == nil {
		return managementusecase.JoinNodeResponse{}, managementusecase.ErrNodeLifecycleUnavailable
	}
	return c.remote.JoinNode(ctx, seedNodeID, req)
}

// NodeReadiness probes one node's app-local startup readiness.
func (c *NodeLifecycleClient) NodeReadiness(ctx context.Context, nodeID uint64) (managementusecase.NodeReadiness, error) {
	if c == nil || c.remote == nil {
		return managementusecase.NodeReadiness{}, managementusecase.ErrNodeLifecycleUnavailable
	}
	resp, err := c.remote.NodeReadiness(ctx, nodeID, accessnode.NodeReadinessRequest{
		NodeID:    nodeID,
		ClusterID: c.clusterID,
	})
	if err != nil {
		return managementusecase.NodeReadiness{}, err
	}
	return managementusecase.NodeReadiness{
		NodeID:            resp.NodeID,
		ExpectedClusterID: resp.ExpectedClusterID,
		MirrorClusterID:   resp.MirrorClusterID,
		MirrorRevision:    resp.MirrorRevision,
		Reachable:         resp.Reachable,
		TransportReady:    resp.TransportReady,
		ControlReady:      resp.ControlReady,
		RuntimeReady:      resp.RuntimeReady,
		Unknown:           resp.Unknown,
		LastError:         resp.LastError,
	}, nil
}

// ControllerVoterReadiness probes one node's readiness for Controller voter preparation.
func (c *NodeLifecycleClient) ControllerVoterReadiness(ctx context.Context, nodeID uint64) (managementusecase.ControllerVoterReadiness, error) {
	if c == nil || c.remote == nil {
		return managementusecase.ControllerVoterReadiness{}, managementusecase.ErrControllerVoterPromotionUnavailable
	}
	resp, err := c.remote.ControllerVoterReadiness(ctx, nodeID, accessnode.ControllerVoterReadinessRequest{
		NodeID:    nodeID,
		ClusterID: c.clusterID,
	})
	if err != nil {
		return managementusecase.ControllerVoterReadiness{}, err
	}
	return managementusecase.ControllerVoterReadiness{
		NodeID:          resp.NodeID,
		ClusterID:       resp.ClusterID,
		Reachable:       resp.Reachable,
		TransportReady:  resp.TransportReady,
		ControlReady:    resp.ControlReady,
		RuntimeReady:    resp.RuntimeReady,
		CanPrepare:      resp.CanPrepare,
		MirrorRevision:  resp.MirrorRevision,
		IsVoter:         resp.IsVoter,
		ControlLeaderID: resp.ControlLeaderID,
		ConfigIndex:     resp.ConfigIndex,
		Voters:          append([]uint64(nil), resp.Voters...),
		Unknown:         resp.Unknown,
		LastError:       resp.LastError,
	}, nil
}

// PrepareControllerVoter asks the target node to prepare for Controller voter promotion.
func (c *NodeLifecycleClient) PrepareControllerVoter(ctx context.Context, req managementusecase.PrepareControllerVoterRequest) (managementusecase.PrepareControllerVoterResponse, error) {
	if c == nil || c.remote == nil {
		return managementusecase.PrepareControllerVoterResponse{}, managementusecase.ErrControllerVoterPromotionUnavailable
	}
	nextVoters := make([]accessnode.ControllerVoter, 0, len(req.NextVoters))
	for _, voter := range req.NextVoters {
		nextVoters = append(nextVoters, accessnode.ControllerVoter{
			NodeID: voter.NodeID,
			Addr:   voter.Addr,
		})
	}
	resp, err := c.remote.PrepareControllerVoter(ctx, req.NodeID, accessnode.PrepareControllerVoterRequest{
		NodeID:           req.NodeID,
		ClusterID:        req.ClusterID,
		ExpectedRevision: req.ExpectedRevision,
		NextVoters:       nextVoters,
	})
	if err != nil {
		return managementusecase.PrepareControllerVoterResponse{}, err
	}
	return managementusecase.PrepareControllerVoterResponse{
		NodeID:              resp.NodeID,
		Prepared:            resp.Prepared,
		StateRevision:       resp.StateRevision,
		ObservedConfigIndex: resp.ObservedConfigIndex,
		ObservedVoters:      append([]uint64(nil), resp.ObservedVoters...),
	}, nil
}
