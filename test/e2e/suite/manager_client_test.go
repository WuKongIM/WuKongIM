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

func TestDecodeManagerNodesResponse(t *testing.T) {
	body := []byte(`{
		"total": 2,
		"items": [{
			"node_id": 1,
			"addr": "127.0.0.1:17001",
			"status": "alive",
			"is_local": true,
			"controller": {"role": "leader"},
			"slot_stats": {"count": 1, "leader_count": 1}
		}, {
			"node_id": 4,
			"addr": "127.0.0.1:17004",
			"status": "alive",
			"is_local": false,
			"controller": {"role": "none"},
			"slot_stats": {"count": 0, "leader_count": 0}
		}]
	}`)

	resp, err := decodeManagerNodesResponse(body)

	require.NoError(t, err)
	require.Equal(t, 2, resp.Total)
	require.Len(t, resp.Items, 2)
	require.Equal(t, uint64(4), resp.Items[1].NodeID)
	require.Equal(t, "127.0.0.1:17004", resp.Items[1].Addr)
	require.Equal(t, "alive", resp.Items[1].Status)
	require.Equal(t, "none", resp.Items[1].Controller.Role)
}

func TestManagerNodesFindsNodeByID(t *testing.T) {
	items := []ManagerNode{
		{NodeID: 1},
		{NodeID: 4, Addr: "127.0.0.1:17004"},
	}

	node, ok := managerNodeByID(items, 4)

	require.True(t, ok)
	require.Equal(t, "127.0.0.1:17004", node.Addr)
	_, ok = managerNodeByID(items, 9)
	require.False(t, ok)
}
