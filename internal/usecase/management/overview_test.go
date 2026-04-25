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

func TestGetOverviewAggregatesCountsAndAnomalies(t *testing.T) {
	now := time.Unix(1713736200, 0).UTC()
	app := New(Options{
		Cluster: fakeClusterReader{
			controllerLeaderID: 1,
			slotIDs:            []multiraft.SlotID{1, 2, 3, 4},
			nodes: []controllermeta.ClusterNode{
				{NodeID: 1, Addr: "127.0.0.1:7001", Status: controllermeta.NodeStatusAlive, CapacityWeight: 1},
				{NodeID: 2, Addr: "127.0.0.1:7002", Status: controllermeta.NodeStatusDraining, CapacityWeight: 2},
			},
			assignments: []controllermeta.SlotAssignment{
				{SlotID: 1, DesiredPeers: []uint64{1, 2, 3}, ConfigEpoch: 8, BalanceVersion: 1},
				{SlotID: 2, DesiredPeers: []uint64{1, 2, 3}, ConfigEpoch: 8, BalanceVersion: 1},
				{SlotID: 3, DesiredPeers: []uint64{1, 2, 3}, ConfigEpoch: 8, BalanceVersion: 1},
			},
			views: []controllermeta.SlotRuntimeView{
				{SlotID: 1, CurrentPeers: []uint64{1, 2, 3}, LeaderID: 1, HealthyVoters: 3, HasQuorum: true, ObservedConfigEpoch: 8, LastReportAt: now.Add(-time.Second)},
				{SlotID: 2, CurrentPeers: []uint64{1, 2, 3}, LeaderID: 0, HealthyVoters: 2, HasQuorum: false, ObservedConfigEpoch: 8, LastReportAt: now.Add(-2 * time.Second)},
				{SlotID: 3, CurrentPeers: []uint64{1, 2}, LeaderID: 2, HealthyVoters: 2, HasQuorum: true, ObservedConfigEpoch: 8, LastReportAt: now.Add(-3 * time.Second)},
			},
			tasks: []controllermeta.ReconcileTask{
				{SlotID: 3, Kind: controllermeta.TaskKindRepair, Step: controllermeta.TaskStepCatchUp, Status: controllermeta.TaskStatusRetrying, Attempt: 2, NextRunAt: now.Add(time.Minute), LastError: "catch-up timeout"},
				{SlotID: 4, Kind: controllermeta.TaskKindRebalance, Step: controllermeta.TaskStepTransferLeader, Status: controllermeta.TaskStatusFailed, Attempt: 3, LastError: "leader transfer rejected"},
				{SlotID: 1, Kind: controllermeta.TaskKindBootstrap, Step: controllermeta.TaskStepAddLearner, Status: controllermeta.TaskStatusPending, TargetNode: 3},
			},
		},
		Now: func() time.Time { return now },
	})

	got, err := app.GetOverview(context.Background())
	require.NoError(t, err)
	require.Equal(t, now, got.GeneratedAt)
	require.Equal(t, uint64(1), got.Cluster.ControllerLeaderID)
	require.Equal(t, OverviewNodes{
		Total:    2,
		Alive:    1,
		Draining: 1,
	}, got.Nodes)
	require.Equal(t, OverviewSlots{
		Total:         4,
		Ready:         2,
		QuorumLost:    1,
		LeaderMissing: 1,
		Unreported:    1,
		PeerMismatch:  1,
		EpochLag:      0,
	}, got.Slots)
	require.Equal(t, OverviewTasks{
		Total:    3,
		Pending:  1,
		Retrying: 1,
		Failed:   1,
	}, got.Tasks)
	require.Equal(t, []overviewSlotAnomalySummary{
		{SlotID: 2, Quorum: "lost", Sync: "matched", LeaderID: 0},
	}, summarizeOverviewSlotAnomalies(got.Anomalies.Slots.QuorumLost.Items))
	require.Equal(t, []overviewSlotAnomalySummary{
		{SlotID: 2, Quorum: "lost", Sync: "matched", LeaderID: 0},
	}, summarizeOverviewSlotAnomalies(got.Anomalies.Slots.LeaderMissing.Items))
	require.Equal(t, []overviewTaskAnomalySummary{
		{SlotID: 4, Status: "failed", Attempt: 3},
	}, summarizeOverviewTaskAnomalies(got.Anomalies.Tasks.Failed.Items))
	require.Equal(t, []overviewTaskAnomalySummary{
		{SlotID: 3, Status: "retrying", Attempt: 2},
	}, summarizeOverviewTaskAnomalies(got.Anomalies.Tasks.Retrying.Items))
}

