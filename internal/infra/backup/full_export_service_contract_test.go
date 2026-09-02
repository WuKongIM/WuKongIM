package backup_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	backupinfra "github.com/WuKongIM/WuKongIM/internal/infra/backup"
	backupusecase "github.com/WuKongIM/WuKongIM/internal/usecase/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
)

func TestFullExportMessagesPublishesAnAuthenticatedAttemptManifestAndClosesTheSnapshot(t *testing.T) {
	node := newFullExportNodeStub(t, 7)
	service, provider, _ := newFullExportService(t, node)
	command := validMessageExportCommand()

	receipt, err := service.ExportMessages(context.Background(), command)
	if err != nil {
		t.Fatalf("ExportMessages(): %v", err)
	}
	if receipt.ChunkCount != 1 || receipt.Records != 3 ||
		receipt.MaxMessageID != 99 || receipt.ManifestSHA256 == "" {
		t.Fatalf("receipt = %+v", receipt)
	}
	if len(node.messageReaders) != 1 || node.messageReaders[0].closeCount != 1 {
		t.Fatalf("message snapshot closes = %d", node.messageReaders[0].closeCount)
	}
	if len(node.messageFences) != 1 || len(node.messageFences[0]) != 1 {
		t.Fatalf("message fences = %#v", node.messageFences)
	}
	fence := node.messageFences[0][0]
	if fence.ChannelID != "room" || fence.ChannelType != 2 ||
		fence.LeaderNodeID != 9 || fence.ChannelEpoch != 11 ||
		fence.LeaderEpoch != 12 || fence.MinISR != 2 ||
		fence.RetentionThroughSeq != 8 {
		t.Fatalf("message fence = %+v", fence)
	}
	store := openFileBackupStore(t, provider)
	manifest, err := backupartifact.LoadStoredMessageChunkManifest(
		context.Background(), store, command.BackupID,
		receipt.ManifestKey, receipt.ManifestSHA256,
	)
	if err != nil {
		t.Fatalf("LoadStoredMessageChunkManifest(): %v", err)
	}
	if len(manifest.Chunks) != 1 ||
		manifest.Chunks[0].Sequence != command.FirstSequence ||
		manifest.Chunks[0].Stream != command.StreamNumber ||
		manifest.Chunks[0].Records != 3 ||
		manifest.Chunks[0].MaxMessageID != 99 {
		t.Fatalf("message manifest = %+v", manifest)
	}
}

func TestFullExportMessagesClosesThePinnedSnapshotWhenTheRepositoryCannotOpen(t *testing.T) {
	node := newFullExportNodeStub(t, 7)
	service, _, _ := newFullExportService(t, node)
	command := validMessageExportCommand()
	command.Store.Kind = backupcontract.StoreKind("unsupported")

	if _, err := service.ExportMessages(context.Background(), command); err == nil {
		t.Fatal("ExportMessages() error = nil")
	}
	if len(node.messageReaders) != 1 || node.messageReaders[0].closeCount != 1 {
		t.Fatalf("message snapshot closes = %#v", node.messageReaders)
	}
}

func TestFullExportMessagesDoesNotPublishAReceiptAfterTheCoordinatorFenceChanges(t *testing.T) {
	node := newFullExportNodeStub(t, 7)
	node.controllerFences = []controllerFenceResult{
		{nodeID: 9, term: 5},
		{nodeID: 3, term: 6},
	}
	service, provider, _ := newFullExportService(t, node)
	command := validMessageExportCommand()

	receipt, err := service.ExportMessages(context.Background(), command)
	if err == nil || receipt != (backupcontract.MessageExportReceipt{}) {
		t.Fatalf("ExportMessages() = %+v, %v, want fenced failure", receipt, err)
	}
	if len(node.messageReaders) != 1 || node.messageReaders[0].closeCount != 1 {
		t.Fatalf("message snapshot closes = %#v", node.messageReaders)
	}
	store := openFileBackupStore(t, provider)
	manifestKey := command.ArtifactPrefix + "/message-stream-000002-manifest.json"
	reader, _, openErr := store.Open(
		context.Background(), "backups/"+command.BackupID+"/"+manifestKey,
	)
	if reader != nil {
		_ = reader.Close()
	}
	if !errors.Is(openErr, backupartifact.ErrObjectNotFound) {
		t.Fatalf("message manifest Open() error = %v, want not found", openErr)
	}
}

