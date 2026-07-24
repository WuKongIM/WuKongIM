package backup

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
)

func TestCoordinatorDoctorFailureDoesNotFailStartOrDispatch(t *testing.T) {
	app := &fakeCoordinatorApp{state: backupcontract.State{Active: coordinatorTestJob()}}
	dispatcher := &fakePartitionDispatcher{}
	coordinator := newTestCoordinator(t, app, fakeCoordinatorDoctor{err: errors.New("object lock disabled")}, dispatcher)
	if err := coordinator.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer coordinator.Stop(context.Background())
	waitCoordinator(t, func() bool { return coordinator.Status().DoctorHealth == backupcontract.HealthFailed })
	if calls := dispatcher.callCount(); calls != 0 {
		t.Fatalf("dispatch calls = %d, want 0", calls)
	}
}

func TestCoordinatorLeaderResumesMissingPartitionsAndPublishes(t *testing.T) {
	app := &fakeCoordinatorApp{state: backupcontract.State{Active: coordinatorTestJob()}}
	dispatcher := &fakePartitionDispatcher{}
	coordinator := newTestCoordinator(t, app, fakeCoordinatorDoctor{}, dispatcher)
	if err := coordinator.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer coordinator.Stop(context.Background())
	waitCoordinator(t, func() bool {
		app.mu.Lock()
		defer app.mu.Unlock()
		return app.publishCalls == 1
	})
	if calls := dispatcher.callCount(); calls != 2 {
		t.Fatalf("dispatch calls = %d, want 2", calls)
	}
	if status := coordinator.Status(); status.LastSuccessAtUnixMillis == 0 || status.LastFailureCategory != "" {
		t.Fatalf("status = %#v", status)
	}
}

func TestCoordinatorReconcilesRetentionAndCompletesDurableGarbage(t *testing.T) {
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	app := &fakeCoordinatorApp{state: backupcontract.State{
		RestorePoints:  []backupcontract.RestorePoint{{ID: "retained", EffectiveAtUnixMillis: now.UnixMilli(), CreatedAtUnixMillis: now.UnixMilli()}},
		PendingGarbage: []backupcontract.RestorePoint{{ID: "expired"}},
	}}
	collector := &fakeRetentionGarbageCollector{result: backupcontract.GarbageCollectionResult{CompletedRestorePointIDs: []string{"expired"}}}
	coordinator, err := NewCoordinator(CoordinatorOptions{
		App: app, Doctor: fakeCoordinatorDoctor{}, Leadership: fakeCoordinatorLeadership{local: 1, leader: 1}, Partitions: &fakePartitionDispatcher{},
		ConfigFingerprint: fmt.Sprintf("%064x", 1), MaxParallel: 1,
		Policy:         backupcontract.SchedulePolicy{RestorePointInterval: time.Hour, SyntheticFullInterval: 24 * time.Hour, MaterializedFullInterval: 30 * 24 * time.Hour},
		DecideSchedule: decideTestSchedule,
		Now:            func() time.Time { return now }, GarbageCollector: collector,
	})
	if err != nil {
		t.Fatalf("NewCoordinator() error = %v", err)
	}
	state, err := coordinator.reconcileRetention(context.Background(), app.state.Clone())
	if err != nil {
		t.Fatalf("reconcileRetention() error = %v", err)
	}
	if len(state.PendingGarbage) != 0 || collector.calls != 1 || app.retentionCalls != 1 || len(app.completedGarbage) != 1 || app.completedGarbage[0] != "expired" {
		t.Fatalf("retention state=%#v collector=%#v app=%#v", state, collector, app)
	}
}

func TestCoordinatorAuditsNewestRestorePointAtBoundedDailyCadence(t *testing.T) {
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	app := &fakeCoordinatorApp{}
	coordinator := newTestCoordinator(t, app, fakeCoordinatorDoctor{}, &fakePartitionDispatcher{})
	coordinator.options.Now = func() time.Time { return now }
	state := backupcontract.State{RestorePoints: []backupcontract.RestorePoint{
		{ID: "older", EffectiveAtUnixMillis: now.Add(-time.Hour).UnixMilli(), CreatedAtUnixMillis: now.Add(-time.Hour).UnixMilli()},
		{ID: "newer", EffectiveAtUnixMillis: now.Add(-time.Minute).UnixMilli(), CreatedAtUnixMillis: now.Add(-time.Minute).UnixMilli()},
	}}
	if err := coordinator.auditLatestRestorePoint(context.Background(), state); err != nil {
		t.Fatalf("auditLatestRestorePoint() error = %v", err)
	}
	if err := coordinator.auditLatestRestorePoint(context.Background(), state); err != nil {
		t.Fatalf("second auditLatestRestorePoint() error = %v", err)
	}
	if len(app.verifiedRestorePoints) != 1 || app.verifiedRestorePoints[0] != "newer" {
		t.Fatalf("verified restore points = %v, want [newer]", app.verifiedRestorePoints)
	}
	if age := coordinator.verificationAgeSeconds(); age != nil {
		t.Fatalf("verification age = %v, want unknown before durable task completion", age)
	}
}

