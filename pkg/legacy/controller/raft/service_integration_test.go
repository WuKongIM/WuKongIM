//go:build integration

package raft

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/hashslot"
	controllermeta "github.com/WuKongIM/WuKongIM/pkg/legacy/controller/meta"
	slotcontroller "github.com/WuKongIM/WuKongIM/pkg/legacy/controller/plane"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/stretchr/testify/require"
	"go.etcd.io/raft/v3/raftpb"
)

func TestServiceRestartRestoresSnapshotCreatedByCompaction(t *testing.T) {
	env := newTestEnv(t, []uint64{1})
	defer env.stopAll()

	compactionCfg := func(cfg *Config) {
		cfg.LogCompaction = LogCompactionConfig{
			Enabled:        true,
			EnabledSet:     true,
			TriggerEntries: 2,
			CheckInterval:  time.Nanosecond,
		}
	}
	env.startNodeWithConfig(t, 1, nil, compactionCfg)
	env.waitForLeader(t, []uint64{1})

	node := env.nodes[1]
	store := node.logDB.ForController()
	require.NoError(t, node.service.Propose(context.Background(), nodeJoinCommand(2)))
	snap2 := waitForControllerSnapshotIndex(t, store, 2)
	require.Equal(t, uint64(2), snap2.Metadata.Index)

	require.NoError(t, node.service.Propose(context.Background(), nodeJoinCommand(3)))
	require.Eventually(t, func() bool {
		state, err := store.InitialState(context.Background())
		return err == nil && state.AppliedIndex >= 3
	}, 5*time.Second, 10*time.Millisecond)

	snapAfterNode3, err := store.Snapshot(context.Background())
	require.NoError(t, err)
	require.Equal(t, uint64(2), snapAfterNode3.Metadata.Index)

	first, err := store.FirstIndex(context.Background())
	require.NoError(t, err)
	require.Equal(t, uint64(3), first)
	last, err := store.LastIndex(context.Background())
	require.NoError(t, err)
	entries, err := store.Entries(context.Background(), first, last+1, 0)
	require.NoError(t, err)
	require.Contains(t, entryIndexes(entries), uint64(3))

	env.stopNode(1)
	require.NoError(t, os.RemoveAll(filepath.Join(node.dir, "controller-meta")))
	env.startNodeWithConfig(t, 1, nil, compactionCfg)

	require.Eventually(t, func() bool {
		_, err := env.nodes[1].meta.GetNode(context.Background(), 2)
		return err == nil
	}, 5*time.Second, 10*time.Millisecond)
	require.Eventually(t, func() bool {
		_, err := env.nodes[1].meta.GetNode(context.Background(), 3)
		return err == nil
	}, 5*time.Second, 10*time.Millisecond)
}

func TestServiceProposeAppliesAddSlotCommand(t *testing.T) {
	env := newTestEnv(t, []uint64{1})
	defer env.stopAll()

	env.startNode(t, 1, nil)
	env.waitForLeader(t, []uint64{1})

	node := env.nodes[1]
	ctx := context.Background()

	require.NoError(t, node.meta.SaveHashSlotTable(ctx, hashslot.NewHashSlotTable(8, 2)))
	require.NoError(t, node.service.Propose(ctx, slotcontroller.Command{
		Kind: slotcontroller.CommandKindAddSlot,
		AddSlot: &slotcontroller.AddSlotRequest{
			NewSlotID:       3,
			Peers:           []uint64{1},
			PreferredLeader: 1,
		},
	}))

	assignment, err := node.meta.GetAssignment(ctx, 3)
	require.NoError(t, err)
	require.Equal(t, uint32(3), assignment.SlotID)
	require.Equal(t, []uint64{1}, assignment.DesiredPeers)
	require.Equal(t, uint64(1), assignment.PreferredLeader)

	task, err := node.meta.GetTask(ctx, 3)
	require.NoError(t, err)
	require.Equal(t, controllermeta.TaskKindBootstrap, task.Kind)
	require.Equal(t, controllermeta.TaskStepAddLearner, task.Step)
	require.Equal(t, uint64(1), task.TargetNode)

	table, err := node.meta.LoadHashSlotTable(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, table.ActiveMigrations())
}