func TestFullExportSlotPublishesOnlyAfterBothAuthorityRechecks(t *testing.T) {
	node := newFullExportNodeStub(t, 7)
	service, provider, _ := newFullExportService(t, node)
	command := validSlotExportCommand()

	receipt, err := service.ExportSlot(context.Background(), command)
	if err != nil {
		t.Fatalf("ExportSlot(): %v", err)
	}
	wantKey := "slots/007/attempts/00000001-00000000000000000009-00000000000000000005/manifest.json"
	if receipt.ManifestKey != wantKey || receipt.ManifestSHA256 == "" ||
		receipt.Records == 0 {
		t.Fatalf("receipt = %+v", receipt)
	}
	if node.authorityCalls != 2 || node.controllerCalls != 3 {
		t.Fatalf(
			"authority/controller checks = %d/%d, want 2/3",
			node.authorityCalls, node.controllerCalls,
		)
	}
	if len(node.captureReaders) != 2 ||
		node.captureReaders[0].closeCount != 1 ||
		node.captureReaders[1].closeCount != 1 {
		t.Fatalf("capture close counts = %#v", captureCloseCounts(node))
	}
	store := openFileBackupStore(t, provider)
	reference, manifest, err := backupartifact.LoadStoredSlotReference(
		context.Background(), store, command.BackupID,
		backupartifact.SlotReference{
			HashSlot: command.HashSlot, ManifestKey: receipt.ManifestKey,
			ManifestSHA256: receipt.ManifestSHA256,
			LogicalBytes:   receipt.LogicalBytes, StoredBytes: receipt.StoredBytes,
			Records: receipt.Records, MaxMessageID: receipt.MaxMessageID,
		},
		true,
	)
	if err != nil {
		t.Fatalf("LoadStoredSlotReference(): %v", err)
	}
	if reference.HashSlot != 7 || manifest.Cut.PhysicalSlotID != 4 ||
		manifest.Cut.ConfigurationVersion != 31 || len(manifest.Chunks) != 1 ||
		manifest.Chunks[0].Kind != backupartifact.ChunkKindMetadata {
		t.Fatalf("reference/manifest = %+v / %+v", reference, manifest)
	}
}

func TestFullExportSlotWithholdsTheReceiptWhenFinalCoordinatorValidationFails(t *testing.T) {
	node := newFullExportNodeStub(t, 7)
	node.controllerFences = []controllerFenceResult{
		{nodeID: 9, term: 5},
		{nodeID: 9, term: 5},
		{nodeID: 2, term: 6},
	}
	service, provider, _ := newFullExportService(t, node)
	command := validSlotExportCommand()

	receipt, err := service.ExportSlot(context.Background(), command)
	if err == nil || receipt != (backupcontract.SlotExportReceipt{}) {
		t.Fatalf("ExportSlot() = %+v, %v, want fenced failure", receipt, err)
	}
	if node.authorityCalls != 1 {
		t.Fatalf("authority checks = %d, want one check before manifest write", node.authorityCalls)
	}
	store := openFileBackupStore(t, provider)
	manifestKey := "backups/backup-1/slots/007/attempts/00000001-00000000000000000009-00000000000000000005/manifest.json"
	reader, _, openErr := store.Open(context.Background(), manifestKey)
	if openErr != nil {
		t.Fatalf("attempt manifest Open(): %v", openErr)
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("attempt manifest Close(): %v", err)
	}
	complete, _, completeErr := store.Open(
		context.Background(), "backups/backup-1/COMPLETE",
	)
	if complete != nil {
		_ = complete.Close()
	}
	if !errors.Is(completeErr, backupartifact.ErrObjectNotFound) {
		t.Fatalf("COMPLETE Open() error = %v, want not found", completeErr)
	}
}

