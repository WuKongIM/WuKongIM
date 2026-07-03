package channels

import (
	"context"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

func TestReplicaReplaceExecutorRunsNonLeaderSourcePhaseOrder(t *testing.T) {
	ctx := context.Background()
	now := time.UnixMilli(1750000300000).UTC()
	id := ch.ChannelID{ID: "executor-replica-replace", Type: 1}
	task := testReplicaReplaceExecutorTask(id)
	meta := testMigrationRuntimeMeta(id)
	meta.Leader = 1
	meta.LeaderEpoch = 20
	meta.Replicas = []uint64{1, 2, 3}
	meta.ISR = []uint64{1, 2, 3}
	store := newFakeMigrationExecutorStore(task, &meta, now)
	runtime := &fakeMigrationExecutorRuntime{
		probes: map[uint64][]ch.RuntimeProbeChannel{
			4: {
				{ChannelID: id, ChannelEpoch: 11, LeaderEpoch: 20, Role: ch.RoleFollower, Status: ch.StatusActive, HW: 8, LEO: 8, CheckpointHW: 8},
				{ChannelID: id, ChannelEpoch: 11, LeaderEpoch: 20, Role: ch.RoleFollower, Status: ch.StatusActive, HW: 9, LEO: 9, CheckpointHW: 9},
				{
					ChannelID: id, ChannelEpoch: 12, LeaderEpoch: 20, Role: ch.RoleFollower, Status: ch.StatusActive, HW: 9, LEO: 9, CheckpointHW: 9,
					WriteFence: ch.WriteFence{Token: task.TaskID, Version: 1, Reason: ch.WriteFenceReasonReplicaReplace},
				},
			},
		},
		drain: ch.DrainChannelResult{Drained: true, LEO: 9, HW: 9},
	}
	executor := NewMigrationExecutor(MigrationExecutorConfig{
		LocalNode: 2,
		Source:    fakeMigrationExecutorSource{store: store},
		Store:     store,
		Runtime:   runtime,
		Meta:      fakeMigrationExecutorMetaReader{meta: &meta},
		Clock:     func() time.Time { return now },
	})

	for i := 0; i < 20 && !store.task.IsTerminal(); i++ {
		require.NoError(t, executor.RunOnce(ctx))
	}

	require.True(t, store.task.IsTerminal())
	require.Equal(t, metadb.ChannelMigrationStatusCompleted, store.task.Status)
	require.Equal(t, metadb.ChannelMigrationPhaseClearFence, store.task.Phase)
	require.Equal(t, []string{
		"claim",
		"advance:20:2",
		"add_learner",
		"advance:22:2",
		"set_fence",
		"advance_proof:5:2",
		"advance_proof:25:2",
		"promote_learner",
		"clear_fence",
	}, store.ops)
	require.Equal(t, []string{"apply_meta:1", "apply_meta:4", "probe:4", "apply_meta:1", "apply_meta:4", "drain:1", "probe:4", "apply_meta:1", "apply_meta:4", "probe:4"}, runtime.ops)
	require.Equal(t, []uint64{1, 2, 4}, meta.Replicas)
	require.Equal(t, []uint64{1, 2, 4}, meta.ISR)
}

func TestReplicaReplaceExecutorBootstrapsLeaderAndTargetRuntimeMeta(t *testing.T) {
	ctx := context.Background()
	now := time.UnixMilli(1750000305000).UTC()
	id := ch.ChannelID{ID: "executor-replica-bootstrap", Type: 1}
	task := testReplicaReplaceExecutorTask(id)
	task.Status = metadb.ChannelMigrationStatusRunning
	task.Phase = metadb.ChannelMigrationPhaseBootstrapTarget
	task.OwnerNodeID = 2
	task.OwnerLeaseUntilMS = now.Add(time.Minute).UnixMilli()
	meta := testMigrationRuntimeMeta(id)
	meta.Leader = 1
	meta.LeaderEpoch = 20
	meta.Replicas = []uint64{1, 2, 3, 4}
	meta.ISR = []uint64{1, 2, 3}
	store := newFakeMigrationExecutorStore(task, &meta, now)
	runtime := &fakeMigrationExecutorRuntime{}
	executor := NewMigrationExecutor(MigrationExecutorConfig{
		LocalNode: 2,
		Source:    fakeMigrationExecutorSource{store: store},
		Store:     store,
		Runtime:   runtime,
		Meta:      fakeMigrationExecutorMetaReader{meta: &meta},
		Clock:     func() time.Time { return now },
	})

	require.NoError(t, executor.RunOnce(ctx))

	require.Equal(t, metadb.ChannelMigrationPhaseWarmCatchUp, store.task.Phase)
	require.Equal(t, []string{"apply_meta:1", "apply_meta:4"}, runtime.ops)
}

func TestReplicaReplaceExecutorAppliesFencedMetaBeforeCutoverDrain(t *testing.T) {
	ctx := context.Background()
	now := time.UnixMilli(1750000306000).UTC()
	id := ch.ChannelID{ID: "executor-replica-fenced-drain", Type: 1}
	task := testReplicaReplaceExecutorTask(id)
	task.Status = metadb.ChannelMigrationStatusRunning
	task.Phase = metadb.ChannelMigrationPhaseCutoverFence
	task.OwnerNodeID = 2
	task.OwnerLeaseUntilMS = now.Add(time.Minute).UnixMilli()
	task.FenceToken = task.TaskID
	task.FenceVersion = 3
	meta := testMigrationRuntimeMeta(id)
	meta.Leader = 1
	meta.LeaderEpoch = 20
	meta.Replicas = []uint64{1, 2, 3, 4}
	meta.ISR = []uint64{1, 2, 3}
	meta.WriteFenceToken = task.TaskID
	meta.WriteFenceVersion = task.FenceVersion
	meta.WriteFenceReason = uint8(ch.WriteFenceReasonReplicaReplace)
	meta.WriteFenceUntilMS = now.Add(time.Minute).UnixMilli()
	store := newFakeMigrationExecutorStore(task, &meta, now)
	runtime := &fakeMigrationExecutorRuntime{drain: ch.DrainChannelResult{Drained: true, LEO: 10, HW: 10}}
	executor := NewMigrationExecutor(MigrationExecutorConfig{
		LocalNode: 2,
		Source:    fakeMigrationExecutorSource{store: store},
		Store:     store,
		Runtime:   runtime,
		Meta:      fakeMigrationExecutorMetaReader{meta: &meta},
		Clock:     func() time.Time { return now },
	})

	require.NoError(t, executor.RunOnce(ctx))

	require.Equal(t, metadb.ChannelMigrationPhaseFinalTargetCatchUp, store.task.Phase)
	require.Equal(t, []string{"apply_meta:1", "apply_meta:4", "drain:1"}, runtime.ops)
}

func TestReplicaReplaceExecutorAppliesFinalMetaBeforeVerifyMembership(t *testing.T) {
	ctx := context.Background()
	now := time.UnixMilli(1750000307000).UTC()
	id := ch.ChannelID{ID: "executor-replica-final-apply", Type: 1}
	task := testReplicaReplaceExecutorTask(id)
	task.Status = metadb.ChannelMigrationStatusRunning
	task.Phase = metadb.ChannelMigrationPhaseVerifyMembership
	task.OwnerNodeID = 2
	task.OwnerLeaseUntilMS = now.Add(time.Minute).UnixMilli()
	task.FenceToken = task.TaskID
	task.FenceVersion = 3
	task.CutoverLEO = 10
	task.CutoverHW = 10
	task.DrainedLeaderNode = 1
	task.DrainedRuntimeGeneration = 1
	task.DrainedChannelEpoch = 11
	task.DrainedLeaderEpoch = 20
	task.DrainedFenceVersion = task.FenceVersion
	meta := testMigrationRuntimeMeta(id)
	meta.Leader = 1
	meta.LeaderEpoch = 20
	meta.ChannelEpoch = 12
	meta.Replicas = []uint64{1, 2, 4}
	meta.ISR = []uint64{1, 2, 4}
	meta.WriteFenceToken = task.TaskID
	meta.WriteFenceVersion = task.FenceVersion
	meta.WriteFenceReason = uint8(ch.WriteFenceReasonReplicaReplace)
	meta.WriteFenceUntilMS = now.Add(time.Minute).UnixMilli()
	store := newFakeMigrationExecutorStore(task, &meta, now)
	runtime := &fakeMigrationExecutorRuntime{
		probes: map[uint64][]ch.RuntimeProbeChannel{
			4: {{
				ChannelID: id, ChannelEpoch: 12, LeaderEpoch: 20, Role: ch.RoleFollower, Status: ch.StatusActive, HW: 10, LEO: 10, CheckpointHW: 10,
				WriteFence: ch.WriteFence{Token: task.TaskID, Version: task.FenceVersion, Reason: ch.WriteFenceReasonReplicaReplace},
			}},
		},
	}
	executor := NewMigrationExecutor(MigrationExecutorConfig{
		LocalNode: 2,
		Source:    fakeMigrationExecutorSource{store: store},
		Store:     store,
		Runtime:   runtime,
		Meta:      fakeMigrationExecutorMetaReader{meta: &meta},
		Clock:     func() time.Time { return now },
	})

	require.NoError(t, executor.RunOnce(ctx))

	require.Equal(t, metadb.ChannelMigrationStatusCompleted, store.task.Status)
	require.Equal(t, metadb.ChannelMigrationPhaseClearFence, store.task.Phase)
	require.Equal(t, []string{"apply_meta:1", "apply_meta:4", "probe:4"}, runtime.ops)
}

func TestReplicaReplaceExecutorBlocksWhenSourceIsLeader(t *testing.T) {
	ctx := context.Background()
	now := time.UnixMilli(1750000310000).UTC()
	id := ch.ChannelID{ID: "executor-replace-source-leader", Type: 1}
	task := testReplicaReplaceExecutorTask(id)
	task.SourceNode = 1
	meta := testMigrationRuntimeMeta(id)
	meta.Leader = 1
	store := newFakeMigrationExecutorStore(task, &meta, now)
	executor := NewMigrationExecutor(MigrationExecutorConfig{
		LocalNode: 2,
		Source:    fakeMigrationExecutorSource{store: store},
		Store:     store,
		Runtime:   &fakeMigrationExecutorRuntime{},
		Meta:      fakeMigrationExecutorMetaReader{meta: &meta},
		Clock:     func() time.Time { return now },
	})

	for i := 0; i < 3 && store.task.Status != metadb.ChannelMigrationStatusBlocked; i++ {
		require.NoError(t, executor.RunOnce(ctx))
	}

	require.Equal(t, metadb.ChannelMigrationStatusBlocked, store.task.Status)
	require.Equal(t, metadb.ChannelMigrationPhaseValidate, store.task.Phase)
	require.Equal(t, "source_is_leader", store.lastReason)
}

func TestReplicaReplaceExecutorAllowsNonISRSourceWhenMinISRSurvives(t *testing.T) {
	ctx := context.Background()
	now := time.UnixMilli(1750000320000).UTC()
	id := ch.ChannelID{ID: "executor-replace-non-isr-source", Type: 1}
	task := testReplicaReplaceExecutorTask(id)
	task.Status = metadb.ChannelMigrationStatusRunning
	task.Phase = metadb.ChannelMigrationPhaseValidate
	task.OwnerNodeID = 2
	task.OwnerLeaseUntilMS = now.Add(time.Minute).UnixMilli()
	meta := testMigrationRuntimeMeta(id)
	meta.Leader = 1
	meta.Replicas = []uint64{1, 2, 3}
	meta.ISR = []uint64{1, 2}
	meta.MinISR = 2
	store := newFakeMigrationExecutorStore(task, &meta, now)
	executor := NewMigrationExecutor(MigrationExecutorConfig{
		LocalNode: 2,
		Source:    fakeMigrationExecutorSource{store: store},
		Store:     store,
		Runtime:   &fakeMigrationExecutorRuntime{},
		Meta:      fakeMigrationExecutorMetaReader{meta: &meta},
		Clock:     func() time.Time { return now },
	})

	require.NoError(t, executor.RunOnce(ctx))

	require.Equal(t, metadb.ChannelMigrationStatusRunning, store.task.Status)
	require.Equal(t, metadb.ChannelMigrationPhaseAddLearner, store.task.Phase)
	require.Equal(t, []string{"advance:20:2"}, store.ops)
}

func testReplicaReplaceExecutorTask(id ch.ChannelID) metadb.ChannelMigrationTask {
	task := testMigrationTask(id, "task-"+id.ID)
	task.Kind = metadb.ChannelMigrationKindReplicaReplace
	task.Status = metadb.ChannelMigrationStatusPending
	task.Phase = metadb.ChannelMigrationPhaseValidate
	task.SourceNode = 3
	task.TargetNode = 4
	task.BaseChannelEpoch = 10
	task.BaseLeaderEpoch = 20
	return task
}
