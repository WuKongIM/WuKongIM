package channels

import (
	"context"
	"fmt"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/routing"
	metafsm "github.com/WuKongIM/WuKongIM/pkg/slot/fsm"
	"testing"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/stretchr/testify/require"
)

type preemptibleRepairStore struct {
	fakeRepairScannerStore
	aborts int
}

func (s *preemptibleRepairStore) AbortReplicaReplacementForFailover(context.Context, ch.ChannelID, uint64, uint64) (bool, error) {
	s.aborts++
	return true, nil
}

func TestRepairScannerPreemptsReplacementBeforeDeadLeaderFailover(t *testing.T) {
	id := ch.ChannelID{ID: "repair-preemption", Type: 1}
	source := newFakeRepairScannerSource(id)
	source.active[id] = true
	source.slotPages[1] = [][]metadb.ChannelRuntimeMeta{{failoverPlannerMeta(id), failoverPlannerMeta(id)}}
	store := &preemptibleRepairStore{}
	scanner := NewRepairScanner(RepairScannerConfig{Enabled: true, PageLimit: 10, MaxPagesPerTick: 10, MaxTasksPerTick: 1}, source, store)
	_, err := scanner.RunOnce(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, store.aborts, "dead leader must not remain hidden behind a replacement task; mutation budget still applies")
	require.Empty(t, store.requests, "failover must use a fresh metadata snapshot on the next tick")
}

func TestReplicaReplacementPreemptionPreservesPromotedAndChangedAuthority(t *testing.T) {
	for _, name := range []string{"pre-promotion", "promoted", "verified", "embedded", "leader-changed", "epoch-changed", "terminal", "missing"} {
		t.Run(name, func(t *testing.T) {
			id := ch.ChannelID{ID: "preempt-guard", Type: 1}
			meta := testMigrationRuntimeMeta(id)
			task := testMigrationTask(id, "guarded-replacement")
			task.Status = metadb.ChannelMigrationStatusRunning
			task.Phase = metadb.ChannelMigrationPhaseCutoverFence
			meta.Replicas = append(meta.Replicas, task.TargetNode)
			leader, epoch := meta.Leader, meta.LeaderEpoch
			switch name {
			case "promoted":
				meta.ISR = append(meta.ISR, task.TargetNode)
			case "verified":
				task.Phase = metadb.ChannelMigrationPhaseVerifyMembership
			case "embedded":
				task.EmbeddedLeaderTransfer = true
			case "leader-changed":
				leader++
			case "epoch-changed":
				epoch++
			case "terminal":
				task.Status = metadb.ChannelMigrationStatusCompleted
			}
			active := map[uint16]metadb.ChannelMigrationTask{22: task}
			if name == "missing" {
				delete(active, 22)
			}
			reader := &fakeMigrationReader{runtimeMeta: map[uint16]metadb.ChannelRuntimeMeta{22: meta}, active: active}
			proposer := &fakeMigrationProposer{}
			now := time.UnixMilli(task.UpdatedAtMS + 1)
			store := NewMigrationStore(MigrationStoreConfig{LocalNode: 2, Router: fakeMigrationRouter{routes: map[string]routing.Route{id.ID: {HashSlot: 22, SlotID: 32, Leader: 2}}}, Reader: reader, Proposer: proposer, Now: func() time.Time { return now }})
			aborted, err := store.AbortReplicaReplacementForFailover(context.Background(), id, leader, epoch)
			require.NoError(t, err)
			require.Equal(t, name == "pre-promotion", aborted)
			if !aborted {
				require.Empty(t, proposer.lastCommand)
				return
			}
			expected := metadb.ChannelMigrationAbortRequest{Guard: migrationTaskGuard(task, task.UpdatedAtMS), RuntimeGuard: migrationRuntimeGuard(meta), Status: metadb.ChannelMigrationStatusAborted, Phase: task.Phase, UpdatedAtMS: now.UnixMilli(), CompletedAtMS: now.UnixMilli(), LastError: "leader unavailable before replica promotion"}
			require.Equal(t, metafsm.EncodeAbortChannelMigrationCommand(expected), proposer.lastCommand)
		})
	}
}

