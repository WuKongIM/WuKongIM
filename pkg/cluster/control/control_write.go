package control

import (
	"context"
	"fmt"

	clusternet "github.com/WuKongIM/WuKongIM/pkg/cluster/net"
)

// ControlWriteApplier applies generic ControllerV2 writes.
type ControlWriteApplier interface {
	// ReportNode reports low-frequency local node state.
	ReportNode(context.Context, NodeReport) error
	// JoinNode submits a data-node join intent.
	JoinNode(context.Context, JoinNodeRequest) (JoinNodeResult, error)
	// ActivateNode submits a node activation intent.
	ActivateNode(context.Context, ActivateNodeRequest) (ActivateNodeResult, error)
	// MarkNodeLeaving submits a node leaving intent.
	MarkNodeLeaving(context.Context, MarkNodeLeavingRequest) (MarkNodeLeavingResult, error)
	// MarkNodeRemoved submits a node removed intent.
	MarkNodeRemoved(context.Context, MarkNodeRemovedRequest) (MarkNodeRemovedResult, error)
	// RequestSlotReplicaMove submits a staged Slot replica move intent.
	RequestSlotReplicaMove(context.Context, SlotReplicaMoveRequest) (SlotReplicaMoveResult, error)
	// PromoteControllerVoter submits an online Controller voter promotion.
	PromoteControllerVoter(context.Context, PromoteControllerVoterRequest) (PromoteControllerVoterResult, error)
}

// ControlWriteClient forwards generic ControllerV2 writes to a remote node.
type ControlWriteClient struct {
	caller clusternet.Caller
}

// NewControlWriteClient creates a generic control write RPC client.
func NewControlWriteClient(caller clusternet.Caller) *ControlWriteClient {
	return &ControlWriteClient{caller: caller}
}

// Submit sends one generic control write request to nodeID.
func (c *ControlWriteClient) Submit(ctx context.Context, nodeID uint64, req ControlWriteRequest) (ControlWriteResponse, error) {
	if c == nil || c.caller == nil {
		return ControlWriteResponse{}, fmt.Errorf("control write: caller is required")
	}
	payload, err := EncodeControlWriteRequest(req)
	if err != nil {
		return ControlWriteResponse{}, err
	}
	resp, err := clusternet.CallOwnedPayload(ctx, c.caller, nodeID, clusternet.RPCControlWrite, payload)
	if err != nil {
		return ControlWriteResponse{}, err
	}
	return DecodeControlWriteResponse(resp)
}

// NewControlWriteHandler creates an RPC handler for generic ControllerV2 writes.
func NewControlWriteHandler(applier ControlWriteApplier) clusternet.Handler {
	return clusternet.HandlerFunc(func(ctx context.Context, payload []byte) ([]byte, error) {
		req, err := DecodeControlWriteRequest(payload)
		if err != nil {
			return nil, err
		}
		if applier == nil {
			return nil, fmt.Errorf("control write: applier is required")
		}
		var resp ControlWriteResponse
		switch req.Action {
		case ControlWriteActionJoinNode:
			result, err := applier.JoinNode(ctx, req.JoinNode)
			if err != nil {
				return encodeControlWriteErrorResponse(err)
			}
			resp.JoinNode = result
		case ControlWriteActionActivateNode:
			result, err := applier.ActivateNode(ctx, req.ActivateNode)
			if err != nil {
				return encodeControlWriteErrorResponse(err)
			}
			resp.ActivateNode = result
		case ControlWriteActionMarkNodeLeaving:
			result, err := applier.MarkNodeLeaving(ctx, req.MarkNodeLeaving)
			if err != nil {
				return encodeControlWriteErrorResponse(err)
			}
			resp.MarkNodeLeaving = result
		case ControlWriteActionMarkNodeRemoved:
			result, err := applier.MarkNodeRemoved(ctx, req.MarkNodeRemoved)
			if err != nil {
				return encodeControlWriteErrorResponse(err)
			}
			resp.MarkNodeRemoved = result
		case ControlWriteActionSlotReplicaMove:
			result, err := applier.RequestSlotReplicaMove(ctx, req.SlotReplicaMove)
			if err != nil {
				return encodeControlWriteErrorResponse(err)
			}
			resp.SlotReplicaMove = result
		case ControlWriteActionPromoteControllerVoter:
			result, err := applier.PromoteControllerVoter(ctx, req.PromoteControllerVoter)
			if err != nil {
				return encodeControlWriteErrorResponse(err)
			}
			resp.PromoteControllerVoter = result
		case ControlWriteActionReportNodeHealth:
			if err := applier.ReportNode(ctx, req.ReportNodeHealth); err != nil {
				return encodeControlWriteErrorResponse(err)
			}
		default:
			return nil, fmt.Errorf("control write: unknown action %q", req.Action)
		}
		return EncodeControlWriteResponse(resp)
	})
}
