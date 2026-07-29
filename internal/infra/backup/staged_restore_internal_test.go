package backup

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	clusterpkg "github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/controller"
)

func TestWriteRestoreBytesHandlesShortWrites(t *testing.T) {
	writer := &boundedWriter{maximum: 3}
	if err := writeRestoreBytes(writer, []byte("restored-data")); err != nil {
		t.Fatalf("writeRestoreBytes(): %v", err)
	}
	if got := writer.body.String(); got != "restored-data" {
		t.Fatalf("body = %q", got)
	}
}

func TestWriteRestoreBytesRejectsNoProgress(t *testing.T) {
	if err := writeRestoreBytes(zeroWriter{}, []byte("data")); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("writeRestoreBytes() error = %v", err)
	}
}

func TestOpenRestoreFilesAcceptsHashSlotWithoutMessageStreams(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(directory, "READY"), []byte("ready\n"), restoreFileMode,
	); err != nil {
		t.Fatalf("WriteFile(READY): %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(directory, "metadata.bin"), []byte("metadata"), restoreFileMode,
	); err != nil {
		t.Fatalf("WriteFile(metadata): %v", err)
	}

	metadata, _, messages, closeFiles, err := openRestoreFiles(directory)
	if err != nil {
		t.Fatalf("openRestoreFiles(): %v", err)
	}
	defer closeFiles()
	if metadata == nil || len(messages) != 0 {
		t.Fatalf("metadata/messages = %v/%d, want metadata and no messages", metadata, len(messages))
	}
}

func TestRollbackRunsAfterInterruptedDestructiveSwitch(t *testing.T) {
	root := t.TempDir()
	node := &restoreFailureNode{installErr: errors.New("injected install failure")}
	service := &StagedRestoreNodeService{node: node, root: root}
	command := backupcontract.RestoreNodeCommand{
		Action: backupcontract.RestoreNodeActionSwitch,
		JobID:  "restore-1", BackupID: "backup-1", HashSlot: 3, Attempt: 1,
		ControllerRevision: 1, TargetActivation: "activation-1",
		CoordinatorNodeID: 1, CoordinatorTerm: 1,
	}
	for _, directory := range []string{
		service.targetDir(command), service.rollbackDir(command),
	} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatalf("MkdirAll(): %v", err)
		}
		if err := os.WriteFile(
			filepath.Join(directory, "READY"), []byte("ready\n"), restoreFileMode,
		); err != nil {
			t.Fatalf("WriteFile(READY): %v", err)
		}
		if err := os.WriteFile(
			filepath.Join(directory, "metadata.bin"), []byte("metadata"), restoreFileMode,
		); err != nil {
			t.Fatalf("WriteFile(metadata): %v", err)
		}
	}

	if err := service.switchPartition(
		context.Background(), command,
	); !errors.Is(err, node.installErr) {
		t.Fatalf("switchPartition() error = %v", err)
	}
	switching := filepath.Join(service.targetDir(command), restoreMarkerSwitching)
	if _, err := os.Stat(switching); err != nil {
		t.Fatalf("SWITCHING marker: %v", err)
	}

	node.installErr = nil
	command.Action = backupcontract.RestoreNodeActionRollback
	if err := service.rollback(context.Background(), command); err != nil {
		t.Fatalf("rollback(): %v", err)
	}
	if node.installCalls != 2 {
		t.Fatalf("InstallLocalRestorePartition() calls = %d, want 2", node.installCalls)
	}
	for _, marker := range []string{restoreMarkerSwitching, restoreMarkerSwitched} {
		if _, err := os.Stat(
			filepath.Join(service.targetDir(command), marker),
		); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s marker error = %v, want absent", marker, err)
		}
	}
}

