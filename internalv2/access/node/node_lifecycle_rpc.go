package node

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/clusterv2/net"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

// NodeLifecycleRPCServiceID is the clusterv2 RPC service for seed join and readiness probes.
const NodeLifecycleRPCServiceID uint8 = clusternet.RPCNodeLifecycle

const (
	nodeLifecycleOpJoin      = "join"
	nodeLifecycleOpReadiness = "readiness"

	nodeLifecycleStatusConflict        = "conflict"
	nodeLifecycleStatusInvalidArgument = "invalid_argument"
)

var (
	nodeLifecycleRequestMagic  = [...]byte{'W', 'K', 'V', 'N', 1}
	nodeLifecycleResponseMagic = [...]byte{'W', 'K', 'V', 'n', 1}
)

// NodeJoinRequest is the seed-join payload sent by a startup joining node.
type NodeJoinRequest struct {
	// NodeID is the stable identity of the joining node.
	NodeID uint64 `json:"node_id"`
	// AdvertiseAddr is the stable cluster RPC address stored in membership.
	AdvertiseAddr string `json:"advertise_addr"`
	// ClusterID is the expected cluster identity for the seed.
	ClusterID string `json:"cluster_id"`
	// JoinToken authenticates the pre-membership join request.
	JoinToken string `json:"join_token"`
	// CapacityWeight is the planner placement weight requested by the joining node.
	CapacityWeight uint32 `json:"capacity_weight,omitempty"`
}

// NodeReadinessRequest asks one node for app-local startup readiness.
type NodeReadinessRequest struct {
	// NodeID is the joining node identity being probed.
	NodeID uint64 `json:"node_id"`
	// ClusterID is the expected cluster identity for the probe.
	ClusterID string `json:"cluster_id"`
}

// NodeReadinessResponse reports whether a joining node is ready for later activation gates.
type NodeReadinessResponse struct {
	// NodeID is the probed node identity.
	NodeID uint64 `json:"node_id"`
	// ClusterID is the local cluster identity observed by the probe handler.
	ClusterID string `json:"cluster_id"`
	// Reachable reports whether the node RPC handler is reachable.
	Reachable bool `json:"reachable"`
	// MirrorRevision is the latest local control mirror revision observed by the node.
	MirrorRevision uint64 `json:"mirror_revision"`
	// MirrorClusterID is the cluster identity in the local control mirror.
	MirrorClusterID string `json:"mirror_cluster_id"`
	// TransportReady reports whether node-to-node transport is serving.
	TransportReady bool `json:"transport_ready"`
	// ControlReady reports whether the control mirror has usable state.
	ControlReady bool `json:"control_ready"`
	// RuntimeReady reports whether local app runtimes are ready.
	RuntimeReady bool `json:"runtime_ready"`
	// Ready reports aggregate readiness for later activation gates.
	Ready bool `json:"ready"`
	// LastError carries a compact diagnostic for the last readiness failure.
	LastError string `json:"last_error,omitempty"`
}

type nodeLifecycleRPCRequest struct {
	Op        string               `json:"op"`
	Join      NodeJoinRequest      `json:"join,omitempty"`
	Readiness NodeReadinessRequest `json:"readiness,omitempty"`
}

type nodeLifecycleRPCResponse struct {
	Status    string                             `json:"status"`
	Join      managementusecase.JoinNodeResponse `json:"join,omitempty"`
	Readiness NodeReadinessResponse              `json:"readiness,omitempty"`
}

// HandleNodeLifecycleRPC handles one encoded seed lifecycle RPC payload.
func (a *Adapter) HandleNodeLifecycleRPC(ctx context.Context, payload []byte) ([]byte, error) {
	req, err := decodeNodeLifecycleRequest(payload)
	if err != nil {
		a.rpcLogger().Warn("node lifecycle rpc decode failed",
			wklog.Event("internalv2.access.node.lifecycle_decode_failed"),
			wklog.Int("payloadBytes", len(payload)),
			wklog.Error(err),
		)
		return nil, err
	}
	switch req.Op {
	case nodeLifecycleOpJoin:
		return a.handleNodeLifecycleJoin(ctx, req.Join)
	case nodeLifecycleOpReadiness:
		return a.handleNodeLifecycleReadiness(ctx, req.Readiness)
	default:
		err := fmt.Errorf("internalv2/access/node: unknown node lifecycle op %q", req.Op)
		a.rpcLogger().Warn("node lifecycle rpc unknown operation",
			wklog.Event("internalv2.access.node.lifecycle_unknown_op"),
			wklog.String("op", req.Op),
			wklog.Error(err),
		)
		return nil, err
	}
}

