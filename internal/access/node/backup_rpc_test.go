package node

import (
	"context"
	"strings"
	"testing"

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
	if node.serviceID != BackupMessageShardRPCServiceID || len(captured.Objects) != 1 || service.shard.ID != shard.ID {
		t.Fatalf("rpc/service = %d captured=%#v shard=%#v", node.serviceID, captured, service.shard)
	}
}

func TestBackupMessageShardCodecRejectsUnknownFields(t *testing.T) {
	body := append(append([]byte(nil), backupMessageShardRequestMagic[:]...), []byte(`{"capture":{},"shard":{},"unknown":true}`)...)
	if _, err := decodeBackupMessageShardRequest(body); err == nil {
		t.Fatal("decodeBackupMessageShardRequest() error = nil")
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

type fakeBackupMessageCapturer struct {
	shard   runtimebackup.MessageShard
	objects []backupartifact.ObjectEntry
}

func (f *fakeBackupMessageCapturer) CaptureMessageShard(_ context.Context, _ runtimebackup.CaptureRequest, shard runtimebackup.MessageShard) (runtimebackup.MessageShardCapture, error) {
	f.shard = shard
	return runtimebackup.MessageShardCapture{Objects: f.objects}, nil
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
	return backupusecase.RestorePartition{HashSlot: hashSlot, Installed: true, MetadataSHA256: strings.Repeat("b", 64)}, nil
}

type fakeBackupRestoreVerifier struct {
	hashSlot       uint16
	metadataSHA256 string
	boundaries     []clusterpkg.RestoreVerifyBoundary
}

func (f *fakeBackupRestoreVerifier) VerifyLocalRestorePartition(_ context.Context, hashSlot uint16, metadataSHA256 string, boundaries []clusterpkg.RestoreVerifyBoundary) error {
	f.hashSlot = hashSlot
	f.metadataSHA256 = metadataSHA256
	f.boundaries = append([]clusterpkg.RestoreVerifyBoundary(nil), boundaries...)
	return nil
}