func TestDistributedSlotExecutorRoutesTheFencedCommandWithoutArchivePayloads(t *testing.T) {
	node := newFullExportNodeStub(t, 7)
	node.route.Leader = 2
	node.route.LeaderTerm = 17
	service, _, remote := newFullExportService(t, node)
	executor, err := backupinfra.NewDistributedSlotExecutor(node, service, remote)
	if err != nil {
		t.Fatalf("NewDistributedSlotExecutor(): %v", err)
	}
	authority, err := executor.Authority(context.Background(), 7)
	if err != nil {
		t.Fatalf("Authority(): %v", err)
	}
	if authority != (backupusecase.SlotAuthority{NodeID: 2, Term: 17}) {
		t.Fatalf("authority = %+v", authority)
	}
	remote.slotReceipt = backupcontract.SlotExportReceipt{
		ManifestKey:    "slots/007/attempts/a/manifest.json",
		ManifestSHA256: fmt.Sprintf("%064x", 1),
		LogicalBytes:   10, StoredBytes: 8, Records: 2, MaxMessageID: 3,
	}
	result, err := executor.ExportSlot(
		context.Background(),
		backupcontract.Plan{Store: backupcontract.StoreConfig{Kind: backupcontract.StoreKindFile}},
		"backup-remote", 7, 4, authority,
	)
	if err != nil {
		t.Fatalf("ExportSlot(): %v", err)
	}
	if result.ManifestKey != remote.slotReceipt.ManifestKey ||
		result.Records != 2 || result.MaxMessageID != 3 {
		t.Fatalf("result = %+v", result)
	}
	if len(remote.slotCalls) != 1 || remote.slotCalls[0].nodeID != 2 {
		t.Fatalf("remote calls = %#v", remote.slotCalls)
	}
	command := remote.slotCalls[0].command
	if command.BackupID != "backup-remote" || command.HashSlot != 7 ||
		command.Attempt != 4 || command.OwnerNodeID != 2 ||
		command.OwnerTerm != 17 || command.CoordinatorNodeID != 9 ||
		command.CoordinatorTerm != 5 {
		t.Fatalf("remote command = %+v", command)
	}
}

func validMessageExportCommand() backupcontract.MessageExportCommand {
	return backupcontract.MessageExportCommand{
		Store:          backupcontract.StoreConfig{Kind: backupcontract.StoreKindFile},
		BackupID:       "backup-1",
		HashSlot:       7,
		ArtifactPrefix: "slots/007/attempts/message-contract",
		Shard: backupcontract.MessageShard{
			ID: "n9-0000", NodeID: 9,
			Channels: []backupcontract.ChannelFence{{
				ChannelID: "room", ChannelType: 2, LeaderNodeID: 9,
				ChannelEpoch: 11, LeaderEpoch: 12, MinISR: 2,
				RetentionThroughSeq: 8,
			}},
		},
		FirstSequence: 4, StreamNumber: 2, RateBytesPerSec: 1 << 30,
		CoordinatorNodeID: 9, CoordinatorTerm: 5,
	}
}

func validSlotExportCommand() backupcontract.SlotExportCommand {
	return backupcontract.SlotExportCommand{
		Plan: backupcontract.Plan{
			Store:           backupcontract.StoreConfig{Kind: backupcontract.StoreKindFile},
			RateBytesPerSec: 1 << 30,
		},
		BackupID: "backup-1", HashSlot: 7, Attempt: 1,
		OwnerNodeID: 9, OwnerTerm: 9,
		CoordinatorNodeID: 9, CoordinatorTerm: 5,
	}
}

func newFullExportService(
	t *testing.T,
	node *fullExportNodeStub,
) (*backupinfra.FullExportService, *backupinfra.RepositoryProvider, *fullExportRemoteStub) {
	t.Helper()
	provider, err := backupinfra.NewRepositoryProvider(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("NewRepositoryProvider(): %v", err)
	}
	remote := &fullExportRemoteStub{}
	service, err := backupinfra.NewFullExportService(
		node, provider, remote, t.TempDir(),
	)
	if err != nil {
		t.Fatalf("NewFullExportService(): %v", err)
	}
	return service, provider, remote
}

func openFileBackupStore(
	t *testing.T,
	provider *backupinfra.RepositoryProvider,
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

type fullExportSlotCall struct {
	nodeID  uint64
	command backupcontract.SlotExportCommand
}

type fullExportRemoteStub struct {
	slotCalls   []fullExportSlotCall
	slotReceipt backupcontract.SlotExportReceipt
	slotErr     error
}

func (s *fullExportRemoteStub) ExportBackupSlot(
	_ context.Context,
	nodeID uint64,
	command backupcontract.SlotExportCommand,
) (backupcontract.SlotExportReceipt, error) {
	s.slotCalls = append(s.slotCalls, fullExportSlotCall{
		nodeID: nodeID, command: command,
	})
	return s.slotReceipt, s.slotErr
}

func (s *fullExportRemoteStub) ExportBackupMessages(
	context.Context,
	uint64,
	backupcontract.MessageExportCommand,
) (backupcontract.MessageExportReceipt, error) {
	return backupcontract.MessageExportReceipt{},
		errors.New("unexpected remote message export")
}

var _ backupinfra.RemoteFullExportClient = (*fullExportRemoteStub)(nil)
