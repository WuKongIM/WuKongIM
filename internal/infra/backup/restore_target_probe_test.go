package backup_test

import (
	"context"
	"testing"

	backupinfra "github.com/WuKongIM/WuKongIM/internal/infra/backup"
	clusterpkg "github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
	"github.com/stretchr/testify/require"
)

func TestClusterRestoreTargetProbeRequiresEveryCurrentNodeEmpty(t *testing.T) {
	node := &fakeRestoreTargetNode{
		nodeID: 1,
		snapshot: control.Snapshot{
			ClusterID: "cluster-b", HashSlots: control.HashSlotTable{Count: 256},
			Nodes: []control.Node{
				{NodeID: 1, JoinState: control.NodeJoinStateActive},
				{NodeID: 2, JoinState: control.NodeJoinStateActive},
				{NodeID: 3, JoinState: control.NodeJoinStateRemoved},
			},
		},
		local: clusterpkg.RestoreTargetLocalState{NodeID: 1, Empty: true, MetadataEmpty: true, MessagesEmpty: true},
	}
	remote := &fakeRestoreTargetRemote{states: map[uint64]clusterpkg.RestoreTargetLocalState{
		2: {NodeID: 2, Empty: true, MetadataEmpty: true, MessagesEmpty: true},
	}}
	probe, err := backupinfra.NewClusterRestoreTargetProbe(backupinfra.ClusterRestoreTargetProbeOptions{
		Node: node, Remote: remote, ClusterID: "cluster-b", Generation: "generation-2", HashSlotCount: 256,
	})
	require.NoError(t, err)

	state, err := probe.InspectRestoreTarget(context.Background())
	require.NoError(t, err)
	require.Equal(t, backupinfra.RestoreTargetState{ClusterID: "cluster-b", Generation: "generation-2", HashSlotCount: 256, Empty: true}, state)
	require.Equal(t, []uint64{2}, remote.calls)

	remote.states[2] = clusterpkg.RestoreTargetLocalState{NodeID: 2, Empty: false, MetadataEmpty: false, MessagesEmpty: true}
	state, err = probe.InspectRestoreTarget(context.Background())
	require.NoError(t, err)
	require.False(t, state.Empty)
}

type fakeRestoreTargetNode struct {
	nodeID   uint64
	snapshot control.Snapshot
	local    clusterpkg.RestoreTargetLocalState
}

func (f *fakeRestoreTargetNode) NodeID() uint64 { return f.nodeID }

func (f *fakeRestoreTargetNode) LocalControlSnapshot(context.Context) (control.Snapshot, error) {
	return f.snapshot, nil
}

func (f *fakeRestoreTargetNode) InspectLocalRestoreTarget(context.Context) (clusterpkg.RestoreTargetLocalState, error) {
	return f.local, nil
}

type fakeRestoreTargetRemote struct {
	states map[uint64]clusterpkg.RestoreTargetLocalState
	calls  []uint64
}

func (f *fakeRestoreTargetRemote) InspectBackupRestoreTarget(_ context.Context, nodeID uint64) (clusterpkg.RestoreTargetLocalState, error) {
	f.calls = append(f.calls, nodeID)
	return f.states[nodeID], nil
}
