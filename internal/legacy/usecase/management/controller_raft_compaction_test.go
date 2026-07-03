package management

import (
	"context"
	"errors"
	"testing"
	"time"

	raftcluster "github.com/WuKongIM/WuKongIM/pkg/legacy/cluster"
	"github.com/stretchr/testify/require"
)

func TestCompactControllerRaftLogsFansOutToControllerPeers(t *testing.T) {
	generatedAt := time.Date(2026, 5, 7, 10, 0, 0, 0, time.UTC)
	sentinel := errors.New("node stopped")
	app := New(Options{
		ControllerPeerIDs: []uint64{2, 1, 2, 0},
		Now:               func() time.Time { return generatedAt },
		Cluster: fakeClusterReader{
			controllerRaftCompactions: map[uint64]raftcluster.ControllerRaftCompactionResult{
				1: {
					NodeID:              1,
					AppliedIndex:        42,
					BeforeSnapshotIndex: 30,
					AfterSnapshotIndex:  42,
					Compacted:           true,
				},
			},
			controllerRaftCompactErr: map[uint64]error{
				2: sentinel,
			},
		},
	})

	got, err := app.CompactControllerRaftLogs(context.Background())
	require.NoError(t, err)
	require.Equal(t, generatedAt, got.GeneratedAt)
	require.Equal(t, 2, got.Total)
	require.Equal(t, 1, got.Succeeded)
	require.Equal(t, 1, got.Failed)
	require.Equal(t, []ControllerRaftCompactionNodeResult{{
		NodeID:              1,
		Success:             true,
		AppliedIndex:        42,
		BeforeSnapshotIndex: 30,
		AfterSnapshotIndex:  42,
		Compacted:           true,
	}, {
		NodeID:  2,
		Success: false,
		Error:   sentinel.Error(),
	}}, got.Items)
}

func TestCompactControllerRaftLogTargetsSelectedNode(t *testing.T) {
	generatedAt := time.Date(2026, 5, 7, 10, 1, 0, 0, time.UTC)
	app := New(Options{
		ControllerPeerIDs: []uint64{1, 2},
		Now:               func() time.Time { return generatedAt },
		Cluster: fakeClusterReader{
			controllerRaftCompactions: map[uint64]raftcluster.ControllerRaftCompactionResult{
				2: {
					NodeID:              2,
					AppliedIndex:        50,
					BeforeSnapshotIndex: 40,
					AfterSnapshotIndex:  50,
					Compacted:           true,
				},
			},
		},
	})

	got, err := app.CompactControllerRaftLog(context.Background(), 2)
	require.NoError(t, err)
	require.Equal(t, generatedAt, got.GeneratedAt)
	require.Equal(t, 1, got.Total)
	require.Equal(t, 1, got.Succeeded)
	require.Equal(t, 0, got.Failed)
	require.Equal(t, []ControllerRaftCompactionNodeResult{{
		NodeID:              2,
		Success:             true,
		AppliedIndex:        50,
		BeforeSnapshotIndex: 40,
		AfterSnapshotIndex:  50,
		Compacted:           true,
	}}, got.Items)
}

func TestCompactControllerRaftLogReportsSelectedNodeFailure(t *testing.T) {
	generatedAt := time.Date(2026, 5, 7, 10, 2, 0, 0, time.UTC)
	sentinel := errors.New("node stopped")
	app := New(Options{
		Now: func() time.Time { return generatedAt },
		Cluster: fakeClusterReader{
			controllerRaftCompactErr: map[uint64]error{
				3: sentinel,
			},
		},
	})

	got, err := app.CompactControllerRaftLog(context.Background(), 3)
	require.NoError(t, err)
	require.Equal(t, 1, got.Total)
	require.Equal(t, 0, got.Succeeded)
	require.Equal(t, 1, got.Failed)
	require.Equal(t, []ControllerRaftCompactionNodeResult{{
		NodeID:  3,
		Success: false,
		Error:   sentinel.Error(),
	}}, got.Items)
}
