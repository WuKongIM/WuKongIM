package control

import (
	"context"

	controller "github.com/WuKongIM/WuKongIM/pkg/controller"
)

// ReplaceRestoreCoordinationState proposes one revision-fenced recovery state replacement.
func (r *Runtime) ReplaceRestoreCoordinationState(ctx context.Context, expectedRevision uint64, replacement controller.RestoreCoordinationState) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if r == nil || r.backend == nil {
		return controller.ErrNotStarted
	}
	req := ControlWriteRequest{
		Action: ControlWriteActionReplaceRestoreCoordination,
		ReplaceRestoreCoordination: ReplaceRestoreCoordinationRequest{
			ExpectedRevision: expectedRevision,
			Replacement:      replacement,
		},
	}
	if r.canForwardControlWriteToLeader() {
		_, err := r.forwardControlWrite(ctx, req)
		return err
	}
	err := r.backend.ReplaceRestoreCoordinationState(
		ctx, expectedRevision, replacement,
	)
	if shouldForwardControlWrite(err) {
		_, forwardErr := r.forwardControlWriteAfterError(ctx, req, err)
		return forwardErr
	}
	return err
}
