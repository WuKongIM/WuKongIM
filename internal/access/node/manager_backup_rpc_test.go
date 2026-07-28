package node

import (
	"context"
	"errors"
	"testing"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	backupusecase "github.com/WuKongIM/WuKongIM/internal/usecase/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
)

func TestManagerBackupRPCRejectsStaleLeaderAndRoutesWithoutReplay(t *testing.T) {
	local := &fakeManagerBackupRPC{status: backupusecase.StatusSnapshot{Enabled: true, Health: backupusecase.HealthHealthy}}
	leadership := &mutableManagerBackupLeadership{local: 2, leader: 1}
	adapter := NewManagerBackupAdapter(ManagerBackupOptions{Local: local, Leadership: leadership})
	raw := &fakeManagerBackupRPCNode{handler: adapter.HandleRPC}
	client := NewClient(raw)

	if _, err := client.ManagerBackupStatus(context.Background(), 2); !errors.Is(err, backupusecase.ErrControllerLeaderUnavailable) {
		t.Fatalf("ManagerBackupStatus(stale leader) error = %v", err)
	}
	if raw.calls != 1 || local.statusCalls != 0 {
		t.Fatalf("stale leader calls: transport=%d local=%d", raw.calls, local.statusCalls)
	}

	leadership.leader = 2
	status, err := client.ManagerBackupStatus(context.Background(), 2)
	if err != nil {
		t.Fatalf("ManagerBackupStatus() error = %v", err)
	}
	if !status.Enabled || status.Health != backupusecase.HealthHealthy || local.statusCalls != 1 {
		t.Fatalf("status = %#v local calls=%d", status, local.statusCalls)
	}

	local.publishErr = backupusecase.ErrStateConflict
	if _, err := client.ManagerBackupPublishCheckpoint(
		context.Background(), 2,
	); !errors.Is(err, backupusecase.ErrStateConflict) {
		t.Fatalf("ManagerBackupPublishCheckpoint() error = %v", err)
	}
	if raw.calls != 3 || local.publishCalls != 1 {
		t.Fatalf("write was replayed: transport=%d local=%d", raw.calls, local.publishCalls)
	}
}

func TestManagerBackupRPCLocalCaptureStatusDoesNotRequireControllerLeader(
	t *testing.T,
) {
	want := []backupcontract.SlotCaptureStatus{{
		HashSlot: 7, MetadataLag: 3, MessageLag: 5,
	}}
	local := &fakeManagerBackupRPC{status: backupusecase.StatusSnapshot{
		CaptureStatuses: want,
	}}
	adapter := NewManagerBackupAdapter(ManagerBackupOptions{
		Local: local,
		Leadership: &mutableManagerBackupLeadership{
			local: 2, leader: 1,
		},
	})
	raw := &fakeManagerBackupRPCNode{handler: adapter.HandleRPC}
	got, err := NewClient(raw).ManagerBackupLocalCaptureStatus(
		context.Background(), 2,
	)
	if err != nil {
		t.Fatalf("ManagerBackupLocalCaptureStatus() error = %v", err)
	}
	if len(got) != 1 || got[0].HashSlot != 7 ||
		got[0].MetadataLag != 3 || got[0].MessageLag != 5 {
		t.Fatalf("capture statuses = %#v", got)
	}
}

func TestManagerBackupRPCRequiresVersionedRequestAndResponseMagic(t *testing.T) {
	local := &fakeManagerBackupRPC{status: backupusecase.StatusSnapshot{Enabled: true}}
	adapter := NewManagerBackupAdapter(ManagerBackupOptions{
		Local:      local,
		Leadership: &mutableManagerBackupLeadership{local: 1, leader: 1},
	})

	request, err := encodeManagerBackupRequest(managerBackupRequest{Operation: managerBackupStatus})
	if err != nil {
		t.Fatalf("encodeManagerBackupRequest() error = %v", err)
	}
	if !hasMagic(request, managerBackupRequestMagic[:]) {
		t.Fatalf("request magic = %x", request)
	}
	response, err := adapter.HandleRPC(context.Background(), request)
	if err != nil {
		t.Fatalf("HandleRPC() error = %v", err)
	}
	if !hasMagic(response, managerBackupResponseMagic[:]) {
		t.Fatalf("response magic = %x", response)
	}
	if _, err := adapter.HandleRPC(context.Background(), []byte(`{"operation":"status"}`)); err == nil {
		t.Fatal("HandleRPC() accepted an unversioned JSON request")
	}
	var decoded managerBackupResponse
	if err := decodeManagerBackupResponse([]byte(`{"status":{"enabled":true}}`), &decoded); err == nil {
		t.Fatal("decodeManagerBackupResponse() accepted an unversioned JSON response")
	}
}

