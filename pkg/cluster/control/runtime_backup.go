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

// ReplaceBackupCoordinationState proposes one revision-fenced backup state replacement.
func (r *Runtime) ReplaceBackupCoordinationState(ctx context.Context, expectedRevision uint64, replacement controller.BackupCoordinationState) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if r == nil || r.backend == nil {
		return controller.ErrNotStarted
	}
	return r.backend.ReplaceBackupCoordinationState(ctx, expectedRevision, replacement)
}
