package management

import (
	"context"
	"testing"
	"time"

	raftcluster "github.com/WuKongIM/WuKongIM/pkg/legacy/cluster"
	controllermeta "github.com/WuKongIM/WuKongIM/pkg/legacy/controller/meta"
	"github.com/WuKongIM/WuKongIM/pkg/legacy/transport"
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
	}, summarizeNodes(got.Items))
	require.Equal(t, now.Add(-1*time.Second), got.Items[0].LastHeartbeatAt)
	require.Equal(t, 2, got.Items[1].CapacityWeight)
}

func TestListNodesReturnsLayeredInventoryFields(t *testing.T) {
	now := time.Unix(1714298400, 0).UTC()
	app := New(Options{
		LocalNodeID:       1,
		ControllerPeerIDs: []uint64{1, 2},
		SlotReplicaN:      3,
		Now:               func() time.Time { return now },
		Cluster: fakeClusterReader{
			controllerLeaderID: 1,
			nodes: []controllermeta.ClusterNode{{
				NodeID:          1,
				Name:            "node-1",
				Addr:            "10.0.0.1:7000",
				Role:            controllermeta.NodeRoleData,
				JoinState:       controllermeta.NodeJoinStateActive,
				Status:          controllermeta.NodeStatusAlive,
				LastHeartbeatAt: now.Add(-2 * time.Second),
				CapacityWeight:  2,
			}},
			views: []controllermeta.SlotRuntimeView{{
				SlotID:       7,
				CurrentPeers: []uint64{1, 2, 3},
				LeaderID:     1,
				HasQuorum:    true,
			}},
		},
		RuntimeSummary: scaleInRuntimeSummaryReader{summary: NodeRuntimeSummary{
			NodeID:               1,
			ActiveOnline:         4,
			GatewaySessions:      5,
			AcceptingNewSessions: true,
		}},
	})

	got, err := app.ListNodes(context.Background())
	require.NoError(t, err)
	require.Equal(t, now, got.GeneratedAt)
	require.Equal(t, uint64(1), got.ControllerLeaderID)
	require.Len(t, got.Items, 1)
	node := got.Items[0]
	require.Equal(t, "node-1", node.Name)
	require.Equal(t, "data", node.Membership.Role)
	require.Equal(t, "active", node.Membership.JoinState)
	require.True(t, node.Membership.Schedulable)
	require.Equal(t, "alive", node.Health.Status)
	require.Equal(t, now.Add(-2*time.Second), node.Health.LastHeartbeatAt)
	require.Equal(t, "leader", node.Controller.Role)
	require.True(t, node.Controller.Voter)
	require.Equal(t, uint64(1), node.Controller.LeaderID)
	require.Equal(t, 1, node.Slots.ReplicaCount)
	require.Equal(t, 1, node.Slots.LeaderCount)
	require.Equal(t, 0, node.Slots.FollowerCount)
	require.Equal(t, 0, node.Slots.QuorumLostCount)
	require.Equal(t, 0, node.Slots.UnreportedCount)
	require.Equal(t, 4, node.Runtime.ActiveOnline)
	require.Equal(t, 5, node.Runtime.GatewaySessions)
	require.True(t, node.Runtime.AcceptingNewSessions)
	require.True(t, node.Actions.CanDrain)
	require.False(t, node.Actions.CanResume)
}

func TestListNodesDoesNotReadDistributedLogStatus(t *testing.T) {
	cluster := &fakeNodeInventoryCluster{fakeClusterReader: fakeClusterReader{
		controllerLeaderID: 1,
		nodes: []controllermeta.ClusterNode{{
			NodeID:         1,
			Addr:           "127.0.0.1:7001",
			Role:           controllermeta.NodeRoleData,
			JoinState:      controllermeta.NodeJoinStateActive,
			Status:         controllermeta.NodeStatusAlive,
			CapacityWeight: 1,
		}},
		views: []controllermeta.SlotRuntimeView{{
			SlotID:       1,
			CurrentPeers: []uint64{1},
			LeaderID:     1,
			HasQuorum:    true,
		}},
	}}
	app := New(Options{Cluster: cluster, ControllerPeerIDs: []uint64{1}, Now: time.Now})

	_, err := app.ListNodes(context.Background())
	require.NoError(t, err)
	require.Zero(t, cluster.logStatusCalls)
}