func TestGetOverviewUsesSlotIDsAsUniverseAndCapsAnomalies(t *testing.T) {
	now := time.Unix(1713736200, 0).UTC()
	views := make([]controllermeta.SlotRuntimeView, 0, 6)
	assignments := make([]controllermeta.SlotAssignment, 0, 6)
	slotIDs := make([]multiraft.SlotID, 0, 7)
	for i := 1; i <= 7; i++ {
		slotIDs = append(slotIDs, multiraft.SlotID(i))
	}
	for i := 1; i <= 6; i++ {
		assignments = append(assignments, controllermeta.SlotAssignment{
			SlotID:         uint32(i),
			DesiredPeers:   []uint64{1, 2, 3},
			ConfigEpoch:    9,
			BalanceVersion: 1,
		})
		views = append(views, controllermeta.SlotRuntimeView{
			SlotID:              uint32(i),
			CurrentPeers:        []uint64{1, 2, 3},
			LeaderID:            0,
			HealthyVoters:       1,
			HasQuorum:           false,
			ObservedConfigEpoch: 9,
			LastReportAt:        now.Add(time.Duration(-i) * time.Second),
		})
	}

	app := New(Options{
		Cluster: fakeClusterReader{
			controllerLeaderID: 2,
			slotIDs:            slotIDs,
			assignments:        assignments,
			views:              views,
		},
		Now: func() time.Time { return now },
	})

	got, err := app.GetOverview(context.Background())
	require.NoError(t, err)
	require.Equal(t, 7, got.Slots.Total)
	require.Equal(t, 1, got.Slots.Unreported)
	require.Equal(t, 6, got.Anomalies.Slots.QuorumLost.Count)
	require.Equal(t, 5, len(got.Anomalies.Slots.QuorumLost.Items))
	require.Equal(t, []uint32{1, 2, 3, 4, 5}, overviewSlotIDs(got.Anomalies.Slots.QuorumLost.Items))
}

func TestGetOverviewCountsSyncMismatchAndPreservesSyncState(t *testing.T) {
	now := time.Unix(1713736200, 0).UTC()
	app := New(Options{
		Cluster: fakeClusterReader{
			controllerLeaderID: 1,
			slotIDs:            []multiraft.SlotID{1, 2},
			assignments: []controllermeta.SlotAssignment{
				{SlotID: 1, DesiredPeers: []uint64{1, 2, 3}, ConfigEpoch: 8, BalanceVersion: 1},
				{SlotID: 2, DesiredPeers: []uint64{1, 2, 3}, ConfigEpoch: 9, BalanceVersion: 1},
			},
			views: []controllermeta.SlotRuntimeView{
				{SlotID: 1, CurrentPeers: []uint64{1, 2}, LeaderID: 1, HealthyVoters: 2, HasQuorum: true, ObservedConfigEpoch: 8, LastReportAt: now},
				{SlotID: 2, CurrentPeers: []uint64{1, 2, 3}, LeaderID: 2, HealthyVoters: 3, HasQuorum: true, ObservedConfigEpoch: 8, LastReportAt: now.Add(-time.Second)},
			},
		},
		Now: func() time.Time { return now },
	})

	got, err := app.GetOverview(context.Background())
	require.NoError(t, err)
	require.Equal(t, 2, got.Anomalies.Slots.SyncMismatch.Count)
	require.Equal(t, []overviewSlotAnomalySummary{
		{SlotID: 1, Quorum: "ready", Sync: "peer_mismatch", LeaderID: 1},
		{SlotID: 2, Quorum: "ready", Sync: "epoch_lag", LeaderID: 2},
	}, summarizeOverviewSlotAnomalies(got.Anomalies.Slots.SyncMismatch.Items))
}

func TestGetOverviewPropagatesStrictReadErrors(t *testing.T) {
	testCases := []struct {
		name    string
		cluster fakeClusterReader
	}{
		{
			name:    "nodes",
			cluster: fakeClusterReader{listNodesErr: raftcluster.ErrNoLeader},
		},
		{
			name:    "assignments",
			cluster: fakeClusterReader{listSlotAssignmentsErr: raftcluster.ErrNotLeader},
		},
		{
			name:    "views",
			cluster: fakeClusterReader{listObservedRuntimeViewsErr: raftcluster.ErrNotStarted},
		},
		{
			name:    "tasks",
			cluster: fakeClusterReader{listTasksErr: raftcluster.ErrNoLeader},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			app := New(Options{
				Cluster: tc.cluster,
				Now:     time.Now,
			})

			_, err := app.GetOverview(context.Background())
			require.Error(t, err)
		})
	}
}

type overviewSlotAnomalySummary struct {
	SlotID   uint32
	Quorum   string
	Sync     string
	LeaderID uint64
}

func summarizeOverviewSlotAnomalies(items []OverviewSlotAnomalyItem) []overviewSlotAnomalySummary {
	out := make([]overviewSlotAnomalySummary, 0, len(items))
	for _, item := range items {
		out = append(out, overviewSlotAnomalySummary{
			SlotID:   item.SlotID,
			Quorum:   item.Quorum,
			Sync:     item.Sync,
			LeaderID: item.LeaderID,
		})
	}
	return out
}

func overviewSlotIDs(items []OverviewSlotAnomalyItem) []uint32 {
	out := make([]uint32, 0, len(items))
	for _, item := range items {
		out = append(out, item.SlotID)
	}
	return out
}

type overviewTaskAnomalySummary struct {
	SlotID  uint32
	Status  string
	Attempt uint32
}

func summarizeOverviewTaskAnomalies(items []OverviewTaskAnomalyItem) []overviewTaskAnomalySummary {
	out := make([]overviewTaskAnomalySummary, 0, len(items))
	for _, item := range items {
		out = append(out, overviewTaskAnomalySummary{
			SlotID:  item.SlotID,
			Status:  item.Status,
			Attempt: item.Attempt,
		})
	}
	return out
}