func TestServiceProposeAppliesRemoveSlotCommand(t *testing.T) {
	env := newTestEnv(t, []uint64{1})
	defer env.stopAll()

	env.startNode(t, 1, nil)
	env.waitForLeader(t, []uint64{1})

	node := env.nodes[1]
	ctx := context.Background()

	require.NoError(t, node.meta.SaveHashSlotTable(ctx, hashslot.NewHashSlotTable(8, 3)))
	require.NoError(t, node.service.Propose(ctx, slotcontroller.Command{
		Kind: slotcontroller.CommandKindRemoveSlot,
		RemoveSlot: &slotcontroller.RemoveSlotRequest{
			SlotID: 3,
		},
	}))

	table, err := node.meta.LoadHashSlotTable(ctx)
	require.NoError(t, err)

	active := table.ActiveMigrations()
	require.NotEmpty(t, active)
	for _, migration := range active {
		require.Equal(t, multiraft.SlotID(3), migration.Source)
		require.NotEqual(t, multiraft.SlotID(3), migration.Target)
	}
}

func entryIndexes(entries []raftpb.Entry) []uint64 {
	out := make([]uint64, 0, len(entries))
	for _, entry := range entries {
		out = append(out, entry.Index)
	}
	return out
}

func TestServiceBootstrapsOnlyOnSmallestDerivedPeer(t *testing.T) {
	env := newTestEnv(t, []uint64{1, 2, 3})
	defer env.stopAll()

	counts := env.captureBootstrapCalls(t)

	env.startNode(t, 2, nil)
	env.startNode(t, 3, nil)
	require.Equal(t, map[uint64]int{}, counts.snapshot())

	env.startNode(t, 1, nil)
	env.waitForLeader(t, []uint64{1, 2, 3})

	require.Equal(t, map[uint64]int{1: 1}, counts.snapshot())
}

func TestServiceRestartUsesPersistedMembershipInsteadOfConfigDrift(t *testing.T) {
	env := newTestEnv(t, []uint64{1, 2, 3})
	defer env.stopAll()

	counts := env.captureBootstrapCalls(t)

	env.startNode(t, 1, nil)
	env.startNode(t, 2, nil)
	env.startNode(t, 3, nil)
	env.waitForLeader(t, []uint64{1, 2, 3})

	state := env.mustInitialState(t, 1)
	require.Equal(t, []uint64{1, 2, 3}, sortedPeers(state.ConfState.Voters))
	require.Equal(t, map[uint64]int{1: 1}, counts.snapshot())

	env.restartNode(t, 1, []Peer{})
	env.waitForLeader(t, []uint64{1, 2, 3})

	restarted := env.mustInitialState(t, 1)
	require.Equal(t, []uint64{1, 2, 3}, sortedPeers(restarted.ConfState.Voters))
	require.Equal(t, map[uint64]int{1: 1}, counts.snapshot())
}

func TestServiceRestartTrustsPersistedMembershipWhenConfigOmitsLocalNode(t *testing.T) {
	env := newTestEnv(t, []uint64{1, 2, 3})
	defer env.stopAll()

	counts := env.captureBootstrapCalls(t)

	env.startNode(t, 1, nil)
	env.startNode(t, 2, nil)
	env.startNode(t, 3, nil)
	env.waitForLeader(t, []uint64{1, 2, 3})

	state := env.mustInitialState(t, 1)
	require.Equal(t, []uint64{1, 2, 3}, sortedPeers(state.ConfState.Voters))
	require.Equal(t, map[uint64]int{1: 1}, counts.snapshot())

	env.restartNode(t, 1, []Peer{
		{NodeID: 2, Addr: env.addrOf(2)},
		{NodeID: 3, Addr: env.addrOf(3)},
	})
	env.waitForLeader(t, []uint64{1, 2, 3})

	restarted := env.mustInitialState(t, 1)
	require.Equal(t, []uint64{1, 2, 3}, sortedPeers(restarted.ConfState.Voters))
	require.Equal(t, map[uint64]int{1: 1}, counts.snapshot())
}

