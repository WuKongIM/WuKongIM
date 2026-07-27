package control

import (
	"context"

	controller "github.com/WuKongIM/WuKongIM/pkg/controller"
)

// ReplaceOpsMCPState commits one bounded MCP desired-state replacement.
func (r *Runtime) ReplaceOpsMCPState(ctx context.Context, expectedRevision uint64, replacement controller.OpsMCPState) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if r == nil || r.backend == nil {
		return controller.ErrNotStarted
	}
	request := ControlWriteRequest{
		Action: ControlWriteActionReplaceOpsMCP,
		ReplaceOpsMCP: ReplaceOpsMCPRequest{
			ExpectedRevision: expectedRevision,
			State:            replacement.Clone(),
		},
	}
	if r.canForwardControlWriteToLeader() {
		_, err := r.forwardControlWrite(ctx, request)
		return err
	}
	err := r.backend.ReplaceOpsMCPState(ctx, expectedRevision, replacement)
	if shouldForwardControlWrite(err) {
		_, forwardErr := r.forwardControlWriteAfterError(ctx, request, err)
		return forwardErr
	}
	return err
}
