package backup_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	backupusecase "github.com/WuKongIM/WuKongIM/internal/usecase/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
)

func TestPublishRejectsIncompleteLogicalPartitionSet(t *testing.T) {
	t.Parallel()

	store := &memoryStateStore{}
	publisher := &recordingPublisher{}
	app, err := backupusecase.NewApp(backupusecase.Options{
		Enabled:       true,
		HashSlotCount: 2,
		Store:         store,
		Publisher:     publisher,
		Now:           func() time.Time { return time.UnixMilli(1_753_056_360_000).UTC() },
		NewJobID:      func() string { return "backup-job-1" },
	})
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	job, err := app.Trigger(context.Background(), backupusecase.TriggerRequest{
		Kind:              backupartifact.RestorePointIncremental,
		ConfigFingerprint: "config-sha256",
	})
	if err != nil {
		t.Fatalf("Trigger() error = %v", err)
	}
	_, err = app.ReportPartition(context.Background(), backupusecase.PartitionReport{
		JobID:                 job.ID,
		BackupEpoch:           job.Epoch,
		HashSlot:              0,
		RaftIndex:             41,
		CommittedAtUnixMillis: 1_753_056_300_000,
		ManifestKey:           "partition-manifests/backup-job-1/00.json",
		ManifestSHA256:        "9f2f3f87937965e5ea1c1f7c15b0f38d2223d81f83a5a9d37f15e7d3f920bbc7",
		ObjectCount:           3,
		CiphertextBytes:       1024,
	})
	if err != nil {
		t.Fatalf("ReportPartition() error = %v", err)
	}

	_, err = app.Publish(context.Background(), job.ID, job.Epoch)
	if !errors.Is(err, backupusecase.ErrPartitionsIncomplete) {
		t.Fatalf("Publish() error = %v, want %v", err, backupusecase.ErrPartitionsIncomplete)
	}
	if publisher.callCount() != 0 {
		t.Fatalf("publisher calls = %d, want 0", publisher.callCount())
	}
}

func TestPublishRejectsRestorePointThatHidesOldestPartitionWatermark(t *testing.T) {
	t.Parallel()

	store := &memoryStateStore{}
	publisher := &recordingPublisher{result: backupusecase.RestorePoint{
		ID:                    "rp-newer-than-cut",
		JobID:                 "backup-job-rpo",
		BackupEpoch:           1,
		Kind:                  backupartifact.RestorePointIncremental,
		EffectiveAtUnixMillis: 1_753_056_350_000,
		CreatedAtUnixMillis:   1_753_056_360_000,
		ManifestSHA256:        "9f2f3f87937965e5ea1c1f7c15b0f38d2223d81f83a5a9d37f15e7d3f920bbc7",
		PrimaryVerified:       true,
		SecondaryVerified:     true,
	}}
	app, err := backupusecase.NewApp(backupusecase.Options{
		Enabled:       true,
		HashSlotCount: 2,
		Store:         store,
		Publisher:     publisher,
		Now:           func() time.Time { return time.UnixMilli(1_753_056_360_000).UTC() },
		NewJobID:      func() string { return "backup-job-rpo" },
	})
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	job, err := app.Trigger(context.Background(), backupusecase.TriggerRequest{Kind: backupartifact.RestorePointIncremental, ConfigFingerprint: "config-sha256"})
	if err != nil {
		t.Fatalf("Trigger() error = %v", err)
	}
	for _, report := range []backupusecase.PartitionReport{
		partitionReport(job, 0, 1_753_056_300_000),
		partitionReport(job, 1, 1_753_056_340_000),
	} {
		if _, err := app.ReportPartition(context.Background(), report); err != nil {
			t.Fatalf("ReportPartition(%d) error = %v", report.HashSlot, err)
		}
	}

	_, err = app.Publish(context.Background(), job.ID, job.Epoch)
	if !errors.Is(err, backupusecase.ErrInvalidRequest) {
		t.Fatalf("Publish() error = %v, want %v", err, backupusecase.ErrInvalidRequest)
	}
}

