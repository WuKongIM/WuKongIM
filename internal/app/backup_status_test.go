package app

import (
	"context"
	"errors"
	"sync/atomic"
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

func TestBackupManagerStatusTreatsInactiveCaptureAsComplete(t *testing.T) {
	for _, facade := range []backupManagerFacade{
		{},
		{app: &App{cfg: Config{Backup: BackupConfig{
			RestoreMode: true,
		}}}},
	} {
		status, err := facade.Status(context.Background())
		require.NoError(t, err)
		require.False(t, status.Enabled)
		require.Equal(t, backupusecase.HealthDisabled, status.Health)
		require.True(t, status.CaptureStatusComplete)
	}
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

func TestActiveRestoreThroughputClearsTerminalPlans(t *testing.T) {
	require.Equal(t, uint64(4096), activeRestoreThroughput(
		&backupusecase.RestoreProgress{
			Status:                   backupcontract.RestoreStatusInstalling,
			ThroughputBytesPerSecond: 4096,
		},
	))
	require.Zero(t, activeRestoreThroughput(
		&backupusecase.RestoreProgress{
			Status:                   backupcontract.RestoreStatusVerified,
			ThroughputBytesPerSecond: 4096,
		},
	))
	require.Zero(t, activeRestoreThroughput(nil))
}

func TestBackupManagerRouterAggregatesCaptureStatusFromEveryLeaseHolder(
	t *testing.T,
) {
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
		CaptureLeases: []backupusecase.CaptureLeaseSnapshot{
			{HashSlot: 1, HolderNodeID: 1, FrontierRevision: 6},
			{HashSlot: 2, HolderNodeID: 2, FrontierRevision: 7},
			{HashSlot: 3, HolderNodeID: 3, FrontierRevision: 8},
		},
		CaptureStatuses: []backupcontract.SlotCaptureStatus{
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
	thirdCapture := backupcontract.SlotCaptureStatus{
		HashSlot: 3, State: backupcontract.CaptureStateCapturing,
		ObservedAtUnixMillis: 33,
	}
	third := &backupStatusManagementStub{status: backupusecase.StatusSnapshot{
		CaptureStatuses: []backupcontract.SlotCaptureStatus{thirdCapture},
	}}
	thirdLeadership := &backupStatusRouteNode{local: 3, leader: 1}
	thirdAdapter := accessnode.NewManagerBackupAdapter(
		accessnode.ManagerBackupOptions{
			Local: third, Leadership: thirdLeadership,
		},
	)
	node := &backupStatusRouteNode{
		local: 2, leader: 1,
		handlers: map[uint64]func(context.Context, []byte) ([]byte, error){
			1: remoteAdapter.HandleRPC,
			3: thirdAdapter.HandleRPC,
		},
	}
	router := backupManagerRouter{
		local: local, leadership: node, client: accessnode.NewClient(node),
	}

	status, err := router.Status(context.Background())
	require.NoError(t, err)
	require.Equal(t, uint64(1), status.CoordinatorNodeID)
	require.Equal(t, remoteStatus.CaptureLeases, status.CaptureLeases)
	require.Equal(
		t,
		[]backupcontract.SlotCaptureStatus{
			remoteCapture, localCapture, thirdCapture,
		},
		status.CaptureStatuses,
	)
	require.True(t, status.CaptureStatusComplete)
	require.Empty(t, status.CaptureStatusMissingNodeIDs)
	require.Empty(t, status.CaptureStatusMissingSlots)

	node.handlers[3] = func(
		context.Context,
		[]byte,
	) ([]byte, error) {
		return nil, errors.New("node unavailable")
	}
	status, err = router.Status(context.Background())
	require.NoError(t, err)
	require.False(t, status.CaptureStatusComplete)
	require.Equal(t, []uint64{3}, status.CaptureStatusMissingNodeIDs)
	require.Equal(t, []uint16{3}, status.CaptureStatusMissingSlots)
	require.Equal(t, backupusecase.HealthDegraded, status.Health)
	require.Equal(
		t, "capture_status_unavailable", status.FailureCategory,
	)
}

func TestBackupManagerRouterBoundsCaptureStatusFanoutWithoutStarvingPeers(
	t *testing.T,
) {
	leases := []backupusecase.CaptureLeaseSnapshot{
		{HashSlot: 0, HolderNodeID: 1},
		{HashSlot: 1, HolderNodeID: 2},
	}
	handlers := make(
		map[uint64]func(context.Context, []byte) ([]byte, error),
	)
	var active atomic.Int64
	var maximum atomic.Int64
	for hashSlot := uint16(2); hashSlot < 12; hashSlot++ {
		nodeID := uint64(hashSlot + 1)
		leases = append(leases, backupusecase.CaptureLeaseSnapshot{
			HashSlot: hashSlot, HolderNodeID: nodeID,
		})
		stub := &backupStatusManagementStub{
			status: backupusecase.StatusSnapshot{
				CaptureStatuses: []backupcontract.SlotCaptureStatus{{
					HashSlot: hashSlot,
					State:    backupcontract.CaptureStateIdle,
				}},
			},
		}
		adapter := accessnode.NewManagerBackupAdapter(
			accessnode.ManagerBackupOptions{
				Local: stub,
				Leadership: &backupStatusRouteNode{
					local: nodeID, leader: 1,
				},
			},
		)
		handlers[nodeID] = func(
			ctx context.Context,
			payload []byte,
		) ([]byte, error) {
			current := active.Add(1)
			defer active.Add(-1)
			for {
				observed := maximum.Load()
				if current <= observed ||
					maximum.CompareAndSwap(observed, current) {
					break
				}
			}
			if nodeID == 3 {
				<-ctx.Done()
				return nil, ctx.Err()
			}
			time.Sleep(25 * time.Millisecond)
			return adapter.HandleRPC(ctx, payload)
		}
	}
	node := &backupStatusRouteNode{
		local: 2, leader: 1, handlers: handlers,
	}
	router := backupManagerRouter{
		local:      backupManagerFacade{},
		leadership: node,
		client:     accessnode.NewClient(node),
	}
	captures, missingNodes, missingSlots := router.clusterCaptureStatuses(
		context.Background(),
		1,
		leases,
		[]backupcontract.SlotCaptureStatus{{
			HashSlot: 0, State: backupcontract.CaptureStateIdle,
		}},
		[]backupcontract.SlotCaptureStatus{{
			HashSlot: 1, State: backupcontract.CaptureStateIdle,
		}},
	)

	require.Len(t, captures, 11)
	require.Equal(t, []uint64{3}, missingNodes)
	require.Equal(t, []uint16{2}, missingSlots)
	require.Greater(t, maximum.Load(), int64(1))
	require.LessOrEqual(
		t, maximum.Load(), int64(backupStatusFanoutConcurrency),
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
	local    uint64
	leader   uint64
	handler  func(context.Context, []byte) ([]byte, error)
	handlers map[uint64]func(context.Context, []byte) ([]byte, error)
}

func (n *backupStatusRouteNode) NodeID() uint64 {
	return n.local
}

func (n *backupStatusRouteNode) BackupControllerLeaderID() uint64 {
	return n.leader
}

func (n *backupStatusRouteNode) CallRPC(
	ctx context.Context,
	nodeID uint64,
	_ uint8,
	payload []byte,
) ([]byte, error) {
	if handler := n.handlers[nodeID]; handler != nil {
		return handler(ctx, payload)
	}
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

func (s *backupStatusManagementStub) LocalCaptureStatus(
	context.Context,
) []backupcontract.SlotCaptureStatus {
	return s.status.CaptureStatuses
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
