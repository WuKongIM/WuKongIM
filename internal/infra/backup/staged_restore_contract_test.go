package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	clusterpkg "github.com/WuKongIM/WuKongIM/pkg/cluster"
	"github.com/WuKongIM/WuKongIM/pkg/controller"
)

const stagedRestoreContractSlot uint16 = 7

func TestStagedRestoreRunPreservesAnAtomicRollbackAcrossTheFullLifecycle(t *testing.T) {
	dataDir := t.TempDir()
	businessBody := []byte("live-business-state")
	if err := os.MkdirAll(filepath.Join(dataDir, "live"), 0o700); err != nil {
		t.Fatalf("MkdirAll(live): %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(dataDir, "live", "state.db"), businessBody, 0o600,
	); err != nil {
		t.Fatalf("WriteFile(business state): %v", err)
	}

	service, node, provider := newStagedRestoreContractService(t, dataDir)
	archive := writeStagedRestoreContractArchive(t, provider, "backup-contract")
	command := stagedRestoreContractCommand(archive.reference)

	command.Action = backupcontract.RestoreNodeActionPreflight
	command.RequiredBytes = 1
	receipt, err := service.Run(context.Background(), command)
	if err != nil {
		t.Fatalf("Run(preflight): %v", err)
	}
	if receipt.AvailableBytes == 0 ||
		receipt.CurrentBusinessBytes != uint64(len(businessBody)) {
		t.Fatalf("preflight receipt = %+v", receipt)
	}

	quiesceCalls := 0
	service.SetMaintenanceQuiescer(func(context.Context) error {
		quiesceCalls++
		return nil
	})
	resumeCalls := 0
	service.SetMaintenanceResumer(func(context.Context) error {
		resumeCalls++
		return nil
	})
	var appliedFloor uint64
	service.SetMessageIDFloor(func(value uint64) error {
		appliedFloor = value
		return nil
	})

	node.setRestorePhase(
		backupcontract.RestoreStatusMaintenance,
		backupcontract.RestoreSlotStatusStaging,
		false,
	)
	command.Action = backupcontract.RestoreNodeActionPrepare
	if _, err := service.Run(context.Background(), command); err != nil {
		t.Fatalf("Run(prepare): %v", err)
	}
	if quiesceCalls != 1 {
		t.Fatalf("quiesce calls = %d, want 1", quiesceCalls)
	}

	command.Action = backupcontract.RestoreNodeActionStage
	receipt, err = service.Run(context.Background(), command)
	if err != nil {
		t.Fatalf("Run(stage): %v", err)
	}
	if receipt.LogicalBytes != archive.logicalBytes {
		t.Fatalf("stage logical bytes = %d, want %d", receipt.LogicalBytes, archive.logicalBytes)
	}
	if node.metadataSnapshotCalls != 1 || node.messageSnapshotCalls != 1 {
		t.Fatalf(
			"rollback snapshot calls = %d/%d, want 1/1",
			node.metadataSnapshotCalls, node.messageSnapshotCalls,
		)
	}
	assertStagedRestorePayload(
		t, node.lastVerified(), archive.metadata, archive.messages,
	)

	// Replaying Stage must verify the durable READY directories instead of
	// recapturing live data or replacing the accepted target.
	if _, err := service.Run(context.Background(), command); err != nil {
		t.Fatalf("Run(stage replay): %v", err)
	}
	if node.metadataSnapshotCalls != 1 || node.messageSnapshotCalls != 1 {
		t.Fatalf(
			"replayed rollback snapshot calls = %d/%d, want 1/1",
			node.metadataSnapshotCalls, node.messageSnapshotCalls,
		)
	}

	node.setRestorePhase(
		backupcontract.RestoreStatusVerifying,
		backupcontract.RestoreSlotStatusStaged,
		false,
	)
	command.Action = backupcontract.RestoreNodeActionVerify
	if _, err := service.Run(context.Background(), command); err != nil {
		t.Fatalf("Run(verify): %v", err)
	}

	node.setRestorePhase(
		backupcontract.RestoreStatusSwitching,
		backupcontract.RestoreSlotStatusVerified,
		true,
	)
	command.Action = backupcontract.RestoreNodeActionSwitch
	if _, err := service.Run(context.Background(), command); err != nil {
		t.Fatalf("Run(switch): %v", err)
	}
	if _, err := service.Run(context.Background(), command); err != nil {
		t.Fatalf("Run(switch replay): %v", err)
	}
	if len(node.installed) != 1 {
		t.Fatalf("install calls = %d, want 1", len(node.installed))
	}
	assertStagedRestorePayload(
		t, node.installed[0], archive.metadata, archive.messages,
	)

	command.Action = backupcontract.RestoreNodeActionActivate
	if _, err := service.Run(context.Background(), command); err != nil {
		t.Fatalf("Run(activate): %v", err)
	}
	if node.activateCalls != 1 || appliedFloor != command.MaxMessageID {
		t.Fatalf(
			"activate calls/floor = %d/%d, want 1/%d",
			node.activateCalls, appliedFloor, command.MaxMessageID,
		)
	}
	command.Action = backupcontract.RestoreNodeActionHealth
	if _, err := service.Run(context.Background(), command); err != nil {
		t.Fatalf("Run(health): %v", err)
	}
	if node.healthCalls != 1 {
		t.Fatalf("health calls = %d, want 1", node.healthCalls)
	}

	node.setRestorePhase(
		backupcontract.RestoreStatusRollingBack,
		backupcontract.RestoreSlotStatusVerified,
		false,
	)
	command.Action = backupcontract.RestoreNodeActionRollback
	if _, err := service.Run(context.Background(), command); err != nil {
		t.Fatalf("Run(rollback): %v", err)
	}
	if _, err := service.Run(context.Background(), command); err != nil {
		t.Fatalf("Run(rollback replay): %v", err)
	}
	if len(node.installed) != 2 {
		t.Fatalf("install calls after rollback = %d, want 2", len(node.installed))
	}
	assertStagedRestorePayload(
		t, node.installed[1], node.liveMetadata, [][]byte{node.liveMessages},
	)

	node.setRestorePhase(
		backupcontract.RestoreStatusFinalizing,
		backupcontract.RestoreSlotStatusVerified,
		false,
	)
	command.Action = backupcontract.RestoreNodeActionResume
	if _, err := service.Run(context.Background(), command); err != nil {
		t.Fatalf("Run(resume): %v", err)
	}
	if resumeCalls != 1 {
		t.Fatalf("resume calls = %d, want 1", resumeCalls)
	}
	command.Action = backupcontract.RestoreNodeActionCleanup
	jobDir := service.jobDir(command)
	if _, err := service.Run(context.Background(), command); err != nil {
		t.Fatalf("Run(cleanup): %v", err)
	}
	if _, err := os.Stat(jobDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("restore job directory error = %v, want absent", err)
	}
}

func TestStagedRestoreRunDiscardsCanceledAndCorruptAttemptsBeforeRetry(t *testing.T) {
	service, node, provider := newStagedRestoreContractService(t, t.TempDir())
	archive := writeStagedRestoreContractArchive(t, provider, "backup-retry")
	node.setRestorePhase(
		backupcontract.RestoreStatusStaging,
		backupcontract.RestoreSlotStatusStaging,
		false,
	)
	node.state.ScheduledBackup.ActiveRestore.BackupID = "backup-retry"
	command := stagedRestoreContractCommand(archive.reference)
	command.BackupID = "backup-retry"
	command.Action = backupcontract.RestoreNodeActionStage

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := service.Run(canceled, command); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run(canceled stage) error = %v", err)
	}
	if _, err := os.Stat(service.rollbackDir(command)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rollback directory after cancellation error = %v, want absent", err)
	}

	store := openStagedRestoreContractStore(t, provider)
	corrupt := bytes.Repeat([]byte{'x'}, len(archive.chunkBody))
	putStagedRestoreContractObject(t, store, archive.chunkKey, corrupt)
	if _, err := service.Run(context.Background(), command); err == nil {
		t.Fatal("Run(corrupt stage) error = nil")
	}
	if _, err := os.Stat(service.targetDir(command)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("target directory after corruption error = %v, want absent", err)
	}

	putStagedRestoreContractObject(t, store, archive.chunkKey, archive.chunkBody)
	receipt, err := service.Run(context.Background(), command)
	if err != nil {
		t.Fatalf("Run(retried stage): %v", err)
	}
	if receipt.LogicalBytes != archive.logicalBytes {
		t.Fatalf("retry logical bytes = %d, want %d", receipt.LogicalBytes, archive.logicalBytes)
	}
	assertStagedRestorePayload(
		t, node.lastVerified(), archive.metadata, archive.messages,
	)
}