func TestStatusKeepsRecoveryPointAgeUnknownBeforeFirstPublish(t *testing.T) {
	t.Parallel()

	app, err := backupusecase.NewApp(backupusecase.Options{
		Enabled:       true,
		HashSlotCount: 2,
		Store:         &memoryStateStore{},
		Publisher:     &recordingPublisher{},
		Now:           func() time.Time { return time.UnixMilli(1_753_056_360_000).UTC() },
		NewJobID:      func() string { return "backup-job-status" },
	})
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}

	status, err := app.Status(context.Background())
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if status.Health != backupusecase.HealthUnknown {
		t.Fatalf("Status() health = %q, want %q", status.Health, backupusecase.HealthUnknown)
	}
	if status.RecoveryPointAgeSeconds != nil {
		t.Fatalf("Status() recovery point age = %d, want unknown", *status.RecoveryPointAgeSeconds)
	}
}

func TestListRestorePointsPageUsesStableNewestFirstCursor(t *testing.T) {
	t.Parallel()

	store := &memoryStateStore{state: backupusecase.State{RestorePoints: []backupusecase.RestorePoint{
		{ID: "rp-oldest", EffectiveAtUnixMillis: 100, CreatedAtUnixMillis: 200},
		{ID: "rp-newest-b", EffectiveAtUnixMillis: 300, CreatedAtUnixMillis: 500},
		{ID: "rp-newest-a", EffectiveAtUnixMillis: 300, CreatedAtUnixMillis: 400},
	}}}
	app, err := backupusecase.NewApp(backupusecase.Options{
		Enabled:       true,
		HashSlotCount: 2,
		Store:         store,
		Publisher:     &recordingPublisher{},
		Now:           time.Now,
		NewJobID:      func() string { return "backup-job-list" },
	})
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}

	first, err := app.ListRestorePointsPage(context.Background(), backupusecase.RestorePointListRequest{Limit: 2})
	if err != nil {
		t.Fatalf("ListRestorePointsPage(first) error = %v", err)
	}
	if first.Total != 3 || len(first.Items) != 2 || first.Items[0].ID != "rp-newest-b" || first.Items[1].ID != "rp-newest-a" || first.NextCursor == "" {
		t.Fatalf("ListRestorePointsPage(first) = %#v", first)
	}

	second, err := app.ListRestorePointsPage(context.Background(), backupusecase.RestorePointListRequest{
		Limit:  2,
		Cursor: first.NextCursor,
	})
	if err != nil {
		t.Fatalf("ListRestorePointsPage(second) error = %v", err)
	}
	if second.Total != 3 || len(second.Items) != 1 || second.Items[0].ID != "rp-oldest" || second.NextCursor != "" {
		t.Fatalf("ListRestorePointsPage(second) = %#v", second)
	}
}

