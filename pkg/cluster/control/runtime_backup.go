package control

import (
	"context"

	controller "github.com/WuKongIM/WuKongIM/pkg/controller"
)

// LocalControllerState returns the exact Controller state used by bounded backup coordination.
func (r *Runtime) LocalControllerState(ctx context.Context) (controller.ClusterState, error) {
	if err := ctxErr(ctx); err != nil {
		return controller.ClusterState{}, err
	}
	if r == nil || r.backend == nil {
		return controller.ClusterState{}, controller.ErrNotStarted
	}
	return r.backend.LocalState(ctx)
}

// ReplaceScheduledBackupState proposes one revision-fenced simplified backup
// state replacement and transparently forwards from non-leader nodes.
func (r *Runtime) ReplaceScheduledBackupState(
	ctx context.Context,
	expectedRevision uint64,
	replacement controller.ScheduledBackupState,
) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if r == nil || r.backend == nil {
		return controller.ErrNotStarted
	}
	request := ControlWriteRequest{
		Action: ControlWriteActionReplaceScheduledBackup,
		ReplaceScheduledBackup: ReplaceScheduledBackupRequest{
			ExpectedRevision: expectedRevision,
			Replacement:      replacement,
		},
	}
	if r.canForwardControlWriteToLeader() {
		_, err := r.forwardControlWrite(ctx, request)
		return err
	}
	err := r.backend.ReplaceScheduledBackupState(
		ctx, expectedRevision, replacement,
	)
	if shouldForwardControlWrite(err) {
		_, forwardErr := r.forwardControlWriteAfterError(ctx, request, err)
		return forwardErr
	}
	return err
}
