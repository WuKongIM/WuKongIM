package backup

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
)

const (
	defaultCoordinatorTickInterval = 5 * time.Second
	defaultDoctorRetryInterval     = time.Minute
	defaultRetentionInterval       = 24 * time.Hour
	defaultRemoteAuditInterval     = 24 * time.Hour
)

// CoordinatorApp is the entry-independent backup state machine surface used by the runtime.
type CoordinatorApp interface {
	CoordinationState(context.Context) (backupcontract.State, error)
	Trigger(context.Context, backupcontract.TriggerRequest) (backupcontract.Job, error)
	ReportPartition(context.Context, backupcontract.PartitionReport) (backupcontract.Job, error)
	Publish(context.Context, string, uint64) (backupcontract.RestorePoint, error)
	Degrade(context.Context, string, uint64, string) error
	ApplyRetention(context.Context, backupcontract.RetentionPolicy) (backupcontract.RetentionDecision, error)
	CompleteGarbage(context.Context, string) error
	Verify(context.Context, string) (backupcontract.Verification, error)
}

// CoordinatorDoctor gates scheduling but never message readiness.
type CoordinatorDoctor interface {
	Check(context.Context) error
}

// CoordinatorLeadership identifies the local node and current Controller leader.
type CoordinatorLeadership interface {
	NodeID() uint64
	BackupControllerLeaderID() uint64
}

// PartitionDispatcher routes one logical partition capture to its Slot leader.
type PartitionDispatcher interface {
	Dispatch(context.Context, CaptureRequest) (backupcontract.PartitionReport, error)
}

// RetentionGarbageCollector sweeps only objects unreachable from authenticated retained points.
type RetentionGarbageCollector interface {
	Collect(context.Context, []backupcontract.RestorePoint, []backupcontract.RestorePoint, *backupcontract.Job) (backupcontract.GarbageCollectionResult, error)
}

// ScheduleDecider applies the entry-independent scheduling policy supplied by
// the composition root without making runtime depend on a usecase package.
type ScheduleDecider func(time.Time, backupcontract.State, backupcontract.SchedulePolicy) (backupcontract.ScheduleDecision, error)

// RuntimeObserver receives low-cardinality backup and restore evidence.
type RuntimeObserver interface {
	SetBackupControllerLeader(bool)
	SetBackupDoctorHealth(string)
	SetBackupActive(bool)
	SetBackupRecoveryPointAgeSeconds(*int64)
	SetBackupVerificationAgeSeconds(*int64)
	ObserveBackupFailure(string)
	SetBackupRestoreProgress(int, int, int)
}

// CoordinatorOptions configures automatic cluster backup scheduling.
type CoordinatorOptions struct {
	App               CoordinatorApp
	Doctor            CoordinatorDoctor
	Leadership        CoordinatorLeadership
	Partitions        PartitionDispatcher
	ConfigFingerprint string
	Policy            backupcontract.SchedulePolicy
	DecideSchedule    ScheduleDecider
	MaxParallel       int
	TickInterval      time.Duration
	DoctorRetry       time.Duration
	Now               func() time.Time
	Observer          RuntimeObserver
	RetentionPolicy   backupcontract.RetentionPolicy
	RetentionInterval time.Duration
	GarbageCollector  RetentionGarbageCollector
	AuditInterval     time.Duration
}

// CoordinatorStatus is a bounded node-local operational snapshot.
type CoordinatorStatus struct {
	Running                    bool
	ControllerLeader           bool
	DoctorHealth               backupcontract.Health
	LastDoctorAtUnixMillis     int64
	LastSuccessAtUnixMillis    int64
	LastRetentionAtUnixMillis  int64
	LastAuditAtUnixMillis      int64
	LastAuditSuccessUnixMillis int64
	LastFailureCategory        string
}

// Coordinator resumes Controller-persisted jobs and dispatches missing logical partitions.
type Coordinator struct {
	options CoordinatorOptions
	mu      sync.Mutex
	status  CoordinatorStatus
	cancel  context.CancelFunc
	done    chan struct{}
}

