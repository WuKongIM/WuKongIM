package management

import (
	"context"
	"testing"

	raftcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	"github.com/WuKongIM/WuKongIM/pkg/slot/multiraft"
	"github.com/stretchr/testify/require"
)

func TestAddSlotRejectsActiveMigrationsWithoutCallingOperator(t *testing.T) {
	cluster := &fakeSlotAddRemoveCluster{
		fakeClusterReader: fakeClusterReader{
			migrationStatus: []raftcluster.HashSlotMigration{{
				HashSlot: 10,
				Source:   1,
				Target:   2,
			}},
		},
		addSlotID: 3,
	}
	app := New(Options{Cluster: cluster})

	_, err := app.AddSlot(context.Background())

	require.ErrorIs(t, err, ErrSlotMigrationsInProgress)
	require.Zero(t, cluster.addSlotCalls)
}

func TestAddSlotCallsOperatorAndReloadsDetail(t *testing.T) {
	cluster := &fakeSlotAddRemoveCluster{
		fakeClusterReader: fakeClusterReader{
			getTaskErr: controllermeta.ErrNotFound,
		},
		addSlotID: 3,
	}
	app := New(Options{Cluster: cluster})

	got, err := app.AddSlot(context.Background())

	require.NoError(t, err)
	require.Equal(t, 1, cluster.addSlotCalls)
	require.Equal(t, uint32(3), got.SlotID)
	require.Equal(t, []uint64{1, 2, 3}, got.Assignment.DesiredPeers)
	require.Equal(t, "ready", got.State.Quorum)
}

func TestAddSlotPropagatesOperatorAndReloadErrors(t *testing.T) {
	operatorUnavailable := &fakeSlotAddRemoveCluster{
		addSlotErr: raftcluster.ErrNoLeader,
	}
	operatorApp := New(Options{Cluster: operatorUnavailable})
	_, err := operatorApp.AddSlot(context.Background())
	require.ErrorIs(t, err, raftcluster.ErrNoLeader)

	reloadUnavailable := &fakeSlotAddRemoveCluster{
		fakeClusterReader: fakeClusterReader{
			listSlotAssignmentsErr: raftcluster.ErrNoLeader,
		},
		addSlotID: 3,
	}
	reloadApp := New(Options{Cluster: reloadUnavailable})
	_, err = reloadApp.AddSlot(context.Background())
	require.ErrorIs(t, err, raftcluster.ErrNoLeader)
}

func TestAddSlotNormalizesOperatorMigrationRace(t *testing.T) {
	cluster := &fakeSlotAddRemoveCluster{
		addSlotErr: raftcluster.ErrInvalidConfig,
		migrationStatusAfterAddErr: []raftcluster.HashSlotMigration{{
			HashSlot: 10,
			Source:   1,
			Target:   2,
		}},
	}
	app := New(Options{Cluster: cluster})

	_, err := app.AddSlot(context.Background())

	require.ErrorIs(t, err, ErrSlotMigrationsInProgress)
	require.Equal(t, 1, cluster.addSlotCalls)
}

func TestRemoveSlotPrechecksNotFoundWithoutCallingOperator(t *testing.T) {
	cluster := &fakeSlotAddRemoveCluster{}
	app := New(Options{Cluster: cluster})

	_, err := app.RemoveSlot(context.Background(), 2)

	require.ErrorIs(t, err, controllermeta.ErrNotFound)
	require.Zero(t, cluster.removeSlotCalls)
}

func TestRemoveSlotRejectsActiveMigrationsWithoutCallingOperator(t *testing.T) {
	cluster := &fakeSlotAddRemoveCluster{
		fakeClusterReader: fakeClusterReader{
			assignments: []controllermeta.SlotAssignment{{
				SlotID:       2,
				DesiredPeers: []uint64{1, 2, 3},
			}},
			getTaskErr: controllermeta.ErrNotFound,
			migrationStatus: []raftcluster.HashSlotMigration{{
				HashSlot: 10,
				Source:   2,
				Target:   1,
			}},
		},
	}
	app := New(Options{Cluster: cluster})

	_, err := app.RemoveSlot(context.Background(), 2)

	require.ErrorIs(t, err, ErrSlotMigrationsInProgress)
	require.Zero(t, cluster.removeSlotCalls)
}

