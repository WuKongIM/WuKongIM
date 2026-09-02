package backup_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	backupinfra "github.com/WuKongIM/WuKongIM/internal/infra/backup"
	backupusecase "github.com/WuKongIM/WuKongIM/internal/usecase/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
)

func TestArchiveRetentionPreservesHeldAndCorruptRecoveryEvidence(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := backupinfra.NewFileArchiveStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileArchiveStore(): %v", err)
	}
	completedAt := int64(1_800_000_000_000)
	for offset, id := range []string{
		"backup-old", "backup-corrupt", "backup-held", "backup-second", "backup-newest",
	} {
		writeCatalogArchive(t, store, id, true, completedAt+int64(offset)*1_000)
	}
	if _, err := backupusecase.SetArchiveHold(
		ctx, store, "backup-held", true, "legal hold",
		time.UnixMilli(completedAt+10_000),
	); err != nil {
		t.Fatalf("SetArchiveHold(): %v", err)
	}
	if err := backupusecase.MarkArchiveCorrupt(
		ctx, store, "backup-corrupt", time.UnixMilli(completedAt+11_000),
	); err != nil {
		t.Fatalf("MarkArchiveCorrupt(): %v", err)
	}

	deleted, err := backupusecase.ApplyRetention(ctx, store, 2)
	if err != nil {
		t.Fatalf("ApplyRetention(): %v", err)
	}
	if !slices.Equal(deleted, []string{"backup-old"}) {
		t.Fatalf("deleted = %v, want [backup-old]", deleted)
	}
	deleted, err = backupusecase.ApplyRetention(ctx, store, 2)
	if err != nil {
		t.Fatalf("ApplyRetention(idempotent): %v", err)
	}
	if len(deleted) != 0 {
		t.Fatalf("idempotent deleted = %v", deleted)
	}

	archives, err := backupusecase.ListArchives(ctx, store)
	if err != nil {
		t.Fatalf("ListArchives(): %v", err)
	}
	byID := make(map[string]backupusecase.ArchiveSummary, len(archives))
	for _, archive := range archives {
		byID[archive.ID] = archive
	}
	if len(byID) != 4 || !byID["backup-held"].Held ||
		byID["backup-corrupt"].Health != backupusecase.ArchiveHealthCorrupt {
		t.Fatalf("retained archives = %#v", archives)
	}
	if _, exists := byID["backup-old"]; exists {
		t.Fatalf("old unheld archive survived retention: %#v", archives)
	}
	for _, retentionCount := range []int{0, 1001} {
		if _, err := backupusecase.ApplyRetention(
			ctx, store, retentionCount,
		); !errors.Is(err, backupusecase.ErrInvalidRequest) {
			t.Fatalf("ApplyRetention(%d) error = %v", retentionCount, err)
		}
	}
}

func TestArchiveCatalogRejectsUnsafeIdentifiersAndSurfacesBrokenPublication(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store, err := backupinfra.NewFileArchiveStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileArchiveStore(): %v", err)
	}
	putCatalogObject(t, store, "catalog/broken-publication", []byte("marker"))
	putCatalogObject(t, store, "catalog/nested/ignored", []byte("marker"))
	putCatalogObject(t, store, "outside/catalog", []byte("marker"))

	archives, err := backupusecase.ListArchives(ctx, store)
	if err != nil {
		t.Fatalf("ListArchives(): %v", err)
	}
	if len(archives) != 1 || archives[0].ID != "broken-publication" ||
		archives[0].Health != backupusecase.ArchiveHealthCorrupt ||
		archives[0].ErrorCode != "archive_metadata_corrupt" {
		t.Fatalf("broken publication = %#v", archives)
	}
	if _, err := backupusecase.ListArchives(
		ctx, nil,
	); !errors.Is(err, backupusecase.ErrInvalidRequest) {
		t.Fatalf("ListArchives(nil) error = %v", err)
	}
	if _, err := backupusecase.ArchiveByID(
		ctx, store, "nested/archive",
	); !errors.Is(err, backupusecase.ErrInvalidRequest) {
		t.Fatalf("ArchiveByID(path) error = %v", err)
	}
	if err := backupusecase.MarkArchiveCorrupt(
		ctx, store, "nested/archive", time.UnixMilli(1_800_000_000_000),
	); !errors.Is(err, backupusecase.ErrInvalidRequest) {
		t.Fatalf("MarkArchiveCorrupt(path) error = %v", err)
	}
	if _, err := backupusecase.SetArchiveHold(
		ctx, store, "broken-publication", true,
		strings.Repeat("n", 257), time.UnixMilli(1_800_000_000_000),
	); !errors.Is(err, backupusecase.ErrInvalidRequest) {
		t.Fatalf("SetArchiveHold(long note) error = %v", err)
	}
}

func TestManagementControlPlaneStartsCancelsAndHolds(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	management, scheduled, stateStore, store := newArchiveManagement(t)
	stateStore.mu.Lock()
	stateStore.state.Plan.Enabled = true
	stateStore.mu.Unlock()
	writeCatalogArchive(t, store, "backup-baseline", true, 1_800_000_001_000)

	job, err := management.StartBackup(ctx)
	if err != nil {
		t.Fatalf("StartBackup(): %v", err)
	}
	if job.Trigger != backupcontract.TriggerManual || job.ID != "unused" {
		t.Fatalf("manual job = %#v", job)
	}
	if err := management.CancelBackup(ctx, job.ID); err != nil {
		t.Fatalf("CancelBackup(): %v", err)
	}
	state, err := scheduled.State(ctx)
	if err != nil {
		t.Fatalf("State(): %v", err)
	}
	if state.ActiveBackup == nil || !state.ActiveBackup.CancelRequested {
		t.Fatalf("cancellation state = %#v", state.ActiveBackup)
	}

	held, err := management.HoldArchive(
		ctx, "backup-baseline", true, "release baseline",
	)
	if err != nil {
		t.Fatalf("HoldArchive(): %v", err)
	}
	if !held.Held || held.HoldNote != "release baseline" {
		t.Fatalf("held archive = %#v", held)
	}
	released, err := management.HoldArchive(ctx, "backup-baseline", false, "")
	if err != nil {
		t.Fatalf("HoldArchive(release): %v", err)
	}
	if released.Held {
		t.Fatalf("released archive = %#v", released)
	}
}