func TestServiceStatusBeforeStartAndAfterStartReportsNodeAndRole(t *testing.T) {
	service := NewService(Config{NodeID: 1})
	before := service.Status()
	require.Equal(t, uint64(1), before.NodeID)
	require.Equal(t, RoleUnknown, before.Role)
	require.True(t, before.Compaction.Enabled)

	env := newTestEnv(t, []uint64{1})
	defer env.stopAll()
	env.startNode(t, 1, nil)
	env.waitForLeader(t, []uint64{1})

	node := env.nodes[1]
	require.Eventually(t, func() bool {
		st := node.service.Status()
		return st.Role == RoleLeader && st.LeaderID == 1 && st.Term > 0
	}, 2*time.Second, 10*time.Millisecond)
}

func TestServiceStatusAfterStopClearsVolatileRaftState(t *testing.T) {
	env := newTestEnv(t, []uint64{1})
	defer env.stopAll()

	env.startNode(t, 1, nil)
	env.waitForLeader(t, []uint64{1})
	require.Equal(t, RoleLeader, env.nodes[1].service.Status().Role)
	sentinel := errors.New("preserve diagnostic")
	env.nodes[1].service.recordCompactionFailure(sentinel, time.Unix(100, 0))
	env.nodes[1].service.recordRestoreFailure(raftpb.Snapshot{
		Metadata: raftpb.SnapshotMetadata{Index: 9, Term: 3},
	}, sentinel)

	require.NoError(t, env.nodes[1].service.Stop())
	st := env.nodes[1].service.Status()
	require.Equal(t, RoleUnknown, st.Role)
	require.Equal(t, uint64(0), st.LeaderID)
	require.Equal(t, uint64(0), st.Term)
	require.Empty(t, st.Peers)
	require.True(t, st.Compaction.Degraded)
	require.Equal(t, sentinel.Error(), st.Compaction.LastError)
	require.True(t, st.Restore.Failed)
	require.Equal(t, uint64(9), st.Restore.LastSnapshotIndex)
}

func TestServiceProposeAppliesMigrationCommand(t *testing.T) {
	env := newTestEnv(t, []uint64{1})
	defer env.stopAll()

	env.startNode(t, 1, nil)
	env.waitForLeader(t, []uint64{1})

	node := env.nodes[1]
	ctx := context.Background()

	require.NoError(t, node.meta.SaveHashSlotTable(ctx, hashslot.NewHashSlotTable(8, 2)))
	require.NoError(t, node.service.Propose(ctx, slotcontroller.Command{
		Kind: slotcontroller.CommandKindStartMigration,
		Migration: &slotcontroller.MigrationRequest{
			HashSlot: 3,
			Source:   1,
			Target:   2,
		},
	}))
	require.NoError(t, node.service.Propose(ctx, slotcontroller.Command{
		Kind: slotcontroller.CommandKindAdvanceMigration,
		Migration: &slotcontroller.MigrationRequest{
			HashSlot: 3,
			Source:   1,
			Target:   2,
			Phase:    uint8(hashslot.PhaseDelta),
		},
	}))

	table, err := node.meta.LoadHashSlotTable(ctx)
	require.NoError(t, err)

	migration := table.GetMigration(3)
	require.NotNil(t, migration)
	require.Equal(t, uint16(3), migration.HashSlot)
	require.Equal(t, multiraft.SlotID(1), migration.Source)
	require.Equal(t, multiraft.SlotID(2), migration.Target)
	require.Equal(t, hashslot.PhaseDelta, migration.Phase)
}

func TestServiceCompactsControllerLogAfterAppliedThreshold(t *testing.T) {
	env := newTestEnv(t, []uint64{1})
	defer env.stopAll()

	env.startNodeWithConfig(t, 1, nil, func(cfg *Config) {
		cfg.LogCompaction = LogCompactionConfig{
			Enabled:        true,
			EnabledSet:     true,
			TriggerEntries: 1,
			CheckInterval:  time.Nanosecond,
		}
	})
	env.waitForLeader(t, []uint64{1})

	node := env.nodes[1]
	require.NoError(t, node.service.Propose(context.Background(), nodeJoinCommand(2)))

	snap := waitForControllerSnapshotIndex(t, node.logDB.ForController(), 2)
	first, err := node.logDB.ForController().FirstIndex(context.Background())
	require.NoError(t, err)
	require.Equal(t, snap.Metadata.Index+1, first)
}