type promotedCompletingRepairStore struct {
	fakeRepairScannerStore
	aborts      int
	completions int
	complete    bool
}

func (s *promotedCompletingRepairStore) AbortReplicaReplacementForFailover(context.Context, ch.ChannelID, uint64, uint64) (bool, error) {
	s.aborts++
	return false, nil
}

func (s *promotedCompletingRepairStore) CompletePromotedReplicaReplacementForFailover(context.Context, ch.ChannelID, uint64, uint64) (bool, error) {
	s.completions++
	return s.complete, nil
}

// Issue #1077: a promoted replacement must yield to failover instead of
// hiding the dead leader forever.
func TestRepairScannerCompletesPromotedReplacementBeforeDeadLeaderFailover(t *testing.T) {
	id := ch.ChannelID{ID: "repair-promoted", Type: 1}
	source := newFakeRepairScannerSource(id)
	source.active[id] = true
	source.slotPages[1] = [][]metadb.ChannelRuntimeMeta{{failoverPlannerMeta(id), failoverPlannerMeta(id)}}
	store := &promotedCompletingRepairStore{complete: true}
	scanner := NewRepairScanner(RepairScannerConfig{Enabled: true, PageLimit: 10, MaxPagesPerTick: 10, MaxTasksPerTick: 1}, source, store)

	result, err := scanner.RunOnce(context.Background())

	require.NoError(t, err)
	require.Equal(t, 1, store.aborts, "unpromoted abort is tried first")
	require.Equal(t, 1, store.completions, "the mutation budget stops the second row")
	require.Equal(t, 1, result.TasksCompletedForFailover)
	require.Zero(t, result.TasksAborted)
	require.Empty(t, store.requests, "failover must use a fresh metadata snapshot on the next tick")

	source.active[id] = false
	source.slotPages[1] = [][]metadb.ChannelRuntimeMeta{{failoverPlannerMeta(id)}}
	nextPass := NewRepairScanner(RepairScannerConfig{Enabled: true, PageLimit: 10, MaxPagesPerTick: 10, MaxTasksPerTick: 1}, source, store)
	_, err = nextPass.RunOnce(context.Background())
	require.NoError(t, err)
	require.Len(t, store.requests, 1, "the next tick elects once the promoted task is completed")
}

func TestRepairScannerLeavesPromotedReplacementWithHealthyLeader(t *testing.T) {
	id := ch.ChannelID{ID: "repair-promoted-healthy", Type: 1}
	source := newFakeRepairScannerSource(id)
	source.active[id] = true
	source.nodes = failoverHealthyNodes(1, 2, 3)
	source.slotPages[1] = [][]metadb.ChannelRuntimeMeta{{failoverPlannerMeta(id)}}
	store := &promotedCompletingRepairStore{complete: true}
	scanner := NewRepairScanner(RepairScannerConfig{Enabled: true, PageLimit: 10, MaxPagesPerTick: 10, MaxTasksPerTick: 1}, source, store)

	result, err := scanner.RunOnce(context.Background())

	require.NoError(t, err)
	require.Zero(t, store.completions)
	require.Zero(t, result.TasksCompletedForFailover)
}

func promotedReplicaReplaceFixture(id ch.ChannelID) (metadb.ChannelRuntimeMeta, metadb.ChannelMigrationTask) {
	task := testMigrationTask(id, "promoted-replacement")
	task.Status = metadb.ChannelMigrationStatusRunning
	task.Phase = metadb.ChannelMigrationPhaseVerifyMembership
	task.SourceNode = 3
	task.TargetNode = 4
	task.FenceToken = task.TaskID
	task.FenceVersion = 7
	task.FenceUntilMS = task.UpdatedAtMS + 30_000
	meta := testMigrationRuntimeMeta(id)
	meta.Replicas = []uint64{1, 2, 4}
	meta.ISR = []uint64{1, 2, 4}
	meta.WriteFenceToken = task.TaskID
	meta.WriteFenceVersion = 7
	return meta, task
}

