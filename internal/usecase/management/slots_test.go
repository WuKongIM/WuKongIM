package management

import (
	"context"
	"testing"
	"time"

	controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
	"github.com/stretchr/testify/require"
)

func TestListSlotsAggregatesAssignmentRuntimeAndDerivedState(t *testing.T) {
	now := time.Unix(1713686400, 0).UTC()
	app := New(Options{
		Cluster: fakeClusterReader{
			assignments: []controllermeta.SlotAssignment{{
				SlotID:         2,
				DesiredPeers:   []uint64{1, 2, 3},
				ConfigEpoch:    8,
				BalanceVersion: 3,
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
		},
	})

	got, err := app.ListSlots(context.Background())
	require.NoError(t, err)
	require.Equal(t, []Slot{{
		SlotID: 2,
		State: SlotState{
			Quorum: "ready",
			Sync:   "matched",
		},
		Assignment: SlotAssignment{
			DesiredPeers:   []uint64{1, 2, 3},
			ConfigEpoch:    8,
			BalanceVersion: 3,
		},
		Runtime: SlotRuntime{
			CurrentPeers:        []uint64{1, 2, 3},
			LeaderID:            1,
			HealthyVoters:       3,
			HasQuorum:           true,
			ObservedConfigEpoch: 8,
			LastReportAt:        now,
		},
	}}, got)
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

	got, err := app.ListSlots(context.Background())
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

	got, err := app.ListSlots(context.Background())
	require.NoError(t, err)
	require.Equal(t, []uint32{4, 9}, []uint32{got[0].SlotID, got[1].SlotID})
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