func TestListNodesIncludesLocalControllerRaftSummary(t *testing.T) {
	app := New(Options{
		LocalNodeID:       1,
		ControllerPeerIDs: []uint64{1, 2},
		Cluster: fakeClusterReader{
			controllerLeaderID: 1,
			nodes: []controllermeta.ClusterNode{{
				NodeID:    1,
				Role:      controllermeta.NodeRoleControllerVoter,
				JoinState: controllermeta.NodeJoinStateActive,
				Status:    controllermeta.NodeStatusAlive,
			}},
			controllerRaftStatus: map[uint64]raftcluster.ControllerRaftStatus{
				1: {NodeID: 1, Role: "leader", FirstIndex: 10, AppliedIndex: 20, SnapshotIndex: 9},
			},
		},
	})

	got, err := app.ListNodes(context.Background())
	require.NoError(t, err)
	require.Len(t, got.Items, 1)
	require.Equal(t, ControllerRaftHealthHealthy, got.Items[0].Controller.RaftHealth)
	require.Equal(t, uint64(10), got.Items[0].Controller.FirstIndex)
	require.Equal(t, uint64(20), got.Items[0].Controller.AppliedIndex)
	require.Equal(t, uint64(9), got.Items[0].Controller.SnapshotIndex)
}

func TestListNodesDoesNotFanOutControllerRaftStatus(t *testing.T) {
	cluster := &fakeNodeInventoryCluster{fakeClusterReader: fakeClusterReader{
		controllerLeaderID: 2,
		nodes: []controllermeta.ClusterNode{{
			NodeID:    1,
			Role:      controllermeta.NodeRoleControllerVoter,
			JoinState: controllermeta.NodeJoinStateActive,
			Status:    controllermeta.NodeStatusAlive,
		}, {
			NodeID:    2,
			Role:      controllermeta.NodeRoleControllerVoter,
			JoinState: controllermeta.NodeJoinStateActive,
			Status:    controllermeta.NodeStatusAlive,
		}},
		controllerRaftStatus: map[uint64]raftcluster.ControllerRaftStatus{
			1: {NodeID: 1, Role: "follower"},
			2: {NodeID: 2, Role: "leader"},
		},
	}}
	app := New(Options{LocalNodeID: 1, ControllerPeerIDs: []uint64{1, 2}, Cluster: cluster})

	got, err := app.ListNodes(context.Background())
	require.NoError(t, err)
	require.Len(t, got.Items, 2)
	require.Equal(t, 1, cluster.controllerRaftStatusCalls)
	require.Equal(t, ControllerRaftHealthHealthy, got.Items[0].Controller.RaftHealth)
	require.Equal(t, ControllerRaftHealthUnknown, got.Items[1].Controller.RaftHealth)
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
	}, summarizeNodes(got.Items))
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
			Membership: NodeMembership{
				Role:      "unknown",
				JoinState: "unknown",
			},
			Health: NodeHealth{
				Status:          "draining",
				LastHeartbeatAt: now.Add(-2 * time.Second),
			},
			Controller: NodeController{
				Role:       "follower",
				Voter:      true,
				LeaderID:   1,
				RaftHealth: ControllerRaftHealthUnknown,
			},
			Slots: NodeSlotSummary{
				ReplicaCount:  3,
				LeaderCount:   2,
				FollowerCount: 1,
			},
			Runtime: NodeRuntimeSummary{
				NodeID:  2,
				Unknown: true,
			},
			Actions: NodeActions{
				CanResume: true,
			},
		},
		Slots: NodeSlots{
			HostedIDs: []uint32{2, 4, 7},
			LeaderIDs: []uint32{2, 4},
		},
	}, got)
}

