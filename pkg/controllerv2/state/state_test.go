package state

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestEncodeCanonicalSortsAndChecksums(t *testing.T) {
	st := testState()
	st.Nodes[0], st.Nodes[1] = st.Nodes[1], st.Nodes[0]
	st.Controllers[0], st.Controllers[1] = st.Controllers[1], st.Controllers[0]

	data, err := Encode(st)
	require.NoError(t, err)
	require.Contains(t, string(data), `"checksum":"crc32c:`)

	decoded, err := Decode(data)
	require.NoError(t, err)
	require.Equal(t, []uint64{1, 2, 3}, nodeIDs(decoded.Nodes))
	require.Equal(t, []uint64{1, 2, 3}, decoded.Slots[0].DesiredPeers)
}

func TestDecodeRejectsChecksumMismatch(t *testing.T) {
	data, err := Encode(testState())
	require.NoError(t, err)
	tampered := strings.Replace(string(data), `"revision":1`, `"revision":2`, 1)

	_, err = Decode([]byte(tampered))
	require.ErrorIs(t, err, ErrChecksumMismatch)
}

func TestValidateRejectsDuplicateNode(t *testing.T) {
	st := testState()
	st.Nodes = append(st.Nodes, st.Nodes[0])
	require.ErrorIs(t, st.Validate(), ErrInvalidState)
}

func TestValidateRejectsControllerWithoutRole(t *testing.T) {
	st := testState()
	st.Nodes[0].Roles = []NodeRole{NodeRoleData}
	require.ErrorIs(t, st.Validate(), ErrInvalidState)
}

func TestValidateRequiresControllerVoterSet(t *testing.T) {
	st := testState()
	st.Controllers = []ControllerVoter{}
	require.ErrorIs(t, st.Validate(), ErrInvalidState)
}

func TestValidateRejectsSlotPeerWithoutDataRole(t *testing.T) {
	st := testState()
	st.Nodes[1].Roles = []NodeRole{NodeRoleControllerVoter}
	require.ErrorIs(t, st.Validate(), ErrInvalidState)
}

func TestValidateRejectsHashSlotGap(t *testing.T) {
	st := testState()
	st.HashSlots.Ranges[0].To = 7
	require.ErrorIs(t, st.Validate(), ErrInvalidState)
}

func TestValidateRequiresBootstrapTaskMatchesAssignment(t *testing.T) {
	st := testState()
	st.Tasks[0].TargetPeers = []uint64{1, 2}
	require.ErrorIs(t, st.Validate(), ErrInvalidState)
}

func TestBuildInitialHashSlotTableDistributesRanges(t *testing.T) {
	table, err := BuildInitialHashSlotTable(16, 16384)
	require.NoError(t, err)
	require.Len(t, table.Ranges, 16)
	require.Equal(t, uint32(1), table.Ranges[0].SlotID)
	require.Equal(t, uint16(0), table.Ranges[0].From)
	require.Equal(t, uint16(1023), table.Ranges[0].To)
	require.Equal(t, uint32(16), table.Ranges[15].SlotID)
	require.Equal(t, uint16(15360), table.Ranges[15].From)
	require.Equal(t, uint16(16383), table.Ranges[15].To)
}

func TestValidateDoesNotMutateCallerState(t *testing.T) {
	st := testState()
	st.UpdatedAt = time.Date(2026, 5, 24, 18, 0, 0, 0, time.FixedZone("plus-eight", 8*60*60))
	st.Controllers[0], st.Controllers[1] = st.Controllers[1], st.Controllers[0]
	st.Nodes = []Node{
		{NodeID: 3, Name: "n3", Addr: "n3", Roles: []NodeRole{NodeRoleData}, JoinState: NodeJoinStateActive, Status: NodeStatusAlive},
		{NodeID: 2, Name: "n2", Addr: "n2", Roles: []NodeRole{NodeRoleData, NodeRoleControllerVoter}, JoinState: NodeJoinStateActive, Status: NodeStatusAlive},
		{NodeID: 1, Name: "n1", Addr: "n1", Roles: []NodeRole{NodeRoleData, NodeRoleControllerVoter}, JoinState: NodeJoinStateActive, Status: NodeStatusAlive},
	}
	st.Slots[0].DesiredPeers = []uint64{3, 1, 2}
	st.Tasks[0].TargetPeers = []uint64{3, 2, 1}
	originalUpdatedAt := st.UpdatedAt

	require.NoError(t, st.Validate())
	require.Equal(t, []uint64{3, 2, 1}, nodeIDs(st.Nodes))
	require.Equal(t, []NodeRole{NodeRoleData, NodeRoleControllerVoter}, st.Nodes[1].Roles)
	require.Equal(t, []uint64{3, 1, 2}, st.Slots[0].DesiredPeers)
	require.Equal(t, []uint64{3, 2, 1}, st.Tasks[0].TargetPeers)
	require.Equal(t, originalUpdatedAt, st.UpdatedAt)
	require.Zero(t, st.Nodes[0].CapacityWeight)
}

func TestDecodeRejectsUnknownTopLevelField(t *testing.T) {
	data, err := Encode(testState())
	require.NoError(t, err)
	withUnknown := strings.Replace(string(data), `{"schema_version":1`, `{"schema_version":1,"unknown":true`, 1)

	_, err = Decode([]byte(withUnknown))
	require.Error(t, err)
	require.NotErrorIs(t, err, ErrChecksumMismatch)
}

