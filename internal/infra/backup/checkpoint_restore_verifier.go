package backup

import (
	"context"
	"fmt"
	"sort"
	"sync"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	backupusecase "github.com/WuKongIM/WuKongIM/internal/usecase/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
)

// LocalCheckpointRestoreReplicaStatus verifies one local replica receipt and
// the currently installed target state without repository or KMS access.
type LocalCheckpointRestoreReplicaStatus interface {
	HandleCheckpointReplica(
		context.Context,
		backupcontract.CheckpointReplicaRequest,
	) (backupcontract.CheckpointReplicaResponse, error)
}

// CheckpointRestoreFinalVerifierOptions configures target-only semantic
// verification after every logical Slot has converged.
type CheckpointRestoreFinalVerifierOptions struct {
	// Node supplies current target topology and Slot authority.
	Node RestoreInstallClusterNode
	// Local and Remote query each desired replica's durable receipt and live
	// semantic state. Neither dependency has repository or KMS access.
	Local  LocalCheckpointRestoreReplicaStatus
	Remote RemoteCheckpointRestoreReplicaClient
	// MaxParallel bounds independently verified logical Slots.
	MaxParallel int
}

// CheckpointRestoreFinalVerifier proves the live target replica set still
// matches every installed checkpoint receipt before activation is allowed.
type CheckpointRestoreFinalVerifier struct {
	node        RestoreInstallClusterNode
	local       LocalCheckpointRestoreReplicaStatus
	remote      RemoteCheckpointRestoreReplicaClient
	maxParallel int
}

// NewCheckpointRestoreFinalVerifier creates a repository-independent final
// verifier.
func NewCheckpointRestoreFinalVerifier(
	options CheckpointRestoreFinalVerifierOptions,
) (*CheckpointRestoreFinalVerifier, error) {
	if options.Node == nil || options.Node.NodeID() == 0 ||
		options.Local == nil || options.Remote == nil ||
		options.MaxParallel <= 0 || options.MaxParallel > 64 {
		return nil, fmt.Errorf(
			"backup checkpoint restore final verifier: invalid options",
		)
	}
	return &CheckpointRestoreFinalVerifier{
		node: options.Node, local: options.Local,
		remote: options.Remote, maxParallel: options.MaxParallel,
	}, nil
}

// VerifyRestore queries every current desired replica and returns the original
// ordered install evidence with only the Verified bit advanced.
func (v *CheckpointRestoreFinalVerifier) VerifyRestore(
	ctx context.Context,
	plan backupusecase.RestorePlan,
) ([]backupusecase.RestorePartition, error) {
	if v == nil || plan.ID == "" || plan.RestorePointID == "" ||
		plan.TargetClusterID == "" || plan.TargetGeneration == "" ||
		plan.HashSlotCount == 0 ||
		len(plan.Partitions) != int(plan.HashSlotCount) ||
		plan.CatalogProof == nil ||
		backupartifact.ValidateCheckpointCatalogProof(*plan.CatalogProof) != nil ||
		plan.CatalogProof.Checkpoint.ID != plan.RestorePointID ||
		plan.CatalogProof.Checkpoint.SHA256 != plan.ManifestSHA256 ||
		!validLowerSHA256(plan.ManifestSHA256) {
		return nil, backupusecase.ErrInvalidRequest
	}
	snapshot, err := v.node.LocalControlSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	if snapshot.ClusterID != plan.TargetClusterID ||
		snapshot.HashSlots.Count != plan.HashSlotCount {
		return nil, fmt.Errorf(
			"backup checkpoint restore final verifier: target topology fence mismatch",
		)
	}
	placement, err := newRestoreReplicaPlacement(
		snapshot, plan.HashSlotCount, v.node.NodeID(),
	)
	if err != nil {
		return nil, fmt.Errorf(
			"backup checkpoint restore final verifier: %w", err,
		)
	}

	type result struct {
		hashSlot uint16
		err      error
	}
	work := make(chan uint16)
	results := make(chan result, int(plan.HashSlotCount))
	workers := v.maxParallel
	if workers > int(plan.HashSlotCount) {
		workers = int(plan.HashSlotCount)
	}
	var group sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		group.Add(1)
		go func() {
			defer group.Done()
			for hashSlot := range work {
				verifyErr := v.verifyPartition(
					ctx, plan, placement, hashSlot,
				)
				results <- result{hashSlot: hashSlot, err: verifyErr}
			}
		}()
	}
	go func() {
		defer close(work)
		for hashSlot := uint16(0); hashSlot < plan.HashSlotCount; hashSlot++ {
			select {
			case work <- hashSlot:
			case <-ctx.Done():
				return
			}
		}
	}()
	group.Wait()
	close(results)
	errorsBySlot := make([]error, plan.HashSlotCount)
	for item := range results {
		errorsBySlot[item.hashSlot] = item.err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for hashSlot, verifyErr := range errorsBySlot {
		if verifyErr != nil {
			return nil, fmt.Errorf(
				"backup checkpoint restore final verifier: Hash Slot %d: %w",
				hashSlot, verifyErr,
			)
		}
	}
	verified := append([]backupusecase.RestorePartition(nil), plan.Partitions...)
	for index := range verified {
		verified[index].Verified = true
		verified[index].FailureCategory = ""
	}
	return verified, nil
}

