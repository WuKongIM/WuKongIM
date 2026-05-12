package channelmigration

import (
	"context"
	"errors"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	slotmeta "github.com/WuKongIM/WuKongIM/pkg/slot/meta"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

// ErrPhaseNotImplemented marks a claimed task phase that is intentionally left
// for the later phase-specific migration tasks.
var ErrPhaseNotImplemented = errors.New("channelmigration: phase not implemented")

// Executor claims channel migration tasks and runs one guarded phase attempt per tick.
type Executor struct {
	// store is the authoritative slot metadata port for task CAS operations.
	store Store
	// slots confirms local slot leadership before task ownership and side effects.
	slots SlotLeadership
	// metrics records executor activity without affecting task progress.
	metrics Metrics
	// localNode is the node ID written into owner leases.
	localNode channel.NodeID
	// now supplies wall-clock time for leases, retries, and cleanup cutoffs.
	now func() time.Time
	// cfg controls scan limits, lease duration, retry backoff, GC, and concurrency.
	cfg Config
	// log receives future executor diagnostics while keeping the runtime app-neutral.
	log wklog.Logger
}

// NewExecutor builds an executor with deterministic defaults for omitted options.
func NewExecutor(opts ExecutorOptions) *Executor {
	cfg := normalizeConfig(opts.Config)
	metrics := opts.Metrics
	if metrics == nil {
		metrics = noopMetrics{}
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	logger := opts.Logger
	if logger == nil {
		logger = wklog.NewNop()
	}
	return &Executor{
		store:     opts.Store,
		slots:     opts.Slots,
		metrics:   metrics,
		localNode: opts.LocalNode,
		now:       now,
		cfg:       cfg,
		log:       logger,
	}
}

// Tick scans runnable tasks, claims owner leases, runs skeleton phase dispatch,
// and performs terminal-task cleanup.
func (e *Executor) Tick(ctx context.Context) error {
	now := e.now()
	nowMS := now.UnixMilli()
	tasks, err := e.store.ListRunnableTasksForLocalLeaderSlots(ctx, nowMS, e.cfg.ScanLimit)
	if err != nil {
		return err
	}
	e.metrics.RecordActiveTasks(len(tasks))

	limits := newConcurrencyLimits(e.cfg)
	for _, task := range tasks {
		if !e.isRunnableTask(task, nowMS) {
			continue
		}
		if ok := e.confirmLocalSlotLeader(ctx, task); !ok {
			continue
		}
		if !limits.Allow(task) {
			continue
		}
		if err := e.runOne(ctx, task, nowMS); err != nil {
			return err
		}
	}
	return e.gcTerminalTasks(ctx, now)
}

func (e *Executor) runOne(ctx context.Context, task Task, nowMS int64) error {
	claimed, claimedOK, err := e.claimOwnerLease(ctx, task, nowMS)
	if err != nil || !claimedOK {
		return err
	}
	if ok := e.confirmLocalSlotLeader(ctx, claimed); !ok {
		return nil
	}
	if !e.hasLocalOwnerLease(claimed, nowMS) {
		return nil
	}
	if err := e.dispatchPhase(ctx, claimed, nowMS); err != nil {
		e.metrics.RecordRetry(claimed, err)
	}
	return nil
}

func (e *Executor) claimOwnerLease(ctx context.Context, task Task, nowMS int64) (Task, bool, error) {
	req := ClaimRequest{
		Guard:             guardFromTask(task),
		Status:            slotmeta.ChannelMigrationStatusRunning,
		Phase:             task.Phase,
		OwnerNodeID:       uint64(e.localNode),
		OwnerLeaseUntilMS: nowMS + e.cfg.OwnerLease.Milliseconds(),
		UpdatedAtMS:       nextUpdatedAtMS(nowMS, task.UpdatedAtMS),
	}
	if err := e.store.ClaimChannelMigrationTask(ctx, req); err != nil {
		if errors.Is(err, slotmeta.ErrStaleMeta) || errors.Is(err, slotmeta.ErrNotFound) {
			return Task{}, false, nil
		}
		return Task{}, false, err
	}

	claimed := task
	claimed.Status = req.Status
	claimed.Phase = req.Phase
	claimed.OwnerNodeID = req.OwnerNodeID
	claimed.OwnerLeaseUntilMS = req.OwnerLeaseUntilMS
	claimed.UpdatedAtMS = req.UpdatedAtMS
	return claimed, true, nil
}

func (e *Executor) dispatchPhase(ctx context.Context, task Task, nowMS int64) error {
	err := ErrPhaseNotImplemented
	nextRunAtMS := int64(0)
	if e.cfg.RetryBackoff > 0 {
		nextRunAtMS = nowMS + e.cfg.RetryBackoff.Milliseconds()
	}
	req := AdvanceRequest{
		Guard:       guardFromTask(task),
		Status:      task.Status,
		Phase:       task.Phase,
		Attempt:     task.Attempt + 1,
		NextRunAtMS: nextRunAtMS,
		LastError:   err.Error(),
		UpdatedAtMS: nextUpdatedAtMS(nowMS, task.UpdatedAtMS),
		Progress:    task.Progress,
	}
	if advanceErr := e.store.AdvanceChannelMigrationTask(ctx, req); advanceErr != nil {
		return advanceErr
	}
	e.metrics.RecordPhaseTransition(PhaseTransition{
		TaskID:     task.TaskID,
		ChannelID:  task.ChannelID,
		FromStatus: task.Status,
		ToStatus:   req.Status,
		FromPhase:  task.Phase,
		ToPhase:    req.Phase,
	})
	return err
}

func (e *Executor) confirmLocalSlotLeader(ctx context.Context, task Task) bool {
	slotID := e.slots.SlotForChannel(task.ChannelID)
	ok, err := e.slots.IsLocalLeader(ctx, slotID)
	if err != nil {
		e.metrics.RecordRetry(task, err)
		return false
	}
	return ok
}

func (e *Executor) isRunnableTask(task Task, nowMS int64) bool {
	switch task.Status {
	case slotmeta.ChannelMigrationStatusPending, slotmeta.ChannelMigrationStatusRunning:
	default:
		return false
	}
	if task.NextRunAtMS > nowMS {
		return false
	}
	if task.OwnerNodeID == 0 {
		return true
	}
	if task.OwnerNodeID == uint64(e.localNode) {
		return true
	}
	return task.OwnerLeaseUntilMS <= nowMS
}

func (e *Executor) hasLocalOwnerLease(task Task, nowMS int64) bool {
	return task.OwnerNodeID == uint64(e.localNode) && task.OwnerLeaseUntilMS > nowMS
}

func guardFromTask(task Task) slotmeta.ChannelMigrationTaskGuard {
	return slotmeta.ChannelMigrationTaskGuard{
		ChannelID:                 task.ChannelID,
		ChannelType:               task.ChannelType,
		TaskID:                    task.TaskID,
		ExpectedStatus:            task.Status,
		ExpectedPhase:             task.Phase,
		ExpectedOwnerNodeID:       task.OwnerNodeID,
		ExpectedOwnerLeaseUntilMS: task.OwnerLeaseUntilMS,
		ExpectedUpdatedAtMS:       task.UpdatedAtMS,
	}
}

func nextUpdatedAtMS(nowMS, observedMS int64) int64 {
	if nowMS > observedMS {
		return nowMS
	}
	return observedMS + 1
}

func normalizeConfig(cfg Config) Config {
	if cfg.ScanLimit <= 0 {
		cfg.ScanLimit = 64
	}
	if cfg.OwnerLease <= 0 {
		cfg.OwnerLease = 30 * time.Second
	}
	if cfg.RetryBackoff <= 0 {
		cfg.RetryBackoff = time.Second
	}
	if cfg.GCLimit <= 0 {
		cfg.GCLimit = 128
	}
	return cfg
}

type concurrencyLimits struct {
	maxSources int
	maxTargets int
	sources    map[uint64]int
	targets    map[uint64]int
}

func newConcurrencyLimits(cfg Config) *concurrencyLimits {
	return &concurrencyLimits{
		maxSources: cfg.MaxConcurrentSources,
		maxTargets: cfg.MaxConcurrentTargets,
		sources:    make(map[uint64]int),
		targets:    make(map[uint64]int),
	}
}

func (l *concurrencyLimits) Allow(task Task) bool {
	if l.maxSources > 0 && l.sources[task.SourceNode] >= l.maxSources {
		return false
	}
	if l.maxTargets > 0 && l.targets[task.TargetNode] >= l.maxTargets {
		return false
	}
	l.sources[task.SourceNode]++
	l.targets[task.TargetNode]++
	return true
}
