package management

import (
	"context"
	"testing"
	"time"

	raftcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
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
