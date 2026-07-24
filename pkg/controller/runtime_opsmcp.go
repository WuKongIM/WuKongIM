package controller

import (
	"context"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/controller/command"
)

// ReplaceOpsMCPState proposes one bounded revision-fenced MCP desired-state replacement.
func (r *Runtime) ReplaceOpsMCPState(ctx context.Context, expectedRevision uint64, replacement OpsMCPState) error {
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
	opsMCP := replacement.Clone()
	_, err := r.raft.ProposeResult(ctx, command.Command{
		Kind:             command.KindReplaceOpsMCPState,
		IssuedAt:         nowFunc().UTC(),
		ExpectedRevision: &expectedRevision,
		OpsMCP:           &opsMCP,
	})
	if err != nil {
		return err
	}
	return r.publishFromState(ctx)
}