func TestCoordinatorResumesDurableVerificationBeforeOtherBackupWork(t *testing.T) {
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	app := &fakeCoordinatorApp{state: backupcontract.State{
		Verification: &backupcontract.VerificationTask{
			ID: "verification-1", RestorePointID: "restore-1",
			VerificationEvidence: backupcontract.VerificationEvidence{
				Status: backupcontract.VerificationTaskPending, StartedAtUnixMillis: now.Add(-time.Second).UnixMilli(),
			},
		},
		RestorePoints: []backupcontract.RestorePoint{{ID: "restore-1"}},
	}}
	dispatcher := &fakePartitionDispatcher{}
	coordinator := newTestCoordinator(t, app, fakeCoordinatorDoctor{}, dispatcher)
	coordinator.options.Now = func() time.Time { return now }

	if err := coordinator.runOnce(context.Background()); err != nil {
		t.Fatalf("runOnce() error = %v", err)
	}
	if app.runVerificationCalls != 1 || dispatcher.callCount() != 0 || app.retentionCalls != 0 {
		t.Fatalf("verification calls=%d dispatch=%d retention=%d", app.runVerificationCalls, dispatcher.callCount(), app.retentionCalls)
	}
	if age := coordinator.verificationAgeSeconds(); age == nil || *age != 0 {
		t.Fatalf("verification age = %v, want 0", age)
	}
}

func newTestCoordinator(t *testing.T, app CoordinatorApp, doctor CoordinatorDoctor, dispatcher PartitionDispatcher) *Coordinator {
	t.Helper()
	coordinator, err := NewCoordinator(CoordinatorOptions{
		App: app, Doctor: doctor, Leadership: fakeCoordinatorLeadership{local: 1, leader: 1}, Partitions: dispatcher,
		ConfigFingerprint: fmt.Sprintf("%064x", 1), MaxParallel: 2, TickInterval: time.Millisecond, DoctorRetry: time.Hour,
		Policy:         backupcontract.SchedulePolicy{RestorePointInterval: time.Hour, SyntheticFullInterval: 24 * time.Hour, MaterializedFullInterval: 30 * 24 * time.Hour},
		DecideSchedule: decideTestSchedule,
		Now:            time.Now,
	})
	if err != nil {
		t.Fatalf("NewCoordinator() error = %v", err)
	}
	return coordinator
}

func decideTestSchedule(_ time.Time, state backupcontract.State, _ backupcontract.SchedulePolicy) (backupcontract.ScheduleDecision, error) {
	if state.Active != nil {
		return backupcontract.ScheduleDecision{Reason: "job_active"}, nil
	}
	return backupcontract.ScheduleDecision{Reason: "not_due"}, nil
}

func coordinatorTestJob() *backupcontract.Job {
	return &backupcontract.Job{ID: "job-resume", Epoch: 1, Kind: backupartifact.RestorePointMaterializedFull, Status: backupcontract.JobStatusCapturing, HashSlotCount: 2, ConfigFingerprint: fmt.Sprintf("%064x", 1)}
}

func waitCoordinator(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("coordinator condition timed out")
}

type fakeCoordinatorDoctor struct{ err error }

func (f fakeCoordinatorDoctor) Check(context.Context) (backupcontract.DoctorReport, error) {
	health := backupcontract.HealthHealthy
	if f.err != nil {
		health = backupcontract.HealthFailed
	}
	return backupcontract.DoctorReport{
		Primary: health, Secondary: health, KMS: health, Staging: health, UTC: health,
		CheckedAtUnixMillis: time.Now().UnixMilli(),
	}, f.err
}

type fakeCoordinatorLeadership struct{ local, leader uint64 }

func (f fakeCoordinatorLeadership) NodeID() uint64                   { return f.local }
func (f fakeCoordinatorLeadership) BackupControllerLeaderID() uint64 { return f.leader }

type fakePartitionDispatcher struct {
	mu    sync.Mutex
	calls int
}