func TestPromotedReplicaReplacementCompletionGuards(t *testing.T) {
	for _, name := range []string{
		"promoted", "pre-promotion", "embedded", "leader-changed", "epoch-changed", "terminal", "missing",
		"leader-transfer", "source-still-replica", "target-not-in-isr", "isr-below-min", "foreign-fence", "fence-version-changed",
	} {
		t.Run(name, func(t *testing.T) {
			id := ch.ChannelID{ID: "promoted-guard", Type: 1}
			meta, task := promotedReplicaReplaceFixture(id)
			leader, epoch := meta.Leader, meta.LeaderEpoch
			switch name {
			case "pre-promotion":
				task.Phase = metadb.ChannelMigrationPhasePromoteAndRemove
			case "embedded":
				task.EmbeddedLeaderTransfer = true
			case "leader-changed":
				leader++
			case "epoch-changed":
				epoch++
			case "terminal":
				task.Status = metadb.ChannelMigrationStatusCompleted
			case "leader-transfer":
				task.Kind = metadb.ChannelMigrationKindLeaderTransfer
			case "source-still-replica":
				meta.Replicas = []uint64{1, 2, 3, 4}
			case "target-not-in-isr":
				meta.ISR = []uint64{1, 2}
			case "isr-below-min":
				meta.MinISR = 4
			case "foreign-fence":
				meta.WriteFenceToken = "another-task"
			case "fence-version-changed":
				meta.WriteFenceVersion = 8
			}
			active := map[uint16]metadb.ChannelMigrationTask{22: task}
			if name == "missing" {
				delete(active, 22)
			}
			reader := &fakeMigrationReader{runtimeMeta: map[uint16]metadb.ChannelRuntimeMeta{22: meta}, active: active}
			proposer := &fakeMigrationProposer{}
			now := time.UnixMilli(task.UpdatedAtMS + 1)
			store := NewMigrationStore(MigrationStoreConfig{LocalNode: 2, Router: fakeMigrationRouter{routes: map[string]routing.Route{id.ID: {HashSlot: 22, SlotID: 32, Leader: 2}}}, Reader: reader, Proposer: proposer, Now: func() time.Time { return now }})

			completed, err := store.CompletePromotedReplicaReplacementForFailover(context.Background(), id, leader, epoch)

			require.NoError(t, err)
			require.Equal(t, name == "promoted", completed)
			if !completed {
				require.Empty(t, proposer.lastCommand)
				return
			}
			expected := metadb.ChannelMigrationClearFenceRequest{
				Guard:         migrationTaskGuard(task, task.UpdatedAtMS),
				RuntimeGuard:  migrationRuntimeGuard(meta),
				Status:        metadb.ChannelMigrationStatusCompleted,
				Phase:         metadb.ChannelMigrationPhaseClearFence,
				UpdatedAtMS:   now.UnixMilli(),
				CompletedAtMS: now.UnixMilli(),
			}
			require.Equal(t, metafsm.EncodeClearChannelWriteFenceCommand(expected), proposer.lastCommand)
		})
	}
}

// A concurrent executor or newer metadata wins; the scanner must not report an error.
func TestPromotedReplicaReplacementCompletionYieldsToConcurrentChange(t *testing.T) {
	for _, proposeErr := range []error{metadb.ErrStaleMeta, fmt.Errorf("propose: %w", metadb.ErrStaleMeta)} {
		id := ch.ChannelID{ID: "promoted-race", Type: 1}
		meta, task := promotedReplicaReplaceFixture(id)
		reader := &fakeMigrationReader{runtimeMeta: map[uint16]metadb.ChannelRuntimeMeta{22: meta}, active: map[uint16]metadb.ChannelMigrationTask{22: task}}
		proposer := &fakeMigrationProposer{err: proposeErr}
		store := NewMigrationStore(MigrationStoreConfig{LocalNode: 2, Router: fakeMigrationRouter{routes: map[string]routing.Route{id.ID: {HashSlot: 22, SlotID: 32, Leader: 2}}}, Reader: reader, Proposer: proposer, Now: func() time.Time { return time.UnixMilli(task.UpdatedAtMS + 1) }})

		completed, err := store.CompletePromotedReplicaReplacementForFailover(context.Background(), id, meta.Leader, meta.LeaderEpoch)

		require.NoError(t, err, "propose error %v", proposeErr)
		require.False(t, completed)
	}
}
