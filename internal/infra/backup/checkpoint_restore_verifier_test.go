package backup_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	backupinfra "github.com/WuKongIM/WuKongIM/internal/infra/backup"
	backupusecase "github.com/WuKongIM/WuKongIM/internal/usecase/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	clusterpkg "github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
	"github.com/stretchr/testify/require"
)

func TestCheckpointRestoreFinalVerifierChecksCurrentThreeReplicaState(
	t *testing.T,
) {
	node := &checkpointFinalVerifierNode{
		nodeID: 1,
		snapshot: control.Snapshot{
			ClusterID: "target-cluster",
			HashSlots: control.HashSlotTable{
				Count:  1,
				Ranges: []control.HashSlotRange{{From: 0, To: 0, SlotID: 11}},
			},
			Slots: []control.SlotAssignment{{
				SlotID: 11, DesiredPeers: []uint64{1, 2, 3}, ConfigEpoch: 4,
			}},
			Nodes: []control.Node{
				{NodeID: 1, Roles: []control.Role{control.RoleData}, JoinState: control.NodeJoinStateActive},
				{NodeID: 2, Roles: []control.Role{control.RoleData}, JoinState: control.NodeJoinStateActive},
				{NodeID: 3, Roles: []control.Role{control.RoleData}, JoinState: control.NodeJoinStateActive},
			},
		},
		route: clusterpkg.Route{
			HashSlot: 0, SlotID: 11, Leader: 2, LeaderTerm: 8,
			ConfigEpoch: 4, Peers: []uint64{3, 1, 2},
		},
	}
	metadataSHA := strings.Repeat("a", 64)
	local := &checkpointFinalVerifierLocal{
		response: backupcontract.CheckpointReplicaResponse{
			Completed: true, MetadataSHA256: metadataSHA, InstalledBytes: 4096,
		},
	}
	remote := &checkpointFinalVerifierRemote{
		responses: map[uint64]backupcontract.CheckpointReplicaResponse{
			2: {Completed: true, MetadataSHA256: metadataSHA, InstalledBytes: 4096},
			3: {Completed: true, MetadataSHA256: metadataSHA, InstalledBytes: 4096},
		},
	}
	verifier, err := backupinfra.NewCheckpointRestoreFinalVerifier(
		backupinfra.CheckpointRestoreFinalVerifierOptions{
			Node: node, Local: local, Remote: remote, MaxParallel: 4,
		},
	)
	require.NoError(t, err)
	plan := checkpointFinalVerifierPlan(metadataSHA)

	verified, err := verifier.VerifyRestore(context.Background(), plan)
	require.NoError(t, err)
	require.Len(t, verified, 1)
	require.True(t, verified[0].Verified)
	require.Equal(t, uint64(1), verified[0].LeaderNodeID,
		"install evidence remains immutable after a safe Leader change")
	require.Len(t, local.requests, 1)
	require.Len(t, remote.requests, 2)
	for _, request := range append(
		append([]backupcontract.CheckpointReplicaRequest(nil), local.requests...),
		remote.requests...,
	) {
		require.Equal(t, backupcontract.CheckpointReplicaStatus, request.Action)
		require.Equal(t, uint64(2), request.Fence.LeaderNodeID)
		require.Equal(t, uint64(8), request.Fence.LeaderTerm)
		require.Equal(t, uint64(7), request.Fence.Attempt)
	}
}