func TestManagerBackupRPCFencesSourceOnExactControllerLeader(t *testing.T) {
	request := backupusecase.SourceFenceRequest{
		RestorePlanID: "plan-1", CheckpointID: "checkpoint-1",
		TargetClusterID:  "target-cluster",
		TargetGeneration: "target-generation-1",
	}
	local := &fakeManagerBackupRPC{
		sourceFenceReceipt: backupusecase.SourceFenceReceipt{
			SourceFenceRecord: backupartifact.SourceFenceRecord{
				ID: "source-fence-1",
			},
		},
	}
	adapter := NewManagerBackupAdapter(ManagerBackupOptions{
		Local: local,
		Leadership: &mutableManagerBackupLeadership{
			local: 2, leader: 2,
		},
	})
	raw := &fakeManagerBackupRPCNode{handler: adapter.HandleRPC}
	receipt, err := NewClient(raw).ManagerBackupFenceSource(
		context.Background(), 2, request,
	)
	if err != nil || receipt.ID != "source-fence-1" ||
		local.sourceFenceCalls != 1 ||
		local.sourceFenceRequest != request ||
		raw.calls != 1 {
		t.Fatalf(
			"receipt=%#v err=%v local=%d request=%#v transport=%d",
			receipt, err, local.sourceFenceCalls,
			local.sourceFenceRequest, raw.calls,
		)
	}
}

func TestManagerBackupRPCFencesCheckpointHoldOnExactControllerLeader(
	t *testing.T,
) {
	local := &fakeManagerBackupRPC{}
	adapter := NewManagerBackupAdapter(ManagerBackupOptions{
		Local: local,
		Leadership: &mutableManagerBackupLeadership{
			local: 2, leader: 2,
		},
	})
	raw := &fakeManagerBackupRPCNode{handler: adapter.HandleRPC}
	checkpoint, err := NewClient(raw).ManagerBackupSetCheckpointHold(
		context.Background(), 2, " checkpoint-7 ", true,
	)
	if err != nil || checkpoint.ID != "checkpoint-7" ||
		!checkpoint.Held || local.holdCalls != 1 ||
		local.holdCheckpointID != "checkpoint-7" || !local.held ||
		raw.calls != 1 {
		t.Fatalf(
			"checkpoint=%#v err=%v local=%d id=%q held=%v transport=%d",
			checkpoint, err, local.holdCalls, local.holdCheckpointID,
			local.held, raw.calls,
		)
	}
}

type mutableManagerBackupLeadership struct {
	local  uint64
	leader uint64
}

func (l *mutableManagerBackupLeadership) NodeID() uint64                   { return l.local }
func (l *mutableManagerBackupLeadership) BackupControllerLeaderID() uint64 { return l.leader }

type fakeManagerBackupRPCNode struct {
	handler func(context.Context, []byte) ([]byte, error)
	calls   int
}

func (n *fakeManagerBackupRPCNode) CallRPC(ctx context.Context, _ uint64, _ uint8, payload []byte) ([]byte, error) {
	n.calls++
	return n.handler(ctx, payload)
}

type fakeManagerBackupRPC struct {
	status             backupusecase.StatusSnapshot
	statusCalls        int
	publishCalls       int
	publishErr         error
	sourceFenceRequest backupusecase.SourceFenceRequest
	sourceFenceReceipt backupusecase.SourceFenceReceipt
	sourceFenceCalls   int
	holdCheckpointID   string
	held               bool
	holdCalls          int
}

func (f *fakeManagerBackupRPC) Status(context.Context) (backupusecase.StatusSnapshot, error) {
	f.statusCalls++
	return f.status, nil
}

func (f *fakeManagerBackupRPC) LocalCaptureStatus(
	context.Context,
) []backupcontract.SlotCaptureStatus {
	return f.status.CaptureStatuses
}

func (f *fakeManagerBackupRPC) PublishCheckpoint(context.Context) (backupusecase.CheckpointPublication, error) {
	f.publishCalls++
	return backupusecase.CheckpointPublication{}, f.publishErr
}

func (f *fakeManagerBackupRPC) SetCheckpointHold(
	_ context.Context,
	checkpointID string,
	held bool,
) (backupusecase.CheckpointSummary, error) {
	f.holdCalls++
	f.holdCheckpointID = checkpointID
	f.held = held
	return backupusecase.CheckpointSummary{
		ID: checkpointID, Held: held,
	}, nil
}

func (f *fakeManagerBackupRPC) FenceSource(
	_ context.Context,
	request backupusecase.SourceFenceRequest,
) (backupusecase.SourceFenceReceipt, error) {
	f.sourceFenceCalls++
	f.sourceFenceRequest = request
	return f.sourceFenceReceipt, nil
}
