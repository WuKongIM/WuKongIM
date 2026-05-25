package proxy

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"testing"

	raftcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	metafsm "github.com/WuKongIM/WuKongIM/pkg/slot/fsm"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/WuKongIM/WuKongIM/pkg/transport"
	"github.com/stretchr/testify/require"
)

func TestChannelMigrationRPCServiceIDDoesNotCollideWithSharedRPCServices(t *testing.T) {
	occupied := map[uint8]string{
		3:  "slot-runtime-meta",
		4:  "slot-identity",
		5:  "node-presence",
		6:  "node-delivery-submit",
		7:  "node-delivery-push",
		8:  "node-delivery-ack",
		9:  "node-delivery-offline",
		10: "slot-subscriber",
		11: "slot-user-conversation-state",
		12: "slot-channel",
		13: "node-conversation-facts",
		30: "channel-fetch",
		33: "node-channel-append",
		34: "channel-reconcile-probe",
		35: "channel-long-poll-fetch",
		36: "node-channel-messages",
		37: "node-channel-leader-repair",
		38: "node-channel-leader-evaluate",
		39: "node-runtime-summary",
		40: "node-connections",
		41: "node-connection",
		42: "node-diagnostics",
		43: "node-channel-retention",
		44: "node-delivery-tag",
		45: "node-system-uid-cache",
		46: "node-channel-leader-transfer",
		48: "channel-fence-and-drain",
		49: "slot-cmd-conversation-state",
		50: "node-cmd-sync",
	}
	if name, exists := occupied[channelMigrationRPCServiceID]; exists {
		t.Fatalf("channelMigrationRPCServiceID = %d collides with %s", channelMigrationRPCServiceID, name)
	}
}

func TestChannelMigrationCreateTaskRoutesToAuthoritativeSlotLeader(t *testing.T) {
	ctx := context.Background()
	nodes := startTwoNodeHashSlotStores(t, 8)

	channelID := findChannelIDForSlotWithDifferentHashSlot(t, nodes[0].cluster, 2, 2, "migration-create")
	hashSlot := mustHashSlotForKey(t, nodes[0].cluster, channelID)
	task := proxyTestChannelMigrationTask("task-create", channelID)

	require.NoError(t, nodes[0].store.CreateChannelMigrationTask(ctx, task))

	got, ok, err := nodes[1].db.ForHashSlot(hashSlot).GetActiveChannelMigrationTask(ctx, channelID, task.ChannelType)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, task, got)

	_, ok, err = nodes[0].db.ForHashSlot(hashSlot).GetActiveChannelMigrationTask(ctx, channelID, task.ChannelType)
	require.NoError(t, err)
	require.False(t, ok)
}

func TestChannelMigrationCreateTaskWithRuntimeGuardRoutesToAuthoritativeSlotLeader(t *testing.T) {
	ctx := context.Background()
	nodes := startTwoNodeHashSlotStores(t, 8)

	channelID := findChannelIDForSlotWithDifferentHashSlot(t, nodes[0].cluster, 2, 2, "migration-create-guard")
	hashSlot := mustHashSlotForKey(t, nodes[0].cluster, channelID)
	task := proxyTestChannelMigrationTask("task-create-guard", channelID)
	meta := proxyTestRuntimeMeta(channelID, task.ChannelType)
	require.NoError(t, nodes[1].db.ForHashSlot(hashSlot).UpsertChannelRuntimeMeta(ctx, meta))

	require.NoError(t, nodes[0].store.CreateChannelMigrationTaskWithRuntimeGuard(ctx, metadb.ChannelMigrationTaskCreate{
		Task:         task,
		RuntimeGuard: proxyTestRuntimeGuard(meta),
	}))

	got, ok, err := nodes[1].db.ForHashSlot(hashSlot).GetActiveChannelMigrationTask(ctx, channelID, task.ChannelType)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, task, got)
}

func TestChannelMigrationCreateTaskWithRuntimeGuardRejectsStaleRemoteMeta(t *testing.T) {
	ctx := context.Background()
	nodes := startTwoNodeHashSlotStores(t, 8)

	channelID := findChannelIDForSlotWithDifferentHashSlot(t, nodes[0].cluster, 2, 2, "migration-create-guard-stale")
	hashSlot := mustHashSlotForKey(t, nodes[0].cluster, channelID)
	task := proxyTestChannelMigrationTask("task-create-guard-stale", channelID)
	meta := proxyTestRuntimeMeta(channelID, task.ChannelType)
	changed := meta
	changed.LeaderEpoch++
	require.NoError(t, nodes[1].db.ForHashSlot(hashSlot).UpsertChannelRuntimeMeta(ctx, changed))

	err := nodes[0].store.CreateChannelMigrationTaskWithRuntimeGuard(ctx, metadb.ChannelMigrationTaskCreate{
		Task:         task,
		RuntimeGuard: proxyTestRuntimeGuard(meta),
	})
	require.ErrorIs(t, err, metadb.ErrStaleMeta)
}

func TestChannelMigrationGetActiveTaskReadsLocalAndRemoteAuthoritativeSlot(t *testing.T) {
	ctx := context.Background()
	nodes := startTwoNodeShardedStores(t)

	localChannelID := findChannelIDForSlot(t, nodes[0].cluster, 1, "migration-get-local")
	localTask := proxyTestChannelMigrationTask("task-get-local", localChannelID)
	require.NoError(t, nodes[0].db.ForHashSlot(mustHashSlotForKey(t, nodes[0].cluster, localChannelID)).CreateChannelMigrationTask(ctx, localTask))

	got, ok, err := nodes[0].store.GetActiveChannelMigrationTask(ctx, localChannelID, localTask.ChannelType)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, localTask, got)

	remoteChannelID := findChannelIDForSlot(t, nodes[0].cluster, 2, "migration-get-remote")
	remoteTask := proxyTestChannelMigrationTask("task-get-remote", remoteChannelID)
	require.NoError(t, nodes[1].db.ForHashSlot(mustHashSlotForKey(t, nodes[1].cluster, remoteChannelID)).CreateChannelMigrationTask(ctx, remoteTask))

	got, ok, err = nodes[0].store.GetActiveChannelMigrationTask(ctx, remoteChannelID, remoteTask.ChannelType)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, remoteTask, got)
}

