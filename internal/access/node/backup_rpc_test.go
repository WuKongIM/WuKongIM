package node

import (
	"context"
	"strings"
	"testing"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	runtimebackup "github.com/WuKongIM/WuKongIM/internal/runtime/backup"
	backupusecase "github.com/WuKongIM/WuKongIM/internal/usecase/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
	clusterpkg "github.com/WuKongIM/WuKongIM/pkg/cluster"
)

func TestBackupMessageShardRPCUsesBoundedSourceNodePort(t *testing.T) {
	service := &fakeBackupMessageCapturer{objects: []backupartifact.ObjectEntry{{Key: "objects/job/00001/messages-n2-0000-000000.bin"}}}
	adapter := New(Options{BackupMessages: service})
	request := runtimebackup.CaptureRequest{JobID: "job", BackupEpoch: 4, HashSlot: 1, ConfigFingerprint: strings.Repeat("a", 64)}
	shard := runtimebackup.MessageShard{ID: "n2-0000", NodeID: 2, Channels: []runtimebackup.ChannelFence{{ChannelID: "room", ChannelType: 2, LeaderNodeID: 2, ChannelEpoch: 3, LeaderEpoch: 4, MinISR: 2}}}
	node := &fakeManagerConnectionRPCNode{handler: adapter.HandleBackupMessageShardRPC}
	captured, err := NewClient(node).CaptureBackupMessageShard(context.Background(), 2, request, shard)
	if err != nil {
		t.Fatalf("CaptureBackupMessageShard(): %v", err)
	}
	if node.serviceID != BackupMessageShardRPCServiceID || len(captured.Objects) != 1 || captured.MessageRecords != 3 || captured.MaxMessageID != 99 || service.shard.ID != shard.ID {
		t.Fatalf("rpc/service = %d captured=%#v shard=%#v", node.serviceID, captured, service.shard)
	}
}

func TestBackupMessageShardCodecRejectsUnknownFields(t *testing.T) {
	body := append(append([]byte(nil), backupMessageShardRequestMagic[:]...), []byte(`{"capture":{},"shard":{},"unknown":true}`)...)
	if _, err := decodeBackupMessageShardRequest(body); err == nil {
		t.Fatal("decodeBackupMessageShardRequest() error = nil")
	}
}

func TestBackupEvidenceCodecsRejectV1WireShapes(t *testing.T) {
	messageBody, err := encodeBackupMessageShardResponse(backupMessageShardRPCResponse{Status: rpcStatusOK})
	if err != nil {
		t.Fatalf("encodeBackupMessageShardResponse(): %v", err)
	}
	messageBody[4] = 1
	if _, err := decodeBackupMessageShardResponse(messageBody); err == nil {
		t.Fatal("decodeBackupMessageShardResponse(v1) error = nil")
	}

	plan := backupusecase.RestorePlan{
		ID: "plan-1", RestorePointID: "restore-1", ManifestSHA256: strings.Repeat("a", 64),
		Repository: "primary", HashSlotCount: 1, Partitions: []backupusecase.RestorePartition{{HashSlot: 0}},
		ErasureLedgerVersion: backupartifact.ErasureLedgerSnapshotVersion, ErasureLedgerSHA256: backupartifact.EmptyErasureLedgerSnapshotSHA256,
	}
	installBody, err := encodeBackupRestoreInstallRequest(backupRestoreInstallRPCRequest{Plan: plan})
	if err != nil {
		t.Fatalf("encodeBackupRestoreInstallRequest(): %v", err)
	}
	installBody[4] = 1
	if _, err := decodeBackupRestoreInstallRequest(installBody); err == nil {
		t.Fatal("decodeBackupRestoreInstallRequest(v1) error = nil")
	}

	responseBody, err := encodeBackupRestoreInstallResponse(backupRestoreInstallRPCResponse{Status: rpcStatusOK})
	if err != nil {
		t.Fatalf("encodeBackupRestoreInstallResponse(): %v", err)
	}
	responseBody[4] = 1
	if _, err := decodeBackupRestoreInstallResponse(responseBody); err == nil {
		t.Fatal("decodeBackupRestoreInstallResponse(v1) error = nil")
	}
}