func TestStagedRestoreRunRejectsStaleControllerFence(t *testing.T) {
	state := controller.ClusterState{
		Revision: 9,
		ScheduledBackup: &controller.ScheduledBackupState{
			Plan: &controller.BackupPlan{
				Revision: 1,
				Store: controller.BackupStoreConfig{
					Kind: controller.BackupStoreKind("file"),
				},
			},
			ActiveRestore: &controller.ScheduledRestoreJob{
				ID: "restore-1", BackupID: "backup-1", Status: "finalizing",
				TargetActivation: "activation-1",
			},
		},
	}
	node := &fencedRestoreNode{state: state}
	cipher, err := NewCredentialCipher("shared-manager-secret", "cluster-1")
	if err != nil {
		t.Fatalf("NewCredentialCipher(): %v", err)
	}
	provider, err := NewRepositoryProvider(t.TempDir(), cipher)
	if err != nil {
		t.Fatalf("NewRepositoryProvider(): %v", err)
	}
	service, err := NewStagedRestoreNodeService(node, provider, t.TempDir())
	if err != nil {
		t.Fatalf("NewStagedRestoreNodeService(): %v", err)
	}
	service.SetMaintenanceQuiescer(func(context.Context) error { return nil })
	service.SetMaintenanceResumer(func(context.Context) error { return nil })
	command := backupcontract.RestoreNodeCommand{
		Action: backupcontract.RestoreNodeActionCleanup,
		Store:  backupcontract.StoreConfig{Kind: backupcontract.StoreKindFile},
		JobID:  "restore-1", BackupID: "backup-1",
		ControllerRevision: 10, TargetActivation: "activation-1",
		CoordinatorNodeID: 1, CoordinatorTerm: 1,
	}
	if _, err := service.Run(context.Background(), command); err == nil {
		t.Fatal("Run(stale revision) error = nil")
	}
	command.ControllerRevision = 9
	command.BackupID = "backup-old"
	if _, err := service.Run(context.Background(), command); err == nil {
		t.Fatal("Run(stale archive) error = nil")
	}
	command.BackupID = "backup-1"
	if _, err := service.Run(context.Background(), command); err != nil {
		t.Fatalf("Run(current fence): %v", err)
	}
}

func TestStagedRestorePrepareAcceptsPublishedMaintenanceFence(t *testing.T) {
	state := controller.ClusterState{
		Revision: 9,
		ScheduledBackup: &controller.ScheduledBackupState{
			Plan: &controller.BackupPlan{
				Revision: 1,
				Store: controller.BackupStoreConfig{
					Kind: controller.BackupStoreKind("file"),
				},
			},
			ActiveRestore: &controller.ScheduledRestoreJob{
				ID: "restore-1", BackupID: "backup-1",
				Status:             string(backupcontract.RestoreStatusMaintenance),
				MaintenanceEntered: true,
				TargetActivation:   "activation-1",
			},
		},
	}
	cipher, err := NewCredentialCipher("shared-manager-secret", "cluster-1")
	if err != nil {
		t.Fatalf("NewCredentialCipher(): %v", err)
	}
	provider, err := NewRepositoryProvider(t.TempDir(), cipher)
	if err != nil {
		t.Fatalf("NewRepositoryProvider(): %v", err)
	}
	service, err := NewStagedRestoreNodeService(
		&fencedRestoreNode{state: state}, provider, t.TempDir(),
	)
	if err != nil {
		t.Fatalf("NewStagedRestoreNodeService(): %v", err)
	}
	service.SetMaintenanceQuiescer(func(context.Context) error { return nil })
	command := backupcontract.RestoreNodeCommand{
		Action: backupcontract.RestoreNodeActionPrepare,
		Store:  backupcontract.StoreConfig{Kind: backupcontract.StoreKindFile},
		JobID:  "restore-1", BackupID: "backup-1",
		ControllerRevision: 9, TargetActivation: "activation-1",
		CoordinatorNodeID: 1, CoordinatorTerm: 1,
	}

	if _, err := service.Run(context.Background(), command); err != nil {
		t.Fatalf("Run(prepare): %v", err)
	}
}

