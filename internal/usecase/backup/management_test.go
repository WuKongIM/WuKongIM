package backup_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	backupinfra "github.com/WuKongIM/WuKongIM/internal/infra/backup"
	backupusecase "github.com/WuKongIM/WuKongIM/internal/usecase/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
)

func TestManagementServiceEnablesFilePlanAfterRepositoryProbe(t *testing.T) {
	now := time.Date(2026, 7, 29, 1, 0, 0, 0, time.UTC)
	stateStore := &memoryScheduledStateStore{}
	scheduled, err := backupusecase.NewScheduledService(backupusecase.ScheduledOptions{
		StateStore: stateStore, Now: func() time.Time { return now },
		NewID: func() string { return "backup-initial" },
	})
	if err != nil {
		t.Fatalf("NewScheduledService(): %v", err)
	}
	cipher, err := backupinfra.NewCredentialCipher(
		"manager-installation-secret", "cluster-a",
	)
	if err != nil {
		t.Fatalf("NewCredentialCipher(): %v", err)
	}
	dataDir := t.TempDir()
	provider, err := backupinfra.NewRepositoryProvider(dataDir, cipher)
	if err != nil {
		t.Fatalf("NewRepositoryProvider(): %v", err)
	}
	management, err := backupusecase.NewManagementService(
		backupusecase.ManagementOptions{
			Scheduled: scheduled, Repository: provider, Sealer: provider,
			Probe: backupusecase.DirectRepositoryProbe{
				NewID: func() string { return "probe-1" },
			},
			ClusterID: "cluster-a", Now: func() time.Time { return now },
		},
	)
	if err != nil {
		t.Fatalf("NewManagementService(): %v", err)
	}

	result, err := management.Configure(
		context.Background(),
		backupusecase.ConfigureManagementRequest{
			ConfigureRequest: backupusecase.ConfigureRequest{
				ExpectedRevision: 0, Enabled: true,
				Store: backupcontract.StoreConfig{Kind: backupcontract.StoreKindFile},
				Cron:  "0 1 * * *", TimeZone: "Asia/Shanghai",
				RetentionCount: 7, RateBytesPerSec: 50 << 20,
				WorkersPerNode: 1, MaxDuration: 12 * time.Hour,
			},
		},
	)
	if err != nil {
		t.Fatalf("Configure(): %v", err)
	}
	if result.InitialJob == nil || result.InitialJob.ID != "backup-initial" {
		t.Fatalf("result = %#v", result)
	}
	if _, err := os.Stat(filepath.Join(
		dataDir, "backup-repository", "repository.json",
	)); err != nil {
		t.Fatalf("repository marker: %v", err)
	}
	dashboard, err := management.Dashboard(context.Background())
	if err != nil {
		t.Fatalf("Dashboard(): %v", err)
	}
	if dashboard.State.Plan == nil || !dashboard.State.Plan.Enabled ||
		len(dashboard.Archives) != 0 ||
		dashboard.NextScheduledUnixMS !=
			time.Date(2026, 7, 29, 17, 0, 0, 0, time.UTC).UnixMilli() {
		t.Fatalf("dashboard = %#v", dashboard)
	}
}

func TestManagementRepositoryProbeClassifiesStoreAccessFailure(t *testing.T) {
	now := time.Date(2026, 7, 29, 1, 0, 0, 0, time.UTC)
	scheduled, err := backupusecase.NewScheduledService(
		backupusecase.ScheduledOptions{
			StateStore: &memoryScheduledStateStore{},
			Now:        func() time.Time { return now },
			NewID:      func() string { return "unused" },
		},
	)
	if err != nil {
		t.Fatalf("NewScheduledService(): %v", err)
	}
	cipher, err := backupinfra.NewCredentialCipher(
		"manager-installation-secret", "cluster-a",
	)
	if err != nil {
		t.Fatalf("NewCredentialCipher(): %v", err)
	}
	provider, err := backupinfra.NewRepositoryProvider(t.TempDir(), cipher)
	if err != nil {
		t.Fatalf("NewRepositoryProvider(): %v", err)
	}
	management, err := backupusecase.NewManagementService(
		backupusecase.ManagementOptions{
			Scheduled:  scheduled,
			Repository: provider,
			Sealer:     provider,
			Probe: failingRepositoryProbe{
				err: errors.New("storage free-space threshold reached"),
			},
			ClusterID: "cluster-a",
			Now:       func() time.Time { return now },
		},
	)
	if err != nil {
		t.Fatalf("NewManagementService(): %v", err)
	}

	err = management.TestRepository(
		context.Background(),
		backupusecase.ConfigureManagementRequest{
			ConfigureRequest: validConfigureRequest(),
		},
	)
	if !errors.Is(err, backupusecase.ErrStoreUnreachable) {
		t.Fatalf("TestRepository() error = %v", err)
	}
}