// JoinNode submits a seed-join request to seedNodeID.
func (c *Client) JoinNode(ctx context.Context, seedNodeID uint64, req NodeJoinRequest) (managementusecase.JoinNodeResponse, error) {
	resp, err := c.callNodeLifecycle(ctx, seedNodeID, nodeLifecycleRPCRequest{Op: nodeLifecycleOpJoin, Join: req})
	if err != nil {
		return managementusecase.JoinNodeResponse{}, err
	}
	if err := nodeLifecycleRPCErrorForStatus(resp.Status); err != nil {
		return managementusecase.JoinNodeResponse{}, err
	}
	return resp.Join, nil
}

// NodeReadiness probes nodeID for app-local startup readiness.
func (c *Client) NodeReadiness(ctx context.Context, nodeID uint64, req NodeReadinessRequest) (NodeReadinessResponse, error) {
	resp, err := c.callNodeLifecycle(ctx, nodeID, nodeLifecycleRPCRequest{Op: nodeLifecycleOpReadiness, Readiness: req})
	if err != nil {
		return NodeReadinessResponse{}, err
	}
	if err := nodeLifecycleRPCErrorForStatus(resp.Status); err != nil {
		return NodeReadinessResponse{}, err
	}
	return resp.Readiness, nil
}

func (a *Adapter) handleNodeLifecycleJoin(ctx context.Context, req NodeJoinRequest) ([]byte, error) {
	if a == nil || a.nodeLifecycle == nil {
		return encodeNodeLifecycleResponse(nodeLifecycleRPCResponse{Status: rpcStatusRejected})
	}
	if err := a.validateNodeLifecycleJoin(req); err != nil {
		a.rpcLogger().Warn("node lifecycle join rejected",
			wklog.Event("internalv2.access.node.lifecycle_join_rejected"),
			wklog.Uint64("nodeID", req.NodeID),
			wklog.String("clusterID", req.ClusterID),
			wklog.Error(err),
		)
		return encodeNodeLifecycleResponse(nodeLifecycleRPCResponse{Status: nodeLifecycleRPCStatusForError(err)})
	}
	resp, err := a.nodeLifecycle.JoinNode(ctx, managementusecase.JoinNodeRequest{
		NodeID:         req.NodeID,
		Addr:           req.AdvertiseAddr,
		CapacityWeight: req.CapacityWeight,
	})
	status := nodeLifecycleRPCStatusForError(err)
	a.logNodeLifecycleError(nodeLifecycleOpJoin, req.NodeID, status, err)
	return encodeNodeLifecycleResponse(nodeLifecycleRPCResponse{Status: status, Join: resp})
}

func (a *Adapter) handleNodeLifecycleReadiness(ctx context.Context, req NodeReadinessRequest) ([]byte, error) {
	if a == nil || a.nodeReadiness == nil {
		return encodeNodeLifecycleResponse(nodeLifecycleRPCResponse{Status: rpcStatusRejected})
	}
	if expected := strings.TrimSpace(a.nodeLifecycleClusterID); expected != "" && strings.TrimSpace(req.ClusterID) != expected {
		return encodeNodeLifecycleResponse(nodeLifecycleRPCResponse{Status: nodeLifecycleStatusInvalidArgument})
	}
	resp, err := a.nodeReadiness.NodeReadiness(ctx, req)
	status := nodeLifecycleRPCStatusForError(err)
	a.logNodeLifecycleError(nodeLifecycleOpReadiness, req.NodeID, status, err)
	return encodeNodeLifecycleResponse(nodeLifecycleRPCResponse{Status: status, Readiness: resp})
}

func (a *Adapter) validateNodeLifecycleJoin(req NodeJoinRequest) error {
	if expected := strings.TrimSpace(a.nodeLifecycleClusterID); expected != "" && strings.TrimSpace(req.ClusterID) != expected {
		return metadb.ErrInvalidArgument
	}
	if expected := a.nodeLifecycleJoinToken; expected != "" && req.JoinToken != expected {
		return metadb.ErrInvalidArgument
	}
	return nil
}