func TestChannelMigrationListActiveTasksForNodeRoutesToAuthoritativeSlots(t *testing.T) {
	ctx := context.Background()
	localDB := openTestDB(t)
	remoteDB := openTestDB(t)
	leaders := map[multiraft.SlotID]multiraft.NodeID{1: 1, 2: 2}
	hashSlots := map[multiraft.SlotID][]uint16{1: {1}, 2: {2}}
	remoteCluster := &proxyTestMigrationCluster{
		nodeID:      2,
		localNodeID: 2,
		slotIDs:     []multiraft.SlotID{1, 2},
		hashSlots:   hashSlots,
		leaders:     leaders,
		peers:       map[multiraft.SlotID][]multiraft.NodeID{1: {1}, 2: {2}},
	}
	remoteStore := &Store{cluster: remoteCluster, db: remoteDB}
	localCluster := &proxyTestMigrationCluster{
		nodeID:      1,
		localNodeID: 1,
		slotIDs:     []multiraft.SlotID{1, 2},
		hashSlots:   hashSlots,
		leaders:     leaders,
		peers:       map[multiraft.SlotID][]multiraft.NodeID{1: {1}, 2: {2}},
		rpcService: func(ctx context.Context, nodeID multiraft.NodeID, slotID multiraft.SlotID, serviceID uint8, payload []byte) ([]byte, error) {
			require.Equal(t, multiraft.NodeID(2), nodeID)
			require.Equal(t, multiraft.SlotID(2), slotID)
			require.Equal(t, channelMigrationRPCServiceID, serviceID)
			return remoteStore.handleChannelMigrationRPC(ctx, payload)
		},
	}
	localStore := &Store{cluster: localCluster, db: localDB}
	task := proxyTestChannelMigrationTask("task-node", "channel-node")
	task.SourceNode = 3
	task.TargetNode = 4

	require.NoError(t, remoteDB.ForHashSlot(2).CreateChannelMigrationTask(ctx, task))

	got, hasMore, err := localStore.ListActiveChannelMigrationTasksForNode(ctx, 3, 10)
	require.NoError(t, err)
	require.False(t, hasMore)
	require.Len(t, got, 1)
	require.Equal(t, task.TaskID, got[0].TaskID)
}

func TestChannelMigrationListActiveTasksForNodeFiltersTerminalAndUnrelated(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	hashSlot := uint16(1)
	store := &Store{
		cluster: &proxyTestMigrationCluster{
			nodeID:      1,
			localNodeID: 1,
			slotIDs:     []multiraft.SlotID{1},
			hashSlots:   map[multiraft.SlotID][]uint16{1: {hashSlot}},
			leaders:     map[multiraft.SlotID]multiraft.NodeID{1: 1},
		},
		db: db,
	}

	matchSource := proxyTestChannelMigrationTask("task-match-source", "channel-match-source")
	matchSource.SourceNode = 3
	matchSource.TargetNode = 4
	require.NoError(t, db.ForHashSlot(hashSlot).CreateChannelMigrationTask(ctx, matchSource))

	matchTarget := proxyTestChannelMigrationTask("task-match-target", "channel-match-target")
	matchTarget.SourceNode = 5
	matchTarget.TargetNode = 3
	require.NoError(t, db.ForHashSlot(hashSlot).CreateChannelMigrationTask(ctx, matchTarget))

	terminal := proxyTestCompletedChannelMigrationTask("task-terminal", "channel-terminal", 1750000005000)
	terminal.SourceNode = 3
	terminal.TargetNode = 4
	require.NoError(t, db.ForHashSlot(hashSlot).CreateChannelMigrationTask(ctx, terminal))

	unrelated := proxyTestChannelMigrationTask("task-unrelated", "channel-unrelated")
	unrelated.SourceNode = 6
	unrelated.TargetNode = 7
	unrelated.OwnerNodeID = 3
	unrelated.OwnerLeaseUntilMS = 1750000010000
	require.NoError(t, db.ForHashSlot(hashSlot).CreateChannelMigrationTask(ctx, unrelated))

	got, hasMore, err := store.ListActiveChannelMigrationTasksForNode(ctx, 3, 10)
	require.NoError(t, err)
	require.False(t, hasMore)
	require.ElementsMatch(t, []string{matchSource.TaskID, matchTarget.TaskID}, channelMigrationTaskIDs(got))
}

func TestChannelMigrationListActiveTasksForNodeReportsHasMore(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	hashSlot := uint16(1)
	store := &Store{
		cluster: &proxyTestMigrationCluster{
			nodeID:      1,
			localNodeID: 1,
			slotIDs:     []multiraft.SlotID{1},
			hashSlots:   map[multiraft.SlotID][]uint16{1: {hashSlot}},
			leaders:     map[multiraft.SlotID]multiraft.NodeID{1: 1},
		},
		db: db,
	}

	first := proxyTestChannelMigrationTask("task-has-more-1", "channel-has-more-1")
	first.SourceNode = 3
	first.TargetNode = 4
	require.NoError(t, db.ForHashSlot(hashSlot).CreateChannelMigrationTask(ctx, first))

	second := proxyTestChannelMigrationTask("task-has-more-2", "channel-has-more-2")
	second.SourceNode = 3
	second.TargetNode = 5
	require.NoError(t, db.ForHashSlot(hashSlot).CreateChannelMigrationTask(ctx, second))

	got, hasMore, err := store.ListActiveChannelMigrationTasksForNode(ctx, 3, 1)
	require.NoError(t, err)
	require.True(t, hasMore)
	require.Len(t, got, 1)
}

func TestChannelMigrationListActiveTasksForNodeReturnsNoLeaderBeforeLocalScan(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	hashSlot := uint16(1)
	cluster := &proxyTestMigrationCluster{
		nodeID:      1,
		localNodeID: 1,
		slotIDs:     []multiraft.SlotID{1},
		hashSlots:   map[multiraft.SlotID][]uint16{1: {hashSlot}},
		leaders:     map[multiraft.SlotID]multiraft.NodeID{},
		peers:       map[multiraft.SlotID][]multiraft.NodeID{1: {1}},
	}
	store := &Store{cluster: cluster, db: db}
	task := proxyTestChannelMigrationTask("task-no-leader", "channel-no-leader")
	task.SourceNode = 3
	task.TargetNode = 4
	require.NoError(t, db.ForHashSlot(hashSlot).CreateChannelMigrationTask(ctx, task))

	got, hasMore, err := store.ListActiveChannelMigrationTasksForNode(ctx, 3, 10)

	require.ErrorIs(t, err, raftcluster.ErrNoLeader)
	require.Nil(t, got)
	require.False(t, hasMore)
}

