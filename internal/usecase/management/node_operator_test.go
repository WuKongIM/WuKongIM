package management

import (
	"context"
	"testing"

	raftcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/stretchr/testify/require"
)

func TestMarkNodeDrainingReturnsNotFoundWithoutCallingOperator(t *testing.T) {
	cluster := &fakeNodeOperatorCluster{
		nodes: []controllermeta.ClusterNode{
			{NodeID: 1, Status: controllermeta.NodeStatusAlive},
		},
	}
	app := New(Options{
		LocalNodeID:       1,
		ControllerPeerIDs: []uint64{1},
		Cluster:           cluster,
	})

	_, err := app.MarkNodeDraining(context.Background(), 2)

	require.ErrorIs(t, err, controllermeta.ErrNotFound)
	require.Zero(t, cluster.markNodeDrainingCalls)
}

func TestMarkNodeDrainingReturnsCurrentDetailWhenAlreadyDraining(t *testing.T) {
	cluster := &fakeNodeOperatorCluster{
		nodes: []controllermeta.ClusterNode{
			{NodeID: 2, Addr: "127.0.0.1:7002", Status: controllermeta.NodeStatusDraining, CapacityWeight: 2},
		},
		views: []controllermeta.SlotRuntimeView{
			{SlotID: 2, CurrentPeers: []uint64{2}, LeaderID: 2, HasQuorum: true},
		},
	}
	app := New(Options{
		LocalNodeID:       2,
		ControllerPeerIDs: []uint64{1, 2},
		Cluster:           cluster,
	})

	got, err := app.MarkNodeDraining(context.Background(), 2)

	require.NoError(t, err)
	require.Equal(t, "draining", got.Status)
	require.Zero(t, cluster.markNodeDrainingCalls)
}

func TestMarkNodeDrainingCallsOperatorAndReloadsNode(t *testing.T) {
	cluster := &fakeNodeOperatorCluster{
		nodes: []controllermeta.ClusterNode{
			{NodeID: 2, Addr: "127.0.0.1:7002", Status: controllermeta.NodeStatusAlive, CapacityWeight: 2},
		},
		views: []controllermeta.SlotRuntimeView{
			{SlotID: 2, CurrentPeers: []uint64{2}, LeaderID: 2, HasQuorum: true},
		},
	}
	app := New(Options{
		LocalNodeID:       2,
		ControllerPeerIDs: []uint64{1, 2},
		Cluster:           cluster,
	})

	got, err := app.MarkNodeDraining(context.Background(), 2)

	require.NoError(t, err)
	require.Equal(t, uint64(2), cluster.markNodeDrainingCalledWith)
	require.Equal(t, 1, cluster.markNodeDrainingCalls)
	require.Equal(t, "draining", got.Status)
}

func TestResumeNodeReturnsCurrentDetailWhenAlreadyAlive(t *testing.T) {
	cluster := &fakeNodeOperatorCluster{
		nodes: []controllermeta.ClusterNode{
			{NodeID: 2, Addr: "127.0.0.1:7002", Status: controllermeta.NodeStatusAlive, CapacityWeight: 2},
		},
	}
	app := New(Options{
		LocalNodeID:       2,
		ControllerPeerIDs: []uint64{1, 2},
		Cluster:           cluster,
	})

	got, err := app.ResumeNode(context.Background(), 2)

	require.NoError(t, err)
	require.Equal(t, "alive", got.Status)
	require.Zero(t, cluster.resumeNodeCalls)
}

func TestResumeNodeCallsOperatorAndReloadsNode(t *testing.T) {
	cluster := &fakeNodeOperatorCluster{
		nodes: []controllermeta.ClusterNode{
			{NodeID: 2, Addr: "127.0.0.1:7002", Status: controllermeta.NodeStatusDraining, CapacityWeight: 2},
		},
	}
	app := New(Options{
		LocalNodeID:       2,
		ControllerPeerIDs: []uint64{1, 2},
		Cluster:           cluster,
	})

	got, err := app.ResumeNode(context.Background(), 2)

	require.NoError(t, err)
	require.Equal(t, uint64(2), cluster.resumeNodeCalledWith)
	require.Equal(t, 1, cluster.resumeNodeCalls)
	require.Equal(t, "alive", got.Status)
}

func TestNodeOperatorsPropagateStrictReadAndOperatorErrors(t *testing.T) {
	readUnavailable := &fakeNodeOperatorCluster{listNodesErr: raftcluster.ErrNoLeader}
	readApp := New(Options{
		LocalNodeID:       1,
		ControllerPeerIDs: []uint64{1},
		Cluster:           readUnavailable,
	})
	_, err := readApp.MarkNodeDraining(context.Background(), 2)
	require.ErrorIs(t, err, raftcluster.ErrNoLeader)

	operatorUnavailable := &fakeNodeOperatorCluster{
		nodes: []controllermeta.ClusterNode{
			{NodeID: 2, Addr: "127.0.0.1:7002", Status: controllermeta.NodeStatusAlive},
		},
		markNodeDrainingErr: raftcluster.ErrNotStarted,
	}
	operatorApp := New(Options{
		LocalNodeID:       1,
		ControllerPeerIDs: []uint64{1},
		Cluster:           operatorUnavailable,
	})
	_, err = operatorApp.MarkNodeDraining(context.Background(), 2)
	require.ErrorIs(t, err, raftcluster.ErrNotStarted)

	resumeUnavailable := &fakeNodeOperatorCluster{
		nodes: []controllermeta.ClusterNode{
			{NodeID: 2, Addr: "127.0.0.1:7002", Status: controllermeta.NodeStatusDraining},
		},
		resumeNodeErr: raftcluster.ErrNotLeader,
	}
	resumeApp := New(Options{
		LocalNodeID:       1,
		ControllerPeerIDs: []uint64{1},
		Cluster:           resumeUnavailable,
	})
	_, err = resumeApp.ResumeNode(context.Background(), 2)
	require.ErrorIs(t, err, raftcluster.ErrNotLeader)
}