func TestDecodeRejectsUnknownNestedField(t *testing.T) {
	data, err := Encode(testState())
	require.NoError(t, err)
	withUnknown := strings.Replace(string(data), `"replica_count":3`, `"replica_count":3,"unknown":true`, 1)

	_, err = Decode([]byte(withUnknown))
	require.Error(t, err)
	require.NotErrorIs(t, err, ErrChecksumMismatch)
}

func TestEncodeEmptyRepeatedFieldsAsArrays(t *testing.T) {
	table, err := BuildInitialHashSlotTable(1, 1)
	require.NoError(t, err)
	st := ClusterState{
		SchemaVersion:    CurrentSchemaVersion,
		ClusterID:        "wk-empty",
		Revision:         1,
		AppliedRaftIndex: 1,
		UpdatedAt:        time.Date(2026, 5, 24, 10, 0, 0, 0, time.UTC),
		Config:           ClusterConfig{SlotCount: 1, HashSlotCount: 1, ReplicaCount: 1},
		Controllers:      []ControllerVoter{{NodeID: 1, Addr: "n1", Role: ControllerRoleVoter}},
		Nodes:            []Node{{NodeID: 1, Addr: "n1", Roles: []NodeRole{NodeRoleControllerVoter, NodeRoleData}, JoinState: NodeJoinStateActive, Status: NodeStatusAlive}},
		Slots:            []SlotAssignment{},
		HashSlots:        table,
		Tasks:            []ReconcileTask{},
	}

	data, err := Encode(st)
	require.NoError(t, err)
	require.Contains(t, string(data), `"slots":[]`)
	require.Contains(t, string(data), `"tasks":[]`)
}

func TestValidateRejectsDesiredPeersLengthPastUint16Boundary(t *testing.T) {
	const peerCount = 1<<16 + 3
	table, err := BuildInitialHashSlotTable(1, 1)
	require.NoError(t, err)
	nodes := make([]Node, 0, peerCount)
	peers := make([]uint64, 0, peerCount)
	for nodeID := uint64(1); nodeID <= peerCount; nodeID++ {
		roles := []NodeRole{NodeRoleData}
		if nodeID == 1 {
			roles = []NodeRole{NodeRoleControllerVoter, NodeRoleData}
		}
		nodes = append(nodes, Node{
			NodeID:         nodeID,
			Name:           fmt.Sprintf("n%d", nodeID),
			Addr:           fmt.Sprintf("n%d", nodeID),
			Roles:          roles,
			JoinState:      NodeJoinStateActive,
			Status:         NodeStatusAlive,
			CapacityWeight: 1,
		})
		peers = append(peers, nodeID)
	}
	st := ClusterState{
		SchemaVersion:    CurrentSchemaVersion,
		ClusterID:        "wk-overflow",
		Revision:         1,
		AppliedRaftIndex: 1,
		UpdatedAt:        time.Date(2026, 5, 24, 10, 0, 0, 0, time.UTC),
		Config:           ClusterConfig{SlotCount: 1, HashSlotCount: 1, ReplicaCount: 3},
		Controllers:      []ControllerVoter{{NodeID: 1, Addr: "n1", Role: ControllerRoleVoter}},
		Nodes:            nodes,
		Slots:            []SlotAssignment{{SlotID: 1, DesiredPeers: peers, ConfigEpoch: 1}},
		HashSlots:        table,
	}

	require.ErrorIs(t, st.Validate(), ErrInvalidState)
}

func testState() ClusterState {
	now := time.Date(2026, 5, 24, 10, 0, 0, 0, time.UTC)
	table, _ := BuildInitialHashSlotTable(1, 16)
	return ClusterState{
		SchemaVersion:    CurrentSchemaVersion,
		ClusterID:        "wk-test",
		Revision:         1,
		AppliedRaftIndex: 1,
		UpdatedAt:        now,
		Config: ClusterConfig{
			SlotCount:     1,
			HashSlotCount: 16,
			ReplicaCount:  3,
		},
		Controllers: []ControllerVoter{
			{NodeID: 1, Addr: "n1", Role: ControllerRoleVoter},
			{NodeID: 2, Addr: "n2", Role: ControllerRoleVoter},
		},
		Nodes: []Node{
			{NodeID: 1, Name: "n1", Addr: "n1", Roles: []NodeRole{NodeRoleControllerVoter, NodeRoleData}, JoinState: NodeJoinStateActive, Status: NodeStatusAlive, CapacityWeight: 100},
			{NodeID: 2, Name: "n2", Addr: "n2", Roles: []NodeRole{NodeRoleControllerVoter, NodeRoleData}, JoinState: NodeJoinStateActive, Status: NodeStatusAlive, CapacityWeight: 100},
			{NodeID: 3, Name: "n3", Addr: "n3", Roles: []NodeRole{NodeRoleData}, JoinState: NodeJoinStateActive, Status: NodeStatusAlive, CapacityWeight: 100},
		},
		Slots:     []SlotAssignment{{SlotID: 1, DesiredPeers: []uint64{3, 1, 2}, ConfigEpoch: 1, PreferredLeader: 1}},
		HashSlots: table,
		Tasks: []ReconcileTask{
			{TaskID: "slot-1-bootstrap-1", SlotID: 1, Kind: TaskKindBootstrap, Step: TaskStepCreateSlot, TargetNode: 1, TargetPeers: []uint64{1, 2, 3}, ConfigEpoch: 1, Status: TaskStatusPending},
		},
	}
}

func nodeIDs(nodes []Node) []uint64 {
	out := make([]uint64, 0, len(nodes))
	for _, node := range nodes {
		out = append(out, node.NodeID)
	}
	return out
}