func TestManualVerificationIsDurableAndMutuallyExclusiveWithBackup(t *testing.T) {
	t.Parallel()

	now := time.UnixMilli(1_753_056_360_000).UTC()
	store := &memoryStateStore{state: backupusecase.State{
		LastEpoch: 1,
		RestorePoints: []backupusecase.RestorePoint{{
			ID: "rp-audit", JobID: "backup-job-1", BackupEpoch: 1,
			Kind:                  backupartifact.RestorePointMaterializedFull,
			EffectiveAtUnixMillis: now.Add(-time.Minute).UnixMilli(),
			CreatedAtUnixMillis:   now.Add(-30 * time.Second).UnixMilli(),
			ManifestSHA256:        strings.Repeat("a", 64),
			PrimaryVerified:       true,
			SecondaryVerified:     true,
		}},
	}}
	app, err := backupusecase.NewApp(backupusecase.Options{
		Enabled:       true,
		HashSlotCount: 2,
		Store:         store,
		Publisher:     &recordingPublisher{},
		Verifier: &fixedVerifier{result: backupusecase.Verification{
			RestorePointID:       "rp-audit",
			VerifiedAtUnixMillis: now.Add(time.Second).UnixMilli(),
			PrimaryVerified:      true,
			SecondaryVerified:    true,
			ManifestSHA256:       strings.Repeat("a", 64),
		}},
		Now:      func() time.Time { return now },
		NewJobID: func() string { return "verification-1" },
	})
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}

	task, err := app.StartVerification(context.Background(), "rp-audit")
	if err != nil {
		t.Fatalf("StartVerification() error = %v", err)
	}
	if task.ID != "verification-1" || task.Status != backupusecase.VerificationTaskPending {
		t.Fatalf("StartVerification() = %#v", task)
	}
	if _, err := app.Trigger(context.Background(), backupusecase.TriggerRequest{
		Kind: backupartifact.RestorePointMaterializedFull, ConfigFingerprint: "config",
	}); !errors.Is(err, backupusecase.ErrVerificationJobActive) {
		t.Fatalf("Trigger() during verification error = %v, want %v", err, backupusecase.ErrVerificationJobActive)
	}

	completed, err := app.RunVerification(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("RunVerification() error = %v", err)
	}
	if completed.Status != backupusecase.VerificationTaskSucceeded || completed.CompletedAtUnixMillis != now.Add(time.Second).UnixMilli() {
		t.Fatalf("RunVerification() = %#v", completed)
	}
	state, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.Verification == nil || state.Verification.Status != backupusecase.VerificationTaskSucceeded ||
		state.RestorePoints[0].LastVerification == nil ||
		state.RestorePoints[0].LastVerification.Status != backupusecase.VerificationTaskSucceeded ||
		state.RestorePoints[0].LastVerification.ManifestSHA256 != strings.Repeat("a", 64) {
		t.Fatalf("durable verification state = %#v", state)
	}
	if _, err := app.Trigger(context.Background(), backupusecase.TriggerRequest{
		Kind: backupartifact.RestorePointMaterializedFull, ConfigFingerprint: "config",
	}); err != nil {
		t.Fatalf("Trigger() after verification error = %v", err)
	}
}

func TestErasureLedgerCommitReservationIsBoundedIdempotentAndContiguous(t *testing.T) {
	t.Parallel()

	store := &memoryStateStore{}
	app, err := backupusecase.NewApp(backupusecase.Options{
		Enabled:       true,
		HashSlotCount: 2,
		Store:         store,
		Publisher:     &recordingPublisher{},
		Now:           func() time.Time { return time.UnixMilli(1_753_056_360_000).UTC() },
		NewJobID:      func() string { return "backup-job-erasure" },
	})
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	first := backupusecase.ErasureLedgerRecordReference{
		EventID: strings.Repeat("a", 64), RecordKey: "erasure-ledger/events/0001/" + strings.Repeat("a", 64) + ".json",
		RecordSHA256: strings.Repeat("b", 64),
	}
	reserved, err := app.ReserveErasureLedgerCommit(context.Background(), first)
	if err != nil {
		t.Fatalf("ReserveErasureLedgerCommit() error = %v", err)
	}
	if reserved.Sequence != 1 || reserved.EventID != first.EventID {
		t.Fatalf("ReserveErasureLedgerCommit() = %#v, want sequence 1", reserved)
	}
	retry, err := app.ReserveErasureLedgerCommit(context.Background(), first)
	if err != nil || retry != reserved {
		t.Fatalf("ReserveErasureLedgerCommit(retry) = %#v err=%v, want idempotent %#v", retry, err, reserved)
	}
	second := backupusecase.ErasureLedgerRecordReference{
		EventID: strings.Repeat("c", 64), RecordKey: "erasure-ledger/events/0001/" + strings.Repeat("c", 64) + ".json",
		RecordSHA256: strings.Repeat("d", 64),
	}
	if _, err := app.ReserveErasureLedgerCommit(context.Background(), second); !errors.Is(err, backupusecase.ErrErasureLedgerPending) {
		t.Fatalf("ReserveErasureLedgerCommit(concurrent) error = %v, want %v", err, backupusecase.ErrErasureLedgerPending)
	}
	if err := app.CommitErasureLedgerCommit(context.Background(), reserved.Sequence, second.EventID); !errors.Is(err, backupusecase.ErrStateConflict) {
		t.Fatalf("CommitErasureLedgerCommit(mismatch) error = %v, want %v", err, backupusecase.ErrStateConflict)
	}
	if err := app.CommitErasureLedgerCommit(context.Background(), reserved.Sequence, reserved.EventID); err != nil {
		t.Fatalf("CommitErasureLedgerCommit() error = %v", err)
	}
	if err := app.CommitErasureLedgerCommit(context.Background(), reserved.Sequence, reserved.EventID); err != nil {
		t.Fatalf("CommitErasureLedgerCommit(retry) error = %v", err)
	}
	committedRetry, err := app.ReserveErasureLedgerCommit(context.Background(), first)
	if err != nil || committedRetry != reserved {
		t.Fatalf("ReserveErasureLedgerCommit(committed retry) = %#v err=%v, want %#v", committedRetry, err, reserved)
	}
	next, err := app.ReserveErasureLedgerCommit(context.Background(), second)
	if err != nil || next.Sequence != 2 {
		t.Fatalf("ReserveErasureLedgerCommit(next) = %#v err=%v, want sequence 2", next, err)
	}
	state, err := store.Load(context.Background())
	if err != nil || state.ErasureLedgerBoundary != 1 || state.PendingErasureLedger == nil || state.PendingErasureLedger.Sequence != 2 {
		t.Fatalf("state = %#v err=%v, want committed boundary 1 and pending sequence 2", state, err)
	}
}