func TestServiceControllerLogCompactionCanBeDisabled(t *testing.T) {
	env := newTestEnv(t, []uint64{1})
	defer env.stopAll()

	env.startNodeWithConfig(t, 1, nil, func(cfg *Config) {
		cfg.LogCompaction = LogCompactionConfig{
			Enabled:        false,
			EnabledSet:     true,
			TriggerEntries: 1,
			CheckInterval:  time.Nanosecond,
		}
	})
	env.waitForLeader(t, []uint64{1})

	node := env.nodes[1]
	require.NoError(t, node.service.Propose(context.Background(), nodeJoinCommand(2)))
	require.NoError(t, node.service.Propose(context.Background(), nodeJoinCommand(3)))

	require.Never(t, func() bool {
		snap, err := node.logDB.ForController().Snapshot(context.Background())
		return err == nil && snap.Metadata.Index > 0
	}, 300*time.Millisecond, 10*time.Millisecond)

	first, err := node.logDB.ForController().FirstIndex(context.Background())
	require.NoError(t, err)
	require.Equal(t, uint64(1), first)

	last, err := node.logDB.ForController().LastIndex(context.Background())
	require.NoError(t, err)
	entries, err := node.logDB.ForController().Entries(context.Background(), first, last+1, 0)
	require.NoError(t, err)
	require.NotEmpty(t, entries)
}

func TestServiceCompactionFailureDoesNotFailProposalLoop(t *testing.T) {
	env := newTestEnv(t, []uint64{1})
	defer env.stopAll()

	env.startNodeWithConfig(t, 1, nil, func(cfg *Config) {
		cfg.LogCompaction = LogCompactionConfig{
			Enabled:        true,
			EnabledSet:     true,
			TriggerEntries: 1,
			CheckInterval:  time.Nanosecond,
		}
	})
	env.waitForLeader(t, []uint64{1})

	sentinel := errors.New("compact once")
	var hookCalls atomic.Uint32
	setCompactControllerLogHookForTest(func() error {
		hookCalls.Add(1)
		return sentinel
	})
	t.Cleanup(func() {
		setCompactControllerLogHookForTest(nil)
	})

	node := env.nodes[1]
	require.NoError(t, node.service.Propose(context.Background(), nodeJoinCommand(2)))
	require.NoError(t, node.service.Propose(context.Background(), nodeJoinCommand(3)))
	require.Eventually(t, func() bool {
		return hookCalls.Load() > 0
	}, 2*time.Second, 10*time.Millisecond)

	setCompactControllerLogHookForTest(nil)
	require.NoError(t, node.service.Propose(context.Background(), nodeJoinCommand(4)))

	waitForControllerSnapshotIndex(t, node.logDB.ForController(), 4)
}

func TestServiceStatusRecordsCompactionFailureAndClearsOnLaterSuccess(t *testing.T) {
	env := newTestEnv(t, []uint64{1})
	defer env.stopAll()

	env.startNodeWithConfig(t, 1, nil, func(cfg *Config) {
		cfg.LogCompaction = LogCompactionConfig{
			Enabled:        true,
			EnabledSet:     true,
			TriggerEntries: 1,
			CheckInterval:  time.Nanosecond,
		}
	})
	env.waitForLeader(t, []uint64{1})

	sentinel := errors.New("compact once")
	setCompactControllerLogHookForTest(func() error { return sentinel })
	t.Cleanup(func() { setCompactControllerLogHookForTest(nil) })

	node := env.nodes[1]
	require.NoError(t, node.service.Propose(context.Background(), nodeJoinCommand(2)))
	require.Eventually(t, func() bool {
		st := node.service.Status()
		return st.Compaction.Degraded &&
			st.Compaction.LastError == sentinel.Error() &&
			!st.Compaction.LastErrorAt.IsZero()
	}, 2*time.Second, 10*time.Millisecond)

	setCompactControllerLogHookForTest(nil)
	require.NoError(t, node.service.Propose(context.Background(), nodeJoinCommand(3)))
	require.Eventually(t, func() bool {
		st := node.service.Status()
		return !st.Compaction.Degraded &&
			st.Compaction.LastError == "" &&
			st.Compaction.LastSnapshotIndex >= 3 &&
			!st.Compaction.LastSnapshotAt.IsZero()
	}, 2*time.Second, 10*time.Millisecond)
}

