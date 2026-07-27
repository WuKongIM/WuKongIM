package app

import (
	"context"
	"errors"
	"testing"
	"time"

	accessnode "github.com/WuKongIM/WuKongIM/internal/access/node"
	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	runtimebackup "github.com/WuKongIM/WuKongIM/internal/runtime/backup"
	backupusecase "github.com/WuKongIM/WuKongIM/internal/usecase/backup"
	"github.com/stretchr/testify/require"
)

func TestBackupManagerStatusExposesInitializationFailureCategory(t *testing.T) {
	facade := backupManagerFacade{app: &App{
		cfg:           Config{Backup: BackupConfig{Enabled: true}},
		backupInitErr: errors.New("repository credential rejected"),
	}}
	status, err := facade.Status(context.Background())
	require.NoError(t, err)
	require.Equal(t, backupusecase.HealthFailed, status.Health)
	require.Equal(t, "initialization", status.FailureCategory)
}

func TestBackupInitializationFailureIsLogged(t *testing.T) {
	logger := &recordingAppLogger{}
	failure := errors.New("repository credential rejected")
	app := &App{logger: logger, backupInitErr: failure}
	app.logBackupInitializationFailure()

	entry := requireAppLogEvent(
		t, logger, "ERROR", "internal.app.backup_initialization",
	)
	requireAppLogField(t, entry, "result", "failed")
	requireAppLogField(t, entry, "error", failure)
}

func TestBackupManagerStatusKeepsUnknownEvidenceUnknown(t *testing.T) {
	facade := newBackupStatusTestFacade(
		t,
		runtimebackup.CoordinatorStatus{
			DoctorHealth: backupusecase.HealthUnknown,
		},
	)
	status, err := facade.Status(context.Background())
	require.NoError(t, err)
	require.Equal(t, backupusecase.HealthUnknown, status.Health)
	require.Nil(t, status.CheckpointAgeSeconds)
}

func TestBackupManagerStatusOverlaysContinuousRuntimeFailure(t *testing.T) {
	facade := newBackupStatusTestFacade(
		t,
		runtimebackup.CoordinatorStatus{
			Running: true, DoctorHealth: backupusecase.HealthFailed,
			LastFailureCategory: "doctor",
		},
	)
	status, err := facade.Status(context.Background())
	require.NoError(t, err)
	require.Equal(t, backupusecase.HealthFailed, status.Health)
	require.Equal(t, "doctor", status.FailureCategory)
	require.True(t, status.Running)
	require.Equal(t, int64(60), status.Policy.CheckpointIntervalSeconds)
}

func TestBackupManagerRouterKeepsContactedNodeCaptureStatus(t *testing.T) {
	localCapture := backupcontract.SlotCaptureStatus{
		HashSlot: 2, State: backupcontract.CaptureStateIdle,
		ObservedAtUnixMillis: 22,
	}
	local := newBackupStatusTestFacade(
		t,
		runtimebackup.CoordinatorStatus{
			Running: true, DoctorHealth: backupusecase.HealthHealthy,
		},
	)
	local.app.backupRuntime = &backupStatusRuntime{
		status: runtimebackup.CoordinatorStatus{
			Running: true, DoctorHealth: backupusecase.HealthHealthy,
		},
		captures: []backupcontract.SlotCaptureStatus{localCapture},
	}

	remoteCapture := backupcontract.SlotCaptureStatus{
		HashSlot: 1, State: backupcontract.CaptureStateReconciling,
		ObservedAtUnixMillis: 11,
	}
	remoteStatus := backupusecase.StatusSnapshot{
		Enabled: true, Health: backupusecase.HealthHealthy,
		CoordinatorNodeID: 1,
		CaptureLeases: []backupusecase.CaptureLeaseSnapshot{{
			HashSlot: 2, HolderNodeID: 2, FrontierRevision: 7,
		}},
		LocalCaptureStatuses: []backupcontract.SlotCaptureStatus{
			remoteCapture,
		},
	}
	remote := &backupStatusManagementStub{status: remoteStatus}
	remoteLeadership := &backupStatusRouteNode{local: 1, leader: 1}
	remoteAdapter := accessnode.NewManagerBackupAdapter(
		accessnode.ManagerBackupOptions{
			Local: remote, Leadership: remoteLeadership,
		},
	)
	node := &backupStatusRouteNode{
		local: 2, leader: 1, handler: remoteAdapter.HandleRPC,
	}
	router := backupManagerRouter{
		local: local, leadership: node, client: accessnode.NewClient(node),
	}

	status, err := router.Status(context.Background())
	require.NoError(t, err)
	require.Equal(t, uint64(1), status.CoordinatorNodeID)
	require.Equal(t, remoteStatus.CaptureLeases, status.CaptureLeases)
	require.Equal(
		t, []backupcontract.SlotCaptureStatus{localCapture},
		status.LocalCaptureStatuses,
	)
}