func TestStagedRestoreRunRejectsCommandsThatCrossControllerOrPhaseFences(t *testing.T) {
	tests := []struct {
		name        string
		action      backupcontract.RestoreNodeAction
		status      backupcontract.RestoreStatus
		slotStatus  backupcontract.RestoreSlotStatus
		allVerified bool
		mutate      func(*stagedRestoreContractNode, *backupcontract.RestoreNodeCommand)
	}{
		{
			name: "stale coordinator", action: backupcontract.RestoreNodeActionStage,
			status:     backupcontract.RestoreStatusStaging,
			slotStatus: backupcontract.RestoreSlotStatusStaging,
			mutate: func(node *stagedRestoreContractNode, _ *backupcontract.RestoreNodeCommand) {
				node.fenceTerm = 4
			},
		},
		{
			name: "Controller fence read failure", action: backupcontract.RestoreNodeActionStage,
			status:     backupcontract.RestoreStatusStaging,
			slotStatus: backupcontract.RestoreSlotStatusStaging,
			mutate: func(node *stagedRestoreContractNode, _ *backupcontract.RestoreNodeCommand) {
				node.fenceErr = errors.New("Controller unavailable")
			},
		},
		{
			name: "Controller state read failure", action: backupcontract.RestoreNodeActionStage,
			status:     backupcontract.RestoreStatusStaging,
			slotStatus: backupcontract.RestoreSlotStatusStaging,
			mutate: func(node *stagedRestoreContractNode, _ *backupcontract.RestoreNodeCommand) {
				node.stateErr = errors.New("state unavailable")
			},
		},
		{
			name: "stale Controller revision", action: backupcontract.RestoreNodeActionStage,
			status:     backupcontract.RestoreStatusStaging,
			slotStatus: backupcontract.RestoreSlotStatusStaging,
			mutate: func(node *stagedRestoreContractNode, _ *backupcontract.RestoreNodeCommand) {
				node.state.Revision = 41
			},
		},
		{
			name: "missing durable plan", action: backupcontract.RestoreNodeActionStage,
			status:     backupcontract.RestoreStatusStaging,
			slotStatus: backupcontract.RestoreSlotStatusStaging,
			mutate: func(node *stagedRestoreContractNode, _ *backupcontract.RestoreNodeCommand) {
				node.state.ScheduledBackup.Plan = nil
			},
		},
		{
			name: "restore identity changed", action: backupcontract.RestoreNodeActionStage,
			status:     backupcontract.RestoreStatusStaging,
			slotStatus: backupcontract.RestoreSlotStatusStaging,
			mutate: func(node *stagedRestoreContractNode, _ *backupcontract.RestoreNodeCommand) {
				node.state.ScheduledBackup.ActiveRestore.TargetActivation = "new-activation"
			},
		},
		{
			name: "prepare phase changed", action: backupcontract.RestoreNodeActionPrepare,
			status:     backupcontract.RestoreStatusStaging,
			slotStatus: backupcontract.RestoreSlotStatusStaging,
		},
		{
			name: "stage phase changed", action: backupcontract.RestoreNodeActionStage,
			status:     backupcontract.RestoreStatusVerifying,
			slotStatus: backupcontract.RestoreSlotStatusStaging,
		},
		{
			name: "stage Slot fence missing", action: backupcontract.RestoreNodeActionStage,
			status:     backupcontract.RestoreStatusStaging,
			slotStatus: backupcontract.RestoreSlotStatusStaging,
			mutate: func(node *stagedRestoreContractNode, _ *backupcontract.RestoreNodeCommand) {
				node.state.ScheduledBackup.ActiveRestore.Slots =
					node.state.ScheduledBackup.ActiveRestore.Slots[:stagedRestoreContractSlot]
			},
		},
		{
			name: "stage attempt changed", action: backupcontract.RestoreNodeActionStage,
			status:     backupcontract.RestoreStatusStaging,
			slotStatus: backupcontract.RestoreSlotStatusStaging,
			mutate: func(node *stagedRestoreContractNode, _ *backupcontract.RestoreNodeCommand) {
				node.state.ScheduledBackup.ActiveRestore.Slots[stagedRestoreContractSlot].Attempt = 2
			},
		},
		{
			name: "stage replica changed", action: backupcontract.RestoreNodeActionStage,
			status:     backupcontract.RestoreStatusStaging,
			slotStatus: backupcontract.RestoreSlotStatusStaging,
			mutate: func(node *stagedRestoreContractNode, _ *backupcontract.RestoreNodeCommand) {
				node.state.Slots[0].DesiredPeers = []uint64{2}
			},
		},
		{
			name: "verify evidence missing", action: backupcontract.RestoreNodeActionVerify,
			status:     backupcontract.RestoreStatusVerifying,
			slotStatus: backupcontract.RestoreSlotStatusStaged,
			mutate: func(node *stagedRestoreContractNode, _ *backupcontract.RestoreNodeCommand) {
				node.state.ScheduledBackup.ActiveRestore.Slots[stagedRestoreContractSlot].ReplicaNodeIDs = nil
			},
		},
		{
			name: "switch phase changed", action: backupcontract.RestoreNodeActionSwitch,
			status:     backupcontract.RestoreStatusVerifying,
			slotStatus: backupcontract.RestoreSlotStatusVerified,
		},
		{
			name: "activation before every Slot verifies", action: backupcontract.RestoreNodeActionActivate,
			status:     backupcontract.RestoreStatusSwitching,
			slotStatus: backupcontract.RestoreSlotStatusVerified,
		},
		{
			name: "rollback evidence missing", action: backupcontract.RestoreNodeActionRollback,
			status:     backupcontract.RestoreStatusRollingBack,
			slotStatus: backupcontract.RestoreSlotStatusVerified,
			mutate: func(node *stagedRestoreContractNode, _ *backupcontract.RestoreNodeCommand) {
				node.state.ScheduledBackup.ActiveRestore.Slots[stagedRestoreContractSlot].ReplicaNodeIDs = nil
			},
		},
		{
			name: "health outside switching", action: backupcontract.RestoreNodeActionHealth,
			status:     backupcontract.RestoreStatusFinalizing,
			slotStatus: backupcontract.RestoreSlotStatusVerified,
		},
		{
			name: "cleanup before finalizing", action: backupcontract.RestoreNodeActionCleanup,
			status:      backupcontract.RestoreStatusSwitching,
			slotStatus:  backupcontract.RestoreSlotStatusVerified,
			allVerified: true,
		},
		{
			name: "resume before finalizing", action: backupcontract.RestoreNodeActionResume,
			status:      backupcontract.RestoreStatusSwitching,
			slotStatus:  backupcontract.RestoreSlotStatusVerified,
			allVerified: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service, node, _ := newStagedRestoreContractService(t, t.TempDir())
			node.setRestorePhase(test.status, test.slotStatus, test.allVerified)
			command := stagedRestoreContractCommand(backupartifact.SlotReference{})
			command.Action = test.action
			if test.mutate != nil {
				test.mutate(node, &command)
			}
			if _, err := service.Run(context.Background(), command); err == nil {
				t.Fatal("Run() error = nil")
			}
			if node.metadataSnapshotCalls != 0 || node.messageSnapshotCalls != 0 ||
				len(node.installed) != 0 || node.activateCalls != 0 {
				t.Fatalf("rejected command mutated restore state: %+v", node)
			}
		})
	}
}