func TestScheduledFailedSlotCanBeReclaimedWithANewFence(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, 7, 29, 1, 0, 0, 0, time.UTC)
	store := &memoryScheduledStateStore{}
	service, err := backupusecase.NewScheduledService(backupusecase.ScheduledOptions{
		StateStore: store, Now: func() time.Time { return now },
		NewID: func() string { return "backup-slot-retry" },
	})
	if err != nil {
		t.Fatalf("NewScheduledService(): %v", err)
	}
	configured, err := service.Configure(ctx, validConfigureRequest())
	if err != nil {
		t.Fatalf("Configure(): %v", err)
	}
	first, err := service.ClaimSlot(ctx, backupusecase.ClaimSlotRequest{
		JobID: configured.InitialJob.ID, HashSlot: 9,
		OwnerNodeID: 1, OwnerTerm: 4,
	})
	if err != nil {
		t.Fatalf("ClaimSlot(first): %v", err)
	}
	now = now.Add(time.Minute)
	if err := service.FailSlot(ctx, backupusecase.FailSlotRequest{
		JobID: configured.InitialJob.ID, HashSlot: 9,
		Attempt: first.Attempt, OwnerNodeID: 1, OwnerTerm: 4,
		ErrorCode: "slot_export_failed",
	}); err != nil {
		t.Fatalf("FailSlot(): %v", err)
	}
	failedState, err := service.State(ctx)
	if err != nil {
		t.Fatalf("State(failed): %v", err)
	}
	failed := failedState.ActiveBackup.Slots[9]
	if failed.Status != backupcontract.SlotStatusFailed ||
		failed.ErrorCode != "slot_export_failed" ||
		failed.UpdatedUnixMillis != now.UnixMilli() {
		t.Fatalf("failed Slot = %#v", failed)
	}

	second, err := service.ClaimSlot(ctx, backupusecase.ClaimSlotRequest{
		JobID: configured.InitialJob.ID, HashSlot: 9,
		OwnerNodeID: 2, OwnerTerm: 5,
	})
	if err != nil {
		t.Fatalf("ClaimSlot(retry): %v", err)
	}
	if second.Attempt != first.Attempt+1 ||
		second.Status != backupcontract.SlotStatusRunning || second.ErrorCode != "" {
		t.Fatalf("reclaimed Slot = %#v", second)
	}
	if err := service.FailSlot(ctx, backupusecase.FailSlotRequest{
		JobID: configured.InitialJob.ID, HashSlot: 9,
		Attempt: first.Attempt, OwnerNodeID: 1, OwnerTerm: 4,
		ErrorCode: "stale_worker",
	}); !errors.Is(err, backupusecase.ErrStateConflict) {
		t.Fatalf("FailSlot(stale) error = %v", err)
	}
}

func TestScheduledEvaluateAdmitsDueJobAndDisableOnlyPreservesActiveJob(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	t.Run("due schedule", func(t *testing.T) {
		now := time.Date(2026, 7, 29, 0, 30, 0, 0, time.UTC)
		store := &memoryScheduledStateStore{}
		service, err := backupusecase.NewScheduledService(backupusecase.ScheduledOptions{
			StateStore: store, Now: func() time.Time { return now },
			NewID: func() string { return "backup-due" },
		})
		if err != nil {
			t.Fatalf("NewScheduledService(): %v", err)
		}
		request := validConfigureRequest()
		request.Enabled = false
		request.TimeZone = "UTC"
		if _, err := service.Configure(ctx, request); err != nil {
			t.Fatalf("Configure(): %v", err)
		}
		store.mu.Lock()
		store.state.Plan.Enabled = true
		store.mu.Unlock()
		now = time.Date(2026, 7, 29, 1, 0, 30, 0, time.UTC)
		if err := service.Evaluate(ctx, 2*time.Minute); err != nil {
			t.Fatalf("Evaluate(): %v", err)
		}
		state, err := service.State(ctx)
		if err != nil {
			t.Fatalf("State(): %v", err)
		}
		if state.ActiveBackup == nil ||
			state.ActiveBackup.Trigger != backupcontract.TriggerScheduled ||
			state.ActiveBackup.ScheduledAtUnixMillis !=
				time.Date(2026, 7, 29, 1, 0, 0, 0, time.UTC).UnixMilli() {
			t.Fatalf("scheduled state = %#v", state)
		}
	})

	t.Run("disable only", func(t *testing.T) {
		now := time.Date(2026, 7, 29, 1, 0, 0, 0, time.UTC)
		store := &memoryScheduledStateStore{}
		service, err := backupusecase.NewScheduledService(backupusecase.ScheduledOptions{
			StateStore: store, Now: func() time.Time { return now },
			NewID: func() string { return "backup-active" },
		})
		if err != nil {
			t.Fatalf("NewScheduledService(): %v", err)
		}
		if _, err := service.Configure(ctx, validConfigureRequest()); err != nil {
			t.Fatalf("Configure(): %v", err)
		}
		disable := validConfigureRequest()
		disable.ExpectedRevision = 1
		disable.Enabled = false
		result, err := service.Configure(ctx, disable)
		if err != nil {
			t.Fatalf("Configure(disable only): %v", err)
		}
		state, err := service.State(ctx)
		if err != nil {
			t.Fatalf("State(): %v", err)
		}
		if result.Plan.Enabled || state.ActiveBackup == nil ||
			state.ActiveBackup.ID != "backup-active" {
			t.Fatalf("disable-only state = %#v", state)
		}
	})
}