func (c *Client) callNodeLifecycle(ctx context.Context, nodeID uint64, req nodeLifecycleRPCRequest) (nodeLifecycleRPCResponse, error) {
	if c == nil || c.node == nil {
		return nodeLifecycleRPCResponse{}, fmt.Errorf("internalv2/access/node: node lifecycle rpc client not configured")
	}
	body, err := encodeNodeLifecycleRequest(req)
	if err != nil {
		return nodeLifecycleRPCResponse{}, err
	}
	respBody, err := c.node.CallRPC(ctx, nodeID, NodeLifecycleRPCServiceID, body)
	if err != nil {
		return nodeLifecycleRPCResponse{}, err
	}
	return decodeNodeLifecycleResponse(respBody)
}

func encodeNodeLifecycleRequest(req nodeLifecycleRPCRequest) ([]byte, error) {
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	dst := make([]byte, 0, len(nodeLifecycleRequestMagic)+len(payload))
	dst = append(dst, nodeLifecycleRequestMagic[:]...)
	return append(dst, payload...), nil
}

func decodeNodeLifecycleRequest(body []byte) (nodeLifecycleRPCRequest, error) {
	if !hasMagic(body, nodeLifecycleRequestMagic[:]) {
		return nodeLifecycleRPCRequest{}, fmt.Errorf("internalv2/access/node: invalid node lifecycle request codec")
	}
	var req nodeLifecycleRPCRequest
	if err := json.Unmarshal(body[len(nodeLifecycleRequestMagic):], &req); err != nil {
		return nodeLifecycleRPCRequest{}, err
	}
	return req, nil
}

func encodeNodeLifecycleResponse(resp nodeLifecycleRPCResponse) ([]byte, error) {
	payload, err := json.Marshal(resp)
	if err != nil {
		return nil, err
	}
	dst := make([]byte, 0, len(nodeLifecycleResponseMagic)+len(payload))
	dst = append(dst, nodeLifecycleResponseMagic[:]...)
	return append(dst, payload...), nil
}

func decodeNodeLifecycleResponse(body []byte) (nodeLifecycleRPCResponse, error) {
	if !hasMagic(body, nodeLifecycleResponseMagic[:]) {
		return nodeLifecycleRPCResponse{}, fmt.Errorf("internalv2/access/node: invalid node lifecycle response codec")
	}
	var resp nodeLifecycleRPCResponse
	if err := json.Unmarshal(body[len(nodeLifecycleResponseMagic):], &resp); err != nil {
		return nodeLifecycleRPCResponse{}, err
	}
	return resp, nil
}

func nodeLifecycleRPCStatusForError(err error) string {
	switch {
	case err == nil:
		return rpcStatusOK
	case errors.Is(err, context.Canceled):
		return rpcStatusContextCanceled
	case errors.Is(err, context.DeadlineExceeded):
		return rpcStatusContextDeadlineExceeded
	case errors.Is(err, metadb.ErrInvalidArgument):
		return nodeLifecycleStatusInvalidArgument
	case errors.Is(err, managementusecase.ErrNodeLifecycleConflict):
		return nodeLifecycleStatusConflict
	case errors.Is(err, managementusecase.ErrNodeLifecycleNotFound):
		return rpcStatusNotFound
	case errors.Is(err, managementusecase.ErrNodeLifecycleUnavailable):
		return rpcStatusRejected
	default:
		return rpcStatusRejected
	}
}

func nodeLifecycleRPCErrorForStatus(status string) error {
	switch status {
	case rpcStatusOK:
		return nil
	case rpcStatusContextCanceled:
		return context.Canceled
	case rpcStatusContextDeadlineExceeded:
		return context.DeadlineExceeded
	case nodeLifecycleStatusInvalidArgument:
		return metadb.ErrInvalidArgument
	case nodeLifecycleStatusConflict:
		return managementusecase.ErrNodeLifecycleConflict
	case rpcStatusNotFound:
		return managementusecase.ErrNodeLifecycleNotFound
	case rpcStatusRejected:
		return managementusecase.ErrNodeLifecycleUnavailable
	default:
		return fmt.Errorf("internalv2/access/node: unknown node lifecycle rpc status %q", status)
	}
}

func (a *Adapter) logNodeLifecycleError(op string, nodeID uint64, status string, err error) {
	if err == nil || status == rpcStatusOK {
		return
	}
	a.rpcLogger().Warn("node lifecycle rpc failed",
		wklog.Event("internalv2.access.node.lifecycle_failed"),
		wklog.String("op", op),
		wklog.String("status", status),
		wklog.Uint64("nodeID", nodeID),
		wklog.Error(err),
	)
}