func TestStagedRestoreRunPropagatesRequiredRuntimeFailures(t *testing.T) {
	tests := []struct {
		name       string
		action     backupcontract.RestoreNodeAction
		status     backupcontract.RestoreStatus
		slotStatus backupcontract.RestoreSlotStatus
		configure  func(*StagedRestoreNodeService, *stagedRestoreContractNode)
	}{
		{
			name: "prepare requires maintenance", action: backupcontract.RestoreNodeActionPrepare,
			status:     backupcontract.RestoreStatusMaintenance,
			slotStatus: backupcontract.RestoreSlotStatusStaging,
			configure: func(_ *StagedRestoreNodeService, node *stagedRestoreContractNode) {
				node.maintenance = false
			},
		},
		{
			name: "prepare requires quiescer", action: backupcontract.RestoreNodeActionPrepare,
			status:     backupcontract.RestoreStatusMaintenance,
			slotStatus: backupcontract.RestoreSlotStatusStaging,
		},
		{
			name: "prepare propagates quiesce", action: backupcontract.RestoreNodeActionPrepare,
			status:     backupcontract.RestoreStatusMaintenance,
			slotStatus: backupcontract.RestoreSlotStatusStaging,
			configure: func(service *StagedRestoreNodeService, _ *stagedRestoreContractNode) {
				service.SetMaintenanceQuiescer(func(context.Context) error {
					return errors.New("quiesce failed")
				})
			},
		},
		{
			name: "stage requires maintenance", action: backupcontract.RestoreNodeActionStage,
			status:     backupcontract.RestoreStatusStaging,
			slotStatus: backupcontract.RestoreSlotStatusStaging,
			configure: func(_ *StagedRestoreNodeService, node *stagedRestoreContractNode) {
				node.maintenance = false
			},
		},
		{
			name: "stage propagates metadata snapshot", action: backupcontract.RestoreNodeActionStage,
			status:     backupcontract.RestoreStatusStaging,
			slotStatus: backupcontract.RestoreSlotStatusStaging,
			configure: func(_ *StagedRestoreNodeService, node *stagedRestoreContractNode) {
				node.metadataSnapshotErr = errors.New("metadata snapshot failed")
			},
		},
		{
			name: "stage propagates message snapshot", action: backupcontract.RestoreNodeActionStage,
			status:     backupcontract.RestoreStatusStaging,
			slotStatus: backupcontract.RestoreSlotStatusStaging,
			configure: func(_ *StagedRestoreNodeService, node *stagedRestoreContractNode) {
				node.messageSnapshotErr = errors.New("message snapshot failed")
			},
		},
		{
			name: "activate propagates storage activation", action: backupcontract.RestoreNodeActionActivate,
			status:     backupcontract.RestoreStatusSwitching,
			slotStatus: backupcontract.RestoreSlotStatusVerified,
			configure: func(_ *StagedRestoreNodeService, node *stagedRestoreContractNode) {
				node.activateErr = errors.New("activation failed")
			},
		},
		{
			name: "activate propagates allocator fence", action: backupcontract.RestoreNodeActionActivate,
			status:     backupcontract.RestoreStatusSwitching,
			slotStatus: backupcontract.RestoreSlotStatusVerified,
			configure: func(service *StagedRestoreNodeService, _ *stagedRestoreContractNode) {
				service.SetMessageIDFloor(func(uint64) error {
					return errors.New("allocator fence failed")
				})
			},
		},
		{
			name: "health propagates node health", action: backupcontract.RestoreNodeActionHealth,
			status:     backupcontract.RestoreStatusSwitching,
			slotStatus: backupcontract.RestoreSlotStatusVerified,
			configure: func(_ *StagedRestoreNodeService, node *stagedRestoreContractNode) {
				node.healthErr = errors.New("health failed")
			},
		},
		{
			name: "resume requires runtime resumer", action: backupcontract.RestoreNodeActionResume,
			status:     backupcontract.RestoreStatusFinalizing,
			slotStatus: backupcontract.RestoreSlotStatusVerified,
		},
		{
			name: "resume propagates runtime restart", action: backupcontract.RestoreNodeActionResume,
			status:     backupcontract.RestoreStatusFinalizing,
			slotStatus: backupcontract.RestoreSlotStatusVerified,
			configure: func(service *StagedRestoreNodeService, _ *stagedRestoreContractNode) {
				service.SetMaintenanceResumer(func(context.Context) error {
					return errors.New("resume failed")
				})
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service, node, _ := newStagedRestoreContractService(t, t.TempDir())
			allVerified := test.action == backupcontract.RestoreNodeActionActivate
			node.setRestorePhase(test.status, test.slotStatus, allVerified)
			if test.configure != nil {
				test.configure(service, node)
			}
			command := stagedRestoreContractCommand(backupartifact.SlotReference{})
			command.Action = test.action
			if _, err := service.Run(context.Background(), command); err == nil {
				t.Fatal("Run() error = nil")
			}
		})
	}
}