func TestPublishResumesDegradedJobAfterRepositoryRecovers(t *testing.T) {
	t.Parallel()

	store := &memoryStateStore{}
	publisher := &sequencedPublisher{results: []publishResult{
		{err: errors.New("secondary repository unavailable")},
		{restorePoint: backupusecase.RestorePoint{
			ID:                    "rp-recovered",
			JobID:                 "backup-job-retry",
			BackupEpoch:           1,
			Kind:                  backupartifact.RestorePointIncremental,
			EffectiveAtUnixMillis: 1_753_056_300_000,
			CreatedAtUnixMillis:   1_753_056_360_000,
			ManifestSHA256:        "9f2f3f87937965e5ea1c1f7c15b0f38d2223d81f83a5a9d37f15e7d3f920bbc7",
			PrimaryVerified:       true,
			SecondaryVerified:     true,
		}},
	}}
	app, err := backupusecase.NewApp(backupusecase.Options{
		Enabled:       true,
		HashSlotCount: 2,
		Store:         store,
		Publisher:     publisher,
		Now:           func() time.Time { return time.UnixMilli(1_753_056_360_000).UTC() },
		NewJobID:      func() string { return "backup-job-retry" },
	})
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	job, err := app.Trigger(context.Background(), backupusecase.TriggerRequest{Kind: backupartifact.RestorePointIncremental, ConfigFingerprint: "config-sha256"})
	if err != nil {
		t.Fatalf("Trigger() error = %v", err)
	}
	for _, report := range []backupusecase.PartitionReport{
		partitionReport(job, 0, 1_753_056_300_000),
		partitionReport(job, 1, 1_753_056_340_000),
	} {
		if _, err := app.ReportPartition(context.Background(), report); err != nil {
			t.Fatalf("ReportPartition(%d) error = %v", report.HashSlot, err)
		}
	}
	if _, err := app.Publish(context.Background(), job.ID, job.Epoch); err == nil {
		t.Fatal("first Publish() error = nil, want repository failure")
	}

	restorePoint, err := app.Publish(context.Background(), job.ID, job.Epoch)
	if err != nil {
		t.Fatalf("second Publish() error = %v", err)
	}
	if restorePoint.ID != "rp-recovered" {
		t.Fatalf("second Publish() restore point = %q", restorePoint.ID)
	}
	status, err := app.Status(context.Background())
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if status.Active != nil || status.Latest == nil || status.Latest.ID != "rp-recovered" || status.Health != backupusecase.HealthHealthy {
		t.Fatalf("Status() = %#v, want completed healthy restore point", status)
	}
}

