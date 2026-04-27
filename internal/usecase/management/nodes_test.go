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

func TestListNodesAggregatesControllerRoleAndSlotCounts(t *testing.T) {
	now := time.Unix(1713686400, 0).UTC()
	app := New(Options{
		LocalNodeID:       2,
		ControllerPeerIDs: []uint64{1, 2},
		Cluster: fakeClusterReader{
			controllerLeaderID: 1,
			nodes: []controllermeta.ClusterNode{
				{NodeID: 3, Addr: "127.0.0.1:7003", Status: controllermeta.NodeStatusAlive, LastHeartbeatAt: now.Add(-3 * time.Second), CapacityWeight: 1},
				{NodeID: 1, Addr: "127.0.0.1:7001", Status: controllermeta.NodeStatusAlive, LastHeartbeatAt: now.Add(-1 * time.Second), CapacityWeight: 1},
				{NodeID: 2, Addr: "127.0.0.1:7002", Status: controllermeta.NodeStatusDraining, LastHeartbeatAt: now.Add(-2 * time.Second), CapacityWeight: 2},
			},
			views: []controllermeta.SlotRuntimeView{
				{SlotID: 1, CurrentPeers: []uint64{1, 2}, LeaderID: 1, HasQuorum: true},
				{SlotID: 2, CurrentPeers: []uint64{2, 3}, LeaderID: 2, HasQuorum: true},
			},
		},
	})

	got, err := app.ListNodes(context.Background())
	require.NoError(t, err)
	require.Equal(t, []nodeSummary{
		{NodeID: 1, Status: "alive", ControllerRole: "leader", SlotCount: 1, LeaderSlotCount: 1, IsLocal: false},
		{NodeID: 2, Status: "draining", ControllerRole: "follower", SlotCount: 2, LeaderSlotCount: 1, IsLocal: true},
		{NodeID: 3, Status: "alive", ControllerRole: "none", SlotCount: 1, LeaderSlotCount: 0, IsLocal: false},
	}, summarizeNodes(got))
	require.Equal(t, now.Add(-1*time.Second), got[0].LastHeartbeatAt)
	require.Equal(t, 2, got[1].CapacityWeight)
}

func TestListNodesAggregatesDistributedLogHealth(t *testing.T) {
	app := New(Options{
		LocalNodeID:       2,
		ControllerPeerIDs: []uint64{1, 2},
		Cluster: fakeClusterReader{
			controllerLeaderID: 1,
			nodes: []controllermeta.ClusterNode{
				{NodeID: 1, Addr: "127.0.0.1:7001", Status: controllermeta.NodeStatusAlive, CapacityWeight: 1},
				{NodeID: 2, Addr: "127.0.0.1:7002", Status: controllermeta.NodeStatusAlive, CapacityWeight: 1},
			},
			views: []controllermeta.SlotRuntimeView{
				{SlotID: 7, CurrentPeers: []uint64{1, 2}, LeaderID: 1, HasQuorum: true},
				{SlotID: 9, CurrentPeers: []uint64{1, 2}, LeaderID: 2, HasQuorum: false},
			},
			slotLogStatus: map[slotLogStatusKey]raftcluster.SlotLogStatus{
				{nodeID: 1, slotID: 7}: {LeaderID: 1, CommitIndex: 120, AppliedIndex: 118},
				{nodeID: 2, slotID: 7}: {LeaderID: 1, CommitIndex: 116, AppliedIndex: 114},
				{nodeID: 1, slotID: 9}: {LeaderID: 2, CommitIndex: 54, AppliedIndex: 54},
				{nodeID: 2, slotID: 9}: {LeaderID: 2, CommitIndex: 60, AppliedIndex: 58},
			},
		},
	})

	got, err := app.ListNodes(context.Background())
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Equal(t, NodeDistributedLog{
		Controller: NodeControllerLog{Role: "leader", LeaderID: 1, Voter: true},
		Slots: NodeSlotLogHealth{
			ReplicaCount:     2,
			LeaderCount:      1,
			FollowerCount:    1,
			MaxCommitLag:     6,
			MaxApplyGap:      2,
			UnavailableCount: 0,
			UnhealthyCount:   2,
			Samples: []NodeSlotLogSample{{
				SlotID:            9,
				Role:              "follower",
				LeaderID:          2,
				CommitIndex:       54,
				AppliedIndex:      54,
				LeaderCommitIndex: 60,
				CommitLag:         6,
				ApplyGap:          0,
				Quorum:            "lost",
				Status:            "quorum_lost",
			}, {
				SlotID:            7,
				Role:              "leader",
				LeaderID:          1,
				CommitIndex:       120,
				AppliedIndex:      118,
				LeaderCommitIndex: 120,
				CommitLag:         0,
				ApplyGap:          2,
				Quorum:            "healthy",
				Status:            "lagging",
			}},
		},
	}, got[0].DistributedLog)
	require.Equal(t, uint64(4), got[1].DistributedLog.Slots.MaxCommitLag)
	require.Equal(t, uint64(2), got[1].DistributedLog.Slots.MaxApplyGap)
}

