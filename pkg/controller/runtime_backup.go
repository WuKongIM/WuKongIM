package controller

import (
	"context"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/controller/command"
)

// ReplaceBackupCoordinationState proposes one bounded revision-fenced backup state replacement.
func (r *Runtime) ReplaceBackupCoordinationState(ctx context.Context, expectedRevision uint64, replacement BackupCoordinationState) error {
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
	now := nowFunc().UTC()
	backup := replacement.Clone()
	_, err := r.raft.ProposeResult(ctx, command.Command{
		Kind:             command.KindReplaceBackupCoordinationState,
		IssuedAt:         now,
		ExpectedRevision: &expectedRevision,
		Backup:           &backup,
	})
	if err != nil {
		return err
	}
	return r.publishFromState(ctx)
}