func TestServiceManualCompactionForcesSnapshotBelowAutomaticThreshold(t *testing.T) {
	env := newTestEnv(t, []uint64{1})
	defer env.stopAll()

	env.startNodeWithConfig(t, 1, nil, func(cfg *Config) {
		cfg.LogCompaction = LogCompactionConfig{
			Enabled:        true,
			EnabledSet:     true,
			TriggerEntries: 1000,
			CheckInterval:  time.Hour,
		}
	})
	env.waitForLeader(t, []uint64{1})

	node := env.nodes[1]
	store := node.logDB.ForController()
	require.NoError(t, node.service.Propose(context.Background(), nodeJoinCommand(2)))
	require.Eventually(t, func() bool {
		state, err := store.InitialState(context.Background())
		return err == nil && state.AppliedIndex >= 2
	}, 2*time.Second, 10*time.Millisecond)

	firstBefore, err := store.FirstIndex(context.Background())
	require.NoError(t, err)
	require.Equal(t, uint64(1), firstBefore)

	result, err := node.service.CompactLog(context.Background())
	require.NoError(t, err)
	require.Equal(t, uint64(1), result.NodeID)
	require.True(t, result.Compacted)
	require.Empty(t, result.SkippedReason)
	require.Zero(t, result.BeforeSnapshotIndex)
	require.GreaterOrEqual(t, result.AppliedIndex, uint64(2))
	require.Equal(t, result.AppliedIndex, result.AfterSnapshotIndex)

	snap := waitForControllerSnapshotIndex(t, store, result.AppliedIndex)
	require.Equal(t, result.AfterSnapshotIndex, snap.Metadata.Index)
	firstAfter, err := store.FirstIndex(context.Background())
	require.NoError(t, err)
	require.Equal(t, result.AfterSnapshotIndex+1, firstAfter)
}

func TestServiceManualCompactionSkipsWhenDisabled(t *testing.T) {
	env := newTestEnv(t, []uint64{1})
	defer env.stopAll()

	env.startNodeWithConfig(t, 1, nil, func(cfg *Config) {
		cfg.LogCompaction = LogCompactionConfig{
			Enabled:        false,
			EnabledSet:     true,
			TriggerEntries: 1000,
			CheckInterval:  time.Hour,
		}
	})
	env.waitForLeader(t, []uint64{1})

	result, err := env.nodes[1].service.CompactLog(context.Background())
	require.NoError(t, err)
	require.Equal(t, uint64(1), result.NodeID)
	require.False(t, result.Compacted)
	require.Equal(t, LogCompactionSkippedDisabled, result.SkippedReason)
}

func TestServiceLaggingFollowerRestoresControllerSnapshot(t *testing.T) {
	env := newTestEnv(t, []uint64{1, 2, 3})
	defer env.stopAll()

	compactionCfg := func(cfg *Config) {
		cfg.LogCompaction = LogCompactionConfig{
			Enabled:        true,
			EnabledSet:     true,
			TriggerEntries: 1,
			CheckInterval:  time.Nanosecond,
		}
	}
	env.startNodeWithConfig(t, 1, nil, compactionCfg)
	env.startNodeWithConfig(t, 2, nil, compactionCfg)
	env.startNodeWithConfig(t, 3, nil, compactionCfg)
	leaderID := env.waitForLeader(t, []uint64{1, 2, 3})
	laggingID := firstNonLeader([]uint64{1, 2, 3}, leaderID)
	laggingStore := env.nodes[laggingID].logDB.ForController()
	laggingLastBeforeStop, err := laggingStore.LastIndex(context.Background())
	require.NoError(t, err)
	laggingSnapBeforeStop, err := laggingStore.Snapshot(context.Background())
	require.NoError(t, err)

	env.stopNode(laggingID)
	activeIDs := removeNodeID([]uint64{1, 2, 3}, laggingID)
	require.Equal(t, leaderID, env.waitForLeader(t, activeIDs))

	leader := env.nodes[leaderID]
	for nodeID := uint64(10); nodeID < 20; nodeID++ {
		require.NoError(t, leader.service.Propose(context.Background(), nodeJoinCommand(nodeID)))
	}
	leaderStore := leader.logDB.ForController()
	var leaderSnap raftpb.Snapshot
	require.Eventually(t, func() bool {
		gotSnap, err := leaderStore.Snapshot(context.Background())
		if err != nil {
			return false
		}
		first, err := leaderStore.FirstIndex(context.Background())
		if err != nil {
			return false
		}
		if gotSnap.Metadata.Index <= laggingLastBeforeStop {
			return false
		}
		if first <= laggingLastBeforeStop+1 {
			return false
		}
		leaderSnap = gotSnap
		return true
	}, 5*time.Second, 10*time.Millisecond)

	env.startNodeWithConfig(t, laggingID, nil, func(cfg *Config) {
		cfg.LogCompaction = LogCompactionConfig{
			Enabled:        false,
			EnabledSet:     true,
			TriggerEntries: 1,
			CheckInterval:  time.Nanosecond,
		}
	})
	for nodeID := uint64(10); nodeID < 20; nodeID++ {
		waitForControllerNode(t, env.nodes[laggingID].meta, nodeID)
	}
	followerSnap := waitForControllerSnapshotIndex(t, env.nodes[laggingID].logDB.ForController(), leaderSnap.Metadata.Index)
	require.Greater(t, followerSnap.Metadata.Index, laggingSnapBeforeStop.Metadata.Index)
	require.GreaterOrEqual(t, followerSnap.Metadata.Index, leaderSnap.Metadata.Index)
}