func TestGetNodeIncludesLocalControllerRaftSummary(t *testing.T) {
	cluster := &fakeNodeInventoryCluster{fakeClusterReader: fakeClusterReader{
		controllerLeaderID: 1,
		nodes: []controllermeta.ClusterNode{{
			NodeID:    1,
			Role:      controllermeta.NodeRoleControllerVoter,
			JoinState: controllermeta.NodeJoinStateActive,
			Status:    controllermeta.NodeStatusAlive,
		}},
		controllerRaftStatus: map[uint64]raftcluster.ControllerRaftStatus{
			1: {NodeID: 1, Role: "leader", FirstIndex: 10, AppliedIndex: 20, SnapshotIndex: 9},
		},
	}}
	app := New(Options{LocalNodeID: 1, ControllerPeerIDs: []uint64{1}, Cluster: cluster})

	got, err := app.GetNode(context.Background(), 1)
	require.NoError(t, err)
	require.Equal(t, 1, cluster.controllerRaftStatusCalls)
	require.Equal(t, ControllerRaftHealthHealthy, got.Controller.RaftHealth)
	require.Equal(t, uint64(10), got.Controller.FirstIndex)
	require.Equal(t, uint64(20), got.Controller.AppliedIndex)
	require.Equal(t, uint64(9), got.Controller.SnapshotIndex)
}

func TestGetNodeDoesNotFanOutControllerRaftStatus(t *testing.T) {
	cluster := &fakeNodeInventoryCluster{fakeClusterReader: fakeClusterReader{
		controllerLeaderID: 2,
		nodes: []controllermeta.ClusterNode{{
			NodeID:    1,
			Role:      controllermeta.NodeRoleControllerVoter,
			JoinState: controllermeta.NodeJoinStateActive,
			Status:    controllermeta.NodeStatusAlive,
		}, {
			NodeID:    2,
			Role:      controllermeta.NodeRoleControllerVoter,
			JoinState: controllermeta.NodeJoinStateActive,
			Status:    controllermeta.NodeStatusAlive,
		}},
		controllerRaftStatus: map[uint64]raftcluster.ControllerRaftStatus{
			1: {NodeID: 1, Role: "follower"},
			2: {NodeID: 2, Role: "leader", FirstIndex: 10},
		},
	}}
	app := New(Options{LocalNodeID: 1, ControllerPeerIDs: []uint64{1, 2}, Cluster: cluster})

	got, err := app.GetNode(context.Background(), 2)
	require.NoError(t, err)
	require.Zero(t, cluster.controllerRaftStatusCalls)
	require.Equal(t, ControllerRaftHealthUnknown, got.Controller.RaftHealth)
	require.Zero(t, got.Controller.FirstIndex)
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
	hashSlotTable               *raftcluster.HashSlotTable
	nodes                       []controllermeta.ClusterNode
	listNodesErr                error
	assignments                 []controllermeta.SlotAssignment
	listSlotAssignmentsErr      error
	views                       []controllermeta.SlotRuntimeView
	listObservedRuntimeViewsErr error
	slotLogStatus               map[slotLogStatusKey]raftcluster.SlotLogStatus
	slotLogStatusErr            map[slotLogStatusKey]error
	slotLogEntries              map[slotLogEntriesKey]raftcluster.SlotLogEntries
	slotLogEntriesErr           map[slotLogEntriesKey]error
	controllerLogEntries        map[uint64]raftcluster.ControllerLogEntries
	controllerLogEntriesErr     map[uint64]error
	controllerRaftStatus        map[uint64]raftcluster.ControllerRaftStatus
	controllerRaftStatusErr     map[uint64]error
	controllerRaftCompactions   map[uint64]raftcluster.ControllerRaftCompactionResult
	controllerRaftCompactErr    map[uint64]error
	slotRaftCompactions         map[slotRaftCompactionKey]raftcluster.SlotRaftCompactionResult
	slotRaftCompactionErrs      map[slotRaftCompactionKey]error
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
	transportStats              []transport.PoolPeerStats
}

