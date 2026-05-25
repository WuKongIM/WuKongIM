package management

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

func TestTransferChannelLeaderDryRun(t *testing.T) {
	now := time.UnixMilli(1750000001000)
	id := channel.ChannelID{ID: "channel-lt-dry-run", Type: 1}
	meta := channelMigrationRuntimeMeta(id, 1, []uint64{1, 2, 3}, []uint64{1, 2, 3})
	store := &fakeChannelMigrationStore{}
	app := New(Options{
		Cluster:            channelMigrationCluster(1, 2, 3),
		ChannelRuntimeMeta: channelMigrationMetaReader(meta),
		ChannelMigration:   store,
		Now:                func() time.Time { return now },
	})

	got, err := app.TransferChannelLeader(context.Background(), id, TransferChannelLeaderRequest{
		TargetNodeID: 2,
		DryRun:       true,
	})

	require.NoError(t, err)
	require.True(t, got.DryRun)
	require.True(t, got.Valid)
	require.Equal(t, "", got.TaskID)
	require.Equal(t, ChannelMigrationKindLeaderTransfer, got.Kind)
	require.Empty(t, got.Blockers)
	require.Equal(t, []string{"validate", "probe_target", "write_fence", "drain_leader", "final_target_catch_up", "commit_leader_meta", "verify_new_leader", "clear_fence"}, got.PhaseSequence)
	require.Equal(t, uint64(1), got.Detail.SourceNode)
	require.Equal(t, uint64(2), got.Detail.TargetNode)
	require.Equal(t, uint64(5), got.Detail.BaseChannelEpoch)
	require.Empty(t, store.created)
}

func TestMigrateChannelReplicaDryRunReportsBlockers(t *testing.T) {
	id := channel.ChannelID{ID: "channel-rr-blocked", Type: 1}
	meta := channelMigrationRuntimeMeta(id, 1, []uint64{1, 2, 3}, []uint64{1, 2})
	store := &fakeChannelMigrationStore{}
	app := New(Options{
		Cluster: fakeClusterReader{
			nodes: []controllermeta.ClusterNode{
				channelMigrationNode(1, controllermeta.NodeStatusAlive),
				channelMigrationNode(2, controllermeta.NodeStatusAlive),
				channelMigrationNode(3, controllermeta.NodeStatusDraining),
			},
		},
		ChannelRuntimeMeta: channelMigrationMetaReader(meta),
		ChannelMigration:   store,
	})

	got, err := app.MigrateChannelReplica(context.Background(), id, MigrateChannelReplicaRequest{
		SourceNodeID: 2,
		TargetNodeID: 3,
		DryRun:       true,
	})

	require.NoError(t, err)
	require.True(t, got.DryRun)
	require.False(t, got.Valid)
	require.ElementsMatch(t, []string{"target_already_replica", "target_node_not_alive"}, got.Blockers)
	require.Empty(t, store.created)
}

func TestMigrateChannelReplicaCreatesTask(t *testing.T) {
	now := time.UnixMilli(1750000002000)
	id := channel.ChannelID{ID: "channel-rr-create", Type: 1}
	meta := channelMigrationRuntimeMeta(id, 1, []uint64{1, 2}, []uint64{1, 2})
	store := &fakeChannelMigrationStore{}
	app := New(Options{
		Cluster:            channelMigrationCluster(1, 2, 3),
		ChannelRuntimeMeta: channelMigrationMetaReader(meta),
		ChannelMigration:   store,
		Now:                func() time.Time { return now },
	})

	got, err := app.MigrateChannelReplica(context.Background(), id, MigrateChannelReplicaRequest{
		SourceNodeID: 2,
		TargetNodeID: 3,
	})

	require.NoError(t, err)
	require.True(t, got.Valid)
	require.False(t, got.DryRun)
	require.NotEmpty(t, got.TaskID)
	require.Len(t, store.created, 1)
	require.Len(t, store.createRequests, 1)
	task := store.created[0]
	require.Equal(t, got.TaskID, task.TaskID)
	require.Equal(t, metadb.ChannelMigrationKindReplicaReplace, task.Kind)
	require.Equal(t, metadb.ChannelMigrationStatusPending, task.Status)
	require.Equal(t, metadb.ChannelMigrationPhaseValidate, task.Phase)
	require.Equal(t, id.ID, task.ChannelID)
	require.Equal(t, int64(id.Type), task.ChannelType)
	require.Equal(t, uint64(2), task.SourceNode)
	require.Equal(t, uint64(3), task.TargetNode)
	require.Zero(t, task.DesiredLeader)
	require.Equal(t, meta.ChannelEpoch, task.BaseChannelEpoch)
	require.Equal(t, meta.LeaderEpoch, task.BaseLeaderEpoch)
	require.Equal(t, now.UnixMilli(), task.CreatedAtMS)
	require.Equal(t, now.UnixMilli(), task.UpdatedAtMS)
	require.Equal(t, meta.ChannelEpoch, store.createRequests[0].RuntimeGuard.ExpectedChannelEpoch)
	require.Equal(t, meta.LeaderEpoch, store.createRequests[0].RuntimeGuard.ExpectedLeaderEpoch)
	require.Equal(t, meta.Leader, store.createRequests[0].RuntimeGuard.ExpectedLeader)
}

