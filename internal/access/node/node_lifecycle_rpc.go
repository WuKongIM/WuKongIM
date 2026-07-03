package node

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
	cv2 "github.com/WuKongIM/WuKongIM/pkg/controller"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

// NodeLifecycleRPCServiceID is the cluster RPC service for seed join and readiness probes.
const NodeLifecycleRPCServiceID uint8 = clusternet.RPCNodeLifecycle

const (
	nodeLifecycleOpJoin                     = "join"
	nodeLifecycleOpReadiness                = "readiness"
	nodeLifecycleOpControllerVoterReadiness = "controller_voter_readiness"
	nodeLifecycleOpPrepareControllerVoter   = "prepare_controller_voter"

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
	// ExpectedClusterID is the configured cluster identity expected by the probed node.
	ExpectedClusterID string `json:"expected_cluster_id"`
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
	// Unknown reports that readiness could not be determined.
	Unknown bool `json:"unknown,omitempty"`
	// LastError carries a compact diagnostic for the last readiness failure.
	LastError string `json:"last_error,omitempty"`
}

// ControllerVoterReadinessRequest asks a target node whether it can prepare Controller voter promotion.
type ControllerVoterReadinessRequest struct {
	// NodeID is the target node identity being probed.
	NodeID uint64 `json:"node_id"`
	// ClusterID is the expected ControllerV2 cluster identity.
	ClusterID string `json:"cluster_id"`
}

// ControllerVoterReadinessResponse reports target readiness for Controller voter preparation.
type ControllerVoterReadinessResponse struct {
	// NodeID is the probed node identity.
	NodeID uint64 `json:"node_id"`
	// ClusterID is the ControllerV2 cluster identity observed in the target mirror.
	ClusterID string `json:"cluster_id"`
	// Reachable reports whether the node RPC handler is reachable.
	Reachable bool `json:"reachable"`
	// TransportReady reports whether node-to-node transport is serving.
	TransportReady bool `json:"transport_ready"`
	// ControlReady reports whether the control mirror has usable state.
	ControlReady bool `json:"control_ready"`
	// RuntimeReady reports whether local app runtimes are ready.
	RuntimeReady bool `json:"runtime_ready"`
	// CanPrepare reports whether the target can attempt Controller voter preparation.
	CanPrepare bool `json:"can_prepare"`
	// MirrorRevision is the latest local control mirror revision observed by the target.
	MirrorRevision uint64 `json:"mirror_revision"`
	// IsVoter reports whether the target currently observes itself in the Controller Raft voter set.
	IsVoter bool `json:"is_voter"`
	// ControlLeaderID is the Controller Raft leader observed by the target when known.
	ControlLeaderID uint64 `json:"control_leader_id"`
	// ConfigIndex is the target's observed Controller Raft applied config index.
	ConfigIndex uint64 `json:"config_index"`
	// Voters is the Controller Raft voter set observed by the target.
	Voters []uint64 `json:"voters"`
	// Unknown reports that readiness could not be determined.
	Unknown bool `json:"unknown,omitempty"`
	// LastError carries a compact diagnostic for the last readiness failure.
	LastError string `json:"last_error,omitempty"`
}

// ControllerVoter identifies one Controller voter endpoint in a preparation request.
type ControllerVoter struct {
	// NodeID is the Controller voter node identity.
	NodeID uint64 `json:"node_id"`
	// Addr is the stable Controller RPC address for the voter.
	Addr string `json:"addr"`
}

// PrepareControllerVoterRequest asks a target node to prepare for Controller voter promotion.
type PrepareControllerVoterRequest struct {
	// NodeID is the target node identity being prepared.
	NodeID uint64 `json:"node_id"`
	// ClusterID is the expected ControllerV2 cluster identity.
	ClusterID string `json:"cluster_id"`
	// ExpectedRevision is the minimum mirrored control revision required before preparing.
	ExpectedRevision uint64 `json:"expected_revision"`
	// NextVoters is the complete Controller voter endpoint set after promotion.
	NextVoters []ControllerVoter `json:"next_voters"`
}

// PrepareControllerVoterResponse reports target preparation and best-effort local Controller Raft status.
type PrepareControllerVoterResponse struct {
	// NodeID is the node that handled preparation.
	NodeID uint64 `json:"node_id"`
	// Prepared reports whether the target is ready to receive Controller Raft traffic.
	Prepared bool `json:"prepared"`
	// StateRevision is the mirrored control-state revision preserved before Raft startup.
	StateRevision uint64 `json:"state_revision"`
	// ObservedConfigIndex is the local Controller Raft applied config index when already visible.
	ObservedConfigIndex uint64 `json:"observed_config_index"`
	// ObservedVoters is the local Controller Raft voter set when already visible.
	ObservedVoters []uint64 `json:"observed_voters"`
}

