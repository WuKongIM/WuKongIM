package backup_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	backupusecase "github.com/WuKongIM/WuKongIM/internal/usecase/backup"
)

func TestScheduledServiceEnablesPlanAndAdmitsInitialFullBackupAtomically(t *testing.T) {
	store := &memoryScheduledStateStore{}
	now := time.Date(2026, 7, 29, 1, 0, 0, 0, time.UTC)
	service, err := backupusecase.NewScheduledService(backupusecase.ScheduledOptions{
		StateStore: store,
		Now:        func() time.Time { return now },
		NewID:      func() string { return "bk_20260729_010000_01" },
	})
	if err != nil {
		t.Fatalf("NewScheduledService(): %v", err)
	}

	result, err := service.Configure(context.Background(), backupusecase.ConfigureRequest{
		ExpectedRevision: 0,
		Enabled:          true,
		Store: backupcontract.StoreConfig{
			Kind: backupcontract.StoreKindFile,
		},
		Cron:            "0 1 * * *",
		TimeZone:        "Asia/Shanghai",
		RetentionCount:  7,
		RateBytesPerSec: 50 << 20,
		WorkersPerNode:  1,
		MaxDuration:     12 * time.Hour,
	})
	if err != nil {
		t.Fatalf("Configure(): %v", err)
	}
	if !result.Plan.Enabled || result.Plan.Revision != 1 {
		t.Fatalf("plan = %#v", result.Plan)
	}
	if result.InitialJob == nil ||
		result.InitialJob.ID != "bk_20260729_010000_01" ||
		result.InitialJob.Trigger != backupcontract.TriggerInitial ||
		result.InitialJob.Status != backupcontract.JobStatusPreparing ||
		len(result.InitialJob.Slots) != backupcontract.HashSlotCount {
		t.Fatalf("initial job = %#v", result.InitialJob)
	}
	if result.InitialJob.DeadlineUnixMillis != now.Add(12*time.Hour).UnixMilli() {
		t.Fatalf("deadline = %d", result.InitialJob.DeadlineUnixMillis)
	}

	state, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	if state.Revision != 1 || state.Plan == nil || state.ActiveBackup == nil {
		t.Fatalf("stored state = %#v", state)
	}

	_, err = service.StartBackup(context.Background(), backupusecase.StartBackupRequest{
		Trigger: backupcontract.TriggerManual,
	})
	if !errors.Is(err, backupusecase.ErrBackupJobActive) {
		t.Fatalf("StartBackup() error = %v", err)
	}
}

func TestScheduledServiceFencesResumedSlotAttempt(t *testing.T) {
	store := &memoryScheduledStateStore{}
	now := time.Date(2026, 7, 29, 1, 0, 0, 0, time.UTC)
	service, err := backupusecase.NewScheduledService(backupusecase.ScheduledOptions{
		StateStore: store,
		Now:        func() time.Time { return now },
		NewID:      func() string { return "bk-resume" },
	})
	if err != nil {
		t.Fatalf("NewScheduledService(): %v", err)
	}
	configured, err := service.Configure(context.Background(), validConfigureRequest())
	if err != nil {
		t.Fatalf("Configure(): %v", err)
	}

	first, err := service.ClaimSlot(context.Background(), backupusecase.ClaimSlotRequest{
		JobID: configured.InitialJob.ID, HashSlot: 7, OwnerNodeID: 1, OwnerTerm: 3,
	})
	if err != nil {
		t.Fatalf("ClaimSlot(first): %v", err)
	}
	second, err := service.ClaimSlot(context.Background(), backupusecase.ClaimSlotRequest{
		JobID: configured.InitialJob.ID, HashSlot: 7, OwnerNodeID: 2, OwnerTerm: 4,
	})
	if err != nil {
		t.Fatalf("ClaimSlot(second): %v", err)
	}
	if first.Attempt != 1 || second.Attempt != 2 {
		t.Fatalf("attempts = %d, %d", first.Attempt, second.Attempt)
	}

	err = service.CompleteSlot(context.Background(), backupusecase.CompleteSlotRequest{
		JobID: configured.InitialJob.ID, HashSlot: 7, Attempt: first.Attempt,
		OwnerNodeID: 1, OwnerTerm: 3,
		ManifestKey:    "slots/007/attempts/00000001/manifest.json",
		ManifestSHA256: strings.Repeat("a", 64),
		LogicalBytes:   10, StoredBytes: 5, Records: 2,
	})
	if !errors.Is(err, backupusecase.ErrStateConflict) {
		t.Fatalf("CompleteSlot(stale) error = %v", err)
	}
	err = service.CompleteSlot(context.Background(), backupusecase.CompleteSlotRequest{
		JobID: configured.InitialJob.ID, HashSlot: 7, Attempt: second.Attempt,
		OwnerNodeID: 2, OwnerTerm: 4,
		ManifestKey:    "slots/007/attempts/00000002/manifest.json",
		ManifestSHA256: strings.Repeat("b", 64),
		LogicalBytes:   10, StoredBytes: 5, Records: 2,
	})
	if err != nil {
		t.Fatalf("CompleteSlot(current): %v", err)
	}
	state, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	slot := state.ActiveBackup.Slots[7]
	if slot.Status != backupcontract.SlotStatusComplete ||
		state.ActiveBackup.LogicalBytes != 10 ||
		state.ActiveBackup.StoredBytes != 5 ||
		state.ActiveBackup.Records != 2 {
		t.Fatalf("completed state = %#v", state.ActiveBackup)
	}
}