type fakeNodeOperatorCluster struct {
	slotIDs                     []multiraft.SlotID
	slotForKey                  map[string]multiraft.SlotID
	hashSlotForKey              map[string]uint16
	controllerLeaderID          uint64
	nodes                       []controllermeta.ClusterNode
	listNodesErr                error
	assignments                 []controllermeta.SlotAssignment
	listSlotAssignmentsErr      error
	views                       []controllermeta.SlotRuntimeView
	listObservedRuntimeViewsErr error
	tasks                       []controllermeta.ReconcileTask
	listTasksErr                error
	taskBySlot                  map[uint32]controllermeta.ReconcileTask
	getTaskErr                  error
	markNodeDrainingCalledWith  uint64
	markNodeDrainingCalls       int
	markNodeDrainingErr         error
	resumeNodeCalledWith        uint64
	resumeNodeCalls             int
	resumeNodeErr               error
}

func (f *fakeNodeOperatorCluster) SlotIDs() []multiraft.SlotID {
	return append([]multiraft.SlotID(nil), f.slotIDs...)
}

func (f *fakeNodeOperatorCluster) SlotForKey(key string) multiraft.SlotID {
	return f.slotForKey[key]
}

func (f *fakeNodeOperatorCluster) HashSlotForKey(key string) uint16 {
	return f.hashSlotForKey[key]
}

func (f *fakeNodeOperatorCluster) ListNodesStrict(context.Context) ([]controllermeta.ClusterNode, error) {
	return append([]controllermeta.ClusterNode(nil), f.nodes...), f.listNodesErr
}

func (f *fakeNodeOperatorCluster) ListSlotAssignmentsStrict(context.Context) ([]controllermeta.SlotAssignment, error) {
	return append([]controllermeta.SlotAssignment(nil), f.assignments...), f.listSlotAssignmentsErr
}

func (f *fakeNodeOperatorCluster) ListObservedRuntimeViewsStrict(context.Context) ([]controllermeta.SlotRuntimeView, error) {
	return append([]controllermeta.SlotRuntimeView(nil), f.views...), f.listObservedRuntimeViewsErr
}

func (f *fakeNodeOperatorCluster) ListTasksStrict(context.Context) ([]controllermeta.ReconcileTask, error) {
	return append([]controllermeta.ReconcileTask(nil), f.tasks...), f.listTasksErr
}

func (f *fakeNodeOperatorCluster) GetReconcileTaskStrict(_ context.Context, slotID uint32) (controllermeta.ReconcileTask, error) {
	if f.getTaskErr != nil {
		return controllermeta.ReconcileTask{}, f.getTaskErr
	}
	if task, ok := f.taskBySlot[slotID]; ok {
		return task, nil
	}
	return controllermeta.ReconcileTask{}, controllermeta.ErrNotFound
}

func (f *fakeNodeOperatorCluster) ControllerLeaderID() uint64 {
	return f.controllerLeaderID
}

func (f *fakeNodeOperatorCluster) MarkNodeDraining(_ context.Context, nodeID uint64) error {
	f.markNodeDrainingCalledWith = nodeID
	f.markNodeDrainingCalls++
	if f.markNodeDrainingErr != nil {
		return f.markNodeDrainingErr
	}
	for i := range f.nodes {
		if f.nodes[i].NodeID == nodeID {
			f.nodes[i].Status = controllermeta.NodeStatusDraining
			return nil
		}
	}
	return controllermeta.ErrNotFound
}

func (f *fakeNodeOperatorCluster) ResumeNode(_ context.Context, nodeID uint64) error {
	f.resumeNodeCalledWith = nodeID
	f.resumeNodeCalls++
	if f.resumeNodeErr != nil {
		return f.resumeNodeErr
	}
	for i := range f.nodes {
		if f.nodes[i].NodeID == nodeID {
			f.nodes[i].Status = controllermeta.NodeStatusAlive
			return nil
		}
	}
	return controllermeta.ErrNotFound
}

func (f *fakeNodeOperatorCluster) TransferSlotLeader(context.Context, uint32, multiraft.NodeID) error {
	return nil
}

func (f *fakeNodeOperatorCluster) RecoverSlotStrict(context.Context, uint32, raftcluster.RecoverStrategy) error {
	return nil
}

func (f *fakeNodeOperatorCluster) GetMigrationStatus() []raftcluster.HashSlotMigration {
	return nil
}

func (f *fakeNodeOperatorCluster) Rebalance(context.Context) ([]raftcluster.MigrationPlan, error) {
	return nil, nil
}
