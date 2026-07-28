package backup_test

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	backupinfra "github.com/WuKongIM/WuKongIM/internal/infra/backup"
	backupusecase "github.com/WuKongIM/WuKongIM/internal/usecase/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
)

func TestJobRunnerExportsEveryHashSlotThenPublishesAndFinishes(t *testing.T) {
	stateStore := &memoryScheduledStateStore{}
	now := time.Date(2026, 7, 29, 1, 0, 0, 0, time.UTC)
	scheduled, err := backupusecase.NewScheduledService(backupusecase.ScheduledOptions{
		StateStore: stateStore,
		Now:        func() time.Time { return now },
		NewID:      func() string { return "backup-runner" },
	})
	if err != nil {
		t.Fatalf("NewScheduledService(): %v", err)
	}
	if _, err := scheduled.Configure(
		context.Background(), validConfigureRequest(),
	); err != nil {
		t.Fatalf("Configure(): %v", err)
	}
	store, err := backupinfra.NewFileArchiveStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileArchiveStore(): %v", err)
	}
	slots := &recordingSlotExecutor{}
	finalizer := &recordingArchiveFinalizer{}
	runner, err := backupusecase.NewJobRunner(backupusecase.JobRunnerOptions{
		Scheduled: scheduled,
		Repository: fixedRepositoryProvider{
			store: store,
		},
		Slots:     slots,
		Finalizer: finalizer,
		Now:       func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewJobRunner(): %v", err)
	}

	for index := 0; index < backupcontract.HashSlotCount+3; index++ {
		advanced, err := runner.RunOnce(context.Background())
		if err != nil {
			t.Fatalf("RunOnce(%d): %v", index, err)
		}
		if !advanced {
			t.Fatalf("RunOnce(%d) advanced = false", index)
		}
	}

	state, err := scheduled.State(context.Background())
	if err != nil {
		t.Fatalf("State(): %v", err)
	}
	if state.ActiveBackup != nil {
		t.Fatalf("active backup = %#v", state.ActiveBackup)
	}
	if len(state.History) != 1 ||
		state.History[0].Status != string(backupcontract.JobStatusSucceeded) {
		t.Fatalf("history = %#v", state.History)
	}
	if len(slots.exported) != backupcontract.HashSlotCount {
		t.Fatalf("exported Slots = %d", len(slots.exported))
	}
	for hashSlot, exported := range slots.exported {
		if exported != uint16(hashSlot) {
			t.Fatalf("exported[%d] = %d", hashSlot, exported)
		}
	}
	if finalizer.published != 1 || finalizer.retentionCount != 7 {
		t.Fatalf(
			"finalizer published=%d retention=%d",
			finalizer.published, finalizer.retentionCount,
		)
	}
}

func TestJobRunnerHonorsWorkersPerNodeAcrossDataNodes(t *testing.T) {
	stateStore := &memoryScheduledStateStore{}
	now := time.Date(2026, 7, 29, 1, 0, 0, 0, time.UTC)
	scheduled, err := backupusecase.NewScheduledService(backupusecase.ScheduledOptions{
		StateStore: stateStore,
		Now:        func() time.Time { return now },
		NewID:      func() string { return "backup-concurrent" },
	})
	if err != nil {
		t.Fatalf("NewScheduledService(): %v", err)
	}
	request := validConfigureRequest()
	request.WorkersPerNode = 2
	if _, err := scheduled.Configure(context.Background(), request); err != nil {
		t.Fatalf("Configure(): %v", err)
	}
	store, err := backupinfra.NewFileArchiveStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileArchiveStore(): %v", err)
	}
	slots := newConcurrentSlotExecutor(4)
	runner, err := backupusecase.NewJobRunner(backupusecase.JobRunnerOptions{
		Scheduled:  scheduled,
		Repository: fixedRepositoryProvider{store: store},
		Slots:      slots,
		Finalizer:  &recordingArchiveFinalizer{},
		Now:        func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewJobRunner(): %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	advanced, err := runner.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce(): %v", err)
	}
	if !advanced {
		t.Fatal("RunOnce() advanced = false")
	}
	if slots.maxActive != 4 {
		t.Fatalf("max concurrent exports = %d, want 4", slots.maxActive)
	}
	for nodeID, maximum := range slots.maxActiveByNode {
		if maximum != 2 {
			t.Fatalf("node %d max concurrent exports = %d, want 2", nodeID, maximum)
		}
	}
	state, err := scheduled.State(context.Background())
	if err != nil {
		t.Fatalf("State(): %v", err)
	}
	completed := 0
	for _, slot := range state.ActiveBackup.Slots {
		if slot.Status == backupcontract.SlotStatusComplete {
			completed++
		}
	}
	if completed != 4 {
		t.Fatalf("completed Slots = %d, want 4", completed)
	}
}