func TestStagedRestoreRunRefusesLostOrTruncatedAcceptedStreams(t *testing.T) {
	service, node, provider := newStagedRestoreContractService(t, t.TempDir())
	archive := writeStagedRestoreContractArchive(t, provider, "backup-contract")
	node.setRestorePhase(
		backupcontract.RestoreStatusStaging,
		backupcontract.RestoreSlotStatusStaging,
		false,
	)
	command := stagedRestoreContractCommand(archive.reference)
	command.Action = backupcontract.RestoreNodeActionStage
	if _, err := service.Run(context.Background(), command); err != nil {
		t.Fatalf("Run(stage): %v", err)
	}
	node.setRestorePhase(
		backupcontract.RestoreStatusVerifying,
		backupcontract.RestoreSlotStatusStaged,
		false,
	)
	command.Action = backupcontract.RestoreNodeActionVerify
	target := service.targetDir(command)
	ready := filepath.Join(target, "READY")
	if err := os.Remove(ready); err != nil {
		t.Fatalf("Remove(READY): %v", err)
	}
	if _, err := service.Run(context.Background(), command); err == nil {
		t.Fatal("Run(verify without READY) error = nil")
	}
	if err := os.WriteFile(ready, []byte("ready\n"), restoreFileMode); err != nil {
		t.Fatalf("restore READY: %v", err)
	}
	metadataPath := filepath.Join(target, "metadata.bin")
	if err := os.WriteFile(metadataPath, nil, restoreFileMode); err != nil {
		t.Fatalf("truncate metadata: %v", err)
	}
	if _, err := service.Run(context.Background(), command); err == nil {
		t.Fatal("Run(verify empty metadata) error = nil")
	}
	if err := os.WriteFile(metadataPath, archive.metadata, restoreFileMode); err != nil {
		t.Fatalf("restore metadata: %v", err)
	}
	messagePath := filepath.Join(target, "messages-000001.bin")
	if err := os.WriteFile(messagePath, nil, restoreFileMode); err != nil {
		t.Fatalf("truncate messages: %v", err)
	}
	if _, err := service.Run(context.Background(), command); err == nil {
		t.Fatal("Run(verify empty messages) error = nil")
	}
	if err := os.WriteFile(messagePath, archive.messages[0], restoreFileMode); err != nil {
		t.Fatalf("restore messages: %v", err)
	}
	if _, err := service.Run(context.Background(), command); err != nil {
		t.Fatalf("Run(verify repaired streams): %v", err)
	}
}