func TestServiceLeaderStatusIncludesPeerProgress(t *testing.T) {
	env := newTestEnv(t, []uint64{1, 2, 3})
	defer env.stopAll()

	env.startNode(t, 1, nil)
	env.startNode(t, 2, nil)
	env.startNode(t, 3, nil)
	leaderID := env.waitForLeader(t, []uint64{1, 2, 3})

	leader := env.nodes[leaderID]
	require.Eventually(t, func() bool {
		st := leader.service.Status()
		if st.Role != RoleLeader || st.LeaderID != leaderID || len(st.Peers) != 2 {
			return false
		}
		seen := map[uint64]bool{}
		for _, peer := range st.Peers {
			if peer.NodeID == leaderID || peer.NodeID == 0 || peer.Next == 0 || peer.State == "" {
				return false
			}
			seen[peer.NodeID] = true
		}
		return seen[firstNonLeader([]uint64{1, 2, 3}, leaderID)]
	}, 2*time.Second, 10*time.Millisecond)
}

func TestControllerRaftServicePublishesCommittedCommandHook(t *testing.T) {
	env := newTestEnv(t, []uint64{1})
	defer env.stopAll()

	var (
		mu        sync.Mutex
		committed []slotcontroller.Command
	)

	env.startNodeWithConfig(t, 1, nil, func(cfg *Config) {
		cfg.OnCommittedCommand = func(cmd slotcontroller.Command) {
			mu.Lock()
			committed = append(committed, cmd)
			mu.Unlock()
		}
	})
	env.waitForLeader(t, []uint64{1})

	require.NoError(t, env.nodes[1].service.Propose(context.Background(), slotcontroller.Command{
		Kind: slotcontroller.CommandKindOperatorRequest,
		Op: &slotcontroller.OperatorRequest{
			Kind:   slotcontroller.OperatorMarkNodeDraining,
			NodeID: 2,
		},
	}))

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(committed) == 1
	}, 2*time.Second, 20*time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, slotcontroller.CommandKindOperatorRequest, committed[0].Kind)
	require.NotNil(t, committed[0].Op)
	require.Equal(t, uint64(2), committed[0].Op.NodeID)
}

func TestControllerRaftServicePublishesLeaderChangeHook(t *testing.T) {
	env := newTestEnv(t, []uint64{1})
	defer env.stopAll()

	type leaderChange struct {
		from uint64
		to   uint64
	}

	var (
		mu      sync.Mutex
		changes []leaderChange
	)

	env.startNodeWithConfig(t, 1, nil, func(cfg *Config) {
		cfg.OnLeaderChange = func(from, to uint64) {
			mu.Lock()
			changes = append(changes, leaderChange{from: from, to: to})
			mu.Unlock()
		}
	})
	env.waitForLeader(t, []uint64{1})

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(changes) >= 1
	}, 2*time.Second, 20*time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, leaderChange{from: 0, to: 1}, changes[len(changes)-1])
}
