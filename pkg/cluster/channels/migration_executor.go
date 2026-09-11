package channels

import (
	"context"
	"errors"
	"fmt"
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	metadb "github.com/WuKongIM/WuKongIM/pkg/db/meta"
)

const defaultMigrationExecutorTaskLimit = 1

// A stalled peer must yield the shared repair supervisor within a bounded tick.
const defaultMigrationExecutorTimeout = 5 * time.Second
const maxMigrationExecutorPhases = 8

// MigrationTaskSource lists active migration work currently runnable by this executor.
type MigrationTaskSource interface {
	ListRunnableMigrationTasks(ctx context.Context, localNode uint64, limit int) ([]metadb.ChannelMigrationTask, error)
}

// MigrationTaskStore advances migration tasks through Slot-owned guarded commands.
type MigrationTaskStore interface {
	Claim(ctx context.Context, task metadb.ChannelMigrationTask, expectedVersion int64) error
	Advance(ctx context.Context, task metadb.ChannelMigrationTask, expectedVersion int64, phase metadb.ChannelMigrationPhase, status metadb.ChannelMigrationStatus, reason string) error
	AdvanceWithProof(ctx context.Context, task metadb.ChannelMigrationTask, expectedVersion int64, phase metadb.ChannelMigrationPhase, status metadb.ChannelMigrationStatus, reason string, progress metadb.ChannelMigrationProgress, proof metadb.ChannelMigrationCutoverProof) error
	SetWriteFence(ctx context.Context, task metadb.ChannelMigrationTask, reason ch.WriteFenceReason) error
	CommitLeaderTransfer(ctx context.Context, task metadb.ChannelMigrationTask) error
	AddLearner(ctx context.Context, task metadb.ChannelMigrationTask) error
	PromoteLearnerAndRemoveSource(ctx context.Context, task metadb.ChannelMigrationTask) error
	ClearWriteFence(ctx context.Context, task metadb.ChannelMigrationTask) error
}

// MigrationTaskProgressReader rereads the exact selected task after a successful
// durable step; runtime caches and speculative phase changes are not progress.
type MigrationTaskProgressReader interface {
	Get(context.Context, ch.ChannelID, string) (metadb.ChannelMigrationTask, bool, error)
}

// MigrationRuntime reads remote/local Channel runtime proof for migration phases.
type MigrationRuntime interface {
	ProbeChannel(ctx context.Context, nodeID uint64, channelID string, channelType uint8) (ch.RuntimeProbeChannel, error)
	DrainChannel(ctx context.Context, nodeID uint64, req ch.DrainChannelRequest) (ch.DrainChannelResult, error)
	ApplyChannelMeta(ctx context.Context, nodeID uint64, meta metadb.ChannelRuntimeMeta) error
}

// MigrationObserver receives low-cardinality migration executor observations.
type MigrationObserver interface {
	MigrationActiveTasks(count int)
	MigrationPhase(taskID string, taskType metadb.ChannelMigrationKind, phase metadb.ChannelMigrationPhase, status metadb.ChannelMigrationStatus, reason string)
	MigrationDuration(taskType metadb.ChannelMigrationKind, phase metadb.ChannelMigrationPhase, d time.Duration)
	MigrationBlocked(reason string)
	WriteFenceActive(count int)
	WriteFenceDuration(taskID string, fenceVersion uint64, d time.Duration)
}

// MigrationExecutorConfig wires a bounded Channel migration executor.
type MigrationExecutorConfig struct {
	// LocalNode is the owner node id used when claiming tasks.
	LocalNode uint64
	// Source lists runnable active tasks for this node's owned Slot scope.
	Source MigrationTaskSource
	// Store persists phase transitions through Slot-owned guarded commands.
	Store MigrationTaskStore
	// Runtime probes and drains Channel runtimes.
	Runtime MigrationRuntime
	// Meta reads authoritative runtime metadata.
	Meta RuntimeMetaReader
	// Observer records phase metrics. Nil is allowed.
	Observer MigrationObserver
	// Clock returns current wall-clock time for lease checks. Nil uses time.Now.
	Clock func() time.Time
	// TaskLimit bounds tasks inspected per RunOnce tick. Zero uses one task.
	TaskLimit int
	// FailoverPhaseLimit bounds automatic failover steps per tick. Zero uses one; the maximum is eight.
	// Planned transfers and replica replacement retain one-step asynchronous boundaries.
	FailoverPhaseLimit int
}

// MigrationExecutor advances one selected task through a bounded number of durable steps per tick.
type MigrationExecutor struct {
	localNode      uint64
	source         MigrationTaskSource
	store          MigrationTaskStore
	runtime        MigrationRuntime
	meta           RuntimeMetaReader
	observer       MigrationObserver
	clock          func() time.Time
	taskLimit      int
	phaseLimit     int
	progressReader MigrationTaskProgressReader
}

