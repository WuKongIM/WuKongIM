package management

import (
	"context"
	"testing"
	"time"

	raftcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/stretchr/testify/require"
)

func TestPlanNodeScaleInBlocksWhenSlotReplicaNMissing(t *testing.T) {
	app := New(Options{Cluster: fakeClusterReader{}})

	report, err := app.PlanNodeScaleIn(context.Background(), 1, NodeScaleInPlanRequest{})

	require.NoError(t, err)
	require.False(t, report.Checks.SlotReplicaCountKnown)
	require.Contains(t, scaleInReasonCodes(report.BlockedReasons), "slot_replica_count_unknown")
}

func TestPlanNodeScaleInPreflightBlockedReasons(t *testing.T) {
	now := time.Unix(1713686400, 0).UTC()
	healthyApp := func(mutator func(*fakeClusterReader, *NodeScaleInPlanRequest)) (*App, uint64, NodeScaleInPlanRequest) {
		req := NodeScaleInPlanRequest{ConfirmStatefulSetTail: true, ExpectedTailNodeID: 3}
		cluster := &fakeClusterReader{
			nodes: []controllermeta.ClusterNode{
				scaleInTestNode(1, controllermeta.NodeRoleData, controllermeta.NodeJoinStateActive, controllermeta.NodeStatusAlive),
				scaleInTestNode(2, controllermeta.NodeRoleData, controllermeta.NodeJoinStateActive, controllermeta.NodeStatusAlive),
				scaleInTestNode(3, controllermeta.NodeRoleData, controllermeta.NodeJoinStateActive, controllermeta.NodeStatusAlive),
			},
			assignments: []controllermeta.SlotAssignment{
				{SlotID: 1, DesiredPeers: []uint64{1, 2}, ConfigEpoch: 1},
				{SlotID: 2, DesiredPeers: []uint64{2, 3}, ConfigEpoch: 1},
			},
			views: []controllermeta.SlotRuntimeView{
				{SlotID: 1, CurrentPeers: []uint64{1, 2}, LeaderID: 1, HealthyVoters: 2, HasQuorum: true, LastReportAt: now},
				{SlotID: 2, CurrentPeers: []uint64{2, 3}, LeaderID: 2, HealthyVoters: 2, HasQuorum: true, LastReportAt: now},
			},
		}
		if mutator != nil {
			mutator(cluster, &req)
		}
		return New(Options{
			ControllerPeerIDs:        []uint64{1},
			SlotReplicaN:             2,
			ScaleInRuntimeViewMaxAge: time.Minute,
			Cluster:                  cluster,
			RuntimeSummary:           scaleInRuntimeSummaryReader{summary: NodeRuntimeSummary{NodeID: 3}},
			Now:                      func() time.Time { return now },
		}), 3, req
	}

	// Traceability:
	// nodes => target_not_found, target_not_data_node, target_not_active_or_draining,
	// target_is_controller_voter, other_draining_node_exists, remaining_data_nodes_insufficient;
	// assignments/views => runtime_views_incomplete_or_stale, slot_quorum_lost, target_unique_healthy_replica;
	// tasks => active_reconcile_tasks_involving_target, failed_reconcile_tasks_exist;
	// migrations/onboarding/strict-read errors => active_hashslot_migrations_exist, running_onboarding_exists,
	// controller_leader_unavailable; request/options => tail_node_mapping_unverified, slot_replica_count_unknown.
	tests := []struct {
		name      string
		mutator   func(*fakeClusterReader, *NodeScaleInPlanRequest)
		wantCodes []string
	}{
		{name: "missing node", mutator: func(c *fakeClusterReader, _ *NodeScaleInPlanRequest) {
			c.nodes = c.nodes[:2]
		}, wantCodes: []string{"target_not_found"}},
		{name: "non data node", mutator: func(c *fakeClusterReader, _ *NodeScaleInPlanRequest) {
			c.nodes[2].Role = controllermeta.NodeRoleUnknown
		}, wantCodes: []string{"target_not_data_node"}},
		{name: "controller voter", mutator: func(c *fakeClusterReader, _ *NodeScaleInPlanRequest) {
			c.nodes[2].Role = controllermeta.NodeRoleControllerVoter
		}, wantCodes: []string{"target_not_data_node", "target_is_controller_voter"}},
		{name: "target not active or draining", mutator: func(c *fakeClusterReader, _ *NodeScaleInPlanRequest) {
			c.nodes[2].Status = controllermeta.NodeStatusDead
		}, wantCodes: []string{"target_not_active_or_draining"}},
		{name: "tail mapping unverified", mutator: func(_ *fakeClusterReader, req *NodeScaleInPlanRequest) {
			req.ConfirmStatefulSetTail = false
		}, wantCodes: []string{"tail_node_mapping_unverified"}},
		{name: "other draining node", mutator: func(c *fakeClusterReader, _ *NodeScaleInPlanRequest) {
			c.nodes[1].Status = controllermeta.NodeStatusDraining
		}, wantCodes: []string{"other_draining_node_exists"}},
		{name: "remaining data nodes insufficient", mutator: func(c *fakeClusterReader, _ *NodeScaleInPlanRequest) {
			c.nodes = c.nodes[1:]
		}, wantCodes: []string{"remaining_data_nodes_insufficient"}},
		{name: "controller leader unavailable", mutator: func(c *fakeClusterReader, _ *NodeScaleInPlanRequest) {
			c.listNodesErr = context.DeadlineExceeded
		}, wantCodes: []string{"controller_leader_unavailable"}},
		{name: "active migration", mutator: func(c *fakeClusterReader, _ *NodeScaleInPlanRequest) {
			c.activeMigrations = []raftcluster.HashSlotMigration{{HashSlot: 1, Source: 2, Target: 1}}
		}, wantCodes: []string{"active_hashslot_migrations_exist"}},
		{name: "running onboarding", mutator: func(c *fakeClusterReader, _ *NodeScaleInPlanRequest) {
			c.onboardingJobs = []controllermeta.NodeOnboardingJob{{JobID: "job-1", Status: controllermeta.OnboardingJobStatusRunning}}
		}, wantCodes: []string{"running_onboarding_exists"}},
		{name: "active target task", mutator: func(c *fakeClusterReader, _ *NodeScaleInPlanRequest) {
			c.tasks = []controllermeta.ReconcileTask{{SlotID: 2, SourceNode: 3, Status: controllermeta.TaskStatusPending}}
		}, wantCodes: []string{"active_reconcile_tasks_involving_target"}},
		{name: "failed task", mutator: func(c *fakeClusterReader, _ *NodeScaleInPlanRequest) {
			c.tasks = []controllermeta.ReconcileTask{{SlotID: 1, Status: controllermeta.TaskStatusFailed}}
		}, wantCodes: []string{"failed_reconcile_tasks_exist"}},
		{name: "runtime view incomplete", mutator: func(c *fakeClusterReader, _ *NodeScaleInPlanRequest) {
			c.views = c.views[:1]
		}, wantCodes: []string{"runtime_views_incomplete_or_stale"}},
		{name: "runtime view stale", mutator: func(c *fakeClusterReader, _ *NodeScaleInPlanRequest) {
			c.views[1].LastReportAt = now.Add(-2 * time.Minute)
		}, wantCodes: []string{"runtime_views_incomplete_or_stale"}},
		{name: "quorum lost", mutator: func(c *fakeClusterReader, _ *NodeScaleInPlanRequest) {
			c.views[1].HasQuorum = false
		}, wantCodes: []string{"slot_quorum_lost"}},
		{name: "unique healthy replica", mutator: func(c *fakeClusterReader, _ *NodeScaleInPlanRequest) {
			c.views[1].HealthyVoters = 1
		}, wantCodes: []string{"target_unique_healthy_replica"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			app, nodeID, req := healthyApp(tc.mutator)

			report, err := app.PlanNodeScaleIn(context.Background(), nodeID, req)

			require.NoError(t, err)
			require.Subset(t, scaleInReasonCodes(report.BlockedReasons), tc.wantCodes)
			require.Equal(t, NodeScaleInStatusBlocked, report.Status)
			require.False(t, report.SafeToRemove)
		})
	}
}