func TestCancelHoldReleaseAndScheduleRemainFenced(t *testing.T) {
	t.Parallel()

	now := time.UnixMilli(1_753_056_360_000).UTC()
	store := &memoryStateStore{state: backupusecase.State{RestorePoints: []backupusecase.RestorePoint{{
		ID:                    "rp-1",
		Kind:                  backupartifact.RestorePointMaterializedFull,
		EffectiveAtUnixMillis: now.Add(-10 * time.Minute).UnixMilli(),
		CreatedAtUnixMillis:   now.Add(-10 * time.Minute).UnixMilli(),
	}}}}
	app, err := backupusecase.NewApp(backupusecase.Options{
		Enabled:       true,
		HashSlotCount: 2,
		Store:         store,
		Publisher:     &recordingPublisher{},
		Now:           func() time.Time { return now },
		NewJobID:      func() string { return "backup-job-controls" },
	})
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	held, err := app.Hold(context.Background(), "rp-1")
	if err != nil || !held.Held {
		t.Fatalf("Hold() = %+v err=%v", held, err)
	}
	released, err := app.Release(context.Background(), "rp-1")
	if err != nil || released.Held {
		t.Fatalf("Release() = %+v err=%v", released, err)
	}
	job, err := app.Trigger(context.Background(), backupusecase.TriggerRequest{Kind: backupartifact.RestorePointIncremental, ConfigFingerprint: "config-sha256"})
	if err != nil {
		t.Fatalf("Trigger() error = %v", err)
	}
	if _, err := app.Cancel(context.Background(), job.ID, job.Epoch+1); !errors.Is(err, backupusecase.ErrJobNotFound) {
		t.Fatalf("Cancel(stale) error = %v", err)
	}
	canceled, err := app.Cancel(context.Background(), job.ID, job.Epoch)
	if err != nil || canceled.Status != backupusecase.JobStatusCanceled {
		t.Fatalf("Cancel() = %+v err=%v", canceled, err)
	}
	state, err := store.Load(context.Background())
	if err != nil || state.Active != nil {
		t.Fatalf("state after cancel = %+v err=%v", state, err)
	}

	decision, err := backupusecase.DecideSchedule(now, state, backupusecase.SchedulePolicy{
		RestorePointInterval:     5 * time.Minute,
		SyntheticFullInterval:    24 * time.Hour,
		MaterializedFullInterval: 30 * 24 * time.Hour,
	})
	if err != nil || !decision.Due || decision.Kind != backupartifact.RestorePointIncremental {
		t.Fatalf("DecideSchedule() = %+v err=%v", decision, err)
	}
}

func TestTriggerUsesPriorRestorePointOnlyForIncrementalCapture(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		kind     backupartifact.RestorePointKind
		wantBase bool
	}{
		{name: "incremental", kind: backupartifact.RestorePointIncremental, wantBase: true},
		{name: "materialized full", kind: backupartifact.RestorePointMaterializedFull},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			store := &memoryStateStore{state: backupusecase.State{RestorePoints: []backupusecase.RestorePoint{{
				ID: "restore-base", EffectiveAtUnixMillis: 1, CreatedAtUnixMillis: 1,
			}}}}
			app, err := backupusecase.NewApp(backupusecase.Options{
				Enabled: true, HashSlotCount: 1, Store: store, Publisher: &recordingPublisher{},
				Now: time.Now, NewJobID: func() string { return "job-baseline" },
			})
			if err != nil {
				t.Fatalf("NewApp() error = %v", err)
			}
			job, err := app.Trigger(context.Background(), backupusecase.TriggerRequest{Kind: testCase.kind, ConfigFingerprint: "config"})
			if err != nil {
				t.Fatalf("Trigger() error = %v", err)
			}
			if got := job.BaseRestorePointID != ""; got != testCase.wantBase {
				t.Fatalf("BaseRestorePointID = %q, want base=%v", job.BaseRestorePointID, testCase.wantBase)
			}
		})
	}
}

