package backup

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	backupusecase "github.com/WuKongIM/WuKongIM/internal/usecase/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
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

func TestRestoreCoordinatorClearsThroughputOutsideInstallingState(
	t *testing.T,
) {
	app := newFakeRestoreCoordinatorApp(1, 0)
	observer := &recordingRestoreThroughputObserver{}
	coordinator, err := NewRestoreCoordinator(RestoreCoordinatorOptions{
		App: app,
		Leadership: &mutableRestoreLeadership{
			local: 1, leader: 1,
		},
		Partitions:  &fakeRestoreCoordinatorInstaller{},
		MaxParallel: 1, TickInterval: time.Hour, Now: time.Now,
		Observer: observer,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := coordinator.runOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if observer.throughput != 321 {
		t.Fatalf("installing throughput = %d, want 321", observer.throughput)
	}
	app.mu.Lock()
	app.plan.Status = backupcontract.RestoreStatusVerified
	app.mu.Unlock()
	if err := coordinator.runOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if observer.throughput != 0 {
		t.Fatalf("verified throughput = %d, want 0", observer.throughput)
	}
}

func TestRestoreCoordinatorResumesCheckpointSlotAfterLeaderChange(t *testing.T) {
	app := newFakeCheckpointRestoreCoordinatorApp()
	installer := &fakeCheckpointRestoreCoordinatorInstaller{
		leader: 2, fail: true,
	}
	coordinator, err := NewRestoreCoordinator(RestoreCoordinatorOptions{
		App: app, Leadership: &mutableRestoreLeadership{local: 1, leader: 1},
		Partitions: installer, MaxParallel: 1, TickInterval: time.Hour,
		Now: func() time.Time {
			return time.UnixMilli(1_753_400_210_000).UTC()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := coordinator.runOnce(context.Background()); err == nil {
		t.Fatal("first runOnce() error = nil")
	}
	plan, _ := app.Status(context.Background())
	if plan.Partitions[0].Status != backupcontract.RestorePartitionInstalling ||
		plan.Partitions[0].InstallAttempt != 1 {
		t.Fatalf("first progress = %#v", plan.Partitions[0])
	}

	installer.mu.Lock()
	installer.leader = 3
	installer.fail = false
	installer.mu.Unlock()
	if err := coordinator.runOnce(context.Background()); err != nil {
		t.Fatalf("takeover runOnce() error = %v", err)
	}
	plan, _ = app.Status(context.Background())
	if plan.Status != backupcontract.RestoreStatusInstalled ||
		plan.Partitions[0].Status != backupcontract.RestorePartitionConverged ||
		plan.Partitions[0].LeaderNodeID != 3 ||
		plan.Partitions[0].InstallAttempt != 2 {
		t.Fatalf("resumed progress = %#v plan status=%q", plan.Partitions[0], plan.Status)
	}
}

func TestRestoreCoordinatorRetriesAmbiguousLeaderErrorUnderSameFence(t *testing.T) {
	app := newFakeCheckpointRestoreCoordinatorApp()
	installer := &fakeCheckpointRestoreCoordinatorInstaller{
		leader: 2, fail: true,
	}
	coordinator, err := NewRestoreCoordinator(RestoreCoordinatorOptions{
		App: app, Leadership: &mutableRestoreLeadership{local: 1, leader: 1},
		Partitions: installer, MaxParallel: 1, TickInterval: time.Hour,
		Now: time.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := coordinator.runOnce(context.Background()); err == nil {
		t.Fatal("first runOnce() error = nil")
	}
	installer.mu.Lock()
	installer.fail = false
	installer.mu.Unlock()
	if err := coordinator.runOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	plan, _ := app.Status(context.Background())
	if plan.Status != backupcontract.RestoreStatusInstalled ||
		plan.Partitions[0].InstallAttempt != 1 {
		t.Fatalf("same-fence retry progress = %#v", plan.Partitions[0])
	}
}

func TestRestoreCoordinatorPublishesPartialReplicaConvergenceOnError(
	t *testing.T,
) {
	app := newFakeCheckpointRestoreCoordinatorApp()
	installer := &fakeCheckpointRestoreCoordinatorInstaller{
		leader: 2, fail: true, partial: true,
	}
	coordinator, err := NewRestoreCoordinator(RestoreCoordinatorOptions{
		App:        app,
		Leadership: &mutableRestoreLeadership{local: 1, leader: 1},
		Partitions: installer, MaxParallel: 1, TickInterval: time.Hour,
		Now: time.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := coordinator.runOnce(context.Background()); err == nil {
		t.Fatal("runOnce() error = nil")
	}
	plan, _ := app.Status(context.Background())
	progress := plan.Partitions[0]
	if progress.Status != backupcontract.RestorePartitionConverging ||
		progress.ConvergedReplicas != 2 ||
		progress.ReplicaCount != 3 {
		t.Fatalf("partial convergence progress = %#v", progress)
	}
}

type mutableRestoreLeadership struct{ local, leader uint64 }

func (f *mutableRestoreLeadership) NodeID() uint64                   { return f.local }
func (f *mutableRestoreLeadership) BackupControllerLeaderID() uint64 { return f.leader }

type recordingRestoreThroughputObserver struct {
	throughput uint64
}

func (*recordingRestoreThroughputObserver) SetBackupControllerLeader(bool) {}
func (*recordingRestoreThroughputObserver) SetBackupDoctorHealth(string)   {}
func (*recordingRestoreThroughputObserver) SetBackupCheckpointAgeSeconds(
	*int64,
) {
}
func (*recordingRestoreThroughputObserver) ObserveBackupFailure(string) {}
func (*recordingRestoreThroughputObserver) SetBackupRestoreProgress(
	int,
	int,
	int,
) {
}
func (o *recordingRestoreThroughputObserver) SetBackupRestoreThroughput(
	value uint64,
) {
	o.throughput = value
}

type fakeRestoreCoordinatorApp struct {
	mu   sync.Mutex
	plan backupcontract.RestorePlan
}

func newFakeRestoreCoordinatorApp(hashSlotCount uint16, installed ...uint16) *fakeRestoreCoordinatorApp {
	partitions := make([]backupcontract.RestorePartition, hashSlotCount)
	for hashSlot := range partitions {
		partitions[hashSlot].HashSlot = uint16(hashSlot)
		partitions[hashSlot].Status = backupcontract.RestorePartitionPending
	}
	for _, hashSlot := range installed {
		partitions[hashSlot].Status = backupcontract.RestorePartitionConverged
		partitions[hashSlot].Installed = true
		partitions[hashSlot].ConvergedReplicas = 1
		partitions[hashSlot].ReplicaCount = 1
	}
	return &fakeRestoreCoordinatorApp{plan: backupcontract.RestorePlan{
		ID: "plan-1", HashSlotCount: hashSlotCount,
		Status:       backupcontract.RestoreStatusInstalling,
		CatalogProof: &backupartifact.CheckpointCatalogProof{},
		Partitions:   partitions,
	}}
}

func (f *fakeRestoreCoordinatorApp) Status(context.Context) (*backupcontract.RestorePlan, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	plan := f.plan
	plan.Partitions = append([]backupcontract.RestorePartition(nil), f.plan.Partitions...)
	return &plan, nil
}

func (*fakeRestoreCoordinatorApp) RestoreThroughput(
	backupcontract.RestorePlan,
) (uint64, error) {
	return 321, nil
}

func (f *fakeRestoreCoordinatorApp) BeginPartitionInstall(
	_ context.Context,
	planID string,
	assignment backupcontract.RestorePartitionAssignment,
) (backupcontract.RestorePlan, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if planID != f.plan.ID {
		return backupcontract.RestorePlan{}, backupcontract.ErrStateConflict
	}
	progress := f.plan.Partitions[assignment.HashSlot]
	progress.Status = backupcontract.RestorePartitionInstalling
	progress.TargetSlotID = assignment.TargetSlotID
	progress.LeaderNodeID = assignment.LeaderNodeID
	progress.LeaderTerm = assignment.LeaderTerm
	progress.ConfigEpoch = assignment.ConfigEpoch
	progress.ReplicaCount = assignment.ReplicaCount
	progress.InstallAttempt++
	progress.StartedAtUnixMillis = 1
	f.plan.Partitions[assignment.HashSlot] = progress
	return f.plan, nil
}

func (f *fakeRestoreCoordinatorApp) ReportPartitionProgress(
	_ context.Context,
	planID string,
	report backupcontract.RestorePartition,
) (backupcontract.RestorePlan, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if planID != f.plan.ID ||
		f.plan.Partitions[report.HashSlot].Status ==
			backupcontract.RestorePartitionConverged {
		return backupcontract.RestorePlan{}, backupcontract.ErrStateConflict
	}
	f.plan.Partitions[report.HashSlot] = report
	complete := true
	for _, partition := range f.plan.Partitions {
		complete = complete &&
			partition.Status == backupcontract.RestorePartitionConverged
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

func (f *fakeRestoreCoordinatorInstaller) Assignment(
	_ context.Context,
	_ backupcontract.RestorePlan,
	hashSlot uint16,
) (backupcontract.RestorePartitionAssignment, error) {
	return backupcontract.RestorePartitionAssignment{
		HashSlot: hashSlot, TargetSlotID: uint32(hashSlot) + 1,
		LeaderNodeID: 1, LeaderTerm: 1, ConfigEpoch: 1, ReplicaCount: 1,
	}, nil
}

func (f *fakeRestoreCoordinatorInstaller) InstallPartition(_ context.Context, plan backupcontract.RestorePlan, hashSlot uint16) (backupcontract.RestorePartition, error) {
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
	progress := plan.Partitions[hashSlot]
	progress.Status = backupcontract.RestorePartitionConverged
	progress.EvidenceVersion = backupartifact.RestoreEvidenceVersion
	progress.Installed = true
	progress.PlainBytes = 1
	progress.MetadataSHA256 = strings.Repeat("a", 64)
	progress.ContentSHA256 = strings.Repeat("b", 64)
	progress.MessageMerkleSHA256 = strings.Repeat("c", 64)
	progress.ConvergedReplicas = progress.ReplicaCount
	progress.InstalledAtUnixMillis = progress.StartedAtUnixMillis
	return progress, nil
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

type fakeCheckpointRestoreCoordinatorApp struct {
	mu   sync.Mutex
	plan backupcontract.RestorePlan
}

func newFakeCheckpointRestoreCoordinatorApp() *fakeCheckpointRestoreCoordinatorApp {
	return &fakeCheckpointRestoreCoordinatorApp{plan: backupcontract.RestorePlan{
		ID: "checkpoint-plan", HashSlotCount: 1,
		Status:       backupcontract.RestoreStatusInstalling,
		CatalogProof: &backupartifact.CheckpointCatalogProof{},
		Partitions: []backupcontract.RestorePartition{{
			HashSlot: 0, Status: backupcontract.RestorePartitionPending,
		}},
	}}
}

func (f *fakeCheckpointRestoreCoordinatorApp) Status(
	context.Context,
) (*backupcontract.RestorePlan, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	copy := f.plan
	copy.Partitions = append([]backupcontract.RestorePartition(nil), f.plan.Partitions...)
	return &copy, nil
}

func (*fakeCheckpointRestoreCoordinatorApp) RestoreThroughput(
	backupcontract.RestorePlan,
) (uint64, error) {
	return 0, nil
}

func (f *fakeCheckpointRestoreCoordinatorApp) BeginPartitionInstall(
	_ context.Context,
	_ string,
	assignment backupusecase.RestorePartitionAssignment,
) (backupcontract.RestorePlan, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	previous := f.plan.Partitions[assignment.HashSlot]
	if previous.Status == backupcontract.RestorePartitionInstalling &&
		previous.TargetSlotID == assignment.TargetSlotID &&
		previous.LeaderNodeID == assignment.LeaderNodeID &&
		previous.LeaderTerm == assignment.LeaderTerm &&
		previous.ConfigEpoch == assignment.ConfigEpoch {
		return f.plan, nil
	}
	previous.Status = backupcontract.RestorePartitionInstalling
	previous.TargetSlotID = assignment.TargetSlotID
	previous.LeaderNodeID = assignment.LeaderNodeID
	previous.LeaderTerm = assignment.LeaderTerm
	previous.ConfigEpoch = assignment.ConfigEpoch
	previous.ReplicaCount = assignment.ReplicaCount
	previous.InstallAttempt++
	previous.StartedAtUnixMillis = 1_753_400_205_000
	f.plan.Partitions[assignment.HashSlot] = previous
	return f.plan, nil
}

func (f *fakeCheckpointRestoreCoordinatorApp) ReportPartitionProgress(
	_ context.Context,
	_ string,
	report backupcontract.RestorePartition,
) (backupcontract.RestorePlan, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.plan.Partitions[report.HashSlot] = report
	if report.Status == backupcontract.RestorePartitionConverged {
		f.plan.Status = backupcontract.RestoreStatusInstalled
	}
	return f.plan, nil
}

type fakeCheckpointRestoreCoordinatorInstaller struct {
	mu      sync.Mutex
	leader  uint64
	fail    bool
	partial bool
}

func (f *fakeCheckpointRestoreCoordinatorInstaller) Assignment(
	_ context.Context,
	_ backupusecase.RestorePlan,
	hashSlot uint16,
) (backupusecase.RestorePartitionAssignment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return backupusecase.RestorePartitionAssignment{
		HashSlot: hashSlot, TargetSlotID: 7, LeaderNodeID: f.leader,
		LeaderTerm: f.leader + 7, ConfigEpoch: 4, ReplicaCount: 3,
	}, nil
}

func (f *fakeCheckpointRestoreCoordinatorInstaller) InstallPartition(
	_ context.Context,
	plan backupcontract.RestorePlan,
	hashSlot uint16,
) (backupcontract.RestorePartition, error) {
	f.mu.Lock()
	fail := f.fail
	partial := f.partial
	f.mu.Unlock()
	if fail {
		if partial {
			progress := plan.Partitions[hashSlot]
			progress.Status = backupcontract.RestorePartitionConverging
			progress.EvidenceVersion = backupartifact.RestoreEvidenceVersion
			progress.Installed = true
			progress.MetadataSHA256 = strings.Repeat("a", 64)
			progress.ContentSHA256 = strings.Repeat("a", 64)
			progress.MessageMerkleSHA256 = strings.Repeat("b", 64)
			progress.ReplicaCount = 3
			progress.ConvergedReplicas = 2
			progress.InstalledAtUnixMillis = 1_753_400_210_000
			return progress, errors.New("follower convergence failed")
		}
		return backupcontract.RestorePartition{}, errors.New("leader stopped")
	}
	progress := plan.Partitions[hashSlot]
	progress.Status = backupcontract.RestorePartitionConverged
	progress.EvidenceVersion = backupartifact.RestoreEvidenceVersion
	progress.Installed = true
	progress.MetadataSHA256 = strings.Repeat("a", 64)
	progress.ContentSHA256 = strings.Repeat("a", 64)
	progress.MessageMerkleSHA256 = strings.Repeat("b", 64)
	progress.ReplicaCount = 3
	progress.ConvergedReplicas = 3
	progress.InstalledAtUnixMillis = 1_753_400_210_000
	return progress, nil
}
