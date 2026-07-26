package backup

import (
	"context"
	"fmt"
	"time"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	backupusecase "github.com/WuKongIM/WuKongIM/internal/usecase/backup"
	"golang.org/x/sync/errgroup"
)

const checkpointRestoreCleanupPropagationTimeout = 5 * time.Second

// CheckpointRestoreActivationCleanerOptions configures cluster-wide,
// plan-bound plaintext staging cleanup.
type CheckpointRestoreActivationCleanerOptions struct {
	// Node supplies one consistent current target placement.
	Node RestoreInstallClusterNode
	// Local and Remote execute the same Controller-authorized cleanup action.
	Local  LocalCheckpointRestoreReplicaStatus
	Remote RemoteCheckpointRestoreReplicaClient
	// MaxParallel bounds node cleanup RPCs.
	MaxParallel int
}

// CheckpointRestoreActivationCleaner removes every replica attempt only after
// the Controller contains matching activating or activated evidence.
type CheckpointRestoreActivationCleaner struct {
	node        RestoreInstallClusterNode
	local       LocalCheckpointRestoreReplicaStatus
	remote      RemoteCheckpointRestoreReplicaClient
	maxParallel int
}

// NewCheckpointRestoreActivationCleaner creates a fail-closed cleaner.
func NewCheckpointRestoreActivationCleaner(
	options CheckpointRestoreActivationCleanerOptions,
) (*CheckpointRestoreActivationCleaner, error) {
	if options.Node == nil || options.Node.NodeID() == 0 ||
		options.Local == nil || options.Remote == nil ||
		options.MaxParallel <= 0 || options.MaxParallel > 64 {
		return nil, fmt.Errorf(
			"backup checkpoint restore activation cleaner: invalid options",
		)
	}
	return &CheckpointRestoreActivationCleaner{
		node: options.Node, local: options.Local, remote: options.Remote,
		maxParallel: options.MaxParallel,
	}, nil
}

// CleanupRestoreStaging removes every plan-bound attempt from the placement
// freshly proved by the final verifier.
func (c *CheckpointRestoreActivationCleaner) CleanupRestoreStaging(
	ctx context.Context,
	plan backupusecase.RestorePlan,
) error {
	if c == nil || plan.Status != backupcontract.RestoreStatusActivating ||
		plan.Activation == nil || plan.ID == "" ||
		plan.RestorePointID == "" || plan.TargetClusterID == "" ||
		plan.TargetGeneration == "" || plan.HashSlotCount == 0 ||
		len(plan.Partitions) != int(plan.HashSlotCount) {
		return backupusecase.ErrInvalidRequest
	}
	snapshot, err := c.node.LocalControlSnapshot(ctx)
	if err != nil {
		return err
	}
	if snapshot.ClusterID != plan.TargetClusterID ||
		snapshot.HashSlots.Count != plan.HashSlotCount {
		return fmt.Errorf(
			"backup checkpoint restore activation cleaner: target topology fence mismatch",
		)
	}
	placement, err := newRestoreReplicaPlacement(
		snapshot, plan.HashSlotCount, c.node.NodeID(),
	)
	if err != nil {
		return fmt.Errorf(
			"backup checkpoint restore activation cleaner: %w", err,
		)
	}
	group, groupCtx := errgroup.WithContext(ctx)
	group.SetLimit(c.maxParallel)
	for hashSlot := uint16(0); hashSlot < plan.HashSlotCount; hashSlot++ {
		partition := plan.Partitions[hashSlot]
		nodeIDs, nodeErr := placement.nodeIDs(hashSlot)
		if nodeErr != nil {
			return nodeErr
		}
		configEpoch, epochErr := placement.configEpoch(hashSlot)
		if epochErr != nil {
			return epochErr
		}
		if partition.HashSlot != hashSlot ||
			partition.TargetSlotID != placement.slotByHashSlot[hashSlot] ||
			partition.ConfigEpoch != configEpoch ||
			partition.ReplicaCount != uint32(len(nodeIDs)) ||
			partition.ConvergedReplicas != partition.ReplicaCount {
			return fmt.Errorf(
				"backup checkpoint restore activation cleaner: partition placement changed",
			)
		}
		fence := checkpointRestoreFenceToContract(
			checkpointRestoreInstallFenceFromPlan(plan, partition),
		)
		for _, nodeID := range nodeIDs {
			nodeID := nodeID
			group.Go(func() error {
				request := backupcontract.CheckpointReplicaRequest{
					Action: backupcontract.CheckpointReplicaCleanup,
					Fence:  fence,
				}
				return c.cleanupReplicaEventually(
					groupCtx, nodeID, request,
				)
			})
		}
	}
	return group.Wait()
}

func (c *CheckpointRestoreActivationCleaner) cleanupReplicaEventually(
	ctx context.Context,
	nodeID uint64,
	request backupcontract.CheckpointReplicaRequest,
) error {
	deadline := time.Now().Add(checkpointRestoreCleanupPropagationTimeout)
	backoff := 25 * time.Millisecond
	var lastErr error
	for {
		var response backupcontract.CheckpointReplicaResponse
		if nodeID == c.node.NodeID() {
			response, lastErr = c.local.HandleCheckpointReplica(ctx, request)
		} else {
			response, lastErr = c.remote.HandleCheckpointReplica(
				ctx, nodeID, request,
			)
		}
		if lastErr == nil && response.Completed {
			return nil
		}
		if lastErr == nil {
			lastErr = fmt.Errorf("cleanup did not complete")
		}
		if time.Now().After(deadline) {
			return fmt.Errorf(
				"backup checkpoint restore activation cleaner: node %d: %w",
				nodeID, lastErr,
			)
		}
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return ctx.Err()
		case <-timer.C:
		}
		if backoff < 250*time.Millisecond {
			backoff *= 2
			if backoff > 250*time.Millisecond {
				backoff = 250 * time.Millisecond
			}
		}
	}
}

func checkpointRestoreInstallFenceFromPlan(
	plan backupusecase.RestorePlan,
	partition backupusecase.RestorePartition,
) CheckpointRestoreInstallFence {
	return CheckpointRestoreInstallFence{
		PlanID: plan.ID, CheckpointID: plan.RestorePointID,
		CheckpointSHA256: plan.ManifestSHA256,
		TargetGeneration: plan.TargetGeneration,
		HashSlot:         partition.HashSlot,
		TargetSlotID:     partition.TargetSlotID,
		ReplicaCount:     partition.ReplicaCount,
		LeaderNodeID:     partition.LeaderNodeID,
		LeaderTerm:       partition.LeaderTerm,
		ConfigEpoch:      partition.ConfigEpoch,
		Attempt:          partition.InstallAttempt,
		InvalidateTokens: plan.InvalidateTokens,
	}
}

var _ backupusecase.RestoreActivationCleaner = (*CheckpointRestoreActivationCleaner)(nil)
