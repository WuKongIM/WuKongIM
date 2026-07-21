package backup

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
)

func TestRestoreCoordinatorOnlyLeaderResumesMissingPartitions(t *testing.T) {
	app := newFakeRestoreCoordinatorApp(4, 1)
	installer := &fakeRestoreCoordinatorInstaller{}
	leadership := &mutableRestoreLeadership{local: 1, leader: 2}
	coordinator, err := NewRestoreCoordinator(RestoreCoordinatorOptions{
		App: app, Leadership: leadership, Partitions: installer, MaxParallel: 2, TickInterval: time.Hour, Now: time.Now,
	})
	if err != nil {
		t.Fatalf("NewRestoreCoordinator() error = %v", err)
	}
	if err := coordinator.runOnce(context.Background()); err != nil {
		t.Fatalf("follower runOnce() error = %v", err)
	}
	if got := installer.calls(); len(got) != 0 {
		t.Fatalf("follower install calls = %v", got)
	}
	leadership.leader = 1
	if err := coordinator.runOnce(context.Background()); err != nil {
		t.Fatalf("leader runOnce() error = %v", err)
	}
	if got := installer.calls(); len(got) != 3 {
		t.Fatalf("leader install calls = %v, want three missing partitions", got)
	}
	plan, _ := app.Status(context.Background())
	if plan.Status != backupcontract.RestoreStatusInstalled {
		t.Fatalf("plan status = %q, want installed", plan.Status)
	}
}

func TestRestoreCoordinatorBoundsParallelInstallation(t *testing.T) {
	app := newFakeRestoreCoordinatorApp(8)
	installer := &fakeRestoreCoordinatorInstaller{block: make(chan struct{})}
	coordinator, err := NewRestoreCoordinator(RestoreCoordinatorOptions{
		App: app, Leadership: &mutableRestoreLeadership{local: 1, leader: 1}, Partitions: installer,
		MaxParallel: 3, TickInterval: time.Hour, Now: time.Now,
	})
	if err != nil {
		t.Fatalf("NewRestoreCoordinator() error = %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- coordinator.runOnce(context.Background()) }()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && installer.maxActiveCount() < 3 {
		time.Sleep(time.Millisecond)
	}
	if got := installer.maxActiveCount(); got != 3 {
		t.Fatalf("max active installs = %d, want 3", got)
	}
	close(installer.block)
	if err := <-done; err != nil {
		t.Fatalf("runOnce() error = %v", err)
	}
}

type mutableRestoreLeadership struct{ local, leader uint64 }

func (f *mutableRestoreLeadership) NodeID() uint64                   { return f.local }
func (f *mutableRestoreLeadership) BackupControllerLeaderID() uint64 { return f.leader }

type fakeRestoreCoordinatorApp struct {
	mu   sync.Mutex
	plan backupcontract.RestorePlan
}

func newFakeRestoreCoordinatorApp(hashSlotCount uint16, installed ...uint16) *fakeRestoreCoordinatorApp {
	partitions := make([]backupcontract.RestorePartition, hashSlotCount)
	for hashSlot := range partitions {
		partitions[hashSlot].HashSlot = uint16(hashSlot)
	}
	for _, hashSlot := range installed {
		partitions[hashSlot].Installed = true
		partitions[hashSlot].MetadataSHA256 = strings.Repeat("a", 64)
	}
	return &fakeRestoreCoordinatorApp{plan: backupcontract.RestorePlan{
		ID: "plan-1", HashSlotCount: hashSlotCount, Status: backupcontract.RestoreStatusInstalling, Partitions: partitions,
	}}
}

func (f *fakeRestoreCoordinatorApp) Status(context.Context) (*backupcontract.RestorePlan, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	plan := f.plan
	plan.Partitions = append([]backupcontract.RestorePartition(nil), f.plan.Partitions...)
	return &plan, nil
}

func (f *fakeRestoreCoordinatorApp) ReportPartition(_ context.Context, planID string, report backupcontract.RestorePartition) (backupcontract.RestorePlan, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if planID != f.plan.ID || f.plan.Partitions[report.HashSlot].Installed {
		return backupcontract.RestorePlan{}, backupcontract.ErrStateConflict
	}
	f.plan.Partitions[report.HashSlot] = report
	complete := true
	for _, partition := range f.plan.Partitions {
		complete = complete && partition.Installed
	}
	if complete {
		f.plan.Status = backupcontract.RestoreStatusInstalled
	}
	return f.plan, nil
}

type fakeRestoreCoordinatorInstaller struct {
	mu        sync.Mutex
	called    []uint16
	active    int
	maxActive int
	block     chan struct{}
}

func (f *fakeRestoreCoordinatorInstaller) InstallPartition(_ context.Context, _ backupcontract.RestorePlan, hashSlot uint16) (backupcontract.RestorePartition, error) {
	f.mu.Lock()
	f.called = append(f.called, hashSlot)
	f.active++
	if f.active > f.maxActive {
		f.maxActive = f.active
	}
	f.mu.Unlock()
	if f.block != nil {
		<-f.block
	}
	f.mu.Lock()
	f.active--
	f.mu.Unlock()
	return backupcontract.RestorePartition{HashSlot: hashSlot, Installed: true, PlainBytes: 1, MetadataSHA256: strings.Repeat("a", 64)}, nil
}

func (f *fakeRestoreCoordinatorInstaller) calls() []uint16 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]uint16(nil), f.called...)
}

func (f *fakeRestoreCoordinatorInstaller) maxActiveCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.maxActive
}
