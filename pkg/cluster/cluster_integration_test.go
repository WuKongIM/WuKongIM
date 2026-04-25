//go:build integration
// +build integration

package cluster_test

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	raftcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/stretchr/testify/require"
)

func TestTestClusterTimingConfigUsesFastTiming(t *testing.T) {
	cfg := testClusterTimingConfig()

	require.Equal(t, 25*time.Millisecond, cfg.TickInterval)
	require.Equal(t, 6, cfg.ElectionTick)
	require.Equal(t, 1, cfg.HeartbeatTick)
	require.Equal(t, 750*time.Millisecond, cfg.DialTimeout)
	require.Equal(t, 750*time.Millisecond, cfg.ForwardTimeout)
	require.Equal(t, 1, cfg.PoolSize)
}

func TestStableLeaderWithinUsesShortConfirmationWindow(t *testing.T) {
	nodes := startThreeNodes(t, 1)

	start := time.Now()
	waitForStableLeader(t, nodes, 1)

	require.Less(t, time.Since(start), time.Second)
}

func TestWaitForManagedSlotsSettledReturnsQuicklyAfterAssignmentsExist(t *testing.T) {
	nodes := startThreeNodesWithControllerWithSettle(t, 4, 3, false)
	waitForControllerAssignments(t, nodes, 4)

	start := time.Now()
	waitForManagedSlotsSettled(t, nodes, 4)

	require.Less(t, time.Since(start), 3*time.Second)
}

func TestStopNodesReturnsWithoutFixedThreeSecondDelay(t *testing.T) {
	nodes := startThreeNodes(t, 1)

	start := time.Now()
	stopNodes(nodes)

	require.Less(t, time.Since(start), time.Second)
}

func TestTestNodeRestartReopensClusterWithSameListenAddr(t *testing.T) {
	testNodes := startThreeNodes(t, 1)
	defer func() {
		for _, n := range testNodes {
			if n != nil {
				n.stop()
			}
		}
	}()

	old := testNodes[0]
	oldAddr := old.listenAddr
	oldDir := old.dir

	restarted := restartNode(t, testNodes, 0)

	if restarted.listenAddr != oldAddr {
		t.Fatalf("listenAddr = %q, want %q", restarted.listenAddr, oldAddr)
	}
	if restarted.dir != oldDir {
		t.Fatalf("dir = %q, want %q", restarted.dir, oldDir)
	}

	waitForStableLeader(t, testNodes, 1)
}