func TestScheduledConfigurationRejectsUnsafeRepositoryAndExecutionLimits(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name   string
		mutate func(*backupusecase.ConfigureRequest)
	}{
		{name: "retention", mutate: func(request *backupusecase.ConfigureRequest) {
			request.RetentionCount = 0
		}},
		{name: "rate", mutate: func(request *backupusecase.ConfigureRequest) {
			request.RateBytesPerSec = 0
		}},
		{name: "workers", mutate: func(request *backupusecase.ConfigureRequest) {
			request.WorkersPerNode = 5
		}},
		{name: "deadline", mutate: func(request *backupusecase.ConfigureRequest) {
			request.MaxDuration = 49 * time.Hour
		}},
		{name: "file fields", mutate: func(request *backupusecase.ConfigureRequest) {
			request.Store.Endpoint = "file://caller-selected"
		}},
		{name: "s3 identity", mutate: func(request *backupusecase.ConfigureRequest) {
			request.Store = backupcontract.StoreConfig{
				Kind: backupcontract.StoreKindS3, Endpoint: "https://s3.example.test",
				Bucket: "backups", Prefix: "cluster-a",
			}
		}},
		{name: "oss region", mutate: func(request *backupusecase.ConfigureRequest) {
			request.Store = backupcontract.StoreConfig{
				Kind: backupcontract.StoreKindOSS, Region: "CN-Hangzhou",
				Bucket: "backups", Prefix: "cluster-a",
				CredentialCiphertext: []byte("sealed"),
			}
		}},
		{name: "cos app id", mutate: func(request *backupusecase.ConfigureRequest) {
			request.Store = backupcontract.StoreConfig{
				Kind: backupcontract.StoreKindCOS, Region: "ap-shanghai",
				Bucket: "backups", Prefix: "cluster-a",
				CredentialCiphertext: []byte("sealed"),
			}
		}},
		{name: "unknown store", mutate: func(request *backupusecase.ConfigureRequest) {
			request.Store.Kind = backupcontract.StoreKind("unknown")
		}},
		{name: "unverified timestamp", mutate: func(request *backupusecase.ConfigureRequest) {
			request.Enabled = false
			request.RepositoryVerification = &backupcontract.RepositoryVerification{
				Status:               backupcontract.RepositoryVerificationUnverified,
				VerifiedAtUnixMillis: 1,
			}
		}},
		{name: "verified without timestamp", mutate: func(request *backupusecase.ConfigureRequest) {
			request.RepositoryVerification = &backupcontract.RepositoryVerification{
				Status: backupcontract.RepositoryVerificationVerified,
			}
		}},
		{name: "unknown verification", mutate: func(request *backupusecase.ConfigureRequest) {
			request.RepositoryVerification = &backupcontract.RepositoryVerification{
				Status: backupcontract.RepositoryVerificationStatus("unknown"),
			}
		}},
		{name: "time zone", mutate: func(request *backupusecase.ConfigureRequest) {
			request.TimeZone = "Mars/Olympus_Mons"
		}},
		{name: "cron syntax", mutate: func(request *backupusecase.ConfigureRequest) {
			request.Cron = "not a schedule"
		}},
		{name: "cron frequency", mutate: func(request *backupusecase.ConfigureRequest) {
			request.Cron = "*/5 * * * *"
			request.TimeZone = "UTC"
		}},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			now := time.Date(2026, 7, 29, 1, 0, 0, 0, time.UTC)
			service, err := backupusecase.NewScheduledService(backupusecase.ScheduledOptions{
				StateStore: &memoryScheduledStateStore{},
				Now:        func() time.Time { return now },
				NewID:      func() string { return "must-not-admit" },
			})
			if err != nil {
				t.Fatalf("NewScheduledService(): %v", err)
			}
			request := validConfigureRequest()
			testCase.mutate(&request)
			if _, err := service.Configure(
				context.Background(), request,
			); !errors.Is(err, backupusecase.ErrInvalidRequest) {
				t.Fatalf("Configure() error = %v", err)
			}
		})
	}
}

func TestScheduledStateMachineFencesLateCancellationAndIncompletePublication(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, 7, 29, 1, 0, 0, 0, time.UTC)
	store := &memoryScheduledStateStore{}
	service, err := backupusecase.NewScheduledService(backupusecase.ScheduledOptions{
		StateStore: store, Now: func() time.Time { return now },
		NewID: func() string { return "backup-fenced-transition" },
	})
	if err != nil {
		t.Fatalf("NewScheduledService(): %v", err)
	}
	configured, err := service.Configure(ctx, validConfigureRequest())
	if err != nil {
		t.Fatalf("Configure(): %v", err)
	}
	jobID := configured.InitialJob.ID

	if err := service.AdvanceBackupPhase(ctx, backupusecase.AdvanceBackupPhaseRequest{
		JobID: jobID, From: backupcontract.JobStatusPreparing,
		To: backupcontract.JobStatusCleaning,
	}); !errors.Is(err, backupusecase.ErrInvalidRequest) {
		t.Fatalf("AdvanceBackupPhase(invalid) error = %v", err)
	}
	if err := service.AdvanceBackupPhase(ctx, backupusecase.AdvanceBackupPhaseRequest{
		JobID: jobID, From: backupcontract.JobStatusPreparing,
		To: backupcontract.JobStatusPublishing,
	}); !errors.Is(err, backupusecase.ErrStateConflict) {
		t.Fatalf("AdvanceBackupPhase(incomplete) error = %v", err)
	}
	if err := service.FinishBackup(ctx, backupusecase.FinishBackupRequest{
		JobID: jobID, Status: backupcontract.JobStatusSucceeded,
	}); !errors.Is(err, backupusecase.ErrStateConflict) {
		t.Fatalf("FinishBackup(incomplete) error = %v", err)
	}
	if err := service.FinishBackup(ctx, backupusecase.FinishBackupRequest{
		JobID: jobID, Status: backupcontract.JobStatusCanceled,
	}); !errors.Is(err, backupusecase.ErrStateConflict) {
		t.Fatalf("FinishBackup(not canceled) error = %v", err)
	}

	store.mu.Lock()
	store.state.ActiveBackup.Status = backupcontract.JobStatusPublishing
	store.mu.Unlock()
	if err := service.RequestBackupCancellation(
		ctx, jobID,
	); !errors.Is(err, backupusecase.ErrStateConflict) {
		t.Fatalf("RequestBackupCancellation(publishing) error = %v", err)
	}
	store.mu.Lock()
	store.state.ActiveBackup.Status = backupcontract.JobStatusPreparing
	for index := range store.state.ActiveBackup.Slots {
		store.state.ActiveBackup.Slots[index].Status = backupcontract.SlotStatusComplete
	}
	store.state.ActiveBackup.CancelRequested = true
	store.mu.Unlock()
	if err := service.AdvanceBackupPhase(ctx, backupusecase.AdvanceBackupPhaseRequest{
		JobID: jobID, From: backupcontract.JobStatusPreparing,
		To: backupcontract.JobStatusPublishing,
	}); !errors.Is(err, backupusecase.ErrStateConflict) {
		t.Fatalf("AdvanceBackupPhase(canceled) error = %v", err)
	}
}

func TestScheduledAuxiliaryHistoryIsIdempotentBoundedAndRetryable(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, 7, 29, 1, 0, 0, 0, time.UTC)
	history := make([]backupcontract.TaskRecord, backupcontract.MaxTaskHistory)
	for index := range history {
		history[index] = backupcontract.TaskRecord{
			ID: fmt.Sprintf("old-%03d", index), Kind: "retention",
			Status: string(backupcontract.JobStatusSucceeded),
		}
	}
	store := &memoryScheduledStateStore{
		state:                   backupcontract.SystemState{Revision: 5, History: history},
		interveningStateUpdates: 1,
	}
	service, err := backupusecase.NewScheduledService(backupusecase.ScheduledOptions{
		StateStore: store, Now: func() time.Time { return now },
		NewID: func() string { return "unused" },
	})
	if err != nil {
		t.Fatalf("NewScheduledService(): %v", err)
	}
	request := backupusecase.RecordTaskRequest{
		ID: "verification-1", Kind: "verification",
		Status:              backupcontract.JobStatusSucceeded,
		StartedUnixMillis:   now.Add(-time.Minute).UnixMilli(),
		CompletedUnixMillis: now.UnixMilli(),
	}
	if err := service.RecordTask(ctx, request); err != nil {
		t.Fatalf("RecordTask(): %v", err)
	}
	state, err := service.State(ctx)
	if err != nil {
		t.Fatalf("State(): %v", err)
	}
	if len(state.History) != backupcontract.MaxTaskHistory ||
		state.History[0].ID != request.ID || state.Revision != 7 {
		t.Fatalf("bounded history = %#v", state)
	}
	if err := service.RecordTask(ctx, request); err != nil {
		t.Fatalf("RecordTask(idempotent): %v", err)
	}
	idempotent, err := service.State(ctx)
	if err != nil {
		t.Fatalf("State(idempotent): %v", err)
	}
	if idempotent.Revision != state.Revision ||
		len(idempotent.History) != len(state.History) {
		t.Fatalf("idempotent history = %#v", idempotent)
	}
	invalid := request
	invalid.Kind = "backup"
	if err := service.RecordTask(
		ctx, invalid,
	); !errors.Is(err, backupusecase.ErrInvalidRequest) {
		t.Fatalf("RecordTask(kind) error = %v", err)
	}
	invalid = request
	invalid.Status = backupcontract.JobStatusPreparing
	if err := service.RecordTask(
		ctx, invalid,
	); !errors.Is(err, backupusecase.ErrInvalidRequest) {
		t.Fatalf("RecordTask(status) error = %v", err)
	}
}

