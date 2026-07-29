package backup

import (
	"context"
	"fmt"
	"sync"
	"time"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
	goruntimeregistry "github.com/WuKongIM/WuKongIM/pkg/goroutine"
)

// ScheduleEvaluator advances the durable schedule cursor and admits due work.
type ScheduleEvaluator interface {
	Evaluate(context.Context, time.Duration) error
}

// ResumableJobRunner advances one durable backup or restore job.
type ResumableJobRunner interface {
	RunOnce(context.Context) (bool, error)
}

// ScheduledStateReader exposes only the bounded active job records needed to
// propagate an operator cancellation into in-flight repository I/O.
type ScheduledStateReader interface {
	State(context.Context) (backupcontract.SystemState, error)
}

// ScheduledLeadership identifies the current Controller coordinator.
type ScheduledLeadership interface {
	NodeID() uint64
	BackupControllerLeaderID() uint64
	BackupControllerFence(context.Context) (uint64, uint64, error)
}

// ScheduledRuntimeOptions configures leader-only scheduling and resumable work.
type ScheduledRuntimeOptions struct {
	// Scheduled advances only the durable Controller-owned schedule cursor.
	Scheduled ScheduleEvaluator
	// State exposes active-job identity for cancellation propagation.
	State ScheduledStateReader
	// Runner advances the single active full-backup state machine.
	Runner ResumableJobRunner
	// Restore advances the single active restore state machine.
	Restore ResumableJobRunner
	// Leadership provides the current Controller leader and term fence.
	Leadership ScheduledLeadership
	// Tick bounds leader polling frequency and must be between one second and one minute.
	Tick time.Duration
	// OnError receives sanitized background-step failures without stopping the loop.
	OnError func(error)
}

// ScheduledRuntime evaluates Cron and resumes one durable full backup only on
// the current Controller leader.
type ScheduledRuntime struct {
	options ScheduledRuntimeOptions

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

// NewScheduledRuntime creates the full-backup supervisor.
func NewScheduledRuntime(
	options ScheduledRuntimeOptions,
) (*ScheduledRuntime, error) {
	if options.Scheduled == nil || options.State == nil || options.Runner == nil ||
		options.Restore == nil ||
		options.Leadership == nil || options.Leadership.NodeID() == 0 {
		return nil, fmt.Errorf("backup scheduled runtime: invalid options")
	}
	if options.Tick == 0 {
		options.Tick = 10 * time.Second
	}
	if options.Tick < time.Second || options.Tick > time.Minute {
		return nil, fmt.Errorf("backup scheduled runtime: invalid tick")
	}
	return &ScheduledRuntime{options: options}, nil
}

// Start begins leader observation. Repeated starts are idempotent.
func (r *ScheduledRuntime) Start(ctx context.Context) error {
	if r == nil {
		return fmt.Errorf("backup scheduled runtime: unavailable")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cancel != nil {
		return nil
	}
	runContext, cancel := context.WithCancel(ctx)
	r.cancel = cancel
	r.done = make(chan struct{})
	done := r.done
	goruntimeregistry.SafeGo(
		nil, goruntimeregistry.TaskBackupScheduledCoordinator,
		func() {
			defer close(done)
			r.loop(runContext)
		},
	)
	return nil
}

// Stop cancels active repository I/O and waits for the worker to exit.
func (r *ScheduledRuntime) Stop(ctx context.Context) error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	cancel := r.cancel
	done := r.done
	r.cancel = nil
	r.done = nil
	r.mu.Unlock()
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

func (r *ScheduledRuntime) loop(ctx context.Context) {
	for {
		active := r.advance(ctx)
		delay := r.options.Tick
		if active {
			delay = 10 * time.Millisecond
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (r *ScheduledRuntime) advance(ctx context.Context) bool {
	if ctx.Err() != nil ||
		r.options.Leadership.BackupControllerLeaderID() !=
			r.options.Leadership.NodeID() {
		return false
	}
	leaderID, leaderTerm, err :=
		r.options.Leadership.BackupControllerFence(ctx)
	if err != nil {
		r.reportError(err)
		return false
	}
	if leaderID != r.options.Leadership.NodeID() || leaderTerm == 0 {
		return false
	}
	workContext, cancel := context.WithCancel(ctx)
	workContext = backupcontract.WithCoordinatorFence(
		workContext, leaderID, leaderTerm,
	)
	baseline, err := r.options.State.State(workContext)
	if err != nil {
		cancel()
		r.reportError(err)
		return false
	}
	monitorDone := make(chan struct{})
	goruntimeregistry.SafeGo(
		nil, goruntimeregistry.TaskBackupCoordinatorFenceMonitor,
		func() {
			defer close(monitorDone)
			r.monitorFence(
				workContext, cancel, leaderID, leaderTerm, baseline,
			)
		},
	)
	defer func() {
		cancel()
		<-monitorDone
	}()

	restoreActive, err := r.options.Restore.RunOnce(workContext)
	if err != nil {
		if workContext.Err() == nil {
			r.reportError(err)
		}
		return false
	}
	if restoreActive {
		return true
	}
	if err := r.options.Scheduled.Evaluate(
		workContext, 2*time.Minute,
	); err != nil {
		if workContext.Err() == nil {
			r.reportError(err)
		}
		return false
	}
	active, err := r.options.Runner.RunOnce(workContext)
	if err != nil && workContext.Err() == nil {
		r.reportError(err)
	}
	return active && err == nil
}

func (r *ScheduledRuntime) monitorFence(
	ctx context.Context,
	cancel context.CancelFunc,
	leaderID uint64,
	leaderTerm uint64,
	baseline backupcontract.SystemState,
) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			currentID, currentTerm, err :=
				r.options.Leadership.BackupControllerFence(ctx)
			if err != nil || currentID != leaderID || currentTerm != leaderTerm {
				cancel()
				return
			}
			state, err := r.options.State.State(ctx)
			if err != nil {
				cancel()
				return
			}
			if cancellationBecameRequested(baseline, state) {
				cancel()
				return
			}
			baseline = state
		}
	}
}

func cancellationBecameRequested(
	before backupcontract.SystemState,
	after backupcontract.SystemState,
) bool {
	if before.ActiveRestore != nil && after.ActiveRestore != nil &&
		before.ActiveRestore.ID == after.ActiveRestore.ID &&
		!before.ActiveRestore.CancelRequested &&
		after.ActiveRestore.CancelRequested {
		return true
	}
	return before.ActiveBackup != nil && after.ActiveBackup != nil &&
		before.ActiveBackup.ID == after.ActiveBackup.ID &&
		!before.ActiveBackup.CancelRequested &&
		after.ActiveBackup.CancelRequested
}

func (r *ScheduledRuntime) reportError(err error) {
	if err != nil && r.options.OnError != nil {
		r.options.OnError(err)
	}
}
