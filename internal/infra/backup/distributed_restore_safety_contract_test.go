package backup

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	"github.com/WuKongIM/WuKongIM/pkg/controller"
)

func TestDistributedRestoreRejectsDivergentReplicaEvidence(t *testing.T) {
	state := distributedRestoreContractState()
	remote := distributedRestoreRemoteFunc(func(
		_ context.Context,
		nodeID uint64,
		command backupcontract.RestoreNodeCommand,
	) (backupcontract.RestoreNodeReceipt, error) {
		logical := uint64(10)
		if nodeID == 2 {
			logical = 11
		}
		return backupcontract.RestoreNodeReceipt{LogicalBytes: logical}, nil
	})
	executor, provider, _ := newDistributedRestoreContractExecutor(t, state, remote)
	writeStagedRestoreContractArchive(t, provider, "backup-contract")
	job := distributedRestoreContractJob()

	if _, err := executor.StageSlot(
		context.Background(), job, stagedRestoreContractSlot, 1,
	); err == nil {
		t.Fatal("StageSlot(divergent bytes) error = nil")
	}
	job.Slots[stagedRestoreContractSlot].Status =
		backupcontract.RestoreSlotStatusStaged
	job.Slots[stagedRestoreContractSlot].LogicalBytes = 10
	if err := executor.VerifySlot(
		context.Background(), job, stagedRestoreContractSlot, 1,
	); err == nil {
		t.Fatal("VerifySlot(divergent bytes) error = nil")
	}

	missing := job
	missing.Slots = missing.Slots[:stagedRestoreContractSlot]
	if err := executor.VerifySlot(
		context.Background(), missing, stagedRestoreContractSlot, 1,
	); err == nil {
		t.Fatal("VerifySlot(missing progress) error = nil")
	}
	changed := job
	changed.Slots = append([]backupcontract.RestoreSlotProgress(nil), job.Slots...)
	changed.Slots[stagedRestoreContractSlot].Attempt = 2
	if err := executor.VerifySlot(
		context.Background(), changed, stagedRestoreContractSlot, 1,
	); err == nil {
		t.Fatal("VerifySlot(changed attempt) error = nil")
	}
}

func TestDistributedRestoreStopsRetriesWhenTheOperationIsCanceled(t *testing.T) {
	state := distributedRestoreContractState()
	remoteErr := errors.New("node unavailable")
	remote := distributedRestoreRemoteFunc(func(
		context.Context,
		uint64,
		backupcontract.RestoreNodeCommand,
	) (backupcontract.RestoreNodeReceipt, error) {
		return backupcontract.RestoreNodeReceipt{}, remoteErr
	})
	executor, _, _ := newDistributedRestoreContractExecutor(t, state, remote)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := executor.EnterMaintenance(
		canceled, distributedRestoreContractJob(),
	); !errors.Is(err, context.Canceled) {
		t.Fatalf("EnterMaintenance(canceled retry) error = %v", err)
	}
}

func TestDistributedRestorePreflightRejectsCapacityOverflowAndShortfall(t *testing.T) {
	plan := backupcontract.Plan{
		Store: backupcontract.StoreConfig{Kind: backupcontract.StoreKindFile},
	}
	job := distributedRestoreContractJob()
	tests := []struct {
		name    string
		logical uint64
		receipt backupcontract.RestoreNodeReceipt
	}{
		{
			name: "archive arithmetic overflow", logical: math.MaxUint64,
			receipt: backupcontract.RestoreNodeReceipt{AvailableBytes: math.MaxUint64},
		},
		{
			name: "current data arithmetic overflow", logical: 1,
			receipt: backupcontract.RestoreNodeReceipt{
				AvailableBytes: math.MaxUint64, CurrentBusinessBytes: math.MaxUint64,
			},
		},
		{
			name: "insufficient staging capacity", logical: 1,
			receipt: backupcontract.RestoreNodeReceipt{AvailableBytes: 1},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state := distributedRestorePreflightState()
			remote := distributedRestoreRemoteFunc(func(
				context.Context,
				uint64,
				backupcontract.RestoreNodeCommand,
			) (backupcontract.RestoreNodeReceipt, error) {
				return test.receipt, nil
			})
			executor, _, _ := newDistributedRestoreContractExecutor(t, state, remote)
			err := executor.Check(
				context.Background(), job, plan,
				backupartifact.ArchiveManifest{
					SourceClusterID: "cluster-contract",
					LogicalBytes:    test.logical,
				},
			)
			if err == nil {
				t.Fatal("Check() error = nil")
			}
		})
	}
}