func TestRestoreAdmissionPublishesOnlyAfterPreflightAndRevalidation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, 7, 29, 3, 0, 0, 0, time.UTC)
	archiveStore, err := backupinfra.NewFileArchiveStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileArchiveStore(): %v", err)
	}
	writeCatalogArchive(t, archiveStore, "backup-restore", true, now.UnixMilli())
	stateStore := &memoryScheduledStateStore{state: backupcontract.SystemState{
		Revision: 7,
		Plan: &backupcontract.Plan{
			Revision: 3,
			Store:    backupcontract.StoreConfig{Kind: backupcontract.StoreKindFile},
		},
	}}
	preflight := &recordingRestorePreflight{}
	ids := []string{"restore-operation", "restore-job"}
	restore, err := backupusecase.NewRestoreService(backupusecase.RestoreServiceOptions{
		StateStore: stateStore,
		Repository: fixedRepositoryProvider{store: archiveStore},
		Preflight:  preflight,
		Now:        func() time.Time { return now },
		NewID: func() string {
			id := ids[0]
			ids = ids[1:]
			return id
		},
		NewActivation: func() string { return "activation-next" },
	})
	if err != nil {
		t.Fatalf("NewRestoreService(): %v", err)
	}

	job, err := restore.StartRestore(ctx, " backup-restore ", " operator-a ")
	if err != nil {
		t.Fatalf("StartRestore(): %v", err)
	}
	if job.ID != "restore-job" || job.BackupID != "backup-restore" ||
		job.Initiator != "operator-a" || job.TargetActivation != "activation-next" ||
		job.Status != backupcontract.RestoreStatusPreparing ||
		len(job.Slots) != backupcontract.HashSlotCount ||
		job.Slots[255].HashSlot != 255 {
		t.Fatalf("restore job = %#v", job)
	}
	if preflight.calls != 1 || preflight.job.ID != job.ID ||
		preflight.plan.Revision != 3 || preflight.manifest.ID != "backup-restore" {
		t.Fatalf("preflight = %#v", preflight)
	}
	state, err := stateStore.Load(ctx)
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	if state.ActiveRestore == nil || state.ActiveRestore.ID != job.ID ||
		state.ActiveArchiveOperation != nil {
		t.Fatalf("admitted state = %#v", state)
	}
}

func TestRestoreAdmissionReleasesLeaseWhenPreflightStateBecomesStale(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, 7, 29, 3, 0, 0, 0, time.UTC)
	archiveStore, err := backupinfra.NewFileArchiveStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileArchiveStore(): %v", err)
	}
	writeCatalogArchive(t, archiveStore, "backup-stale", true, now.UnixMilli())
	stateStore := &memoryScheduledStateStore{state: backupcontract.SystemState{
		Revision: 2,
		Plan: &backupcontract.Plan{
			Revision: 1,
			Store:    backupcontract.StoreConfig{Kind: backupcontract.StoreKindFile},
		},
	}}
	preflight := &recordingRestorePreflight{beforeReturn: func() {
		stateStore.mu.Lock()
		defer stateStore.mu.Unlock()
		stateStore.state.Revision++
		stateStore.state.Plan.Revision++
	}}
	ids := []string{"stale-operation", "stale-restore"}
	restore, err := backupusecase.NewRestoreService(backupusecase.RestoreServiceOptions{
		StateStore: stateStore,
		Repository: fixedRepositoryProvider{store: archiveStore},
		Preflight:  preflight,
		Now:        func() time.Time { return now },
		NewID: func() string {
			id := ids[0]
			ids = ids[1:]
			return id
		},
		NewActivation: func() string { return "activation-stale" },
	})
	if err != nil {
		t.Fatalf("NewRestoreService(): %v", err)
	}

	_, err = restore.StartRestore(ctx, "backup-stale", "operator-a")
	if !errors.Is(err, backupusecase.ErrStateConflict) {
		t.Fatalf("StartRestore(stale) error = %v", err)
	}
	state, loadErr := stateStore.Load(ctx)
	if loadErr != nil {
		t.Fatalf("Load(): %v", loadErr)
	}
	if state.ActiveRestore != nil || state.ActiveArchiveOperation != nil {
		t.Fatalf("stale admission leaked ownership: %#v", state)
	}
}