func TestGetNodeIncludesUnavailableDistributedLogSamples(t *testing.T) {
	app := New(Options{
		LocalNodeID:       1,
		ControllerPeerIDs: []uint64{1},
		Cluster: fakeClusterReader{
			controllerLeaderID: 1,
			nodes: []controllermeta.ClusterNode{
				{NodeID: 2, Addr: "127.0.0.1:7002", Status: controllermeta.NodeStatusAlive, CapacityWeight: 1},
			},
			views: []controllermeta.SlotRuntimeView{
				{SlotID: 11, CurrentPeers: []uint64{2}, LeaderID: 0, HasQuorum: false},
			},
			slotLogStatusErr: map[slotLogStatusKey]error{
				{nodeID: 2, slotID: 11}: raftcluster.ErrSlotNotFound,
			},
		},
	})

	got, err := app.GetNode(context.Background(), 2)
	require.NoError(t, err)
	require.Equal(t, 1, got.DistributedLog.Slots.UnavailableCount)
	require.Equal(t, 1, got.DistributedLog.Slots.UnhealthyCount)
	require.Equal(t, []NodeSlotLogSample{{
		SlotID: 11,
		Role:   "unknown",
		Quorum: "lost",
		Status: "unavailable",
	}}, got.DistributedLog.Slots.Samples)
}

func TestListNodesSortsByNodeIDAndDefaultsCountsToZero(t *testing.T) {
	app := New(Options{
		LocalNodeID:       9,
		ControllerPeerIDs: []uint64{4},
		Cluster: fakeClusterReader{
			controllerLeaderID: 4,
			nodes: []controllermeta.ClusterNode{
				{NodeID: 9, Addr: "127.0.0.1:7009", Status: controllermeta.NodeStatusSuspect, CapacityWeight: 1},
				{NodeID: 4, Addr: "127.0.0.1:7004", Status: controllermeta.NodeStatusDead, CapacityWeight: 3},
			},
		},
	})

	got, err := app.ListNodes(context.Background())
	require.NoError(t, err)
	require.Equal(t, []nodeSummary{
		{NodeID: 4, Status: "dead", ControllerRole: "leader", SlotCount: 0, LeaderSlotCount: 0, IsLocal: false},
		{NodeID: 9, Status: "suspect", ControllerRole: "none", SlotCount: 0, LeaderSlotCount: 0, IsLocal: true},
	}, summarizeNodes(got))
}

func TestGetNodeReturnsNodeWithHostedAndLeaderSlots(t *testing.T) {
	now := time.Unix(1713686400, 0).UTC()
	app := New(Options{
		LocalNodeID:       2,
		ControllerPeerIDs: []uint64{1, 2},
		Cluster: fakeClusterReader{
			controllerLeaderID: 1,
			nodes: []controllermeta.ClusterNode{
				{NodeID: 2, Addr: "127.0.0.1:7002", Status: controllermeta.NodeStatusDraining, LastHeartbeatAt: now.Add(-2 * time.Second), CapacityWeight: 2},
				{NodeID: 1, Addr: "127.0.0.1:7001", Status: controllermeta.NodeStatusAlive, LastHeartbeatAt: now.Add(-1 * time.Second), CapacityWeight: 1},
			},
			views: []controllermeta.SlotRuntimeView{
				{SlotID: 7, CurrentPeers: []uint64{2, 1}, LeaderID: 1, HasQuorum: true},
				{SlotID: 2, CurrentPeers: []uint64{3, 2}, LeaderID: 2, HasQuorum: true},
				{SlotID: 4, CurrentPeers: []uint64{2}, LeaderID: 2, HasQuorum: true},
			},
		},
	})

	got, err := app.GetNode(context.Background(), 2)
	require.NoError(t, err)
	require.Equal(t, NodeDetail{
		Node: Node{
			NodeID:          2,
			Addr:            "127.0.0.1:7002",
			Status:          "draining",
			LastHeartbeatAt: now.Add(-2 * time.Second),
			ControllerRole:  "follower",
			SlotCount:       3,
			LeaderSlotCount: 2,
			IsLocal:         true,
			CapacityWeight:  2,
			DistributedLog: NodeDistributedLog{
				Controller: NodeControllerLog{Role: "follower", LeaderID: 1, Voter: true},
				Slots: NodeSlotLogHealth{
					ReplicaCount:  3,
					LeaderCount:   2,
					FollowerCount: 1,
				},
			},
		},
		Slots: NodeSlots{
			HostedIDs: []uint32{2, 4, 7},
			LeaderIDs: []uint32{2, 4},
		},
	}, got)
}