func TestPlanNodeScaleInReportsReadyToRemove(t *testing.T) {
	now := time.Unix(1713686400, 0).UTC()
	app := New(Options{
		ControllerPeerIDs:        []uint64{1},
		SlotReplicaN:             2,
		ScaleInRuntimeViewMaxAge: time.Minute,
		Cluster: &fakeClusterReader{
			nodes: []controllermeta.ClusterNode{
				scaleInTestNode(1, controllermeta.NodeRoleData, controllermeta.NodeJoinStateActive, controllermeta.NodeStatusAlive),
				scaleInTestNode(2, controllermeta.NodeRoleData, controllermeta.NodeJoinStateActive, controllermeta.NodeStatusAlive),
				scaleInTestNode(3, controllermeta.NodeRoleData, controllermeta.NodeJoinStateActive, controllermeta.NodeStatusDraining),
			},
			assignments: []controllermeta.SlotAssignment{{SlotID: 1, DesiredPeers: []uint64{1, 2}, ConfigEpoch: 2}},
			views:       []controllermeta.SlotRuntimeView{{SlotID: 1, CurrentPeers: []uint64{1, 2}, LeaderID: 1, HealthyVoters: 2, HasQuorum: true, LastReportAt: now}},
		},
		RuntimeSummary: scaleInRuntimeSummaryReader{summary: NodeRuntimeSummary{NodeID: 3}},
		Now:            func() time.Time { return now },
	})

	report, err := app.PlanNodeScaleIn(context.Background(), 3, NodeScaleInPlanRequest{ConfirmStatefulSetTail: true, ExpectedTailNodeID: 3})

	require.NoError(t, err)
	require.Empty(t, report.BlockedReasons)
	require.Equal(t, NodeScaleInStatusReadyToRemove, report.Status)
	require.True(t, report.SafeToRemove)
	require.True(t, report.ConnectionSafetyVerified)
}

