//go:build e2e

package suite

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResolveSlotTopologyRejectsMissingLeader(t *testing.T) {
	body := []byte(`{
		"slot_id":1,
		"state":{"quorum":"ready","sync":"matched"},
		"runtime":{"leader_id":0,"current_peers":[1,2,3],"has_quorum":true}
	}`)

	_, err := parseSlotTopology(1, []uint64{1, 2, 3}, body)
	require.Error(t, err)
}

func TestResolveSlotTopologyRejectsPeerMismatch(t *testing.T) {
	body := []byte(`{
		"slot_id":1,
		"state":{"quorum":"ready","sync":"peer_mismatch"},
		"runtime":{"leader_id":2,"current_peers":[1,2],"has_quorum":true}
	}`)

	_, err := parseSlotTopology(1, []uint64{1, 2, 3}, body)
	require.Error(t, err)
}

func TestResolveSlotTopologyReturnsLeaderAndFollowers(t *testing.T) {
	body := []byte(`{
		"slot_id":1,
		"state":{"quorum":"ready","sync":"matched"},
		"runtime":{"leader_id":2,"current_peers":[1,2,3],"has_quorum":true}
	}`)

	got, err := parseSlotTopology(1, []uint64{1, 2, 3}, body)
	require.NoError(t, err)
	require.Equal(t, uint32(1), got.SlotID)
	require.Equal(t, uint64(2), got.LeaderNodeID)
	require.Equal(t, []uint64{1, 3}, got.FollowerNodeIDs)
	require.Contains(t, got.RawBody, `"leader_id":2`)
}

func TestConnectionsContainUID(t *testing.T) {
	items := []ManagerConnection{
		{UID: "u1"},
		{UID: "u2"},
	}

	require.True(t, connectionsContainUID(items, "u1"))
	require.False(t, connectionsContainUID(items, "u3"))
}