func TestMigrateChannelReplicaSourceLeaderDryRunIncludesEmbeddedTransferPhases(t *testing.T) {
	id := channel.ChannelID{ID: "channel-rr-source-leader", Type: 1}
	meta := channelMigrationRuntimeMeta(id, 1, []uint64{1, 2}, []uint64{1, 2})
	store := &fakeChannelMigrationStore{}
	app := New(Options{
		Cluster:            channelMigrationCluster(1, 2, 3),
		ChannelRuntimeMeta: channelMigrationMetaReader(meta),
		ChannelMigration:   store,
	})

	got, err := app.MigrateChannelReplica(context.Background(), id, MigrateChannelReplicaRequest{
		SourceNodeID: 1,
		TargetNodeID: 3,
		DryRun:       true,
	})

	require.NoError(t, err)
	require.True(t, got.Valid)
	require.Equal(t, []string{
		"validate",
		"probe_target",
		"write_fence",
		"drain_leader",
		"final_target_catch_up",
		"commit_leader_meta",
		"verify_new_leader",
		"add_learner",
		"bootstrap_target",
		"warm_catch_up",
		"cutover_fence",
		"final_target_catch_up",
		"promote_and_remove",
		"verify_membership",
		"clear_fence",
	}, got.PhaseSequence)
}

func TestMigrateChannelReplicaSourceLeaderDryRunBlocksWithoutEmbeddedTransferTarget(t *testing.T) {
	id := channel.ChannelID{ID: "channel-rr-no-embedded-target", Type: 1}
	meta := channelMigrationRuntimeMeta(id, 1, []uint64{1}, []uint64{1})
	store := &fakeChannelMigrationStore{}
	app := New(Options{
		Cluster:            channelMigrationCluster(1, 2),
		ChannelRuntimeMeta: channelMigrationMetaReader(meta),
		ChannelMigration:   store,
	})

	got, err := app.MigrateChannelReplica(context.Background(), id, MigrateChannelReplicaRequest{
		SourceNodeID: 1,
		TargetNodeID: 2,
		DryRun:       true,
	})

	require.NoError(t, err)
	require.False(t, got.Valid)
	require.Contains(t, got.Blockers, "no_eligible_embedded_leader")
	require.Empty(t, store.created)
}

func TestMigrateChannelReplicaDryRunBlocksMissingOrDeadCurrentLeader(t *testing.T) {
	tests := []struct {
		name    string
		meta    metadb.ChannelRuntimeMeta
		cluster fakeClusterReader
		want    string
	}{
		{
			name:    "missing leader",
			meta:    channelMigrationRuntimeMeta(channel.ChannelID{ID: "channel-rr-missing-leader", Type: 1}, 0, []uint64{1, 2}, []uint64{1, 2}),
			cluster: channelMigrationCluster(1, 2, 3),
			want:    "missing_leader",
		},
		{
			name: "dead current leader",
			meta: channelMigrationRuntimeMeta(channel.ChannelID{ID: "channel-rr-dead-leader", Type: 1}, 1, []uint64{1, 2}, []uint64{1, 2}),
			cluster: fakeClusterReader{
				nodes: []controllermeta.ClusterNode{
					channelMigrationNode(1, controllermeta.NodeStatusDead),
					channelMigrationNode(2, controllermeta.NodeStatusAlive),
					channelMigrationNode(3, controllermeta.NodeStatusAlive),
				},
			},
			want: "source_leader_not_alive",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			id := channel.ChannelID{ID: tc.meta.ChannelID, Type: uint8(tc.meta.ChannelType)}
			store := &fakeChannelMigrationStore{}
			app := New(Options{
				Cluster:            tc.cluster,
				ChannelRuntimeMeta: channelMigrationMetaReader(tc.meta),
				ChannelMigration:   store,
			})

			got, err := app.MigrateChannelReplica(context.Background(), id, MigrateChannelReplicaRequest{
				SourceNodeID: 2,
				TargetNodeID: 3,
				DryRun:       true,
			})

			require.NoError(t, err)
			require.False(t, got.Valid)
			require.Contains(t, got.Blockers, tc.want)
			require.Empty(t, store.created)
		})
	}
}