func TestStartNodeScaleInBlocksWhenPreflightFails(t *testing.T) {
	fixture := newScaleInActionFixture()

	report, err := fixture.app.StartNodeScaleIn(context.Background(), 3, NodeScaleInPlanRequest{})

	require.ErrorIs(t, err, ErrNodeScaleInBlocked)
	require.Contains(t, scaleInReasonCodes(report.BlockedReasons), "tail_node_mapping_unverified")
	require.Zero(t, fixture.cluster.markNodeDrainingCalls)
	require.False(t, report.SafeToRemove)
}

func TestStartNodeScaleInMarksNodeDrainingAndRefreshesReport(t *testing.T) {
	fixture := newScaleInActionFixture()

	report, err := fixture.app.StartNodeScaleIn(context.Background(), 3, fixture.req)

	require.NoError(t, err)
	require.Equal(t, uint64(3), fixture.cluster.markNodeDrainingNodeID)
	require.Equal(t, 1, fixture.cluster.markNodeDrainingCalls)
	require.Equal(t, NodeScaleInStatusMigratingReplicas, report.Status)
	require.False(t, report.SafeToRemove)
}

func TestStartNodeScaleInIsIdempotentWhenAlreadyDraining(t *testing.T) {
	fixture := newScaleInActionFixture()
	fixture.cluster.nodes[2].Status = controllermeta.NodeStatusDraining

	report, err := fixture.app.StartNodeScaleIn(context.Background(), 3, fixture.req)

	require.NoError(t, err)
	require.Zero(t, fixture.cluster.markNodeDrainingCalls)
	require.Equal(t, NodeScaleInStatusMigratingReplicas, report.Status)
}

func TestCancelNodeScaleInCallsResumeNode(t *testing.T) {
	fixture := newScaleInActionFixture()
	fixture.cluster.nodes[2].Status = controllermeta.NodeStatusDraining

	report, err := fixture.app.CancelNodeScaleIn(context.Background(), 3)

	require.NoError(t, err)
	require.Equal(t, uint64(3), fixture.cluster.resumeNodeID)
	require.Equal(t, 1, fixture.cluster.resumeNodeCalls)
	require.Equal(t, NodeScaleInStatusNotStarted, report.Status)
}