type nodeLifecycleRPCRequest struct {
	Op                       string                          `json:"op"`
	Join                     NodeJoinRequest                 `json:"join,omitempty"`
	Readiness                NodeReadinessRequest            `json:"readiness,omitempty"`
	ControllerVoterReadiness ControllerVoterReadinessRequest `json:"controller_voter_readiness,omitempty"`
	PrepareControllerVoter   PrepareControllerVoterRequest   `json:"prepare_controller_voter,omitempty"`
}

type nodeLifecycleRPCResponse struct {
	Status                   string                             `json:"status"`
	Join                     managementusecase.JoinNodeResponse `json:"join,omitempty"`
	Readiness                NodeReadinessResponse              `json:"readiness,omitempty"`
	ControllerVoterReadiness ControllerVoterReadinessResponse   `json:"controller_voter_readiness,omitempty"`
	PrepareControllerVoter   PrepareControllerVoterResponse     `json:"prepare_controller_voter,omitempty"`
}

// HandleNodeLifecycleRPC handles one encoded seed lifecycle RPC payload.
func (a *Adapter) HandleNodeLifecycleRPC(ctx context.Context, payload []byte) ([]byte, error) {
	req, err := decodeNodeLifecycleRequest(payload)
	if err != nil {
		a.rpcLogger().Warn("node lifecycle rpc decode failed",
			wklog.Event("internal.access.node.lifecycle_decode_failed"),
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
	case nodeLifecycleOpControllerVoterReadiness:
		return a.handleNodeLifecycleControllerVoterReadiness(ctx, req.ControllerVoterReadiness)
	case nodeLifecycleOpPrepareControllerVoter:
		return a.handleNodeLifecyclePrepareControllerVoter(ctx, req.PrepareControllerVoter)
	default:
		err := fmt.Errorf("internal/access/node: unknown node lifecycle op %q", req.Op)
		a.rpcLogger().Warn("node lifecycle rpc unknown operation",
			wklog.Event("internal.access.node.lifecycle_unknown_op"),
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

// ControllerVoterReadiness probes nodeID for Controller voter preparation readiness.
func (c *Client) ControllerVoterReadiness(ctx context.Context, nodeID uint64, req ControllerVoterReadinessRequest) (ControllerVoterReadinessResponse, error) {
	resp, err := c.callNodeLifecycle(ctx, nodeID, nodeLifecycleRPCRequest{Op: nodeLifecycleOpControllerVoterReadiness, ControllerVoterReadiness: req})
	if err != nil {
		return ControllerVoterReadinessResponse{}, err
	}
	if err := controllerVoterRPCErrorForStatus(resp.Status); err != nil {
		return ControllerVoterReadinessResponse{}, err
	}
	return resp.ControllerVoterReadiness, nil
}

// PrepareControllerVoter asks nodeID to prepare for Controller voter promotion.
func (c *Client) PrepareControllerVoter(ctx context.Context, nodeID uint64, req PrepareControllerVoterRequest) (PrepareControllerVoterResponse, error) {
	resp, err := c.callNodeLifecycle(ctx, nodeID, nodeLifecycleRPCRequest{Op: nodeLifecycleOpPrepareControllerVoter, PrepareControllerVoter: req})
	if err != nil {
		return PrepareControllerVoterResponse{}, err
	}
	if err := controllerVoterRPCErrorForStatus(resp.Status); err != nil {
		return PrepareControllerVoterResponse{}, err
	}
	return resp.PrepareControllerVoter, nil
}

func (a *Adapter) handleNodeLifecycleJoin(ctx context.Context, req NodeJoinRequest) ([]byte, error) {
	if a == nil || a.nodeLifecycle == nil {
		return encodeNodeLifecycleResponse(nodeLifecycleRPCResponse{Status: rpcStatusRejected})
	}
	if err := a.validateNodeLifecycleJoin(req); err != nil {
		a.rpcLogger().Warn("node lifecycle join rejected",
			wklog.Event("internal.access.node.lifecycle_join_rejected"),
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

func (a *Adapter) handleNodeLifecycleControllerVoterReadiness(ctx context.Context, req ControllerVoterReadinessRequest) ([]byte, error) {
	if a == nil || a.controllerVoterReadiness == nil {
		return encodeNodeLifecycleResponse(nodeLifecycleRPCResponse{Status: rpcStatusRejected})
	}
	if expected := strings.TrimSpace(a.nodeLifecycleClusterID); expected != "" && strings.TrimSpace(req.ClusterID) != expected {
		return encodeNodeLifecycleResponse(nodeLifecycleRPCResponse{Status: nodeLifecycleStatusInvalidArgument})
	}
	resp, err := a.controllerVoterReadiness.ControllerVoterReadiness(ctx, req)
	status := controllerVoterRPCStatusForError(err)
	a.logNodeLifecycleError(nodeLifecycleOpControllerVoterReadiness, req.NodeID, status, err)
	return encodeNodeLifecycleResponse(nodeLifecycleRPCResponse{Status: status, ControllerVoterReadiness: resp})
}

func (a *Adapter) handleNodeLifecyclePrepareControllerVoter(ctx context.Context, req PrepareControllerVoterRequest) ([]byte, error) {
	if a == nil || a.controllerVoterPreparer == nil {
		return encodeNodeLifecycleResponse(nodeLifecycleRPCResponse{Status: rpcStatusRejected})
	}
	if expected := strings.TrimSpace(a.nodeLifecycleClusterID); expected != "" && strings.TrimSpace(req.ClusterID) != expected {
		return encodeNodeLifecycleResponse(nodeLifecycleRPCResponse{Status: nodeLifecycleStatusInvalidArgument})
	}
	resp, err := a.controllerVoterPreparer.PrepareControllerVoter(ctx, req)
	status := controllerVoterRPCStatusForError(err)
	a.logNodeLifecycleError(nodeLifecycleOpPrepareControllerVoter, req.NodeID, status, err)
	return encodeNodeLifecycleResponse(nodeLifecycleRPCResponse{Status: status, PrepareControllerVoter: resp})
}

func (a *Adapter) validateNodeLifecycleJoin(req NodeJoinRequest) error {
	if expected := strings.TrimSpace(a.nodeLifecycleClusterID); expected != "" && strings.TrimSpace(req.ClusterID) != expected {
		return metadb.ErrInvalidArgument
	}
	expectedToken := a.nodeLifecycleJoinToken
	if expectedToken == "" {
		return managementusecase.ErrNodeLifecycleUnavailable
	}
	if req.JoinToken != expectedToken {
		return metadb.ErrInvalidArgument
	}
	return nil
}

func (c *Client) callNodeLifecycle(ctx context.Context, nodeID uint64, req nodeLifecycleRPCRequest) (nodeLifecycleRPCResponse, error) {
	if c == nil || c.node == nil {
		return nodeLifecycleRPCResponse{}, fmt.Errorf("internal/access/node: node lifecycle rpc client not configured")
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
		return nodeLifecycleRPCRequest{}, fmt.Errorf("internal/access/node: invalid node lifecycle request codec")
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
		return nodeLifecycleRPCResponse{}, fmt.Errorf("internal/access/node: invalid node lifecycle response codec")
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

func controllerVoterRPCStatusForError(err error) string {
	switch {
	case err == nil:
		return rpcStatusOK
	case errors.Is(err, context.Canceled):
		return rpcStatusContextCanceled
	case errors.Is(err, context.DeadlineExceeded):
		return rpcStatusContextDeadlineExceeded
	case errors.Is(err, metadb.ErrInvalidArgument):
		return nodeLifecycleStatusInvalidArgument
	case errors.Is(err, managementusecase.ErrControllerVoterPromotionBlocked),
		cv2.IsExpectedRevisionMismatch(err),
		errors.Is(err, cv2.ErrProposalRejected):
		return nodeLifecycleStatusConflict
	case errors.Is(err, managementusecase.ErrControllerVoterPromotionUnavailable):
		return rpcStatusRejected
	default:
		return rpcStatusRejected
	}
}

func controllerVoterRPCErrorForStatus(status string) error {
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
		return managementusecase.ErrControllerVoterPromotionBlocked
	case rpcStatusRejected:
		return managementusecase.ErrControllerVoterPromotionUnavailable
	default:
		return fmt.Errorf("internal/access/node: unknown controller voter lifecycle rpc status %q", status)
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
		return fmt.Errorf("internal/access/node: unknown node lifecycle rpc status %q", status)
	}
}

func (a *Adapter) logNodeLifecycleError(op string, nodeID uint64, status string, err error) {
	if err == nil || status == rpcStatusOK {
		return
	}
	a.rpcLogger().Warn("node lifecycle rpc failed",
		wklog.Event("internal.access.node.lifecycle_failed"),
		wklog.String("op", op),
		wklog.String("status", status),
		wklog.Uint64("nodeID", nodeID),
		wklog.Error(err),
	)
}