func newBackupStatusTestFacade(
	t *testing.T,
	operational runtimebackup.CoordinatorStatus,
) backupManagerFacade {
	t.Helper()
	backupApp, err := backupusecase.NewApp(backupusecase.Options{
		Enabled: true, HashSlotCount: 1,
		Store: &backupStatusStateStore{},
		Now:   time.Now, MaxCheckpointAge: time.Minute,
	})
	require.NoError(t, err)
	return backupManagerFacade{app: &App{
		cfg: Config{Backup: BackupConfig{
			Enabled: true, CheckpointInterval: time.Minute,
			CaptureReconcileInterval: time.Second,
		}},
		backup:        backupApp,
		backupRuntime: &backupStatusRuntime{status: operational},
	}}
}

type backupStatusStateStore struct {
	state backupusecase.State
}

func (s *backupStatusStateStore) Load(context.Context) (backupusecase.State, error) {
	return s.state.Clone(), nil
}

func (s *backupStatusStateStore) CompareAndSwap(
	context.Context,
	uint64,
	backupusecase.State,
) error {
	return nil
}

type backupStatusRuntime struct {
	status   runtimebackup.CoordinatorStatus
	captures []backupcontract.SlotCaptureStatus
}

func (*backupStatusRuntime) Start(context.Context) error { return nil }
func (*backupStatusRuntime) Stop(context.Context) error  { return nil }
func (r *backupStatusRuntime) Status() runtimebackup.CoordinatorStatus {
	return r.status
}

func (r *backupStatusRuntime) CaptureStatus() []backupcontract.SlotCaptureStatus {
	return r.captures
}

type backupStatusRouteNode struct {
	local   uint64
	leader  uint64
	handler func(context.Context, []byte) ([]byte, error)
}

func (n *backupStatusRouteNode) NodeID() uint64 {
	return n.local
}

func (n *backupStatusRouteNode) BackupControllerLeaderID() uint64 {
	return n.leader
}

func (n *backupStatusRouteNode) CallRPC(
	ctx context.Context,
	_ uint64,
	_ uint8,
	payload []byte,
) ([]byte, error) {
	return n.handler(ctx, payload)
}

type backupStatusManagementStub struct {
	status backupusecase.StatusSnapshot
}

func (s *backupStatusManagementStub) Status(
	context.Context,
) (backupusecase.StatusSnapshot, error) {
	return s.status, nil
}

func (*backupStatusManagementStub) PublishCheckpoint(
	context.Context,
) (backupusecase.CheckpointPublication, error) {
	return backupusecase.CheckpointPublication{}, nil
}

func (*backupStatusManagementStub) SetCheckpointHold(
	context.Context,
	string,
	bool,
) (backupusecase.CheckpointSummary, error) {
	return backupusecase.CheckpointSummary{}, nil
}

func (*backupStatusManagementStub) FenceSource(
	context.Context,
	backupusecase.SourceFenceRequest,
) (backupusecase.SourceFenceReceipt, error) {
	return backupusecase.SourceFenceReceipt{}, nil
}