func TestDistributedRestoreRejectsMissingStateAndNonLeaderCoordinator(t *testing.T) {
	remote := distributedRestoreRemoteFunc(func(
		context.Context,
		uint64,
		backupcontract.RestoreNodeCommand,
	) (backupcontract.RestoreNodeReceipt, error) {
		return backupcontract.RestoreNodeReceipt{}, nil
	})
	state := distributedRestoreContractState()
	state.ScheduledBackup.ActiveRestore = nil
	executor, _, _ := newDistributedRestoreContractExecutor(t, state, remote)
	if _, err := executor.EnterMaintenance(
		context.Background(), distributedRestoreContractJob(),
	); err == nil {
		t.Fatal("EnterMaintenance(missing state) error = nil")
	}

	state = distributedRestoreContractState()
	executor, _, cluster := newDistributedRestoreContractExecutor(t, state, remote)
	cluster.fenceNodeID = 2
	if _, err := executor.EnterMaintenance(
		context.Background(), distributedRestoreContractJob(),
	); err == nil {
		t.Fatal("EnterMaintenance(non-leader) error = nil")
	}
}

func newDistributedRestoreContractExecutor(
	t *testing.T,
	state controller.ClusterState,
	remote RemoteRestoreClient,
) (*DistributedRestoreExecutor, *RepositoryProvider, *distributedRestoreContractCluster) {
	t.Helper()
	provider, err := NewRepositoryProvider(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("NewRepositoryProvider(): %v", err)
	}
	localNode := &stagedRestoreContractNode{
		state: state, maintenance: true, fenceNodeID: 9, fenceTerm: 5,
		liveMetadata: []byte("metadata"), liveMessages: []byte("messages"),
	}
	local, err := NewStagedRestoreNodeService(localNode, provider, t.TempDir())
	if err != nil {
		t.Fatalf("NewStagedRestoreNodeService(): %v", err)
	}
	cluster := &distributedRestoreContractCluster{
		nodeID: 9, fenceNodeID: 9, fenceTerm: 5, state: state,
	}
	executor, err := NewDistributedRestoreExecutor(cluster, local, remote)
	if err != nil {
		t.Fatalf("NewDistributedRestoreExecutor(): %v", err)
	}
	return executor, provider, cluster
}

func distributedRestoreContractState() controller.ClusterState {
	state := stagedRestoreContractState(&controller.ScheduledRestoreJob{
		ID: "restore-contract", BackupID: "backup-contract",
		Status:           string(backupcontract.RestoreStatusStaging),
		TargetActivation: "activation-contract", MaxMessageID: 102,
	})
	state.Nodes = []controller.Node{
		{NodeID: 1, Roles: []controller.NodeRole{controller.NodeRoleData}, JoinState: controller.NodeJoinStateActive},
		{NodeID: 2, Roles: []controller.NodeRole{controller.NodeRoleData}, JoinState: controller.NodeJoinStateActive},
	}
	state.Slots[0].DesiredPeers = []uint64{2, 1}
	return state
}

func distributedRestorePreflightState() controller.ClusterState {
	state := distributedRestoreContractState()
	state.ClusterID = "cluster-contract"
	state.Config.HashSlotCount = backupcontract.HashSlotCount
	state.ScheduledBackup.ActiveRestore = nil
	now := time.Now().UTC().UnixMilli()
	for index := range state.Nodes {
		state.Nodes[index].Status = controller.NodeStatusAlive
		state.NodeHealthReports = append(
			state.NodeHealthReports,
			controller.NodeHealthReport{
				NodeID: state.Nodes[index].NodeID,
				Status: controller.NodeStatusAlive, RuntimeReady: true,
				ObservedControlRevision: state.Revision,
				ReportedAtUnixMilli:     now,
			},
		)
	}
	return state
}

func distributedRestoreContractJob() backupcontract.RestoreJob {
	slots := make([]backupcontract.RestoreSlotProgress, backupcontract.HashSlotCount)
	for hashSlot := range slots {
		slots[hashSlot] = backupcontract.RestoreSlotProgress{
			HashSlot: uint16(hashSlot),
		}
	}
	slots[stagedRestoreContractSlot] = backupcontract.RestoreSlotProgress{
		HashSlot: stagedRestoreContractSlot,
		Status:   backupcontract.RestoreSlotStatusStaging,
		Attempt:  1, ReplicaNodeIDs: []uint64{1, 2},
	}
	return backupcontract.RestoreJob{
		ID: "restore-contract", BackupID: "backup-contract",
		TargetActivation: "activation-contract", MaxMessageID: 102,
		Slots: slots,
	}
}

type distributedRestoreContractCluster struct {
	nodeID      uint64
	fenceNodeID uint64
	fenceTerm   uint64
	state       controller.ClusterState
}

func (c *distributedRestoreContractCluster) NodeID() uint64 { return c.nodeID }

func (c *distributedRestoreContractCluster) BackupControllerFence(
	context.Context,
) (uint64, uint64, error) {
	return c.fenceNodeID, c.fenceTerm, nil
}

func (c *distributedRestoreContractCluster) LocalState(
	context.Context,
) (controller.ClusterState, error) {
	return c.state.Clone(), nil
}

type distributedRestoreRemoteFunc func(
	context.Context,
	uint64,
	backupcontract.RestoreNodeCommand,
) (backupcontract.RestoreNodeReceipt, error)

func (f distributedRestoreRemoteFunc) RunBackupRestoreNode(
	ctx context.Context,
	nodeID uint64,
	command backupcontract.RestoreNodeCommand,
) (backupcontract.RestoreNodeReceipt, error) {
	return f(ctx, nodeID, command)
}
