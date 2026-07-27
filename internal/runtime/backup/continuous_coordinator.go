package backup

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	backupartifact "github.com/WuKongIM/WuKongIM/pkg/backup"
)

const defaultContinuousCoordinatorTick = time.Second

type coordinatorFailureSource uint8

const (
	coordinatorFailureNone coordinatorFailureSource = iota
	coordinatorFailureCheckpointObservation
	coordinatorFailureCheckpointPublication
	coordinatorFailureAuditMaintenance
	coordinatorFailureGarbageMaintenance
	coordinatorFailureDoctor
	coordinatorFailureCapture
	coordinatorFailureProjection
)

// ContinuousCaptureRunner owns node-local Slot-leader capture workers.
type ContinuousCaptureRunner interface {
	Run(context.Context) error
	Status() []backupcontract.SlotCaptureStatus
}

// ContinuousCheckpointPublisher freezes one complete current Slot vector.
type ContinuousCheckpointPublisher interface {
	Publish(context.Context) (backupartifact.CheckpointCatalogCommit, error)
}

// ContinuousCoordinatorOptions configures the single vNext backup runtime.
type ContinuousCoordinatorOptions struct {
	// Capture runs on every node and performs work only for locally led Slots.
	Capture ContinuousCaptureRunner
	// Checkpoints publishes catalog entries only from the Controller Leader.
	Checkpoints ContinuousCheckpointPublisher
	// LatestCheckpoint hydrates checkpoint age and cadence on every node.
	LatestCheckpoint ContinuousCheckpointObservationSource
	// Doctor gates repository/key work without affecting foreground readiness.
	Doctor CoordinatorDoctor
	// Leadership identifies the current Controller Leader.
	Leadership CoordinatorLeadership
	// CheckpointInterval is the maximum target recovery-point cadence.
	CheckpointInterval time.Duration
	// TickInterval controls bounded retry and leadership observation latency.
	TickInterval time.Duration
	// DoctorRetry controls dependency qualification retries.
	DoctorRetry time.Duration
	// Auditor advances one bounded authenticated repair transition.
	Auditor ControllerMaintenance
	// AuditInterval controls bounded integrity-audit work cadence.
	AuditInterval time.Duration
	// GarbageCollector advances one bounded Generation sweep.
	GarbageCollector ControllerMaintenance
	// GarbageCollectionInterval controls destructive maintenance cadence.
	GarbageCollectionInterval time.Duration
	// Projection keeps follower capture workers aligned with durable audit state.
	Projection ContinuousProjectionRunner
	// Now may be replaced by deterministic tests.
	Now func() time.Time
	// Observer receives low-cardinality operational evidence.
	Observer RuntimeObserver
}

// ContinuousCoordinator supervises capture and complete checkpoint publication.
// It deliberately has no job, partition-dispatch, or synthetic-full state.
type ContinuousCoordinator struct {
	options ContinuousCoordinatorOptions

	mu                        sync.Mutex
	status                    CoordinatorStatus
	lastCheckpointEffectiveAt int64
	nextCheckpointAt          time.Time
	nextDoctorAt              time.Time
	nextAuditAt               time.Time
	nextGarbageCollectionAt   time.Time
	captureStarted            bool
	captureCancel             context.CancelFunc
	captureDone               chan error
	projectionStarted         bool
	projectionDone            chan error
	cancel                    context.CancelFunc
	done                      chan struct{}
	publicationSerial         sync.Mutex
	// failureRevision fences recovery from clearing a newer concurrent failure.
	failureRevision uint64
	// failureSource distinguishes equal public categories owned by different loops.
	failureSource coordinatorFailureSource
}

