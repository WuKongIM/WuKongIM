package node

import (
	"context"
	"errors"
	"testing"

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

	local.triggerErr = backupusecase.ErrJobActive
	if _, err := client.ManagerBackupTrigger(context.Background(), 2, backupartifact.RestorePointMaterializedFull); !errors.Is(err, backupusecase.ErrJobActive) {
		t.Fatalf("ManagerBackupTrigger() error = %v", err)
	}
	if raw.calls != 3 || local.triggerCalls != 1 {
		t.Fatalf("write was replayed: transport=%d local=%d", raw.calls, local.triggerCalls)
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
		RestorePlanID: "plan-1", RestorePointID: "checkpoint-1",
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
	triggerCalls       int
	triggerErr         error
	sourceFenceRequest backupusecase.SourceFenceRequest
	sourceFenceReceipt backupusecase.SourceFenceReceipt
	sourceFenceCalls   int
}

func (f *fakeManagerBackupRPC) Status(context.Context) (backupusecase.StatusSnapshot, error) {
	f.statusCalls++
	return f.status, nil
}

func (f *fakeManagerBackupRPC) ListRestorePointsPage(context.Context, backupusecase.RestorePointListRequest) (backupusecase.RestorePointPage, error) {
	return backupusecase.RestorePointPage{}, nil
}

func (f *fakeManagerBackupRPC) PublishCheckpoint(context.Context) (backupusecase.CheckpointPublication, error) {
	return backupusecase.CheckpointPublication{}, nil
}

func (f *fakeManagerBackupRPC) Trigger(_ context.Context, kind backupartifact.RestorePointKind) (backupusecase.Job, error) {
	f.triggerCalls++
	return backupusecase.Job{ID: "job-1", Kind: kind}, f.triggerErr
}

func (f *fakeManagerBackupRPC) Cancel(context.Context, string, uint64) (backupusecase.Job, error) {
	return backupusecase.Job{}, nil
}

func (f *fakeManagerBackupRPC) Hold(context.Context, string) (backupusecase.RestorePoint, error) {
	return backupusecase.RestorePoint{}, nil
}

func (f *fakeManagerBackupRPC) Release(context.Context, string) (backupusecase.RestorePoint, error) {
	return backupusecase.RestorePoint{}, nil
}

func (f *fakeManagerBackupRPC) StartVerification(context.Context, string) (backupusecase.VerificationTask, error) {
	return backupusecase.VerificationTask{}, nil
}

func (f *fakeManagerBackupRPC) FenceSource(
	_ context.Context,
	request backupusecase.SourceFenceRequest,
) (backupusecase.SourceFenceReceipt, error) {
	f.sourceFenceCalls++
	f.sourceFenceRequest = request
	return f.sourceFenceReceipt, nil
}
