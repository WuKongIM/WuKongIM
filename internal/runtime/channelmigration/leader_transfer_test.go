package channelmigration

import (
	"context"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	channelhandler "github.com/WuKongIM/WuKongIM/pkg/channel/handler"
	slotmeta "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
	"github.com/stretchr/testify/require"
)

func TestLeaderTransferHappyPath(t *testing.T) {
	now := time.UnixMilli(20000)
	clock := &leaderTransferClock{now: now}
	task := leaderTransferTask("task-happy", "channel-happy", 1, 2, now)
	store := newFakeExecutorStore(task)
	store.putRuntimeMeta(leaderTransferRuntimeMeta("channel-happy", 1, 2, now))
	drainer := &recordingMigrationControl{}
	probes := &recordingProbeClient{}
	executor := newLeaderTransferExecutorHarness(store, clock, drainer, probes, 9)

	for i := 0; i < 8; i++ {
		require.NoError(t, executor.Tick(context.Background()))
		clock.advance(100 * time.Millisecond)
	}

	gotTask := store.task(task.TaskID)
	require.Equal(t, slotmeta.ChannelMigrationStatusCompleted, gotTask.Status)
	require.Equal(t, slotmeta.ChannelMigrationPhaseClearFence, gotTask.Phase)
	require.Zero(t, gotTask.FenceToken)
	require.Zero(t, gotTask.CutoverHW)
	gotMeta := store.runtimeMeta(task.ChannelID, task.ChannelType)
	require.Equal(t, uint64(2), gotMeta.Leader)
	require.Equal(t, uint64(8), gotMeta.LeaderEpoch)
	require.Zero(t, gotMeta.WriteFenceToken)
	require.Equal(t, []channel.NodeID{1}, drainer.calledNodes())
	require.Equal(t, []channel.NodeID{2, 2}, probes.calledNodes())
}

func TestLeaderTransferRejectsTargetOutsideISR(t *testing.T) {
	now := time.UnixMilli(21000)
	task := leaderTransferTask("task-invalid-target", "channel-invalid-target", 1, 3, now)
	store := newFakeExecutorStore(task)
	meta := leaderTransferRuntimeMeta(task.ChannelID, 1, 2, now)
	meta.Replicas = []uint64{1, 2, 3}
	meta.ISR = []uint64{1, 2}
	store.putRuntimeMeta(meta)
	drainer := &recordingMigrationControl{}
	executor := newLeaderTransferExecutorHarness(store, &leaderTransferClock{now: now}, drainer, &recordingProbeClient{}, 9)

	err := executor.Tick(context.Background())

	require.NoError(t, err)
	gotTask := store.task(task.TaskID)
	require.Equal(t, slotmeta.ChannelMigrationStatusFailed, gotTask.Status)
	require.Equal(t, slotmeta.ChannelMigrationPhaseValidate, gotTask.Phase)
	require.Contains(t, gotTask.LastError, "target outside ISR")
	require.Empty(t, drainer.calls)
}

func TestLeaderTransferLaggingTargetWaitsAfterDrain(t *testing.T) {
	now := time.UnixMilli(22000)
	task := leaderTransferTask("task-lagging", "channel-lagging", 1, 2, now)
	task.Status = slotmeta.ChannelMigrationStatusRunning
	task.Phase = slotmeta.ChannelMigrationPhaseFinalTargetCatchUp
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
	store := newFakeExecutorStore(task)
	meta := leaderTransferRuntimeMeta(task.ChannelID, 1, 2, now)
	meta.WriteFenceToken = task.TaskID
	meta.WriteFenceVersion = 4
	meta.WriteFenceReason = uint8(channel.WriteFenceReasonMigration)
	meta.WriteFenceUntilMS = task.FenceUntilMS
	store.putRuntimeMeta(meta)
	probes := &recordingProbeClient{
		report: ProbeReport{
			ChannelKey:   leaderTransferChannelKey(task.ChannelID),
			ChannelEpoch: 5,
			LeaderEpoch:  7,
			ReplicaID:    2,
			OffsetEpoch:  5,
			LogEndOffset: 20,
			CheckpointHW: 17,
		},
	}
	executor := newLeaderTransferExecutorHarness(store, &leaderTransferClock{now: now}, &recordingMigrationControl{}, probes, 9)

	err := executor.Tick(context.Background())

	require.NoError(t, err)
	require.Empty(t, store.leaderTransferCommits)
	gotTask := store.task(task.TaskID)
	require.Equal(t, slotmeta.ChannelMigrationPhaseFinalTargetCatchUp, gotTask.Phase)
	require.Equal(t, uint64(20), gotTask.Progress.TargetLEO)
	require.Equal(t, uint64(17), gotTask.Progress.TargetCheckpointHW)
	require.Greater(t, gotTask.NextRunAtMS, now.UnixMilli())
	require.Contains(t, gotTask.LastError, channel.ErrNotReady.Error())
}

