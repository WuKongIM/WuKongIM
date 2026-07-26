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
	// Doctor gates repository/KMS work without affecting foreground readiness.
	Doctor CoordinatorDoctor
	// Leadership identifies the current Controller Leader.
	Leadership CoordinatorLeadership
	// CheckpointInterval is the maximum target recovery-point cadence.
	CheckpointInterval time.Duration
	// TickInterval controls bounded retry and leadership observation latency.
	TickInterval time.Duration
	// DoctorRetry controls dependency qualification retries.
	DoctorRetry time.Duration
	// Now may be replaced by deterministic tests.
	Now func() time.Time
	// Observer receives low-cardinality operational evidence.
	Observer RuntimeObserver
}

// ContinuousCoordinator supervises capture and complete checkpoint publication.
// It deliberately has no job, partition-dispatch, or synthetic-full state.
type ContinuousCoordinator struct {
	options ContinuousCoordinatorOptions

	mu                sync.Mutex
	status            CoordinatorStatus
	lastCheckpointAt  int64
	nextCheckpointAt  time.Time
	nextDoctorAt      time.Time
	captureStarted    bool
	captureCancel     context.CancelFunc
	captureDone       chan error
	cancel            context.CancelFunc
	done              chan struct{}
	publicationSerial sync.Mutex
}

// NewContinuousCoordinator creates the production continuous-backup supervisor.
func NewContinuousCoordinator(options ContinuousCoordinatorOptions) (*ContinuousCoordinator, error) {
	if options.Capture == nil || options.Checkpoints == nil || options.Doctor == nil ||
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
	return &ContinuousCoordinator{
		options: options,
		status: CoordinatorStatus{
			DoctorHealth: backupcontract.HealthUnknown,
			Doctor: backupcontract.DoctorReport{
				Primary: backupcontract.HealthUnknown, Secondary: backupcontract.HealthUnknown,
				KMS: backupcontract.HealthUnknown, Staging: backupcontract.HealthUnknown,
				UTC: backupcontract.HealthUnknown,
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
		c.stopCapture()
		c.mu.Lock()
		c.status.Running = false
		c.cancel = nil
		c.mu.Unlock()
		close(done)
	}()
	for {
		c.runOnce(ctx)
		select {
		case <-ctx.Done():
			return
		case err := <-c.captureDoneChannel():
			c.captureExited(err)
		case <-ticker.C:
		}
	}
}

func (c *ContinuousCoordinator) runOnce(ctx context.Context) {
	now := c.options.Now().UTC()
	leader := c.options.Leadership.BackupControllerLeaderID() == c.options.Leadership.NodeID()
	c.mu.Lock()
	c.status.ControllerLeader = leader
	doctorDue := c.nextDoctorAt.IsZero() || !now.Before(c.nextDoctorAt)
	c.mu.Unlock()
	if c.options.Observer != nil {
		c.options.Observer.SetBackupControllerLeader(leader)
	}
	if doctorDue {
		c.runDoctor(ctx, now)
	}
	c.mu.Lock()
	healthy := c.status.DoctorHealth == backupcontract.HealthHealthy
	publishDue := leader && healthy &&
		(c.nextCheckpointAt.IsZero() || !now.Before(c.nextCheckpointAt))
	c.mu.Unlock()
	if healthy {
		c.startCapture(ctx)
	}
	if !publishDue {
		return
	}
	if _, err := c.publish(ctx); err != nil {
		if !errors.Is(err, context.Canceled) {
			c.recordContinuousFailure(checkpointFailureCategory(err))
		}
		return
	}
	c.mu.Lock()
	c.nextCheckpointAt = now.Add(c.options.CheckpointInterval)
	c.mu.Unlock()
}

func (c *ContinuousCoordinator) runDoctor(ctx context.Context, now time.Time) {
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
		c.status.LastFailureCategory = report.FailureCategory
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
	captureContext, cancel := context.WithCancel(parent)
	c.captureStarted = true
	c.captureCancel = cancel
	c.captureDone = make(chan error, 1)
	done := c.captureDone
	c.mu.Unlock()
	go func() {
		done <- c.options.Capture.Run(captureContext)
	}()
}

func (c *ContinuousCoordinator) stopCapture() {
	c.mu.Lock()
	cancel := c.captureCancel
	c.mu.Unlock()
	if cancel != nil {
		cancel()
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
		c.recordContinuousFailure("capture_runtime")
	}
}

func (c *ContinuousCoordinator) publish(
	ctx context.Context,
) (backupartifact.CheckpointCatalogCommit, error) {
	c.publicationSerial.Lock()
	defer c.publicationSerial.Unlock()
	commit, err := c.options.Checkpoints.Publish(ctx)
	if err != nil {
		return backupartifact.CheckpointCatalogCommit{}, err
	}
	now := c.options.Now().UTC()
	c.mu.Lock()
	c.lastCheckpointAt = commit.Checkpoint.CreatedAtUnixMillis
	c.status.LastSuccessAtUnixMillis = commit.Checkpoint.CreatedAtUnixMillis
	c.status.LastFailureCategory = ""
	c.nextCheckpointAt = now.Add(c.options.CheckpointInterval)
	c.mu.Unlock()
	if c.options.Observer != nil {
		c.options.Observer.SetBackupActive(false)
		age := int64(0)
		c.options.Observer.SetBackupRecoveryPointAgeSeconds(&age)
	}
	return commit, nil
}

func (c *ContinuousCoordinator) recordContinuousFailure(category string) {
	if category == "" {
		category = "checkpoint"
	}
	c.mu.Lock()
	c.status.LastFailureCategory = category
	c.mu.Unlock()
	if c.options.Observer != nil {
		c.options.Observer.ObserveBackupFailure(category)
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
