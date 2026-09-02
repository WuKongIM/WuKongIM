package backup_test

import (
	"context"
	"testing"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	backupinfra "github.com/WuKongIM/WuKongIM/internal/infra/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
)

func TestDistributedRestoreSwitchesEveryReplicaBeforeReactivatingTraffic(t *testing.T) {
	state := restoreControllerState()
	cluster := &restoreClusterStub{nodeID: 9, state: state}
	remote := &restoreRemoteStub{logicalBytes: 123}
	executor := newDistributedRestoreExecutor(t, cluster, remote)
	job := backupcontract.RestoreJob{
		ID: "restore-1", BackupID: "backup-1",
		TargetActivation: "archive-backup-1",
		Slots: []backupcontract.RestoreSlotProgress{{
			HashSlot: 0, Status: backupcontract.RestoreSlotStatusVerified,
			Attempt: 1, ReplicaNodeIDs: []uint64{1, 2}, LogicalBytes: 123,
		}},
	}

	if err := executor.ActivateRestore(context.Background(), job); err != nil {
		t.Fatalf("ActivateRestore(): %v", err)
	}
	assertRestoreCalls(t, remote.takeCalls(), []restoreCall{
		{nodeID: 1, action: backupcontract.RestoreNodeActionSwitch},
		{nodeID: 2, action: backupcontract.RestoreNodeActionSwitch},
		{nodeID: 1, action: backupcontract.RestoreNodeActionActivate},
		{nodeID: 2, action: backupcontract.RestoreNodeActionActivate},
		{nodeID: 1, action: backupcontract.RestoreNodeActionHealth},
		{nodeID: 2, action: backupcontract.RestoreNodeActionHealth},
	})
}

func TestDistributedRestoreRollsBackOnlyReplicasWithDurableStageEvidence(t *testing.T) {
	state := restoreControllerState()
	cluster := &restoreClusterStub{nodeID: 9, state: state}
	remote := &restoreRemoteStub{logicalBytes: 123}
	executor := newDistributedRestoreExecutor(t, cluster, remote)
	job := backupcontract.RestoreJob{
		ID: "restore-1", BackupID: "backup-1",
		TargetActivation: "archive-backup-1",
		Slots: []backupcontract.RestoreSlotProgress{
			{
				HashSlot: 0, Attempt: 1,
				ReplicaNodeIDs: []uint64{1, 2}, LogicalBytes: 123,
			},
			{HashSlot: 1},
		},
	}

	if err := executor.Rollback(context.Background(), job); err != nil {
		t.Fatalf("Rollback(): %v", err)
	}
	assertRestoreCalls(t, remote.takeCalls(), []restoreCall{
		{nodeID: 1, action: backupcontract.RestoreNodeActionRollback},
		{nodeID: 2, action: backupcontract.RestoreNodeActionRollback},
		{nodeID: 1, action: backupcontract.RestoreNodeActionActivate},
		{nodeID: 2, action: backupcontract.RestoreNodeActionActivate},
		{nodeID: 1, action: backupcontract.RestoreNodeActionHealth},
		{nodeID: 2, action: backupcontract.RestoreNodeActionHealth},
	})
}

func TestDistributedRestoreResumesEveryNodeBeforeCleaningStagingData(t *testing.T) {
	state := restoreControllerState()
	cluster := &restoreClusterStub{nodeID: 9, state: state}
	remote := &restoreRemoteStub{}
	executor := newDistributedRestoreExecutor(t, cluster, remote)
	job := backupcontract.RestoreJob{ID: "restore-1", BackupID: "backup-1"}

	if err := executor.ExitMaintenance(
		context.Background(), job, true,
	); err != nil {
		t.Fatalf("ExitMaintenance(): %v", err)
	}
	assertRestoreCalls(t, remote.takeCalls(), []restoreCall{
		{nodeID: 1, action: backupcontract.RestoreNodeActionResume},
		{nodeID: 2, action: backupcontract.RestoreNodeActionResume},
		{nodeID: 1, action: backupcontract.RestoreNodeActionCleanup},
		{nodeID: 2, action: backupcontract.RestoreNodeActionCleanup},
	})
}

func TestDistributedRestoreQuarantinesAnArchiveThatFailsFullVerification(t *testing.T) {
	state := restoreControllerState()
	cluster := &restoreClusterStub{nodeID: 9, state: state}
	remote := &restoreRemoteStub{}
	executor, store := newDistributedRestoreExecutorWithStore(t, cluster, remote)
	job := backupcontract.RestoreJob{ID: "restore-1", BackupID: "backup-1"}

	if err := executor.VerifyArchive(context.Background(), job); err == nil {
		t.Fatal("VerifyArchive() error = nil for archive with missing Slot artifacts")
	}
	marker, _, err := store.Open(
		context.Background(), "backups/backup-1/CORRUPT",
	)
	if err != nil {
		t.Fatalf("Open(CORRUPT): %v", err)
	}
	if closeErr := marker.Close(); closeErr != nil {
		t.Fatalf("Close(CORRUPT): %v", closeErr)
	}
}

func newDistributedRestoreExecutorWithStore(
	t *testing.T,
	cluster *restoreClusterStub,
	remote *restoreRemoteStub,
) (*backupinfra.DistributedRestoreExecutor, backupartifact.ArchiveStore) {
	t.Helper()
	cipher, err := backupinfra.NewCredentialCipher(
		"manager-secret-for-restore-contracts", "cluster-1",
	)
	if err != nil {
		t.Fatalf("NewCredentialCipher(): %v", err)
	}
	provider, err := backupinfra.NewRepositoryProvider(t.TempDir(), cipher)
	if err != nil {
		t.Fatalf("NewRepositoryProvider(): %v", err)
	}
	store, err := provider.Open(
		context.Background(),
		backupcontract.StoreConfig{Kind: backupcontract.StoreKindFile},
	)
	if err != nil {
		t.Fatalf("Open(repository): %v", err)
	}
	writeRestoreArchiveMetadata(t, store, "backup-1")
	local, err := backupinfra.NewStagedRestoreNodeService(
		restorePartitionNodeStub{}, provider, t.TempDir(),
	)
	if err != nil {
		t.Fatalf("NewStagedRestoreNodeService(): %v", err)
	}
	local.SetMaintenanceQuiescer(func(context.Context) error { return nil })
	local.SetMaintenanceResumer(func(context.Context) error { return nil })
	executor, err := backupinfra.NewDistributedRestoreExecutor(
		cluster, local, remote,
	)
	if err != nil {
		t.Fatalf("NewDistributedRestoreExecutor(): %v", err)
	}
	return executor, store
}
