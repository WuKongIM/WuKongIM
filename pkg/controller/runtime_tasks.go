package controller

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/controller/command"
)

// CompleteTask proposes a fenced task completion result through Controller Raft.
func (r *Runtime) CompleteTask(ctx context.Context, result TaskResult) error {
	return r.proposeTaskCommand(ctx, command.Command{
		Kind:       command.KindCompleteTask,
		TaskResult: &result,
	})
}

// FailTask proposes a fenced task failure result through Controller Raft.
func (r *Runtime) FailTask(ctx context.Context, result TaskResult) error {
	return r.proposeTaskCommand(ctx, command.Command{
		Kind:       command.KindFailTask,
		TaskResult: &result,
	})
}

// ReportTaskProgress proposes one participant progress update through Controller Raft.
func (r *Runtime) ReportTaskProgress(ctx context.Context, progress TaskProgress) error {
	return r.proposeTaskCommand(ctx, command.Command{
		Kind:         command.KindReportTaskProgress,
		TaskProgress: &progress,
	})
}

// AdvanceSlotReplicaMovePhase proposes one fenced Slot replica move phase observation.
func (r *Runtime) AdvanceSlotReplicaMovePhase(ctx context.Context, phase SlotReplicaMovePhaseAdvance) error {
	return r.proposeTaskCommand(ctx, command.Command{
		Kind:                 command.KindAdvanceSlotReplicaMovePhase,
		SlotReplicaMovePhase: &phase,
	})
}

// CommitSlotReplicaMove proposes the final fenced Slot replica move assignment replacement.
func (r *Runtime) CommitSlotReplicaMove(ctx context.Context, commit SlotReplicaMoveCommit) error {
	return r.proposeTaskCommand(ctx, command.Command{
		Kind:                  command.KindCommitSlotReplicaMove,
		SlotReplicaMoveCommit: &commit,
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