// NewCoordinator creates a backup-only background runtime.
func NewCoordinator(options CoordinatorOptions) (*Coordinator, error) {
	if options.App == nil || options.Doctor == nil || options.Leadership == nil || options.Leadership.NodeID() == 0 || options.Partitions == nil || options.DecideSchedule == nil || !validFingerprint(options.ConfigFingerprint) || options.MaxParallel <= 0 {
		return nil, fmt.Errorf("%w: coordinator options are incomplete", ErrInvalidCapture)
	}
	if _, err := options.DecideSchedule(time.Now(), backupcontract.State{}, options.Policy); err != nil {
		return nil, err
	}
	if options.TickInterval == 0 {
		options.TickInterval = defaultCoordinatorTickInterval
	}
	if options.DoctorRetry == 0 {
		options.DoctorRetry = defaultDoctorRetryInterval
	}
	if options.RetentionInterval == 0 {
		options.RetentionInterval = defaultRetentionInterval
	}
	if options.AuditInterval == 0 {
		options.AuditInterval = defaultRemoteAuditInterval
	}
	if options.TickInterval <= 0 || options.DoctorRetry <= 0 || options.RetentionInterval <= 0 || options.AuditInterval <= 0 {
		return nil, fmt.Errorf("%w: coordinator intervals must be positive", ErrInvalidCapture)
	}
	if options.RetentionPolicy.MonthlyMonths < 0 || options.RetentionPolicy.MonthlyMonths > 120 {
		return nil, fmt.Errorf("%w: monthly retention months must be between 0 and 120", ErrInvalidCapture)
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	return &Coordinator{options: options, status: CoordinatorStatus{DoctorHealth: backupcontract.HealthUnknown}}, nil
}

// Start starts the backup-only loop. Doctor failure is recorded and retried,
// but intentionally does not fail application startup.
func (c *Coordinator) Start(ctx context.Context) error {
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
	done := c.done
	c.mu.Unlock()
	go c.loop(runContext, done)
	return nil
}

// Stop cancels in-flight backup work and waits for the coordinator loop.
func (c *Coordinator) Stop(ctx context.Context) error {
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

// Status returns a detached node-local operational snapshot.
func (c *Coordinator) Status() CoordinatorStatus {
	if c == nil {
		return CoordinatorStatus{DoctorHealth: backupcontract.HealthUnknown}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.status
}

func (c *Coordinator) loop(ctx context.Context, done chan struct{}) {
	defer func() {
		c.mu.Lock()
		c.status.Running = false
		c.cancel = nil
		c.mu.Unlock()
		close(done)
	}()
	ticker := time.NewTicker(c.options.TickInterval)
	defer ticker.Stop()
	for {
		_ = c.runOnce(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (c *Coordinator) runOnce(ctx context.Context) error {
	leader := c.options.Leadership.BackupControllerLeaderID() == c.options.Leadership.NodeID()
	if c.options.Observer != nil {
		c.options.Observer.SetBackupControllerLeader(leader)
	}
	c.mu.Lock()
	c.status.ControllerLeader = leader
	c.mu.Unlock()
	if !leader {
		return nil
	}
	if err := c.ensureDoctor(ctx); err != nil {
		return err
	}
	state, err := c.options.App.CoordinationState(ctx)
	if err != nil {
		c.recordFailure("coordination_state")
		return err
	}
	if c.options.GarbageCollector != nil {
		updated, retentionErr := c.reconcileRetention(ctx, state)
		if retentionErr != nil {
			c.recordFailure("retention")
		} else {
			state = updated
		}
	}
	if c.options.Observer != nil {
		c.options.Observer.SetBackupActive(state.Active != nil)
		var age *int64
		if len(state.RestorePoints) > 0 {
			latestEffectiveAt := state.RestorePoints[0].EffectiveAtUnixMillis
			for _, point := range state.RestorePoints[1:] {
				if point.EffectiveAtUnixMillis > latestEffectiveAt {
					latestEffectiveAt = point.EffectiveAtUnixMillis
				}
			}
			seconds := c.options.Now().UTC().Unix() - time.UnixMilli(latestEffectiveAt).UTC().Unix()
			if seconds < 0 {
				seconds = 0
			}
			age = &seconds
		}
		c.options.Observer.SetBackupRecoveryPointAgeSeconds(age)
		c.options.Observer.SetBackupVerificationAgeSeconds(c.verificationAgeSeconds())
	}
	if auditErr := c.auditLatestRestorePoint(ctx, state); auditErr != nil {
		c.recordFailure("audit")
	}
	if state.Active == nil {
		decision, err := c.options.DecideSchedule(c.options.Now(), state, c.options.Policy)
		if err != nil {
			c.recordFailure("schedule")
			return err
		}
		if !decision.Due {
			return nil
		}
		job, err := c.options.App.Trigger(ctx, backupcontract.TriggerRequest{Kind: decision.Kind, ConfigFingerprint: c.options.ConfigFingerprint})
		if err != nil {
			if errors.Is(err, backupcontract.ErrJobActive) || errors.Is(err, backupcontract.ErrStateConflict) {
				return nil
			}
			c.recordFailure("trigger")
			return err
		}
		state.Active = &job
	}
	return c.resumeJob(ctx, *state.Active)
}

func (c *Coordinator) auditLatestRestorePoint(ctx context.Context, state backupcontract.State) error {
	if len(state.RestorePoints) == 0 {
		return nil
	}
	now := c.options.Now().UTC()
	c.mu.Lock()
	lastAttempt := time.UnixMilli(c.status.LastAuditAtUnixMillis)
	if !lastAttempt.IsZero() && now.Sub(lastAttempt) < c.options.AuditInterval {
		c.mu.Unlock()
		return nil
	}
	c.status.LastAuditAtUnixMillis = now.UnixMilli()
	c.mu.Unlock()
	latest := state.RestorePoints[0]
	for _, point := range state.RestorePoints[1:] {
		if point.EffectiveAtUnixMillis > latest.EffectiveAtUnixMillis ||
			(point.EffectiveAtUnixMillis == latest.EffectiveAtUnixMillis && point.CreatedAtUnixMillis > latest.CreatedAtUnixMillis) {
			latest = point
		}
	}
	if _, err := c.options.App.Verify(ctx, latest.ID); err != nil {
		return err
	}
	c.mu.Lock()
	c.status.LastAuditSuccessUnixMillis = now.UnixMilli()
	c.mu.Unlock()
	if c.options.Observer != nil {
		zero := int64(0)
		c.options.Observer.SetBackupVerificationAgeSeconds(&zero)
	}
	return nil
}

func (c *Coordinator) verificationAgeSeconds() *int64 {
	c.mu.Lock()
	lastSuccess := c.status.LastAuditSuccessUnixMillis
	c.mu.Unlock()
	if lastSuccess == 0 {
		return nil
	}
	seconds := c.options.Now().UTC().Unix() - time.UnixMilli(lastSuccess).UTC().Unix()
	if seconds < 0 {
		seconds = 0
	}
	return &seconds
}

func (c *Coordinator) reconcileRetention(ctx context.Context, state backupcontract.State) (backupcontract.State, error) {
	now := c.options.Now().UTC()
	c.mu.Lock()
	lastAttempt := time.UnixMilli(c.status.LastRetentionAtUnixMillis)
	if !lastAttempt.IsZero() && now.Sub(lastAttempt) < c.options.RetentionInterval {
		c.mu.Unlock()
		return state, nil
	}
	c.status.LastRetentionAtUnixMillis = now.UnixMilli()
	c.mu.Unlock()

	if _, err := c.options.App.ApplyRetention(ctx, c.options.RetentionPolicy); err != nil {
		return state, err
	}
	current, err := c.options.App.CoordinationState(ctx)
	if err != nil {
		return state, err
	}
	result, err := c.options.GarbageCollector.Collect(ctx, current.RestorePoints, current.PendingGarbage, cloneJobPointer(current.Active))
	if err != nil {
		return current, err
	}
	for _, restorePointID := range result.CompletedRestorePointIDs {
		if err := c.options.App.CompleteGarbage(ctx, restorePointID); err != nil {
			return current, err
		}
	}
	if len(result.CompletedRestorePointIDs) == 0 {
		return current, nil
	}
	return c.options.App.CoordinationState(ctx)
}

func cloneJobPointer(job *backupcontract.Job) *backupcontract.Job {
	if job == nil {
		return nil
	}
	copy := *job
	copy.Partitions = append([]backupcontract.PartitionReport(nil), job.Partitions...)
	return &copy
}

func (c *Coordinator) ensureDoctor(ctx context.Context) error {
	now := c.options.Now().UTC()
	c.mu.Lock()
	lastAttempt := time.UnixMilli(c.status.LastDoctorAtUnixMillis)
	health := c.status.DoctorHealth
	c.mu.Unlock()
	if health == backupcontract.HealthHealthy {
		return nil
	}
	if !lastAttempt.IsZero() && now.Sub(lastAttempt) < c.options.DoctorRetry {
		return fmt.Errorf("backup doctor retry pending")
	}
	err := c.options.Doctor.Check(ctx)
	c.mu.Lock()
	c.status.LastDoctorAtUnixMillis = now.UnixMilli()
	if err != nil {
		c.status.DoctorHealth = backupcontract.HealthFailed
		c.status.LastFailureCategory = "doctor"
	} else {
		c.status.DoctorHealth = backupcontract.HealthHealthy
		c.status.LastFailureCategory = ""
	}
	doctorHealth := c.status.DoctorHealth
	c.mu.Unlock()
	if c.options.Observer != nil {
		c.options.Observer.SetBackupDoctorHealth(string(doctorHealth))
	}
	return err
}

func (c *Coordinator) resumeJob(ctx context.Context, job backupcontract.Job) error {
	if job.ConfigFingerprint != c.options.ConfigFingerprint {
		_ = c.options.App.Degrade(ctx, job.ID, job.Epoch, "config_drift")
		c.recordFailure("config_drift")
		return ErrStaleCapture
	}
	completed := make(map[uint16]struct{}, len(job.Partitions))
	for _, report := range job.Partitions {
		completed[report.HashSlot] = struct{}{}
	}
	missing := make([]uint16, 0, int(job.HashSlotCount)-len(completed))
	for hashSlot := uint16(0); hashSlot < job.HashSlotCount; hashSlot++ {
		if _, ok := completed[hashSlot]; !ok {
			missing = append(missing, hashSlot)
		}
	}
	if len(missing) > 0 {
		if err := c.captureMissing(ctx, job, missing); err != nil {
			_ = c.options.App.Degrade(ctx, job.ID, job.Epoch, captureFailureCategory(err))
			c.recordFailure(captureFailureCategory(err))
			return err
		}
	}
	if _, err := c.options.App.Publish(ctx, job.ID, job.Epoch); err != nil {
		c.recordFailure("publish")
		return err
	}
	c.mu.Lock()
	c.status.LastSuccessAtUnixMillis = c.options.Now().UTC().UnixMilli()
	c.status.LastFailureCategory = ""
	c.mu.Unlock()
	return nil
}

func (c *Coordinator) captureMissing(ctx context.Context, job backupcontract.Job, missing []uint16) error {
	type result struct {
		report backupcontract.PartitionReport
		err    error
	}
	work := make(chan uint16)
	results := make(chan result, len(missing))
	workers := c.options.MaxParallel
	if workers > len(missing) {
		workers = len(missing)
	}
	var group sync.WaitGroup
	for index := 0; index < workers; index++ {
		group.Add(1)
		go func() {
			defer group.Done()
			for hashSlot := range work {
				report, err := c.options.Partitions.Dispatch(ctx, CaptureRequest{
					JobID: job.ID, BackupEpoch: job.Epoch, HashSlot: hashSlot,
					ConfigFingerprint: job.ConfigFingerprint, Kind: job.Kind,
					BaseRestorePointID: job.BaseRestorePointID,
				})
				results <- result{report: report, err: err}
			}
		}()
	}
	go func() {
		defer close(work)
		for _, hashSlot := range missing {
			select {
			case work <- hashSlot:
			case <-ctx.Done():
				return
			}
		}
	}()
	group.Wait()
	close(results)
	var firstErr error
	for result := range results {
		if result.err != nil {
			if firstErr == nil {
				firstErr = result.err
			}
			continue
		}
		if _, err := c.options.App.ReportPartition(ctx, result.report); err != nil && !errors.Is(err, backupcontract.ErrStateConflict) {
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

func captureFailureCategory(err error) string {
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return "capture_canceled"
	case errors.Is(err, ErrStaleCapture):
		return "stale_partition_owner"
	default:
		return "partition_capture"
	}
}

func (c *Coordinator) recordFailure(category string) {
	c.mu.Lock()
	c.status.LastFailureCategory = category
	c.mu.Unlock()
	if c.options.Observer != nil {
		c.options.Observer.ObserveBackupFailure(category)
	}
}

var _ interface {
	Start(context.Context) error
	Stop(context.Context) error
} = (*Coordinator)(nil)