func (v *CheckpointRestoreFinalVerifier) verifyPartition(
	ctx context.Context,
	plan backupusecase.RestorePlan,
	placement *restoreReplicaPlacement,
	hashSlot uint16,
) error {
	partition := plan.Partitions[hashSlot]
	if partition.HashSlot != hashSlot ||
		partition.Status != backupcontract.RestorePartitionConverged ||
		!partition.Installed ||
		partition.EvidenceVersion != backupartifact.RestoreEvidenceVersion ||
		partition.InstallAttempt == 0 ||
		partition.ReplicaCount == 0 ||
		partition.ConvergedReplicas != partition.ReplicaCount ||
		!validLowerSHA256(partition.MetadataSHA256) ||
		!validLowerSHA256(partition.ContentSHA256) ||
		!validLowerSHA256(partition.MessageMerkleSHA256) {
		return backupusecase.ErrStateConflict
	}
	desired, err := placement.nodeIDs(hashSlot)
	if err != nil {
		return err
	}
	configEpoch, err := placement.configEpoch(hashSlot)
	if err != nil {
		return err
	}
	route, err := v.node.RouteHashSlot(hashSlot)
	if err != nil {
		return err
	}
	if route.HashSlot != hashSlot ||
		route.SlotID != partition.TargetSlotID ||
		route.SlotID != placement.slotByHashSlot[hashSlot] ||
		route.Leader == 0 || route.LeaderTerm == 0 ||
		route.ConfigEpoch != partition.ConfigEpoch ||
		route.ConfigEpoch != configEpoch ||
		len(route.Peers) != int(partition.ReplicaCount) ||
		!sameCheckpointRestoreNodeSet(route.Peers, desired) ||
		!containsRestoreNode(route.Peers, route.Leader) {
		return fmt.Errorf("current Slot replica fence changed")
	}
	fence := CheckpointRestoreInstallFence{
		PlanID: plan.ID, CheckpointID: plan.RestorePointID,
		CheckpointSHA256: plan.ManifestSHA256,
		TargetGeneration: plan.TargetGeneration,
		HashSlot:         hashSlot, TargetSlotID: route.SlotID,
		ReplicaCount: uint32(len(route.Peers)),
		LeaderNodeID: route.Leader, LeaderTerm: route.LeaderTerm,
		ConfigEpoch: route.ConfigEpoch, Attempt: partition.InstallAttempt,
		InvalidateTokens: plan.InvalidateTokens,
	}
	request := backupcontract.CheckpointReplicaRequest{
		Action: backupcontract.CheckpointReplicaStatus,
		Fence:  checkpointRestoreFenceToContract(fence),
	}
	peers := append([]uint64(nil), route.Peers...)
	sort.Slice(peers, func(left, right int) bool {
		return peers[left] < peers[right]
	})
	var installedBytes uint64
	for _, nodeID := range peers {
		response, err := v.replicaStatus(ctx, nodeID, request)
		if err != nil {
			return fmt.Errorf("node %d: %w", nodeID, err)
		}
		if !response.Completed ||
			response.MetadataSHA256 != partition.MetadataSHA256 ||
			response.InstalledBytes == 0 ||
			(installedBytes != 0 &&
				response.InstalledBytes != installedBytes) {
			return fmt.Errorf(
				"node %d returned conflicting semantic evidence", nodeID,
			)
		}
		installedBytes = response.InstalledBytes
	}
	return nil
}

func (v *CheckpointRestoreFinalVerifier) replicaStatus(
	ctx context.Context,
	nodeID uint64,
	request backupcontract.CheckpointReplicaRequest,
) (backupcontract.CheckpointReplicaResponse, error) {
	if nodeID == v.node.NodeID() {
		return v.local.HandleCheckpointReplica(ctx, request)
	}
	return v.remote.HandleCheckpointReplica(ctx, nodeID, request)
}

func sameCheckpointRestoreNodeSet(left, right []uint64) bool {
	if len(left) != len(right) ||
		restoreNodeIDsContainDuplicate(left) ||
		restoreNodeIDsContainDuplicate(right) {
		return false
	}
	leftCopy := append([]uint64(nil), left...)
	rightCopy := append([]uint64(nil), right...)
	sort.Slice(leftCopy, func(i, j int) bool { return leftCopy[i] < leftCopy[j] })
	sort.Slice(rightCopy, func(i, j int) bool { return rightCopy[i] < rightCopy[j] })
	for index := range leftCopy {
		if leftCopy[index] != rightCopy[index] {
			return false
		}
	}
	return true
}

var _ backupusecase.RestoreFinalVerifier = (*CheckpointRestoreFinalVerifier)(nil)