func TestGetNodeReturnsNotFound(t *testing.T) {
	app := New(Options{
		LocalNodeID:       1,
		ControllerPeerIDs: []uint64{1},
		Cluster: fakeClusterReader{
			controllerLeaderID: 1,
			nodes: []controllermeta.ClusterNode{
				{NodeID: 1, Addr: "127.0.0.1:7001", Status: controllermeta.NodeStatusAlive},
			},
		},
	})

	_, err := app.GetNode(context.Background(), 2)
	require.ErrorIs(t, err, controllermeta.ErrNotFound)
}

type fakeClusterReader struct {
	controllerLeaderID          uint64
	slotIDs                     []multiraft.SlotID
	slotForKey                  map[string]multiraft.SlotID
	hashSlotForKey              map[string]uint16
	nodes                       []controllermeta.ClusterNode
	listNodesErr                error
	assignments                 []controllermeta.SlotAssignment
	listSlotAssignmentsErr      error
	views                       []controllermeta.SlotRuntimeView
	listObservedRuntimeViewsErr error
	slotLogStatus               map[slotLogStatusKey]raftcluster.SlotLogStatus
	slotLogStatusErr            map[slotLogStatusKey]error
	tasks                       []controllermeta.ReconcileTask
	taskBySlot                  map[uint32]controllermeta.ReconcileTask
	listTasksErr                error
	getTaskErr                  error
	markNodeDrainingErr         error
	resumeNodeErr               error
	transferSlotLeaderErr       error
	recoverSlotStrictErr        error
	activeMigrations            []raftcluster.HashSlotMigration
	listActiveMigrationsErr     error
	migrationStatus             []raftcluster.HashSlotMigration
	rebalancePlan               []raftcluster.MigrationPlan
	rebalanceErr                error
	onboardingJobs              []controllermeta.NodeOnboardingJob
	onboardingHasMore           bool
	listOnboardingJobsErr       error
}

type slotLogStatusKey struct {
	nodeID uint64
	slotID uint32
}

func (f fakeClusterReader) SlotIDs() []multiraft.SlotID {
	return append([]multiraft.SlotID(nil), f.slotIDs...)
}

func (f fakeClusterReader) SlotForKey(key string) multiraft.SlotID {
	return f.slotForKey[key]
}

func (f fakeClusterReader) HashSlotForKey(key string) uint16 {
	return f.hashSlotForKey[key]
}

func (f fakeClusterReader) ListNodesStrict(context.Context) ([]controllermeta.ClusterNode, error) {
	return append([]controllermeta.ClusterNode(nil), f.nodes...), f.listNodesErr
}

func (f fakeClusterReader) ListSlotAssignmentsStrict(context.Context) ([]controllermeta.SlotAssignment, error) {
	return append([]controllermeta.SlotAssignment(nil), f.assignments...), f.listSlotAssignmentsErr
}

func (f fakeClusterReader) ListObservedRuntimeViewsStrict(context.Context) ([]controllermeta.SlotRuntimeView, error) {
	return append([]controllermeta.SlotRuntimeView(nil), f.views...), f.listObservedRuntimeViewsErr
}

func (f fakeClusterReader) SlotLogStatusOnNode(_ context.Context, nodeID uint64, slotID uint32) (raftcluster.SlotLogStatus, error) {
	key := slotLogStatusKey{nodeID: nodeID, slotID: slotID}
	if err := f.slotLogStatusErr[key]; err != nil {
		return raftcluster.SlotLogStatus{}, err
	}
	if status, ok := f.slotLogStatus[key]; ok {
		return status, nil
	}
	return raftcluster.SlotLogStatus{}, nil
}

