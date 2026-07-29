package controller

import (
	"context"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/controller/command"
)

// ReplaceScheduledBackupState proposes one revision-fenced replacement of the
// complete bounded scheduled-backup subsystem state.
func (r *Runtime) ReplaceScheduledBackupState(
	ctx context.Context,
	expectedRevision uint64,
	replacement ScheduledBackupState,
) error {
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
	value := replacement.Clone()
	_, err := r.raft.ProposeResult(ctx, command.Command{
		Kind:             command.KindReplaceScheduledBackupState,
		IssuedAt:         nowFunc().UTC(),
		ExpectedRevision: &expectedRevision,
		ScheduledBackup:  &value,
	})
	if err != nil {
		return err
	}
	return r.publishFromState(ctx)
}