func TestThreeNodeClusterReelectsAfterLeaderRestart(t *testing.T) {
	testNodes := startThreeNodes(t, 1)
	defer func() {
		for _, n := range testNodes {
			if n != nil {
				n.stop()
			}
		}
	}()

	originalLeader := waitForStableLeader(t, testNodes, 1)
	leaderIdx := int(originalLeader - 1)

	testNodes[leaderIdx].stop()
	newLeader := waitForStableLeader(t, testNodes, 1)
	if newLeader == originalLeader {
		t.Fatalf("leader did not change after restarting node %d", originalLeader)
	}

	var follower *testNode
	for _, node := range testNodes {
		if node == nil || node.store == nil || node.cluster == nil || node.nodeID == newLeader {
			continue
		}
		follower = node
		break
	}
	if follower == nil {
		t.Fatal("no follower available after leader restart")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	duringFailoverID := fmt.Sprintf("reelect-during-failover-%d", time.Now().UnixNano())
	if err := follower.store.CreateChannel(ctx, duringFailoverID, 1); err != nil {
		t.Fatalf("CreateChannel during failover: %v", err)
	}
	waitForChannelVisibleOnNodes(t, testNodes, duringFailoverID, 1)

	restarted := restartNode(t, testNodes, leaderIdx)
	stableLeader := waitForStableLeader(t, testNodes, 1)
	if stableLeader == 0 {
		t.Fatal("missing stable leader after restarting old leader")
	}

	afterRestartID := fmt.Sprintf("reelect-after-restart-%d", time.Now().UnixNano())
	if err := restarted.store.CreateChannel(ctx, afterRestartID, 1); err != nil {
		t.Fatalf("CreateChannel via restarted node: %v", err)
	}
	waitForChannelVisibleOnNodes(t, testNodes, afterRestartID, 1)
}

func TestClusterReportsRuntimeViewsToControllerIncrementally(t *testing.T) {
	nodes := startThreeNodesWithControllerWithSettle(t, 4, 3, false)
	defer stopNodes(nodes)

	require.Eventually(t, func() bool {
		views, err := nodes[0].cluster.ListObservedRuntimeViews(context.Background())
		return err == nil && len(views) == 4
	}, 40*time.Second, 100*time.Millisecond)
}

func TestClusterGroupIDsNoLongerDependOnStaticSlotConfig(t *testing.T) {
	node := startSingleNodeWithController(t, 8, 1)
	defer node.stop()

	require.Equal(t, []multiraft.SlotID{1, 2, 3, 4, 5, 6, 7, 8}, node.cluster.SlotIDs())
}

func TestClusterBootstrapsManagedSlotsFromControllerAssignments(t *testing.T) {
	nodes := startThreeNodesWithController(t, 4, 3)
	defer stopNodes(nodes)

	for slotID := 1; slotID <= 4; slotID++ {
		waitForStableLeader(t, nodes, uint64(slotID))
	}
}

func TestClusterContinuesServingWithOneReplicaNodeDown(t *testing.T) {
	nodes := startThreeNodesWithController(t, 1, 3)
	defer stopNodes(nodes)

	leaderIdx := int(waitForStableLeader(t, nodes, 1) - 1)
	nodes[leaderIdx].stop()

	require.Eventually(t, func() bool {
		_, err := nodes[(leaderIdx+1)%3].cluster.LeaderOf(1)
		return err == nil
	}, 10*time.Second, 100*time.Millisecond)
}

func TestClusterListSlotAssignmentsReflectsControllerState(t *testing.T) {
	nodes := startFourNodesWithController(t, 2, 3)
	defer stopNodes(nodes)

	assignments, err := nodes[0].cluster.ListSlotAssignments(context.Background())
	require.NoError(t, err)
	require.Len(t, assignments, 2)
	require.Len(t, assignments[0].DesiredPeers, 3)
}

func TestClusterTransferSlotLeaderDelegatesToManagedSlot(t *testing.T) {
	nodes := startThreeNodesWithController(t, 1, 3)
	defer stopNodes(nodes)

	currentLeader := waitForStableLeader(t, nodes, 1)
	target := multiraft.NodeID(1)
	if currentLeader == target {
		target = 2
	}

	require.NoError(t, nodes[0].cluster.TransferSlotLeader(context.Background(), 1, target))
	require.Eventually(t, func() bool {
		leader, err := nodes[0].cluster.LeaderOf(1)
		return err == nil && leader == target
	}, 10*time.Second, 100*time.Millisecond)
}

func TestClusterMarkNodeDrainingMovesAssignmentsAway(t *testing.T) {
	nodes := startFourNodesWithController(t, 2, 3)
	defer stopNodes(nodes)

	require.NoError(t, nodes[0].cluster.MarkNodeDraining(context.Background(), 1))
	require.Eventually(t, func() bool {
		assignments, err := nodes[0].cluster.ListSlotAssignments(context.Background())
		if err != nil {
			return false
		}
		for _, assignment := range assignments {
			for _, peer := range assignment.DesiredPeers {
				if peer == 1 {
					return false
				}
			}
		}
		return true
	}, 20*time.Second, 200*time.Millisecond)
}

func TestClusterSurfacesFailedRepairAfterRetryExhaustion(t *testing.T) {
	nodes := startFourNodesWithPermanentRepairFailure(t, 1, 3)
	defer stopNodes(nodes)

	var last string
	require.Eventually(t, func() bool {
		controller, ok := currentControllerLeaderNode(nodes)
		if !ok {
			last = "controller unavailable"
			return false
		}
		task, err := controller.cluster.GetReconcileTask(context.Background(), 1)
		if err != nil {
			last = fmt.Sprintf("controller=%d err=%v", controller.nodeID, err)
			return false
		}
		last = fmt.Sprintf(
			"controller=%d status=%v attempt=%d last_error=%q",
			controller.nodeID,
			task.Status,
			task.Attempt,
			task.LastError,
		)
		return task.Status == raftcluster.TaskStatusFailed &&
			task.Attempt == 3 &&
			task.LastError == "injected repair failure"
	}, 180*time.Second, 200*time.Millisecond, "last observed task: %s", last)
}

func TestClusterRecoverSlotReturnsManualRecoveryErrorWhenQuorumLost(t *testing.T) {
	nodes := startThreeNodesWithController(t, 1, 3)
	defer stopNodes(nodes)

	waitForStableLeader(t, nodes, 1)
	nodes[1].stop()
	nodes[2].stop()

	err := nodes[0].cluster.RecoverSlot(context.Background(), 1, raftcluster.RecoverStrategyLatestLiveReplica)
	require.ErrorIs(t, err, raftcluster.ErrManualRecoveryRequired)
}

func TestClusterRebalancesAfterNewWorkerNodeJoins(t *testing.T) {
	nodes := startThreeOfFourNodesWithController(t, 2, 3)
	defer stopNodes(nodes)

	assignments := snapshotAssignments(t, nodes[:3], 2)
	require.False(t, assignmentsContainPeer(assignments, 4))

	nodes[3] = restartNode(t, nodes, 3)

	require.Eventually(t, func() bool {
		assignments, ok := loadAssignments(nodes, 2)
		return ok && assignmentsContainPeer(assignments, 4)
	}, 20*time.Second, 200*time.Millisecond)
}

func TestClusterRebalancesAfterRecoveredNodeReturns(t *testing.T) {
	nodes := startFourNodesWithController(t, 2, 3)
	defer stopNodes(nodes)

	require.Eventually(t, func() bool {
		assignments, ok := loadAssignments(nodes, 2)
		return ok && assignmentsContainPeer(assignments, 4)
	}, 20*time.Second, 200*time.Millisecond)

	requireControllerCommand(t, nodes, func(cluster *raftcluster.Cluster) error {
		return cluster.MarkNodeDraining(context.Background(), 4)
	})

	require.Eventually(t, func() bool {
		assignments, ok := loadAssignments(nodes, 2)
		return ok && !assignmentsContainPeer(assignments, 4)
	}, 20*time.Second, 200*time.Millisecond)

	requireControllerCommand(t, nodes, func(cluster *raftcluster.Cluster) error {
		return cluster.ResumeNode(context.Background(), 4)
	})

	require.Eventually(t, func() bool {
		assignments, ok := loadAssignments(nodes, 2)
		return ok && assignmentsContainPeer(assignments, 4)
	}, 20*time.Second, 200*time.Millisecond)
}

func TestClusterControllerLeaderFailoverResumesInFlightRepair(t *testing.T) {
	nodes := startFourNodesWithController(t, 2, 3)
	defer stopNodes(nodes)

	controllerLeader := waitForControllerLeader(t, nodes)
	assignments := snapshotAssignments(t, nodes, 2)
	slotID, sourceNode := slotForControllerLeader(assignments, controllerLeader)
	require.NotZero(t, slotID)
	require.NotZero(t, sourceNode)

	var failRepair atomic.Bool
	failRepair.Store(true)
	var repairExecCount atomic.Int32
	restore := setManagedSlotExecutionHookOnNodes(t, nodes, func(taskGroupID uint32, task controllermeta.ReconcileTask) error {
		if failRepair.Load() && taskGroupID == slotID && task.Kind == controllermeta.TaskKindRepair {
			repairExecCount.Add(1)
			return errors.New("injected repair failure")
		}
		return nil
	})
	defer restore()

	requireControllerCommand(t, nodes, func(cluster *raftcluster.Cluster) error {
		return cluster.MarkNodeDraining(context.Background(), sourceNode)
	})
	require.Eventually(t, func() bool {
		controller, ok := currentControllerLeaderNode(nodes)
		if !ok {
			return false
		}
		if err := controller.cluster.ForceReconcile(context.Background(), slotID); err != nil {
			return false
		}
		_, err := controller.cluster.GetReconcileTask(context.Background(), slotID)
		return err == nil
	}, 10*time.Second, 200*time.Millisecond)
	require.Eventually(t, func() bool {
		return repairExecCount.Load() >= 1
	}, 30*time.Second, 200*time.Millisecond)

	nodes[int(controllerLeader-1)].stop()
	failRepair.Store(false)

	require.Eventually(t, func() bool {
		assignments, ok := loadAssignments(nodes, 2)
		if !ok {
			return false
		}
		for _, assignment := range assignments {
			if assignment.SlotID == slotID {
				for _, peer := range assignment.DesiredPeers {
					if peer == sourceNode {
						return false
					}
				}
				return true
			}
		}
		return false
	}, 20*time.Second, 200*time.Millisecond)
}
func TestGroupAgentApplyAssignmentsFallsBackToLocalControllerTaskWhenControllerReadTimesOut(t *testing.T) {
	harness := newStandaloneAgentTestCluster(t)

	dir := t.TempDir()
	store, err := controllermeta.Open(filepath.Join(dir, "controller-meta"))
	if err != nil {
		t.Fatalf("open controllermeta: %v", err)
	}
	harness.cluster.controllerMeta = store

	assignment := controllermeta.SlotAssignment{
		SlotID:       1,
		DesiredPeers: []uint64{1},
		ConfigEpoch:  1,
	}
	task := controllermeta.ReconcileTask{
		SlotID:    1,
		Kind:      controllermeta.TaskKindBootstrap,
		Step:      controllermeta.TaskStepAddLearner,
		Status:    controllermeta.TaskStatusPending,
		NextRunAt: time.Now(),
	}
	if err := store.UpsertAssignment(context.Background(), assignment); err != nil {
		t.Fatalf("UpsertAssignment() error = %v", err)
	}
	if err := store.UpsertTask(context.Background(), task); err != nil {
		t.Fatalf("UpsertTask() error = %v", err)
	}

	harness.cluster.assignments.SetAssignments([]controllermeta.SlotAssignment{assignment})
	harness.cluster.agent = &slotAgent{
		cluster: harness.cluster,
		client: fakeControllerClient{
			listNodesErr:        context.DeadlineExceeded,
			listRuntimeViewsErr: context.DeadlineExceeded,
			getTaskErr:          context.DeadlineExceeded,
		},
		cache: harness.cluster.assignments,
	}

	if err := harness.cluster.agent.ApplyAssignments(context.Background()); err != nil {
		t.Fatalf("ApplyAssignments() error = %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := harness.cluster.runtime.Status(1); err == nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("slot 1 was not bootstrapped")
}

func TestObserveOnceAppliesCachedAssignmentsWhenSyncAssignmentsTimesOut(t *testing.T) {
	harness := newStandaloneAgentTestCluster(t)

	assignment := controllermeta.SlotAssignment{
		SlotID:       1,
		DesiredPeers: []uint64{1},
		ConfigEpoch:  1,
	}
	task := controllermeta.ReconcileTask{
		SlotID:    1,
		Kind:      controllermeta.TaskKindBootstrap,
		Step:      controllermeta.TaskStepAddLearner,
		Status:    controllermeta.TaskStatusPending,
		NextRunAt: time.Now(),
	}

	reportCalls := 0
	agent := &slotAgent{
		cluster: harness.cluster,
		client: fakeControllerClient{
			assignmentsErr: context.DeadlineExceeded,
			tasks:          map[uint32]controllermeta.ReconcileTask{1: task},
			reportTaskResultFn: func(_ context.Context, task controllermeta.ReconcileTask, taskErr error) error {
				reportCalls++
				if task.SlotID != 1 {
					t.Fatalf("ReportTaskResult() slotID = %d, want 1", task.SlotID)
				}
				if taskErr != nil {
					t.Fatalf("ReportTaskResult() err = %v, want nil", taskErr)
				}
				return nil
			},
		},
		cache: harness.cluster.assignments,
	}
	harness.cluster.assignments.SetAssignments([]controllermeta.SlotAssignment{assignment})
	agent.storePendingTaskReport(1, task, nil)
	harness.cluster.agent = agent

	harness.cluster.observeOnce(context.Background())

	if reportCalls != 1 {
		t.Fatalf("ReportTaskResult() calls = %d, want 1", reportCalls)
	}
	if _, ok := agent.pendingTaskReport(1); ok {
		t.Fatal("pending task report was not cleared")
	}
}

func TestGroupAgentBootstrapsBrandNewGroupWhenBootstrapTaskExists(t *testing.T) {
	harness := newStandaloneAgentTestCluster(t)
	assignment := controllermeta.SlotAssignment{
		SlotID:       1,
		DesiredPeers: []uint64{1},
		ConfigEpoch:  1,
	}
	client := fakeControllerClient{
		assignments: []controllermeta.SlotAssignment{assignment},
		tasks: map[uint32]controllermeta.ReconcileTask{
			1: {
				SlotID:    1,
				Kind:      controllermeta.TaskKindBootstrap,
				Step:      controllermeta.TaskStepAddLearner,
				Status:    controllermeta.TaskStatusPending,
				NextRunAt: time.Now(),
			},
		},
	}
	harness.cluster.assignments.SetAssignments(client.assignments)
	harness.cluster.agent = &slotAgent{
		cluster: harness.cluster,
		client:  client,
		cache:   harness.cluster.assignments,
	}

	if err := harness.cluster.agent.ApplyAssignments(context.Background()); err != nil {
		t.Fatalf("ApplyAssignments() error = %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := harness.cluster.runtime.Status(1); err == nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("slot 1 was not bootstrapped")
}

func TestGroupAgentReopensPersistedGroupBeforeTaskFetch(t *testing.T) {
	harness := newStandaloneAgentTestCluster(t)
	requireNoError := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	requireNoError(harness.cluster.ensureManagedSlotLocal(context.Background(), 1, []uint64{1}, false, true))
	requireNoError(harness.cluster.waitForManagedSlotLeader(context.Background(), 1))
	requireNoError(harness.cluster.runtime.CloseSlot(context.Background(), 1))

	taskErr := errors.New("task fetch failed")
	assignment := controllermeta.SlotAssignment{
		SlotID:       1,
		DesiredPeers: []uint64{1},
		ConfigEpoch:  1,
	}
	client := fakeControllerClient{
		assignments: []controllermeta.SlotAssignment{assignment},
		getTaskErr:  taskErr,
	}
	harness.cluster.assignments.SetAssignments(client.assignments)
	harness.cluster.agent = &slotAgent{
		cluster: harness.cluster,
		client:  client,
		cache:   harness.cluster.assignments,
	}

	err := harness.cluster.agent.ApplyAssignments(context.Background())
	if !errors.Is(err, taskErr) {
		t.Fatalf("ApplyAssignments() error = %v, want %v", err, taskErr)
	}
	if _, err := harness.cluster.runtime.Status(1); err != nil {
		t.Fatalf("runtime.Status() error = %v", err)
	}
}

func TestGroupAgentKeepsSourceGroupOpenWhileRepairTaskPending(t *testing.T) {
	harness := newStandaloneAgentTestCluster(t)
	requireNoError := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	requireNoError(harness.cluster.ensureManagedSlotLocal(context.Background(), 1, []uint64{1}, false, true))
	requireNoError(harness.cluster.waitForManagedSlotLeader(context.Background(), 1))

	assignment := controllermeta.SlotAssignment{
		SlotID:       1,
		DesiredPeers: []uint64{2, 3},
		ConfigEpoch:  2,
	}
	client := fakeControllerClient{
		assignments: []controllermeta.SlotAssignment{assignment},
		tasks: map[uint32]controllermeta.ReconcileTask{
			1: {
				SlotID:     1,
				Kind:       controllermeta.TaskKindRepair,
				SourceNode: 1,
				TargetNode: 2,
				Status:     controllermeta.TaskStatusPending,
				NextRunAt:  time.Now(),
			},
		},
	}
	harness.cluster.assignments.SetAssignments(client.assignments)
	harness.cluster.agent = &slotAgent{
		cluster: harness.cluster,
		client:  client,
		cache:   harness.cluster.assignments,
	}

	if err := harness.cluster.agent.ApplyAssignments(context.Background()); err != nil {
		t.Fatalf("ApplyAssignments() error = %v", err)
	}
	if _, err := harness.cluster.runtime.Status(1); err != nil {
		t.Fatalf("runtime.Status() error = %v, want source slot to remain open while repair is pending", err)
	}
}

func TestGroupAgentRetriesTaskResultReportOnControllerLeaderChange(t *testing.T) {
	harness := newStandaloneAgentTestCluster(t)
	assignment := controllermeta.SlotAssignment{
		SlotID:       1,
		DesiredPeers: []uint64{1},
		ConfigEpoch:  1,
	}
	task := controllermeta.ReconcileTask{
		SlotID:    1,
		Kind:      controllermeta.TaskKindBootstrap,
		Step:      controllermeta.TaskStepAddLearner,
		Status:    controllermeta.TaskStatusPending,
		NextRunAt: time.Now(),
	}
	execErr := errors.New("injected execution failure")
	restore := SetManagedSlotExecutionTestHook(func(slotID uint32, got controllermeta.ReconcileTask) error {
		if slotID == 1 && got.Kind == controllermeta.TaskKindBootstrap {
			return execErr
		}
		return nil
	})
	defer restore()

	reportCalls := 0
	client := fakeControllerClient{
		assignments: []controllermeta.SlotAssignment{assignment},
		tasks:       map[uint32]controllermeta.ReconcileTask{1: task},
		reportTaskResultFn: func(_ context.Context, task controllermeta.ReconcileTask, taskErr error) error {
			reportCalls++
			if task.SlotID != 1 {
				t.Fatalf("ReportTaskResult() slotID = %d, want 1", task.SlotID)
			}
			if !errors.Is(taskErr, execErr) {
				t.Fatalf("ReportTaskResult() err = %v, want %v", taskErr, execErr)
			}
			if reportCalls == 1 {
				return ErrNotLeader
			}
			return nil
		},
	}
	harness.cluster.assignments.SetAssignments(client.assignments)
	harness.cluster.agent = &slotAgent{
		cluster: harness.cluster,
		client:  client,
		cache:   harness.cluster.assignments,
	}

	if err := harness.cluster.agent.ApplyAssignments(context.Background()); err != nil {
		t.Fatalf("ApplyAssignments() error = %v", err)
	}
	if reportCalls != 2 {
		t.Fatalf("ReportTaskResult() calls = %d, want 2", reportCalls)
	}
}

func TestGroupAgentRetriesTaskResultReportAfterTransientControllerTimeout(t *testing.T) {
	harness := newStandaloneAgentTestCluster(t)
	assignment := controllermeta.SlotAssignment{
		SlotID:       1,
		DesiredPeers: []uint64{1},
		ConfigEpoch:  1,
	}
	task := controllermeta.ReconcileTask{
		SlotID:    1,
		Kind:      controllermeta.TaskKindBootstrap,
		Step:      controllermeta.TaskStepAddLearner,
		Status:    controllermeta.TaskStatusPending,
		NextRunAt: time.Now(),
	}
	execErr := errors.New("injected execution failure")
	restore := SetManagedSlotExecutionTestHook(func(slotID uint32, got controllermeta.ReconcileTask) error {
		if slotID == 1 && got.Kind == controllermeta.TaskKindBootstrap {
			return execErr
		}
		return nil
	})
	defer restore()

	reportCalls := 0
	client := fakeControllerClient{
		assignments: []controllermeta.SlotAssignment{assignment},
		tasks:       map[uint32]controllermeta.ReconcileTask{1: task},
		reportTaskResultFn: func(_ context.Context, task controllermeta.ReconcileTask, taskErr error) error {
			reportCalls++
			if task.SlotID != 1 {
				t.Fatalf("ReportTaskResult() slotID = %d, want 1", task.SlotID)
			}
			if !errors.Is(taskErr, execErr) {
				t.Fatalf("ReportTaskResult() err = %v, want %v", taskErr, execErr)
			}
			if reportCalls == 1 {
				return context.DeadlineExceeded
			}
			return nil
		},
	}
	harness.cluster.assignments.SetAssignments(client.assignments)
	harness.cluster.agent = &slotAgent{
		cluster: harness.cluster,
		client:  client,
		cache:   harness.cluster.assignments,
	}

	if err := harness.cluster.agent.ApplyAssignments(context.Background()); err != nil {
		t.Fatalf("ApplyAssignments() error = %v", err)
	}
	if reportCalls != 2 {
		t.Fatalf("ReportTaskResult() calls = %d, want 2", reportCalls)
	}
}

func TestGroupAgentApplyAssignmentsRetriesTransientGetTaskTimeoutWithoutLocalControllerMeta(t *testing.T) {
	harness := newStandaloneAgentTestCluster(t)
	assignment := controllermeta.SlotAssignment{
		SlotID:       1,
		DesiredPeers: []uint64{1},
		ConfigEpoch:  1,
	}
	task := controllermeta.ReconcileTask{
		SlotID:    1,
		Kind:      controllermeta.TaskKindBootstrap,
		Step:      controllermeta.TaskStepAddLearner,
		Status:    controllermeta.TaskStatusPending,
		NextRunAt: time.Now(),
	}

	listTasksCalls := 0
	getTaskCalls := 0
	client := fakeControllerClient{
		assignments: []controllermeta.SlotAssignment{assignment},
		listTasksFn: func(context.Context) ([]controllermeta.ReconcileTask, error) {
			listTasksCalls++
			if listTasksCalls == 1 {
				return nil, context.DeadlineExceeded
			}
			return []controllermeta.ReconcileTask{task}, nil
		},
		getTaskFn: func(_ context.Context, slotID uint32) (controllermeta.ReconcileTask, error) {
			getTaskCalls++
			if slotID != 1 {
				t.Fatalf("GetTask() slotID = %d, want 1", slotID)
			}
			return task, nil
		},
	}
	harness.cluster.assignments.SetAssignments(client.assignments)
	harness.cluster.agent = &slotAgent{
		cluster: harness.cluster,
		client:  client,
		cache:   harness.cluster.assignments,
	}

	if err := harness.cluster.agent.ApplyAssignments(context.Background()); err != nil {
		t.Fatalf("ApplyAssignments() error = %v", err)
	}
	if listTasksCalls < 2 {
		t.Fatalf("ListTasks() calls = %d, want >= 2 retry attempts", listTasksCalls)
	}
	if getTaskCalls == 0 {
		t.Fatal("GetTask() was not called for fresh confirmation")
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := harness.cluster.runtime.Status(1); err == nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("slot 1 was not bootstrapped")
}

func TestGroupAgentDoesNotReexecuteTaskWhileResultReportIsPending(t *testing.T) {
	harness := newStandaloneAgentTestCluster(t)
	assignment := controllermeta.SlotAssignment{
		SlotID:       1,
		DesiredPeers: []uint64{1},
		ConfigEpoch:  1,
	}
	task := controllermeta.ReconcileTask{
		SlotID:    1,
		Kind:      controllermeta.TaskKindBootstrap,
		Step:      controllermeta.TaskStepAddLearner,
		Status:    controllermeta.TaskStatusPending,
		NextRunAt: time.Now(),
	}
	execCalls := 0
	restore := SetManagedSlotExecutionTestHook(func(slotID uint32, got controllermeta.ReconcileTask) error {
		if slotID == 1 && got.Kind == controllermeta.TaskKindBootstrap {
			execCalls++
		}
		return nil
	})
	defer restore()

	allowReport := false
	reportCalls := 0
	client := fakeControllerClient{
		assignments: []controllermeta.SlotAssignment{assignment},
		tasks:       map[uint32]controllermeta.ReconcileTask{1: task},
		reportTaskResultFn: func(_ context.Context, task controllermeta.ReconcileTask, _ error) error {
			reportCalls++
			if task.SlotID != 1 {
				t.Fatalf("ReportTaskResult() slotID = %d, want 1", task.SlotID)
			}
			if !allowReport {
				return ErrNotLeader
			}
			return nil
		},
	}
	harness.cluster.assignments.SetAssignments(client.assignments)
	harness.cluster.agent = &slotAgent{
		cluster: harness.cluster,
		client:  client,
		cache:   harness.cluster.assignments,
	}

	firstCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := harness.cluster.agent.ApplyAssignments(firstCtx); !errors.Is(err, ErrNotLeader) {
		t.Fatalf("ApplyAssignments() first error = %v, want %v", err, ErrNotLeader)
	}
	if execCalls != 1 {
		t.Fatalf("exec calls after first ApplyAssignments() = %d, want 1", execCalls)
	}

	allowReport = true
	if err := harness.cluster.agent.ApplyAssignments(context.Background()); err != nil {
		t.Fatalf("ApplyAssignments() second error = %v", err)
	}
	if execCalls != 1 {
		t.Fatalf("exec calls after second ApplyAssignments() = %d, want 1", execCalls)
	}
	if reportCalls < 2 {
		t.Fatalf("ReportTaskResult() calls = %d, want >= 2", reportCalls)
	}
}

func TestGroupAgentRetriesPendingTaskReportWithoutRefreshingTask(t *testing.T) {
	harness := newStandaloneAgentTestCluster(t)
	assignment := controllermeta.SlotAssignment{
		SlotID:       1,
		DesiredPeers: []uint64{1},
		ConfigEpoch:  1,
	}
	task := controllermeta.ReconcileTask{
		SlotID:    1,
		Kind:      controllermeta.TaskKindBootstrap,
		Step:      controllermeta.TaskStepAddLearner,
		Status:    controllermeta.TaskStatusPending,
		NextRunAt: time.Now(),
	}
	execErr := errors.New("injected execution failure")
	restore := SetManagedSlotExecutionTestHook(func(slotID uint32, got controllermeta.ReconcileTask) error {
		if slotID == 1 && got.Kind == controllermeta.TaskKindBootstrap {
			return execErr
		}
		return nil
	})
	defer restore()

	getTaskCalls := 0
	reportCalls := 0
	client := fakeControllerClient{
		assignments: []controllermeta.SlotAssignment{assignment},
		tasks:       map[uint32]controllermeta.ReconcileTask{1: task},
		getTaskFn: func(_ context.Context, slotID uint32) (controllermeta.ReconcileTask, error) {
			getTaskCalls++
			if slotID != 1 {
				t.Fatalf("GetTask() slotID = %d, want 1", slotID)
			}
			if getTaskCalls == 1 {
				return task, nil
			}
			return controllermeta.ReconcileTask{}, context.DeadlineExceeded
		},
		reportTaskResultFn: func(_ context.Context, got controllermeta.ReconcileTask, taskErr error) error {
			reportCalls++
			if got.SlotID != 1 {
				t.Fatalf("ReportTaskResult() slotID = %d, want 1", got.SlotID)
			}
			if got.Attempt != task.Attempt {
				t.Fatalf("ReportTaskResult() attempt = %d, want %d", got.Attempt, task.Attempt)
			}
			if !errors.Is(taskErr, execErr) {
				t.Fatalf("ReportTaskResult() err = %v, want %v", taskErr, execErr)
			}
			if reportCalls == 1 {
				return context.DeadlineExceeded
			}
			return nil
		},
	}
	harness.cluster.assignments.SetAssignments(client.assignments)
	harness.cluster.agent = &slotAgent{
		cluster: harness.cluster,
		client:  client,
		cache:   harness.cluster.assignments,
	}

	firstCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := harness.cluster.agent.ApplyAssignments(firstCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ApplyAssignments() first error = %v, want %v", err, context.DeadlineExceeded)
	}
	if reportCalls != 1 {
		t.Fatalf("ReportTaskResult() calls after first ApplyAssignments() = %d, want 1", reportCalls)
	}

	if err := harness.cluster.agent.ApplyAssignments(context.Background()); err != nil {
		t.Fatalf("ApplyAssignments() second error = %v", err)
	}
	if reportCalls != 2 {
		t.Fatalf("ReportTaskResult() calls after second ApplyAssignments() = %d, want 2", reportCalls)
	}
	if getTaskCalls != 1 {
		t.Fatalf("GetTask() calls = %d, want 1 because pending replay skips task refresh", getTaskCalls)
	}
}

func TestGroupAgentExecutesKnownTaskWhenFreshConfirmationTimesOut(t *testing.T) {
	harness := newStandaloneAgentTestCluster(t)
	assignment := controllermeta.SlotAssignment{
		SlotID:       1,
		DesiredPeers: []uint64{1},
		ConfigEpoch:  1,
	}
	task := controllermeta.ReconcileTask{
		SlotID:    1,
		Kind:      controllermeta.TaskKindBootstrap,
		Step:      controllermeta.TaskStepAddLearner,
		Status:    controllermeta.TaskStatusPending,
		NextRunAt: time.Now(),
	}
	execErr := errors.New("injected execution failure")
	execCalls := 0
	restore := SetManagedSlotExecutionTestHook(func(slotID uint32, got controllermeta.ReconcileTask) error {
		if slotID == 1 && got.Kind == controllermeta.TaskKindBootstrap {
			execCalls++
			return execErr
		}
		return nil
	})
	defer restore()

	getTaskCalls := 0
	reportCalls := 0
	client := fakeControllerClient{
		assignments: []controllermeta.SlotAssignment{assignment},
		tasks:       map[uint32]controllermeta.ReconcileTask{1: task},
		getTaskFn: func(_ context.Context, slotID uint32) (controllermeta.ReconcileTask, error) {
			getTaskCalls++
			if slotID != 1 {
				t.Fatalf("GetTask() slotID = %d, want 1", slotID)
			}
			return controllermeta.ReconcileTask{}, context.DeadlineExceeded
		},
		reportTaskResultFn: func(_ context.Context, got controllermeta.ReconcileTask, taskErr error) error {
			reportCalls++
			if got.SlotID != 1 {
				t.Fatalf("ReportTaskResult() slotID = %d, want 1", got.SlotID)
			}
			if got.Attempt != task.Attempt {
				t.Fatalf("ReportTaskResult() attempt = %d, want %d", got.Attempt, task.Attempt)
			}
			if !errors.Is(taskErr, execErr) {
				t.Fatalf("ReportTaskResult() err = %v, want %v", taskErr, execErr)
			}
			return nil
		},
	}
	harness.cluster.assignments.SetAssignments(client.assignments)
	harness.cluster.agent = &slotAgent{
		cluster: harness.cluster,
		client:  client,
		cache:   harness.cluster.assignments,
	}

	if err := harness.cluster.agent.ApplyAssignments(context.Background()); err != nil {
		t.Fatalf("ApplyAssignments() error = %v", err)
	}
	if getTaskCalls == 0 {
		t.Fatal("GetTask() was not called for fresh confirmation")
	}
	if execCalls != 1 {
		t.Fatalf("execution calls = %d, want 1", execCalls)
	}
	if reportCalls != 1 {
		t.Fatalf("ReportTaskResult() calls = %d, want 1", reportCalls)
	}
}

func TestGroupAgentSkipsBootstrapUntilBootstrapTaskExists(t *testing.T) {
	harness := newStandaloneAgentTestCluster(t)
	assignment := controllermeta.SlotAssignment{
		SlotID:       1,
		DesiredPeers: []uint64{1},
		ConfigEpoch:  1,
	}
	client := fakeControllerClient{
		assignments: []controllermeta.SlotAssignment{assignment},
	}
	harness.cluster.assignments.SetAssignments(client.assignments)
	harness.cluster.agent = &slotAgent{
		cluster: harness.cluster,
		client:  client,
		cache:   harness.cluster.assignments,
	}

	if err := harness.cluster.agent.ApplyAssignments(context.Background()); err != nil {
		t.Fatalf("ApplyAssignments() error = %v", err)
	}

	_, err := harness.cluster.runtime.Status(1)
	if !errors.Is(err, multiraft.ErrSlotNotFound) {
		t.Fatalf("runtime.Status() error = %v, want %v", err, multiraft.ErrSlotNotFound)
	}
}

func newStandaloneAgentTestCluster(t *testing.T) *standaloneAgentTestCluster {
	t.Helper()

	dir := t.TempDir()
	metaDB, err := metadb.Open(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatalf("open metadb: %v", err)
	}
	raftDB, err := raftstorage.Open(filepath.Join(dir, "raft"))
	if err != nil {
		_ = metaDB.Close()
		t.Fatalf("open raftstorage: %v", err)
	}

	cluster, err := NewCluster(Config{
		NodeID:       1,
		ListenAddr:   "127.0.0.1:0",
		SlotCount:    1,
		SlotReplicaN: 1,
		Nodes: []NodeConfig{
			{NodeID: 1, Addr: "127.0.0.1:0"},
		},
		NewStorage: func(slotID multiraft.SlotID) (multiraft.Storage, error) {
			return raftDB.ForSlot(uint64(slotID)), nil
		},
		NewStateMachine:              metafsm.NewStateMachineFactory(metaDB),
		NewStateMachineWithHashSlots: metafsm.NewHashSlotStateMachineFactory(metaDB),
	})
	if err != nil {
		_ = raftDB.Close()
		_ = metaDB.Close()
		t.Fatalf("NewCluster() error = %v", err)
	}
	if err := cluster.Start(); err != nil {
		_ = raftDB.Close()
		_ = metaDB.Close()
		t.Fatalf("cluster.Start() error = %v", err)
	}

	t.Cleanup(func() {
		(&standaloneAgentTestCluster{
			cluster: cluster,
			raftDB:  raftDB,
			metaDB:  metaDB,
		}).Close()
	})

	return &standaloneAgentTestCluster{
		cluster: cluster,
		raftDB:  raftDB,
		metaDB:  metaDB,
	}
}