func TestRestoreAdmissionRejectsConflictingWorkAndReleasesFailureLease(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, 7, 29, 3, 0, 0, 0, time.UTC)
	archiveStore, err := backupinfra.NewFileArchiveStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileArchiveStore(): %v", err)
	}
	writeCatalogArchive(t, archiveStore, "backup-admission", true, now.UnixMilli())
	preflightErr := errors.New("replica capacity changed")
	openErr := errors.New("repository unavailable")
	testCases := []struct {
		name       string
		state      backupcontract.SystemState
		repository backupusecase.ArchiveRepositoryProvider
		preflight  backupusecase.RestorePreflight
		ids        []string
		activation string
		archiveID  string
		initiator  string
		want       error
	}{
		{
			name: "missing plan", state: backupcontract.SystemState{Revision: 1},
			repository: fixedRepositoryProvider{store: archiveStore},
			preflight:  noopRestorePreflight{}, ids: []string{"unused"},
			activation: "activation", archiveID: "backup-admission",
			initiator: "operator", want: backupusecase.ErrDisabled,
		},
		{
			name: "active backup", state: restoreAdmissionState(),
			repository: fixedRepositoryProvider{store: archiveStore},
			preflight:  noopRestorePreflight{}, ids: []string{"unused"},
			activation: "activation", archiveID: "backup-admission",
			initiator: "operator", want: backupusecase.ErrBackupJobActive,
		},
		{
			name: "active restore", state: func() backupcontract.SystemState {
				state := restoreAdmissionState()
				state.ActiveBackup = nil
				state.ActiveRestore = &backupcontract.RestoreJob{ID: "restore-active"}
				return state
			}(),
			repository: fixedRepositoryProvider{store: archiveStore},
			preflight:  noopRestorePreflight{}, ids: []string{"unused"},
			activation: "activation", archiveID: "backup-admission",
			initiator: "operator", want: backupusecase.ErrRestoreJobActive,
		},
		{
			name: "repository open", state: func() backupcontract.SystemState {
				state := restoreAdmissionState()
				state.ActiveBackup = nil
				return state
			}(),
			repository: failingArchiveRepository{err: openErr},
			preflight:  noopRestorePreflight{}, ids: []string{"operation"},
			activation: "activation", archiveID: "backup-admission",
			initiator: "operator", want: openErr,
		},
		{
			name: "preflight", state: func() backupcontract.SystemState {
				state := restoreAdmissionState()
				state.ActiveBackup = nil
				return state
			}(),
			repository: fixedRepositoryProvider{store: archiveStore},
			preflight:  &recordingRestorePreflight{err: preflightErr},
			ids:        []string{"operation", "restore"}, activation: "activation",
			archiveID: "backup-admission", initiator: "operator", want: preflightErr,
		},
		{
			name: "empty restore id", state: func() backupcontract.SystemState {
				state := restoreAdmissionState()
				state.ActiveBackup = nil
				return state
			}(),
			repository: fixedRepositoryProvider{store: archiveStore},
			preflight:  noopRestorePreflight{}, ids: []string{"operation", ""},
			activation: "activation", archiveID: "backup-admission",
			initiator: "operator", want: backupusecase.ErrInvalidRequest,
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			stateStore := &memoryScheduledStateStore{state: testCase.state}
			ids := append([]string(nil), testCase.ids...)
			restore, err := backupusecase.NewRestoreService(
				backupusecase.RestoreServiceOptions{
					StateStore: stateStore, Repository: testCase.repository,
					Preflight: testCase.preflight, Now: func() time.Time { return now },
					NewID: func() string {
						if len(ids) == 0 {
							return "unexpected-id-request"
						}
						id := ids[0]
						ids = ids[1:]
						return id
					},
					NewActivation: func() string { return testCase.activation },
				},
			)
			if err != nil {
				t.Fatalf("NewRestoreService(): %v", err)
			}
			_, err = restore.StartRestore(
				ctx, testCase.archiveID, testCase.initiator,
			)
			if !errors.Is(err, testCase.want) {
				t.Fatalf("StartRestore() error = %v, want %v", err, testCase.want)
			}
			state, loadErr := stateStore.Load(ctx)
			if loadErr != nil {
				t.Fatalf("Load(): %v", loadErr)
			}
			if state.ActiveArchiveOperation != nil {
				t.Fatalf("failed admission leaked lease: %#v", state)
			}
		})
	}

	service, err := backupusecase.NewRestoreService(backupusecase.RestoreServiceOptions{
		StateStore: &memoryScheduledStateStore{},
		Repository: fixedRepositoryProvider{store: archiveStore},
		Preflight:  noopRestorePreflight{}, Now: func() time.Time { return now },
		NewID:         func() string { return "unused" },
		NewActivation: func() string { return "activation" },
	})
	if err != nil {
		t.Fatalf("NewRestoreService(invalid request): %v", err)
	}
	if _, err := service.StartRestore(
		ctx, "", "operator",
	); !errors.Is(err, backupusecase.ErrInvalidRequest) {
		t.Fatalf("StartRestore(empty archive) error = %v", err)
	}
}

func TestRestoreCancellationRetriesDurableRollbackBeforeClearingMaintenance(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, 7, 29, 3, 0, 0, 0, time.UTC)
	slots := make([]backupcontract.RestoreSlotProgress, backupcontract.HashSlotCount)
	for hashSlot := range slots {
		slots[hashSlot] = backupcontract.RestoreSlotProgress{
			HashSlot: uint16(hashSlot), Status: backupcontract.RestoreSlotStatusPending,
		}
	}
	stateStore := &memoryScheduledStateStore{state: backupcontract.SystemState{
		Revision: 4,
		Plan: &backupcontract.Plan{
			Revision: 1,
			Store:    backupcontract.StoreConfig{Kind: backupcontract.StoreKindFile},
		},
		ActiveRestore: &backupcontract.RestoreJob{
			ID: "restore-cancel", BackupID: "backup-1", Initiator: "operator-a",
			Status:             backupcontract.RestoreStatusStaging,
			StartedUnixMillis:  now.Add(-time.Hour).UnixMilli(),
			DeadlineUnixMillis: now.Add(time.Hour).UnixMilli(),
			UpdatedUnixMillis:  now.UnixMilli(),
			CancelRequested:    true, MaintenanceEntered: true,
			PreviousActivation: "activation-old", TargetActivation: "activation-new",
			Slots: slots,
		},
	}}
	scheduled, err := backupusecase.NewScheduledService(backupusecase.ScheduledOptions{
		StateStore: stateStore, Now: func() time.Time { return now },
		NewID: func() string { return "unused" },
	})
	if err != nil {
		t.Fatalf("NewScheduledService(): %v", err)
	}
	restore, err := backupusecase.NewRestoreService(backupusecase.RestoreServiceOptions{
		StateStore: stateStore, Repository: fixedRepositoryProvider{},
		Preflight: noopRestorePreflight{}, Now: func() time.Time { return now },
		NewID:         func() string { return "unused" },
		NewActivation: func() string { return "unused" },
	})
	if err != nil {
		t.Fatalf("NewRestoreService(): %v", err)
	}
	rollbackErr := errors.New("replica rollback unavailable")
	executor := &rollbackRestoreExecutor{rollbackErr: rollbackErr}
	runner, err := backupusecase.NewRestoreRunner(
		scheduled, restore, executor, func() time.Time { return now },
	)
	if err != nil {
		t.Fatalf("NewRestoreRunner(): %v", err)
	}

	advanced, err := runner.RunOnce(ctx)
	if !advanced || !errors.Is(err, rollbackErr) {
		t.Fatalf("RunOnce(first) advanced=%v error=%v", advanced, err)
	}
	state, err := scheduled.State(ctx)
	if err != nil {
		t.Fatalf("State(first): %v", err)
	}
	if state.ActiveRestore == nil ||
		state.ActiveRestore.Status != backupcontract.RestoreStatusRollingBack ||
		state.ActiveRestore.ErrorCode != "canceled" || len(state.History) != 0 {
		t.Fatalf("durable rollback state = %#v", state)
	}
	if executor.exits != 0 {
		t.Fatalf("maintenance exited before rollback succeeded: %d", executor.exits)
	}

	executor.rollbackErr = nil
	advanced, err = runner.RunOnce(ctx)
	if !advanced || err != nil {
		t.Fatalf("RunOnce(retry) advanced=%v error=%v", advanced, err)
	}
	state, err = scheduled.State(ctx)
	if err != nil {
		t.Fatalf("State(retry): %v", err)
	}
	if state.ActiveRestore != nil || len(state.History) != 1 ||
		state.History[0].Status != string(backupcontract.RestoreStatusCanceled) ||
		state.ManagerSessionEpoch != 0 || executor.rollbacks != 2 ||
		executor.exits != 1 || executor.lastExitSuccess {
		t.Fatalf("completed rollback state=%#v executor=%#v", state, executor)
	}
}