func TestCheckpointRestoreFinalVerifierRejectsReplicaEvidenceMismatch(
	t *testing.T,
) {
	node := &checkpointFinalVerifierNode{
		nodeID: 1,
		snapshot: control.Snapshot{
			ClusterID: "target-cluster",
			HashSlots: control.HashSlotTable{
				Count:  1,
				Ranges: []control.HashSlotRange{{From: 0, To: 0, SlotID: 11}},
			},
			Slots: []control.SlotAssignment{{
				SlotID: 11, DesiredPeers: []uint64{1, 2, 3}, ConfigEpoch: 4,
			}},
			Nodes: []control.Node{
				{NodeID: 1, Roles: []control.Role{control.RoleData}, JoinState: control.NodeJoinStateActive},
				{NodeID: 2, Roles: []control.Role{control.RoleData}, JoinState: control.NodeJoinStateActive},
				{NodeID: 3, Roles: []control.Role{control.RoleData}, JoinState: control.NodeJoinStateActive},
			},
		},
		route: clusterpkg.Route{
			HashSlot: 0, SlotID: 11, Leader: 2, LeaderTerm: 8,
			ConfigEpoch: 4, Peers: []uint64{1, 2, 3},
		},
	}
	metadataSHA := strings.Repeat("a", 64)
	local := &checkpointFinalVerifierLocal{
		response: backupcontract.CheckpointReplicaResponse{
			Completed: true, MetadataSHA256: metadataSHA, InstalledBytes: 4096,
		},
	}
	remote := &checkpointFinalVerifierRemote{
		responses: map[uint64]backupcontract.CheckpointReplicaResponse{
			2: {Completed: true, MetadataSHA256: strings.Repeat("b", 64), InstalledBytes: 4096},
			3: {Completed: true, MetadataSHA256: metadataSHA, InstalledBytes: 4096},
		},
	}
	verifier, err := backupinfra.NewCheckpointRestoreFinalVerifier(
		backupinfra.CheckpointRestoreFinalVerifierOptions{
			Node: node, Local: local, Remote: remote, MaxParallel: 4,
		},
	)
	require.NoError(t, err)

	_, err = verifier.VerifyRestore(
		context.Background(), checkpointFinalVerifierPlan(metadataSHA),
	)
	require.ErrorContains(t, err, "conflicting semantic evidence")
}

func TestCheckpointRestoreActivationCleanerTargetsEveryCurrentReplica(
	t *testing.T,
) {
	node := &checkpointFinalVerifierNode{
		nodeID: 1,
		snapshot: control.Snapshot{
			ClusterID: "target-cluster",
			HashSlots: control.HashSlotTable{
				Count:  1,
				Ranges: []control.HashSlotRange{{From: 0, To: 0, SlotID: 11}},
			},
			Slots: []control.SlotAssignment{{
				SlotID: 11, DesiredPeers: []uint64{1, 2, 3}, ConfigEpoch: 4,
			}},
			Nodes: []control.Node{
				{NodeID: 1, Roles: []control.Role{control.RoleData}, JoinState: control.NodeJoinStateActive},
				{NodeID: 2, Roles: []control.Role{control.RoleData}, JoinState: control.NodeJoinStateActive},
				{NodeID: 3, Roles: []control.Role{control.RoleData}, JoinState: control.NodeJoinStateActive},
			},
		},
	}
	local := &checkpointFinalVerifierLocal{
		response: backupcontract.CheckpointReplicaResponse{Completed: true},
	}
	remote := &checkpointFinalVerifierRemote{
		responses: map[uint64]backupcontract.CheckpointReplicaResponse{
			2: {Completed: true},
			3: {Completed: true},
		},
		failures: map[uint64]int{3: 2},
	}
	cleaner, err := backupinfra.NewCheckpointRestoreActivationCleaner(
		backupinfra.CheckpointRestoreActivationCleanerOptions{
			Node: node, Local: local, Remote: remote, MaxParallel: 3,
		},
	)
	require.NoError(t, err)
	plan := checkpointFinalVerifierPlan(strings.Repeat("a", 64))
	plan.Status = backupusecase.RestoreStatusActivating
	audit := backupartifact.BreakGlassActivationAudit{
		ID: "audit-1", RestorePlanID: plan.ID,
		Operator:               "recovery-admin",
		Reason:                 "All source Controller disks are permanently unavailable.",
		AuthorizedAtUnixMillis: 1_800_000_000_000,
	}
	digest, err := backupartifact.BreakGlassActivationDigest(audit)
	require.NoError(t, err)
	plan.Activation = &backupartifact.RestoreActivationEvidence{
		Kind:           backupartifact.RestoreActivationBreakGlass,
		EvidenceSHA256: digest, Operator: audit.Operator,
		RecordedAtUnixMillis: audit.AuthorizedAtUnixMillis,
		BreakGlass:           &audit,
	}

	require.NoError(t, cleaner.CleanupRestoreStaging(
		context.Background(), plan,
	))
	require.Len(t, local.requests, 1)
	require.Len(t, remote.requests, 4)
	for _, request := range append(
		append([]backupcontract.CheckpointReplicaRequest(nil), local.requests...),
		remote.requests...,
	) {
		require.Equal(t, backupcontract.CheckpointReplicaCleanup, request.Action)
		require.Equal(t, plan.ID, request.Fence.PlanID)
		require.Equal(t, uint64(7), request.Fence.Attempt)
	}
}