func TestChannelMigrationListActiveTasksForNodeScansLocalHashSlotsInOrder(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	store := &Store{
		cluster: &proxyTestMigrationCluster{
			nodeID:      1,
			localNodeID: 1,
			slotIDs:     []multiraft.SlotID{1},
			hashSlots:   map[multiraft.SlotID][]uint16{1: {5, 1}},
			leaders:     map[multiraft.SlotID]multiraft.NodeID{1: 1},
		},
		db: db,
	}
	high := proxyTestChannelMigrationTask("task-high-hash-slot", "channel-high-hash-slot")
	high.SourceNode = 3
	high.TargetNode = 4
	low := proxyTestChannelMigrationTask("task-low-hash-slot", "channel-low-hash-slot")
	low.SourceNode = 3
	low.TargetNode = 5
	require.NoError(t, db.ForHashSlot(5).CreateChannelMigrationTask(ctx, high))
	require.NoError(t, db.ForHashSlot(1).CreateChannelMigrationTask(ctx, low))

	got, hasMore, err := store.ListActiveChannelMigrationTasksForNode(ctx, 3, 1)

	require.NoError(t, err)
	require.True(t, hasMore)
	require.Len(t, got, 1)
	require.Equal(t, low.TaskID, got[0].TaskID)
}

func TestChannelMigrationListActiveTasksForNodeClampsHugeLimit(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	hashSlot := uint16(1)
	store := &Store{
		cluster: &proxyTestMigrationCluster{
			nodeID:      1,
			localNodeID: 1,
			slotIDs:     []multiraft.SlotID{1},
			hashSlots:   map[multiraft.SlotID][]uint16{1: {hashSlot}},
			leaders:     map[multiraft.SlotID]multiraft.NodeID{1: 1},
		},
		db: db,
	}
	for i := 0; i < 2; i++ {
		task := proxyTestChannelMigrationTask(fmt.Sprintf("task-huge-limit-%d", i), fmt.Sprintf("channel-huge-limit-%d", i))
		task.SourceNode = 3
		task.TargetNode = 4
		require.NoError(t, db.ForHashSlot(hashSlot).CreateChannelMigrationTask(ctx, task))
	}

	var got []metadb.ChannelMigrationTask
	var hasMore bool
	var err error
	require.NotPanics(t, func() {
		got, hasMore, err = store.ListActiveChannelMigrationTasksForNode(ctx, 3, int(^uint(0)>>1))
	})
	require.NoError(t, err)
	require.False(t, hasMore)
	require.Len(t, got, 2)
}

func TestChannelMigrationListActiveTasksForNodeRPCClampsHugeLimitAndReportsHasMore(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	hashSlot := uint16(1)
	store := &Store{
		cluster: &proxyTestMigrationCluster{
			nodeID:      1,
			localNodeID: 1,
			slotIDs:     []multiraft.SlotID{1},
			hashSlots:   map[multiraft.SlotID][]uint16{1: {hashSlot}},
			leaders:     map[multiraft.SlotID]multiraft.NodeID{1: 1},
		},
		db: db,
	}
	const wantLimit = 1024
	for i := 0; i < wantLimit+1; i++ {
		task := proxyTestChannelMigrationTask(fmt.Sprintf("task-rpc-huge-limit-%04d", i), fmt.Sprintf("channel-rpc-huge-limit-%04d", i))
		task.SourceNode = 3
		task.TargetNode = 4
		require.NoError(t, db.ForHashSlot(hashSlot).CreateChannelMigrationTask(ctx, task))
	}
	body, err := encodeChannelMigrationRPCRequestBinary(channelMigrationRPCRequest{
		Op:     channelMigrationRPCListActiveForNode,
		SlotID: 1,
		NodeID: 3,
		Limit:  int(^uint(0) >> 1),
	})
	require.NoError(t, err)

	var resp channelMigrationRPCResponse
	require.NotPanics(t, func() {
		respBody, err := store.handleChannelMigrationRPC(ctx, body)
		require.NoError(t, err)
		resp, err = decodeChannelMigrationRPCResponse(respBody)
		require.NoError(t, err)
	})
	require.Equal(t, rpcStatusOK, resp.Status)
	require.Len(t, resp.Tasks, wantLimit)
	require.True(t, resp.HasMore)
}

func TestChannelMigrationClaimUsesLocalSlotLeaderAsOwner(t *testing.T) {
	ctx := context.Background()
	nodes := startTwoNodeShardedStores(t)
	proxyWaitForClusterLeader(t, nodes[1].cluster, 2, 2)

	channelID := findChannelIDForSlot(t, nodes[1].cluster, 2, "migration-claim-owner")
	hashSlot := mustHashSlotForKey(t, nodes[1].cluster, channelID)
	task := proxyTestChannelMigrationTask("task-claim-owner", channelID)
	require.NoError(t, nodes[1].db.ForHashSlot(hashSlot).CreateChannelMigrationTask(ctx, task))

	req := proxyTestChannelMigrationClaim(task, 777, 1750000005000, 1750000001000)
	require.NoError(t, nodes[1].store.ClaimChannelMigrationTask(ctx, req))

	got, err := nodes[1].db.ForHashSlot(hashSlot).GetChannelMigrationTask(ctx, task.ChannelID, task.ChannelType, task.TaskID)
	require.NoError(t, err)
	require.Equal(t, uint64(nodes[1].nodeID), got.OwnerNodeID)
	require.Equal(t, req.OwnerLeaseUntilMS, got.OwnerLeaseUntilMS)
	require.Equal(t, metadb.ChannelMigrationStatusRunning, got.Status)
}

func TestChannelMigrationClaimRejectsNonLocalSlotLeader(t *testing.T) {
	ctx := context.Background()
	nodes := startTwoNodeShardedStores(t)

	channelID := findChannelIDForSlot(t, nodes[0].cluster, 2, "migration-claim-not-leader")
	hashSlot := mustHashSlotForKey(t, nodes[1].cluster, channelID)
	task := proxyTestChannelMigrationTask("task-claim-not-leader", channelID)
	require.NoError(t, nodes[1].db.ForHashSlot(hashSlot).CreateChannelMigrationTask(ctx, task))

	err := nodes[0].store.ClaimChannelMigrationTask(ctx, proxyTestChannelMigrationClaim(task, 1, 1750000005000, 1750000001000))
	require.ErrorIs(t, err, raftcluster.ErrNotLeader)

	got, err := nodes[1].db.ForHashSlot(hashSlot).GetChannelMigrationTask(ctx, task.ChannelID, task.ChannelType, task.TaskID)
	require.NoError(t, err)
	require.Zero(t, got.OwnerNodeID)
	require.Zero(t, got.OwnerLeaseUntilMS)
}