func TestRestoreExecutionFailureIsDurablyRolledBackBeforeFailureHistory(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, 7, 29, 3, 0, 0, 0, time.UTC)
	slots := make([]backupcontract.RestoreSlotProgress, backupcontract.HashSlotCount)
	for hashSlot := range slots {
		slots[hashSlot] = backupcontract.RestoreSlotProgress{
			HashSlot: uint16(hashSlot), Status: backupcontract.RestoreSlotStatusPending,
		}
	}
	stateStore := &memoryScheduledStateStore{state: backupcontract.SystemState{
		Revision: 9,
		Plan: &backupcontract.Plan{
			Revision: 1,
			Store:    backupcontract.StoreConfig{Kind: backupcontract.StoreKindFile},
		},
		ActiveRestore: &backupcontract.RestoreJob{
			ID: "restore-stage-failure", BackupID: "backup-1", Initiator: "operator-a",
			Status:             backupcontract.RestoreStatusMaintenance,
			StartedUnixMillis:  now.Add(-time.Hour).UnixMilli(),
			DeadlineUnixMillis: now.Add(time.Hour).UnixMilli(),
			UpdatedUnixMillis:  now.UnixMilli(), MaintenanceEntered: true,
			PreviousActivation: "activation-old", TargetActivation: "activation-new",
			Slots: slots,
		},
	}}
	scheduled, err := backupusecase.NewScheduledService(backupusecase.ScheduledOptions{
		StateStore: stateStore, Now: func() time.Time { return now },
		NewID: func() string { return "unused" },
	})
	if err != nil {
		t.Fatalf("NewScheduledService(): %v", err)
	}
	restore, err := backupusecase.NewRestoreService(backupusecase.RestoreServiceOptions{
		StateStore: stateStore, Repository: fixedRepositoryProvider{},
		Preflight: noopRestorePreflight{}, Now: func() time.Time { return now },
		NewID:         func() string { return "unused" },
		NewActivation: func() string { return "unused" },
	})
	if err != nil {
		t.Fatalf("NewRestoreService(): %v", err)
	}
	stageErr := errors.New("replica staging checksum mismatch")
	executor := &rollbackRestoreExecutor{stageErr: stageErr}
	runner, err := backupusecase.NewRestoreRunner(
		scheduled, restore, executor, func() time.Time { return now },
	)
	if err != nil {
		t.Fatalf("NewRestoreRunner(): %v", err)
	}

	advanced, err := runner.RunOnce(ctx)
	if !advanced || !errors.Is(err, stageErr) {
		t.Fatalf("RunOnce(stage) advanced=%v error=%v", advanced, err)
	}
	state, err := scheduled.State(ctx)
	if err != nil {
		t.Fatalf("State(stage): %v", err)
	}
	if state.ActiveRestore == nil ||
		state.ActiveRestore.Status != backupcontract.RestoreStatusRollingBack ||
		state.ActiveRestore.ErrorCode != "staging_failed" || len(state.History) != 0 {
		t.Fatalf("rollback admission state = %#v", state)
	}

	executor.stageErr = nil
	advanced, err = runner.RunOnce(ctx)
	if !advanced || err != nil {
		t.Fatalf("RunOnce(rollback) advanced=%v error=%v", advanced, err)
	}
	state, err = scheduled.State(ctx)
	if err != nil {
		t.Fatalf("State(rollback): %v", err)
	}
	if state.ActiveRestore != nil || len(state.History) != 1 ||
		state.History[0].Status != string(backupcontract.RestoreStatusFailed) ||
		state.History[0].ErrorCode != "staging_failed" ||
		executor.rollbacks != 1 || executor.exits != 1 ||
		executor.lastExitSuccess {
		t.Fatalf("failed restore state=%#v executor=%#v", state, executor)
	}
}

func TestRestoreCancellationAndRollbackTransitionsAreFenced(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, 7, 29, 3, 0, 0, 0, time.UTC)
	stateStore := &memoryScheduledStateStore{state: backupcontract.SystemState{
		Revision: 1,
		Plan:     &backupcontract.Plan{Revision: 1},
		ActiveRestore: &backupcontract.RestoreJob{
			ID: "restore-fenced", Status: backupcontract.RestoreStatusStaging,
			StartedUnixMillis:  now.Add(-time.Hour).UnixMilli(),
			DeadlineUnixMillis: now.Add(time.Hour).UnixMilli(),
		},
	}}
	restore, err := backupusecase.NewRestoreService(backupusecase.RestoreServiceOptions{
		StateStore: stateStore, Repository: fixedRepositoryProvider{},
		Preflight: noopRestorePreflight{}, Now: func() time.Time { return now },
		NewID:         func() string { return "unused" },
		NewActivation: func() string { return "unused" },
	})
	if err != nil {
		t.Fatalf("NewRestoreService(): %v", err)
	}

	if err := restore.RequestCancellation(ctx, "restore-fenced"); err != nil {
		t.Fatalf("RequestCancellation(): %v", err)
	}
	state, err := stateStore.Load(ctx)
	if err != nil {
		t.Fatalf("Load(canceled): %v", err)
	}
	if state.ActiveRestore == nil || !state.ActiveRestore.CancelRequested {
		t.Fatalf("cancellation state = %#v", state.ActiveRestore)
	}
	if err := restore.BeginRollback(
		ctx, "restore-fenced", "operator_canceled",
	); err != nil {
		t.Fatalf("BeginRollback(): %v", err)
	}
	if err := restore.BeginRollback(
		ctx, "restore-fenced", "operator_canceled",
	); err != nil {
		t.Fatalf("BeginRollback(idempotent): %v", err)
	}
	if err := restore.BeginRollback(
		ctx, "restore-fenced", "different_failure",
	); !errors.Is(err, backupusecase.ErrStateConflict) {
		t.Fatalf("BeginRollback(different) error = %v", err)
	}
	if err := restore.RequestCancellation(
		ctx, "restore-fenced",
	); !errors.Is(err, backupusecase.ErrStateConflict) {
		t.Fatalf("RequestCancellation(rolling back) error = %v", err)
	}
	if err := restore.BeginRollback(
		ctx, "restore-fenced", "",
	); !errors.Is(err, backupusecase.ErrInvalidRequest) {
		t.Fatalf("BeginRollback(empty) error = %v", err)
	}
}