func TestJobRunnerStopsAfterThreeSlotAttemptsAndCleansPartialObjects(t *testing.T) {
	stateStore := &memoryScheduledStateStore{}
	now := time.Date(2026, 7, 29, 1, 0, 0, 0, time.UTC)
	scheduled, err := backupusecase.NewScheduledService(backupusecase.ScheduledOptions{
		StateStore: stateStore,
		Now:        func() time.Time { return now },
		NewID:      func() string { return "backup-retry" },
	})
	if err != nil {
		t.Fatalf("NewScheduledService(): %v", err)
	}
	if _, err := scheduled.Configure(
		context.Background(), validConfigureRequest(),
	); err != nil {
		t.Fatalf("Configure(): %v", err)
	}
	stateStore.mu.Lock()
	stateStore.state.ActiveBackup.Slots[7].Status = backupcontract.SlotStatusFailed
	stateStore.state.ActiveBackup.Slots[7].Attempt = 3
	stateStore.mu.Unlock()

	store, err := backupinfra.NewFileArchiveStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileArchiveStore(): %v", err)
	}
	body := []byte("partial")
	if err := store.Put(context.Background(), backupartifact.PutObject{
		Key:  "backups/backup-retry/slots/007/partial",
		Body: bytes.NewReader(body), ExpectedBytes: uint64(len(body)),
	}); err != nil {
		t.Fatalf("Put(partial): %v", err)
	}
	runner, err := backupusecase.NewJobRunner(backupusecase.JobRunnerOptions{
		Scheduled:  scheduled,
		Repository: fixedRepositoryProvider{store: store},
		Slots:      &recordingSlotExecutor{},
		Finalizer:  &recordingArchiveFinalizer{},
		Now:        func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewJobRunner(): %v", err)
	}
	if advanced, err := runner.RunOnce(context.Background()); err != nil || !advanced {
		t.Fatalf("RunOnce() advanced=%v error=%v", advanced, err)
	}
	state, err := scheduled.State(context.Background())
	if err != nil {
		t.Fatalf("State(): %v", err)
	}
	if state.ActiveBackup != nil || len(state.History) != 1 ||
		state.History[0].Status != string(backupcontract.JobStatusFailed) ||
		state.History[0].ErrorCode != "slot_retry_exhausted" {
		t.Fatalf("terminal state = %#v", state)
	}
	objects, err := store.List(context.Background(), "backups/backup-retry")
	if err != nil {
		t.Fatalf("List(): %v", err)
	}
	if len(objects) != 0 {
		t.Fatalf("partial objects remain: %#v", objects)
	}
}

type fixedRepositoryProvider struct {
	store backupartifact.ArchiveStore
}

func (p fixedRepositoryProvider) Open(
	context.Context,
	backupcontract.StoreConfig,
) (backupartifact.ArchiveStore, error) {
	return p.store, nil
}

type recordingSlotExecutor struct {
	exported []uint16
}

func (e *recordingSlotExecutor) Authority(
	context.Context,
	uint16,
) (backupusecase.SlotAuthority, error) {
	return backupusecase.SlotAuthority{NodeID: 1, Term: 9}, nil
}

type concurrentSlotExecutor struct {
	mu              sync.Mutex
	active          int
	maxActive       int
	activeByNode    map[uint64]int
	maxActiveByNode map[uint64]int
	release         chan struct{}
	releaseOnce     sync.Once
	target          int
}

func newConcurrentSlotExecutor(target int) *concurrentSlotExecutor {
	return &concurrentSlotExecutor{
		activeByNode:    make(map[uint64]int),
		maxActiveByNode: make(map[uint64]int),
		release:         make(chan struct{}),
		target:          target,
	}
}

func (e *concurrentSlotExecutor) Authority(
	_ context.Context,
	hashSlot uint16,
) (backupusecase.SlotAuthority, error) {
	return backupusecase.SlotAuthority{
		NodeID: uint64(hashSlot%2) + 1,
		Term:   9,
	}, nil
}

func (e *concurrentSlotExecutor) ExportSlot(
	ctx context.Context,
	_ backupcontract.Plan,
	_ string,
	hashSlot uint16,
	attempt uint32,
	authority backupusecase.SlotAuthority,
) (backupusecase.SlotExportResult, error) {
	e.mu.Lock()
	e.active++
	e.activeByNode[authority.NodeID]++
	if e.active > e.maxActive {
		e.maxActive = e.active
	}
	if e.activeByNode[authority.NodeID] > e.maxActiveByNode[authority.NodeID] {
		e.maxActiveByNode[authority.NodeID] = e.activeByNode[authority.NodeID]
	}
	if e.active == e.target {
		e.releaseOnce.Do(func() { close(e.release) })
	}
	e.mu.Unlock()

	select {
	case <-ctx.Done():
		return backupusecase.SlotExportResult{}, ctx.Err()
	case <-e.release:
	}

	e.mu.Lock()
	e.active--
	e.activeByNode[authority.NodeID]--
	e.mu.Unlock()
	return backupusecase.SlotExportResult{
		ManifestKey: fmt.Sprintf(
			"slots/%03d/attempts/%08d/manifest.json", hashSlot, attempt,
		),
		ManifestSHA256: strings.Repeat("a", 64),
		LogicalBytes:   uint64(hashSlot) + 1,
		StoredBytes:    uint64(hashSlot) + 1,
		Records:        1,
	}, nil
}

func (e *recordingSlotExecutor) ExportSlot(
	_ context.Context,
	_ backupcontract.Plan,
	_ string,
	hashSlot uint16,
	attempt uint32,
	_ backupusecase.SlotAuthority,
) (backupusecase.SlotExportResult, error) {
	e.exported = append(e.exported, hashSlot)
	return backupusecase.SlotExportResult{
		ManifestKey: fmt.Sprintf(
			"slots/%03d/attempts/%08d/manifest.json", hashSlot, attempt,
		),
		ManifestSHA256: strings.Repeat("a", 64),
		LogicalBytes:   10,
		StoredBytes:    8,
		Records:        1,
	}, nil
}

type recordingArchiveFinalizer struct {
	published      int
	retentionCount int
}

func (f *recordingArchiveFinalizer) Publish(
	context.Context,
	backupartifact.ArchiveStore,
	backupcontract.BackupJob,
) error {
	f.published++
	return nil
}

func (f *recordingArchiveFinalizer) ApplyRetention(
	_ context.Context,
	_ backupartifact.ArchiveStore,
	retentionCount int,
) error {
	f.retentionCount = retentionCount
	return nil
}