func TestChannelMigrationAdvancePersistsThroughAuthoritativeSlot(t *testing.T) {
	ctx := context.Background()
	nodes := startTwoNodeShardedStores(t)

	channelID := findChannelIDForSlot(t, nodes[0].cluster, 2, "migration-advance")
	hashSlot := mustHashSlotForKey(t, nodes[1].cluster, channelID)
	task := proxyTestChannelMigrationTask("task-advance", channelID)
	task.Status = metadb.ChannelMigrationStatusRunning
	task.OwnerNodeID = uint64(nodes[1].nodeID)
	task.OwnerLeaseUntilMS = 1750000005000
	require.NoError(t, nodes[1].db.ForHashSlot(hashSlot).CreateChannelMigrationTask(ctx, task))

	next := task
	next.Status = metadb.ChannelMigrationStatusBlocked
	next.Phase = metadb.ChannelMigrationPhaseWarmCatchUp
	next.Attempt = 2
	next.NextRunAtMS = 1750000009000
	next.BlockerCode = metadb.ChannelMigrationBlockerNeedsSnapshotBootstrap
	next.BlockerMessage = "snapshot bootstrap required"
	next.LastError = "target lagging"
	next.UpdatedAtMS = 1750000002000
	next.Progress = metadb.ChannelMigrationProgress{
		LeaderLEO:          100,
		LeaderHW:           98,
		TargetLEO:          91,
		TargetCheckpointHW: 90,
		LagRecords:         9,
		StableSinceMS:      1750000003000,
	}

	require.NoError(t, nodes[0].store.AdvanceChannelMigrationTask(ctx, proxyTestChannelMigrationAdvance(task, next)))

	got, err := nodes[1].db.ForHashSlot(hashSlot).GetChannelMigrationTask(ctx, task.ChannelID, task.ChannelType, task.TaskID)
	require.NoError(t, err)
	require.Equal(t, next.Phase, got.Phase)
	require.Equal(t, next.Attempt, got.Attempt)
	require.Equal(t, next.NextRunAtMS, got.NextRunAtMS)
	require.Equal(t, next.BlockerCode, got.BlockerCode)
	require.Equal(t, next.BlockerMessage, got.BlockerMessage)
	require.Equal(t, next.LastError, got.LastError)
	require.Equal(t, next.Progress, got.Progress)
}

func TestChannelMigrationResetExpiredFenceRoutesThroughSlotRaft(t *testing.T) {
	ctx := context.Background()
	nodes := startTwoNodeShardedStores(t)

	channelID := findChannelIDForSlot(t, nodes[0].cluster, 2, "migration-reset")
	hashSlot := mustHashSlotForKey(t, nodes[1].cluster, channelID)
	task := proxyTestChannelMigrationTask("task-reset", channelID)
	task.Status = metadb.ChannelMigrationStatusRunning
	task.Phase = metadb.ChannelMigrationPhaseCutoverFence
	task.UpdatedAtMS = 1750000001000
	task.FenceToken = task.TaskID
	task.FenceVersion = 7
	task.FenceUntilMS = 1750000002000
	meta := proxyTestFencedRuntimeMeta(channelID, task.ChannelType, task.TaskID, 7)
	meta.WriteFenceUntilMS = task.FenceUntilMS
	require.NoError(t, nodes[1].db.ForHashSlot(hashSlot).UpsertChannelRuntimeMeta(ctx, meta))
	require.NoError(t, nodes[1].db.ForHashSlot(hashSlot).CreateChannelMigrationTask(ctx, task))

	req := metadb.ChannelMigrationResetFenceRequest{
		Guard:        proxyTestTaskGuard(task),
		RuntimeGuard: proxyTestRuntimeGuard(meta),
		Status:       metadb.ChannelMigrationStatusRunning,
		Phase:        metadb.ChannelMigrationPhaseWarmCatchUp,
		NowMS:        meta.WriteFenceUntilMS + 1,
		UpdatedAtMS:  1750000003000,
	}
	require.NoError(t, nodes[0].store.ResetChannelWriteFenceToPreCutover(ctx, req))

	gotTask, err := nodes[1].db.ForHashSlot(hashSlot).GetChannelMigrationTask(ctx, task.ChannelID, task.ChannelType, task.TaskID)
	require.NoError(t, err)
	require.Equal(t, metadb.ChannelMigrationPhaseWarmCatchUp, gotTask.Phase)
	require.Empty(t, gotTask.FenceToken)
	require.Zero(t, gotTask.FenceVersion)
	require.Zero(t, gotTask.DrainedFenceVersion)

	gotMeta, err := nodes[1].db.ForHashSlot(hashSlot).GetChannelRuntimeMeta(ctx, channelID, task.ChannelType)
	require.NoError(t, err)
	require.Empty(t, gotMeta.WriteFenceToken)
	require.Equal(t, uint64(8), gotMeta.WriteFenceVersion)
	require.Zero(t, gotMeta.WriteFenceUntilMS)
}

func TestChannelMigrationPromoteRejectsStaleMetaFromRemotePath(t *testing.T) {
	ctx := context.Background()
	nodes := startTwoNodeShardedStores(t)

	channelID := findChannelIDForSlot(t, nodes[0].cluster, 2, "migration-promote-stale")
	hashSlot := mustHashSlotForKey(t, nodes[1].cluster, channelID)
	task := proxyTestChannelMigrationTask("task-promote-stale", channelID)
	task.Status = metadb.ChannelMigrationStatusRunning
	task.Phase = metadb.ChannelMigrationPhasePromoteAndRemove
	task.UpdatedAtMS = 1750000001000
	task.FenceToken = task.TaskID
	task.FenceVersion = 7
	task.FenceUntilMS = 1750000010000
	proxyTestSetDrainProof(&task, 7)
	meta := proxyTestFencedRuntimeMeta(channelID, task.ChannelType, task.TaskID, 7)
	meta.Replicas = []uint64{1, 2, 3}
	meta.ISR = []uint64{1, 2}
	require.NoError(t, nodes[1].db.ForHashSlot(hashSlot).UpsertChannelRuntimeMeta(ctx, meta))
	require.NoError(t, nodes[1].db.ForHashSlot(hashSlot).CreateChannelMigrationTask(ctx, task))

	req := metadb.ChannelMigrationPromoteLearnerRequest{
		Guard:        proxyTestTaskGuard(task),
		RuntimeGuard: proxyTestRuntimeGuard(meta),
		Status:       metadb.ChannelMigrationStatusRunning,
		Phase:        metadb.ChannelMigrationPhaseVerifyMembership,
		SourceNode:   task.SourceNode,
		TargetNode:   task.TargetNode,
		NowMS:        meta.WriteFenceUntilMS - 1,
		UpdatedAtMS:  1750000003000,
	}
	req.RuntimeGuard.ExpectedChannelEpoch++

	err := nodes[0].store.PromoteLearnerAndRemoveReplica(ctx, req)
	require.True(t, errors.Is(err, metadb.ErrStaleMeta), "err = %v", err)

	got, err := nodes[1].db.ForHashSlot(hashSlot).GetChannelMigrationTask(ctx, task.ChannelID, task.ChannelType, task.TaskID)
	require.NoError(t, err)
	require.Equal(t, metadb.ChannelMigrationPhasePromoteAndRemove, got.Phase)
}