func TestAdvanceNodeScaleInTransfersOneLeaderToAliveDataCandidate(t *testing.T) {
	fixture := newScaleInActionFixture()
	fixture.cluster.nodes[2].Status = controllermeta.NodeStatusDraining
	fixture.cluster.assignments = []controllermeta.SlotAssignment{{SlotID: 7, DesiredPeers: []uint64{1, 2, 3}, ConfigEpoch: 1}}
	fixture.cluster.views = []controllermeta.SlotRuntimeView{{SlotID: 7, CurrentPeers: []uint64{1, 2, 3}, LeaderID: 3, HealthyVoters: 3, HasQuorum: true, LastReportAt: fixture.now}}

	report, err := fixture.app.AdvanceNodeScaleIn(context.Background(), 3, AdvanceNodeScaleInRequest{MaxLeaderTransfers: 1})

	require.NoError(t, err)
	require.Equal(t, []scaleInTransfer{{slotID: 7, nodeID: 1}}, fixture.cluster.transfers)
	require.Equal(t, NodeScaleInStatusMigratingReplicas, report.Status)
	require.Equal(t, 0, report.Progress.SlotLeaders)
}

func TestAdvanceNodeScaleInReturnsInvalidStateWhenNoLeaderCandidate(t *testing.T) {
	fixture := newScaleInActionFixture()
	fixture.cluster.nodes[2].Status = controllermeta.NodeStatusDraining
	fixture.cluster.assignments = []controllermeta.SlotAssignment{{SlotID: 7, DesiredPeers: []uint64{3}, ConfigEpoch: 1}}
	fixture.cluster.views = []controllermeta.SlotRuntimeView{{SlotID: 7, CurrentPeers: []uint64{3}, LeaderID: 3, HealthyVoters: 1, HasQuorum: true, LastReportAt: fixture.now}}

	report, err := fixture.app.AdvanceNodeScaleIn(context.Background(), 3, AdvanceNodeScaleInRequest{MaxLeaderTransfers: 1})

	require.ErrorIs(t, err, ErrInvalidNodeScaleInState)
	require.Empty(t, fixture.cluster.transfers)
	require.False(t, report.SafeToRemove)
}

func TestAdvanceNodeScaleInDoesNotTreatForceCloseAsSafetyOverride(t *testing.T) {
	fixture := newScaleInActionFixture()
	fixture.cluster.nodes[2].Status = controllermeta.NodeStatusDraining
	fixture.cluster.assignments = []controllermeta.SlotAssignment{{SlotID: 1, DesiredPeers: []uint64{1, 2}, ConfigEpoch: 2}}
	fixture.cluster.views = []controllermeta.SlotRuntimeView{{SlotID: 1, CurrentPeers: []uint64{1, 2}, LeaderID: 1, HealthyVoters: 2, HasQuorum: true, LastReportAt: fixture.now}}
	fixture.runtime.summary = NodeRuntimeSummary{NodeID: 3, ActiveOnline: 2}

	report, err := fixture.app.AdvanceNodeScaleIn(context.Background(), 3, AdvanceNodeScaleInRequest{ForceCloseConnections: true})

	require.ErrorIs(t, err, ErrInvalidNodeScaleInState)
	require.Equal(t, NodeScaleInStatusWaitingConnections, report.Status)
	require.False(t, report.SafeToRemove)
	require.Zero(t, len(fixture.cluster.transfers))
}

func scaleInReasonCodes(reasons []NodeScaleInBlockedReason) []string {
	codes := make([]string, 0, len(reasons))
	for _, reason := range reasons {
		codes = append(codes, reason.Code)
	}
	return codes
}

func scaleInTestNode(nodeID uint64, role controllermeta.NodeRole, joinState controllermeta.NodeJoinState, status controllermeta.NodeStatus) controllermeta.ClusterNode {
	return controllermeta.ClusterNode{
		NodeID:         nodeID,
		Addr:           "127.0.0.1:7000",
		Role:           role,
		JoinState:      joinState,
		Status:         status,
		CapacityWeight: 1,
	}
}

type scaleInRuntimeSummaryReader struct {
	summary NodeRuntimeSummary
	err     error
}

func (r scaleInRuntimeSummaryReader) NodeRuntimeSummary(context.Context, uint64) (NodeRuntimeSummary, error) {
	return r.summary, r.err
}