func (f *fakePartitionDispatcher) Dispatch(_ context.Context, request CaptureRequest) (backupcontract.PartitionReport, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	return backupcontract.PartitionReport{JobID: request.JobID, BackupEpoch: request.BackupEpoch, HashSlot: request.HashSlot, RaftIndex: uint64(request.HashSlot) + 1, CommittedAtUnixMillis: time.Now().UnixMilli(), ManifestKey: fmt.Sprintf("partition-manifests/%s/%05d.json", request.JobID, request.HashSlot), ManifestSHA256: fmt.Sprintf("%064x", request.HashSlot+1), ObjectCount: 1, CiphertextBytes: 1}, nil
}
func (f *fakePartitionDispatcher) callCount() int { f.mu.Lock(); defer f.mu.Unlock(); return f.calls }

type fakeCoordinatorApp struct {
	mu                    sync.Mutex
	state                 backupcontract.State
	publishCalls          int
	retentionCalls        int
	completedGarbage      []string
	verifiedRestorePoints []string
	runVerificationCalls  int
}

func (f *fakeCoordinatorApp) CoordinationState(context.Context) (backupcontract.State, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.state.Clone(), nil
}
func (f *fakeCoordinatorApp) Trigger(_ context.Context, request backupcontract.TriggerRequest) (backupcontract.Job, error) {
	return backupcontract.Job{}, backupcontract.ErrJobActive
}
func (f *fakeCoordinatorApp) ReportPartition(_ context.Context, report backupcontract.PartitionReport) (backupcontract.Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.state.Active.Partitions = append(f.state.Active.Partitions, report)
	return *f.state.Active, nil
}
func (f *fakeCoordinatorApp) Publish(context.Context, string, uint64) (backupcontract.RestorePoint, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.publishCalls++
	job := f.state.Active
	point := backupcontract.RestorePoint{ID: "restore-job-resume", JobID: job.ID, BackupEpoch: job.Epoch, Kind: job.Kind, EffectiveAtUnixMillis: time.Now().UnixMilli(), CreatedAtUnixMillis: time.Now().UnixMilli(), PrimaryVerified: true, SecondaryVerified: true}
	f.state.RestorePoints = append(f.state.RestorePoints, point)
	f.state.Active = nil
	return point, nil
}
func (f *fakeCoordinatorApp) Degrade(context.Context, string, uint64, string) error { return nil }
func (f *fakeCoordinatorApp) ApplyRetention(context.Context, backupcontract.RetentionPolicy) (backupcontract.RetentionDecision, error) {
	f.mu.Lock()
	f.retentionCalls++
	f.mu.Unlock()
	return backupcontract.RetentionDecision{}, nil
}
func (f *fakeCoordinatorApp) CompleteGarbage(_ context.Context, restorePointID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.completedGarbage = append(f.completedGarbage, restorePointID)
	for index, point := range f.state.PendingGarbage {
		if point.ID == restorePointID {
			f.state.PendingGarbage = append(f.state.PendingGarbage[:index], f.state.PendingGarbage[index+1:]...)
			break
		}
	}
	return nil
}
func (f *fakeCoordinatorApp) StartVerification(_ context.Context, restorePointID string) (backupcontract.VerificationTask, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.verifiedRestorePoints = append(f.verifiedRestorePoints, restorePointID)
	task := backupcontract.VerificationTask{
		ID: "verification-scheduled", RestorePointID: restorePointID,
		VerificationEvidence: backupcontract.VerificationEvidence{
			Status: backupcontract.VerificationTaskPending, StartedAtUnixMillis: time.Now().UnixMilli(),
		},
	}
	f.state.Verification = &task
	return task, nil
}
func (f *fakeCoordinatorApp) RunVerification(_ context.Context, taskID string) (backupcontract.VerificationTask, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.runVerificationCalls++
	if f.state.Verification == nil || f.state.Verification.ID != taskID {
		return backupcontract.VerificationTask{}, errors.New("verification task missing")
	}
	task := *f.state.Verification
	task.Status = backupcontract.VerificationTaskSucceeded
	task.CompletedAtUnixMillis = time.Now().UnixMilli()
	task.PrimaryVerified = true
	task.SecondaryVerified = true
	task.ManifestSHA256 = fmt.Sprintf("%064x", 1)
	f.state.Verification = &task
	return task, nil
}

type fakeRetentionGarbageCollector struct {
	calls  int
	result backupcontract.GarbageCollectionResult
	err    error
}

func (f *fakeRetentionGarbageCollector) Collect(context.Context, []backupcontract.RestorePoint, []backupcontract.RestorePoint, *backupcontract.Job, *backupcontract.ErasureLedgerRecordReference) (backupcontract.GarbageCollectionResult, error) {
	f.calls++
	return f.result, f.err
}