func TestRemoveSlotMapsClusterSlotNotFoundToControllerNotFound(t *testing.T) {
	cluster := &fakeSlotAddRemoveCluster{
		fakeClusterReader: fakeClusterReader{
			assignments: []controllermeta.SlotAssignment{{
				SlotID:       2,
				DesiredPeers: []uint64{1, 2, 3},
			}},
			getTaskErr: controllermeta.ErrNotFound,
		},
		removeSlotErr: raftcluster.ErrSlotNotFound,
	}
	app := New(Options{Cluster: cluster})

	_, err := app.RemoveSlot(context.Background(), 2)

	require.ErrorIs(t, err, controllermeta.ErrNotFound)
	require.Equal(t, 1, cluster.removeSlotCalls)
}

func TestRemoveSlotNormalizesOperatorMigrationRace(t *testing.T) {
	cluster := &fakeSlotAddRemoveCluster{
		fakeClusterReader: fakeClusterReader{
			assignments: []controllermeta.SlotAssignment{{
				SlotID:       2,
				DesiredPeers: []uint64{1, 2, 3},
			}},
			getTaskErr: controllermeta.ErrNotFound,
		},
		removeSlotErr: raftcluster.ErrInvalidConfig,
		migrationStatusAfterRemoveErr: []raftcluster.HashSlotMigration{{
			HashSlot: 10,
			Source:   2,
			Target:   1,
		}},
	}
	app := New(Options{Cluster: cluster})

	_, err := app.RemoveSlot(context.Background(), 2)

	require.ErrorIs(t, err, ErrSlotMigrationsInProgress)
	require.Equal(t, 1, cluster.removeSlotCalls)
}

func TestRemoveSlotPropagatesOperatorFailure(t *testing.T) {
	cluster := &fakeSlotAddRemoveCluster{
		fakeClusterReader: fakeClusterReader{
			assignments: []controllermeta.SlotAssignment{{
				SlotID:       2,
				DesiredPeers: []uint64{1, 2, 3},
			}},
			getTaskErr: controllermeta.ErrNotFound,
		},
		removeSlotErr: raftcluster.ErrNoLeader,
	}
	app := New(Options{Cluster: cluster})

	_, err := app.RemoveSlot(context.Background(), 2)

	require.ErrorIs(t, err, raftcluster.ErrNoLeader)
	require.Equal(t, 1, cluster.removeSlotCalls)
}

func TestRemoveSlotReturnsStartedResult(t *testing.T) {
	cluster := &fakeSlotAddRemoveCluster{
		fakeClusterReader: fakeClusterReader{
			assignments: []controllermeta.SlotAssignment{{
				SlotID:       2,
				DesiredPeers: []uint64{1, 2, 3},
			}},
			getTaskErr: controllermeta.ErrNotFound,
		},
	}
	app := New(Options{Cluster: cluster})

	got, err := app.RemoveSlot(context.Background(), 2)

	require.NoError(t, err)
	require.Equal(t, 1, cluster.removeSlotCalls)
	require.Equal(t, multiraft.SlotID(2), cluster.removeSlotID)
	require.Equal(t, uint32(2), got.SlotID)
	require.Equal(t, "removal_started", got.Result)
}

type fakeSlotAddRemoveCluster struct {
	fakeClusterReader
	addSlotCalls                  int
	addSlotID                     multiraft.SlotID
	addSlotErr                    error
	migrationStatusAfterAddErr    []raftcluster.HashSlotMigration
	removeSlotCalls               int
	removeSlotID                  multiraft.SlotID
	removeSlotErr                 error
	migrationStatusAfterRemoveErr []raftcluster.HashSlotMigration
}

func (f *fakeSlotAddRemoveCluster) AddSlot(context.Context) (multiraft.SlotID, error) {
	f.addSlotCalls++
	if f.addSlotErr != nil {
		f.migrationStatus = append([]raftcluster.HashSlotMigration(nil), f.migrationStatusAfterAddErr...)
		return 0, f.addSlotErr
	}
	f.assignments = []controllermeta.SlotAssignment{{
		SlotID:       uint32(f.addSlotID),
		DesiredPeers: []uint64{1, 2, 3},
	}}
	f.views = []controllermeta.SlotRuntimeView{{
		SlotID:       uint32(f.addSlotID),
		CurrentPeers: []uint64{1, 2, 3},
		LeaderID:     1,
		HasQuorum:    true,
	}}
	return f.addSlotID, nil
}

func (f *fakeSlotAddRemoveCluster) RemoveSlot(_ context.Context, slotID multiraft.SlotID) error {
	f.removeSlotCalls++
	f.removeSlotID = slotID
	if f.removeSlotErr != nil {
		f.migrationStatus = append([]raftcluster.HashSlotMigration(nil), f.migrationStatusAfterRemoveErr...)
	}
	return f.removeSlotErr
}