func TestJobRunnerCleansUnpublishedPermanentPublicationFailure(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, 7, 29, 4, 0, 0, 0, time.UTC)
	_, scheduled := newPublishingBackupState(t, now, "backup-publish-failed")
	store, err := backupinfra.NewFileArchiveStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileArchiveStore(): %v", err)
	}
	putBytes := func(key string, body []byte) {
		t.Helper()
		if err := store.Put(ctx, backupartifact.PutObject{
			Key: key, Body: bytes.NewReader(body), ExpectedBytes: uint64(len(body)),
		}); err != nil {
			t.Fatalf("Put(%s): %v", key, err)
		}
	}
	putBytes("pending/backup-publish-failed", []byte("started"))
	putBytes("backups/backup-publish-failed/slots/000/partial", []byte("partial"))
	finalizer := &failingPublicationFinalizer{err: backupartifact.ErrInvalidManifest}
	runner, err := backupusecase.NewJobRunner(backupusecase.JobRunnerOptions{
		Scheduled: scheduled, Repository: fixedRepositoryProvider{store: store},
		Slots: &recordingSlotExecutor{}, Finalizer: finalizer,
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewJobRunner(): %v", err)
	}

	advanced, err := runner.RunOnce(ctx)
	if !advanced || err != nil {
		t.Fatalf("RunOnce() advanced=%v error=%v", advanced, err)
	}
	state, err := scheduled.State(ctx)
	if err != nil {
		t.Fatalf("State(): %v", err)
	}
	if state.ActiveBackup != nil || len(state.History) != 1 ||
		state.History[0].Status != string(backupcontract.JobStatusFailed) ||
		state.History[0].ErrorCode != "publication_failed" {
		t.Fatalf("terminal publication state = %#v", state)
	}
	objects, err := store.List(ctx, "backups/backup-publish-failed")
	if err != nil {
		t.Fatalf("List(partial): %v", err)
	}
	if len(objects) != 0 {
		t.Fatalf("unpublished partial objects remain: %#v", objects)
	}
	if _, _, err := store.Open(
		ctx, "pending/backup-publish-failed",
	); !errors.Is(err, backupartifact.ErrObjectNotFound) {
		t.Fatalf("pending marker error = %v", err)
	}
	if finalizer.publishCalls != 1 {
		t.Fatalf("publish calls = %d", finalizer.publishCalls)
	}
}

func TestJobRunnerPreservesPublishedArchiveAcrossFinalizerRetryError(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, 7, 29, 4, 0, 0, 0, time.UTC)
	_, scheduled := newPublishingBackupState(t, now, "backup-already-published")
	store, err := backupinfra.NewFileArchiveStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileArchiveStore(): %v", err)
	}
	marker := []byte("publication evidence")
	if err := store.Put(ctx, backupartifact.PutObject{
		Key:  "backups/backup-already-published/COMPLETE",
		Body: bytes.NewReader(marker), ExpectedBytes: uint64(len(marker)),
	}); err != nil {
		t.Fatalf("Put(COMPLETE): %v", err)
	}
	finalizer := &failingPublicationFinalizer{err: backupartifact.ErrObjectCorrupt}
	runner, err := backupusecase.NewJobRunner(backupusecase.JobRunnerOptions{
		Scheduled: scheduled, Repository: fixedRepositoryProvider{store: store},
		Slots: &recordingSlotExecutor{}, Finalizer: finalizer,
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewJobRunner(): %v", err)
	}

	advanced, err := runner.RunOnce(ctx)
	if !advanced || err != nil {
		t.Fatalf("RunOnce() advanced=%v error=%v", advanced, err)
	}
	state, err := scheduled.State(ctx)
	if err != nil {
		t.Fatalf("State(): %v", err)
	}
	if state.ActiveBackup != nil || len(state.History) != 1 ||
		state.History[0].ErrorCode != "publication_failed" {
		t.Fatalf("terminal state = %#v", state)
	}
	reader, object, err := store.Open(
		ctx, "backups/backup-already-published/COMPLETE",
	)
	if err != nil {
		t.Fatalf("Open(COMPLETE): %v", err)
	}
	defer reader.Close()
	if object.Bytes != uint64(len(marker)) {
		t.Fatalf("COMPLETE bytes = %d", object.Bytes)
	}
}

func TestJobRunnerLeavesTransientPublicationFailureResumable(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, 7, 29, 4, 0, 0, 0, time.UTC)
	_, scheduled := newPublishingBackupState(t, now, "backup-transient-publish")
	store, err := backupinfra.NewFileArchiveStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileArchiveStore(): %v", err)
	}
	transientErr := errors.New("repository transport interrupted")
	runner, err := backupusecase.NewJobRunner(backupusecase.JobRunnerOptions{
		Scheduled: scheduled, Repository: fixedRepositoryProvider{store: store},
		Slots:     &recordingSlotExecutor{},
		Finalizer: &failingPublicationFinalizer{err: transientErr},
		Now:       func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewJobRunner(): %v", err)
	}

	advanced, err := runner.RunOnce(ctx)
	if !advanced || !errors.Is(err, transientErr) {
		t.Fatalf("RunOnce() advanced=%v error=%v", advanced, err)
	}
	state, err := scheduled.State(ctx)
	if err != nil {
		t.Fatalf("State(): %v", err)
	}
	if state.ActiveBackup == nil ||
		state.ActiveBackup.Status != backupcontract.JobStatusPublishing ||
		len(state.History) != 0 {
		t.Fatalf("transient publication state = %#v", state)
	}
}

func TestJobRunnerRecordsExportFailureForDurableRetry(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, 7, 29, 4, 0, 0, 0, time.UTC)
	stateStore := &memoryScheduledStateStore{}
	scheduled, err := backupusecase.NewScheduledService(backupusecase.ScheduledOptions{
		StateStore: stateStore, Now: func() time.Time { return now },
		NewID: func() string { return "backup-export-retry" },
	})
	if err != nil {
		t.Fatalf("NewScheduledService(): %v", err)
	}
	if _, err := scheduled.Configure(ctx, validConfigureRequest()); err != nil {
		t.Fatalf("Configure(): %v", err)
	}
	store, err := backupinfra.NewFileArchiveStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileArchiveStore(): %v", err)
	}
	exportErr := errors.New("replica snapshot unavailable")
	executor := &retryingSlotExecutor{err: exportErr}
	runner, err := backupusecase.NewJobRunner(backupusecase.JobRunnerOptions{
		Scheduled: scheduled, Repository: fixedRepositoryProvider{store: store},
		Slots: executor, Finalizer: &recordingArchiveFinalizer{},
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewJobRunner(): %v", err)
	}

	advanced, err := runner.RunOnce(ctx)
	if !advanced || !errors.Is(err, exportErr) {
		t.Fatalf("RunOnce(failed) advanced=%v error=%v", advanced, err)
	}
	state, err := scheduled.State(ctx)
	if err != nil {
		t.Fatalf("State(failed): %v", err)
	}
	failed := state.ActiveBackup.Slots[0]
	if failed.Status != backupcontract.SlotStatusFailed || failed.Attempt != 1 ||
		failed.ErrorCode != "slot_export_failed" {
		t.Fatalf("durable failed Slot = %#v", failed)
	}

	executor.err = nil
	advanced, err = runner.RunOnce(ctx)
	if !advanced || err != nil {
		t.Fatalf("RunOnce(retry) advanced=%v error=%v", advanced, err)
	}
	state, err = scheduled.State(ctx)
	if err != nil {
		t.Fatalf("State(retry): %v", err)
	}
	completed := state.ActiveBackup.Slots[0]
	if completed.Status != backupcontract.SlotStatusComplete ||
		completed.Attempt != 2 || completed.ManifestKey == "" {
		t.Fatalf("retried Slot = %#v", completed)
	}
}