func TestLeaderTransferDrainsRemoteCurrentLeaderThroughMigrationControlClient(t *testing.T) {
	now := time.UnixMilli(23000)
	task := leaderTransferTask("task-remote-drain", "channel-remote-drain", 3, 2, now)
	task.Status = slotmeta.ChannelMigrationStatusRunning
	task.Phase = slotmeta.ChannelMigrationPhaseDrainLeader
	task.FenceToken = task.TaskID
	task.FenceVersion = 6
	task.FenceUntilMS = now.Add(time.Minute).UnixMilli()
	store := newFakeExecutorStore(task)
	meta := leaderTransferRuntimeMeta(task.ChannelID, 3, 2, now)
	meta.WriteFenceToken = task.TaskID
	meta.WriteFenceVersion = 6
	meta.WriteFenceReason = uint8(channel.WriteFenceReasonMigration)
	meta.WriteFenceUntilMS = task.FenceUntilMS
	store.putRuntimeMeta(meta)
	drainer := &recordingMigrationControl{}
	executor := newLeaderTransferExecutorHarness(store, &leaderTransferClock{now: now}, drainer, &recordingProbeClient{}, 9)

	err := executor.Tick(context.Background())

	require.NoError(t, err)
	require.Equal(t, []channel.NodeID{3}, drainer.calledNodes())
	require.Len(t, store.advances, 1)
	require.Equal(t, uint64(6), store.advances[0].CutoverProof.DrainedFenceVersion)
}

func TestLeaderTransferPersistsProgressBetweenPhaseOnlySteps(t *testing.T) {
	now := time.UnixMilli(24000)
	task := leaderTransferTask("task-progress", "channel-progress", 1, 2, now)
	task.Status = slotmeta.ChannelMigrationStatusRunning
	task.Phase = slotmeta.ChannelMigrationPhaseProbeTarget
	store := newFakeExecutorStore(task)
	store.putRuntimeMeta(leaderTransferRuntimeMeta(task.ChannelID, 1, 2, now))
	probes := &recordingProbeClient{
		report: ProbeReport{
			ChannelKey:       leaderTransferChannelKey(task.ChannelID),
			ChannelEpoch:     5,
			LeaderEpoch:      7,
			ReplicaID:        2,
			OffsetEpoch:      5,
			LogEndOffset:     33,
			CheckpointHW:     31,
			LogStartOffset:   1,
			SnapshotRequired: false,
		},
	}
	executor := newLeaderTransferExecutorHarness(store, &leaderTransferClock{now: now}, &recordingMigrationControl{}, probes, 9)

	err := executor.Tick(context.Background())

	require.NoError(t, err)
	gotTask := store.task(task.TaskID)
	require.Equal(t, slotmeta.ChannelMigrationPhaseWriteFence, gotTask.Phase)
	require.Equal(t, uint64(33), gotTask.Progress.TargetLEO)
	require.Equal(t, uint64(31), gotTask.Progress.TargetCheckpointHW)
}

