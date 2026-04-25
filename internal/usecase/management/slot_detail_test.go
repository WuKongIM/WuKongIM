package management

import (
	"context"
	"testing"
	"time"

	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	"github.com/stretchr/testify/require"
)

func TestGetSlotReturnsDetailWithTaskSummary(t *testing.T) {
	nextRunAt := time.Unix(1713687400, 0).UTC()
	reportAt := time.Unix(1713686400, 0).UTC()
	app := New(Options{
		Cluster: fakeClusterReader{
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
			taskBySlot: map[uint32]controllermeta.ReconcileTask{2: {
				SlotID:     2,
				Kind:       controllermeta.TaskKindRepair,
				Step:       controllermeta.TaskStepCatchUp,
				Status:     controllermeta.TaskStatusRetrying,
				SourceNode: 3,
				TargetNode: 5,
				Attempt:    1,
				NextRunAt:  nextRunAt,
				LastError:  "learner catch-up timeout",
			}},
		},
	})

	got, err := app.GetSlot(context.Background(), 2)
	require.NoError(t, err)
	require.NotNil(t, got.Task)
	require.Equal(t, SlotDetail{
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
		Task: &Task{
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
	}, got)
}

func TestGetSlotReturnsDetailWithNilTaskWhenTaskNotFound(t *testing.T) {
	reportAt := time.Unix(1713686400, 0).UTC()
	app := New(Options{
		Cluster: fakeClusterReader{
			assignments: []controllermeta.SlotAssignment{{
				SlotID:         4,
				DesiredPeers:   []uint64{4, 5, 6},
				ConfigEpoch:    11,
				BalanceVersion: 2,
			}},
			views: []controllermeta.SlotRuntimeView{{
				SlotID:              4,
				CurrentPeers:        []uint64{4, 5, 6},
				LeaderID:            4,
				HealthyVoters:       3,
				HasQuorum:           true,
				ObservedConfigEpoch: 11,
				LastReportAt:        reportAt,
			}},
			getTaskErr: controllermeta.ErrNotFound,
		},
	})

	got, err := app.GetSlot(context.Background(), 4)
	require.NoError(t, err)
	require.Equal(t, uint32(4), got.SlotID)
	require.Nil(t, got.Task)
}

func TestGetSlotReturnsRuntimeOnlyDetailWhenAssignmentMissing(t *testing.T) {
	reportAt := time.Unix(1713686400, 0).UTC()
	app := New(Options{
		Cluster: fakeClusterReader{
			views: []controllermeta.SlotRuntimeView{{
				SlotID:              7,
				CurrentPeers:        []uint64{7, 8, 9},
				LeaderID:            8,
				HealthyVoters:       2,
				HasQuorum:           false,
				ObservedConfigEpoch: 0,
				LastReportAt:        reportAt,
			}},
			getTaskErr: controllermeta.ErrNotFound,
		},
	})

	got, err := app.GetSlot(context.Background(), 7)
	require.NoError(t, err)
	require.Equal(t, SlotDetail{
		Slot: Slot{
			SlotID: 7,
			State:  SlotState{Quorum: "lost", Sync: "unreported"},
			Runtime: SlotRuntime{
				CurrentPeers:        []uint64{7, 8, 9},
				LeaderID:            8,
				HealthyVoters:       2,
				HasQuorum:           false,
				ObservedConfigEpoch: 0,
				LastReportAt:        reportAt,
			},
		},
		Task: nil,
	}, got)
}

func TestGetSlotReturnsNotFoundWhenAssignmentAndRuntimeMissing(t *testing.T) {
	app := New(Options{Cluster: fakeClusterReader{getTaskErr: controllermeta.ErrNotFound}})

	_, err := app.GetSlot(context.Background(), 9)
	require.ErrorIs(t, err, controllermeta.ErrNotFound)
}
