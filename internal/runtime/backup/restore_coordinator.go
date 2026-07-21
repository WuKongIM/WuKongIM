package backup

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	backupcontract "github.com/WuKongIM/WuKongIM/internal/contracts/backup"
)

const defaultRestoreCoordinatorTickInterval = 5 * time.Second

// RestoreCoordinatorApp exposes persisted restore progress to the runtime.
type RestoreCoordinatorApp interface {
	Status(context.Context) (*backupcontract.RestorePlan, error)
	ReportPartition(context.Context, string, backupcontract.RestorePartition) (backupcontract.RestorePlan, error)
}

// RestorePartitionInstaller installs one logical partition on the target cluster.
type RestorePartitionInstaller interface {
	InstallPartition(context.Context, backupcontract.RestorePlan, uint16) (backupcontract.RestorePartition, error)
}

// RestoreCoordinatorOptions configures resumable restore installation.
type RestoreCoordinatorOptions struct {
	App          RestoreCoordinatorApp
	Leadership   CoordinatorLeadership
	Partitions   RestorePartitionInstaller
	MaxParallel  int
	TickInterval time.Duration
	Now          func() time.Time
	Observer     RuntimeObserver
}

// RestoreCoordinatorStatus is bounded node-local operational evidence.
type RestoreCoordinatorStatus struct {
	Running                 bool
	ControllerLeader        bool
	LastSuccessAtUnixMillis int64
	LastFailureCategory     string
}

// RestoreCoordinator resumes an installing Controller-persisted plan only on
// the current Controller leader.
type RestoreCoordinator struct {
	options RestoreCoordinatorOptions
	mu      sync.Mutex
	status  RestoreCoordinatorStatus
	cancel  context.CancelFunc
	done    chan struct{}
}

// NewRestoreCoordinator creates the restore-only background runtime.
func NewRestoreCoordinator(options RestoreCoordinatorOptions) (*RestoreCoordinator, error) {
	if options.App == nil || options.Leadership == nil || options.Leadership.NodeID() == 0 || options.Partitions == nil || options.MaxParallel <= 0 {
		return nil, fmt.Errorf("%w: restore coordinator options are incomplete", ErrInvalidCapture)
	}
	if options.TickInterval == 0 {
		options.TickInterval = defaultRestoreCoordinatorTickInterval
	}
	if options.TickInterval <= 0 {
		return nil, fmt.Errorf("%w: restore coordinator interval must be positive", ErrInvalidCapture)
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	return &RestoreCoordinator{options: options}, nil
}

// Start starts a loop tied to the caller's lifecycle context.
func (c *RestoreCoordinator) Start(ctx context.Context) error {
	if c == nil {
		return ErrInvalidCapture
	}
	c.mu.Lock()
	if c.cancel != nil {
		c.mu.Unlock()
		return nil
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

// Stop cancels installation work and waits for the loop.
func (c *RestoreCoordinator) Stop(ctx context.Context) error {
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

// Status returns detached operational evidence.
func (c *RestoreCoordinator) Status() RestoreCoordinatorStatus {
	if c == nil {
		return RestoreCoordinatorStatus{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.status
}

func (c *RestoreCoordinator) loop(ctx context.Context, done chan struct{}) {
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

func (c *RestoreCoordinator) runOnce(ctx context.Context) error {
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
	plan, err := c.options.App.Status(ctx)
	if err != nil {
		c.recordRestoreFailure("restore_state")
		return err
	}
	if plan == nil || plan.Status != backupcontract.RestoreStatusInstalling {
		if c.options.Observer != nil && plan == nil {
			c.options.Observer.SetBackupRestoreProgress(0, 0, 0)
		}
		return nil
	}
	missing := make([]uint16, 0, plan.HashSlotCount)
	if len(plan.Partitions) != int(plan.HashSlotCount) {
		c.recordRestoreFailure("restore_state")
		return backupcontract.ErrStateConflict
	}
	for hashSlot := uint16(0); hashSlot < plan.HashSlotCount; hashSlot++ {
		if !plan.Partitions[hashSlot].Installed {
			missing = append(missing, hashSlot)
		}
	}
	if c.options.Observer != nil {
		installed, verified := 0, 0
		for _, partition := range plan.Partitions {
			if partition.Installed {
				installed++
			}
			if partition.Verified {
				verified++
			}
		}
		c.options.Observer.SetBackupRestoreProgress(installed, verified, int(plan.HashSlotCount))
	}
	if len(missing) == 0 {
		return nil
	}
	err = c.installMissing(ctx, *plan, missing)
	if err != nil {
		c.recordRestoreFailure(restoreInstallFailureCategory(err))
		return err
	}
	c.mu.Lock()
	c.status.LastSuccessAtUnixMillis = c.options.Now().UTC().UnixMilli()
	c.status.LastFailureCategory = ""
	c.mu.Unlock()
	return nil
}

func (c *RestoreCoordinator) installMissing(ctx context.Context, plan backupcontract.RestorePlan, missing []uint16) error {
	type result struct {
		report backupcontract.RestorePartition
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
				report, err := c.options.Partitions.InstallPartition(ctx, plan, hashSlot)
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
		if _, err := c.options.App.ReportPartition(ctx, plan.ID, result.report); err != nil && !errors.Is(err, backupcontract.ErrStateConflict) {
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

func restoreInstallFailureCategory(err error) string {
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return "restore_canceled"
	case errors.Is(err, backupcontract.ErrStateConflict):
		return "restore_state"
	default:
		return "restore_partition_install"
	}
}

func (c *RestoreCoordinator) recordRestoreFailure(category string) {
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
} = (*RestoreCoordinator)(nil)
