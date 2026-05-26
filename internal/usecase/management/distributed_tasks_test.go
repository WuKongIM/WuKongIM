package management

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/stretchr/testify/require"
)

type fakeDistributedTaskSource struct {
	name     DistributedTaskDomain
	items    []DistributedTask
	warnings []DistributedTaskWarning
	err      error
	detail   DistributedTaskDetail
}

func (f fakeDistributedTaskSource) domain() DistributedTaskDomain { return f.name }

func (f fakeDistributedTaskSource) list(context.Context) ([]DistributedTask, []DistributedTaskWarning, error) {
	return append([]DistributedTask(nil), f.items...), append([]DistributedTaskWarning(nil), f.warnings...), f.err
}

func (f fakeDistributedTaskSource) get(context.Context, string) (DistributedTaskDetail, error) {
	if f.err != nil {
		return DistributedTaskDetail{}, f.err
	}
	return f.detail, nil
}

func TestAggregateDistributedTasksFiltersSortsAndPaginates(t *testing.T) {
	now := time.Date(2026, 5, 14, 10, 0, 0, 0, time.UTC)
	updated := now.Add(-time.Minute)
	created := now.Add(-time.Hour)

	result, err := aggregateDistributedTasks(context.Background(), []distributedTaskSource{
		fakeDistributedTaskSource{name: DistributedTaskDomainSlotReconcile, items: []DistributedTask{{
			ID:         "slot-reconcile:1",
			Domain:     DistributedTaskDomainSlotReconcile,
			Kind:       "repair",
			Status:     DistributedTaskStatusRetrying,
			Phase:      "catch_up",
			Scope:      DistributedTaskScope{Type: DistributedTaskScopeSlot, ID: "1", SlotID: 1},
			TargetNode: 3,
			UpdatedAt:  &updated,
		}}},
		fakeDistributedTaskSource{name: DistributedTaskDomainNodeOnboarding, items: []DistributedTask{{
			ID:         "node-onboarding:job-1",
			Domain:     DistributedTaskDomainNodeOnboarding,
			Kind:       "onboarding",
			Status:     DistributedTaskStatusFailed,
			Scope:      DistributedTaskScope{Type: DistributedTaskScopeJob, ID: "job-1"},
			TargetNode: 4,
			CreatedAt:  &created,
			LastError:  "plan changed",
		}}},
	}, DistributedTaskQuery{Status: DistributedTaskStatusRetrying, NodeID: 3, Limit: 1}, now)

	require.NoError(t, err)
	require.Equal(t, 1, result.Total)
	require.Len(t, result.Items, 1)
	require.Equal(t, "slot-reconcile:1", result.Items[0].ID)
	require.False(t, result.Partial)
	require.False(t, result.HasMore)
}

func TestAggregateDistributedTasksReturnsPartialWarnings(t *testing.T) {
	result, err := aggregateDistributedTasks(context.Background(), []distributedTaskSource{
		fakeDistributedTaskSource{name: DistributedTaskDomainSlotReconcile, items: []DistributedTask{{
			ID:     "slot-reconcile:1",
			Domain: DistributedTaskDomainSlotReconcile,
			Status: DistributedTaskStatusPending,
		}}},
		fakeDistributedTaskSource{name: DistributedTaskDomainChannelMigration, err: errors.New("store unavailable")},
	}, DistributedTaskQuery{Limit: 50}, time.Now())

	require.NoError(t, err)
	require.True(t, result.Partial)
	require.Len(t, result.Warnings, 1)
	require.Equal(t, DistributedTaskDomainChannelMigration, result.Warnings[0].Domain)
	require.Equal(t, "source_unavailable", result.Warnings[0].Code)
}

func TestAggregateDistributedTasksReturnsUnavailableWhenAllSourcesFail(t *testing.T) {
	_, err := aggregateDistributedTasks(context.Background(), []distributedTaskSource{
		fakeDistributedTaskSource{name: DistributedTaskDomainSlotReconcile, err: errors.New("no leader")},
	}, DistributedTaskQuery{Limit: 50}, time.Now())

	require.ErrorIs(t, err, ErrDistributedTasksUnavailable)
}

func TestDistributedTaskSummaryCountsStatusAndDomain(t *testing.T) {
	summary, err := aggregateDistributedTaskSummary(context.Background(), []distributedTaskSource{
		fakeDistributedTaskSource{name: DistributedTaskDomainSlotReconcile, items: []DistributedTask{{
			ID:     "slot-reconcile:1",
			Domain: DistributedTaskDomainSlotReconcile,
			Status: DistributedTaskStatusRetrying,
		}}},
		fakeDistributedTaskSource{name: DistributedTaskDomainNodeOnboarding, items: []DistributedTask{{
			ID:     "node-onboarding:job-1",
			Domain: DistributedTaskDomainNodeOnboarding,
			Status: DistributedTaskStatusRunning,
		}}},
	}, time.Now())

	require.NoError(t, err)
	require.Equal(t, 2, summary.Total)
	require.Equal(t, 1, summary.ByStatus[DistributedTaskStatusRetrying])
	require.Equal(t, 1, summary.ByDomain[DistributedTaskDomainNodeOnboarding])
}