func TestChannelMigrationListRunnableTasksForLocalLeaderSlots(t *testing.T) {
	ctx := context.Background()
	nodes := startTwoNodeShardedStores(t)
	nowMS := int64(1750000010000)

	runnableChannelID := findChannelIDForSlot(t, nodes[0].cluster, 1, "migration-runnable")
	runnable := proxyTestChannelMigrationTask("task-runnable", runnableChannelID)
	require.NoError(t, nodes[0].db.ForHashSlot(mustHashSlotForKey(t, nodes[0].cluster, runnableChannelID)).CreateChannelMigrationTask(ctx, runnable))

	futureChannelID := findChannelIDForSlot(t, nodes[0].cluster, 1, "migration-future")
	future := proxyTestChannelMigrationTask("task-future", futureChannelID)
	future.NextRunAtMS = nowMS + 1
	require.NoError(t, nodes[0].db.ForHashSlot(mustHashSlotForKey(t, nodes[0].cluster, futureChannelID)).CreateChannelMigrationTask(ctx, future))

	ownedChannelID := findChannelIDForSlot(t, nodes[0].cluster, 1, "migration-owned")
	owned := proxyTestChannelMigrationTask("task-owned", ownedChannelID)
	owned.OwnerNodeID = uint64(nodes[1].nodeID)
	owned.OwnerLeaseUntilMS = nowMS + 1000
	require.NoError(t, nodes[0].db.ForHashSlot(mustHashSlotForKey(t, nodes[0].cluster, ownedChannelID)).CreateChannelMigrationTask(ctx, owned))

	remoteChannelID := findChannelIDForSlot(t, nodes[0].cluster, 2, "migration-remote-runnable")
	remote := proxyTestChannelMigrationTask("task-remote-runnable", remoteChannelID)
	require.NoError(t, nodes[1].db.ForHashSlot(mustHashSlotForKey(t, nodes[1].cluster, remoteChannelID)).CreateChannelMigrationTask(ctx, remote))

	got, err := nodes[0].store.ListRunnableChannelMigrationTasksForLocalLeaderSlots(ctx, nowMS, 10)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, runnable.TaskID, got[0].TaskID)
}

func TestChannelMigrationGarbageCollectsTerminalTasksForLocalLeaderSlots(t *testing.T) {
	ctx := context.Background()
	nodes := startTwoNodeShardedStores(t)
	beforeMS := int64(1750000020000)

	localChannelID := findChannelIDForSlot(t, nodes[0].cluster, 1, "migration-gc-local")
	local := proxyTestCompletedChannelMigrationTask("task-gc-local", localChannelID, beforeMS-1000)
	localHashSlot := mustHashSlotForKey(t, nodes[0].cluster, localChannelID)
	require.NoError(t, nodes[0].db.ForHashSlot(localHashSlot).CreateChannelMigrationTask(ctx, local))

	remoteChannelID := findChannelIDForSlot(t, nodes[0].cluster, 2, "migration-gc-remote")
	remote := proxyTestCompletedChannelMigrationTask("task-gc-remote", remoteChannelID, beforeMS-1000)
	remoteHashSlot := mustHashSlotForKey(t, nodes[1].cluster, remoteChannelID)
	require.NoError(t, nodes[1].db.ForHashSlot(remoteHashSlot).CreateChannelMigrationTask(ctx, remote))

	deleted, err := nodes[0].store.GarbageCollectTerminalChannelMigrationTasks(ctx, beforeMS, 10)
	require.NoError(t, err)
	require.Equal(t, 1, deleted)

	_, err = nodes[0].db.ForHashSlot(localHashSlot).GetChannelMigrationTask(ctx, local.ChannelID, local.ChannelType, local.TaskID)
	require.ErrorIs(t, err, metadb.ErrNotFound)

	gotRemote, err := nodes[1].db.ForHashSlot(remoteHashSlot).GetChannelMigrationTask(ctx, remote.ChannelID, remote.ChannelType, remote.TaskID)
	require.NoError(t, err)
	require.Equal(t, remote.TaskID, gotRemote.TaskID)
}

func TestChannelMigrationGarbageCollectProposesForReplicatedSlots(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	hashSlot := uint16(7)
	cluster := &proxyTestMigrationCluster{
		nodeID:         1,
		localNodeID:    1,
		slotForKey:     1,
		hashSlotForKey: hashSlot,
		slotIDs:        []multiraft.SlotID{1},
		hashSlots:      map[multiraft.SlotID][]uint16{1: {hashSlot}},
		leaders:        map[multiraft.SlotID]multiraft.NodeID{1: 1},
		peers:          map[multiraft.SlotID][]multiraft.NodeID{1: {1, 2}},
	}
	store := &Store{cluster: cluster, db: db}
	task := proxyTestCompletedChannelMigrationTask("task-gc-replicated", "channel-gc-replicated", 1750000010000)
	require.NoError(t, db.ForHashSlot(hashSlot).CreateChannelMigrationTask(ctx, task))
	sm, err := metafsm.NewStateMachineWithHashSlots(db, 1, []uint16{hashSlot})
	require.NoError(t, err)
	cluster.proposeResult = func(ctx context.Context, slotID multiraft.SlotID, hashSlot uint16, cmd []byte) ([]byte, error) {
		return sm.Apply(ctx, multiraft.Command{SlotID: slotID, HashSlot: hashSlot, Data: cmd})
	}

	deleted, err := store.GarbageCollectTerminalChannelMigrationTasks(ctx, 1750000020000, 10)
	require.NoError(t, err)
	require.Equal(t, 1, deleted)
	require.Equal(t, 1, cluster.proposals)

	_, err = db.ForHashSlot(hashSlot).GetChannelMigrationTask(ctx, task.ChannelID, task.ChannelType, task.TaskID)
	require.ErrorIs(t, err, metadb.ErrNotFound)
}

