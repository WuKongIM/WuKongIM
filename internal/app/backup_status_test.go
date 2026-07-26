package app

import (
	"context"
	"testing"
	"time"

	runtimebackup "github.com/WuKongIM/WuKongIM/internal/runtime/backup"
	backupusecase "github.com/WuKongIM/WuKongIM/internal/usecase/backup"
	"github.com/stretchr/testify/require"
)

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
	status runtimebackup.CoordinatorStatus
}

func (*backupStatusRuntime) Start(context.Context) error { return nil }
func (*backupStatusRuntime) Stop(context.Context) error  { return nil }
func (r *backupStatusRuntime) Status() runtimebackup.CoordinatorStatus {
	return r.status
}