func TestSlotReconcileSourceMapsExistingTasks(t *testing.T) {
	next := time.Date(2026, 5, 14, 10, 5, 0, 0, time.UTC)
	app := New(Options{Cluster: fakeClusterReader{tasks: []controllermeta.ReconcileTask{{
		SlotID:     2,
		Kind:       controllermeta.TaskKindRepair,
		Step:       controllermeta.TaskStepCatchUp,
		Status:     controllermeta.TaskStatusRetrying,
		SourceNode: 3,
		TargetNode: 5,
		Attempt:    1,
		NextRunAt:  next,
		LastError:  "learner catch-up timeout",
	}}}})

	items, warnings, err := distributedSlotReconcileSource{app: app}.list(context.Background())

	require.NoError(t, err)
	require.Empty(t, warnings)
	require.Len(t, items, 1)
	require.Equal(t, DistributedTaskDomainSlotReconcile, items[0].Domain)
	require.Equal(t, DistributedTaskStatusRetrying, items[0].Status)
	require.Equal(t, DistributedTaskScopeSlot, items[0].Scope.Type)
	require.Equal(t, uint32(2), items[0].Scope.SlotID)
	require.Equal(t, &next, items[0].NextRunAt)
}

func TestNodeOnboardingSourceMapsJobs(t *testing.T) {
	now := time.Date(2026, 5, 14, 10, 0, 0, 0, time.UTC)
	app := New(Options{Cluster: fakeClusterReader{onboardingJobs: []controllermeta.NodeOnboardingJob{{
		JobID:            "job-1",
		TargetNodeID:     4,
		Status:           controllermeta.OnboardingJobStatusRunning,
		CreatedAt:        now,
		UpdatedAt:        now.Add(time.Minute),
		CurrentMoveIndex: 0,
	}}}})

	items, warnings, err := distributedNodeOnboardingSource{app: app}.list(context.Background())

	require.NoError(t, err)
	require.Empty(t, warnings)
	require.Len(t, items, 1)
	require.Equal(t, "job-1", items[0].ID)
	require.Equal(t, DistributedTaskStatusRunning, items[0].Status)
	require.Equal(t, uint64(4), items[0].TargetNode)
}

func TestNodeScaleInSourceMapsDrainingNodes(t *testing.T) {
	app := New(Options{
		SlotReplicaN: 1,
		Cluster: fakeClusterReader{
			slotIDs: []multiraft.SlotID{1},
			nodes: []controllermeta.ClusterNode{
				{
					NodeID:    2,
					Role:      controllermeta.NodeRoleData,
					JoinState: controllermeta.NodeJoinStateActive,
					Status:    controllermeta.NodeStatusAlive,
				},
				{
					NodeID:    3,
					Role:      controllermeta.NodeRoleData,
					JoinState: controllermeta.NodeJoinStateActive,
					Status:    controllermeta.NodeStatusDraining,
				},
			},
		},
		RuntimeSummary: scaleInRuntimeSummaryReader{summary: NodeRuntimeSummary{NodeID: 3}},
		ChannelRuntimeMeta: &fakeScaleInChannelRuntimeMeta{metas: map[multiraft.SlotID][]metadb.ChannelRuntimeMeta{
			1: {channelMigrationRuntimeMeta(channel.ChannelID{ID: "room-scale-in", Type: 2}, 2, []uint64{2, 3}, []uint64{2, 3})},
		}},
		ChannelMigration: &fakeScaleInChannelMigrationStore{},
	})

	items, warnings, err := distributedNodeScaleInSource{app: app}.list(context.Background())

	require.NoError(t, err)
	require.Empty(t, warnings)
	require.Len(t, items, 1)
	require.Equal(t, "node-scale-in:3", items[0].ID)
	require.Equal(t, DistributedTaskDomainNodeScaleIn, items[0].Domain)
	require.Equal(t, DistributedTaskStatusRunning, items[0].Status)
	require.Equal(t, DistributedTaskScopeNode, items[0].Scope.Type)
	require.Equal(t, uint64(3), items[0].Scope.NodeID)
}

func TestChannelMigrationSourceDedupesTasksAndMapsCancelled(t *testing.T) {
	id := channel.ChannelID{ID: "room-1", Type: 2}
	meta := channelMigrationRuntimeMeta(id, 1, []uint64{1, 2}, []uint64{1, 2})
	task := channelMigrationTask("task-1", id, metadb.ChannelMigrationKindReplicaReplace, 1, 2, 0, meta)
	task.Status = metadb.ChannelMigrationStatusAborted
	store := &fakeScaleInChannelMigrationStore{active: []metadb.ChannelMigrationTask{task}}
	app := New(Options{
		Cluster: fakeClusterReader{nodes: []controllermeta.ClusterNode{
			channelMigrationNode(1, controllermeta.NodeStatusAlive),
			channelMigrationNode(2, controllermeta.NodeStatusAlive),
		}},
		ChannelRuntimeMeta: channelMigrationMetaReader(meta),
		ChannelMigration:   store,
	})

	items, warnings, err := distributedChannelMigrationSource{app: app}.list(context.Background())

	require.NoError(t, err)
	require.Empty(t, warnings)
	require.Len(t, items, 1)
	require.Equal(t, DistributedTaskDomainChannelMigration, items[0].Domain)
	require.Equal(t, DistributedTaskStatusCancelled, items[0].Status)
	require.Equal(t, DistributedTaskScopeChannel, items[0].Scope.Type)
	require.Equal(t, "room-1", items[0].Scope.ChannelID)
}