func TestManagementDeletePreservesLastHealthyArchiveAndActiveRestoreSource(t *testing.T) {
	management, scheduled, stateStore, store := newArchiveManagement(t)
	writeCatalogArchive(t, store, "backup-one", true, 1_800_000_001_000)
	if err := management.DeleteArchive(
		context.Background(), "backup-one",
	); !errors.Is(err, backupusecase.ErrLastUsableArchive) {
		t.Fatalf("DeleteArchive(last) error = %v", err)
	}
	writeCatalogArchive(t, store, "backup-two", true, 1_800_000_002_000)
	stateStore.mu.Lock()
	stateStore.state.ActiveRestore = &backupcontract.RestoreJob{
		ID: "restore-two", BackupID: "backup-two",
	}
	stateStore.mu.Unlock()
	if err := management.DeleteArchive(
		context.Background(), "backup-two",
	); !errors.Is(err, backupusecase.ErrArchiveInUse) {
		t.Fatalf("DeleteArchive(active restore) error = %v", err)
	}
	stateStore.mu.Lock()
	stateStore.state.ActiveRestore = nil
	stateStore.mu.Unlock()
	if err := management.DeleteArchive(
		context.Background(), "backup-two",
	); err != nil {
		t.Fatalf("DeleteArchive(second): %v", err)
	}
	state, err := scheduled.State(context.Background())
	if err != nil || state.Plan == nil {
		t.Fatalf("scheduled state = %#v, error=%v", state, err)
	}
}

func TestManagementVerifyMarksIntegrityFailureCorrupt(t *testing.T) {
	management, _, _, store := newArchiveManagement(t)
	writeCatalogArchive(t, store, "backup-corrupt", true, 1_800_000_001_000)
	if _, err := management.VerifyArchive(
		context.Background(), "backup-corrupt",
	); !errors.Is(err, backupusecase.ErrArchiveCorrupt) {
		t.Fatalf("VerifyArchive() error = %v", err)
	}
	detail, err := management.Archive(context.Background(), "backup-corrupt")
	if err != nil {
		t.Fatalf("Archive(): %v", err)
	}
	if detail.Archive.Health != backupusecase.ArchiveHealthCorrupt {
		t.Fatalf("archive = %#v", detail.Archive)
	}
}

type failingRepositoryProbe struct {
	err error
}

func (f failingRepositoryProbe) ProbeRepository(
	context.Context,
	backupcontract.StoreConfig,
	backupartifact.ArchiveStore,
) error {
	return f.err
}

func newArchiveManagement(
	t *testing.T,
) (
	*backupusecase.ManagementService,
	*backupusecase.ScheduledService,
	*memoryScheduledStateStore,
	backupartifact.ArchiveStore,
) {
	t.Helper()
	now := time.Date(2026, 7, 29, 1, 0, 0, 0, time.UTC)
	stateStore := &memoryScheduledStateStore{}
	scheduled, err := backupusecase.NewScheduledService(backupusecase.ScheduledOptions{
		StateStore: stateStore, Now: func() time.Time { return now },
		NewID: func() string { return "unused" },
	})
	if err != nil {
		t.Fatalf("NewScheduledService(): %v", err)
	}
	request := validConfigureRequest()
	request.Enabled = false
	if _, err := scheduled.Configure(context.Background(), request); err != nil {
		t.Fatalf("Configure(): %v", err)
	}
	cipher, err := backupinfra.NewCredentialCipher(
		"manager-installation-secret", "cluster-a",
	)
	if err != nil {
		t.Fatalf("NewCredentialCipher(): %v", err)
	}
	provider, err := backupinfra.NewRepositoryProvider(t.TempDir(), cipher)
	if err != nil {
		t.Fatalf("NewRepositoryProvider(): %v", err)
	}
	store, err := provider.Open(
		context.Background(),
		backupcontract.StoreConfig{Kind: backupcontract.StoreKindFile},
	)
	if err != nil {
		t.Fatalf("Open(): %v", err)
	}
	management, err := backupusecase.NewManagementService(
		backupusecase.ManagementOptions{
			Scheduled: scheduled, Repository: provider, Sealer: provider,
			Probe: backupusecase.DirectRepositoryProbe{
				NewID: func() string { return "probe" },
			},
			ClusterID: "cluster-a", Now: func() time.Time { return now },
		},
	)
	if err != nil {
		t.Fatalf("NewManagementService(): %v", err)
	}
	return management, scheduled, stateStore, store
}
