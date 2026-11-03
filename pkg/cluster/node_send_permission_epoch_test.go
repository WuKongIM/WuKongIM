package cluster

import (
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/routing"
	"github.com/WuKongIM/WuKongIM/pkg/slot/proxy"
	"github.com/stretchr/testify/require"
)

// Failure inventory: a projected local token must be nonzero and paired with
// the same immutable route publication. Pure global revision changes must not
// retire that token, while mapping A->B->A must retire the original token even
// when its final hard authority fields are equal again.
func TestSendPermissionRoutesLocalEpochTracksPublishedTopology(t *testing.T) {
	n := &Node{cfg: Config{NodeID: 1}, router: routing.NewRouter(), routeAuthorityEpochs: map[uint16]uint64{}}
	snapshot := routeAuthoritySnapshot(1)
	snapshot.Slots = append(snapshot.Slots, control.SlotAssignment{SlotID: 2, DesiredPeers: []uint64{1}, ConfigEpoch: 1, PreferredLeader: 1})
	snapshot.HashSlots.Ranges = []control.HashSlotRange{{From: 0, To: 0, SlotID: 1}, {From: 1, To: 1, SlotID: 2}}
	require.NoError(t, n.router.UpdateControlSnapshot(snapshot))
	n.router.UpdateSlotLeaders([]routing.SlotStatus{{SlotID: 1, Leader: 1, LeaderTerm: 9}, {SlotID: 2, Leader: 1, LeaderTerm: 9}})
	n.publishRouteAuthorityChanges(nil)
	n.started.Store(true)
	key := keyForNodeHashSlot(t, 2, 0)
	read := func() proxy.SendPermissionRoute {
		rows := n.SendPermissionRoutes([]string{key})
		require.Len(t, rows, 1)
		require.NoError(t, rows[0].Err)
		require.NotZero(t, rows[0].LocalAuthorityEpoch)
		return rows[0]
	}
	first := read()
	apply := func(next control.Snapshot) {
		require.NoError(t, n.updateRouteAuthorityTable(func() error { return n.router.UpdateControlSnapshot(next) }))
	}
	snapshot.Revision, snapshot.HashSlots.Revision = 2, 2
	apply(snapshot)
	revisionOnly := read()
	require.Equal(t, first.LocalAuthorityEpoch, revisionOnly.LocalAuthorityEpoch)
	require.EqualValues(t, 2, revisionOnly.Fence.RouteRevision)

	moved := snapshot.Clone()
	moved.Revision, moved.HashSlots.Revision = 3, 3
	moved.HashSlots.Ranges = []control.HashSlotRange{{From: 0, To: 1, SlotID: 2}}
	apply(moved)
	require.Greater(t, read().LocalAuthorityEpoch, first.LocalAuthorityEpoch)
	snapshot.Revision, snapshot.HashSlots.Revision = 4, 4
	apply(snapshot)
	restored := read()
	first.Fence.RouteRevision = restored.Fence.RouteRevision
	require.Equal(t, first.Fence, restored.Fence)
	require.Equal(t, first.HashSlot, restored.HashSlot)
	require.Greater(t, restored.LocalAuthorityEpoch, revisionOnly.LocalAuthorityEpoch)

	// An unfinished low-level table update cannot pair new routing with an old
	// token. Public callers retain the last complete Node publication.
	moved.Revision, moved.HashSlots.Revision = 5, 5
	require.NoError(t, n.router.UpdateControlSnapshot(moved))
	require.Equal(t, restored, read())
	n.publishRouteAuthorityChanges(nil)
	require.EqualValues(t, 2, read().Fence.SlotID)
	require.Greater(t, read().LocalAuthorityEpoch, restored.LocalAuthorityEpoch)
}
