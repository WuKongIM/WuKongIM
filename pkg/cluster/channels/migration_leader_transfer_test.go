package channels

import (
	"context"
	"fmt"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

func TestLeaderTransferExecutorRunsPhaseOrder(t *testing.T) {
	ctx := context.Background()
	now := time.UnixMilli(1750000100000).UTC()
	id := ch.ChannelID{ID: "executor-leader-transfer", Type: 1}
	task := testLeaderTransferExecutorTask(id)
	meta := testMigrationRuntimeMeta(id)
	meta.Leader = 1
	meta.LeaderEpoch = 20
	store := newFakeMigrationExecutorStore(task, &meta, now)
	runtime := &fakeMigrationExecutorRuntime{
		probes: map[uint64][]ch.RuntimeProbeChannel{
			1: {
				{ChannelID: id, ChannelEpoch: 10, LeaderEpoch: 20, Role: ch.RoleLeader, Status: ch.StatusActive, HW: 9, LEO: 9, CheckpointHW: 9},
			},
			3: {
				{ChannelID: id, ChannelEpoch: 10, LeaderEpoch: 20, Role: ch.RoleFollower, Status: ch.StatusActive, HW: 9, LEO: 9, CheckpointHW: 9},
				{ChannelID: id, ChannelEpoch: 10, LeaderEpoch: 20, Role: ch.RoleFollower, Status: ch.StatusActive, HW: 9, LEO: 9, CheckpointHW: 9},
				{
					ChannelID: id, ChannelEpoch: 10, LeaderEpoch: 21, Role: ch.RoleLeader, Status: ch.StatusActive, HW: 9, LEO: 9, CheckpointHW: 9,
					WriteFence: ch.WriteFence{Token: task.TaskID, Version: 1, Reason: ch.WriteFenceReasonLeaderTransfer},
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
		"advance:2:2",
		"advance:3:2",
		"set_fence",
		"advance_proof:5:2",
		"advance_proof:6:2",
		"commit_leader",
		"clear_fence",
	}, store.ops)
	require.Equal(t, []string{"probe:1", "probe:3", "drain:1", "probe:3", "probe:3"}, runtime.ops)
	require.Equal(t, uint64(9), store.lastProof.CutoverLEO)
	require.Equal(t, uint64(9), store.lastProof.CutoverHW)
	require.Equal(t, uint64(1), store.lastProof.DrainedLeaderNode)
	require.Equal(t, uint64(10), store.lastProof.DrainedChannelEpoch)
	require.Equal(t, uint64(20), store.lastProof.DrainedLeaderEpoch)
	require.Equal(t, uint64(0), store.task.FenceVersion)
	require.Equal(t, uint64(3), meta.Leader)
	require.Equal(t, uint64(21), meta.LeaderEpoch)
}

func TestLeaderTransferExecutorBlocksLaggingFinalTarget(t *testing.T) {
	ctx := context.Background()
	now := time.UnixMilli(1750000200000).UTC()
	id := ch.ChannelID{ID: "executor-leader-lagging", Type: 1}
	task := testLeaderTransferExecutorTask(id)
	task.Status = metadb.ChannelMigrationStatusRunning
	task.Phase = metadb.ChannelMigrationPhaseFinalTargetCatchUp
	task.OwnerNodeID = 2
	task.OwnerLeaseUntilMS = now.Add(time.Minute).UnixMilli()
	task.FenceToken = task.TaskID
	task.FenceVersion = 4
	task.CutoverLEO = 10
	task.CutoverHW = 10
	task.DrainedLeaderNode = 1
	task.DrainedRuntimeGeneration = 30
	task.DrainedChannelEpoch = 10
	task.DrainedLeaderEpoch = 20
	task.DrainedFenceVersion = 4
	meta := testMigrationRuntimeMeta(id)
	meta.Leader = 1
	meta.LeaderEpoch = 20
	meta.WriteFenceToken = task.TaskID
	meta.WriteFenceVersion = 4
	meta.WriteFenceReason = uint8(ch.WriteFenceReasonLeaderTransfer)
	meta.WriteFenceUntilMS = now.Add(time.Minute).UnixMilli()
	store := newFakeMigrationExecutorStore(task, &meta, now)
	runtime := &fakeMigrationExecutorRuntime{
		probes: map[uint64][]ch.RuntimeProbeChannel{
			3: {{ChannelID: id, ChannelEpoch: 10, LeaderEpoch: 20, Role: ch.RoleFollower, Status: ch.StatusActive, HW: 9, LEO: 9, CheckpointHW: 9}},
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

	require.Equal(t, metadb.ChannelMigrationStatusBlocked, store.task.Status)
	require.Equal(t, metadb.ChannelMigrationPhaseFinalTargetCatchUp, store.task.Phase)
	require.Equal(t, "target_lagging", store.lastReason)
	require.Equal(t, []string{"probe:3"}, runtime.ops)
}

func TestLeaderTransferExecutorBlocksPreFenceLaggingTarget(t *testing.T) {
	ctx := context.Background()
	now := time.UnixMilli(1750000210000).UTC()
	id := ch.ChannelID{ID: "executor-leader-prefence-lagging", Type: 1}
	task := testLeaderTransferExecutorTask(id)
	task.Status = metadb.ChannelMigrationStatusRunning
	task.Phase = metadb.ChannelMigrationPhaseProbeTarget
	task.OwnerNodeID = 2
	task.OwnerLeaseUntilMS = now.Add(time.Minute).UnixMilli()
	meta := testMigrationRuntimeMeta(id)
	meta.Leader = 1
	meta.LeaderEpoch = 20
	store := newFakeMigrationExecutorStore(task, &meta, now)
	runtime := &fakeMigrationExecutorRuntime{
		probes: map[uint64][]ch.RuntimeProbeChannel{
			1: {{ChannelID: id, ChannelEpoch: 10, LeaderEpoch: 20, Role: ch.RoleLeader, Status: ch.StatusActive, HW: 10, LEO: 10, CheckpointHW: 10}},
			3: {{ChannelID: id, ChannelEpoch: 10, LeaderEpoch: 20, Role: ch.RoleFollower, Status: ch.StatusActive, HW: 9, LEO: 9, CheckpointHW: 9}},
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

	require.Equal(t, metadb.ChannelMigrationStatusBlocked, store.task.Status)
	require.Equal(t, metadb.ChannelMigrationPhaseProbeTarget, store.task.Phase)
	require.Equal(t, "target_lagging", store.lastReason)
	require.Equal(t, []string{"probe:1", "probe:3"}, runtime.ops)
}

func TestMigrationExecutorObservesBlockedPhaseAndActiveCounts(t *testing.T) {
	ctx := context.Background()
	now := time.UnixMilli(1750000215000).UTC()
	id := ch.ChannelID{ID: "executor-observe-blocked", Type: 1}
	task := testLeaderTransferExecutorTask(id)
	task.Status = metadb.ChannelMigrationStatusRunning
	task.Phase = metadb.ChannelMigrationPhaseProbeTarget
	task.OwnerNodeID = 2
	task.OwnerLeaseUntilMS = now.Add(time.Minute).UnixMilli()
	meta := testMigrationRuntimeMeta(id)
	meta.Leader = 1
	meta.LeaderEpoch = 20
	store := newFakeMigrationExecutorStore(task, &meta, now)
	runtime := &fakeMigrationExecutorRuntime{
		probes: map[uint64][]ch.RuntimeProbeChannel{
			1: {{ChannelID: id, ChannelEpoch: 10, LeaderEpoch: 20, Role: ch.RoleLeader, Status: ch.StatusActive, HW: 10, LEO: 10, CheckpointHW: 10}},
			3: {{ChannelID: id, ChannelEpoch: 10, LeaderEpoch: 20, Role: ch.RoleFollower, Status: ch.StatusActive, HW: 9, LEO: 9, CheckpointHW: 9}},
		},
	}
	observer := &fakeMigrationObserver{}
	executor := NewMigrationExecutor(MigrationExecutorConfig{
		LocalNode: 2,
		Source:    fakeMigrationExecutorSource{store: store},
		Store:     store,
		Runtime:   runtime,
		Meta:      fakeMigrationExecutorMetaReader{meta: &meta},
		Observer:  observer,
		Clock:     func() time.Time { return now },
	})

	require.NoError(t, executor.RunOnce(ctx))

	require.Equal(t, []int{1}, observer.activeTaskCounts)
	require.Equal(t, []int{0}, observer.writeFenceActiveCounts)
	require.Equal(t, []string{"target_lagging"}, observer.blockedReasons)
	require.Len(t, observer.durations, 1)
	require.Equal(t, metadb.ChannelMigrationKindLeaderTransfer, observer.durations[0].kind)
	require.Equal(t, metadb.ChannelMigrationPhaseProbeTarget, observer.durations[0].phase)
}

func TestLeaderTransferExecutorRetriesLaggingNewLeaderWithoutBlocking(t *testing.T) {
	ctx := context.Background()
	now := time.UnixMilli(1750000220000).UTC()
	id := ch.ChannelID{ID: "executor-new-leader-lagging", Type: 1}
	task := testLeaderTransferExecutorTask(id)
	task.Status = metadb.ChannelMigrationStatusRunning
	task.Phase = metadb.ChannelMigrationPhaseVerifyNewLeader
	task.OwnerNodeID = 2
	task.OwnerLeaseUntilMS = now.Add(time.Minute).UnixMilli()
	task.FenceToken = task.TaskID
	task.FenceVersion = 4
	task.CutoverLEO = 10
	task.CutoverHW = 10
	task.DrainedLeaderNode = 1
	task.DrainedRuntimeGeneration = 30
	task.DrainedChannelEpoch = 10
	task.DrainedLeaderEpoch = 20
	task.DrainedFenceVersion = 4
	meta := testMigrationRuntimeMeta(id)
	meta.Leader = 3
	meta.LeaderEpoch = 21
	meta.WriteFenceToken = task.TaskID
	meta.WriteFenceVersion = 4
	meta.WriteFenceReason = uint8(ch.WriteFenceReasonLeaderTransfer)
	meta.WriteFenceUntilMS = now.Add(time.Minute).UnixMilli()
	store := newFakeMigrationExecutorStore(task, &meta, now)
	runtime := &fakeMigrationExecutorRuntime{
		probes: map[uint64][]ch.RuntimeProbeChannel{
			3: {
				{
					ChannelID: id, ChannelEpoch: 10, LeaderEpoch: 21, Role: ch.RoleLeader, Status: ch.StatusActive, HW: 9, LEO: 9,
					WriteFence: ch.WriteFence{Token: task.TaskID, Version: 4, Reason: ch.WriteFenceReasonLeaderTransfer},
				},
				{
					ChannelID: id, ChannelEpoch: 10, LeaderEpoch: 21, Role: ch.RoleLeader, Status: ch.StatusActive, HW: 10, LEO: 10,
					WriteFence: ch.WriteFence{Token: task.TaskID, Version: 4, Reason: ch.WriteFenceReasonLeaderTransfer},
				},
			},
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

	require.Equal(t, metadb.ChannelMigrationStatusRunning, store.task.Status)
	require.Equal(t, metadb.ChannelMigrationPhaseVerifyNewLeader, store.task.Phase)
	require.Empty(t, store.lastReason)
	require.Empty(t, store.ops)
	require.NotContains(t, store.ops, "clear_fence")
	require.Equal(t, []string{"probe:3"}, runtime.ops)

	require.NoError(t, executor.RunOnce(ctx))

	require.Equal(t, metadb.ChannelMigrationStatusCompleted, store.task.Status)
	require.Equal(t, metadb.ChannelMigrationPhaseClearFence, store.task.Phase)
	require.Equal(t, []string{"clear_fence"}, store.ops)
	require.Equal(t, []string{"probe:3", "probe:3"}, runtime.ops)
}

func TestMigrationExecutorRenewsExpiredLocalOwnerLease(t *testing.T) {
	ctx := context.Background()
	now := time.UnixMilli(1750000230000).UTC()
	id := ch.ChannelID{ID: "executor-expired-owner-lease", Type: 1}
	task := testLeaderTransferExecutorTask(id)
	task.Status = metadb.ChannelMigrationStatusRunning
	task.Phase = metadb.ChannelMigrationPhaseValidate
	task.OwnerNodeID = 2
	task.OwnerLeaseUntilMS = now.Add(-time.Second).UnixMilli()
	meta := testMigrationRuntimeMeta(id)
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

	require.Equal(t, []string{"claim"}, store.ops)
	require.Greater(t, store.task.OwnerLeaseUntilMS, now.UnixMilli())
}

func TestLeaderTransferExecutorRenewsExpiredFenceBeforeCatchUp(t *testing.T) {
	ctx := context.Background()
	now := time.UnixMilli(1750000240000).UTC()
	id := ch.ChannelID{ID: "executor-expired-fence", Type: 1}
	task := testLeaderTransferExecutorTask(id)
	task.Status = metadb.ChannelMigrationStatusRunning
	task.Phase = metadb.ChannelMigrationPhaseFinalTargetCatchUp
	task.OwnerNodeID = 2
	task.OwnerLeaseUntilMS = now.Add(time.Minute).UnixMilli()
	task.FenceToken = task.TaskID
	task.FenceVersion = 4
	task.FenceUntilMS = now.Add(-time.Second).UnixMilli()
	task.CutoverLEO = 10
	task.CutoverHW = 10
	task.DrainedLeaderNode = 1
	task.DrainedRuntimeGeneration = 30
	task.DrainedChannelEpoch = 10
	task.DrainedLeaderEpoch = 20
	task.DrainedFenceVersion = 4
	meta := testMigrationRuntimeMeta(id)
	meta.Leader = 1
	meta.LeaderEpoch = 20
	meta.WriteFenceToken = task.TaskID
	meta.WriteFenceVersion = 4
	meta.WriteFenceReason = uint8(ch.WriteFenceReasonLeaderTransfer)
	meta.WriteFenceUntilMS = task.FenceUntilMS
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

	require.Equal(t, []string{"set_fence"}, store.ops)
	require.Greater(t, store.task.FenceUntilMS, now.UnixMilli())
}

func TestFailoverExecutorSetsFailoverFenceReason(t *testing.T) {
	ctx := context.Background()
	now := time.UnixMilli(1750000250000).UTC()
	id := ch.ChannelID{ID: "executor-failover-fence", Type: 1}
	task := testLeaderFailoverExecutorTask(id)
	task.Status = metadb.ChannelMigrationStatusRunning
	task.Phase = metadb.ChannelMigrationPhaseWriteFence
	task.OwnerNodeID = 2
	task.OwnerLeaseUntilMS = now.Add(time.Minute).UnixMilli()
	meta := testMigrationRuntimeMeta(id)
	meta.Leader = 1
	meta.LeaderEpoch = 20
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

	require.Equal(t, []string{"set_fence"}, store.ops)
	require.Equal(t, uint8(ch.WriteFenceReasonFailover), meta.WriteFenceReason)
}

func TestFailoverExecutorProbesTargetWithoutSourceBeforeFence(t *testing.T) {
	ctx := context.Background()
	now := time.UnixMilli(1750000260000).UTC()
	id := ch.ChannelID{ID: "executor-failover-prefence", Type: 1}
	task := testLeaderFailoverExecutorTask(id)
	task.Status = metadb.ChannelMigrationStatusRunning
	task.Phase = metadb.ChannelMigrationPhaseProbeTarget
	task.OwnerNodeID = 2
	task.OwnerLeaseUntilMS = now.Add(time.Minute).UnixMilli()
	task.Progress.LeaderHW = 9
	meta := testMigrationRuntimeMeta(id)
	meta.Leader = 1
	meta.LeaderEpoch = 20
	store := newFakeMigrationExecutorStore(task, &meta, now)
	runtime := &fakeMigrationExecutorRuntime{
		probes: map[uint64][]ch.RuntimeProbeChannel{
			3: {{ChannelID: id, ChannelEpoch: 10, LeaderEpoch: 20, Role: ch.RoleFollower, Status: ch.StatusActive, HW: 9, LEO: 11, CheckpointHW: 9}},
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

	require.Equal(t, metadb.ChannelMigrationPhaseWriteFence, store.task.Phase)
	require.Equal(t, []string{"advance:3:2"}, store.ops)
	require.Equal(t, []string{"probe:3"}, runtime.ops)
}

func TestFailoverExecutorSynthesizesCutoverProofFromTarget(t *testing.T) {
	ctx := context.Background()
	now := time.UnixMilli(1750000270000).UTC()
	id := ch.ChannelID{ID: "executor-failover-proof", Type: 1}
	task := testLeaderFailoverExecutorTask(id)
	task.Status = metadb.ChannelMigrationStatusRunning
	task.Phase = metadb.ChannelMigrationPhaseDrainLeader
	task.OwnerNodeID = 2
	task.OwnerLeaseUntilMS = now.Add(time.Minute).UnixMilli()
	task.FenceToken = task.TaskID
	task.FenceVersion = 4
	task.FenceUntilMS = now.Add(time.Minute).UnixMilli()
	task.Progress.LeaderHW = 9
	meta := testMigrationRuntimeMeta(id)
	meta.Leader = 1
	meta.LeaderEpoch = 20
	meta.WriteFenceToken = task.TaskID
	meta.WriteFenceVersion = 4
	meta.WriteFenceReason = uint8(ch.WriteFenceReasonFailover)
	meta.WriteFenceUntilMS = task.FenceUntilMS
	store := newFakeMigrationExecutorStore(task, &meta, now)
	runtime := &fakeMigrationExecutorRuntime{
		probes: map[uint64][]ch.RuntimeProbeChannel{
			3: {{ChannelID: id, ChannelEpoch: 10, LeaderEpoch: 20, Role: ch.RoleFollower, Status: ch.StatusActive, HW: 9, LEO: 11, CheckpointHW: 9}},
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

	require.Equal(t, metadb.ChannelMigrationPhaseCommitLeaderMeta, store.task.Phase)
	require.Equal(t, metadb.ChannelMigrationStatusRunning, store.task.Status)
	require.Equal(t, []string{"advance_proof:6:2"}, store.ops)
	require.Equal(t, []string{"probe:3"}, runtime.ops)
	require.Equal(t, uint64(9), store.lastProof.CutoverLEO)
	require.Equal(t, uint64(9), store.lastProof.CutoverHW)
	require.Equal(t, uint64(1), store.lastProof.DrainedLeaderNode)
	require.Equal(t, uint64(10), store.lastProof.DrainedChannelEpoch)
	require.Equal(t, uint64(20), store.lastProof.DrainedLeaderEpoch)
	require.Equal(t, uint64(4), store.lastProof.DrainedFenceVersion)
	require.Equal(t, uint64(11), store.task.Progress.TargetLEO)
	require.Equal(t, uint64(9), store.task.Progress.TargetCheckpointHW)
}

func testLeaderTransferExecutorTask(id ch.ChannelID) metadb.ChannelMigrationTask {
	task := testMigrationTask(id, "task-"+id.ID)
	task.Kind = metadb.ChannelMigrationKindLeaderTransfer
	task.Status = metadb.ChannelMigrationStatusPending
	task.Phase = metadb.ChannelMigrationPhaseValidate
	task.SourceNode = 1
	task.TargetNode = 3
	task.DesiredLeader = 3
	task.BaseChannelEpoch = 10
	task.BaseLeaderEpoch = 20
	return task
}

func testLeaderFailoverExecutorTask(id ch.ChannelID) metadb.ChannelMigrationTask {
	task := testLeaderTransferExecutorTask(id)
	task.Kind = metadb.ChannelMigrationKindLeaderFailover
	task.TaskID = "task-failover-" + id.ID
	return task
}

type fakeMigrationExecutorSource struct {
	store *fakeMigrationExecutorStore
}

func (s fakeMigrationExecutorSource) ListRunnableMigrationTasks(context.Context, uint64, int) ([]metadb.ChannelMigrationTask, error) {
	if s.store == nil || s.store.task.IsTerminal() {
		return nil, nil
	}
	return []metadb.ChannelMigrationTask{s.store.task}, nil
}

type fakeMigrationExecutorStore struct {
	task       metadb.ChannelMigrationTask
	meta       *metadb.ChannelRuntimeMeta
	now        time.Time
	version    int64
	ops        []string
	lastProof  metadb.ChannelMigrationCutoverProof
	lastReason string
}

func newFakeMigrationExecutorStore(task metadb.ChannelMigrationTask, meta *metadb.ChannelRuntimeMeta, now time.Time) *fakeMigrationExecutorStore {
	return &fakeMigrationExecutorStore{task: task, meta: meta, now: now, version: task.UpdatedAtMS}
}

func (s *fakeMigrationExecutorStore) Claim(_ context.Context, task metadb.ChannelMigrationTask, expectedVersion int64) error {
	s.expectTask(task, expectedVersion)
	s.ops = append(s.ops, "claim")
	s.bump()
	s.task.Status = metadb.ChannelMigrationStatusRunning
	s.task.OwnerNodeID = 2
	s.task.OwnerLeaseUntilMS = s.now.Add(time.Minute).UnixMilli()
	return nil
}

func (s *fakeMigrationExecutorStore) Advance(_ context.Context, task metadb.ChannelMigrationTask, expectedVersion int64, phase metadb.ChannelMigrationPhase, status metadb.ChannelMigrationStatus, reason string) error {
	s.expectTask(task, expectedVersion)
	s.ops = append(s.ops, fmt.Sprintf("advance:%d:%d", phase, status))
	s.bump()
	s.task.Phase = phase
	s.task.Status = status
	s.lastReason = reason
	return nil
}

func (s *fakeMigrationExecutorStore) AdvanceWithProof(_ context.Context, task metadb.ChannelMigrationTask, expectedVersion int64, phase metadb.ChannelMigrationPhase, status metadb.ChannelMigrationStatus, reason string, progress metadb.ChannelMigrationProgress, proof metadb.ChannelMigrationCutoverProof) error {
	s.expectTask(task, expectedVersion)
	s.ops = append(s.ops, fmt.Sprintf("advance_proof:%d:%d", phase, status))
	s.bump()
	s.task.Phase = phase
	s.task.Status = status
	s.task.CutoverLEO = proof.CutoverLEO
	s.task.CutoverHW = proof.CutoverHW
	s.task.DrainedLeaderNode = proof.DrainedLeaderNode
	s.task.DrainedRuntimeGeneration = proof.DrainedRuntimeGeneration
	s.task.DrainedChannelEpoch = proof.DrainedChannelEpoch
	s.task.DrainedLeaderEpoch = proof.DrainedLeaderEpoch
	s.task.DrainedFenceVersion = proof.DrainedFenceVersion
	s.task.Progress = progress
	s.lastProof = proof
	s.lastReason = reason
	return nil
}

func (s *fakeMigrationExecutorStore) SetWriteFence(_ context.Context, task metadb.ChannelMigrationTask, reason ch.WriteFenceReason) error {
	s.expectTask(task, task.UpdatedAtMS)
	s.ops = append(s.ops, "set_fence")
	s.bump()
	s.task.Status = metadb.ChannelMigrationStatusRunning
	if (s.task.Kind == metadb.ChannelMigrationKindLeaderTransfer || s.task.Kind == metadb.ChannelMigrationKindLeaderFailover) && s.task.Phase == metadb.ChannelMigrationPhaseWriteFence {
		s.task.Phase = metadb.ChannelMigrationPhaseDrainLeader
	}
	if s.task.Kind == metadb.ChannelMigrationKindReplicaReplace && s.task.Phase == metadb.ChannelMigrationPhaseWarmCatchUp {
		s.task.Phase = metadb.ChannelMigrationPhaseCutoverFence
	}
	s.task.FenceToken = s.task.TaskID
	s.task.FenceVersion = s.meta.WriteFenceVersion + 1
	s.task.FenceUntilMS = s.now.Add(time.Minute).UnixMilli()
	s.task.CutoverLEO = 0
	s.task.CutoverHW = 0
	s.task.DrainedLeaderNode = 0
	s.task.DrainedRuntimeGeneration = 0
	s.task.DrainedChannelEpoch = 0
	s.task.DrainedLeaderEpoch = 0
	s.task.DrainedFenceVersion = 0
	s.meta.WriteFenceToken = s.task.TaskID
	s.meta.WriteFenceVersion = s.task.FenceVersion
	s.meta.WriteFenceReason = uint8(reason)
	s.meta.WriteFenceUntilMS = s.task.FenceUntilMS
	return nil
}

func (s *fakeMigrationExecutorStore) AddLearner(_ context.Context, task metadb.ChannelMigrationTask) error {
	s.expectTask(task, task.UpdatedAtMS)
	s.ops = append(s.ops, "add_learner")
	s.bump()
	s.task.Status = metadb.ChannelMigrationStatusRunning
	s.task.Phase = metadb.ChannelMigrationPhaseBootstrapTarget
	if !migrationNodeInList(s.meta.Replicas, task.TargetNode) {
		s.meta.Replicas = append(s.meta.Replicas, task.TargetNode)
		s.meta.ChannelEpoch++
	}
	return nil
}

func (s *fakeMigrationExecutorStore) CommitLeaderTransfer(_ context.Context, task metadb.ChannelMigrationTask) error {
	s.expectTask(task, task.UpdatedAtMS)
	s.ops = append(s.ops, "commit_leader")
	s.bump()
	s.task.Status = metadb.ChannelMigrationStatusRunning
	s.task.Phase = metadb.ChannelMigrationPhaseVerifyNewLeader
	s.meta.Leader = s.task.TargetNode
	s.meta.LeaderEpoch++
	return nil
}

func (s *fakeMigrationExecutorStore) PromoteLearnerAndRemoveSource(_ context.Context, task metadb.ChannelMigrationTask) error {
	s.expectTask(task, task.UpdatedAtMS)
	s.ops = append(s.ops, "promote_learner")
	s.bump()
	s.task.Status = metadb.ChannelMigrationStatusRunning
	s.task.Phase = metadb.ChannelMigrationPhaseVerifyMembership
	s.meta.Replicas = replaceMigrationNode(s.meta.Replicas, task.SourceNode, task.TargetNode)
	s.meta.ISR = replaceMigrationNode(s.meta.ISR, task.SourceNode, task.TargetNode)
	s.meta.ChannelEpoch++
	return nil
}

func (s *fakeMigrationExecutorStore) ClearWriteFence(_ context.Context, task metadb.ChannelMigrationTask) error {
	s.expectTask(task, task.UpdatedAtMS)
	s.ops = append(s.ops, "clear_fence")
	s.bump()
	s.task.Status = metadb.ChannelMigrationStatusCompleted
	s.task.Phase = metadb.ChannelMigrationPhaseClearFence
	s.task.FenceToken = ""
	s.task.FenceVersion = 0
	s.meta.WriteFenceToken = ""
	s.meta.WriteFenceVersion++
	s.meta.WriteFenceReason = 0
	s.meta.WriteFenceUntilMS = 0
	return nil
}

func (s *fakeMigrationExecutorStore) expectTask(task metadb.ChannelMigrationTask, expectedVersion int64) {
	if task.TaskID != s.task.TaskID || expectedVersion != s.task.UpdatedAtMS {
		panic(fmt.Sprintf("unexpected task guard task=%s expected=%d current=%d", task.TaskID, expectedVersion, s.task.UpdatedAtMS))
	}
}

func (s *fakeMigrationExecutorStore) bump() {
	s.version++
	s.task.UpdatedAtMS = s.version
	s.task.Attempt++
}

func replaceMigrationNode(nodes []uint64, oldNode uint64, newNode uint64) []uint64 {
	out := make([]uint64, 0, len(nodes))
	replaced := false
	for _, node := range nodes {
		if node == oldNode {
			if !migrationNodeInList(out, newNode) {
				out = append(out, newNode)
			}
			replaced = true
			continue
		}
		if node == newNode && replaced {
			continue
		}
		out = append(out, node)
	}
	return out
}

type fakeMigrationExecutorMetaReader struct {
	meta *metadb.ChannelRuntimeMeta
}

func (r fakeMigrationExecutorMetaReader) GetChannelRuntimeMeta(_ context.Context, _ string, _ int64) (metadb.ChannelRuntimeMeta, error) {
	return *r.meta, nil
}

type fakeMigrationExecutorRuntime struct {
	probes map[uint64][]ch.RuntimeProbeChannel
	drain  ch.DrainChannelResult
	ops    []string
}

func (r *fakeMigrationExecutorRuntime) ProbeChannel(_ context.Context, nodeID uint64, channelID string, channelType uint8) (ch.RuntimeProbeChannel, error) {
	r.ops = append(r.ops, fmt.Sprintf("probe:%d", nodeID))
	queue := r.probes[nodeID]
	if len(queue) == 0 {
		return ch.RuntimeProbeChannel{}, ch.ErrChannelNotFound
	}
	probe := queue[0]
	r.probes[nodeID] = queue[1:]
	return probe, nil
}

func (r *fakeMigrationExecutorRuntime) DrainChannel(_ context.Context, nodeID uint64, req ch.DrainChannelRequest) (ch.DrainChannelResult, error) {
	r.ops = append(r.ops, fmt.Sprintf("drain:%d", nodeID))
	return r.drain, nil
}

func (r *fakeMigrationExecutorRuntime) ApplyChannelMeta(_ context.Context, nodeID uint64, _ metadb.ChannelRuntimeMeta) error {
	r.ops = append(r.ops, fmt.Sprintf("apply_meta:%d", nodeID))
	return nil
}

type fakeMigrationObserver struct {
	activeTaskCounts       []int
	writeFenceActiveCounts []int
	blockedReasons         []string
	durations              []migrationDurationObservation
	writeFenceDurations    []uint64
	phases                 []metadb.ChannelMigrationPhase
}

type migrationDurationObservation struct {
	kind  metadb.ChannelMigrationKind
	phase metadb.ChannelMigrationPhase
}

func (o *fakeMigrationObserver) MigrationActiveTasks(count int) {
	o.activeTaskCounts = append(o.activeTaskCounts, count)
}

func (o *fakeMigrationObserver) MigrationBlocked(reason string) {
	o.blockedReasons = append(o.blockedReasons, reason)
}

func (o *fakeMigrationObserver) WriteFenceActive(count int) {
	o.writeFenceActiveCounts = append(o.writeFenceActiveCounts, count)
}

func (o *fakeMigrationObserver) MigrationPhase(_ string, _ metadb.ChannelMigrationKind, phase metadb.ChannelMigrationPhase, _ metadb.ChannelMigrationStatus, _ string) {
	o.phases = append(o.phases, phase)
}

func (o *fakeMigrationObserver) MigrationDuration(kind metadb.ChannelMigrationKind, phase metadb.ChannelMigrationPhase, _ time.Duration) {
	o.durations = append(o.durations, migrationDurationObservation{kind: kind, phase: phase})
}

func (o *fakeMigrationObserver) WriteFenceDuration(_ string, fenceVersion uint64, _ time.Duration) {
	o.writeFenceDurations = append(o.writeFenceDurations, fenceVersion)
}