func TestScheduledServiceCancellationFinishesIntoBoundedHistory(t *testing.T) {
	store := &memoryScheduledStateStore{}
	now := time.Date(2026, 7, 29, 1, 0, 0, 0, time.UTC)
	service, err := backupusecase.NewScheduledService(backupusecase.ScheduledOptions{
		StateStore: store,
		Now:        func() time.Time { return now },
		NewID:      func() string { return "bk-cancel" },
	})
	if err != nil {
		t.Fatalf("NewScheduledService(): %v", err)
	}
	configured, err := service.Configure(context.Background(), validConfigureRequest())
	if err != nil {
		t.Fatalf("Configure(): %v", err)
	}
	if err := service.RequestBackupCancellation(
		context.Background(), configured.InitialJob.ID,
	); err != nil {
		t.Fatalf("RequestBackupCancellation(): %v", err)
	}
	now = now.Add(time.Minute)
	if err := service.FinishBackup(context.Background(), backupusecase.FinishBackupRequest{
		JobID:  configured.InitialJob.ID,
		Status: backupcontract.JobStatusCanceled,
	}); err != nil {
		t.Fatalf("FinishBackup(): %v", err)
	}

	state, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	if state.ActiveBackup != nil || len(state.History) != 1 ||
		state.History[0].Status != string(backupcontract.JobStatusCanceled) ||
		state.History[0].CompletedUnixMillis != now.UnixMilli() {
		t.Fatalf("terminal state = %#v", state)
	}
}

func TestScheduledServiceRecordsOverlappingCronOccurrenceAsSkipped(t *testing.T) {
	store := &memoryScheduledStateStore{}
	now := time.Date(2026, 7, 29, 0, 30, 0, 0, time.UTC)
	service, err := backupusecase.NewScheduledService(backupusecase.ScheduledOptions{
		StateStore: store,
		Now:        func() time.Time { return now },
		NewID:      func() string { return "backup-initial" },
	})
	if err != nil {
		t.Fatalf("NewScheduledService(): %v", err)
	}
	request := validConfigureRequest()
	request.TimeZone = "UTC"
	if _, err := service.Configure(
		context.Background(), request,
	); err != nil {
		t.Fatalf("Configure(): %v", err)
	}
	now = time.Date(2026, 7, 29, 1, 0, 30, 0, time.UTC)
	result, err := service.EvaluateSchedule(context.Background(), 2*time.Minute)
	if err != nil {
		t.Fatalf("EvaluateSchedule(): %v", err)
	}
	if !result.Skipped || result.Job != nil ||
		!result.Occurrence.Equal(time.Date(2026, 7, 29, 1, 0, 0, 0, time.UTC)) {
		t.Fatalf("result = %#v", result)
	}
	state, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	if len(state.History) != 1 ||
		state.History[0].Status != string(backupcontract.JobStatusSkipped) ||
		state.Plan.ScheduleCursorUnixMillis != result.Occurrence.UnixMilli() {
		t.Fatalf("state = %#v", state)
	}
}

func TestScheduledServiceAcceptsTwelveHourIntervalSchedule(t *testing.T) {
	store := &memoryScheduledStateStore{}
	now := time.Date(2026, 7, 29, 1, 0, 0, 0, time.UTC)
	service, err := backupusecase.NewScheduledService(backupusecase.ScheduledOptions{
		StateStore: store,
		Now:        func() time.Time { return now },
		NewID:      func() string { return "backup-interval" },
	})
	if err != nil {
		t.Fatalf("NewScheduledService(): %v", err)
	}
	request := validConfigureRequest()
	request.Cron = "@every 12h"
	request.TimeZone = "UTC"
	if _, err := service.Configure(context.Background(), request); err != nil {
		t.Fatalf("Configure(@every 12h): %v", err)
	}
}