func TestTriggerRejectsUnqualifiedSyntheticFull(t *testing.T) {
	app, err := backupusecase.NewApp(backupusecase.Options{
		Enabled: true, HashSlotCount: 1, Store: &memoryStateStore{}, Publisher: &recordingPublisher{},
		Now: time.Now, NewJobID: func() string { return "job-synthetic" },
	})
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	_, err = app.Trigger(context.Background(), backupusecase.TriggerRequest{
		Kind: backupartifact.RestorePointSyntheticFull, ConfigFingerprint: "config",
	})
	if !errors.Is(err, backupusecase.ErrInvalidRequest) {
		t.Fatalf("Trigger(synthetic_full) error = %v, want ErrInvalidRequest", err)
	}
}

func TestDecideScheduleUsesMaterializedFallbackForIndependentFullCadence(t *testing.T) {
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	decision, err := backupusecase.DecideSchedule(now, backupusecase.State{RestorePoints: []backupusecase.RestorePoint{
		{ID: "full", Kind: backupartifact.RestorePointMaterializedFull, CreatedAtUnixMillis: now.Add(-48 * time.Hour).UnixMilli(), EffectiveAtUnixMillis: now.Add(-48 * time.Hour).UnixMilli()},
		{ID: "increment", Kind: backupartifact.RestorePointIncremental, CreatedAtUnixMillis: now.Add(-10 * time.Minute).UnixMilli(), EffectiveAtUnixMillis: now.Add(-10 * time.Minute).UnixMilli()},
	}}, backupusecase.SchedulePolicy{
		RestorePointInterval: 5 * time.Minute, SyntheticFullInterval: 24 * time.Hour,
		MaterializedFullInterval: 30 * 24 * time.Hour,
	})
	if err != nil || !decision.Due || decision.Kind != backupartifact.RestorePointMaterializedFull || decision.Reason != "independent_full_fallback_due" {
		t.Fatalf("DecideSchedule() = %+v err=%v", decision, err)
	}
}

func partitionReport(job backupusecase.Job, hashSlot uint16, committedAt int64) backupusecase.PartitionReport {
	return backupusecase.PartitionReport{
		JobID:                 job.ID,
		BackupEpoch:           job.Epoch,
		HashSlot:              hashSlot,
		RaftIndex:             uint64(40 + hashSlot),
		CommittedAtUnixMillis: committedAt,
		ManifestKey:           "partition-manifests/backup-job/part.json",
		ManifestSHA256:        "9f2f3f87937965e5ea1c1f7c15b0f38d2223d81f83a5a9d37f15e7d3f920bbc7",
		ObjectCount:           3,
		CiphertextBytes:       1024,
	}
}

type memoryStateStore struct {
	mu    sync.Mutex
	state backupusecase.State
}

func (s *memoryStateStore) Load(_ context.Context) (backupusecase.State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state.Clone(), nil
}

func (s *memoryStateStore) CompareAndSwap(_ context.Context, revision uint64, next backupusecase.State) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state.Revision != revision {
		return backupusecase.ErrStateConflict
	}
	next.Revision = revision + 1
	s.state = next.Clone()
	return nil
}

type recordingPublisher struct {
	mu     sync.Mutex
	calls  int
	result backupusecase.RestorePoint
	err    error
}

func (p *recordingPublisher) Publish(_ context.Context, _ backupusecase.Job) (backupusecase.RestorePoint, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	return p.result, p.err
}

func (p *recordingPublisher) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

type publishResult struct {
	restorePoint backupusecase.RestorePoint
	err          error
}

type sequencedPublisher struct {
	mu      sync.Mutex
	results []publishResult
}

type fixedVerifier struct {
	result backupusecase.Verification
	err    error
}

func (v *fixedVerifier) Verify(context.Context, string) (backupusecase.Verification, error) {
	return v.result, v.err
}

func (p *sequencedPublisher) Publish(_ context.Context, _ backupusecase.Job) (backupusecase.RestorePoint, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.results) == 0 {
		return backupusecase.RestorePoint{}, errors.New("unexpected publish call")
	}
	result := p.results[0]
	p.results = p.results[1:]
	return result.restorePoint, result.err
}