type stagedRestoreContractArchive struct {
	reference    backupartifact.SlotReference
	logicalBytes uint64
	metadata     []byte
	messages     [][]byte
	chunkKey     string
	chunkBody    []byte
}

func writeStagedRestoreContractArchive(
	t *testing.T,
	provider *RepositoryProvider,
	backupID string,
) stagedRestoreContractArchive {
	t.Helper()
	store := openStagedRestoreContractStore(t, provider)
	metadata := []byte("portable-metadata")
	messages := [][]byte{
		[]byte("portable-message-stream-one"),
		[]byte("portable-message-stream-two"),
	}
	plain := append([][]byte{metadata}, messages...)
	chunks := make([]backupartifact.ChunkReference, 0, len(plain))
	var logicalBytes, storedBytes, records uint64
	var firstChunkKey string
	var firstChunkBody []byte
	for index, body := range plain {
		var encoded bytes.Buffer
		descriptor, err := backupartifact.EncodeChunk(&encoded, bytes.NewReader(body))
		if err != nil {
			t.Fatalf("EncodeChunk(%d): %v", index, err)
		}
		kind := backupartifact.ChunkKindMetadata
		sequence := uint32(1)
		stream := uint32(0)
		keyKind := "meta"
		maxMessageID := uint64(0)
		if index > 0 {
			kind = backupartifact.ChunkKindMessages
			sequence = uint32(index)
			stream = uint32(index)
			keyKind = "messages"
			maxMessageID = uint64(100 + index)
		}
		key := fmt.Sprintf(
			"slots/%03d/attempts/00000001-contract/%s-%06d.zst",
			stagedRestoreContractSlot, keyKind, sequence,
		)
		storedKey := "backups/" + backupID + "/" + key
		putStagedRestoreContractObject(t, store, storedKey, encoded.Bytes())
		if index == 0 {
			firstChunkKey = storedKey
			firstChunkBody = append([]byte(nil), encoded.Bytes()...)
		}
		chunks = append(chunks, backupartifact.ChunkReference{
			Kind: kind, Sequence: sequence, Stream: stream, Part: 1, Final: true,
			Key: key, Descriptor: descriptor, Records: 1,
			MaxMessageID: maxMessageID,
		})
		logicalBytes += descriptor.LogicalBytes
		storedBytes += descriptor.StoredBytes
		records++
	}
	manifest := backupartifact.SlotManifest{
		Format:   backupartifact.SlotManifestFormat,
		Version:  backupartifact.SlotManifestVersion,
		HashSlot: stagedRestoreContractSlot,
		Cut: backupartifact.SlotCut{
			PhysicalSlotID: 1, LeaderTerm: 2, AppliedTerm: 2,
			ConfigurationVersion: 3, AppliedIndex: 4,
			CapturedAtUnixMillis: 1_800_000_000_100,
		},
		Chunks: chunks, LogicalBytes: logicalBytes, StoredBytes: storedBytes,
		Records: records, MaxMessageID: 102,
	}
	manifestBody, err := backupartifact.MarshalSlotManifest(manifest)
	if err != nil {
		t.Fatalf("MarshalSlotManifest(): %v", err)
	}
	manifestKey := fmt.Sprintf(
		"slots/%03d/attempts/00000001-contract/manifest.json",
		stagedRestoreContractSlot,
	)
	putStagedRestoreContractObject(
		t, store, "backups/"+backupID+"/"+manifestKey, manifestBody,
	)
	manifestSum := sha256.Sum256(manifestBody)
	reference := backupartifact.SlotReference{
		HashSlot:       stagedRestoreContractSlot,
		ManifestKey:    manifestKey,
		ManifestSHA256: hex.EncodeToString(manifestSum[:]),
		LogicalBytes:   logicalBytes, StoredBytes: storedBytes,
		Records: records, MaxMessageID: 102,
	}

	slots := make([]backupartifact.SlotReference, backupartifact.DefaultHashSlotCount)
	for hashSlot := range slots {
		digest := sha256.Sum256([]byte(fmt.Sprintf("slot-%d", hashSlot)))
		slots[hashSlot] = backupartifact.SlotReference{
			HashSlot:       uint16(hashSlot),
			ManifestKey:    fmt.Sprintf("slots/%03d/manifest.json", hashSlot),
			ManifestSHA256: hex.EncodeToString(digest[:]),
		}
	}
	slots[stagedRestoreContractSlot] = reference
	archive := backupartifact.ArchiveManifest{
		Format:  backupartifact.ArchiveFormat,
		Version: backupartifact.ArchiveVersion,
		ID:      backupID, Trigger: backupartifact.TriggerManual,
		SourceClusterID:       "cluster-contract",
		SourceApplication:     "staged-restore-contract",
		HashSlotCount:         backupartifact.DefaultHashSlotCount,
		StartedAtUnixMillis:   1_800_000_000_000,
		CompletedAtUnixMillis: 1_800_000_001_000,
		CutStartedUnixMillis:  1_800_000_000_100,
		CutEndedUnixMillis:    1_800_000_000_900,
		Compression:           backupartifact.CompressionZstd,
		Checksum:              backupartifact.ChecksumSHA256,
		Slots:                 slots,
	}
	archiveBody, err := backupartifact.MarshalArchiveManifest(archive)
	if err != nil {
		t.Fatalf("MarshalArchiveManifest(): %v", err)
	}
	marker, err := backupartifact.NewCompleteMarker(archiveBody)
	if err != nil {
		t.Fatalf("NewCompleteMarker(): %v", err)
	}
	markerBody, err := backupartifact.MarshalCompleteMarker(marker)
	if err != nil {
		t.Fatalf("MarshalCompleteMarker(): %v", err)
	}
	putStagedRestoreContractObject(
		t, store, "backups/"+backupID+"/manifest.json", archiveBody,
	)
	putStagedRestoreContractObject(
		t, store, "backups/"+backupID+"/COMPLETE", markerBody,
	)
	return stagedRestoreContractArchive{
		reference: reference, logicalBytes: logicalBytes,
		metadata: metadata, messages: messages,
		chunkKey: firstChunkKey, chunkBody: firstChunkBody,
	}
}