func TestLeaderTransferRejectsCommitWhenFenceVersionChanged(t *testing.T) {
	now := time.UnixMilli(25000)
	task := leaderTransferTask("task-version-changed", "channel-version-changed", 1, 2, now)
	task.Status = slotmeta.ChannelMigrationStatusRunning
	task.Phase = slotmeta.ChannelMigrationPhaseCommitLeaderMeta
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
	store := newFakeExecutorStore(task)
	meta := leaderTransferRuntimeMeta(task.ChannelID, 1, 2, now)
	meta.WriteFenceToken = task.TaskID
	meta.WriteFenceVersion = 5
	meta.WriteFenceReason = uint8(channel.WriteFenceReasonMigration)
	meta.WriteFenceUntilMS = task.FenceUntilMS
	store.putRuntimeMeta(meta)
	executor := newLeaderTransferExecutorHarness(store, &leaderTransferClock{now: now}, &recordingMigrationControl{}, &recordingProbeClient{}, 9)

	err := executor.Tick(context.Background())

	require.ErrorIs(t, err, slotmeta.ErrStaleMeta)
	gotMeta := store.runtimeMeta(task.ChannelID, task.ChannelType)
	require.Equal(t, uint64(1), gotMeta.Leader)
	require.Empty(t, store.clearFenceRequests)
}

func TestLeaderTransferFenceExpiryResetsToPreCutoverWithVersionBump(t *testing.T) {
	now := time.UnixMilli(26000)
	task := leaderTransferTask("task-expired-fence", "channel-expired-fence", 1, 2, now)
	task.Status = slotmeta.ChannelMigrationStatusRunning
	task.Phase = slotmeta.ChannelMigrationPhaseFinalTargetCatchUp
	task.FenceToken = task.TaskID
	task.FenceVersion = 7
	task.FenceUntilMS = now.Add(-time.Millisecond).UnixMilli()
	task.CutoverLEO = 20
	task.CutoverHW = 18
	task.DrainedLeaderNode = 1
	task.DrainedRuntimeGeneration = 90
	task.DrainedChannelEpoch = 5
	task.DrainedLeaderEpoch = 7
	task.DrainedFenceVersion = 7
	store := newFakeExecutorStore(task)
	meta := leaderTransferRuntimeMeta(task.ChannelID, 1, 2, now)
	meta.WriteFenceToken = task.TaskID
	meta.WriteFenceVersion = 7
	meta.WriteFenceReason = uint8(channel.WriteFenceReasonMigration)
	meta.WriteFenceUntilMS = task.FenceUntilMS
	store.putRuntimeMeta(meta)
	executor := newLeaderTransferExecutorHarness(store, &leaderTransferClock{now: now}, &recordingMigrationControl{}, &recordingProbeClient{}, 9)

	err := executor.Tick(context.Background())

	require.NoError(t, err)
	gotTask := store.task(task.TaskID)
	require.Equal(t, slotmeta.ChannelMigrationPhaseWriteFence, gotTask.Phase)
	require.Zero(t, gotTask.FenceToken)
	require.Zero(t, gotTask.CutoverHW)
	gotMeta := store.runtimeMeta(task.ChannelID, task.ChannelType)
	require.Zero(t, gotMeta.WriteFenceToken)
	require.Equal(t, uint64(8), gotMeta.WriteFenceVersion)
	require.Len(t, store.resetFenceRequests, 1)
}

func newLeaderTransferExecutorHarness(store *fakeExecutorStore, clock *leaderTransferClock, drainer *recordingMigrationControl, probes *recordingProbeClient, localNode channel.NodeID) *Executor {
	return NewExecutor(ExecutorOptions{
		Store:            store,
		Slots:            newFakeSlotLeadership().withDefault(true),
		ProbeClient:      probes,
		MigrationControl: drainer,
		Metrics:          noopMetrics{},
		LocalNode:        localNode,
		Now:              clock.Now,
		Config: Config{
			ScanLimit:            16,
			OwnerLease:           time.Minute,
			RetryBackoff:         time.Second,
			FenceLease:           time.Minute,
			LeaderLease:          time.Minute,
			MaxConcurrentSources: 0,
			MaxConcurrentTargets: 0,
		},
	})
}

type leaderTransferClock struct {
	now time.Time
}

func (c *leaderTransferClock) Now() time.Time {
	return c.now
}

func (c *leaderTransferClock) advance(d time.Duration) {
	c.now = c.now.Add(d)
}

