package controller

import (
	"context"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/controller/command"
)

// ReplaceRestoreCoordinationState proposes one bounded revision-fenced recovery state replacement.
func (r *Runtime) ReplaceRestoreCoordinationState(ctx context.Context, expectedRevision uint64, replacement RestoreCoordinationState) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if r == nil || r.raft == nil {
		return ErrNotStarted
	}
	nowFunc := r.cfg.Now
	if nowFunc == nil {
		nowFunc = time.Now
	}
	restore := replacement.Clone()
	_, err := r.raft.ProposeResult(ctx, command.Command{
		Kind: command.KindReplaceRestoreCoordinationState, IssuedAt: nowFunc().UTC(),
		ExpectedRevision: &expectedRevision, Restore: &restore,
	})
	if err != nil {
		return err
	}
	return r.publishFromState(ctx)
}