func checkpointFinalVerifierPlan(
	metadataSHA string,
) backupusecase.RestorePlan {
	checkpointID := "checkpoint-restore-1"
	checkpointSHA := strings.Repeat("d", 64)
	vectorID := strings.Repeat("e", 64)
	checkpoint := backupartifact.CatalogCheckpointReference{
		ID: checkpointID, Key: backupartifact.CheckpointObjectKey(checkpointID),
		SHA256: checkpointSHA, Bytes: 100,
		CreatedAtUnixMillis:   1_753_400_201_000,
		EffectiveAtUnixMillis: 1_753_400_200_000,
		GenerationVector: backupartifact.GenerationVectorReference{
			ID: vectorID, Key: backupartifact.GenerationVectorObjectKey(vectorID),
			SHA256: strings.Repeat("f", 64), Bytes: 100, HashSlotCount: 1,
		},
	}
	page := backupartifact.CatalogPageReference{
		Sequence: 1,
		Key:      backupartifact.CatalogPageObjectKey(1, checkpointID),
		SHA256:   strings.Repeat("c", 64), Bytes: 100,
		LatestCheckpointID: checkpointID,
	}
	return backupusecase.RestorePlan{
		ID: "plan-restore-1", CheckpointID: checkpointID,
		CheckpointSHA256: checkpointSHA,
		CatalogProof: &backupartifact.CheckpointCatalogProof{
			Head: page, EntryPage: page, Checkpoint: checkpoint,
		},
		TargetClusterID:  "target-cluster",
		TargetGeneration: "target-generation-1",
		HashSlotCount:    1,
		Partitions: []backupusecase.RestorePartition{{
			HashSlot: 0, Status: backupcontract.RestorePartitionConverged,
			TargetSlotID: 11, LeaderNodeID: 1, LeaderTerm: 7,
			ConfigEpoch: 4, InstallAttempt: 7,
			EvidenceVersion:     backupartifact.RestoreEvidenceVersion,
			Installed:           true,
			MetadataSHA256:      metadataSHA,
			ContentSHA256:       strings.Repeat("1", 64),
			MessageMerkleSHA256: strings.Repeat("2", 64),
			ReplicaCount:        3,
			ConvergedReplicas:   3,
		}},
	}
}

type checkpointFinalVerifierNode struct {
	nodeID   uint64
	snapshot control.Snapshot
	route    clusterpkg.Route
}

func (n *checkpointFinalVerifierNode) NodeID() uint64 { return n.nodeID }

func (n *checkpointFinalVerifierNode) LocalControlSnapshot(
	context.Context,
) (control.Snapshot, error) {
	return n.snapshot, nil
}

func (n *checkpointFinalVerifierNode) RouteHashSlot(
	hashSlot uint16,
) (clusterpkg.Route, error) {
	if n.route.HashSlot != hashSlot {
		return clusterpkg.Route{}, fmt.Errorf("route missing")
	}
	return n.route, nil
}

type checkpointFinalVerifierLocal struct {
	mu       sync.Mutex
	response backupcontract.CheckpointReplicaResponse
	requests []backupcontract.CheckpointReplicaRequest
}

func (l *checkpointFinalVerifierLocal) HandleCheckpointReplica(
	_ context.Context,
	request backupcontract.CheckpointReplicaRequest,
) (backupcontract.CheckpointReplicaResponse, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.requests = append(l.requests, request)
	return l.response, nil
}

type checkpointFinalVerifierRemote struct {
	mu        sync.Mutex
	responses map[uint64]backupcontract.CheckpointReplicaResponse
	failures  map[uint64]int
	requests  []backupcontract.CheckpointReplicaRequest
}

func (r *checkpointFinalVerifierRemote) HandleCheckpointReplica(
	_ context.Context,
	nodeID uint64,
	request backupcontract.CheckpointReplicaRequest,
) (backupcontract.CheckpointReplicaResponse, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests = append(r.requests, request)
	if r.failures[nodeID] > 0 {
		r.failures[nodeID]--
		return backupcontract.CheckpointReplicaResponse{},
			fmt.Errorf("node %d has not observed activation yet", nodeID)
	}
	response, ok := r.responses[nodeID]
	if !ok {
		return backupcontract.CheckpointReplicaResponse{},
			fmt.Errorf("node %d response missing", nodeID)
	}
	return response, nil
}