func TestChannelMigrationProposeRejectsStaleRouteBeforeRaftApply(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	cluster := &proxyTestMigrationCluster{
		nodeID:         2,
		localNodeID:    2,
		slotForKey:     1,
		hashSlotForKey: 1,
		leaders:        map[multiraft.SlotID]multiraft.NodeID{1: 1, 2: 2},
	}
	store := &Store{cluster: cluster, db: db}
	task := proxyTestChannelMigrationTask("task-stale-route", "channel-stale-route")
	body, err := encodeChannelMigrationRPCRequestBinary(channelMigrationRPCRequest{
		Op:        channelMigrationRPCPropose,
		SlotID:    2,
		HashSlot:  2,
		ChannelID: task.ChannelID,
		Command:   []byte{1, 30},
	})
	require.NoError(t, err)

	respBody, err := store.handleChannelMigrationRPC(ctx, body)
	require.NoError(t, err)
	resp, err := decodeChannelMigrationRPCResponse(respBody)
	require.NoError(t, err)
	require.Equal(t, rpcStatusNotLeader, resp.Status)
	require.Equal(t, uint64(1), resp.LeaderID)
	require.Zero(t, cluster.proposals)
}

func TestChannelMigrationGetActiveRejectsStaleRoute(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	cluster := &proxyTestMigrationCluster{
		nodeID:         2,
		localNodeID:    2,
		slotForKey:     1,
		hashSlotForKey: 1,
		leaders:        map[multiraft.SlotID]multiraft.NodeID{1: 1, 2: 2},
	}
	store := &Store{cluster: cluster, db: db}
	body, err := encodeChannelMigrationRPCRequestBinary(channelMigrationRPCRequest{
		Op:          channelMigrationRPCGetActive,
		SlotID:      2,
		ChannelID:   "channel-stale-get-active-route",
		ChannelType: 1,
	})
	require.NoError(t, err)

	respBody, err := store.handleChannelMigrationRPC(ctx, body)
	require.NoError(t, err)
	resp, err := decodeChannelMigrationRPCResponse(respBody)
	require.NoError(t, err)
	require.Equal(t, rpcStatusNotLeader, resp.Status)
	require.Equal(t, uint64(1), resp.LeaderID)
}

func TestChannelMigrationReadDoesNotHideStaleMetaStatus(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	cluster := &proxyTestMigrationCluster{
		slotForKey:     2,
		hashSlotForKey: 1,
		leaders:        map[multiraft.SlotID]multiraft.NodeID{2: 2},
		peers:          map[multiraft.SlotID][]multiraft.NodeID{2: {2}},
		rpcResponse: func() []byte {
			body, err := encodeChannelMigrationRPCResponse(channelMigrationRPCResponse{Status: rpcStatusStaleMeta})
			require.NoError(t, err)
			return body
		}(),
	}
	store := &Store{cluster: cluster, db: db}

	_, ok, err := store.GetActiveChannelMigrationTask(ctx, "channel-stale-read", 1)
	require.ErrorContains(t, err, "unexpected rpc status")
	require.False(t, ok)
}

func TestChannelMigrationRPCCodecRejectsInvalidTaskEnums(t *testing.T) {
	task := proxyTestChannelMigrationTask("task-invalid-enum", "channel-invalid-enum")
	task.Kind = metadb.ChannelMigrationKind(99)
	body, err := encodeChannelMigrationRPCResponse(channelMigrationRPCResponse{
		Status: rpcStatusOK,
		Task:   &task,
	})
	require.NoError(t, err)

	_, err = decodeChannelMigrationRPCResponse(body)
	require.Error(t, err)
}

func TestChannelMigrationRPCCodecDecodesLegacyFrames(t *testing.T) {
	task := proxyTestChannelMigrationTask("task-legacy-codec", "channel-legacy-codec")
	getActive := channelMigrationRPCRequest{
		Op:          channelMigrationRPCGetActive,
		SlotID:      2,
		ChannelID:   task.ChannelID,
		ChannelType: task.ChannelType,
	}
	propose := channelMigrationRPCRequest{
		Op:        channelMigrationRPCPropose,
		SlotID:    2,
		HashSlot:  7,
		ChannelID: task.ChannelID,
		Command:   []byte{1, 30},
	}

	gotGetActive, err := decodeChannelMigrationRPCRequest(encodeLegacyChannelMigrationRPCRequest(t, getActive))
	require.NoError(t, err)
	require.Equal(t, getActive, gotGetActive)
	require.Zero(t, gotGetActive.NodeID)
	require.Zero(t, gotGetActive.Limit)

	gotPropose, err := decodeChannelMigrationRPCRequest(encodeLegacyChannelMigrationRPCRequest(t, propose))
	require.NoError(t, err)
	require.Equal(t, propose, gotPropose)
	require.Zero(t, gotPropose.NodeID)
	require.Zero(t, gotPropose.Limit)

	legacyResp := channelMigrationRPCResponse{
		Status:   rpcStatusOK,
		LeaderID: 2,
		Task:     &task,
	}
	gotResp, err := decodeChannelMigrationRPCResponse(encodeLegacyChannelMigrationRPCResponse(legacyResp))
	require.NoError(t, err)
	require.Equal(t, legacyResp.Status, gotResp.Status)
	require.Equal(t, legacyResp.LeaderID, gotResp.LeaderID)
	require.Equal(t, legacyResp.Task, gotResp.Task)
	require.Nil(t, gotResp.Tasks)
	require.False(t, gotResp.HasMore)
}

func TestChannelMigrationRPCCodecEncodesLegacyFramesForExistingOpsAndEmptyResponses(t *testing.T) {
	getActive := channelMigrationRPCRequest{
		Op:          channelMigrationRPCGetActive,
		SlotID:      2,
		ChannelID:   "channel-legacy-get-active",
		ChannelType: 1,
	}
	propose := channelMigrationRPCRequest{
		Op:        channelMigrationRPCPropose,
		SlotID:    2,
		HashSlot:  7,
		ChannelID: "channel-legacy-propose",
		Command:   []byte{1, 30},
	}

	getActiveBody, err := encodeChannelMigrationRPCRequestBinary(getActive)
	require.NoError(t, err)
	require.Equal(t, encodeLegacyChannelMigrationRPCRequest(t, getActive), getActiveBody)

	proposeBody, err := encodeChannelMigrationRPCRequestBinary(propose)
	require.NoError(t, err)
	require.Equal(t, encodeLegacyChannelMigrationRPCRequest(t, propose), proposeBody)

	resp := channelMigrationRPCResponse{Status: rpcStatusOK, LeaderID: 2}
	respBody, err := encodeChannelMigrationRPCResponse(resp)
	require.NoError(t, err)
	require.Equal(t, encodeLegacyChannelMigrationRPCResponse(resp), respBody)
}