// NewMigrationExecutor creates a migration executor.
func NewMigrationExecutor(cfg MigrationExecutorConfig) *MigrationExecutor {
	clock := cfg.Clock
	if clock == nil {
		clock = time.Now
	}
	limit := cfg.TaskLimit
	if limit <= 0 {
		limit = defaultMigrationExecutorTaskLimit
	}
	phaseLimit := cfg.FailoverPhaseLimit
	if phaseLimit <= 0 {
		phaseLimit = 1
	}
	if phaseLimit > maxMigrationExecutorPhases {
		phaseLimit = maxMigrationExecutorPhases
	}
	reader, _ := cfg.Store.(MigrationTaskProgressReader)
	return &MigrationExecutor{
		localNode:      cfg.LocalNode,
		source:         cfg.Source,
		store:          cfg.Store,
		runtime:        cfg.Runtime,
		meta:           cfg.Meta,
		observer:       cfg.Observer,
		clock:          clock,
		taskLimit:      limit,
		phaseLimit:     phaseLimit,
		progressReader: reader,
	}
}

// RunOnce advances a bounded task page within one time budget. Failover tasks
// may take several durable steps; waiting or failed tasks yield to their peers.
func (e *MigrationExecutor) RunOnce(ctx context.Context) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if e == nil || e.localNode == 0 || e.source == nil || e.store == nil || e.runtime == nil || e.meta == nil || (e.phaseLimit > 1 && e.progressReader == nil) {
		return fmt.Errorf("%w: migration executor is not fully configured", ch.ErrInvalidConfig)
	}
	ctx, cancel := context.WithTimeout(ctx, defaultMigrationExecutorTimeout)
	defer cancel()
	var firstErr error
	type taskIdentity struct {
		channelID   string
		channelType int64
		taskID      string
	}
	seen := make(map[taskIdentity]struct{}, e.taskLimit)
	for inspected := 0; inspected < e.taskLimit; inspected++ {
		if err := ctxErr(ctx); err != nil {
			return errors.Join(err, firstErr)
		}
		// Advance the source cursor only for work about to be attempted. A slow
		// task cannot skip its unstarted peers, and each lookup rechecks Slot ownership.
		tasks, err := e.source.ListRunnableMigrationTasks(ctx, e.localNode, 1)
		if err != nil {
			return errors.Join(err, firstErr)
		}
		if len(tasks) == 0 {
			break
		}
		e.observeMigrationTaskCounts(tasks[:1])
		task := tasks[0]
		identity := taskIdentity{task.ChannelID, task.ChannelType, task.TaskID}
		if _, duplicate := seen[identity]; duplicate {
			break
		}
		seen[identity] = struct{}{}
		if task.IsTerminal() || task.Status == metadb.ChannelMigrationStatusBlocked {
			continue
		}
		if task.OwnerNodeID != e.localNode && task.OwnerNodeID != 0 && task.OwnerLeaseUntilMS > e.clock().UnixMilli() {
			continue
		}
		if err := e.advanceSelectedTask(ctx, task); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// advanceSelectedTask follows only a fresh durable version of the same identity.
// An unchanged asynchronous phase yields; it cannot spin or consume another task.
func (e *MigrationExecutor) advanceSelectedTask(ctx context.Context, task metadb.ChannelMigrationTask) (err error) {
	// Capture the fresh phase at failure, not the phase selected before durable
	// progress. The node bounds the resulting diagnostic log size and frequency.
	defer func() {
		if err != nil {
			err = fmt.Errorf("migration task %q channel %q/%d phase %d: %w", task.TaskID, task.ChannelID, task.ChannelType, task.Phase, err)
		}
	}()
	phaseLimit := e.phaseLimit
	// Only automatic failover has no planned catch-up boundary between these steps.
	if task.Kind != metadb.ChannelMigrationKindLeaderFailover {
		phaseLimit = 1
	}
	for step := 0; step < phaseLimit; step++ {
		if err := ctxErr(ctx); err != nil {
			return err
		}
		err := e.runSelectedStep(ctx, task)
		if isMigrationVersionConflict(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if step+1 == phaseLimit {
			return nil
		}
		id, err := migrationChannelIDFromTask(task)
		if err != nil {
			return err
		}
		latest, ok, err := e.progressReader.Get(ctx, id, task.TaskID)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		if latest.TaskID != task.TaskID || latest.ChannelID != task.ChannelID || latest.ChannelType != task.ChannelType {
			return fmt.Errorf("%w: migration task progress identity changed", ch.ErrInvalidConfig)
		}
		if latest.IsTerminal() || latest.Status == metadb.ChannelMigrationStatusBlocked || latest.UpdatedAtMS <= task.UpdatedAtMS || latest.OwnerNodeID != e.localNode {
			return nil
		}
		task = latest
	}
	return nil
}

func (e *MigrationExecutor) runSelectedStep(ctx context.Context, task metadb.ChannelMigrationTask) error {
	nowMS := e.clock().UnixMilli()
	if task.OwnerNodeID != e.localNode || task.OwnerLeaseUntilMS <= nowMS {
		return e.store.Claim(ctx, task, task.UpdatedAtMS)
	}
	if shouldRenewMigrationFence(task, nowMS) {
		return e.store.SetWriteFence(ctx, task, migrationFenceReasonForTask(task))
	}
	start := e.clock()
	err := e.runTaskPhase(ctx, task)
	e.observeMigrationDuration(task.Kind, task.Phase, start)
	return err
}

func isMigrationVersionConflict(err error) bool {
	return errors.Is(err, metadb.ErrStaleMeta)
}

func migrationChannelIDFromTask(task metadb.ChannelMigrationTask) (ch.ChannelID, error) {
	return migrationTaskChannelID(task)
}

func (e *MigrationExecutor) runTaskPhase(ctx context.Context, task metadb.ChannelMigrationTask) error {
	switch task.Kind {
	case metadb.ChannelMigrationKindLeaderTransfer, metadb.ChannelMigrationKindLeaderFailover:
		return e.runLeaderTransferPhase(ctx, task)
	case metadb.ChannelMigrationKindReplicaReplace:
		return e.runReplicaReplacePhase(ctx, task)
	default:
		return fmt.Errorf("%w: unknown migration task kind %d", ch.ErrInvalidConfig, task.Kind)
	}
}

func (e *MigrationExecutor) clearWriteFenceAndObserve(ctx context.Context, task metadb.ChannelMigrationTask) error {
	fenceVersion := task.FenceVersion
	fenceStartedAt := migrationObservedFenceStart(task, e.clock())
	if err := e.store.ClearWriteFence(ctx, task); err != nil {
		return err
	}
	if e.observer != nil {
		if fenceVersion != 0 {
			e.observer.WriteFenceDuration(task.TaskID, fenceVersion, nonNegativeDuration(e.clock().Sub(fenceStartedAt)))
		}
		e.observer.MigrationPhase(task.TaskID, task.Kind, metadb.ChannelMigrationPhaseClearFence, metadb.ChannelMigrationStatusCompleted, "")
	}
	return nil
}

func (e *MigrationExecutor) observeMigrationTaskCounts(tasks []metadb.ChannelMigrationTask) {
	if e.observer == nil {
		return
	}
	active := 0
	fenced := 0
	for _, task := range tasks {
		if !task.IsTerminal() {
			active++
		}
		if task.FenceToken == task.TaskID && task.FenceVersion != 0 {
			fenced++
		}
	}
	e.observer.MigrationActiveTasks(active)
	e.observer.WriteFenceActive(fenced)
}

func (e *MigrationExecutor) observeMigrationDuration(kind metadb.ChannelMigrationKind, phase metadb.ChannelMigrationPhase, start time.Time) {
	if e.observer == nil {
		return
	}
	e.observer.MigrationDuration(kind, phase, nonNegativeDuration(e.clock().Sub(start)))
}

func (e *MigrationExecutor) observeMigrationBlocked(task metadb.ChannelMigrationTask, reason string) {
	if e.observer == nil {
		return
	}
	e.observer.MigrationBlocked(reason)
	e.observer.MigrationPhase(task.TaskID, task.Kind, task.Phase, metadb.ChannelMigrationStatusBlocked, reason)
}

func migrationObservedFenceStart(task metadb.ChannelMigrationTask, now time.Time) time.Time {
	if task.UpdatedAtMS <= 0 {
		return now
	}
	return time.UnixMilli(task.UpdatedAtMS)
}

func nonNegativeDuration(d time.Duration) time.Duration {
	if d < 0 {
		return 0
	}
	return d
}

func shouldRenewMigrationFence(task metadb.ChannelMigrationTask, nowMS int64) bool {
	if task.FenceToken != task.TaskID || task.FenceVersion == 0 || task.FenceUntilMS <= 0 || task.FenceUntilMS > nowMS {
		return false
	}
	if task.Kind == metadb.ChannelMigrationKindLeaderTransfer || task.Kind == metadb.ChannelMigrationKindLeaderFailover {
		switch task.Phase {
		case metadb.ChannelMigrationPhaseDrainLeader,
			metadb.ChannelMigrationPhaseFinalTargetCatchUp,
			metadb.ChannelMigrationPhaseCommitLeaderMeta:
			return true
		default:
			return false
		}
	}
	if task.Kind == metadb.ChannelMigrationKindReplicaReplace {
		switch task.Phase {
		case metadb.ChannelMigrationPhaseCutoverFence,
			metadb.ChannelMigrationPhaseFinalTargetCatchUp,
			metadb.ChannelMigrationPhasePromoteAndRemove:
			return true
		default:
			return false
		}
	}
	return false
}

func migrationFenceReasonForTask(task metadb.ChannelMigrationTask) ch.WriteFenceReason {
	if task.Kind == metadb.ChannelMigrationKindReplicaReplace {
		return ch.WriteFenceReasonReplicaReplace
	}
	if task.Kind == metadb.ChannelMigrationKindLeaderFailover {
		return ch.WriteFenceReasonFailover
	}
	return ch.WriteFenceReasonLeaderTransfer
}
