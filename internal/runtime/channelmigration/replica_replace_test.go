package channelmigration

import (
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	slotmeta "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

func TestReplicaReplaceFollowerSourceHappyPath(t *testing.T) {
	now := time.UnixMilli(30000)
	clock := &leaderTransferClock{now: now}
	task := replicaReplaceTask("task-replace-happy", "channel-replace-happy", 2, 3, now)
	store := newFakeExecutorStore(task)
	store.putRuntimeMeta(replicaReplaceRuntimeMeta(task.ChannelID, 1, []uint64{1, 2}, []uint64{1, 2}, now))
	executor := newLeaderTransferExecutorHarness(store, clock, &recordingMigrationControl{}, &recordingProbeClient{}, 9)
	executor.cfg.CatchUpStableWindow = 100 * time.Millisecond

	for i := 0; i < 11; i++ {
		require.NoError(t, executor.Tick(context.Background()))
		clock.advance(100 * time.Millisecond)
	}

	gotTask := store.task(task.TaskID)
	require.Equal(t, slotmeta.ChannelMigrationStatusCompleted, gotTask.Status)
	require.Equal(t, slotmeta.ChannelMigrationPhaseClearFence, gotTask.Phase)
	gotMeta := store.runtimeMeta(task.ChannelID, task.ChannelType)
	require.Equal(t, uint64(1), gotMeta.Leader)
	require.ElementsMatch(t, []uint64{1, 3}, gotMeta.Replicas)
	require.ElementsMatch(t, []uint64{1, 3}, gotMeta.ISR)
	require.Empty(t, gotMeta.WriteFenceToken)
}

func TestReplicaReplaceSourceLeaderUsesEmbeddedTransfer(t *testing.T) {
	now := time.UnixMilli(31000)
	clock := &leaderTransferClock{now: now}
	task := replicaReplaceTask("task-replace-leader", "channel-replace-leader", 1, 3, now)
	store := newFakeExecutorStore(task)
	store.putRuntimeMeta(replicaReplaceRuntimeMeta(task.ChannelID, 1, []uint64{1, 2}, []uint64{1, 2}, now))
	executor := newLeaderTransferExecutorHarness(store, clock, &recordingMigrationControl{}, &recordingProbeClient{}, 9)
	executor.cfg.CatchUpStableWindow = 100 * time.Millisecond

	for i := 0; i < 18; i++ {
		err := executor.Tick(context.Background())
		require.NoErrorf(t, err, "tick=%d task=%+v meta=%+v", i, store.task(task.TaskID), store.runtimeMeta(task.ChannelID, task.ChannelType))
		clock.advance(100 * time.Millisecond)
	}

	gotTask := store.task(task.TaskID)
	require.Equal(t, slotmeta.ChannelMigrationStatusCompleted, gotTask.Status)
	require.False(t, gotTask.EmbeddedLeaderTransfer)
	require.Zero(t, gotTask.EmbeddedDesiredLeader)
	require.Len(t, store.leaderTransferCommits, 1)
	require.Equal(t, uint64(2), store.leaderTransferCommits[0].DesiredLeader)
	gotMeta := store.runtimeMeta(task.ChannelID, task.ChannelType)
	require.Equal(t, uint64(2), gotMeta.Leader)
	require.ElementsMatch(t, []uint64{2, 3}, gotMeta.Replicas)
	require.ElementsMatch(t, []uint64{2, 3}, gotMeta.ISR)
}

func TestReplicaReplaceRejectsBaseEpochChanged(t *testing.T) {
	now := time.UnixMilli(31500)
	task := replicaReplaceTask("task-base-changed", "channel-base-changed", 2, 3, now)
	store := newFakeExecutorStore(task)
	meta := replicaReplaceRuntimeMeta(task.ChannelID, 1, []uint64{1, 2}, []uint64{1, 2}, now)
	meta.ChannelEpoch = task.BaseChannelEpoch + 1
	store.putRuntimeMeta(meta)
	executor := newLeaderTransferExecutorHarness(store, &leaderTransferClock{now: now}, &recordingMigrationControl{}, &recordingProbeClient{}, 9)

	require.NoError(t, executor.Tick(context.Background()))

	gotTask := store.task(task.TaskID)
	require.Equal(t, slotmeta.ChannelMigrationStatusFailed, gotTask.Status)
	require.Contains(t, gotTask.LastError, slotmeta.ErrStaleMeta.Error())
}

func TestReplicaReplaceAddLearnerKeepsISRUnchanged(t *testing.T) {
	now := time.UnixMilli(32000)
	task := replicaReplaceTask("task-add-learner", "channel-add-learner", 2, 3, now)
	task.Status = slotmeta.ChannelMigrationStatusRunning
	task.Phase = slotmeta.ChannelMigrationPhaseAddLearner
	store := newFakeExecutorStore(task)
	store.putRuntimeMeta(replicaReplaceRuntimeMeta(task.ChannelID, 1, []uint64{1, 2}, []uint64{1, 2}, now))
	executor := newLeaderTransferExecutorHarness(store, &leaderTransferClock{now: now}, &recordingMigrationControl{}, &recordingProbeClient{}, 9)

	require.NoError(t, executor.Tick(context.Background()))

	gotTask := store.task(task.TaskID)
	require.Equal(t, slotmeta.ChannelMigrationPhaseBootstrapTarget, gotTask.Phase)
	gotMeta := store.runtimeMeta(task.ChannelID, task.ChannelType)
	require.ElementsMatch(t, []uint64{1, 2, 3}, gotMeta.Replicas)
	require.ElementsMatch(t, []uint64{1, 2}, gotMeta.ISR)
	require.Equal(t, uint64(6), gotMeta.ChannelEpoch)
	require.Len(t, store.addLearnerRequests, 1)
}

func TestReplicaReplaceAddLearnerRoutesBackToEmbeddedTransferWhenSourceBecomesLeader(t *testing.T) {
	now := time.UnixMilli(32300)
	task := replicaReplaceTask("task-add-leader-reentry", "channel-add-leader-reentry", 1, 3, now)
	task.Status = slotmeta.ChannelMigrationStatusRunning
	task.Phase = slotmeta.ChannelMigrationPhaseAddLearner
	store := newFakeExecutorStore(task)
	store.putRuntimeMeta(replicaReplaceRuntimeMeta(task.ChannelID, 1, []uint64{1, 2}, []uint64{1, 2}, now))
	executor := newLeaderTransferExecutorHarness(store, &leaderTransferClock{now: now}, &recordingMigrationControl{}, &recordingProbeClient{}, 9)

	require.NoError(t, executor.Tick(context.Background()))

	gotTask := store.task(task.TaskID)
	require.Equal(t, slotmeta.ChannelMigrationPhaseProbeTarget, gotTask.Phase)
	require.True(t, gotTask.EmbeddedLeaderTransfer)
	require.Equal(t, uint64(2), gotTask.EmbeddedDesiredLeader)
	require.Empty(t, store.addLearnerRequests)
}

func TestReplicaReplaceDoesNotAddLearnerAfterRuntimeMetaReadExpiresOwnerLease(t *testing.T) {
	now := time.UnixMilli(32500)
	clock := &leaderTransferClock{now: now}
	task := replicaReplaceTask("task-add-expired-owner", "channel-add-expired-owner", 2, 3, now)
	task.Status = slotmeta.ChannelMigrationStatusRunning
	task.Phase = slotmeta.ChannelMigrationPhaseAddLearner
	store := newFakeExecutorStore(task)
	store.putRuntimeMeta(replicaReplaceRuntimeMeta(task.ChannelID, 1, []uint64{1, 2}, []uint64{1, 2}, now))
	store.onGetRuntimeMeta = func() {
		clock.advance(10 * time.Millisecond)
	}
	executor := newLeaderTransferExecutorHarness(store, clock, &recordingMigrationControl{}, &recordingProbeClient{}, 9)
	executor.cfg.OwnerLease = 5 * time.Millisecond

	require.NoError(t, executor.Tick(context.Background()))

	gotTask := store.task(task.TaskID)
	require.Equal(t, slotmeta.ChannelMigrationPhaseAddLearner, gotTask.Phase)
	require.Empty(t, store.addLearnerRequests)
}

func TestReplicaReplaceWarmCatchUpProgressSurvivesExecutorRestart(t *testing.T) {
	now := time.UnixMilli(33000)
	clock := &leaderTransferClock{now: now}
	task := replicaReplaceTask("task-warm-restart", "channel-warm-restart", 2, 3, now)
	task.Status = slotmeta.ChannelMigrationStatusRunning
	task.Phase = slotmeta.ChannelMigrationPhaseWarmCatchUp
	store := newFakeExecutorStore(task)
	store.putRuntimeMeta(replicaReplaceRuntimeMeta(task.ChannelID, 1, []uint64{1, 2, 3}, []uint64{1, 2}, now))
	lagging := &recordingProbeClient{reports: map[channel.NodeID]ProbeReport{
		1: replicaReplaceProbeReport(task.ChannelID, 1, 5, 7, 50, 45),
		3: replicaReplaceProbeReport(task.ChannelID, 3, 5, 7, 41, 40),
	}}
	executor := newLeaderTransferExecutorHarness(store, clock, &recordingMigrationControl{}, lagging, 9)

	require.NoError(t, executor.Tick(context.Background()))
	gotTask := store.task(task.TaskID)
	require.Equal(t, slotmeta.ChannelMigrationPhaseWarmCatchUp, gotTask.Phase)
	require.Equal(t, uint64(50), gotTask.Progress.LeaderLEO)
	require.Equal(t, uint64(41), gotTask.Progress.TargetLEO)
	require.Greater(t, gotTask.NextRunAtMS, now.UnixMilli())

	clock.advance(time.Second + time.Millisecond)
	caughtUp := &recordingProbeClient{reports: map[channel.NodeID]ProbeReport{
		1: replicaReplaceProbeReport(task.ChannelID, 1, 5, 7, 50, 45),
		3: replicaReplaceProbeReport(task.ChannelID, 3, 5, 7, 50, 45),
	}}
	restarted := newLeaderTransferExecutorHarness(store, clock, &recordingMigrationControl{}, caughtUp, 9)
	require.NoError(t, restarted.Tick(context.Background()))
	require.Equal(t, slotmeta.ChannelMigrationPhaseWarmCatchUp, store.task(task.TaskID).Phase)

	clock.advance(time.Second)
	require.NoError(t, restarted.Tick(context.Background()))

	gotTask = store.task(task.TaskID)
	require.Equal(t, slotmeta.ChannelMigrationPhaseCutoverFence, gotTask.Phase)
	require.Equal(t, uint64(50), gotTask.Progress.TargetLEO)
}

func TestReplicaReplaceWarmCatchUpAllowsConfiguredStableLag(t *testing.T) {
	now := time.UnixMilli(33500)
	clock := &leaderTransferClock{now: now}
	task := replicaReplaceTask("task-warm-lag", "channel-warm-lag", 2, 3, now)
	task.Status = slotmeta.ChannelMigrationStatusRunning
	task.Phase = slotmeta.ChannelMigrationPhaseWarmCatchUp
	store := newFakeExecutorStore(task)
	store.putRuntimeMeta(replicaReplaceRuntimeMeta(task.ChannelID, 1, []uint64{1, 2, 3}, []uint64{1, 2}, now))
	probes := &recordingProbeClient{reports: map[channel.NodeID]ProbeReport{
		1: replicaReplaceProbeReport(task.ChannelID, 1, 5, 7, 50, 45),
		3: replicaReplaceProbeReport(task.ChannelID, 3, 5, 7, 48, 45),
	}}
	executor := newLeaderTransferExecutorHarness(store, clock, &recordingMigrationControl{}, probes, 9)
	executor.cfg.CatchUpLagThreshold = 2
	executor.cfg.CatchUpStableWindow = time.Second

	require.NoError(t, executor.Tick(context.Background()))
	require.Equal(t, slotmeta.ChannelMigrationPhaseWarmCatchUp, store.task(task.TaskID).Phase)

	clock.advance(time.Second)
	require.NoError(t, executor.Tick(context.Background()))

	gotTask := store.task(task.TaskID)
	require.Equal(t, slotmeta.ChannelMigrationPhaseCutoverFence, gotTask.Phase)
	require.Equal(t, uint64(2), gotTask.Progress.LagRecords)
}

func TestReplicaReplaceFinalCatchUpRequiresLineageProof(t *testing.T) {
	now := time.UnixMilli(34000)
	task := replicaReplaceDrainedTask("task-lineage", "channel-lineage", 2, 3, now, slotmeta.ChannelMigrationPhaseFinalTargetCatchUp)
	store := newFakeExecutorStore(task)
	store.putRuntimeMeta(replicaReplaceFencedRuntimeMeta(task, 1, []uint64{1, 2, 3}, []uint64{1, 2}, now))
	probes := &recordingProbeClient{report: ProbeReport{
		ChannelKey:   leaderTransferChannelKey(task.ChannelID),
		ChannelEpoch: 5,
		LeaderEpoch:  7,
		ReplicaID:    3,
		OffsetEpoch:  4,
		LogEndOffset: 20,
		CheckpointHW: 18,
		CommitReady:  true,
	}}
	executor := newLeaderTransferExecutorHarness(store, &leaderTransferClock{now: now}, &recordingMigrationControl{}, probes, 9)

	require.NoError(t, executor.Tick(context.Background()))

	gotTask := store.task(task.TaskID)
	require.Equal(t, slotmeta.ChannelMigrationPhaseFinalTargetCatchUp, gotTask.Phase)
	require.Contains(t, gotTask.LastError, channel.ErrStaleMeta.Error())
	require.Empty(t, store.promoteLearnerRequests)
}

func TestReplicaReplaceFinalCatchUpAfterLearnerIgnoresEmbeddedTransferFlag(t *testing.T) {
	now := time.UnixMilli(34200)
	task := replicaReplaceDrainedTask("task-embedded-final-replica", "channel-embedded-final-replica", 1, 3, now, slotmeta.ChannelMigrationPhaseFinalTargetCatchUp)
	task.EmbeddedLeaderTransfer = true
	task.EmbeddedDesiredLeader = 2
	task.DrainedChannelEpoch = task.BaseChannelEpoch + 1
	store := newFakeExecutorStore(task)
	meta := replicaReplaceFencedRuntimeMeta(task, 1, []uint64{1, 2, 3}, []uint64{1, 2}, now)
	meta.ChannelEpoch = task.DrainedChannelEpoch
	store.putRuntimeMeta(meta)
	probes := &recordingProbeClient{reports: map[channel.NodeID]ProbeReport{
		3: {
			ChannelKey:   leaderTransferChannelKey(task.ChannelID),
			ChannelEpoch: task.DrainedChannelEpoch,
			LeaderEpoch:  7,
			ReplicaID:    3,
			OffsetEpoch:  task.DrainedChannelEpoch,
			LogEndOffset: 20,
			CheckpointHW: 18,
			CommitReady:  true,
		},
	}}
	executor := newLeaderTransferExecutorHarness(store, &leaderTransferClock{now: now}, &recordingMigrationControl{}, probes, 9)

	require.NoError(t, executor.Tick(context.Background()))

	gotTask := store.task(task.TaskID)
	require.Equal(t, slotmeta.ChannelMigrationPhasePromoteAndRemove, gotTask.Phase)
	require.Equal(t, []channel.NodeID{3}, probes.calledNodes())
	require.Empty(t, store.leaderTransferCommits)
}

func TestReplicaReplaceDoesNotPromoteAfterRuntimeMetaReadExpiresOwnerLease(t *testing.T) {
	now := time.UnixMilli(34500)
	clock := &leaderTransferClock{now: now}
	task := replicaReplaceDrainedTask("task-promote-expired-owner", "channel-promote-expired-owner", 2, 3, now, slotmeta.ChannelMigrationPhasePromoteAndRemove)
	store := newFakeExecutorStore(task)
	store.putRuntimeMeta(replicaReplaceFencedRuntimeMeta(task, 1, []uint64{1, 2, 3}, []uint64{1, 2}, now))
	store.onGetRuntimeMeta = func() {
		clock.advance(10 * time.Millisecond)
	}
	executor := newLeaderTransferExecutorHarness(store, clock, &recordingMigrationControl{}, &recordingProbeClient{}, 9)
	executor.cfg.OwnerLease = 5 * time.Millisecond

	require.NoError(t, executor.Tick(context.Background()))

	gotTask := store.task(task.TaskID)
	require.Equal(t, slotmeta.ChannelMigrationPhasePromoteAndRemove, gotTask.Phase)
	require.Empty(t, store.promoteLearnerRequests)
	gotMeta := store.runtimeMeta(task.ChannelID, task.ChannelType)
	require.ElementsMatch(t, []uint64{1, 2, 3}, gotMeta.Replicas)
	require.ElementsMatch(t, []uint64{1, 2}, gotMeta.ISR)
}

func TestReplicaReplaceDoesNotPromoteWhenSourceIsStillLeader(t *testing.T) {
	now := time.UnixMilli(34750)
	task := replicaReplaceDrainedTask("task-promote-source-leader", "channel-promote-source-leader", 1, 3, now, slotmeta.ChannelMigrationPhasePromoteAndRemove)
	store := newFakeExecutorStore(task)
	store.putRuntimeMeta(replicaReplaceFencedRuntimeMeta(task, 1, []uint64{1, 2, 3}, []uint64{1, 2}, now))
	executor := newLeaderTransferExecutorHarness(store, &leaderTransferClock{now: now}, &recordingMigrationControl{}, &recordingProbeClient{}, 9)

	require.NoError(t, executor.Tick(context.Background()))

	gotTask := store.task(task.TaskID)
	require.Equal(t, slotmeta.ChannelMigrationPhasePromoteAndRemove, gotTask.Phase)
	require.Contains(t, gotTask.LastError, slotmeta.ErrStaleMeta.Error())
	require.Empty(t, store.promoteLearnerRequests)
	gotMeta := store.runtimeMeta(task.ChannelID, task.ChannelType)
	require.Equal(t, uint64(1), gotMeta.Leader)
	require.ElementsMatch(t, []uint64{1, 2, 3}, gotMeta.Replicas)
	require.ElementsMatch(t, []uint64{1, 2}, gotMeta.ISR)
}

func TestReplicaReplaceSnapshotRequiredBlocksBeforePromote(t *testing.T) {
	now := time.UnixMilli(35000)
	task := replicaReplaceDrainedTask("task-snapshot", "channel-snapshot-replace", 2, 3, now, slotmeta.ChannelMigrationPhaseFinalTargetCatchUp)
	store := newFakeExecutorStore(task)
	store.putRuntimeMeta(replicaReplaceFencedRuntimeMeta(task, 1, []uint64{1, 2, 3}, []uint64{1, 2}, now))
	probes := &recordingProbeClient{report: ProbeReport{
		ChannelKey:       leaderTransferChannelKey(task.ChannelID),
		ChannelEpoch:     5,
		LeaderEpoch:      7,
		ReplicaID:        3,
		SnapshotRequired: true,
	}}
	executor := newLeaderTransferExecutorHarness(store, &leaderTransferClock{now: now}, &recordingMigrationControl{}, probes, 9)

	require.NoError(t, executor.Tick(context.Background()))

	gotTask := store.task(task.TaskID)
	require.Equal(t, slotmeta.ChannelMigrationStatusBlocked, gotTask.Status)
	require.Equal(t, slotmeta.ChannelMigrationBlockerNeedsSnapshotBootstrap, gotTask.BlockerCode)
	require.Empty(t, store.promoteLearnerRequests)
}

func replicaReplaceTask(taskID, channelID string, source, target uint64, now time.Time) Task {
	task := executorTestTask(taskID, channelID, slotmeta.ChannelMigrationStatusPending, now.Add(-time.Second))
	task.Kind = slotmeta.ChannelMigrationKindReplicaReplace
	task.SourceNode = source
	task.TargetNode = target
	task.DesiredLeader = 0
	task.BaseChannelEpoch = 5
	task.BaseLeaderEpoch = 7
	return task
}

func replicaReplaceDrainedTask(taskID, channelID string, source, target uint64, now time.Time, phase slotmeta.ChannelMigrationPhase) Task {
	task := replicaReplaceTask(taskID, channelID, source, target, now)
	task.Status = slotmeta.ChannelMigrationStatusRunning
	task.Phase = phase
	task.FenceToken = task.TaskID
	task.FenceVersion = 4
	task.FenceUntilMS = now.Add(time.Minute).UnixMilli()
	task.CutoverLEO = 20
	task.CutoverHW = 18
	task.DrainedLeaderNode = 1
	task.DrainedRuntimeGeneration = 90
	task.DrainedChannelEpoch = 5
	task.DrainedLeaderEpoch = 7
	task.DrainedFenceVersion = 4
	return task
}

func replicaReplaceRuntimeMeta(channelID string, leader uint64, replicas, isr []uint64, now time.Time) slotmeta.ChannelRuntimeMeta {
	meta := leaderTransferRuntimeMeta(channelID, leader, leader, now)
	meta.Replicas = append([]uint64(nil), replicas...)
	meta.ISR = append([]uint64(nil), isr...)
	return meta
}

func replicaReplaceFencedRuntimeMeta(task Task, leader uint64, replicas, isr []uint64, now time.Time) slotmeta.ChannelRuntimeMeta {
	meta := replicaReplaceRuntimeMeta(task.ChannelID, leader, replicas, isr, now)
	meta.WriteFenceToken = task.TaskID
	meta.WriteFenceVersion = task.FenceVersion
	meta.WriteFenceReason = uint8(channel.WriteFenceReasonMigration)
	meta.WriteFenceUntilMS = task.FenceUntilMS
	return meta
}

func replicaReplaceProbeReport(channelID string, nodeID channel.NodeID, channelEpoch, leaderEpoch, leo, hw uint64) ProbeReport {
	return ProbeReport{
		ChannelKey:   leaderTransferChannelKey(channelID),
		ChannelEpoch: channelEpoch,
		LeaderEpoch:  leaderEpoch,
		ReplicaID:    nodeID,
		Leader:       1,
		Role:         channel.ReplicaRoleFollower,
		CommitReady:  true,
		OffsetEpoch:  channelEpoch,
		LogEndOffset: leo,
		CheckpointHW: hw,
	}
}
