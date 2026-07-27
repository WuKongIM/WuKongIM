package backup

import (
	"context"
	"errors"
	"testing"
	"time"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
)

func TestContinuousCoordinatorPublishUsesTypedDoctorGate(t *testing.T) {
	coordinator, err := NewContinuousCoordinator(
		ContinuousCoordinatorOptions{
			Capture:            idleContinuousCapture{},
			Checkpoints:        recordingContinuousCheckpointPublisher{},
			LatestCheckpoint:   staticCheckpointObservationSource{},
			Doctor:             fakeCoordinatorDoctor{},
			Leadership:         fakeCoordinatorLeadership{local: 1, leader: 1},
			CheckpointInterval: time.Minute,
		},
	)
	if err != nil {
		t.Fatalf("NewContinuousCoordinator() error = %v", err)
	}
	if _, err := coordinator.PublishCheckpoint(
		context.Background(),
	); !errors.Is(err, ErrContinuousDoctorUnhealthy) {
		t.Fatalf(
			"PublishCheckpoint() error = %v, want ErrContinuousDoctorUnhealthy",
			err,
		)
	}
}

func TestContinuousCoordinatorRefreshesCheckpointAgeOnEveryTick(t *testing.T) {
	now := time.UnixMilli(1_800_000_060_000)
	observer := &recordingRuntimeObserver{}
	coordinator, err := NewContinuousCoordinator(
		ContinuousCoordinatorOptions{
			Capture:     idleContinuousCapture{},
			Checkpoints: recordingContinuousCheckpointPublisher{},
			LatestCheckpoint: staticCheckpointObservationSource{
				observation: CheckpointObservation{
					EffectiveAtUnixMillis: now.Add(-45 * time.Second).UnixMilli(),
					CreatedAtUnixMillis:   now.Add(-30 * time.Second).UnixMilli(),
				},
				found: true,
			},
			Doctor:             fakeCoordinatorDoctor{err: errors.New("offline")},
			Leadership:         fakeCoordinatorLeadership{local: 1, leader: 2},
			CheckpointInterval: time.Minute,
			Now:                func() time.Time { return now },
			Observer:           observer,
		},
	)
	if err != nil {
		t.Fatalf("NewContinuousCoordinator() error = %v", err)
	}
	coordinator.runOnce(context.Background())
	if observer.checkpointAge == nil ||
		*observer.checkpointAge != 45 {
		t.Fatalf(
			"checkpoint age = %v, want 45",
			observer.checkpointAge,
		)
	}
	now = now.Add(15 * time.Second)
	coordinator.runOnce(context.Background())
	if observer.checkpointAge == nil ||
		*observer.checkpointAge != 60 {
		t.Fatalf(
			"checkpoint age = %v, want 60",
			observer.checkpointAge,
		)
	}
}

func TestContinuousCoordinatorLeaderTransitionKeepsDurableCadence(t *testing.T) {
	now := time.UnixMilli(1_800_000_060_000)
	publisher := &countingContinuousCheckpointPublisher{}
	leadership := &mutableCoordinatorLeadership{local: 1, leader: 2}
	coordinator, err := NewContinuousCoordinator(
		ContinuousCoordinatorOptions{
			Capture:     idleContinuousCapture{},
			Checkpoints: publisher,
			LatestCheckpoint: staticCheckpointObservationSource{
				observation: CheckpointObservation{
					EffectiveAtUnixMillis: now.Add(-20 * time.Second).UnixMilli(),
					CreatedAtUnixMillis:   now.Add(-10 * time.Second).UnixMilli(),
				},
				found: true,
			},
			Doctor:             fakeCoordinatorDoctor{},
			Leadership:         leadership,
			CheckpointInterval: time.Minute,
			Now:                func() time.Time { return now },
		},
	)
	if err != nil {
		t.Fatalf("NewContinuousCoordinator() error = %v", err)
	}
	coordinator.runOnce(context.Background())
	leadership.leader = 1
	coordinator.runOnce(context.Background())
	if publisher.calls != 0 {
		t.Fatalf("checkpoint publications = %d, want 0 before durable cadence", publisher.calls)
	}
	now = now.Add(51 * time.Second)
	coordinator.runOnce(context.Background())
	if publisher.calls != 1 {
		t.Fatalf("checkpoint publications = %d, want 1 after durable cadence", publisher.calls)
	}
}