func newStagedRestoreContractService(
	t *testing.T,
	dataDir string,
) (*StagedRestoreNodeService, *stagedRestoreContractNode, *RepositoryProvider) {
	t.Helper()
	provider, err := NewRepositoryProvider(dataDir, nil)
	if err != nil {
		t.Fatalf("NewRepositoryProvider(): %v", err)
	}
	node := &stagedRestoreContractNode{
		maintenance:  true,
		fenceNodeID:  9,
		fenceTerm:    5,
		liveMetadata: []byte("original-live-metadata"),
		liveMessages: []byte("original-live-messages"),
	}
	node.state = stagedRestoreContractState(nil)
	service, err := NewStagedRestoreNodeService(node, provider, dataDir)
	if err != nil {
		t.Fatalf("NewStagedRestoreNodeService(): %v", err)
	}
	return service, node, provider
}

func stagedRestoreContractCommand(
	reference backupartifact.SlotReference,
) backupcontract.RestoreNodeCommand {
	return backupcontract.RestoreNodeCommand{
		Store: backupcontract.StoreConfig{Kind: backupcontract.StoreKindFile},
		JobID: "restore-contract", BackupID: "backup-contract",
		HashSlot: stagedRestoreContractSlot, Attempt: 1,
		SlotReference:      reference,
		ControllerRevision: 42, TargetActivation: "activation-contract",
		MaxMessageID: 102, CoordinatorNodeID: 9, CoordinatorTerm: 5,
	}
}

