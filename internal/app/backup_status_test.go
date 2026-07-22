package app

import (
	"context"
	"testing"
	"time"

	runtimebackup "github.com/WuKongIM/WuKongIM/internal/runtime/backup"
	backupusecase "github.com/WuKongIM/WuKongIM/internal/usecase/backup"
)

func TestBackupManagerStatusKeepsUnknownDoctorEvidenceUnknown(t *testing.T) {
	now := time.Unix(1_753_000_000, 0).UTC()
	facade := newBackupStatusTestFacade(t, now, runtimebackup.CoordinatorStatus{DoctorHealth: backupusecase.HealthUnknown})

	status, err := facade.Status(context.Background())
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if status.Health != backupusecase.HealthUnknown {
		t.Fatalf("Status() health = %q, want %q", status.Health, backupusecase.HealthUnknown)
	}
}

func TestBackupManagerStatusKeepsMissingAuditEvidenceUnknown(t *testing.T) {
	now := time.Unix(1_753_000_000, 0).UTC()
	facade := newBackupStatusTestFacade(t, now, runtimebackup.CoordinatorStatus{DoctorHealth: backupusecase.HealthHealthy})

	status, err := facade.Status(context.Background())
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if status.Health != backupusecase.HealthUnknown {
		t.Fatalf("Status() health = %q, want %q", status.Health, backupusecase.HealthUnknown)
	}
}

func newBackupStatusTestFacade(t *testing.T, now time.Time, operational runtimebackup.CoordinatorStatus) backupManagerFacade {
	t.Helper()
	backupApp, err := backupusecase.NewApp(backupusecase.Options{
		Enabled: true, HashSlotCount: 1,
		Store: &backupStatusStateStore{
			state: backupusecase.State{
				RestorePoints: []backupusecase.RestorePoint{{
					ID: "restore-current", CreatedAtUnixMillis: now.Add(-time.Minute).UnixMilli(), EffectiveAtUnixMillis: now.Add(-time.Minute).UnixMilli(),
				}},
			},
		},
		Publisher: backupStatusPublisher{}, Now: func() time.Time { return now }, NewJobID: func() string { return "job" },
	})
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	return backupManagerFacade{app: &App{
		cfg: Config{Backup: BackupConfig{Enabled: true}}, backup: backupApp,
		backupRuntime: &backupStatusRuntime{status: operational},
	}}
}

type backupStatusStateStore struct {
	state backupusecase.State
}

func (s *backupStatusStateStore) Load(context.Context) (backupusecase.State, error) {
	return s.state.Clone(), nil
}

func (s *backupStatusStateStore) CompareAndSwap(context.Context, uint64, backupusecase.State) error {
	return nil
}

type backupStatusPublisher struct{}

func (backupStatusPublisher) Publish(context.Context, backupusecase.Job) (backupusecase.RestorePoint, error) {
	return backupusecase.RestorePoint{}, nil
}

type backupStatusRuntime struct {
	status runtimebackup.CoordinatorStatus
}

func (*backupStatusRuntime) Start(context.Context) error { return nil }
func (*backupStatusRuntime) Stop(context.Context) error  { return nil }
func (r *backupStatusRuntime) Status() runtimebackup.CoordinatorStatus {
	return r.status
}
