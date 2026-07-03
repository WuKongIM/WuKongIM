package cluster

import (
	"context"

	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
)

// ManagementNodeLifecycleNode exposes Controller-backed node lifecycle writes.
type ManagementNodeLifecycleNode interface {
	// JoinNode submits a data-node join intent to cluster control.
	JoinNode(context.Context, control.JoinNodeRequest) (control.JoinNodeResult, error)
	// ActivateNode submits a node activation intent to cluster control.
	ActivateNode(context.Context, control.ActivateNodeRequest) (control.ActivateNodeResult, error)
	// MarkNodeLeaving submits a node leaving intent to cluster control.
	MarkNodeLeaving(context.Context, control.MarkNodeLeavingRequest) (control.MarkNodeLeavingResult, error)
	// MarkNodeRemoved submits a node removed intent to cluster control.
	MarkNodeRemoved(context.Context, control.MarkNodeRemovedRequest) (control.MarkNodeRemovedResult, error)
	// PromoteControllerVoter submits a Controller voter promotion intent to cluster control.
	PromoteControllerVoter(context.Context, control.PromoteControllerVoterRequest) (control.PromoteControllerVoterResult, error)
}

// ManagementNodeLifecycleAdapter adapts cluster lifecycle writes to management usecases.
type ManagementNodeLifecycleAdapter struct {
	node ManagementNodeLifecycleNode
}

// NewManagementNodeLifecycleAdapter creates a node lifecycle writer.
func NewManagementNodeLifecycleAdapter(node ManagementNodeLifecycleNode) *ManagementNodeLifecycleAdapter {
	return &ManagementNodeLifecycleAdapter{node: node}
}

// JoinNode submits a validated data-node join request.
func (a *ManagementNodeLifecycleAdapter) JoinNode(ctx context.Context, req control.JoinNodeRequest) (control.JoinNodeResult, error) {
	if a == nil || a.node == nil {
		return control.JoinNodeResult{}, managementusecase.ErrNodeLifecycleUnavailable
	}
	return a.node.JoinNode(ctx, req)
}

// ActivateNode submits a validated node activation request.
func (a *ManagementNodeLifecycleAdapter) ActivateNode(ctx context.Context, req control.ActivateNodeRequest) (control.ActivateNodeResult, error) {
	if a == nil || a.node == nil {
		return control.ActivateNodeResult{}, managementusecase.ErrNodeLifecycleUnavailable
	}
	return a.node.ActivateNode(ctx, req)
}

// MarkNodeLeaving submits a validated node leaving request.
func (a *ManagementNodeLifecycleAdapter) MarkNodeLeaving(ctx context.Context, req control.MarkNodeLeavingRequest) (control.MarkNodeLeavingResult, error) {
	if a == nil || a.node == nil {
		return control.MarkNodeLeavingResult{}, managementusecase.ErrNodeLifecycleUnavailable
	}
	return a.node.MarkNodeLeaving(ctx, req)
}

// MarkNodeRemoved submits a validated node removed request.
func (a *ManagementNodeLifecycleAdapter) MarkNodeRemoved(ctx context.Context, req control.MarkNodeRemovedRequest) (control.MarkNodeRemovedResult, error) {
	if a == nil || a.node == nil {
		return control.MarkNodeRemovedResult{}, managementusecase.ErrNodeLifecycleUnavailable
	}
	return a.node.MarkNodeRemoved(ctx, req)
}

// PromoteControllerVoter submits a validated Controller voter promotion request.
func (a *ManagementNodeLifecycleAdapter) PromoteControllerVoter(ctx context.Context, req control.PromoteControllerVoterRequest) (control.PromoteControllerVoterResult, error) {
	if a == nil || a.node == nil {
		return control.PromoteControllerVoterResult{}, managementusecase.ErrControllerVoterPromotionUnavailable
	}
	return a.node.PromoteControllerVoter(ctx, req)
}