func stagedRestoreContractState(
	job *controller.ScheduledRestoreJob,
) controller.ClusterState {
	return controller.ClusterState{
		Revision: 42,
		Nodes: []controller.Node{{
			NodeID: 1, Roles: []controller.NodeRole{controller.NodeRoleData},
			JoinState: controller.NodeJoinStateActive,
		}},
		Slots: []controller.SlotAssignment{{
			SlotID: 1, DesiredPeers: []uint64{1}, ConfigEpoch: 1,
		}},
		HashSlots: controller.HashSlotTable{
			SlotCount: backupcontract.HashSlotCount,
			Ranges: []controller.HashSlotRange{{
				From: 0, To: backupcontract.HashSlotCount - 1, SlotID: 1,
			}},
		},
		ScheduledBackup: &controller.ScheduledBackupState{
			Revision: 1,
			Plan: &controller.BackupPlan{
				Revision: 1,
				Store: controller.BackupStoreConfig{
					Kind: controller.BackupStoreKind(backupcontract.StoreKindFile),
				},
			},
			ActiveRestore: job,
		},
	}
}

type stagedRestorePayload struct {
	metadata []byte
	messages [][]byte
}

type stagedRestoreContractNode struct {
	state                 controller.ClusterState
	maintenance           bool
	fenceNodeID           uint64
	fenceTerm             uint64
	fenceErr              error
	stateErr              error
	liveMetadata          []byte
	liveMessages          []byte
	metadataSnapshotErr   error
	messageSnapshotErr    error
	metadataSnapshotCalls int
	messageSnapshotCalls  int
	verified              []stagedRestorePayload
	installed             []stagedRestorePayload
	activateCalls         int
	activateErr           error
	healthCalls           int
	healthErr             error
}

func (n *stagedRestoreContractNode) setRestorePhase(
	status backupcontract.RestoreStatus,
	slotStatus backupcontract.RestoreSlotStatus,
	allVerified bool,
) {
	slots := make([]controller.RestoreSlotProgress, backupcontract.HashSlotCount)
	for hashSlot := range slots {
		statusValue := backupcontract.RestoreSlotStatusPending
		if allVerified {
			statusValue = backupcontract.RestoreSlotStatusVerified
		}
		slots[hashSlot] = controller.RestoreSlotProgress{
			HashSlot: uint16(hashSlot), Status: string(statusValue),
		}
	}
	slots[stagedRestoreContractSlot] = controller.RestoreSlotProgress{
		HashSlot: stagedRestoreContractSlot, Status: string(slotStatus),
		Attempt: 1, ReplicaNodeIDs: []uint64{1},
	}
	job := &controller.ScheduledRestoreJob{
		ID: "restore-contract", BackupID: "backup-contract",
		Status: string(status), MaintenanceEntered: true,
		TargetActivation: "activation-contract", MaxMessageID: 102,
		Slots: slots,
	}
	n.state = stagedRestoreContractState(job)
}

func (n *stagedRestoreContractNode) lastVerified() stagedRestorePayload {
	if len(n.verified) == 0 {
		return stagedRestorePayload{}
	}
	return n.verified[len(n.verified)-1]
}

