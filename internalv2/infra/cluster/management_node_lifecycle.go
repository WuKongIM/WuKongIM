package cluster

import (
	"context"

	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
	"github.com/WuKongIM/WuKongIM/pkg/clusterv2/control"
)

// ManagementNodeLifecycleNode exposes Controller-backed node lifecycle writes.
type ManagementNodeLifecycleNode interface {
	// JoinNode submits a data-node join intent to cluster control.
	JoinNode(context.Context, control.JoinNodeRequest) (control.JoinNodeResult, error)
	// ActivateNode submits a node activation intent to cluster control.
	ActivateNode(context.Context, control.ActivateNodeRequest) (control.ActivateNodeResult, error)
}

// ManagementNodeLifecycleAdapter adapts clusterv2 lifecycle writes to management usecases.
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