func TestBackupRestoreTargetRPCReturnsExactNodeEvidence(t *testing.T) {
	service := fakeBackupRestoreTargetInspector{state: clusterpkg.RestoreTargetLocalState{
		NodeID: 2, Empty: true, MetadataEmpty: true, MessagesEmpty: true,
	}}
	adapter := New(Options{BackupRestoreTarget: service})
	node := &fakeManagerConnectionRPCNode{handler: adapter.HandleBackupRestoreTargetRPC}
	state, err := NewClient(node).InspectBackupRestoreTarget(context.Background(), 2)
	if err != nil {
		t.Fatalf("InspectBackupRestoreTarget(): %v", err)
	}
	if node.serviceID != BackupRestoreTargetRPCServiceID || !state.Empty || state.NodeID != 2 {
		t.Fatalf("rpc/service = %d state=%#v", node.serviceID, state)
	}
}

func TestBackupRestoreInstallRPCReturnsPartitionReport(t *testing.T) {
	service := &fakeBackupRestoreInstaller{}
	adapter := New(Options{BackupRestoreInstaller: service})
	node := &fakeManagerConnectionRPCNode{handler: adapter.HandleBackupRestoreInstallRPC}
	plan := backupusecase.RestorePlan{
		ID: "plan-1", RestorePointID: "restore-1", ManifestSHA256: strings.Repeat("a", 64),
		Repository: "primary", HashSlotCount: 1, Partitions: []backupusecase.RestorePartition{{HashSlot: 0}},
		ErasureLedgerVersion: backupartifact.ErasureLedgerSnapshotVersion, ErasureLedgerSHA256: backupartifact.EmptyErasureLedgerSnapshotSHA256,
	}
	report, err := NewClient(node).InstallBackupRestorePartition(context.Background(), 2, plan, 0)
	if err != nil {
		t.Fatalf("InstallBackupRestorePartition(): %v", err)
	}
	if node.serviceID != BackupRestoreInstallRPCServiceID || !report.Installed || service.plan.ID != plan.ID {
		t.Fatalf("rpc/service = %d report=%#v plan=%#v", node.serviceID, report, service.plan)
	}
}

func TestBackupRestoreVerifyRPCChecksBoundedCuts(t *testing.T) {
	service := &fakeBackupRestoreVerifier{}
	adapter := New(Options{BackupRestoreVerifier: service})
	node := &fakeManagerConnectionRPCNode{handler: adapter.HandleBackupRestoreVerifyRPC}
	boundaries := []clusterpkg.RestoreVerifyBoundary{{ChannelID: "room", ChannelType: 2, Epoch: 3, HW: 8}}
	digest := strings.Repeat("a", 64)
	if err := NewClient(node).VerifyBackupRestorePartition(context.Background(), 2, 7, digest, boundaries); err != nil {
		t.Fatalf("VerifyBackupRestorePartition(): %v", err)
	}
	if node.serviceID != BackupRestoreVerifyRPCServiceID || service.hashSlot != 7 || service.metadataSHA256 != digest || len(service.boundaries) != 1 {
		t.Fatalf("rpc/service = %d slot=%d digest=%q boundaries=%#v", node.serviceID, service.hashSlot, service.metadataSHA256, service.boundaries)
	}
}

func TestBackupCheckpointReplicaRPCRoundTripsBoundedTransfer(t *testing.T) {
	service := &fakeBackupCheckpointReplicaReceiver{}
	adapter := New(Options{BackupCheckpointReplica: service})
	node := &fakeManagerConnectionRPCNode{
		handler: adapter.HandleBackupCheckpointReplicaRPC,
	}
	client := NewClient(node)
	fence := backupcontract.CheckpointReplicaFence{
		PlanID: "plan-1", CheckpointID: "checkpoint-1",
		CheckpointSHA256: strings.Repeat("a", 64),
		TargetGeneration: "target-1",
		HashSlot:         7, TargetSlotID: 8, ReplicaCount: 3,
		LeaderNodeID: 2, LeaderTerm: 9, ConfigEpoch: 4, Attempt: 1,
	}
	files := []backupcontract.CheckpointReplicaFile{
		{Kind: backupcontract.CheckpointReplicaMetadata, Size: 4, SHA256: strings.Repeat("b", 64)},
		{Kind: backupcontract.CheckpointReplicaErasures, Size: 0, SHA256: strings.Repeat("c", 64)},
	}
	_, err := client.HandleCheckpointReplica(
		context.Background(), 2,
		backupcontract.CheckpointReplicaRequest{
			Action: backupcontract.CheckpointReplicaBegin,
			Fence:  fence, Files: files,
			Evidence: backupartifact.RestoreEvidence{
				Version: backupartifact.RestoreEvidenceVersion,
			},
			InstalledAtUnixMillis: 1_753_400_200_000,
		},
	)
	if err != nil {
		t.Fatalf("HandleCheckpointReplica(begin): %v", err)
	}
	chunk, err := client.HandleCheckpointReplica(
		context.Background(), 2,
		backupcontract.CheckpointReplicaRequest{
			Action: backupcontract.CheckpointReplicaChunk,
			Fence:  fence, File: files[0], Data: []byte("meta"),
		},
	)
	if err != nil {
		t.Fatalf("HandleCheckpointReplica(chunk): %v", err)
	}
	completed, err := client.HandleCheckpointReplica(
		context.Background(), 2,
		backupcontract.CheckpointReplicaRequest{
			Action: backupcontract.CheckpointReplicaStatus, Fence: fence,
		},
	)
	if err != nil {
		t.Fatalf("HandleCheckpointReplica(status): %v", err)
	}
	cleaned, err := client.HandleCheckpointReplica(
		context.Background(), 2,
		backupcontract.CheckpointReplicaRequest{
			Action: backupcontract.CheckpointReplicaCleanup, Fence: fence,
		},
	)
	if err != nil {
		t.Fatalf("HandleCheckpointReplica(cleanup): %v", err)
	}
	if node.serviceID != BackupCheckpointReplicaRPCServiceID ||
		len(service.requests) != 4 ||
		service.requests[1].File != files[0] ||
		string(service.requests[1].Data) != "meta" ||
		chunk.AcceptedOffset != 4 || !completed.Completed ||
		completed.MetadataSHA256 != strings.Repeat("b", 64) ||
		!cleaned.Completed {
		t.Fatalf(
			"rpc/service=%d requests=%#v chunk=%#v completed=%#v",
			node.serviceID, service.requests, chunk, completed,
		)
	}
}