func TestTransferChannelLeaderDryRunBlocksActiveWriteFenceAndDeadSourceLeader(t *testing.T) {
	id := channel.ChannelID{ID: "channel-lt-fenced", Type: 1}
	meta := channelMigrationRuntimeMeta(id, 1, []uint64{1, 2}, []uint64{1, 2})
	meta.WriteFenceToken = "foreign"
	meta.WriteFenceVersion = 8
	store := &fakeChannelMigrationStore{}
	app := New(Options{
		Cluster: fakeClusterReader{
			nodes: []controllermeta.ClusterNode{
				channelMigrationNode(1, controllermeta.NodeStatusDead),
				channelMigrationNode(2, controllermeta.NodeStatusAlive),
			},
		},
		ChannelRuntimeMeta: channelMigrationMetaReader(meta),
		ChannelMigration:   store,
	})

	got, err := app.TransferChannelLeader(context.Background(), id, TransferChannelLeaderRequest{
		TargetNodeID: 2,
		DryRun:       true,
	})

	require.NoError(t, err)
	require.False(t, got.Valid)
	require.ElementsMatch(t, []string{"write_fence_active", "source_leader_not_alive"}, got.Blockers)
	require.Empty(t, store.created)
}

func TestGetChannelMigrationReportsProgressAndNeedsSnapshotBootstrapBlocker(t *testing.T) {
	id := channel.ChannelID{ID: "channel-migration-detail", Type: 1}
	meta := channelMigrationRuntimeMeta(id, 1, []uint64{1, 2, 3}, []uint64{1, 2})
	meta.WriteFenceToken = "task-detail"
	meta.WriteFenceVersion = 9
	meta.WriteFenceReason = uint8(channel.WriteFenceReasonMigration)
	meta.WriteFenceUntilMS = 1750000010000
	task := channelMigrationTask("task-detail", id, metadb.ChannelMigrationKindReplicaReplace, 2, 3, 0, meta)
	task.Status = metadb.ChannelMigrationStatusBlocked
	task.Phase = metadb.ChannelMigrationPhaseFinalTargetCatchUp
	task.BlockerCode = metadb.ChannelMigrationBlockerNeedsSnapshotBootstrap
	task.BlockerMessage = "snapshot required"
	task.Progress = metadb.ChannelMigrationProgress{
		LeaderLEO:          100,
		LeaderHW:           98,
		TargetLEO:          90,
		TargetCheckpointHW: 88,
		LagRecords:         10,
		StableSinceMS:      1750000003000,
	}
	store := &fakeChannelMigrationStore{active: task, activeOK: true}
	app := New(Options{
		ChannelRuntimeMeta: channelMigrationMetaReader(meta),
		ChannelMigration:   store,
	})

	got, err := app.GetChannelMigration(context.Background(), id)

	require.NoError(t, err)
	require.Equal(t, "task-detail", got.TaskID)
	require.Equal(t, ChannelMigrationKindReplicaReplace, got.Kind)
	require.Equal(t, "blocked", got.Status)
	require.Equal(t, "final_target_catch_up", got.Phase)
	require.Equal(t, metadb.ChannelMigrationBlockerNeedsSnapshotBootstrap, got.BlockerCode)
	require.True(t, got.FenceActive)
	require.Equal(t, uint64(5), got.CurrentChannelEpoch)
	require.Equal(t, uint64(7), got.CurrentLeaderEpoch)
	require.Equal(t, task.Progress, got.Progress)
}

