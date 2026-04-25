package management

import (
	"context"
	"testing"
	"time"

	raftcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	"github.com/stretchr/testify/require"
)

func TestRecoverSlotRejectsUnsupportedStrategy(t *testing.T) {
	app := New(Options{
		Cluster: &fakeSlotRecoverRebalanceCluster{},
	})

	_, err := app.RecoverSlot(context.Background(), 2, SlotRecoverStrategy("unknown"))

	require.ErrorIs(t, err, ErrUnsupportedRecoverStrategy)
}

func TestRecoverSlotReturnsNotFoundBeforeCallingOperator(t *testing.T) {
	cluster := &fakeSlotRecoverRebalanceCluster{}
	app := New(Options{
		Cluster: cluster,
	})

	_, err := app.RecoverSlot(context.Background(), 2, SlotRecoverStrategyLatestLiveReplica)

	require.ErrorIs(t, err, controllermeta.ErrNotFound)
	require.Zero(t, cluster.recoverSlotStrictCalls)
}

func TestRecoverSlotReturnsOutcomeAndReloadedSlot(t *testing.T) {
	now := time.Unix(1713686400, 0).UTC()
	cluster := &fakeSlotRecoverRebalanceCluster{
		fakeClusterReader: fakeClusterReader{
			assignments: []controllermeta.SlotAssignment{{
				SlotID:       2,
				DesiredPeers: []uint64{1, 2, 3},
			}},
			views: []controllermeta.SlotRuntimeView{{
				SlotID:              2,
				CurrentPeers:        []uint64{1, 2, 3},
				LeaderID:            1,
				HealthyVoters:       3,
				HasQuorum:           true,
				ObservedConfigEpoch: 8,
				LastReportAt:        now,
			}},
			getTaskErr: controllermeta.ErrNotFound,
		},
	}
	app := New(Options{
		Cluster: cluster,
	})

	got, err := app.RecoverSlot(context.Background(), 2, SlotRecoverStrategyLatestLiveReplica)

	require.NoError(t, err)
	require.Equal(t, "latest_live_replica", got.Strategy)
	require.Equal(t, "quorum_reachable", got.Result)
	require.Equal(t, uint32(2), got.Slot.SlotID)
	require.Equal(t, 1, cluster.recoverSlotStrictCalls)
	require.GreaterOrEqual(t, cluster.listSlotAssignmentsStrictCalls, 2)
}

func TestRecoverSlotPropagatesManualRecoveryRequired(t *testing.T) {
	cluster := &fakeSlotRecoverRebalanceCluster{
		fakeClusterReader: fakeClusterReader{
			assignments: []controllermeta.SlotAssignment{{
				SlotID:       2,
				DesiredPeers: []uint64{1, 2, 3},
			}},
			getTaskErr: controllermeta.ErrNotFound,
		},
		recoverSlotStrictErr: raftcluster.ErrManualRecoveryRequired,
	}
	app := New(Options{
		Cluster: cluster,
	})

	_, err := app.RecoverSlot(context.Background(), 2, SlotRecoverStrategyLatestLiveReplica)

	require.ErrorIs(t, err, raftcluster.ErrManualRecoveryRequired)
}

func TestRebalanceSlotsRejectsWhenMigrationsAlreadyInProgress(t *testing.T) {
	cluster := &fakeSlotRecoverRebalanceCluster{
		migrationStatus: []raftcluster.HashSlotMigration{{
			HashSlot: 1,
		}},
	}
	app := New(Options{
		Cluster: cluster,
	})

	_, err := app.RebalanceSlots(context.Background())

	require.ErrorIs(t, err, ErrSlotMigrationsInProgress)
	require.Equal(t, 1, cluster.listSlotAssignmentsStrictCalls)
	require.Zero(t, cluster.rebalanceCalls)
}

func TestRebalanceSlotsReturnsEmptyPlan(t *testing.T) {
	cluster := &fakeSlotRecoverRebalanceCluster{}
	app := New(Options{
		Cluster: cluster,
	})

	got, err := app.RebalanceSlots(context.Background())

	require.NoError(t, err)
	require.Equal(t, 0, got.Total)
	require.Empty(t, got.Items)
	require.Equal(t, 1, cluster.rebalanceCalls)
}

func TestRebalanceSlotsMapsMigrationPlanItems(t *testing.T) {
	cluster := &fakeSlotRecoverRebalanceCluster{
		rebalancePlan: []raftcluster.MigrationPlan{
			{HashSlot: 4, From: 1, To: 2},
			{HashSlot: 5, From: 1, To: 3},
		},
	}
	app := New(Options{
		Cluster: cluster,
	})

	got, err := app.RebalanceSlots(context.Background())

	require.NoError(t, err)
	require.Equal(t, SlotRebalanceResult{
		Total: 2,
		Items: []SlotRebalancePlanItem{
			{HashSlot: 4, FromSlotID: 1, ToSlotID: 2},
			{HashSlot: 5, FromSlotID: 1, ToSlotID: 3},
		},
	}, got)
}

func TestRebalanceSlotsPropagatesStrictSyncAndOperatorErrors(t *testing.T) {
	syncErrCluster := &fakeSlotRecoverRebalanceCluster{
		fakeClusterReader: fakeClusterReader{
			listSlotAssignmentsErr: raftcluster.ErrNoLeader,
		},
	}
	syncErrApp := New(Options{
		Cluster: syncErrCluster,
	})
	_, err := syncErrApp.RebalanceSlots(context.Background())
	require.ErrorIs(t, err, raftcluster.ErrNoLeader)

	rebalanceErrCluster := &fakeSlotRecoverRebalanceCluster{
		rebalanceErr: raftcluster.ErrNotStarted,
	}
	rebalanceErrApp := New(Options{
		Cluster: rebalanceErrCluster,
	})
	_, err = rebalanceErrApp.RebalanceSlots(context.Background())
	require.ErrorIs(t, err, raftcluster.ErrNotStarted)
}

type fakeSlotRecoverRebalanceCluster struct {
	fakeClusterReader
	listSlotAssignmentsStrictCalls int
	recoverSlotStrictErr           error
	recoverSlotStrictCalls         int
	recoverSlotStrictSlotID        uint32
	recoverSlotStrictStrategy      raftcluster.RecoverStrategy
	migrationStatus                []raftcluster.HashSlotMigration
	rebalancePlan                  []raftcluster.MigrationPlan
	rebalanceErr                   error
	rebalanceCalls                 int
}

func (f *fakeSlotRecoverRebalanceCluster) ListSlotAssignmentsStrict(ctx context.Context) ([]controllermeta.SlotAssignment, error) {
	f.listSlotAssignmentsStrictCalls++
	return f.fakeClusterReader.ListSlotAssignmentsStrict(ctx)
}

func (f *fakeSlotRecoverRebalanceCluster) RecoverSlotStrict(_ context.Context, slotID uint32, strategy raftcluster.RecoverStrategy) error {
	f.recoverSlotStrictCalls++
	f.recoverSlotStrictSlotID = slotID
	f.recoverSlotStrictStrategy = strategy
	return f.recoverSlotStrictErr
}

func (f *fakeSlotRecoverRebalanceCluster) GetMigrationStatus() []raftcluster.HashSlotMigration {
	return append([]raftcluster.HashSlotMigration(nil), f.migrationStatus...)
}

func (f *fakeSlotRecoverRebalanceCluster) Rebalance(context.Context) ([]raftcluster.MigrationPlan, error) {
	f.rebalanceCalls++
	return append([]raftcluster.MigrationPlan(nil), f.rebalancePlan...), f.rebalanceErr
}
