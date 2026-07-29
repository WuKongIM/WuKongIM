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

func TestManagementServiceSavesFilePlanBeforeRepositoryTest(t *testing.T) {
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
				ExpectedRevision: 0, Enabled: false,
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
	if result.InitialJob != nil || result.Plan.RepositoryVerification == nil ||
		result.Plan.RepositoryVerification.Status !=
			backupcontract.RepositoryVerificationUnverified {
		t.Fatalf("result = %#v", result)
	}
	if _, err := os.Stat(filepath.Join(
		dataDir, "backup-repository", "repository.json",
	)); !os.IsNotExist(err) {
		t.Fatalf("repository marker exists before test: %v", err)
	}

	if _, err := management.TestRepository(
		context.Background(),
		backupusecase.TestRepositoryRequest{ExpectedPlanRevision: 2},
	); !errors.Is(err, backupusecase.ErrStateConflict) {
		t.Fatalf("TestRepository(stale) error = %v", err)
	}
	verified, err := management.TestRepository(
		context.Background(),
		backupusecase.TestRepositoryRequest{ExpectedPlanRevision: 1},
	)
	if err != nil {
		t.Fatalf("TestRepository(): %v", err)
	}
	if verified.RepositoryVerification == nil ||
		verified.RepositoryVerification.Status !=
			backupcontract.RepositoryVerificationVerified {
		t.Fatalf("verified plan = %#v", verified)
	}
	if _, err := os.Stat(filepath.Join(
		dataDir, "backup-repository", "repository.json",
	)); err != nil {
		t.Fatalf("repository marker after test: %v", err)
	}

	enabled, err := management.Configure(
		context.Background(),
		backupusecase.ConfigureManagementRequest{
			ConfigureRequest: backupusecase.ConfigureRequest{
				ExpectedRevision: 1, Enabled: true,
				Store: backupcontract.StoreConfig{Kind: backupcontract.StoreKindFile},
				Cron:  "0 1 * * *", TimeZone: "Asia/Shanghai",
				RetentionCount: 7, RateBytesPerSec: 50 << 20,
				WorkersPerNode: 1, MaxDuration: 12 * time.Hour,
			},
		},
	)
	if err != nil {
		t.Fatalf("Configure(enabled): %v", err)
	}
	if enabled.InitialJob == nil ||
		enabled.InitialJob.ID != "backup-initial" ||
		enabled.Plan.RepositoryVerification == nil ||
		enabled.Plan.RepositoryVerification.Status !=
			backupcontract.RepositoryVerificationVerified {
		t.Fatalf("enabled result = %#v", enabled)
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

func TestManagementServicePreservesCloudDefaultEndpointSelection(t *testing.T) {
	testCases := []struct {
		name   string
		kind   backupcontract.StoreKind
		region string
		bucket string
	}{
		{
			name: "Alibaba OSS", kind: backupcontract.StoreKindOSS,
			region: "cn-hangzhou", bucket: "wukongim-backups",
		},
		{
			name: "Tencent COS", kind: backupcontract.StoreKindCOS,
			region: "ap-shanghai", bucket: "wukongim-backups-1250000000",
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			now := time.Date(2026, 7, 29, 1, 0, 0, 0, time.UTC)
			stateStore := &memoryScheduledStateStore{}
			scheduled, err := backupusecase.NewScheduledService(
				backupusecase.ScheduledOptions{
					StateStore: stateStore,
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
			sealer, err := backupinfra.NewRepositoryProvider(t.TempDir(), cipher)
			if err != nil {
				t.Fatalf("NewRepositoryProvider(): %v", err)
			}
			fileStore, err := backupinfra.NewFileArchiveStore(t.TempDir())
			if err != nil {
				t.Fatalf("NewFileArchiveStore(): %v", err)
			}
			repository := &recordingArchiveRepository{store: fileStore}
			probe := &recordingRepositoryProbe{}
			management, err := backupusecase.NewManagementService(
				backupusecase.ManagementOptions{
					Scheduled: scheduled, Repository: repository, Sealer: sealer,
					Probe:     probe,
					ClusterID: "cluster-a", Now: func() time.Time { return now },
				},
			)
			if err != nil {
				t.Fatalf("NewManagementService(): %v", err)
			}
			request := validConfigureRequest()
			request.Enabled = false
			request.Store = backupcontract.StoreConfig{
				Kind: testCase.kind, Region: testCase.region,
				Bucket: testCase.bucket, Prefix: "cluster-a",
			}

			result, err := management.Configure(
				context.Background(),
				backupusecase.ConfigureManagementRequest{
					ConfigureRequest: request,
					AccessKey:        "access-key-id",
					SecretKey:        "access-key-secret",
				},
			)
			if err != nil {
				t.Fatalf("Configure(): %v", err)
			}
			if repository.opens != 0 || probe.calls != 0 {
				t.Fatalf(
					"save performed repository I/O: opens=%d probes=%d",
					repository.opens, probe.calls,
				)
			}
			if result.Plan.Store.Endpoint != "" ||
				len(result.Plan.Store.CredentialCiphertext) == 0 ||
				result.Plan.RepositoryVerification == nil ||
				result.Plan.RepositoryVerification.Status !=
					backupcontract.RepositoryVerificationUnverified {
				t.Fatalf("plan store = %#v", result.Plan.Store)
			}
			saved, err := scheduled.State(context.Background())
			if err != nil {
				t.Fatalf("State(): %v", err)
			}
			if saved.Plan == nil ||
				saved.Plan.Store.Region != testCase.region ||
				saved.Plan.Store.Bucket != testCase.bucket ||
				saved.Plan.Store.Prefix != "cluster-a" {
				t.Fatalf("saved state = %#v", saved)
			}
		})
	}
}

func TestManagementServicePreservesVerificationOnlyForSameRepository(t *testing.T) {
	now := time.Date(2026, 7, 29, 1, 0, 0, 0, time.UTC)
	stateStore := &memoryScheduledStateStore{}
	scheduled, err := backupusecase.NewScheduledService(
		backupusecase.ScheduledOptions{
			StateStore: stateStore,
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
	sealer, err := backupinfra.NewRepositoryProvider(t.TempDir(), cipher)
	if err != nil {
		t.Fatalf("NewRepositoryProvider(): %v", err)
	}
	fileStore, err := backupinfra.NewFileArchiveStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileArchiveStore(): %v", err)
	}
	repository := &recordingArchiveRepository{store: fileStore}
	probe := &recordingRepositoryProbe{}
	management, err := backupusecase.NewManagementService(
		backupusecase.ManagementOptions{
			Scheduled: scheduled, Repository: repository, Sealer: sealer,
			Probe: probe, ClusterID: "cluster-a",
			Now: func() time.Time { return now },
		},
	)
	if err != nil {
		t.Fatalf("NewManagementService(): %v", err)
	}
	request := validConfigureRequest()
	request.Enabled = false
	request.Store = backupcontract.StoreConfig{
		Kind: backupcontract.StoreKindOSS, Region: "cn-hangzhou",
		Bucket: "wukongim-backups", Prefix: "cluster-a",
	}
	first, err := management.Configure(
		context.Background(),
		backupusecase.ConfigureManagementRequest{
			ConfigureRequest: request,
			AccessKey:        "access-key-id",
			SecretKey:        "access-key-secret",
		},
	)
	if err != nil {
		t.Fatalf("Configure(first): %v", err)
	}
	if first.Plan.Store.CredentialRevision != 1 {
		t.Fatalf(
			"first credential revision = %d",
			first.Plan.Store.CredentialRevision,
		)
	}
	if _, err := scheduled.MarkRepositoryVerified(
		context.Background(), first.Plan.Revision,
	); err != nil {
		t.Fatalf("MarkRepositoryVerified(): %v", err)
	}

	request.ExpectedRevision = first.Plan.Revision
	request.Cron = "0 13 * * *"
	scheduleOnly, err := management.Configure(
		context.Background(),
		backupusecase.ConfigureManagementRequest{
			ConfigureRequest: request,
		},
	)
	if err != nil {
		t.Fatalf("Configure(schedule only): %v", err)
	}
	if scheduleOnly.Plan.RepositoryVerification == nil ||
		scheduleOnly.Plan.RepositoryVerification.Status !=
			backupcontract.RepositoryVerificationVerified ||
		scheduleOnly.Plan.Store.CredentialRevision != 1 {
		t.Fatalf("schedule-only plan = %#v", scheduleOnly.Plan)
	}

	request.ExpectedRevision = scheduleOnly.Plan.Revision
	request.Store.Region = "cn-shanghai"
	changedStore, err := management.Configure(
		context.Background(),
		backupusecase.ConfigureManagementRequest{
			ConfigureRequest: request,
		},
	)
	if err != nil {
		t.Fatalf("Configure(changed store): %v", err)
	}
	if changedStore.Plan.RepositoryVerification == nil ||
		changedStore.Plan.RepositoryVerification.Status !=
			backupcontract.RepositoryVerificationUnverified ||
		changedStore.Plan.Store.CredentialRevision != 1 {
		t.Fatalf("changed-store plan = %#v", changedStore.Plan)
	}

	request.ExpectedRevision = changedStore.Plan.Revision
	replacement, err := management.Configure(
		context.Background(),
		backupusecase.ConfigureManagementRequest{
			ConfigureRequest: request,
			AccessKey:        "replacement-access-key-id",
			SecretKey:        "replacement-access-key-secret",
		},
	)
	if err != nil {
		t.Fatalf("Configure(replacement credential): %v", err)
	}
	if replacement.Plan.RepositoryVerification == nil ||
		replacement.Plan.RepositoryVerification.Status !=
			backupcontract.RepositoryVerificationUnverified ||
		replacement.Plan.Store.CredentialRevision != 2 {
		t.Fatalf("replacement plan = %#v", replacement.Plan)
	}
	if repository.opens != 0 || probe.calls != 0 {
		t.Fatalf(
			"save performed repository I/O: opens=%d probes=%d",
			repository.opens, probe.calls,
		)
	}
}

func TestManagementServiceRejectsCallerSuppliedCredentialCiphertext(
	t *testing.T,
) {
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
	sealer, err := backupinfra.NewRepositoryProvider(t.TempDir(), cipher)
	if err != nil {
		t.Fatalf("NewRepositoryProvider(): %v", err)
	}
	fileStore, err := backupinfra.NewFileArchiveStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileArchiveStore(): %v", err)
	}
	repository := &recordingArchiveRepository{store: fileStore}
	management, err := backupusecase.NewManagementService(
		backupusecase.ManagementOptions{
			Scheduled: scheduled, Repository: repository, Sealer: sealer,
			Probe: backupusecase.DirectRepositoryProbe{
				NewID: func() string { return "probe-untrusted" },
			},
			ClusterID: "cluster-a", Now: func() time.Time { return now },
		},
	)
	if err != nil {
		t.Fatalf("NewManagementService(): %v", err)
	}
	request := validConfigureRequest()
	request.Store = backupcontract.StoreConfig{
		Kind: backupcontract.StoreKindOSS, Region: "cn-hangzhou",
		Endpoint: "https://oss-cn-hangzhou.aliyuncs.com",
		Bucket:   "wukongim-backups", Prefix: "cluster-a",
		CredentialCiphertext: []byte("caller-controlled"),
		CredentialRevision:   99,
	}

	_, err = management.Configure(
		context.Background(),
		backupusecase.ConfigureManagementRequest{
			ConfigureRequest: request,
		},
	)
	if !errors.Is(err, backupusecase.ErrInvalidRequest) {
		t.Fatalf("Configure() error = %v", err)
	}
	if repository.config.Kind != "" {
		t.Fatalf("repository opened with %#v", repository.config)
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
	request := validConfigureRequest()
	request.Enabled = false
	if _, err := management.Configure(
		context.Background(),
		backupusecase.ConfigureManagementRequest{
			ConfigureRequest: request,
		},
	); err != nil {
		t.Fatalf("Configure(): %v", err)
	}

	_, err = management.TestRepository(
		context.Background(),
		backupusecase.TestRepositoryRequest{ExpectedPlanRevision: 1},
	)
	if !errors.Is(err, backupusecase.ErrStoreUnreachable) {
		t.Fatalf("TestRepository() error = %v", err)
	}
}

func TestManagementDashboardReportsBackupHealth(t *testing.T) {
	testCases := []struct {
		name       string
		createdAt  time.Time
		history    []backupcontract.TaskRecord
		wantHealth backupusecase.BackupHealth
		wantReason string
	}{
		{
			name:      "healthy after latest expected backup",
			createdAt: time.Date(2026, 7, 27, 17, 0, 0, 0, time.UTC),
			history: []backupcontract.TaskRecord{{
				ID: "backup-success", Kind: "backup",
				Status: string(backupcontract.JobStatusSucceeded),
				CompletedUnixMillis: time.Date(
					2026, 7, 28, 17, 10, 0, 0, time.UTC,
				).UnixMilli(),
			}},
			wantHealth: backupusecase.BackupHealthHealthy,
		},
		{
			name:      "warning immediately after failure",
			createdAt: time.Date(2026, 7, 27, 17, 0, 0, 0, time.UTC),
			history: []backupcontract.TaskRecord{{
				ID: "backup-failed", Kind: "backup",
				Status: string(backupcontract.JobStatusFailed),
				CompletedUnixMillis: time.Date(
					2026, 7, 28, 18, 0, 0, 0, time.UTC,
				).UnixMilli(),
			}},
			wantHealth: backupusecase.BackupHealthWarning,
			wantReason: "latest_backup_failed",
		},
		{
			name:       "critical after two expected runs without success",
			createdAt:  time.Date(2026, 7, 26, 16, 0, 0, 0, time.UTC),
			wantHealth: backupusecase.BackupHealthCritical,
			wantReason: "successful_backup_stale",
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			management, _, stateStore, _ := newArchiveManagement(t)
			stateStore.mu.Lock()
			stateStore.state.Plan.Enabled = true
			stateStore.state.Plan.CreatedUnixMillis =
				testCase.createdAt.UnixMilli()
			stateStore.state.History = append(
				[]backupcontract.TaskRecord(nil), testCase.history...,
			)
			stateStore.mu.Unlock()

			dashboard, err := management.Dashboard(context.Background())
			if err != nil {
				t.Fatalf("Dashboard(): %v", err)
			}
			if dashboard.BackupHealth != testCase.wantHealth ||
				dashboard.BackupHealthReason != testCase.wantReason {
				t.Fatalf(
					"health = %q, reason = %q, want %q, %q",
					dashboard.BackupHealth, dashboard.BackupHealthReason,
					testCase.wantHealth, testCase.wantReason,
				)
			}
		})
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
	management, scheduled, _, store := newArchiveManagement(t)
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
	state, err := scheduled.State(context.Background())
	if err != nil {
		t.Fatalf("State(): %v", err)
	}
	if len(state.History) != 1 ||
		state.History[0].Kind != "verification" ||
		state.History[0].Status != string(backupcontract.JobStatusFailed) ||
		state.History[0].ErrorCode != "archive_corrupt" {
		t.Fatalf("history = %#v", state.History)
	}
}

type failingRepositoryProbe struct {
	err error
}

type recordingArchiveRepository struct {
	store  backupartifact.ArchiveStore
	config backupcontract.StoreConfig
	opens  int
}

func (r *recordingArchiveRepository) Open(
	_ context.Context,
	config backupcontract.StoreConfig,
) (backupartifact.ArchiveStore, error) {
	r.opens++
	r.config = config
	return r.store, nil
}

type recordingRepositoryProbe struct {
	calls int
	err   error
}

func (p *recordingRepositoryProbe) ProbeRepository(
	context.Context,
	backupcontract.StoreConfig,
	backupartifact.ArchiveStore,
) error {
	p.calls++
	return p.err
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