type recordingMigrationControl struct {
	calls []migrationDrainCall
	err   error
}

type migrationDrainCall struct {
	nodeID channel.NodeID
	req    channel.FenceAndDrainRequest
}

func (c *recordingMigrationControl) FenceAndDrain(ctx context.Context, nodeID channel.NodeID, req channel.FenceAndDrainRequest) (channel.DrainResult, error) {
	c.calls = append(c.calls, migrationDrainCall{nodeID: nodeID, req: req})
	if c.err != nil {
		return channel.DrainResult{}, c.err
	}
	return channel.DrainResult{
		ChannelKey:        req.ChannelKey,
		LEO:               20,
		HW:                18,
		CheckpointHW:      18,
		ChannelEpoch:      req.ExpectedChannelEpoch,
		LeaderEpoch:       req.ExpectedLeaderEpoch,
		WriteFenceVersion: req.WriteFenceVersion,
		RuntimeGeneration: 90,
	}, nil
}

func (c *recordingMigrationControl) calledNodes() []channel.NodeID {
	out := make([]channel.NodeID, 0, len(c.calls))
	for _, call := range c.calls {
		out = append(out, call.nodeID)
	}
	return out
}

type recordingProbeClient struct {
	report ProbeReport
	err    error
	calls  []probeCall
}

type probeCall struct {
	nodeID channel.NodeID
	meta   channel.Meta
}

func (c *recordingProbeClient) ProbeChannel(ctx context.Context, nodeID channel.NodeID, meta channel.Meta) (ProbeReport, error) {
	c.calls = append(c.calls, probeCall{nodeID: nodeID, meta: meta})
	if c.err != nil {
		return ProbeReport{}, c.err
	}
	if c.report.ChannelKey != "" {
		return c.report, nil
	}
	return ProbeReport{
		ChannelKey:   meta.Key,
		ChannelEpoch: meta.Epoch,
		LeaderEpoch:  meta.LeaderEpoch,
		ReplicaID:    nodeID,
		OffsetEpoch:  meta.Epoch,
		LogEndOffset: 20,
		CheckpointHW: 18,
	}, nil
}

func (c *recordingProbeClient) calledNodes() []channel.NodeID {
	out := make([]channel.NodeID, 0, len(c.calls))
	for _, call := range c.calls {
		out = append(out, call.nodeID)
	}
	return out
}

func leaderTransferTask(taskID, channelID string, source, target uint64, now time.Time) Task {
	task := executorTestTask(taskID, channelID, slotmeta.ChannelMigrationStatusPending, now.Add(-time.Second))
	task.Kind = slotmeta.ChannelMigrationKindLeaderTransfer
	task.SourceNode = source
	task.TargetNode = target
	task.DesiredLeader = target
	task.BaseChannelEpoch = 5
	task.BaseLeaderEpoch = 7
	return task
}

func leaderTransferRuntimeMeta(channelID string, leader, target uint64, now time.Time) slotmeta.ChannelRuntimeMeta {
	return slotmeta.ChannelRuntimeMeta{
		ChannelID:    channelID,
		ChannelType:  1,
		ChannelEpoch: 5,
		LeaderEpoch:  7,
		Replicas:     []uint64{leader, target},
		ISR:          []uint64{leader, target},
		Leader:       leader,
		MinISR:       1,
		Status:       uint8(channel.StatusActive),
		LeaseUntilMS: now.Add(time.Minute).UnixMilli(),
	}
}

func leaderTransferChannelKey(channelID string) channel.ChannelKey {
	return channelhandler.KeyFromChannelID(channel.ChannelID{ID: channelID, Type: 1})
}

func (s *fakeExecutorStore) putRuntimeMeta(meta slotmeta.ChannelRuntimeMeta) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runtimeMetas[runtimeMetaFakeKey(meta.ChannelID, meta.ChannelType)] = meta
}

func (s *fakeExecutorStore) runtimeMeta(channelID string, channelType int64) slotmeta.ChannelRuntimeMeta {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.runtimeMetas[runtimeMetaFakeKey(channelID, channelType)]
}