func TestContinuousCoordinatorMaintenanceSuccessClearsMatchingFailure(t *testing.T) {
	maintenance := &scriptedControllerMaintenance{
		errs: []error{errors.New("transient audit failure"), nil},
	}
	coordinator, err := NewContinuousCoordinator(
		ContinuousCoordinatorOptions{
			Capture:            idleContinuousCapture{},
			Checkpoints:        recordingContinuousCheckpointPublisher{},
			LatestCheckpoint:   staticCheckpointObservationSource{},
			Doctor:             fakeCoordinatorDoctor{},
			Leadership:         fakeCoordinatorLeadership{local: 1, leader: 1},
			CheckpointInterval: time.Minute,
		},
	)
	if err != nil {
		t.Fatalf("NewContinuousCoordinator() error = %v", err)
	}

	next := time.Time{}
	now := time.UnixMilli(1_800_000_060_000)
	coordinator.runMaintenance(
		context.Background(), now, maintenance, time.Second,
		"integrity audit", "audit", &next,
	)
	if got := coordinator.Status().LastFailureCategory; got != "audit" {
		t.Fatalf("failure category after failed audit = %q, want audit", got)
	}
	coordinator.runMaintenance(
		context.Background(), now.Add(time.Second), maintenance, time.Second,
		"integrity audit", "audit", &next,
	)
	if got := coordinator.Status().LastFailureCategory; got != "" {
		t.Fatalf("failure category after successful audit = %q, want empty", got)
	}
}

func TestContinuousCoordinatorMaintenanceFailureUsesBoundedRetry(t *testing.T) {
	coordinator, err := NewContinuousCoordinator(
		ContinuousCoordinatorOptions{
			Capture:            idleContinuousCapture{},
			Checkpoints:        recordingContinuousCheckpointPublisher{},
			LatestCheckpoint:   staticCheckpointObservationSource{},
			Doctor:             fakeCoordinatorDoctor{},
			Leadership:         fakeCoordinatorLeadership{local: 1, leader: 1},
			CheckpointInterval: time.Minute,
			DoctorRetry:        5 * time.Second,
		},
	)
	if err != nil {
		t.Fatalf("NewContinuousCoordinator() error = %v", err)
	}
	now := time.UnixMilli(1_800_000_060_000)
	next := time.Time{}
	coordinator.runMaintenance(
		context.Background(), now,
		&scriptedControllerMaintenance{
			errs: []error{errors.New("transient garbage failure")},
		},
		time.Hour, "Generation garbage collection", "gc", &next,
	)
	if want := now.Add(5 * time.Second); next != want {
		t.Fatalf("next maintenance = %v, want bounded retry %v", next, want)
	}
	if got := coordinator.Status().LastFailureCategory; got != "gc" {
		t.Fatalf("failure category = %q, want gc", got)
	}
}

