package management

import (
	"context"
	"testing"
	"time"

	raftcluster "github.com/WuKongIM/WuKongIM/pkg/legacy/cluster"
	"github.com/stretchr/testify/require"
)

func TestGetControllerRaftStatusMapsFullStatus(t *testing.T) {
	restoredAt := time.Unix(1715000000, 0).UTC()
	checkedAt := restoredAt.Add(time.Second)
	app := New(Options{Cluster: fakeClusterReader{controllerRaftStatus: map[uint64]raftcluster.ControllerRaftStatus{
		2: {
			NodeID: 2, Role: "leader", LeaderID: 2, Term: 4,
			FirstIndex: 10, LastIndex: 30, CommitIndex: 29, AppliedIndex: 28, SnapshotIndex: 9, SnapshotTerm: 3,
			Compaction: raftcluster.ControllerRaftCompactionStatus{Enabled: true, TriggerEntries: 100, CheckInterval: time.Second, LastSnapshotIndex: 9, LastSnapshotAt: restoredAt, LastCheckAt: checkedAt},
			Restore:    raftcluster.ControllerRaftRestoreStatus{LastSnapshotIndex: 9, LastSnapshotTerm: 3, LastRestoredAt: restoredAt},
			Peers:      []raftcluster.ControllerRaftPeerProgress{{NodeID: 3, Match: 20, Next: 21, State: "StateReplicate", RecentActive: true}},
		},
	}}})

	got, err := app.GetControllerRaftStatus(context.Background(), 2)
	require.NoError(t, err)
	require.Equal(t, uint64(2), got.NodeID)
	require.Equal(t, "leader", got.Role)
	require.Equal(t, ControllerRaftHealthAppendCatchup, got.Health)
	require.Equal(t, uint64(10), got.FirstIndex)
	require.True(t, got.Compaction.Enabled)
	require.Equal(t, restoredAt, got.Restore.LastRestoredAt)
	require.Len(t, got.Peers, 1)
	require.Equal(t, uint64(3), got.Peers[0].NodeID)
}

func TestControllerRaftHealthDerivesHealthy(t *testing.T) {
	status := raftcluster.ControllerRaftStatus{NodeID: 1, Role: "leader", CommitIndex: 10, Peers: []raftcluster.ControllerRaftPeerProgress{{NodeID: 2, Match: 10, Next: 11}}}
	require.Equal(t, ControllerRaftHealthHealthy, controllerRaftHealth(status))
}

func TestControllerRaftHealthDerivesUnknownForMissingIdentity(t *testing.T) {
	for _, status := range []raftcluster.ControllerRaftStatus{
		{},
		{NodeID: 1},
		{NodeID: 1, Role: "unknown", Restore: raftcluster.ControllerRaftRestoreStatus{Failed: true}},
	} {
		require.Equal(t, ControllerRaftHealthUnknown, controllerRaftHealth(status))
	}
}

func TestControllerRaftHealthDerivesAppendCatchup(t *testing.T) {
	status := raftcluster.ControllerRaftStatus{NodeID: 1, Role: "leader", CommitIndex: 10, Peers: []raftcluster.ControllerRaftPeerProgress{{NodeID: 2, Match: 8, Next: 9}}}
	require.Equal(t, ControllerRaftHealthAppendCatchup, controllerRaftHealth(status))
}

func TestControllerRaftHealthDerivesSnapshotRequired(t *testing.T) {
	status := raftcluster.ControllerRaftStatus{NodeID: 1, Role: "leader", CommitIndex: 10, Peers: []raftcluster.ControllerRaftPeerProgress{{NodeID: 2, Match: 4, Next: 5, NeedsSnapshot: true}}}
	require.Equal(t, ControllerRaftHealthSnapshotRequired, controllerRaftHealth(status))
}

func TestControllerRaftHealthDerivesSnapshotTransferring(t *testing.T) {
	status := raftcluster.ControllerRaftStatus{NodeID: 1, Role: "leader", CommitIndex: 10, Peers: []raftcluster.ControllerRaftPeerProgress{{NodeID: 2, Match: 4, Next: 5, NeedsSnapshot: true, SnapshotTransferring: true}}}
	require.Equal(t, ControllerRaftHealthSnapshotTransferring, controllerRaftHealth(status))
}

func TestControllerRaftHealthPrefersRestoreFailed(t *testing.T) {
	status := raftcluster.ControllerRaftStatus{
		NodeID: 1, Role: "leader", CommitIndex: 10,
		Compaction: raftcluster.ControllerRaftCompactionStatus{Degraded: true},
		Restore:    raftcluster.ControllerRaftRestoreStatus{Failed: true},
		Peers:      []raftcluster.ControllerRaftPeerProgress{{NodeID: 2, Match: 4, NeedsSnapshot: true, SnapshotTransferring: true}},
	}
	require.Equal(t, ControllerRaftHealthRestoreFailed, controllerRaftHealth(status))
}

func TestControllerRaftHealthPrefersCompactionDegraded(t *testing.T) {
	status := raftcluster.ControllerRaftStatus{
		NodeID: 1, Role: "leader", CommitIndex: 10,
		Compaction: raftcluster.ControllerRaftCompactionStatus{Degraded: true},
		Peers:      []raftcluster.ControllerRaftPeerProgress{{NodeID: 2, Match: 4, NeedsSnapshot: true, SnapshotTransferring: true}},
	}
	require.Equal(t, ControllerRaftHealthCompactionDegraded, controllerRaftHealth(status))
}