func TestChannelMigrationRPCCodecRejectsMalformedHugeTaskListWithoutLargeAllocation(t *testing.T) {
	body := make([]byte, 0, len(channelMigrationRPCResponseMagic)+32)
	body = append(body, channelMigrationRPCResponseMagic[:]...)
	body = runtimeMetaAppendString(body, rpcStatusOK)
	body = runtimeMetaAppendUvarint(body, 0)
	body = appendChannelMigrationTaskPtr(body, nil)
	body = runtimeMetaAppendUvarint(body, 1<<16)

	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	_, err := decodeChannelMigrationRPCResponse(body)
	var after runtime.MemStats
	runtime.ReadMemStats(&after)

	require.Error(t, err)
	require.Less(t, after.TotalAlloc-before.TotalAlloc, uint64(4<<20))
}

func TestChannelMigrationRPCCodecRejectsTrailingBytesAfterOptionalFields(t *testing.T) {
	task := proxyTestChannelMigrationTask("task-trailing-codec", "channel-trailing-codec")
	reqBody, err := encodeChannelMigrationRPCRequestBinary(channelMigrationRPCRequest{
		Op:     channelMigrationRPCListActiveForNode,
		SlotID: 2,
		NodeID: 3,
		Limit:  10,
	})
	require.NoError(t, err)
	_, err = decodeChannelMigrationRPCRequest(append(reqBody, 0))
	require.ErrorContains(t, err, "trailing channel migration request bytes")

	respBody, err := encodeChannelMigrationRPCResponse(channelMigrationRPCResponse{
		Status:  rpcStatusOK,
		Tasks:   []metadb.ChannelMigrationTask{task},
		HasMore: true,
	})
	require.NoError(t, err)
	_, err = decodeChannelMigrationRPCResponse(append(respBody, 0))
	require.ErrorContains(t, err, "trailing channel migration response bytes")
}

func TestChannelMigrationRPCCodecRoundTripsListActiveForNode(t *testing.T) {
	task := proxyTestChannelMigrationTask("task-codec-list-active", "channel-codec-list-active")
	task.SourceNode = 3
	task.TargetNode = 4
	req := channelMigrationRPCRequest{
		Op:     channelMigrationRPCListActiveForNode,
		SlotID: 2,
		NodeID: 3,
		Limit:  25,
	}

	reqBody, err := encodeChannelMigrationRPCRequestBinary(req)
	require.NoError(t, err)
	gotReq, err := decodeChannelMigrationRPCRequest(reqBody)
	require.NoError(t, err)
	require.Equal(t, req, gotReq)

	resp := channelMigrationRPCResponse{
		Status:  rpcStatusOK,
		Tasks:   []metadb.ChannelMigrationTask{task},
		HasMore: true,
	}
	respBody, err := encodeChannelMigrationRPCResponse(resp)
	require.NoError(t, err)
	gotResp, err := decodeChannelMigrationRPCResponse(respBody)
	require.NoError(t, err)
	require.Equal(t, resp, gotResp)
}

func encodeLegacyChannelMigrationRPCRequest(t testing.TB, req channelMigrationRPCRequest) []byte {
	t.Helper()
	opID, err := channelMigrationOpID(req.Op)
	require.NoError(t, err)
	dst := make([]byte, 0, len(channelMigrationRPCRequestMagic)+len(req.ChannelID)+32)
	dst = append(dst, channelMigrationRPCRequestMagic[:]...)
	dst = append(dst, opID)
	dst = runtimeMetaAppendUvarint(dst, req.SlotID)
	dst = runtimeMetaAppendUvarint(dst, uint64(req.HashSlot))
	dst = runtimeMetaAppendString(dst, req.ChannelID)
	dst = runtimeMetaAppendVarint(dst, req.ChannelType)
	return channelMigrationAppendBytes(dst, req.Command)
}

func encodeLegacyChannelMigrationRPCResponse(resp channelMigrationRPCResponse) []byte {
	dst := make([]byte, 0, len(channelMigrationRPCResponseMagic)+128)
	dst = append(dst, channelMigrationRPCResponseMagic[:]...)
	dst = runtimeMetaAppendString(dst, resp.Status)
	dst = runtimeMetaAppendUvarint(dst, resp.LeaderID)
	return appendChannelMigrationTaskPtr(dst, resp.Task)
}

func channelMigrationTaskIDs(tasks []metadb.ChannelMigrationTask) []string {
	out := make([]string, 0, len(tasks))
	for _, task := range tasks {
		out = append(out, task.TaskID)
	}
	return out
}

func proxyTestChannelMigrationTask(taskID, channelID string) metadb.ChannelMigrationTask {
	return metadb.ChannelMigrationTask{
		TaskID:           taskID,
		Kind:             metadb.ChannelMigrationKindReplicaReplace,
		Status:           metadb.ChannelMigrationStatusPending,
		Phase:            metadb.ChannelMigrationPhaseValidate,
		ChannelID:        channelID,
		ChannelType:      1,
		SourceNode:       2,
		TargetNode:       3,
		DesiredLeader:    1,
		BaseChannelEpoch: 10,
		BaseLeaderEpoch:  20,
		CreatedAtMS:      1750000000000,
		UpdatedAtMS:      1750000000000,
	}
}

func proxyTestCompletedChannelMigrationTask(taskID, channelID string, completedAtMS int64) metadb.ChannelMigrationTask {
	task := proxyTestChannelMigrationTask(taskID, channelID)
	task.Status = metadb.ChannelMigrationStatusCompleted
	task.Phase = metadb.ChannelMigrationPhaseVerifyMembership
	task.UpdatedAtMS = completedAtMS
	task.CompletedAtMS = completedAtMS
	return task
}

func proxyWaitForClusterLeader(t testing.TB, cluster *raftcluster.Cluster, slotID, want uint64) {
	t.Helper()
	waitForCondition(t, func() bool {
		leaderID, err := cluster.LeaderOf(multiraft.SlotID(slotID))
		return err == nil && uint64(leaderID) == want
	}, "caller cluster observes slot leader")
}

func proxyTestRuntimeMeta(channelID string, channelType int64) metadb.ChannelRuntimeMeta {
	return metadb.ChannelRuntimeMeta{
		ChannelID:    channelID,
		ChannelType:  channelType,
		ChannelEpoch: 10,
		LeaderEpoch:  20,
		Replicas:     []uint64{1, 2},
		ISR:          []uint64{1, 2},
		Leader:       1,
		MinISR:       2,
		Status:       1,
		Features:     1,
		LeaseUntilMS: 1750000010000,
	}
}