func TestStagedRestoreActivateAppliesArchiveMessageIDFloor(t *testing.T) {
	const maxMessageID uint64 = 7_000_000_000
	state := controller.ClusterState{
		Revision: 12,
		ScheduledBackup: &controller.ScheduledBackupState{
			Plan: &controller.BackupPlan{
				Revision: 2,
				Store: controller.BackupStoreConfig{
					Kind: controller.BackupStoreKind("file"),
				},
			},
			ActiveRestore: &controller.ScheduledRestoreJob{
				ID: "restore-2", BackupID: "backup-2",
				Status:           string(backupcontract.RestoreStatusSwitching),
				TargetActivation: "activation-2",
				MaxMessageID:     maxMessageID,
			},
		},
	}
	cipher, err := NewCredentialCipher("shared-manager-secret", "cluster-1")
	if err != nil {
		t.Fatalf("NewCredentialCipher(): %v", err)
	}
	provider, err := NewRepositoryProvider(t.TempDir(), cipher)
	if err != nil {
		t.Fatalf("NewRepositoryProvider(): %v", err)
	}
	service, err := NewStagedRestoreNodeService(
		&fencedRestoreNode{state: state}, provider, t.TempDir(),
	)
	if err != nil {
		t.Fatalf("NewStagedRestoreNodeService(): %v", err)
	}
	service.SetMaintenanceQuiescer(func(context.Context) error { return nil })
	service.SetMaintenanceResumer(func(context.Context) error { return nil })
	var applied uint64
	service.SetMessageIDFloor(func(messageID uint64) error {
		applied = messageID
		return nil
	})
	command := backupcontract.RestoreNodeCommand{
		Action: backupcontract.RestoreNodeActionActivate,
		Store:  backupcontract.StoreConfig{Kind: backupcontract.StoreKindFile},
		JobID:  "restore-2", BackupID: "backup-2",
		ControllerRevision: 12, TargetActivation: "activation-2",
		MaxMessageID: maxMessageID, CoordinatorNodeID: 1, CoordinatorTerm: 1,
	}
	if _, err := service.Run(context.Background(), command); err != nil {
		t.Fatalf("Run(activate): %v", err)
	}
	if applied != maxMessageID {
		t.Fatalf("message ID floor = %d, want %d", applied, maxMessageID)
	}
}

type boundedWriter struct {
	body    bytes.Buffer
	maximum int
}

func (w *boundedWriter) Write(value []byte) (int, error) {
	return w.body.Write(value[:min(len(value), w.maximum)])
}

type zeroWriter struct{}

func (zeroWriter) Write([]byte) (int, error) { return 0, nil }

type restoreFailureNode struct {
	installErr   error
	installCalls int
}

func (*restoreFailureNode) RestoreMaintenanceReady() bool { return true }

func (*restoreFailureNode) NodeID() uint64 { return 1 }
func (*restoreFailureNode) BackupControllerFence(
	context.Context,
) (uint64, uint64, error) {
	return 1, 1, nil
}

func (*restoreFailureNode) LocalState(
	context.Context,
) (controller.ClusterState, error) {
	return controller.ClusterState{}, nil
}

func (*restoreFailureNode) OpenLocalRestoreMetadataSnapshot(
	context.Context,
	uint16,
) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader([]byte("metadata"))), nil
}

func (*restoreFailureNode) OpenLocalRestoreMessageSnapshot(
	context.Context,
	uint16,
) (clusterpkg.BackupMessageSnapshot, error) {
	return clusterpkg.BackupMessageSnapshot{}, nil
}

func (*restoreFailureNode) VerifyLocalRestorePartitionStreams(
	context.Context,
	uint16,
	io.ReadSeeker,
	int64,
	[]clusterpkg.RestoreMessageStream,
) (uint64, error) {
	return 1, nil
}

func (n *restoreFailureNode) InstallLocalRestorePartition(
	context.Context,
	uint16,
	io.ReadSeeker,
	int64,
	[]clusterpkg.RestoreMessageStream,
) error {
	n.installCalls++
	return n.installErr
}

func (*restoreFailureNode) ActivateLocalRestore(context.Context) error { return nil }
func (*restoreFailureNode) CheckLocalRestoreHealth(context.Context) error {
	return nil
}

type fencedRestoreNode struct {
	restoreFailureNode
	state controller.ClusterState
}

func (*fencedRestoreNode) NodeID() uint64 { return 1 }
func (*fencedRestoreNode) BackupControllerFence(
	context.Context,
) (uint64, uint64, error) {
	return 1, 1, nil
}

func (n *fencedRestoreNode) LocalState(
	context.Context,
) (controller.ClusterState, error) {
	return n.state, nil
}