func (*stagedRestoreContractNode) NodeID() uint64 { return 1 }

func (n *stagedRestoreContractNode) BackupControllerFence(
	context.Context,
) (uint64, uint64, error) {
	return n.fenceNodeID, n.fenceTerm, n.fenceErr
}

func (n *stagedRestoreContractNode) LocalState(
	context.Context,
) (controller.ClusterState, error) {
	if n.stateErr != nil {
		return controller.ClusterState{}, n.stateErr
	}
	return n.state.Clone(), nil
}

func (n *stagedRestoreContractNode) RestoreMaintenanceReady() bool {
	return n.maintenance
}

func (n *stagedRestoreContractNode) OpenLocalRestoreMetadataSnapshot(
	context.Context,
	uint16,
) (io.ReadCloser, error) {
	n.metadataSnapshotCalls++
	if n.metadataSnapshotErr != nil {
		return nil, n.metadataSnapshotErr
	}
	return io.NopCloser(bytes.NewReader(n.liveMetadata)), nil
}

func (n *stagedRestoreContractNode) OpenLocalRestoreMessageSnapshot(
	context.Context,
	uint16,
) (clusterpkg.BackupMessageSnapshot, error) {
	n.messageSnapshotCalls++
	if n.messageSnapshotErr != nil {
		return clusterpkg.BackupMessageSnapshot{}, n.messageSnapshotErr
	}
	return clusterpkg.BackupMessageSnapshot{
		Reader: io.NopCloser(bytes.NewReader(n.liveMessages)),
	}, nil
}

func (n *stagedRestoreContractNode) VerifyLocalRestorePartitionStreams(
	_ context.Context,
	_ uint16,
	metadata io.ReadSeeker,
	_ int64,
	messages []clusterpkg.RestoreMessageStream,
) (uint64, error) {
	payload, err := readStagedRestorePayload(metadata, messages)
	if err != nil {
		return 0, err
	}
	n.verified = append(n.verified, payload)
	logical := uint64(len(payload.metadata))
	for _, body := range payload.messages {
		logical += uint64(len(body))
	}
	return logical, nil
}

func (n *stagedRestoreContractNode) InstallLocalRestorePartition(
	_ context.Context,
	_ uint16,
	metadata io.ReadSeeker,
	_ int64,
	messages []clusterpkg.RestoreMessageStream,
) error {
	payload, err := readStagedRestorePayload(metadata, messages)
	if err != nil {
		return err
	}
	n.installed = append(n.installed, payload)
	return nil
}

func (n *stagedRestoreContractNode) ActivateLocalRestore(context.Context) error {
	n.activateCalls++
	return n.activateErr
}

func (n *stagedRestoreContractNode) CheckLocalRestoreHealth(context.Context) error {
	n.healthCalls++
	return n.healthErr
}

func readStagedRestorePayload(
	metadata io.Reader,
	messages []clusterpkg.RestoreMessageStream,
) (stagedRestorePayload, error) {
	metadataBody, err := io.ReadAll(metadata)
	if err != nil {
		return stagedRestorePayload{}, err
	}
	payload := stagedRestorePayload{metadata: metadataBody}
	for _, message := range messages {
		body, err := io.ReadAll(message.Reader)
		if err != nil {
			return stagedRestorePayload{}, err
		}
		payload.messages = append(payload.messages, body)
	}
	return payload, nil
}

func assertStagedRestorePayload(
	t *testing.T,
	got stagedRestorePayload,
	wantMetadata []byte,
	wantMessages [][]byte,
) {
	t.Helper()
	if !bytes.Equal(got.metadata, wantMetadata) {
		t.Fatalf("metadata = %q, want %q", got.metadata, wantMetadata)
	}
	if len(got.messages) != len(wantMessages) {
		t.Fatalf("message streams = %d, want %d", len(got.messages), len(wantMessages))
	}
	for index := range wantMessages {
		if !bytes.Equal(got.messages[index], wantMessages[index]) {
			t.Fatalf(
				"message stream %d = %q, want %q",
				index, got.messages[index], wantMessages[index],
			)
		}
	}
}

func openStagedRestoreContractStore(
	t *testing.T,
	provider *RepositoryProvider,
) backupartifact.ArchiveStore {
	t.Helper()
	store, err := provider.Open(
		context.Background(),
		backupcontract.StoreConfig{Kind: backupcontract.StoreKindFile},
	)
	if err != nil {
		t.Fatalf("Open(file repository): %v", err)
	}
	return store
}

func putStagedRestoreContractObject(
	t *testing.T,
	store backupartifact.ArchiveStore,
	key string,
	body []byte,
) {
	t.Helper()
	if err := store.Put(context.Background(), backupartifact.PutObject{
		Key: key, Body: bytes.NewReader(body), ExpectedBytes: uint64(len(body)),
	}); err != nil {
		t.Fatalf("Put(%s): %v", key, err)
	}
}