func proxyTestFencedRuntimeMeta(channelID string, channelType int64, taskID string, version uint64) metadb.ChannelRuntimeMeta {
	meta := proxyTestRuntimeMeta(channelID, channelType)
	meta.WriteFenceToken = taskID
	meta.WriteFenceVersion = version
	meta.WriteFenceReason = 1
	meta.WriteFenceUntilMS = 1750000010000
	return meta
}

func proxyTestTaskGuard(task metadb.ChannelMigrationTask) metadb.ChannelMigrationTaskGuard {
	return metadb.ChannelMigrationTaskGuard{
		ChannelID:                 task.ChannelID,
		ChannelType:               task.ChannelType,
		TaskID:                    task.TaskID,
		ExpectedStatus:            task.Status,
		ExpectedPhase:             task.Phase,
		ExpectedOwnerNodeID:       task.OwnerNodeID,
		ExpectedOwnerLeaseUntilMS: task.OwnerLeaseUntilMS,
		ExpectedUpdatedAtMS:       task.UpdatedAtMS,
	}
}

func proxyTestRuntimeGuard(meta metadb.ChannelRuntimeMeta) metadb.ChannelMigrationRuntimeGuard {
	return metadb.ChannelMigrationRuntimeGuard{
		ChannelID:            meta.ChannelID,
		ChannelType:          meta.ChannelType,
		ExpectedChannelEpoch: meta.ChannelEpoch,
		ExpectedLeaderEpoch:  meta.LeaderEpoch,
		ExpectedLeader:       meta.Leader,
		ExpectedFenceToken:   meta.WriteFenceToken,
		ExpectedFenceVersion: meta.WriteFenceVersion,
	}
}

func proxyTestChannelMigrationClaim(task metadb.ChannelMigrationTask, owner uint64, leaseUntilMS, updatedAtMS int64) metadb.ChannelMigrationTaskClaim {
	return metadb.ChannelMigrationTaskClaim{
		Guard:             proxyTestTaskGuard(task),
		Status:            metadb.ChannelMigrationStatusRunning,
		Phase:             task.Phase,
		OwnerNodeID:       owner,
		OwnerLeaseUntilMS: leaseUntilMS,
		UpdatedAtMS:       updatedAtMS,
	}
}

func proxyTestChannelMigrationAdvance(existing, next metadb.ChannelMigrationTask) metadb.ChannelMigrationTaskAdvance {
	return metadb.ChannelMigrationTaskAdvance{
		Guard:          proxyTestTaskGuard(existing),
		Status:         next.Status,
		Phase:          next.Phase,
		Attempt:        next.Attempt,
		NextRunAtMS:    next.NextRunAtMS,
		BlockerCode:    next.BlockerCode,
		BlockerMessage: next.BlockerMessage,
		LastError:      next.LastError,
		UpdatedAtMS:    next.UpdatedAtMS,
		CompletedAtMS:  next.CompletedAtMS,
		Progress:       next.Progress,
	}
}

func proxyTestSetDrainProof(task *metadb.ChannelMigrationTask, fenceVersion uint64) {
	task.CutoverLEO = 100
	task.CutoverHW = 99
	task.DrainedLeaderNode = 1
	task.DrainedRuntimeGeneration = 2
	task.DrainedChannelEpoch = task.BaseChannelEpoch
	task.DrainedLeaderEpoch = task.BaseLeaderEpoch
	task.DrainedFenceVersion = fenceVersion
}

type proxyTestMigrationCluster struct {
	raftcluster.API
	nodeID         multiraft.NodeID
	localNodeID    multiraft.NodeID
	slotForKey     multiraft.SlotID
	hashSlotForKey uint16
	slotIDs        []multiraft.SlotID
	hashSlots      map[multiraft.SlotID][]uint16
	leaders        map[multiraft.SlotID]multiraft.NodeID
	peers          map[multiraft.SlotID][]multiraft.NodeID
	rpcResponse    []byte
	rpcService     func(context.Context, multiraft.NodeID, multiraft.SlotID, uint8, []byte) ([]byte, error)
	proposeResult  func(context.Context, multiraft.SlotID, uint16, []byte) ([]byte, error)
	proposals      int
}

func (c *proxyTestMigrationCluster) NodeID() multiraft.NodeID {
	return c.nodeID
}

func (c *proxyTestMigrationCluster) RPCMux() *transport.RPCMux {
	return nil
}

func (c *proxyTestMigrationCluster) SlotForKey(string) multiraft.SlotID {
	return c.slotForKey
}

func (c *proxyTestMigrationCluster) HashSlotForKey(string) uint16 {
	return c.hashSlotForKey
}

func (c *proxyTestMigrationCluster) HashSlotsOf(slotID multiraft.SlotID) []uint16 {
	return append([]uint16(nil), c.hashSlots[slotID]...)
}

func (c *proxyTestMigrationCluster) SlotIDs() []multiraft.SlotID {
	return append([]multiraft.SlotID(nil), c.slotIDs...)
}

func (c *proxyTestMigrationCluster) LeaderOf(slotID multiraft.SlotID) (multiraft.NodeID, error) {
	leaderID, ok := c.leaders[slotID]
	if !ok {
		return 0, raftcluster.ErrNoLeader
	}
	return leaderID, nil
}

func (c *proxyTestMigrationCluster) IsLocal(nodeID multiraft.NodeID) bool {
	return nodeID == c.localNodeID
}

func (c *proxyTestMigrationCluster) PeersForSlot(slotID multiraft.SlotID) []multiraft.NodeID {
	return append([]multiraft.NodeID(nil), c.peers[slotID]...)
}

func (c *proxyTestMigrationCluster) ProposeWithHashSlot(context.Context, multiraft.SlotID, uint16, []byte) error {
	c.proposals++
	return nil
}

func (c *proxyTestMigrationCluster) ProposeWithHashSlotResult(ctx context.Context, slotID multiraft.SlotID, hashSlot uint16, cmd []byte) ([]byte, error) {
	c.proposals++
	if c.proposeResult != nil {
		return c.proposeResult(ctx, slotID, hashSlot, cmd)
	}
	return nil, nil
}

func (c *proxyTestMigrationCluster) RPCService(ctx context.Context, nodeID multiraft.NodeID, slotID multiraft.SlotID, serviceID uint8, payload []byte) ([]byte, error) {
	if c.rpcService != nil {
		return c.rpcService(ctx, nodeID, slotID, serviceID, payload)
	}
	if c.rpcResponse == nil {
		return nil, fmt.Errorf("missing rpc response")
	}
	return c.rpcResponse, nil
}