func TestContinuousCoordinatorCheckpointSuccessKeepsSameTickAuditFailure(t *testing.T) {
	now := time.UnixMilli(1_800_000_060_000)
	publisher := &countingContinuousCheckpointPublisher{}
	coordinator, err := NewContinuousCoordinator(
		ContinuousCoordinatorOptions{
			Capture:            idleContinuousCapture{},
			Checkpoints:        publisher,
			LatestCheckpoint:   staticCheckpointObservationSource{},
			Doctor:             fakeCoordinatorDoctor{},
			Leadership:         fakeCoordinatorLeadership{local: 1, leader: 1},
			CheckpointInterval: time.Minute,
			Auditor: &scriptedControllerMaintenance{
				errs: []error{errors.New("transient audit failure")},
			},
			AuditInterval: time.Second,
			Now:           func() time.Time { return now },
		},
	)
	if err != nil {
		t.Fatalf("NewContinuousCoordinator() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	coordinator.runOnce(ctx)

	if publisher.calls != 1 {
		t.Fatalf("checkpoint publications = %d, want 1", publisher.calls)
	}
	if got := coordinator.Status().LastFailureCategory; got != "audit" {
		t.Fatalf(
			"failure category after same-tick audit failure and checkpoint success = %q, want audit",
			got,
		)
	}
}

func TestContinuousCoordinatorMaintenanceNoopDoesNotClearFailure(t *testing.T) {
	coordinator := newTestContinuousCoordinator(t, fakeCoordinatorLeadership{
		local: 1, leader: 1,
	})
	coordinator.mu.Lock()
	coordinator.recordFailureLocked(
		"audit", coordinatorFailureAuditMaintenance,
	)
	coordinator.mu.Unlock()

	next := time.Time{}
	coordinator.runMaintenance(
		context.Background(), time.UnixMilli(1_800_000_060_000),
		controllerMaintenanceFunc(func(
			context.Context,
			CoordinatorLeadership,
		) (bool, error) {
			return false, nil
		}),
		time.Second, "integrity audit", "audit", &next,
	)
	if got := coordinator.Status().LastFailureCategory; got != "audit" {
		t.Fatalf("failure category after no-op audit = %q, want audit", got)
	}
}

func TestContinuousCoordinatorMaintenanceSuccessKeepsNewerMatchingFailure(t *testing.T) {
	coordinator := newTestContinuousCoordinator(t, fakeCoordinatorLeadership{
		local: 1, leader: 1,
	})
	next := time.Time{}
	coordinator.runMaintenance(
		context.Background(), time.UnixMilli(1_800_000_060_000),
		controllerMaintenanceFunc(func(
			context.Context,
			CoordinatorLeadership,
		) (bool, error) {
			coordinator.recordContinuousFailure(
				"audit", coordinatorFailureAuditMaintenance,
			)
			return true, nil
		}),
		time.Second, "integrity audit", "audit", &next,
	)
	if got := coordinator.Status().LastFailureCategory; got != "audit" {
		t.Fatalf(
			"failure category after newer matching audit failure = %q, want audit",
			got,
		)
	}
}

func TestContinuousCoordinatorLeaderLossRetiresLeaderOwnedFailure(t *testing.T) {
	leadership := &mutableCoordinatorLeadership{local: 1, leader: 1}
	coordinator := newTestContinuousCoordinator(t, leadership)
	coordinator.mu.Lock()
	coordinator.status.ControllerLeader = true
	coordinator.recordFailureLocked(
		"audit", coordinatorFailureAuditMaintenance,
	)
	coordinator.mu.Unlock()
	leadership.leader = 2
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	coordinator.runOnce(ctx)

	if got := coordinator.Status().LastFailureCategory; got != "" {
		t.Fatalf(
			"failure category after Controller leadership loss = %q, want empty",
			got,
		)
	}
}

func TestContinuousCoordinatorStopWaitsForCaptureAndProjection(t *testing.T) {
	capture := newBlockingContinuousChild()
	projection := newBlockingContinuousChild()
	coordinator, err := NewContinuousCoordinator(
		ContinuousCoordinatorOptions{
			Capture:            capture,
			Checkpoints:        recordingContinuousCheckpointPublisher{},
			LatestCheckpoint:   staticCheckpointObservationSource{},
			Doctor:             fakeCoordinatorDoctor{},
			Leadership:         fakeCoordinatorLeadership{local: 1, leader: 2},
			CheckpointInterval: time.Minute,
			TickInterval:       time.Hour,
			Projection:         projection,
		},
	)
	if err != nil {
		t.Fatalf("NewContinuousCoordinator() error = %v", err)
	}
	if err := coordinator.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	waitContinuousChildSignal(t, capture.started, "capture start")
	waitContinuousChildSignal(t, projection.started, "projection start")

	stopResult := make(chan error, 1)
	go func() {
		stopResult <- coordinator.Stop(context.Background())
	}()
	waitContinuousChildSignal(t, capture.canceled, "capture cancellation")
	waitContinuousChildSignal(t, projection.canceled, "projection cancellation")
	select {
	case err := <-stopResult:
		t.Fatalf("Stop() returned before children exited: %v", err)
	case <-time.After(25 * time.Millisecond):
	}

	close(capture.release)
	select {
	case err := <-stopResult:
		t.Fatalf("Stop() returned before projection exited: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
	close(projection.release)
	select {
	case err := <-stopResult:
		if err != nil {
			t.Fatalf("Stop() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Stop() did not return after both children exited")
	}
}

type idleContinuousCapture struct{}

func (idleContinuousCapture) Run(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}

func (idleContinuousCapture) Status() []backupcontract.SlotCaptureStatus {
	return nil
}

type blockingContinuousChild struct {
	started  chan struct{}
	canceled chan struct{}
	release  chan struct{}
}

func newBlockingContinuousChild() *blockingContinuousChild {
	return &blockingContinuousChild{
		started:  make(chan struct{}),
		canceled: make(chan struct{}),
		release:  make(chan struct{}),
	}
}

func (c *blockingContinuousChild) Run(ctx context.Context) error {
	close(c.started)
	<-ctx.Done()
	close(c.canceled)
	<-c.release
	return ctx.Err()
}

func (*blockingContinuousChild) Status() []backupcontract.SlotCaptureStatus {
	return nil
}

func waitContinuousChildSignal(
	t *testing.T,
	signal <-chan struct{},
	name string,
) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", name)
	}
}

type recordingContinuousCheckpointPublisher struct{}

func (recordingContinuousCheckpointPublisher) Publish(
	context.Context,
) (backupartifact.CheckpointCatalogCommit, error) {
	return backupartifact.CheckpointCatalogCommit{}, nil
}

type countingContinuousCheckpointPublisher struct {
	calls int
}

func (p *countingContinuousCheckpointPublisher) Publish(
	context.Context,
) (backupartifact.CheckpointCatalogCommit, error) {
	p.calls++
	now := time.Now().UTC().UnixMilli()
	return backupartifact.CheckpointCatalogCommit{
		Checkpoint: backupartifact.CatalogCheckpointReference{
			EffectiveAtUnixMillis: now, CreatedAtUnixMillis: now,
		},
	}, nil
}

type scriptedControllerMaintenance struct {
	errs []error
}

func (m *scriptedControllerMaintenance) RunIfLeader(
	context.Context,
	CoordinatorLeadership,
) (bool, error) {
	if len(m.errs) == 0 {
		return true, nil
	}
	err := m.errs[0]
	m.errs = m.errs[1:]
	return true, err
}

type controllerMaintenanceFunc func(
	context.Context,
	CoordinatorLeadership,
) (bool, error)

func (f controllerMaintenanceFunc) RunIfLeader(
	ctx context.Context,
	leadership CoordinatorLeadership,
) (bool, error) {
	return f(ctx, leadership)
}

func newTestContinuousCoordinator(
	t *testing.T,
	leadership CoordinatorLeadership,
) *ContinuousCoordinator {
	t.Helper()
	coordinator, err := NewContinuousCoordinator(
		ContinuousCoordinatorOptions{
			Capture:            idleContinuousCapture{},
			Checkpoints:        recordingContinuousCheckpointPublisher{},
			LatestCheckpoint:   staticCheckpointObservationSource{},
			Doctor:             fakeCoordinatorDoctor{},
			Leadership:         leadership,
			CheckpointInterval: time.Minute,
		},
	)
	if err != nil {
		t.Fatalf("NewContinuousCoordinator() error = %v", err)
	}
	return coordinator
}

type staticCheckpointObservationSource struct {
	observation CheckpointObservation
	found       bool
	err         error
}

func (s staticCheckpointObservationSource) LatestCheckpoint(
	context.Context,
) (CheckpointObservation, bool, error) {
	return s.observation, s.found, s.err
}

type fakeCoordinatorDoctor struct {
	err error
}

func (f fakeCoordinatorDoctor) Check(
	context.Context,
) (backupcontract.DoctorReport, error) {
	health := backupcontract.HealthHealthy
	if f.err != nil {
		health = backupcontract.HealthFailed
	}
	return backupcontract.DoctorReport{
		Primary: health, Secondary: health, KeyAuthority: health,
		Staging: health, UTC: health,
		CheckedAtUnixMillis: time.Now().UTC().UnixMilli(),
		FailureCategory: func() string {
			if f.err != nil {
				return "doctor"
			}
			return ""
		}(),
	}, f.err
}

type fakeCoordinatorLeadership struct {
	local  uint64
	leader uint64
}

type mutableCoordinatorLeadership struct {
	local  uint64
	leader uint64
}

func (f *mutableCoordinatorLeadership) NodeID() uint64 {
	return f.local
}

func (f *mutableCoordinatorLeadership) BackupControllerLeaderID() uint64 {
	return f.leader
}

type recordingRuntimeObserver struct {
	checkpointAge *int64
}

func (*recordingRuntimeObserver) SetBackupControllerLeader(bool) {}
func (*recordingRuntimeObserver) SetBackupDoctorHealth(string)   {}
func (o *recordingRuntimeObserver) SetBackupCheckpointAgeSeconds(age *int64) {
	if age == nil {
		o.checkpointAge = nil
		return
	}
	value := *age
	o.checkpointAge = &value
}
func (*recordingRuntimeObserver) ObserveBackupFailure(string)            {}
func (*recordingRuntimeObserver) SetBackupRestoreProgress(int, int, int) {}

func (f fakeCoordinatorLeadership) NodeID() uint64 {
	return f.local
}

func (f fakeCoordinatorLeadership) BackupControllerLeaderID() uint64 {
	return f.leader
}
