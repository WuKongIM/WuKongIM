package management

import (
	"context"
	"testing"
	"time"

	raftcluster "github.com/WuKongIM/WuKongIM/pkg/legacy/cluster"
	"github.com/stretchr/testify/require"
)

func TestCompactSlotRaftLogTargetsSelectedNodeAndSlot(t *testing.T) {
	generatedAt := time.Date(2026, 5, 7, 11, 0, 0, 0, time.UTC)
	app := New(Options{
		Now: func() time.Time { return generatedAt },
		Cluster: fakeClusterReader{
			slotRaftCompactions: map[slotRaftCompactionKey]raftcluster.SlotRaftCompactionResult{
				{nodeID: 2, slotID: 9}: {
					NodeID:              2,
					SlotID:              9,
					AppliedIndex:        50,
					BeforeSnapshotIndex: 40,
					AfterSnapshotIndex:  50,
					Compacted:           true,
				},
			},
		},
	})

	got, err := app.CompactSlotRaftLog(context.Background(), 2, 9)

	require.NoError(t, err)
	require.Equal(t, CompactSlotRaftLogResponse{
		GeneratedAt: generatedAt,
		Total:       1,
		Succeeded:   1,
		Items: []SlotRaftCompactionNodeResult{{
			NodeID:              2,
			SlotID:              9,
			Success:             true,
			AppliedIndex:        50,
			BeforeSnapshotIndex: 40,
			AfterSnapshotIndex:  50,
			Compacted:           true,
		}},
	}, got)
}

func TestCompactSlotRaftLogReportsSelectedNodeFailure(t *testing.T) {
	app := New(Options{
		Cluster: fakeClusterReader{
			slotRaftCompactionErrs: map[slotRaftCompactionKey]error{
				{nodeID: 3, slotID: 9}: context.DeadlineExceeded,
			},
		},
	})

	got, err := app.CompactSlotRaftLog(context.Background(), 3, 9)

	require.NoError(t, err)
	require.Equal(t, 1, got.Total)
	require.Equal(t, 0, got.Succeeded)
	require.Equal(t, 1, got.Failed)
	require.Equal(t, []SlotRaftCompactionNodeResult{{
		NodeID:  3,
		SlotID:  9,
		Success: false,
		Error:   context.DeadlineExceeded.Error(),
	}}, got.Items)
}
