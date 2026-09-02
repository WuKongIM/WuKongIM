package controller

import (
	"context"
	"errors"
	"testing"
)

func TestRuntimeMutationsFailClosedBeforeControllerStarts(t *testing.T) {
	runtime := &Runtime{}
	operations := []struct {
		name string
		run  func(context.Context) error
	}{
		{name: "join node", run: func(ctx context.Context) error { _, err := runtime.JoinNode(ctx, JoinNodeRequest{}); return err }},
		{name: "activate node", run: func(ctx context.Context) error {
			_, err := runtime.ActivateNode(ctx, ActivateNodeRequest{})
			return err
		}},
		{name: "mark leaving", run: func(ctx context.Context) error {
			_, err := runtime.MarkNodeLeaving(ctx, MarkNodeLeavingRequest{})
			return err
		}},
		{name: "mark removed", run: func(ctx context.Context) error {
			_, err := runtime.MarkNodeRemoved(ctx, MarkNodeRemovedRequest{})
			return err
		}},
		{name: "node health", run: func(ctx context.Context) error {
			_, err := runtime.ReportNodeHealth(ctx, ReportNodeHealthRequest{})
			return err
		}},
		{name: "leader transfer", run: func(ctx context.Context) error {
			_, err := runtime.RequestSlotLeaderTransfer(ctx, SlotLeaderTransferRequest{})
			return err
		}},
		{name: "replica move", run: func(ctx context.Context) error {
			_, err := runtime.RequestSlotReplicaMove(ctx, SlotReplicaMoveRequest{})
			return err
		}},
		{name: "complete task", run: func(ctx context.Context) error { return runtime.CompleteTask(ctx, TaskResult{}) }},
		{name: "fail task", run: func(ctx context.Context) error { return runtime.FailTask(ctx, TaskResult{}) }},
		{name: "task progress", run: func(ctx context.Context) error { return runtime.ReportTaskProgress(ctx, TaskProgress{}) }},
		{name: "advance move", run: func(ctx context.Context) error {
			return runtime.AdvanceSlotReplicaMovePhase(ctx, SlotReplicaMovePhaseAdvance{})
		}},
		{name: "commit move", run: func(ctx context.Context) error { return runtime.CommitSlotReplicaMove(ctx, SlotReplicaMoveCommit{}) }},
		{name: "replace backup", run: func(ctx context.Context) error {
			return runtime.ReplaceScheduledBackupState(ctx, 1, ScheduledBackupState{})
		}},
		{name: "replace ops mcp", run: func(ctx context.Context) error { return runtime.ReplaceOpsMCPState(ctx, 1, OpsMCPState{}) }},
		{name: "proposal probe", run: runtime.ProbePropose},
		{name: "raft status", run: func(ctx context.Context) error { _, err := runtime.ControllerRaftStatus(ctx); return err }},
		{name: "raft compaction", run: func(ctx context.Context) error { _, err := runtime.CompactControllerRaftLog(ctx); return err }},
		{name: "raft log read", run: func(ctx context.Context) error { _, err := runtime.LogEntries(ctx, LogEntriesOptions{}); return err }},
	}

	for _, operation := range operations {
		t.Run(operation.name, func(t *testing.T) {
			if err := operation.run(context.Background()); !errors.Is(err, ErrNotStarted) {
				t.Fatalf("before start error = %v, want ErrNotStarted", err)
			}

			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if err := operation.run(ctx); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled error = %v, want context.Canceled", err)
			}
		})
	}
}