func (f fakeClusterReader) ListTasksStrict(context.Context) ([]controllermeta.ReconcileTask, error) {
	return append([]controllermeta.ReconcileTask(nil), f.tasks...), f.listTasksErr
}

func (f fakeClusterReader) GetReconcileTaskStrict(_ context.Context, slotID uint32) (controllermeta.ReconcileTask, error) {
	if f.getTaskErr != nil {
		return controllermeta.ReconcileTask{}, f.getTaskErr
	}
	if task, ok := f.taskBySlot[slotID]; ok {
		return task, nil
	}
	return controllermeta.ReconcileTask{}, controllermeta.ErrNotFound
}

func (f fakeClusterReader) ControllerLeaderID() uint64 {
	return f.controllerLeaderID
}

func (f fakeClusterReader) MarkNodeDraining(context.Context, uint64) error {
	return f.markNodeDrainingErr
}

func (f fakeClusterReader) ResumeNode(context.Context, uint64) error {
	return f.resumeNodeErr
}

func (f fakeClusterReader) TransferSlotLeader(context.Context, uint32, multiraft.NodeID) error {
	return f.transferSlotLeaderErr
}

func (f fakeClusterReader) RecoverSlotStrict(context.Context, uint32, raftcluster.RecoverStrategy) error {
	return f.recoverSlotStrictErr
}

func (f fakeClusterReader) ListActiveMigrationsStrict(context.Context) ([]raftcluster.HashSlotMigration, error) {
	return append([]raftcluster.HashSlotMigration(nil), f.activeMigrations...), f.listActiveMigrationsErr
}

func (f fakeClusterReader) GetMigrationStatus() []raftcluster.HashSlotMigration {
	return append([]raftcluster.HashSlotMigration(nil), f.migrationStatus...)
}

func (f fakeClusterReader) Rebalance(context.Context) ([]raftcluster.MigrationPlan, error) {
	return append([]raftcluster.MigrationPlan(nil), f.rebalancePlan...), f.rebalanceErr
}

func (f fakeClusterReader) AddSlot(context.Context) (multiraft.SlotID, error) {
	return 0, nil
}

func (f fakeClusterReader) RemoveSlot(context.Context, multiraft.SlotID) error {
	return nil
}

func (f fakeClusterReader) ListNodeOnboardingCandidates(context.Context) ([]raftcluster.NodeOnboardingCandidate, error) {
	return nil, nil
}

func (f fakeClusterReader) CreateNodeOnboardingPlan(context.Context, uint64, string) (controllermeta.NodeOnboardingJob, error) {
	return controllermeta.NodeOnboardingJob{}, nil
}

func (f fakeClusterReader) StartNodeOnboardingJob(context.Context, string) (controllermeta.NodeOnboardingJob, error) {
	return controllermeta.NodeOnboardingJob{}, nil
}

func (f fakeClusterReader) ListNodeOnboardingJobs(context.Context, int, string) ([]controllermeta.NodeOnboardingJob, string, bool, error) {
	return append([]controllermeta.NodeOnboardingJob(nil), f.onboardingJobs...), "", f.onboardingHasMore, f.listOnboardingJobsErr
}

func (f fakeClusterReader) GetNodeOnboardingJob(context.Context, string) (controllermeta.NodeOnboardingJob, error) {
	return controllermeta.NodeOnboardingJob{}, nil
}

func (f fakeClusterReader) RetryNodeOnboardingJob(context.Context, string) (controllermeta.NodeOnboardingJob, error) {
	return controllermeta.NodeOnboardingJob{}, nil
}

type nodeSummary struct {
	NodeID          uint64
	Status          string
	ControllerRole  string
	SlotCount       int
	LeaderSlotCount int
	IsLocal         bool
}

func summarizeNodes(nodes []Node) []nodeSummary {
	out := make([]nodeSummary, 0, len(nodes))
	for _, node := range nodes {
		out = append(out, nodeSummary{
			NodeID:          node.NodeID,
			Status:          node.Status,
			ControllerRole:  node.ControllerRole,
			SlotCount:       node.SlotCount,
			LeaderSlotCount: node.LeaderSlotCount,
			IsLocal:         node.IsLocal,
		})
	}
	return out
}
