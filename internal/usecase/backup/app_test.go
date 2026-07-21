package backup_test

import (
	"context"
	"errors"
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