func TestAbortChannelMigrationRequiresMatchingTask(t *testing.T) {
	now := time.UnixMilli(1750000004000)
	id := channel.ChannelID{ID: "channel-abort", Type: 1}
	meta := channelMigrationRuntimeMeta(id, 1, []uint64{1, 2, 3}, []uint64{1, 2})
	task := channelMigrationTask("task-abort", id, metadb.ChannelMigrationKindReplicaReplace, 2, 3, 0, meta)
	task.Status = metadb.ChannelMigrationStatusRunning
	task.Phase = metadb.ChannelMigrationPhaseWarmCatchUp
	task.OwnerNodeID = 9
	task.OwnerLeaseUntilMS = 1750000010000
	task.UpdatedAtMS = 1750000001000
	store := &fakeChannelMigrationStore{active: task, activeOK: true}
	app := New(Options{
		ChannelRuntimeMeta: channelMigrationMetaReader(meta),
		ChannelMigration:   store,
		Now:                func() time.Time { return now },
	})

	_, err := app.AbortChannelMigration(context.Background(), id, "other-task")
	require.ErrorIs(t, err, metadb.ErrStaleMeta)
	require.Empty(t, store.abortRequests)

	got, err := app.AbortChannelMigration(context.Background(), id, "task-abort")
	require.NoError(t, err)
	require.Equal(t, "aborted", got.Status)
	require.Equal(t, "warm_catch_up", got.Phase)
	require.Len(t, store.abortRequests, 1)
	req := store.abortRequests[0]
	require.Equal(t, "task-abort", req.Guard.TaskID)
	require.Equal(t, metadb.ChannelMigrationStatusAborted, req.Status)
	require.Equal(t, metadb.ChannelMigrationPhaseWarmCatchUp, req.Phase)
	require.Equal(t, now.UnixMilli(), req.UpdatedAtMS)
	require.Equal(t, now.UnixMilli(), req.CompletedAtMS)
}

func TestSingleNodeClusterReplicaMigrationRejectedDeterministically(t *testing.T) {
	now := time.UnixMilli(1750000005000)
	id := channel.ChannelID{ID: "channel-single-node-cluster", Type: 1}
	meta := channelMigrationRuntimeMeta(id, 1, []uint64{1}, []uint64{1})
	store := &fakeChannelMigrationStore{}
	app := New(Options{
		Cluster: fakeClusterReader{
			nodes: []controllermeta.ClusterNode{
				channelMigrationNode(1, controllermeta.NodeStatusAlive),
			},
		},
		ChannelRuntimeMeta: channelMigrationMetaReader(meta),
		ChannelMigration:   store,
		Now:                func() time.Time { return now },
	})

	got, err := app.MigrateChannelReplica(context.Background(), id, MigrateChannelReplicaRequest{
		SourceNodeID: 1,
		TargetNodeID: 2,
	})

	require.ErrorIs(t, err, metadb.ErrInvalidArgument)
	require.False(t, got.Valid)
	require.Contains(t, got.Blockers, "single_node_cluster")
	require.Empty(t, store.created)
}

type fakeChannelMigrationStore struct {
	created        []metadb.ChannelMigrationTask
	createRequests []metadb.ChannelMigrationTaskCreate
	active         metadb.ChannelMigrationTask
	activeOK       bool
	getErr         error
	createErr      error
	abortErr       error
	abortRequests  []metadb.ChannelMigrationAbortRequest
}

func (f *fakeChannelMigrationStore) CreateChannelMigrationTask(_ context.Context, task metadb.ChannelMigrationTask) error {
	if f.createErr != nil {
		return f.createErr
	}
	f.created = append(f.created, task)
	f.active = task
	f.activeOK = true
	return nil
}

func (f *fakeChannelMigrationStore) CreateChannelMigrationTaskWithRuntimeGuard(_ context.Context, req metadb.ChannelMigrationTaskCreate) error {
	if f.createErr != nil {
		return f.createErr
	}
	f.createRequests = append(f.createRequests, req)
	f.created = append(f.created, req.Task)
	f.active = req.Task
	f.activeOK = true
	return nil
}

func (f *fakeChannelMigrationStore) GetActiveChannelMigrationTask(_ context.Context, channelID string, channelType int64) (metadb.ChannelMigrationTask, bool, error) {
	if f.getErr != nil {
		return metadb.ChannelMigrationTask{}, false, f.getErr
	}
	if !f.activeOK || f.active.ChannelID != channelID || f.active.ChannelType != channelType {
		return metadb.ChannelMigrationTask{}, false, nil
	}
	return f.active, true, nil
}

