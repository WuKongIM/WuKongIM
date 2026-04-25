package management

import (
	"context"
	"testing"

	raftcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/stretchr/testify/require"
)

func TestTransferSlotLeaderReturnsNotFoundWithoutCallingOperator(t *testing.T) {
	cluster := &fakeSlotOperatorCluster{}
	app := New(Options{
		LocalNodeID:       1,
		ControllerPeerIDs: []uint64{1},
		Cluster:           cluster,
	})

	_, err := app.TransferSlotLeader(context.Background(), 2, 3)

	require.ErrorIs(t, err, controllermeta.ErrNotFound)
	require.Zero(t, cluster.transferSlotLeaderCalls)
}

func TestTransferSlotLeaderRejectsTargetOutsideDesiredPeers(t *testing.T) {
	cluster := &fakeSlotOperatorCluster{
		fakeClusterReader: fakeClusterReader{
			assignments: []controllermeta.SlotAssignment{{
				SlotID:       2,
				DesiredPeers: []uint64{1, 2, 3},
			}},
			views: []controllermeta.SlotRuntimeView{{
				SlotID:       2,
				CurrentPeers: []uint64{1, 2, 3},
				LeaderID:     1,
				HasQuorum:    true,
			}},
			getTaskErr: controllermeta.ErrNotFound,
		},
	}
	app := New(Options{
		LocalNodeID:       1,
		ControllerPeerIDs: []uint64{1},
		Cluster:           cluster,
	})

	_, err := app.TransferSlotLeader(context.Background(), 2, 9)

	require.ErrorIs(t, err, ErrTargetNodeNotAssigned)
	require.Zero(t, cluster.transferSlotLeaderCalls)
}

func TestTransferSlotLeaderReturnsCurrentDetailWhenLeaderAlreadyMatches(t *testing.T) {
	cluster := &fakeSlotOperatorCluster{
		fakeClusterReader: fakeClusterReader{
			assignments: []controllermeta.SlotAssignment{{
				SlotID:       2,
				DesiredPeers: []uint64{1, 2, 3},
			}},
			views: []controllermeta.SlotRuntimeView{{
				SlotID:       2,
				CurrentPeers: []uint64{1, 2, 3},
				LeaderID:     3,
				HasQuorum:    true,
			}},
			getTaskErr: controllermeta.ErrNotFound,
		},
	}
	app := New(Options{
		LocalNodeID:       1,
		ControllerPeerIDs: []uint64{1},
		Cluster:           cluster,
	})

	got, err := app.TransferSlotLeader(context.Background(), 2, 3)

	require.NoError(t, err)
	require.Equal(t, uint32(2), got.SlotID)
	require.Equal(t, uint64(3), got.Runtime.LeaderID)
	require.Zero(t, cluster.transferSlotLeaderCalls)
}

func TestTransferSlotLeaderCallsOperatorAndReloadsDetail(t *testing.T) {
	cluster := &fakeSlotOperatorCluster{
		fakeClusterReader: fakeClusterReader{
			assignments: []controllermeta.SlotAssignment{{
				SlotID:       2,
				DesiredPeers: []uint64{1, 2, 3},
			}},
			views: []controllermeta.SlotRuntimeView{{
				SlotID:       2,
				CurrentPeers: []uint64{1, 2, 3},
				LeaderID:     1,
				HasQuorum:    true,
			}},
			getTaskErr: controllermeta.ErrNotFound,
		},
	}
	app := New(Options{
		LocalNodeID:       1,
		ControllerPeerIDs: []uint64{1},
		Cluster:           cluster,
	})

	got, err := app.TransferSlotLeader(context.Background(), 2, 3)

	require.NoError(t, err)
	require.Equal(t, 1, cluster.transferSlotLeaderCalls)
	require.Equal(t, uint32(2), cluster.transferSlotLeaderSlotID)
	require.Equal(t, multiraft.NodeID(3), cluster.transferSlotLeaderNodeID)
	require.Equal(t, uint64(3), got.Runtime.LeaderID)
}

func TestTransferSlotLeaderPropagatesStrictReadAndOperatorErrors(t *testing.T) {
	readUnavailable := &fakeSlotOperatorCluster{
		fakeClusterReader: fakeClusterReader{
			listSlotAssignmentsErr: raftcluster.ErrNoLeader,
		},
	}
	readApp := New(Options{
		LocalNodeID:       1,
		ControllerPeerIDs: []uint64{1},
		Cluster:           readUnavailable,
	})
	_, err := readApp.TransferSlotLeader(context.Background(), 2, 3)
	require.ErrorIs(t, err, raftcluster.ErrNoLeader)

	operatorUnavailable := &fakeSlotOperatorCluster{
		fakeClusterReader: fakeClusterReader{
			assignments: []controllermeta.SlotAssignment{{
				SlotID:       2,
				DesiredPeers: []uint64{1, 2, 3},
			}},
			views: []controllermeta.SlotRuntimeView{{
				SlotID:       2,
				CurrentPeers: []uint64{1, 2, 3},
				LeaderID:     1,
				HasQuorum:    true,
			}},
			getTaskErr: controllermeta.ErrNotFound,
		},
		transferSlotLeaderErr: raftcluster.ErrNotLeader,
	}
	operatorApp := New(Options{
		LocalNodeID:       1,
		ControllerPeerIDs: []uint64{1},
		Cluster:           operatorUnavailable,
	})
	_, err = operatorApp.TransferSlotLeader(context.Background(), 2, 3)
	require.ErrorIs(t, err, raftcluster.ErrNotLeader)
}

type fakeSlotOperatorCluster struct {
	fakeClusterReader
	transferSlotLeaderCalls  int
	transferSlotLeaderSlotID uint32
	transferSlotLeaderNodeID multiraft.NodeID
	transferSlotLeaderErr    error
}

func (f *fakeSlotOperatorCluster) TransferSlotLeader(_ context.Context, slotID uint32, nodeID multiraft.NodeID) error {
	f.transferSlotLeaderCalls++
	f.transferSlotLeaderSlotID = slotID
	f.transferSlotLeaderNodeID = nodeID
	if f.transferSlotLeaderErr != nil {
		return f.transferSlotLeaderErr
	}
	for i := range f.views {
		if f.views[i].SlotID == slotID {
			f.views[i].LeaderID = uint64(nodeID)
			return nil
		}
	}
	return controllermeta.ErrNotFound
}
