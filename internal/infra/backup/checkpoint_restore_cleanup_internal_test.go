package backup

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	"github.com/WuKongIM/WuKongIM/pkg/controller"
	controllerstate "github.com/WuKongIM/WuKongIM/pkg/controller/state"
)

func TestCheckpointRestoreReplicaCleanupRequiresDurableActivation(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fence := CheckpointRestoreInstallFence{
		PlanID: "plan-1", CheckpointID: "checkpoint-1",
		CheckpointSHA256: strings.Repeat("a", 64),
		TargetGeneration: "target-generation-1",
		HashSlot:         7, TargetSlotID: 8, ReplicaCount: 3,
		LeaderNodeID: 2, LeaderTerm: 9, ConfigEpoch: 4, Attempt: 1,
	}
	attemptDir := checkpointRestoreAttemptDir(root, fence)
	if err := os.MkdirAll(attemptDir, 0o750); err != nil {
		t.Fatal(err)
	}
	plainPath := filepath.Join(attemptDir, "messages-00000.snapshot")
	if err := os.WriteFile(plainPath, []byte("plaintext"), 0o600); err != nil {
		t.Fatal(err)
	}
	quota, err := NewCheckpointRestoreStagingQuota(root, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	reader := &cleanupActivationStateReader{
		state: cleanupActivationState(fence, controllerstate.RestoreStatusVerified),
	}
	receiver := &CheckpointRestoreReplicaReceiver{
		stagingDir: root, stagingMaxBytes: 1 << 20,
		stagingQuota: quota, activationState: reader,
		reservations: make(map[string]uint64),
	}
	request := backupcontract.CheckpointReplicaRequest{
		Action: backupcontract.CheckpointReplicaCleanup,
		Fence:  checkpointRestoreFenceToContract(fence),
	}
	if _, err := receiver.HandleCheckpointReplica(
		context.Background(), request,
	); err == nil {
		t.Fatal("cleanup before durable activation authorization error = nil")
	}
	if _, err := os.Stat(plainPath); err != nil {
		t.Fatalf("unauthorized cleanup removed plaintext: %v", err)
	}

	reader.state = cleanupActivationState(
		fence, controllerstate.RestoreStatusActivating,
	)
	response, err := receiver.HandleCheckpointReplica(
		context.Background(), request,
	)
	if err != nil || !response.Completed {
		t.Fatalf("authorized cleanup response=%#v err=%v", response, err)
	}
	if _, err := os.Stat(attemptDir); !os.IsNotExist(err) {
		t.Fatalf("attempt directory still exists after cleanup: %v", err)
	}
	response, err = receiver.HandleCheckpointReplica(
		context.Background(), request,
	)
	if err != nil || !response.Completed {
		t.Fatalf("idempotent cleanup response=%#v err=%v", response, err)
	}
}

type cleanupActivationStateReader struct {
	state controller.ClusterState
}

func (r *cleanupActivationStateReader) LoadRestoreCoordinationState(
	context.Context,
) (controller.ClusterState, error) {
	return r.state.Clone(), nil
}

func cleanupActivationState(
	fence CheckpointRestoreInstallFence,
	status controllerstate.RestoreStatus,
) controller.ClusterState {
	audit := backupartifact.BreakGlassActivationAudit{
		ID: "audit-1", RestorePlanID: fence.PlanID,
		Operator:               "recovery-admin",
		Reason:                 "All source Controller disks are permanently unavailable.",
		AuthorizedAtUnixMillis: 1_800_000_000_000,
	}
	digest, _ := backupartifact.BreakGlassActivationDigest(audit)
	evidence := &backupartifact.RestoreActivationEvidence{
		Kind:           backupartifact.RestoreActivationBreakGlass,
		EvidenceSHA256: digest, Operator: audit.Operator,
		RecordedAtUnixMillis: audit.AuthorizedAtUnixMillis,
		BreakGlass:           &audit,
	}
	return controller.ClusterState{
		Restore: &controllerstate.RestoreCoordinationState{
			Plan: &controllerstate.RestorePlan{
				ID: fence.PlanID, CheckpointID: fence.CheckpointID,
				CheckpointSHA256: fence.CheckpointSHA256,
				TargetGeneration: fence.TargetGeneration,
				InvalidateTokens: fence.InvalidateTokens,
				Status:           status, Activation: evidence,
				Partitions: []controllerstate.RestorePartition{
					{}, {}, {}, {}, {}, {}, {},
					{
						HashSlot:          fence.HashSlot,
						Status:            controllerstate.RestorePartitionConverged,
						TargetSlotID:      fence.TargetSlotID,
						ReplicaCount:      fence.ReplicaCount,
						ConvergedReplicas: fence.ReplicaCount,
						LeaderNodeID:      fence.LeaderNodeID,
						LeaderTerm:        fence.LeaderTerm,
						ConfigEpoch:       fence.ConfigEpoch,
						InstallAttempt:    fence.Attempt,
						Installed:         true, Verified: true,
					},
				},
			},
		},
	}
}
