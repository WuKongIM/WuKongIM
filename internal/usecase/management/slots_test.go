package management

import (
	"context"
	"testing"
	"time"

	raftcluster "github.com/WuKongIM/WuKongIM/pkg/cluster"
	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	"github.com/stretchr/testify/require"
)

func TestListSlotsAggregatesAssignmentRuntimeAndDerivedState(t *testing.T) {
	now := time.Unix(1713686400, 0).UTC()
	app := New(Options{
		Cluster: fakeClusterReader{
			assignments: []controllermeta.SlotAssignment{{
				SlotID:          2,
				DesiredPeers:    []uint64{1, 2, 3},
				ConfigEpoch:     8,
				BalanceVersion:  3,
				PreferredLeader: 2,
			}},
			views: []controllermeta.SlotRuntimeView{{
				SlotID:              2,
				CurrentPeers:        []uint64{1, 2, 3},
				CurrentVoters:       []uint64{1, 2, 3},
				LeaderID:            1,
				HealthyVoters:       3,
				HasQuorum:           true,
				ObservedConfigEpoch: 8,
				LastReportAt:        now,
			}},
			tasks: []controllermeta.ReconcileTask{{
				SlotID: 2,
				Kind:   controllermeta.TaskKindLeaderTransfer,
				Status: controllermeta.TaskStatusPending,
			}},
		},
	})

	got, err := app.ListSlots(context.Background(), ListSlotsOptions{})
	require.NoError(t, err)
	require.Equal(t, []Slot{{
		SlotID: 2,
		State: SlotState{
			Quorum:                "ready",
			Sync:                  "matched",
			LeaderMatch:           false,
			LeaderTransferPending: true,
		},
		Assignment: SlotAssignment{
			DesiredPeers:    []uint64{1, 2, 3},
			PreferredLeader: 2,
			ConfigEpoch:     8,
			BalanceVersion:  3,
		},
		Runtime: SlotRuntime{
			CurrentPeers:        []uint64{1, 2, 3},
			CurrentVoters:       []uint64{1, 2, 3},
			LeaderID:            1,
			HealthyVoters:       3,
			HasQuorum:           true,
			ObservedConfigEpoch: 8,
			LastReportAt:        now,
		},
	}}, got)
}

func TestSlotFromAssignmentViewMarksLeaderMatchWhenPreferredLeaderLeads(t *testing.T) {
	assignment := controllermeta.SlotAssignment{
		SlotID:          1,
		DesiredPeers:    []uint64{1, 2, 3},
		PreferredLeader: 2,
	}
	view := controllermeta.SlotRuntimeView{
		SlotID:        1,
		CurrentPeers:  []uint64{1, 2, 3},
		CurrentVoters: []uint64{1, 2, 3},
		LeaderID:      2,
		HasQuorum:     true,
	}

	got := slotFromAssignmentView(assignment, view, true)

	require.Equal(t, uint64(2), got.Assignment.PreferredLeader)
	require.Equal(t, []uint64{1, 2, 3}, got.Runtime.CurrentVoters)
	require.True(t, got.State.LeaderMatch)
}

func TestListSlotsMarksPeerMismatchEpochLagLostAndUnreportedStates(t *testing.T) {
	now := time.Unix(1713686400, 0).UTC()
	app := New(Options{
		Cluster: fakeClusterReader{
			assignments: []controllermeta.SlotAssignment{
				{SlotID: 1, DesiredPeers: []uint64{1, 2, 3}, ConfigEpoch: 9},
				{SlotID: 2, DesiredPeers: []uint64{2, 3, 4}, ConfigEpoch: 10},
				{SlotID: 3, DesiredPeers: []uint64{3, 4, 5}, ConfigEpoch: 11},
			},
			views: []controllermeta.SlotRuntimeView{
				{
					SlotID:              1,
					CurrentPeers:        []uint64{1, 2},
					LeaderID:            1,
					HealthyVoters:       2,
					HasQuorum:           false,
					ObservedConfigEpoch: 9,
					LastReportAt:        now,
				},
				{
					SlotID:              2,
					CurrentPeers:        []uint64{2, 3, 4},
					LeaderID:            2,
					HealthyVoters:       3,
					HasQuorum:           true,
					ObservedConfigEpoch: 8,
					LastReportAt:        now.Add(time.Second),
				},
			},
		},
	})

	got, err := app.ListSlots(context.Background(), ListSlotsOptions{})
	require.NoError(t, err)
	require.Equal(t, []slotStateSummary{
		{SlotID: 1, Quorum: "lost", Sync: "peer_mismatch"},
		{SlotID: 2, Quorum: "ready", Sync: "epoch_lag"},
		{SlotID: 3, Quorum: "unknown", Sync: "unreported"},
	}, summarizeSlots(got))
}