type fakeBackupMessageCapturer struct {
	shard   runtimebackup.MessageShard
	objects []backupartifact.ObjectEntry
}

func (f *fakeBackupMessageCapturer) CaptureMessageShard(_ context.Context, _ runtimebackup.CaptureRequest, shard runtimebackup.MessageShard) (runtimebackup.MessageShardCapture, error) {
	f.shard = shard
	return runtimebackup.MessageShardCapture{Objects: f.objects, MessageRecords: 3, MaxMessageID: 99}, nil
}

type fakeBackupRestoreTargetInspector struct {
	state clusterpkg.RestoreTargetLocalState
}

func (f fakeBackupRestoreTargetInspector) InspectLocalRestoreTarget(context.Context) (clusterpkg.RestoreTargetLocalState, error) {
	return f.state, nil
}

type fakeBackupRestoreInstaller struct{ plan backupusecase.RestorePlan }

func (f *fakeBackupRestoreInstaller) InstallPartition(_ context.Context, plan backupusecase.RestorePlan, hashSlot uint16) (backupusecase.RestorePartition, error) {
	f.plan = plan
	return backupusecase.RestorePartition{HashSlot: hashSlot, EvidenceVersion: backupartifact.PartitionEvidenceVersion, Installed: true, MetadataSHA256: strings.Repeat("b", 64)}, nil
}

type fakeBackupRestoreVerifier struct {
	hashSlot       uint16
	metadataSHA256 string
	boundaries     []clusterpkg.RestoreVerifyBoundary
}

type fakeBackupCheckpointReplicaReceiver struct {
	requests []backupcontract.CheckpointReplicaRequest
}

func (f *fakeBackupCheckpointReplicaReceiver) HandleCheckpointReplica(
	_ context.Context,
	request backupcontract.CheckpointReplicaRequest,
) (backupcontract.CheckpointReplicaResponse, error) {
	request.Data = append([]byte(nil), request.Data...)
	f.requests = append(f.requests, request)
	switch request.Action {
	case backupcontract.CheckpointReplicaChunk:
		return backupcontract.CheckpointReplicaResponse{
			AcceptedOffset: request.Offset + int64(len(request.Data)),
		}, nil
	case backupcontract.CheckpointReplicaStatus:
		return backupcontract.CheckpointReplicaResponse{
			Completed: true, MetadataSHA256: strings.Repeat("b", 64),
			InstalledBytes: 4,
		}, nil
	case backupcontract.CheckpointReplicaCleanup:
		return backupcontract.CheckpointReplicaResponse{Completed: true}, nil
	default:
		return backupcontract.CheckpointReplicaResponse{}, nil
	}
}

func (f *fakeBackupRestoreVerifier) VerifyLocalRestorePartition(_ context.Context, hashSlot uint16, metadataSHA256 string, boundaries []clusterpkg.RestoreVerifyBoundary) error {
	f.hashSlot = hashSlot
	f.metadataSHA256 = metadataSHA256
	f.boundaries = append([]clusterpkg.RestoreVerifyBoundary(nil), boundaries...)
	return nil
}