func (f *fakeChannelMigrationStore) ListActiveChannelMigrationTasksForNode(context.Context, uint64, int) ([]metadb.ChannelMigrationTask, bool, error) {
	return nil, false, nil
}

func (f *fakeChannelMigrationStore) AbortChannelMigration(_ context.Context, req metadb.ChannelMigrationAbortRequest) error {
	if f.abortErr != nil {
		return f.abortErr
	}
	if !f.activeOK || f.active.TaskID != req.Guard.TaskID {
		return metadb.ErrStaleMeta
	}
	if f.active.UpdatedAtMS != req.Guard.ExpectedUpdatedAtMS {
		return metadb.ErrStaleMeta
	}
	f.abortRequests = append(f.abortRequests, req)
	f.active.Status = req.Status
	f.active.Phase = req.Phase
	f.active.UpdatedAtMS = req.UpdatedAtMS
	f.active.CompletedAtMS = req.CompletedAtMS
	f.active.LastError = req.LastError
	return nil
}

func channelMigrationMetaReader(meta metadb.ChannelRuntimeMeta) *fakeChannelRuntimeMetaReader {
	return &fakeChannelRuntimeMetaReader{
		metaByKey: map[metadb.ConversationKey]metadb.ChannelRuntimeMeta{
			{ChannelID: meta.ChannelID, ChannelType: meta.ChannelType}: meta,
		},
	}
}

func channelMigrationCluster(nodeIDs ...uint64) fakeClusterReader {
	nodes := make([]controllermeta.ClusterNode, 0, len(nodeIDs))
	for _, nodeID := range nodeIDs {
		nodes = append(nodes, channelMigrationNode(nodeID, controllermeta.NodeStatusAlive))
	}
	return fakeClusterReader{nodes: nodes}
}

func channelMigrationNode(nodeID uint64, status controllermeta.NodeStatus) controllermeta.ClusterNode {
	return controllermeta.ClusterNode{
		NodeID:    nodeID,
		Role:      controllermeta.NodeRoleData,
		JoinState: controllermeta.NodeJoinStateActive,
		Status:    status,
	}
}

func channelMigrationRuntimeMeta(id channel.ChannelID, leader uint64, replicas, isr []uint64) metadb.ChannelRuntimeMeta {
	return metadb.ChannelRuntimeMeta{
		ChannelID:    id.ID,
		ChannelType:  int64(id.Type),
		Status:       uint8(channel.StatusActive),
		ChannelEpoch: 5,
		LeaderEpoch:  7,
		Leader:       leader,
		Replicas:     append([]uint64(nil), replicas...),
		ISR:          append([]uint64(nil), isr...),
		MinISR:       1,
	}
}

func channelMigrationTask(taskID string, id channel.ChannelID, kind metadb.ChannelMigrationKind, source, target, desired uint64, meta metadb.ChannelRuntimeMeta) metadb.ChannelMigrationTask {
	return metadb.ChannelMigrationTask{
		TaskID:           taskID,
		Kind:             kind,
		Status:           metadb.ChannelMigrationStatusPending,
		Phase:            metadb.ChannelMigrationPhaseValidate,
		ChannelID:        id.ID,
		ChannelType:      int64(id.Type),
		SourceNode:       source,
		TargetNode:       target,
		DesiredLeader:    desired,
		BaseChannelEpoch: meta.ChannelEpoch,
		BaseLeaderEpoch:  meta.LeaderEpoch,
		CreatedAtMS:      1750000000000,
		UpdatedAtMS:      1750000000000,
	}
}

func TestChannelMigrationStoreErrorSurfaces(t *testing.T) {
	id := channel.ChannelID{ID: "channel-create-error", Type: 1}
	meta := channelMigrationRuntimeMeta(id, 1, []uint64{1, 2}, []uint64{1, 2})
	wantErr := errors.New("create failed")
	app := New(Options{
		Cluster:            channelMigrationCluster(1, 2, 3),
		ChannelRuntimeMeta: channelMigrationMetaReader(meta),
		ChannelMigration:   &fakeChannelMigrationStore{createErr: wantErr},
	})

	_, err := app.MigrateChannelReplica(context.Background(), id, MigrateChannelReplicaRequest{
		SourceNodeID: 2,
		TargetNodeID: 3,
	})

	require.ErrorIs(t, err, wantErr)
}