func TestListSlotsSortsBySlotID(t *testing.T) {
	app := New(Options{
		Cluster: fakeClusterReader{
			assignments: []controllermeta.SlotAssignment{
				{SlotID: 9, DesiredPeers: []uint64{1}},
				{SlotID: 4, DesiredPeers: []uint64{2}},
			},
		},
	})

	got, err := app.ListSlots(context.Background(), ListSlotsOptions{})
	require.NoError(t, err)
	require.Equal(t, []uint32{4, 9}, []uint32{got[0].SlotID, got[1].SlotID})
}

func TestListSlotsFiltersByNodeAndAttachesNodeLogStatus(t *testing.T) {
	now := time.Unix(1713686400, 0).UTC()
	app := New(Options{
		Cluster: fakeClusterReader{
			assignments: []controllermeta.SlotAssignment{
				{SlotID: 1, DesiredPeers: []uint64{1, 2}, ConfigEpoch: 7},
				{SlotID: 2, DesiredPeers: []uint64{3}, ConfigEpoch: 8},
				{SlotID: 3, DesiredPeers: []uint64{4}, ConfigEpoch: 9},
			},
			views: []controllermeta.SlotRuntimeView{
				{SlotID: 1, CurrentPeers: []uint64{1, 2}, LeaderID: 1, HasQuorum: true, ObservedConfigEpoch: 7, LastReportAt: now},
				{SlotID: 2, CurrentPeers: []uint64{2, 3}, LeaderID: 3, HasQuorum: true, ObservedConfigEpoch: 8, LastReportAt: now},
				{SlotID: 3, CurrentPeers: []uint64{4}, LeaderID: 4, HasQuorum: true, ObservedConfigEpoch: 9, LastReportAt: now},
			},
			slotLogStatus: map[slotLogStatusKey]raftcluster.SlotLogStatus{
				{nodeID: 2, slotID: 1}: {LeaderID: 1, CommitIndex: 93, AppliedIndex: 91},
				{nodeID: 2, slotID: 2}: {LeaderID: 3, CommitIndex: 44, AppliedIndex: 44},
			},
		},
	})

	got, err := app.ListSlots(context.Background(), ListSlotsOptions{NodeID: 2})
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Equal(t, []uint32{1, 2}, []uint32{got[0].SlotID, got[1].SlotID})
	require.Equal(t, &SlotNodeLogStatus{NodeID: 2, LeaderID: 1, CommitIndex: 93, AppliedIndex: 91}, got[0].NodeLog)
	require.Equal(t, &SlotNodeLogStatus{NodeID: 2, LeaderID: 3, CommitIndex: 44, AppliedIndex: 44}, got[1].NodeLog)
}

type slotStateSummary struct {
	SlotID uint32
	Quorum string
	Sync   string
}

func summarizeSlots(slots []Slot) []slotStateSummary {
	out := make([]slotStateSummary, 0, len(slots))
	for _, slot := range slots {
		out = append(out, slotStateSummary{
			SlotID: slot.SlotID,
			Quorum: slot.State.Quorum,
			Sync:   slot.State.Sync,
		})
	}
	return out
}
