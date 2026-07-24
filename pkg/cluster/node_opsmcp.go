package cluster

import (
	"context"
	"fmt"

	controller "github.com/WuKongIM/WuKongIM/pkg/controller"
)

type opsMCPController interface {
	LocalControllerState(context.Context) (controller.ClusterState, error)
	ReplaceOpsMCPState(context.Context, uint64, controller.OpsMCPState) error
}

// LoadOpsMCPState returns the exact locally visible Controller state and revision.
func (n *Node) LoadOpsMCPState(ctx context.Context) (controller.ClusterState, error) {
	if err := ctxErr(ctx); err != nil {
		return controller.ClusterState{}, err
	}
	if n == nil || n.control == nil {
		return controller.ClusterState{}, ErrNotStarted
	}
	runtime, ok := n.control.(opsMCPController)
	if !ok {
		return controller.ClusterState{}, fmt.Errorf("cluster: operations MCP coordination is unsupported")
	}
	return runtime.LocalControllerState(ctx)
}

// ReplaceOpsMCPState commits one revision-fenced bounded MCP desired state.
func (n *Node) ReplaceOpsMCPState(ctx context.Context, expectedRevision uint64, replacement controller.OpsMCPState) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if n == nil || n.control == nil {
		return ErrNotStarted
	}
	runtime, ok := n.control.(opsMCPController)
	if !ok {
		return fmt.Errorf("cluster: operations MCP coordination is unsupported")
	}
	return normalizeControlWriteError(runtime.ReplaceOpsMCPState(ctx, expectedRevision, replacement))
}