type recordingRestorePreflight struct {
	calls        int
	job          backupcontract.RestoreJob
	plan         backupcontract.Plan
	manifest     backupartifact.ArchiveManifest
	beforeReturn func()
	err          error
}

type failingArchiveRepository struct {
	err error
}

func (r failingArchiveRepository) Open(
	context.Context,
	backupcontract.StoreConfig,
) (backupartifact.ArchiveStore, error) {
	return nil, r.err
}

func restoreAdmissionState() backupcontract.SystemState {
	return backupcontract.SystemState{
		Revision: 1,
		Plan: &backupcontract.Plan{
			Revision: 1,
			Store:    backupcontract.StoreConfig{Kind: backupcontract.StoreKindFile},
		},
		ActiveBackup: &backupcontract.BackupJob{ID: "backup-active"},
	}
}

func (p *recordingRestorePreflight) Check(
	_ context.Context,
	job backupcontract.RestoreJob,
	plan backupcontract.Plan,
	manifest backupartifact.ArchiveManifest,
) error {
	p.calls++
	p.job = job
	p.plan = plan
	p.manifest = manifest
	if p.beforeReturn != nil {
		p.beforeReturn()
	}
	return p.err
}

type rollbackRestoreExecutor struct {
	rollbacks       int
	exits           int
	lastExitSuccess bool
	rollbackErr     error
	stageErr        error
}

func (*rollbackRestoreExecutor) VerifyArchive(
	context.Context,
	backupcontract.RestoreJob,
) error {
	return errors.New("unexpected archive verification")
}

func (*rollbackRestoreExecutor) EnterMaintenance(
	context.Context,
	backupcontract.RestoreJob,
) (string, error) {
	return "", errors.New("unexpected maintenance entry")
}

func (e *rollbackRestoreExecutor) StageSlot(
	context.Context,
	backupcontract.RestoreJob,
	uint16,
	uint32,
) (backupusecase.RestoreStageResult, error) {
	if e.stageErr == nil {
		return backupusecase.RestoreStageResult{}, errors.New("unexpected staging")
	}
	return backupusecase.RestoreStageResult{}, e.stageErr
}

func (*rollbackRestoreExecutor) VerifySlot(
	context.Context,
	backupcontract.RestoreJob,
	uint16,
	uint32,
) error {
	return errors.New("unexpected Slot verification")
}

func (*rollbackRestoreExecutor) ActivateRestore(
	context.Context,
	backupcontract.RestoreJob,
) error {
	return errors.New("unexpected activation")
}

func (e *rollbackRestoreExecutor) Rollback(
	context.Context,
	backupcontract.RestoreJob,
) error {
	e.rollbacks++
	return e.rollbackErr
}

func (e *rollbackRestoreExecutor) ExitMaintenance(
	_ context.Context,
	_ backupcontract.RestoreJob,
	success bool,
) error {
	e.exits++
	e.lastExitSuccess = success
	return nil
}

type failingPublicationFinalizer struct {
	err          error
	publishCalls int
}

func (f *failingPublicationFinalizer) Publish(
	context.Context,
	backupartifact.ArchiveStore,
	backupcontract.BackupJob,
) error {
	f.publishCalls++
	return f.err
}

func (*failingPublicationFinalizer) ApplyRetention(
	context.Context,
	backupartifact.ArchiveStore,
	int,
) error {
	return errors.New("unexpected retention")
}

func newPublishingBackupState(
	t *testing.T,
	now time.Time,
	jobID string,
) (*memoryScheduledStateStore, *backupusecase.ScheduledService) {
	t.Helper()
	slots := make([]backupcontract.SlotProgress, backupcontract.HashSlotCount)
	for hashSlot := range slots {
		slots[hashSlot] = backupcontract.SlotProgress{
			HashSlot: uint16(hashSlot), Status: backupcontract.SlotStatusComplete,
		}
	}
	store := &memoryScheduledStateStore{state: backupcontract.SystemState{
		Revision: 3,
		Plan: &backupcontract.Plan{
			Revision: 1, Enabled: true,
			Store:             backupcontract.StoreConfig{Kind: backupcontract.StoreKindFile},
			RetentionCount:    7,
			WorkersPerNode:    1,
			MaxDurationMillis: (12 * time.Hour).Milliseconds(),
		},
		ActiveBackup: &backupcontract.BackupJob{
			ID: jobID, Trigger: backupcontract.TriggerManual,
			Status: backupcontract.JobStatusPublishing, PlanRevision: 1,
			StartedAtUnixMillis: now.Add(-time.Hour).UnixMilli(),
			DeadlineUnixMillis:  now.Add(time.Hour).UnixMilli(),
			UpdatedUnixMillis:   now.UnixMilli(), Slots: slots,
		},
	}}
	scheduled, err := backupusecase.NewScheduledService(backupusecase.ScheduledOptions{
		StateStore: store, Now: func() time.Time { return now },
		NewID: func() string { return "unused" },
	})
	if err != nil {
		t.Fatalf("NewScheduledService(): %v", err)
	}
	return store, scheduled
}

type retryingSlotExecutor struct {
	err error
}

func (*retryingSlotExecutor) Authority(
	context.Context,
	uint16,
) (backupusecase.SlotAuthority, error) {
	return backupusecase.SlotAuthority{NodeID: 1, Term: 9}, nil
}

func (e *retryingSlotExecutor) ExportSlot(
	_ context.Context,
	_ backupcontract.Plan,
	_ string,
	hashSlot uint16,
	attempt uint32,
	_ backupusecase.SlotAuthority,
) (backupusecase.SlotExportResult, error) {
	if e.err != nil {
		return backupusecase.SlotExportResult{}, e.err
	}
	return backupusecase.SlotExportResult{
		ManifestKey: fmt.Sprintf(
			"slots/%03d/attempts/%08d/manifest.json", hashSlot, attempt,
		),
		ManifestSHA256: strings.Repeat("f", 64),
		LogicalBytes:   10, StoredBytes: 8, Records: 1,
	}, nil
}