type slotLogStatusKey struct {
	nodeID uint64
	slotID uint32
}

type slotLogEntriesKey struct {
	nodeID uint64
	slotID uint32
}

type slotRaftCompactionKey struct {
	nodeID uint64
	slotID uint32
}

type fakeNodeInventoryCluster struct {
	fakeClusterReader
	logStatusCalls            int
	controllerRaftStatusCalls int
}

func (f *fakeNodeInventoryCluster) SlotLogStatusOnNode(context.Context, uint64, uint32) (raftcluster.SlotLogStatus, error) {
	f.logStatusCalls++
	return raftcluster.SlotLogStatus{}, nil
}

func (f *fakeNodeInventoryCluster) ControllerRaftStatusOnNode(ctx context.Context, nodeID uint64) (raftcluster.ControllerRaftStatus, error) {
	f.controllerRaftStatusCalls++
	return f.fakeClusterReader.ControllerRaftStatusOnNode(ctx, nodeID)
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

func (f fakeClusterReader) GetHashSlotTable() *raftcluster.HashSlotTable {
	if f.hashSlotTable == nil {
		return nil
	}
	return f.hashSlotTable.Clone()
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

func (f fakeClusterReader) SlotLogEntriesOnNode(_ context.Context, nodeID uint64, slotID uint32, _ raftcluster.SlotLogEntriesOptions) (raftcluster.SlotLogEntries, error) {
	key := slotLogEntriesKey{nodeID: nodeID, slotID: slotID}
	if err := f.slotLogEntriesErr[key]; err != nil {
		return raftcluster.SlotLogEntries{}, err
	}
	if page, ok := f.slotLogEntries[key]; ok {
		return page, nil
	}
	return raftcluster.SlotLogEntries{}, nil
}

func (f fakeClusterReader) ControllerLogEntriesOnNode(_ context.Context, nodeID uint64, _ raftcluster.ControllerLogEntriesOptions) (raftcluster.ControllerLogEntries, error) {
	if err := f.controllerLogEntriesErr[nodeID]; err != nil {
		return raftcluster.ControllerLogEntries{}, err
	}
	if page, ok := f.controllerLogEntries[nodeID]; ok {
		return page, nil
	}
	return raftcluster.ControllerLogEntries{}, nil
}

func (f fakeClusterReader) ControllerRaftStatusOnNode(_ context.Context, nodeID uint64) (raftcluster.ControllerRaftStatus, error) {
	if err := f.controllerRaftStatusErr[nodeID]; err != nil {
		return raftcluster.ControllerRaftStatus{}, err
	}
	if status, ok := f.controllerRaftStatus[nodeID]; ok {
		return status, nil
	}
	return raftcluster.ControllerRaftStatus{}, nil
}

func (f fakeClusterReader) CompactControllerRaftLogOnNode(_ context.Context, nodeID uint64) (raftcluster.ControllerRaftCompactionResult, error) {
	if err := f.controllerRaftCompactErr[nodeID]; err != nil {
		return raftcluster.ControllerRaftCompactionResult{}, err
	}
	if result, ok := f.controllerRaftCompactions[nodeID]; ok {
		return result, nil
	}
	return raftcluster.ControllerRaftCompactionResult{NodeID: nodeID}, nil
}

func (f fakeClusterReader) CompactSlotRaftLogOnNode(_ context.Context, nodeID uint64, slotID uint32) (raftcluster.SlotRaftCompactionResult, error) {
	key := slotRaftCompactionKey{nodeID: nodeID, slotID: slotID}
	if err := f.slotRaftCompactionErrs[key]; err != nil {
		return raftcluster.SlotRaftCompactionResult{}, err
	}
	if result, ok := f.slotRaftCompactions[key]; ok {
		return result, nil
	}
	return raftcluster.SlotRaftCompactionResult{NodeID: nodeID, SlotID: slotID}, nil
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

func (f fakeClusterReader) TransportPoolStats() []transport.PoolPeerStats {
	return append([]transport.PoolPeerStats(nil), f.transportStats...)
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