type mutableScaleInRuntimeSummaryReader struct {
	summary NodeRuntimeSummary
	err     error
}

func (r *mutableScaleInRuntimeSummaryReader) NodeRuntimeSummary(context.Context, uint64) (NodeRuntimeSummary, error) {
	return r.summary, r.err
}

type scaleInActionFixture struct {
	app     *App
	cluster *fakeScaleInActionCluster
	runtime *mutableScaleInRuntimeSummaryReader
	req     NodeScaleInPlanRequest
	now     time.Time
}

func newScaleInActionFixture() scaleInActionFixture {
	now := time.Unix(1713686400, 0).UTC()
	cluster := &fakeScaleInActionCluster{fakeClusterReader: fakeClusterReader{
		nodes: []controllermeta.ClusterNode{
			scaleInTestNode(1, controllermeta.NodeRoleData, controllermeta.NodeJoinStateActive, controllermeta.NodeStatusAlive),
			scaleInTestNode(2, controllermeta.NodeRoleData, controllermeta.NodeJoinStateActive, controllermeta.NodeStatusAlive),
			scaleInTestNode(3, controllermeta.NodeRoleData, controllermeta.NodeJoinStateActive, controllermeta.NodeStatusAlive),
		},
		assignments: []controllermeta.SlotAssignment{
			{SlotID: 1, DesiredPeers: []uint64{1, 2}, ConfigEpoch: 1},
			{SlotID: 2, DesiredPeers: []uint64{2, 3}, ConfigEpoch: 1},
		},
		views: []controllermeta.SlotRuntimeView{
			{SlotID: 1, CurrentPeers: []uint64{1, 2}, LeaderID: 1, HealthyVoters: 2, HasQuorum: true, LastReportAt: now},
			{SlotID: 2, CurrentPeers: []uint64{2, 3}, LeaderID: 2, HealthyVoters: 2, HasQuorum: true, LastReportAt: now},
		},
	}}
	runtime := &mutableScaleInRuntimeSummaryReader{summary: NodeRuntimeSummary{NodeID: 3}}
	req := NodeScaleInPlanRequest{ConfirmStatefulSetTail: true, ExpectedTailNodeID: 3}
	app := New(Options{
		ControllerPeerIDs:        []uint64{1},
		SlotReplicaN:             2,
		ScaleInRuntimeViewMaxAge: time.Minute,
		Cluster:                  cluster,
		RuntimeSummary:           runtime,
		Now:                      func() time.Time { return now },
	})
	return scaleInActionFixture{app: app, cluster: cluster, runtime: runtime, req: req, now: now}
}

type fakeScaleInActionCluster struct {
	fakeClusterReader
	markNodeDrainingNodeID uint64
	markNodeDrainingCalls  int
	resumeNodeID           uint64
	resumeNodeCalls        int
	transfers              []scaleInTransfer
}

type scaleInTransfer struct {
	slotID uint32
	nodeID multiraft.NodeID
}

func (f *fakeScaleInActionCluster) MarkNodeDraining(_ context.Context, nodeID uint64) error {
	f.markNodeDrainingNodeID = nodeID
	f.markNodeDrainingCalls++
	for i := range f.nodes {
		if f.nodes[i].NodeID == nodeID {
			f.nodes[i].Status = controllermeta.NodeStatusDraining
			return nil
		}
	}
	return controllermeta.ErrNotFound
}

func (f *fakeScaleInActionCluster) ResumeNode(_ context.Context, nodeID uint64) error {
	f.resumeNodeID = nodeID
	f.resumeNodeCalls++
	for i := range f.nodes {
		if f.nodes[i].NodeID == nodeID {
			f.nodes[i].Status = controllermeta.NodeStatusAlive
			return nil
		}
	}
	return controllermeta.ErrNotFound
}

func (f *fakeScaleInActionCluster) TransferSlotLeader(_ context.Context, slotID uint32, nodeID multiraft.NodeID) error {
	f.transfers = append(f.transfers, scaleInTransfer{slotID: slotID, nodeID: nodeID})
	for i := range f.views {
		if f.views[i].SlotID == slotID {
			f.views[i].LeaderID = uint64(nodeID)
		}
	}
	return nil
}
