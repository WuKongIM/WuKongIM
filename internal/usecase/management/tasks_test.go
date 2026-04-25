package management

import (
	"context"
	"testing"
	"time"

	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	"github.com/stretchr/testify/require"
)

func TestListTasksSortsBySlotIDAndMapsEnums(t *testing.T) {
	nextRunAt := time.Unix(1713686400, 0).UTC()
	app := New(Options{
		Cluster: fakeClusterReader{
			tasks: []controllermeta.ReconcileTask{
				{
					SlotID:     9,
					Kind:       controllermeta.TaskKindBootstrap,
					Step:       controllermeta.TaskStepAddLearner,
					Status:     controllermeta.TaskStatusPending,
					TargetNode: 2,
				},
				{
					SlotID:     4,
					Kind:       controllermeta.TaskKindRepair,
					Step:       controllermeta.TaskStepCatchUp,
					Status:     controllermeta.TaskStatusRetrying,
					SourceNode: 3,
					TargetNode: 5,
					Attempt:    1,
					NextRunAt:  nextRunAt,
				},
				{
					SlotID:     6,
					Kind:       controllermeta.TaskKindRebalance,
					Step:       controllermeta.TaskStepTransferLeader,
					Status:     controllermeta.TaskStatusFailed,
					SourceNode: 2,
					TargetNode: 4,
					Attempt:    3,
					LastError:  "leader transfer rejected",
				},
			},
		},
	})

	got, err := app.ListTasks(context.Background())
	require.NoError(t, err)
	require.Equal(t, []taskSummary{
		{SlotID: 4, Kind: "repair", Step: "catch_up", Status: "retrying", SourceNode: 3, TargetNode: 5, Attempt: 1},
		{SlotID: 6, Kind: "rebalance", Step: "transfer_leader", Status: "failed", SourceNode: 2, TargetNode: 4, Attempt: 3},
		{SlotID: 9, Kind: "bootstrap", Step: "add_learner", Status: "pending", SourceNode: 0, TargetNode: 2, Attempt: 0},
	}, summarizeTasks(got))
}

func TestListTasksMapsRetryScheduleAndFailureMessage(t *testing.T) {
	retryAt := time.Unix(1713687400, 0).UTC()
	app := New(Options{
		Cluster: fakeClusterReader{
			tasks: []controllermeta.ReconcileTask{
				{
					SlotID:    1,
					Kind:      controllermeta.TaskKindRepair,
					Step:      controllermeta.TaskStepCatchUp,
					Status:    controllermeta.TaskStatusRetrying,
					Attempt:   2,
					NextRunAt: retryAt,
					LastError: "learner catch-up timeout",
				},
				{
					SlotID:    2,
					Kind:      controllermeta.TaskKindBootstrap,
					Step:      controllermeta.TaskStepAddLearner,
					Status:    controllermeta.TaskStatusPending,
					NextRunAt: retryAt,
				},
				{
					SlotID:    3,
					Kind:      controllermeta.TaskKindRebalance,
					Step:      controllermeta.TaskStepPromote,
					Status:    controllermeta.TaskStatusFailed,
					LastError: "operator action required",
				},
			},
		},
	})

	got, err := app.ListTasks(context.Background())
	require.NoError(t, err)
	require.Len(t, got, 3)
	require.Equal(t, &retryAt, got[0].NextRunAt)
	require.Equal(t, "learner catch-up timeout", got[0].LastError)
	require.Nil(t, got[1].NextRunAt)
	require.Equal(t, "", got[1].LastError)
	require.Nil(t, got[2].NextRunAt)
	require.Equal(t, "operator action required", got[2].LastError)
}

func TestGetTaskReturnsTaskWithSlotContext(t *testing.T) {
	nextRunAt := time.Unix(1713687400, 0).UTC()
	reportAt := time.Unix(1713686400, 0).UTC()
	app := New(Options{
		Cluster: fakeClusterReader{
			taskBySlot: map[uint32]controllermeta.ReconcileTask{
				2: {
					SlotID:     2,
					Kind:       controllermeta.TaskKindRepair,
					Step:       controllermeta.TaskStepCatchUp,
					Status:     controllermeta.TaskStatusRetrying,
					SourceNode: 3,
					TargetNode: 5,
					Attempt:    1,
					NextRunAt:  nextRunAt,
					LastError:  "learner catch-up timeout",
				},
			},
			assignments: []controllermeta.SlotAssignment{{
				SlotID:         2,
				DesiredPeers:   []uint64{2, 3, 5},
				ConfigEpoch:    8,
				BalanceVersion: 3,
			}},
			views: []controllermeta.SlotRuntimeView{{
				SlotID:              2,
				CurrentPeers:        []uint64{2, 3, 5},
				LeaderID:            2,
				HealthyVoters:       3,
				HasQuorum:           true,
				ObservedConfigEpoch: 8,
				LastReportAt:        reportAt,
			}},
		},
	})

	got, err := app.GetTask(context.Background(), 2)
	require.NoError(t, err)
	require.Equal(t, TaskDetail{
		Task: Task{
			SlotID:     2,
			Kind:       "repair",
			Step:       "catch_up",
			Status:     "retrying",
			SourceNode: 3,
			TargetNode: 5,
			Attempt:    1,
			NextRunAt:  &nextRunAt,
			LastError:  "learner catch-up timeout",
		},
		Slot: Slot{
			SlotID: 2,
			State:  SlotState{Quorum: "ready", Sync: "matched"},
			Assignment: SlotAssignment{
				DesiredPeers:   []uint64{2, 3, 5},
				ConfigEpoch:    8,
				BalanceVersion: 3,
			},
			Runtime: SlotRuntime{
				CurrentPeers:        []uint64{2, 3, 5},
				LeaderID:            2,
				HealthyVoters:       3,
				HasQuorum:           true,
				ObservedConfigEpoch: 8,
				LastReportAt:        reportAt,
			},
		},
	}, got)
}

func TestGetTaskReturnsNotFound(t *testing.T) {
	app := New(Options{
		Cluster: fakeClusterReader{getTaskErr: controllermeta.ErrNotFound},
	})

	_, err := app.GetTask(context.Background(), 7)
	require.ErrorIs(t, err, controllermeta.ErrNotFound)
}

type taskSummary struct {
	SlotID     uint32
	Kind       string
	Step       string
	Status     string
	SourceNode uint64
	TargetNode uint64
	Attempt    uint32
}

func summarizeTasks(tasks []Task) []taskSummary {
	out := make([]taskSummary, 0, len(tasks))
	for _, task := range tasks {
		out = append(out, taskSummary{
			SlotID:     task.SlotID,
			Kind:       task.Kind,
			Step:       task.Step,
			Status:     task.Status,
			SourceNode: task.SourceNode,
			TargetNode: task.TargetNode,
			Attempt:    task.Attempt,
		})
	}
	return out
}
