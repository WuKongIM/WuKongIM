package state

import (
	"fmt"
	"strings"
	"testing"
	"time"

	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
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

func TestValidateAllowsRemovedDataNodeTombstone(t *testing.T) {
	st := testState()
	st.Nodes = append(st.Nodes, Node{
		NodeID:         4,
		Name:           "n4",
		Addr:           "n4",
		Roles:          []NodeRole{NodeRoleData},
		JoinState:      NodeJoinStateRemoved,
		Status:         NodeStatusDown,
		CapacityWeight: 1,
	})

	require.NoError(t, st.Validate())
}

func TestValidateRejectsRemovedControllerVoter(t *testing.T) {
	st := testState()
	st.Nodes[0].JoinState = NodeJoinStateRemoved

	require.ErrorIs(t, st.Validate(), ErrInvalidState)
}

func TestValidateRejectsRemovedSlotPeer(t *testing.T) {
	st := testState()
	st.Nodes[2].JoinState = NodeJoinStateRemoved

	require.ErrorIs(t, st.Validate(), ErrInvalidState)
}

func TestValidateAllowsLeavingDataSlotPeer(t *testing.T) {
	st := testState()
	st.Nodes[2].JoinState = NodeJoinStateLeaving

	require.NoError(t, st.Validate())
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

func TestValidateBootstrapTaskRequiresAllTargetPeerProgress(t *testing.T) {
	st := testState()
	st.Tasks = []ReconcileTask{{
		TaskID:           "slot-1-bootstrap-1",
		SlotID:           1,
		Kind:             TaskKindBootstrap,
		Step:             TaskStepCreateSlot,
		TargetNode:       1,
		TargetPeers:      []uint64{1, 2, 3},
		CompletionPolicy: TaskCompletionPolicyAllTargetPeers,
		ParticipantProgress: []TaskParticipantProgress{
			{NodeID: 1, Status: TaskParticipantStatusPending},
			{NodeID: 2, Status: TaskParticipantStatusPending},
		},
		ConfigEpoch: 1,
		Status:      TaskStatusPending,
	}}

	require.ErrorIs(t, st.Validate(), ErrInvalidState)
}

func TestClusterStateValidateAcceptsLeaderTransferTask(t *testing.T) {
	st := testState()
	st.Slots = []SlotAssignment{{SlotID: 1, DesiredPeers: []uint64{1, 2, 3}, ConfigEpoch: 7, PreferredLeader: 2}}
	st.Tasks = []ReconcileTask{validLeaderTransferTask()}

	require.NoError(t, st.Validate())
}

func TestClusterStateValidateRejectsInvalidLeaderTransferTask(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(st *ClusterState)
	}{
		{
			name: "wrong step",
			mutate: func(st *ClusterState) {
				st.Tasks[0].Step = TaskStepCreateSlot
			},
		},
		{
			name: "missing source",
			mutate: func(st *ClusterState) {
				st.Tasks[0].SourceNode = 0
			},
		},
		{
			name: "missing target",
			mutate: func(st *ClusterState) {
				st.Tasks[0].TargetNode = 0
			},
		},
		{
			name: "same source and target",
			mutate: func(st *ClusterState) {
				st.Tasks[0].TargetNode = st.Tasks[0].SourceNode
				st.Slots[0].PreferredLeader = st.Tasks[0].SourceNode
			},
		},
		{
			name: "source outside assignment",
			mutate: func(st *ClusterState) {
				st.Tasks[0].SourceNode = 4
			},
		},
		{
			name: "target outside assignment",
			mutate: func(st *ClusterState) {
				st.Tasks[0].TargetNode = 4
			},
		},
		{
			name: "target peers mismatch assignment",
			mutate: func(st *ClusterState) {
				st.Tasks[0].TargetPeers = []uint64{1, 2}
			},
		},
		{
			name: "config epoch mismatch",
			mutate: func(st *ClusterState) {
				st.Tasks[0].ConfigEpoch = 8
			},
		},
		{
			name: "target not preferred leader",
			mutate: func(st *ClusterState) {
				st.Slots[0].PreferredLeader = 1
			},
		},
		{
			name: "wrong completion policy",
			mutate: func(st *ClusterState) {
				st.Tasks[0].CompletionPolicy = TaskCompletionPolicyAllTargetPeers
			},
		},
		{
			name: "participant progress present",
			mutate: func(st *ClusterState) {
				st.Tasks[0].ParticipantProgress = []TaskParticipantProgress{{NodeID: 2, Status: TaskParticipantStatusPending}}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := testState()
			st.Slots = []SlotAssignment{{SlotID: 1, DesiredPeers: []uint64{1, 2, 3}, ConfigEpoch: 7, PreferredLeader: 2}}
			st.Tasks = []ReconcileTask{validLeaderTransferTask()}
			tt.mutate(&st)

			require.ErrorIs(t, st.Validate(), ErrInvalidState)
		})
	}
}

func TestNormalizeBootstrapTaskDefaultsParticipantProgress(t *testing.T) {
	st := testState()
	st.Slots = []SlotAssignment{{SlotID: 1, DesiredPeers: []uint64{3, 1, 2}, ConfigEpoch: 1, PreferredLeader: 1}}
	st.Tasks = []ReconcileTask{{
		TaskID:      "slot-1-bootstrap-1",
		SlotID:      1,
		Kind:        TaskKindBootstrap,
		Step:        TaskStepCreateSlot,
		TargetNode:  1,
		TargetPeers: []uint64{3, 1, 2},
		ConfigEpoch: 1,
		Status:      TaskStatusPending,
	}}

	st.Normalize()

	require.Equal(t, TaskCompletionPolicyAllTargetPeers, st.Tasks[0].CompletionPolicy)
	require.Equal(t, []TaskParticipantProgress{
		{NodeID: 1, Status: TaskParticipantStatusPending},
		{NodeID: 2, Status: TaskParticipantStatusPending},
		{NodeID: 3, Status: TaskParticipantStatusPending},
	}, st.Tasks[0].ParticipantProgress)
}

func TestClusterStateCloneCopiesParticipantProgress(t *testing.T) {
	st := testState()
	st.Tasks = []ReconcileTask{{
		TaskID:           "slot-1-bootstrap-1",
		SlotID:           1,
		Kind:             TaskKindBootstrap,
		Step:             TaskStepCreateSlot,
		TargetNode:       1,
		TargetPeers:      []uint64{1, 2, 3},
		CompletionPolicy: TaskCompletionPolicyAllTargetPeers,
		ParticipantProgress: []TaskParticipantProgress{
			{NodeID: 1, Status: TaskParticipantStatusPending},
		},
		ConfigEpoch: 1,
		Status:      TaskStatusPending,
	}}

	clone := st.Clone()
	clone.Tasks[0].ParticipantProgress[0].Status = TaskParticipantStatusDone

	require.Equal(t, TaskParticipantStatusPending, st.Tasks[0].ParticipantProgress[0].Status)
}

func TestClusterStateNodeHealthReportsCloneNormalizeValidate(t *testing.T) {
	st := testState()
	st.NodeHealthReports = []NodeHealthReport{
		{NodeID: 2, Status: NodeStatusAlive, RuntimeReady: true, ObservedControlRevision: 7, ObservedSlotRevision: 11, ReportSeq: 3, ReportedAtUnixMilli: 1710000002000},
		{NodeID: 1, Status: NodeStatusSuspect, RuntimeReady: false, ObservedControlRevision: 6, ReportSeq: 4, ReportedAtUnixMilli: 1710000001000, ErrorCode: "runtime_starting"},
	}

	clone := st.Clone()
	clone.NodeHealthReports[0].Status = NodeStatusDown
	require.Equal(t, NodeStatusAlive, st.NodeHealthReports[0].Status, "Clone() shared NodeHealthReports backing array")

	st.Normalize()
	require.Equal(t, uint64(1), st.NodeHealthReports[0].NodeID)
	require.NoError(t, st.Validate())
}

func TestClusterStateRejectsInvalidNodeHealthReports(t *testing.T) {
	tests := []struct {
		name    string
		reports []NodeHealthReport
	}{
		{
			name:    "unknown node",
			reports: []NodeHealthReport{{NodeID: 99, Status: NodeStatusAlive, ReportedAtUnixMilli: 1710000000000}},
		},
		{
			name:    "invalid status",
			reports: []NodeHealthReport{{NodeID: 1, Status: NodeStatus("bad"), ReportedAtUnixMilli: 1710000000000}},
		},
		{
			name:    "duplicate node",
			reports: []NodeHealthReport{{NodeID: 1, Status: NodeStatusAlive, ReportedAtUnixMilli: 1710000000000}, {NodeID: 1, Status: NodeStatusSuspect, ReportedAtUnixMilli: 1710000001000}},
		},
		{
			name:    "zero node id",
			reports: []NodeHealthReport{{NodeID: 0, Status: NodeStatusAlive, ReportedAtUnixMilli: 1710000000000}},
		},
		{
			name:    "negative timestamp",
			reports: []NodeHealthReport{{NodeID: 1, Status: NodeStatusAlive, ReportedAtUnixMilli: -1}},
		},
		{
			name:    "oversized error code",
			reports: []NodeHealthReport{{NodeID: 1, Status: NodeStatusAlive, ReportedAtUnixMilli: 1710000000000, ErrorCode: strings.Repeat("x", 129)}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := testState()
			st.NodeHealthReports = tt.reports

			require.ErrorIs(t, st.Validate(), ErrInvalidState)
		})
	}
}

func TestChecksumIncludesNodeHealthReports(t *testing.T) {
	left := testState()
	right := left.Clone()
	left.NodeHealthReports = []NodeHealthReport{{NodeID: 1, Status: NodeStatusAlive, RuntimeReady: true, ReportSeq: 1, ReportedAtUnixMilli: 1710000000000}}
	right.NodeHealthReports = []NodeHealthReport{{NodeID: 1, Status: NodeStatusDown, RuntimeReady: false, ReportSeq: 2, ReportedAtUnixMilli: 1710000001000}}

	leftChecksum, err := Checksum(left)
	require.NoError(t, err)
	rightChecksum, err := Checksum(right)
	require.NoError(t, err)
	require.NotEqual(t, leftChecksum, rightChecksum, "Checksum() ignored NodeHealthReports")
}

func TestEncodeDecodePreservesNodeHealthReports(t *testing.T) {
	st := testState()
	st.NodeHealthReports = []NodeHealthReport{
		{NodeID: 3, Status: NodeStatusDown, RuntimeReady: false, ObservedControlRevision: 8, ObservedSlotRevision: 13, ReportSeq: 5, ReportedAtUnixMilli: 1710000003000, AppliedRaftIndex: 21, ErrorCode: "disk_full"},
		{NodeID: 1, Status: NodeStatusAlive, RuntimeReady: true, ObservedControlRevision: 9, ObservedSlotRevision: 14, ReportSeq: 6, ReportedAtUnixMilli: 1710000001000, AppliedRaftIndex: 22},
	}

	data, err := Encode(st)
	require.NoError(t, err)
	require.Contains(t, string(data), `"node_health_reports":[`)

	decoded, err := Decode(data)
	require.NoError(t, err)
	require.Equal(t, []NodeHealthReport{
		{NodeID: 1, Status: NodeStatusAlive, RuntimeReady: true, ObservedControlRevision: 9, ObservedSlotRevision: 14, ReportSeq: 6, ReportedAtUnixMilli: 1710000001000, AppliedRaftIndex: 22},
		{NodeID: 3, Status: NodeStatusDown, RuntimeReady: false, ObservedControlRevision: 8, ObservedSlotRevision: 13, ReportSeq: 5, ReportedAtUnixMilli: 1710000003000, AppliedRaftIndex: 21, ErrorCode: "disk_full"},
	}, decoded.NodeHealthReports)
	require.NotEmpty(t, decoded.Checksum)
}

func TestChecksumNormalizesNodeHealthReportsOrder(t *testing.T) {
	left := testState()
	left.NodeHealthReports = []NodeHealthReport{
		{NodeID: 3, Status: NodeStatusSuspect, RuntimeReady: false, ReportSeq: 2, ReportedAtUnixMilli: 1710000003000, ErrorCode: "runtime_starting"},
		{NodeID: 1, Status: NodeStatusAlive, RuntimeReady: true, ReportSeq: 1, ReportedAtUnixMilli: 1710000001000},
	}
	right := testState()
	right.NodeHealthReports = []NodeHealthReport{
		{NodeID: 1, Status: NodeStatusAlive, RuntimeReady: true, ReportSeq: 1, ReportedAtUnixMilli: 1710000001000},
		{NodeID: 3, Status: NodeStatusSuspect, RuntimeReady: false, ReportSeq: 2, ReportedAtUnixMilli: 1710000003000, ErrorCode: "runtime_starting"},
	}

	leftChecksum, err := Checksum(left)
	require.NoError(t, err)
	rightChecksum, err := Checksum(right)
	require.NoError(t, err)
	require.Equal(t, leftChecksum, rightChecksum)
}

func TestDecodeAcceptsStateWithoutNodeHealthReports(t *testing.T) {
	data, err := Encode(testState())
	require.NoError(t, err)
	require.NotContains(t, string(data), `"node_health_reports"`)

	decoded, err := Decode(data)
	require.NoError(t, err)
	require.Empty(t, decoded.NodeHealthReports)
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

func TestValidateRejectsNonContiguousPendingErasureLedgerCommit(t *testing.T) {
	st := testState()
	st.Backup = &BackupCoordinationState{
		RestorePoints: []BackupRestorePoint{},
		ErasureStreams: []BackupErasureStreamState{{
			HashSlot: 1,
			Head: &backupartifact.ErasureStreamHead{
				HashSlot: 1, Sequence: 7, CommitKey: backupartifact.ErasureLedgerCommitKey(strings.Repeat("e", 64), 1, 7), CommitSHA256: strings.Repeat("c", 64),
			},
			Pending: &BackupErasureLedgerReference{
				HashSlot: 1, Sequence: 9, EventID: strings.Repeat("a", 64), RecordKey: "erasure-ledger/events/0001/" + strings.Repeat("a", 64) + ".json", RecordSHA256: strings.Repeat("b", 64),
			},
		}},
	}

	require.ErrorIs(t, st.Validate(), ErrInvalidState)
}

func TestValidateRejectsMissingErasureLedgerFence(t *testing.T) {
	st := testState()
	partitions := make([]RestorePartition, st.Config.HashSlotCount)
	for hashSlot := range partitions {
		partitions[hashSlot].HashSlot = uint16(hashSlot)
	}
	st.Restore = &RestoreCoordinationState{Plan: &RestorePlan{
		ID: "plan-missing-erasure-fence", RestorePointID: "restore-1",
		ManifestSHA256: strings.Repeat("c", 64), Repository: "primary",
		SourceClusterID: "cluster-a", SourceGeneration: "generation-a",
		TargetClusterID: st.ClusterID, TargetGeneration: "generation-b",
		HashSlotCount: st.Config.HashSlotCount, Status: RestoreStatusPlanned,
		CreatedAtUnixMillis: 1710000000000, UpdatedAtUnixMillis: 1710000000000,
		Partitions: partitions,
	}}

	require.ErrorIs(t, st.Validate(), ErrInvalidState)
}

func TestEncodeDecodePreservesBoundedSlotFrontiers(t *testing.T) {
	st := testState()
	segmentID := strings.Repeat("a", 64)
	cursorID := strings.Repeat("c", 64)
	baselineCursorID := strings.Repeat("f", 64)
	st.Backup = &BackupCoordinationState{
		RestorePoints:  []BackupRestorePoint{},
		PendingGarbage: []BackupRestorePoint{},
		CatalogHead: &BackupCatalogPageReference{
			Sequence: 3, Key: "catalog/pages/00000000000000000003-checkpoint-3.json",
			SHA256: strings.Repeat("e", 64), Bytes: 512, LatestCheckpointID: "checkpoint-3",
		},
		GenerationGCCursors: []BackupGenerationGCCursor{{
			Repository: "primary", Revision: 2, CycleID: "gc-cycle-1",
			AfterKey:         "objects/generation-old/00001/object.bin",
			CutoffUnixMillis: 1_753_400_000_000, UpdatedAtUnixMillis: 1_753_400_110_000,
		}},
		SlotFrontiers: []BackupSlotFrontier{{
			Revision: 1, HashSlot: 1, Generation: "slot-generation-1", SourceSlotID: 2,
			GenerationStartedAtUnixMillis: 1_753_400_100_000,
			SourcePinStartedAtUnixMillis:  1_753_400_100_000,
			Lease: BackupSlotCaptureLease{
				SlotID: 2, LeaderTerm: 7, ConfigEpoch: 3, HolderNodeID: 1,
				Generation: "slot-generation-1", Sequence: 1,
				AcquiredAtUnixMillis: 1_753_400_100_000,
			},
			Baseline: &BackupSlotBaselineReference{PlaintextBytes: 256, Partition: BackupPartitionReference{
				HashSlot: 1, Key: "partition-manifests/rebase-1/00001.json",
				SHA256: strings.Repeat("9", 64), Bytes: 1024,
				ObjectCount: 3, CiphertextBytes: 2048,
				Evidence: backupartifact.PartitionEvidence{Version: backupartifact.PartitionEvidenceVersion},
			}},
			Rebase: &BackupSlotRebase{
				TargetGeneration: "rebase-00001-00000000000000000002",
				Epoch:            2, Reason: "pin_age", StartedAtUnixMillis: 1_753_400_105_000,
			},
			Metadata: BackupStreamFrontier{
				SourceHighWatermark: 3, SourceCursor: "metadata/3",
				WatermarkAtUnixMillis: 1_753_400_100_000,
			},
			Messages: BackupStreamFrontier{
				Sequence: 1, SourceHighWatermark: 5, SourceCursor: "messages/5",
				WatermarkAtUnixMillis: 1_753_400_090_000,
				Head: &BackupSegmentReference{
					SegmentID: segmentID, CommitKey: "segments/" + segmentID + "/commit.json",
					CommitSHA256: strings.Repeat("b", 64), PlaintextBytes: 1,
				},
				CursorHead: &BackupSegmentReference{
					SegmentID: cursorID, CommitKey: "segments/" + cursorID + "/commit.json",
					CommitSHA256: strings.Repeat("d", 64), PlaintextBytes: 1,
				},
				BaselineCursorHead: &BackupSegmentReference{
					SegmentID: baselineCursorID, CommitKey: "segments/" + baselineCursorID + "/commit.json",
					CommitSHA256: strings.Repeat("8", 64), PlaintextBytes: 1,
				},
			},
			WatermarkAtUnixMillis: 1_753_400_090_000,
			UpdatedAtUnixMillis:   1_753_400_110_000,
		}},
	}

	body, err := Encode(st)
	require.NoError(t, err)
	decoded, err := Decode(body)
	require.NoError(t, err)
	require.Len(t, decoded.Backup.SlotFrontiers, 1)
	require.Equal(t, uint64(5), decoded.Backup.SlotFrontiers[0].Messages.SourceHighWatermark)
	require.Equal(t, segmentID, decoded.Backup.SlotFrontiers[0].Messages.Head.SegmentID)
	require.Equal(t, cursorID, decoded.Backup.SlotFrontiers[0].Messages.CursorHead.SegmentID)
	require.Equal(t, baselineCursorID, decoded.Backup.SlotFrontiers[0].Messages.BaselineCursorHead.SegmentID)
	require.Equal(t, uint64(2), decoded.Backup.SlotFrontiers[0].Rebase.Epoch)
	require.Equal(t, "partition-manifests/rebase-1/00001.json", decoded.Backup.SlotFrontiers[0].Baseline.Partition.Key)
	require.Equal(t, uint64(3), decoded.Backup.CatalogHead.Sequence)
	require.Equal(t, "checkpoint-3", decoded.Backup.CatalogHead.LatestCheckpointID)
	require.Equal(t, uint64(2), decoded.Backup.GenerationGCCursors[0].Revision)
	require.Equal(t, "objects/generation-old/00001/object.bin", decoded.Backup.GenerationGCCursors[0].AfterKey)
}

func TestValidateRejectsMalformedSlotCaptureLease(t *testing.T) {
	validLease := BackupSlotCaptureLease{
		SlotID: 2, LeaderTerm: 7, ConfigEpoch: 3, HolderNodeID: 1,
		Generation: "slot-generation-1", Sequence: 1,
		AcquiredAtUnixMillis: 1_753_400_100_000,
	}
	tests := []struct {
		name   string
		mutate func(*BackupSlotCaptureLease)
	}{
		{name: "missing holder", mutate: func(lease *BackupSlotCaptureLease) { lease.HolderNodeID = 0 }},
		{name: "missing leader term", mutate: func(lease *BackupSlotCaptureLease) { lease.LeaderTerm = 0 }},
		{name: "missing config epoch", mutate: func(lease *BackupSlotCaptureLease) { lease.ConfigEpoch = 0 }},
		{name: "missing sequence", mutate: func(lease *BackupSlotCaptureLease) { lease.Sequence = 0 }},
		{name: "generation mismatch", mutate: func(lease *BackupSlotCaptureLease) { lease.Generation = "other-generation" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			st := testState()
			lease := validLease
			test.mutate(&lease)
			st.Backup = &BackupCoordinationState{
				RestorePoints: []BackupRestorePoint{},
				SlotFrontiers: []BackupSlotFrontier{{
					Revision: 1, HashSlot: 1, Generation: "slot-generation-1",
					Lease: lease, UpdatedAtUnixMillis: 1_753_400_100_000,
				}},
			}
			require.ErrorIs(t, st.Validate(), ErrInvalidState)
		})
	}
}

func TestValidateRejectsMalformedCheckpointCatalogHead(t *testing.T) {
	st := testState()
	st.Backup = &BackupCoordinationState{
		RestorePoints: []BackupRestorePoint{},
		CatalogHead: &BackupCatalogPageReference{
			Sequence: 2, Key: "catalog/pages/00000000000000000001-checkpoint-2.json",
			SHA256: strings.Repeat("a", 64), Bytes: 512, LatestCheckpointID: "checkpoint-2",
		},
	}

	require.ErrorIs(t, st.Validate(), ErrInvalidState)
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
	require.NotContains(t, string(data), `"node_health_reports"`)
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

func validLeaderTransferTask() ReconcileTask {
	return ReconcileTask{
		TaskID:           "slot-1-leader-transfer-7-r9",
		SlotID:           1,
		Kind:             TaskKindLeaderTransfer,
		Step:             TaskStepTransferLeader,
		SourceNode:       1,
		TargetNode:       2,
		TargetPeers:      []uint64{1, 2, 3},
		CompletionPolicy: TaskCompletionPolicySingleObserver,
		ConfigEpoch:      7,
		Status:           TaskStatusPending,
	}
}

func nodeIDs(nodes []Node) []uint64 {
	out := make([]uint64, 0, len(nodes))
	for _, node := range nodes {
		out = append(out, node.NodeID)
	}
	return out
}