func TestScheduledServiceAcceptsCloudObjectStores(t *testing.T) {
	testCases := []struct {
		name     string
		kind     backupcontract.StoreKind
		endpoint string
		region   string
		bucket   string
	}{
		{
			name: "Alibaba OSS", kind: backupcontract.StoreKindOSS,
			endpoint: "https://oss-cn-hangzhou.aliyuncs.com",
			region:   "cn-hangzhou", bucket: "wukongim-backups",
		},
		{
			name: "Tencent COS", kind: backupcontract.StoreKindCOS,
			endpoint: "https://cos.ap-shanghai.myqcloud.com",
			region:   "ap-shanghai", bucket: "wukongim-backups-1250000000",
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			service, err := backupusecase.NewScheduledService(
				backupusecase.ScheduledOptions{
					StateStore: &memoryScheduledStateStore{},
					Now: func() time.Time {
						return time.Date(2026, 7, 29, 1, 0, 0, 0, time.UTC)
					},
					NewID: func() string { return "backup-cloud" },
				},
			)
			if err != nil {
				t.Fatalf("NewScheduledService(): %v", err)
			}
			request := validConfigureRequest()
			request.Store = backupcontract.StoreConfig{
				Kind: testCase.kind, Endpoint: testCase.endpoint,
				Region: testCase.region, Bucket: testCase.bucket,
				Prefix:               "cluster-a",
				CredentialCiphertext: []byte("sealed-credential"),
			}
			if _, err := service.Configure(
				context.Background(), request,
			); err != nil {
				t.Fatalf("Configure(): %v", err)
			}
			request.ExpectedRevision = 1
			request.Store.PathStyle = true
			if _, err := service.Configure(
				context.Background(), request,
			); !errors.Is(err, backupusecase.ErrInvalidRequest) {
				t.Fatalf("Configure(path style) error = %v", err)
			}
		})
	}
}

func TestScheduledServiceSerializesArchiveOperations(t *testing.T) {
	store := &memoryScheduledStateStore{}
	now := time.Date(2026, 7, 29, 1, 0, 0, 0, time.UTC)
	nextID := 0
	service, err := backupusecase.NewScheduledService(backupusecase.ScheduledOptions{
		StateStore: store,
		Now:        func() time.Time { return now },
		NewID: func() string {
			nextID++
			return fmt.Sprintf("archive-operation-%d", nextID)
		},
	})
	if err != nil {
		t.Fatalf("NewScheduledService(): %v", err)
	}

	first, err := service.AcquireArchiveOperation(
		context.Background(), "verify", "backup-1",
	)
	if err != nil {
		t.Fatalf("AcquireArchiveOperation(first): %v", err)
	}
	if _, err := service.AcquireArchiveOperation(
		context.Background(), "delete", "backup-2",
	); !errors.Is(err, backupusecase.ErrArchiveOperationActive) {
		t.Fatalf("AcquireArchiveOperation(concurrent) error = %v", err)
	}
	if err := service.ReleaseArchiveOperation(
		context.Background(), first.Token,
	); err != nil {
		t.Fatalf("ReleaseArchiveOperation(): %v", err)
	}
	if _, err := service.AcquireArchiveOperation(
		context.Background(), "delete", "backup-2",
	); err != nil {
		t.Fatalf("AcquireArchiveOperation(after release): %v", err)
	}
}

func TestScheduledServiceRejectsPlanReplacementWhileBackupIsActive(t *testing.T) {
	store := &memoryScheduledStateStore{}
	now := time.Date(2026, 7, 29, 1, 0, 0, 0, time.UTC)
	service, err := backupusecase.NewScheduledService(backupusecase.ScheduledOptions{
		StateStore: store,
		Now:        func() time.Time { return now },
		NewID:      func() string { return "backup-active-plan" },
	})
	if err != nil {
		t.Fatalf("NewScheduledService(): %v", err)
	}
	if _, err := service.Configure(
		context.Background(), validConfigureRequest(),
	); err != nil {
		t.Fatalf("Configure(initial): %v", err)
	}
	replacement := validConfigureRequest()
	replacement.ExpectedRevision = 1
	replacement.Cron = "0 13 * * *"
	if _, err := service.Configure(
		context.Background(), replacement,
	); !errors.Is(err, backupusecase.ErrBackupJobActive) {
		t.Fatalf("Configure(active) error = %v", err)
	}
}

func validConfigureRequest() backupusecase.ConfigureRequest {
	return backupusecase.ConfigureRequest{
		ExpectedRevision: 0,
		Enabled:          true,
		Store:            backupcontract.StoreConfig{Kind: backupcontract.StoreKindFile},
		Cron:             "0 1 * * *",
		TimeZone:         "Asia/Shanghai",
		RetentionCount:   7,
		RateBytesPerSec:  50 << 20,
		WorkersPerNode:   1,
		MaxDuration:      12 * time.Hour,
	}
}

type memoryScheduledStateStore struct {
	mu    sync.Mutex
	state backupcontract.SystemState
}

func (s *memoryScheduledStateStore) Load(context.Context) (backupcontract.SystemState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state.Clone(), nil
}

func (s *memoryScheduledStateStore) CompareAndSwap(
	_ context.Context,
	expectedRevision uint64,
	next backupcontract.SystemState,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state.Revision != expectedRevision {
		return backupusecase.ErrStateConflict
	}
	s.state = next.Clone()
	return nil
}
