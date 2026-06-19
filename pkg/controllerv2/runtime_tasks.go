package controllerv2

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/controllerv2/command"
)

// CompleteTask proposes a fenced task completion result through ControllerV2 Raft.
func (r *Runtime) CompleteTask(ctx context.Context, result TaskResult) error {
	return r.proposeTaskCommand(ctx, command.Command{
		Kind:       command.KindCompleteTask,
		TaskResult: &result,
	})
}

// FailTask proposes a fenced task failure result through ControllerV2 Raft.
func (r *Runtime) FailTask(ctx context.Context, result TaskResult) error {
	return r.proposeTaskCommand(ctx, command.Command{
		Kind:       command.KindFailTask,
		TaskResult: &result,
	})
}

// ReportTaskProgress proposes one participant progress update through ControllerV2 Raft.
func (r *Runtime) ReportTaskProgress(ctx context.Context, progress TaskProgress) error {
	return r.proposeTaskCommand(ctx, command.Command{
		Kind:         command.KindReportTaskProgress,
		TaskProgress: &progress,
	})
}

func (r *Runtime) proposeTaskCommand(ctx context.Context, cmd command.Command) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if r == nil || r.raft == nil {
		return ErrNotStarted
	}
	if cmd.IssuedAt.IsZero() {
		cmd.IssuedAt = r.cfg.Now().UTC()
	} else {
		cmd.IssuedAt = cmd.IssuedAt.UTC()
	}
	return r.raft.Propose(ctx, cmd)
}