// NewContinuousCoordinator creates the production continuous-backup supervisor.
func NewContinuousCoordinator(options ContinuousCoordinatorOptions) (*ContinuousCoordinator, error) {
	if options.Capture == nil || options.Checkpoints == nil ||
		options.LatestCheckpoint == nil || options.Doctor == nil ||
		options.Leadership == nil || options.Leadership.NodeID() == 0 ||
		options.CheckpointInterval <= 0 {
		return nil, fmt.Errorf("%w: continuous coordinator options are incomplete", ErrInvalidCapture)
	}
	if options.TickInterval == 0 {
		options.TickInterval = defaultContinuousCoordinatorTick
	}
	if options.DoctorRetry == 0 {
		options.DoctorRetry = defaultDoctorRetryInterval
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.TickInterval <= 0 || options.DoctorRetry <= 0 {
		return nil, fmt.Errorf("%w: continuous coordinator intervals must be positive", ErrInvalidCapture)
	}
	if (options.Auditor == nil) != (options.AuditInterval == 0) ||
		(options.GarbageCollector == nil) !=
			(options.GarbageCollectionInterval == 0) ||
		options.AuditInterval < 0 ||
		options.GarbageCollectionInterval < 0 {
		return nil, fmt.Errorf("%w: continuous maintenance options are incomplete", ErrInvalidCapture)
	}
	return &ContinuousCoordinator{
		options: options,
		status: CoordinatorStatus{
			DoctorHealth: backupcontract.HealthUnknown,
			Doctor: backupcontract.DoctorReport{
				Primary: backupcontract.HealthUnknown, Secondary: backupcontract.HealthUnknown,
				KeyAuthority: backupcontract.HealthUnknown,
				Staging:      backupcontract.HealthUnknown,
				UTC:          backupcontract.HealthUnknown,
			},
		},
	}, nil
}

// Start starts dependency qualification, node-local capture, and Leader-only
// checkpoint publication. Repeated calls are idempotent.
func (c *ContinuousCoordinator) Start(ctx context.Context) error {
	if c == nil {
		return ErrInvalidCapture
	}
	c.mu.Lock()
	if c.cancel != nil {
		c.mu.Unlock()
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	runContext, cancel := context.WithCancel(ctx)
	c.cancel = cancel
	c.done = make(chan struct{})
	c.status.Running = true
	c.nextDoctorAt = time.Time{}
	c.nextCheckpointAt = time.Time{}
	c.nextAuditAt = time.Time{}
	c.nextGarbageCollectionAt = time.Time{}
	done := c.done
	c.mu.Unlock()
	go c.loop(runContext, done)
	return nil
}

// Stop cancels capture and waits for the complete supervisor to exit.
func (c *ContinuousCoordinator) Stop(ctx context.Context) error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	cancel, done := c.cancel, c.done
	c.mu.Unlock()
	if cancel == nil {
		return nil
	}
	cancel()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Status returns detached bounded node-local operational evidence.
func (c *ContinuousCoordinator) Status() CoordinatorStatus {
	if c == nil {
		return CoordinatorStatus{DoctorHealth: backupcontract.HealthUnknown}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.status
}

// CaptureStatus returns the engine's sorted bounded Slot projection.
func (c *ContinuousCoordinator) CaptureStatus() []backupcontract.SlotCaptureStatus {
	if c == nil || c.options.Capture == nil {
		return nil
	}
	return c.options.Capture.Status()
}

// PublishCheckpoint performs one explicit complete-vector publication through
// the current Controller Leader. It shares the same serialization as the
// automatic cadence.
func (c *ContinuousCoordinator) PublishCheckpoint(
	ctx context.Context,
) (backupartifact.CheckpointCatalogCommit, error) {
	if c == nil || c.options.Checkpoints == nil || c.options.Leadership == nil {
		return backupartifact.CheckpointCatalogCommit{}, ErrInvalidCapture
	}
	if c.options.Leadership.BackupControllerLeaderID() != c.options.Leadership.NodeID() {
		return backupartifact.CheckpointCatalogCommit{}, ErrCaptureNotLeader
	}
	c.mu.Lock()
	healthy := c.status.DoctorHealth == backupcontract.HealthHealthy
	c.mu.Unlock()
	if !healthy {
		return backupartifact.CheckpointCatalogCommit{},
			ErrContinuousDoctorUnhealthy
	}
	return c.publish(ctx)
}

func (c *ContinuousCoordinator) loop(ctx context.Context, done chan struct{}) {
	ticker := time.NewTicker(c.options.TickInterval)
	defer ticker.Stop()
	defer func() {
		c.stopAndWaitChildren()
		c.mu.Lock()
		c.status.Running = false
		c.cancel = nil
		c.mu.Unlock()
		close(done)
	}()
	for {
		c.startProjection(ctx)
		c.runOnce(ctx)
		select {
		case <-ctx.Done():
			return
		case err := <-c.captureDoneChannel():
			c.captureExited(err)
		case err := <-c.projectionDoneChannel():
			c.projectionExited(err)
		case <-ticker.C:
		}
	}
}

func (c *ContinuousCoordinator) startProjection(parent context.Context) {
	c.mu.Lock()
	if c.options.Projection == nil || c.projectionStarted {
		c.mu.Unlock()
		return
	}
	observedFailureRevision := c.failureRevision
	c.projectionStarted = true
	c.projectionDone = make(chan error, 1)
	done := c.projectionDone
	c.clearFailureLocked(
		observedFailureRevision, coordinatorFailureProjection,
	)
	c.mu.Unlock()
	go func() {
		done <- c.options.Projection.Run(parent)
	}()
}

func (c *ContinuousCoordinator) projectionDoneChannel() <-chan error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.projectionDone
}

func (c *ContinuousCoordinator) projectionExited(err error) {
	c.mu.Lock()
	c.projectionStarted = false
	c.projectionDone = nil
	c.mu.Unlock()
	if err != nil && !errors.Is(err, context.Canceled) {
		c.recordContinuousFailure(
			"audit", coordinatorFailureProjection,
		)
	}
}

func (c *ContinuousCoordinator) runOnce(ctx context.Context) {
	now := c.options.Now().UTC()
	leader := c.options.Leadership.BackupControllerLeaderID() == c.options.Leadership.NodeID()
	checkpointStateReady := c.refreshLatestCheckpoint(ctx)
	c.mu.Lock()
	c.retireFormerLeaderFailureLocked(leader)
	c.status.ControllerLeader = leader
	doctorDue := c.nextDoctorAt.IsZero() || !now.Before(c.nextDoctorAt)
	checkpointEffectiveAt := c.lastCheckpointEffectiveAt
	c.mu.Unlock()
	if c.options.Observer != nil {
		c.options.Observer.SetBackupControllerLeader(leader)
		var age *int64
		if checkpointEffectiveAt > 0 {
			seconds := now.Unix() - time.UnixMilli(
				checkpointEffectiveAt,
			).UTC().Unix()
			if seconds < 0 {
				seconds = 0
			}
			age = &seconds
		}
		c.options.Observer.SetBackupCheckpointAgeSeconds(age)
	}
	if doctorDue {
		c.runDoctor(ctx, now)
	}
	c.mu.Lock()
	healthy := c.status.DoctorHealth == backupcontract.HealthHealthy
	publishDue := leader && healthy && checkpointStateReady &&
		(c.nextCheckpointAt.IsZero() || !now.Before(c.nextCheckpointAt))
	auditDue := leader && healthy && c.options.Auditor != nil &&
		(c.nextAuditAt.IsZero() || !now.Before(c.nextAuditAt))
	garbageDue := leader && healthy && c.options.GarbageCollector != nil &&
		(c.nextGarbageCollectionAt.IsZero() ||
			!now.Before(c.nextGarbageCollectionAt))
	c.mu.Unlock()
	if healthy {
		c.startCapture(ctx)
	}
	if auditDue {
		c.runMaintenance(
			ctx, now, c.options.Auditor, c.options.AuditInterval,
			"integrity audit", "audit", &c.nextAuditAt,
		)
	}
	if garbageDue {
		c.runMaintenance(
			ctx, now, c.options.GarbageCollector,
			c.options.GarbageCollectionInterval,
			"Generation garbage collection", "gc",
			&c.nextGarbageCollectionAt,
		)
	}
	if !publishDue {
		return
	}
	if _, err := c.publish(ctx); err != nil {
		if !errors.Is(err, context.Canceled) {
			c.recordContinuousFailure(
				checkpointFailureCategory(err),
				coordinatorFailureCheckpointPublication,
			)
		}
		return
	}
	c.mu.Lock()
	c.nextCheckpointAt = now.Add(c.options.CheckpointInterval)
	c.mu.Unlock()
}

func (c *ContinuousCoordinator) refreshLatestCheckpoint(
	ctx context.Context,
) bool {
	c.mu.Lock()
	observedFailureRevision := c.failureRevision
	c.mu.Unlock()
	observation, found, err := c.options.LatestCheckpoint.LatestCheckpoint(ctx)
	if err != nil {
		c.recordContinuousFailure(
			"checkpoint", coordinatorFailureCheckpointObservation,
		)
		return false
	}
	if !found {
		c.mu.Lock()
		c.clearFailureLocked(
			observedFailureRevision,
			coordinatorFailureCheckpointObservation,
		)
		c.mu.Unlock()
		return true
	}
	if observation.EffectiveAtUnixMillis <= 0 ||
		observation.CreatedAtUnixMillis <
			observation.EffectiveAtUnixMillis {
		c.recordContinuousFailure(
			"checkpoint", coordinatorFailureCheckpointObservation,
		)
		return false
	}
	c.mu.Lock()
	if observation.CreatedAtUnixMillis >= c.status.LastSuccessAtUnixMillis {
		c.lastCheckpointEffectiveAt =
			observation.EffectiveAtUnixMillis
		c.status.LastSuccessAtUnixMillis =
			observation.CreatedAtUnixMillis
		c.nextCheckpointAt = time.UnixMilli(
			observation.CreatedAtUnixMillis,
		).UTC().Add(c.options.CheckpointInterval)
	}
	c.clearFailureLocked(
		observedFailureRevision,
		coordinatorFailureCheckpointObservation,
	)
	c.mu.Unlock()
	return true
}

func (c *ContinuousCoordinator) runMaintenance(
	ctx context.Context,
	now time.Time,
	runner ControllerMaintenance,
	interval time.Duration,
	_ string,
	failureCategory string,
	next *time.Time,
) {
	source := maintenanceFailureSource(failureCategory)
	c.mu.Lock()
	observedFailureRevision := c.failureRevision
	c.mu.Unlock()
	ran, err := runner.RunIfLeader(ctx, c.options.Leadership)
	stillLeader := c.options.Leadership.BackupControllerLeaderID() ==
		c.options.Leadership.NodeID()
	c.mu.Lock()
	*next = now.Add(interval)
	if err != nil && stillLeader {
		c.recordFailureLocked(failureCategory, source)
	} else if err == nil && ran {
		c.clearFailureLocked(observedFailureRevision, source)
	}
	c.mu.Unlock()
	if err != nil && failureCategory != "audit" &&
		c.options.Observer != nil {
		c.options.Observer.ObserveBackupFailure(failureCategory)
	}
}

func (c *ContinuousCoordinator) runDoctor(ctx context.Context, now time.Time) {
	c.mu.Lock()
	observedFailureRevision := c.failureRevision
	c.mu.Unlock()
	report, err := c.options.Doctor.Check(ctx)
	health := backupcontract.HealthHealthy
	if err != nil {
		health = backupcontract.HealthFailed
	}
	c.mu.Lock()
	c.status.Doctor = report
	c.status.DoctorHealth = health
	c.status.LastDoctorAtUnixMillis = report.CheckedAtUnixMillis
	c.nextDoctorAt = now.Add(c.options.DoctorRetry)
	if err != nil {
		c.recordFailureLocked(
			report.FailureCategory, coordinatorFailureDoctor,
		)
	} else {
		c.clearFailureLocked(
			observedFailureRevision, coordinatorFailureDoctor,
		)
	}
	c.mu.Unlock()
	if c.options.Observer != nil {
		c.options.Observer.SetBackupDoctorHealth(string(health))
		if err != nil {
			c.options.Observer.ObserveBackupFailure("doctor")
		}
	}
}

func (c *ContinuousCoordinator) startCapture(parent context.Context) {
	c.mu.Lock()
	if c.captureStarted {
		c.mu.Unlock()
		return
	}
	observedFailureRevision := c.failureRevision
	captureContext, cancel := context.WithCancel(parent)
	c.captureStarted = true
	c.captureCancel = cancel
	c.captureDone = make(chan error, 1)
	done := c.captureDone
	c.clearFailureLocked(
		observedFailureRevision, coordinatorFailureCapture,
	)
	c.mu.Unlock()
	go func() {
		done <- c.options.Capture.Run(captureContext)
	}()
}

func (c *ContinuousCoordinator) stopAndWaitChildren() {
	c.mu.Lock()
	captureCancel := c.captureCancel
	captureDone := c.captureDone
	projectionDone := c.projectionDone
	c.mu.Unlock()
	if captureCancel != nil {
		captureCancel()
	}
	if captureDone != nil {
		c.captureExited(<-captureDone)
	}
	if projectionDone != nil {
		c.projectionExited(<-projectionDone)
	}
}

func (c *ContinuousCoordinator) captureDoneChannel() <-chan error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.captureDone
}

func (c *ContinuousCoordinator) captureExited(err error) {
	c.mu.Lock()
	c.captureStarted = false
	c.captureCancel = nil
	c.captureDone = nil
	c.mu.Unlock()
	if err != nil && !errors.Is(err, context.Canceled) {
		c.recordContinuousFailure(
			"capture_runtime", coordinatorFailureCapture,
		)
	}
}

func (c *ContinuousCoordinator) publish(
	ctx context.Context,
) (backupartifact.CheckpointCatalogCommit, error) {
	c.publicationSerial.Lock()
	defer c.publicationSerial.Unlock()
	c.mu.Lock()
	observedFailureRevision := c.failureRevision
	c.mu.Unlock()
	commit, err := c.options.Checkpoints.Publish(ctx)
	if err != nil {
		return backupartifact.CheckpointCatalogCommit{}, err
	}
	now := c.options.Now().UTC()
	c.mu.Lock()
	c.lastCheckpointEffectiveAt =
		commit.Checkpoint.EffectiveAtUnixMillis
	c.status.LastSuccessAtUnixMillis = commit.Checkpoint.CreatedAtUnixMillis
	c.clearFailureLocked(
		observedFailureRevision,
		coordinatorFailureCheckpointPublication,
	)
	c.nextCheckpointAt = now.Add(c.options.CheckpointInterval)
	c.mu.Unlock()
	if c.options.Observer != nil {
		age := now.Unix() - time.UnixMilli(
			commit.Checkpoint.EffectiveAtUnixMillis,
		).UTC().Unix()
		if age < 0 {
			age = 0
		}
		c.options.Observer.SetBackupCheckpointAgeSeconds(&age)
	}
	return commit, nil
}

func (c *ContinuousCoordinator) recordContinuousFailure(
	category string,
	source coordinatorFailureSource,
) {
	if category == "" {
		category = "checkpoint"
	}
	c.mu.Lock()
	c.recordFailureLocked(category, source)
	c.mu.Unlock()
	if c.options.Observer != nil {
		c.options.Observer.ObserveBackupFailure(category)
	}
}

func (c *ContinuousCoordinator) recordFailureLocked(
	category string,
	source coordinatorFailureSource,
) {
	if category == "" {
		category = "checkpoint"
	}
	c.failureRevision++
	c.failureSource = source
	c.status.LastFailureCategory = category
}

func (c *ContinuousCoordinator) clearFailureLocked(
	expectedRevision uint64,
	source coordinatorFailureSource,
) {
	if source == coordinatorFailureNone ||
		c.failureRevision != expectedRevision ||
		c.failureSource != source {
		return
	}
	c.failureRevision++
	c.failureSource = coordinatorFailureNone
	c.status.LastFailureCategory = ""
}

func (c *ContinuousCoordinator) retireFormerLeaderFailureLocked(
	leader bool,
) {
	if !c.status.ControllerLeader || leader ||
		!leaderOwnedFailure(c.failureSource) {
		return
	}
	c.clearFailureLocked(c.failureRevision, c.failureSource)
}

func leaderOwnedFailure(source coordinatorFailureSource) bool {
	switch source {
	case coordinatorFailureCheckpointPublication,
		coordinatorFailureAuditMaintenance,
		coordinatorFailureGarbageMaintenance:
		return true
	default:
		return false
	}
}

func maintenanceFailureSource(
	category string,
) coordinatorFailureSource {
	switch category {
	case "audit":
		return coordinatorFailureAuditMaintenance
	case "gc":
		return coordinatorFailureGarbageMaintenance
	default:
		return coordinatorFailureNone
	}
}

func checkpointFailureCategory(err error) string {
	switch {
	case errors.Is(err, ErrCaptureNotLeader):
		return "leadership"
	case errors.Is(err, ErrFrontierConflict):
		return "frontier_conflict"
	default:
		return "checkpoint"
	}
}
