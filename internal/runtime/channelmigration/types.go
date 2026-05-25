package channelmigration

import (
	"context"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
	slotmeta "github.com/WuKongIM/WuKongIM/pkg/db/meta"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

// Task is the durable slot-layer migration task observed by the executor.
type Task = slotmeta.ChannelMigrationTask

// ClaimRequest is the guarded slot-layer owner claim command.
type ClaimRequest = slotmeta.ChannelMigrationTaskClaim

// AdvanceRequest is the guarded slot-layer progress command.
type AdvanceRequest = slotmeta.ChannelMigrationTaskAdvance

// SlotLeadership resolves and verifies local slot leadership before side effects.
type SlotLeadership interface {
	// IsLocalLeader reports whether this node currently leads the slot.
	IsLocalLeader(ctx context.Context, slotID uint32) (bool, error)
	// SlotForChannel resolves the authoritative slot for a channel ID.
	SlotForChannel(channelID string) uint32
}

// Store persists migration task ownership, progress, and retention cleanup.
type Store interface {
	// GetChannelRuntimeMeta reads the authoritative channel runtime metadata.
	GetChannelRuntimeMeta(ctx context.Context, channelID string, channelType int64) (slotmeta.ChannelRuntimeMeta, error)
	// ListRunnableTasksForLocalLeaderSlots returns candidate tasks; the executor still re-checks readiness and leadership.
	ListRunnableTasksForLocalLeaderSlots(ctx context.Context, nowMS int64, limit int) ([]Task, error)
	// ClaimChannelMigrationTask compares and swaps the executor owner lease.
	ClaimChannelMigrationTask(ctx context.Context, req ClaimRequest) error
	// AdvanceChannelMigrationTask compares and swaps durable phase or progress state.
	AdvanceChannelMigrationTask(ctx context.Context, req AdvanceRequest) error
	// SetChannelWriteFence persists a task-owned channel write fence.
	SetChannelWriteFence(ctx context.Context, req slotmeta.ChannelMigrationFenceRequest) error
	// ResetChannelWriteFenceToPreCutover clears an expired pre-commit fence and rewinds task phase.
	ResetChannelWriteFenceToPreCutover(ctx context.Context, req slotmeta.ChannelMigrationResetFenceRequest) error
	// CommitChannelLeaderTransfer commits the fenced leader metadata change.
	CommitChannelLeaderTransfer(ctx context.Context, req slotmeta.ChannelMigrationLeaderTransferRequest) error
	// AddChannelLearner adds the replacement target as a learner and advances the task.
	AddChannelLearner(ctx context.Context, req slotmeta.ChannelMigrationAddLearnerRequest) error
	// PromoteLearnerAndRemoveReplica promotes a proven learner and removes the source replica.
	PromoteLearnerAndRemoveReplica(ctx context.Context, req slotmeta.ChannelMigrationPromoteLearnerRequest) error
	// ClearChannelWriteFence clears a matching migration fence and completes or advances the task.
	ClearChannelWriteFence(ctx context.Context, req slotmeta.ChannelMigrationClearFenceRequest) error
	// GarbageCollectTerminalTasks removes terminal tasks older than beforeMS.
	GarbageCollectTerminalTasks(ctx context.Context, beforeMS int64, limit int) (int, error)
}

// Config controls executor scan, lease, retry, cleanup, and local concurrency behavior.
type Config struct {
	// ScanLimit caps how many runnable tasks one tick reads from the store.
	ScanLimit int
	// OwnerLease is the executor owner lease duration persisted before side effects.
	OwnerLease time.Duration
	// RetryBackoff delays the next attempt after an unimplemented or retryable phase.
	RetryBackoff time.Duration
	// FenceLease is the write-fence lease duration used by migration cutover phases.
	FenceLease time.Duration
	// LeaderLease is the metadata leader lease duration written after leader transfer.
	LeaderLease time.Duration
	// CatchUpStableWindow is how long warm catch-up lag must stay within threshold before cutover.
	CatchUpStableWindow time.Duration
	// CatchUpLagThreshold is the maximum leader-to-target record lag accepted during warm catch-up.
	CatchUpLagThreshold uint64
	// GCRetention keeps terminal tasks visible for operators before cleanup.
	GCRetention time.Duration
	// GCLimit bounds terminal-task cleanup work in one tick.
	GCLimit int
	// MaxConcurrent limits how many migration tasks this node advances in one tick; zero disables the limit.
	MaxConcurrent int
	// MaxConcurrentSources limits how many tasks per source node may run in one tick; zero disables the limit.
	MaxConcurrentSources int
	// MaxConcurrentTargets limits how many tasks per target node may run in one tick; zero disables the limit.
	MaxConcurrentTargets int
}

// ExecutorOptions groups dependencies for constructing an Executor.
type ExecutorOptions struct {
	// Store persists authoritative task state through slot metadata.
	Store Store
	// Slots protects task execution with current local slot leadership checks.
	Slots SlotLeadership
	// Metrics records executor observations; nil installs a no-op implementation.
	Metrics Metrics
	// ProbeClient reads target replica proof reports for catch-up decisions.
	ProbeClient ProbeClient
	// MigrationControl sends drain commands to current channel leader nodes.
	MigrationControl channel.MigrationControlClient
	// LocalNode is the node ID that will own claimed task leases.
	LocalNode channel.NodeID
	// Now supplies wall-clock time for deterministic tests and lease math.
	Now func() time.Time
	// Config controls executor tick behavior.
	Config Config
	// Logger receives executor diagnostics; nil installs a no-op logger.
	Logger wklog.Logger
}

// ProbeClient reads a target replica's durable channel state for migration proof checks.
type ProbeClient interface {
	// ProbeChannel returns the target replica's current durable proof report.
	ProbeChannel(ctx context.Context, nodeID channel.NodeID, meta channel.Meta) (ProbeReport, error)
}

// ProbeReport describes one target replica's durable catch-up state.
type ProbeReport struct {
	// ChannelKey fences the report to one channel runtime.
	ChannelKey channel.ChannelKey
	// ChannelEpoch fences the report to the authoritative channel epoch.
	ChannelEpoch uint64
	// LeaderEpoch fences the report to the authoritative leader epoch.
	LeaderEpoch uint64
	// ReplicaID is the reporting replica node.
	ReplicaID channel.NodeID
	// Leader is the reporting replica's applied leader node.
	Leader channel.NodeID
	// Role is the reporting replica's local role after applying metadata.
	Role channel.ReplicaRole
	// CommitReady reports whether the replica can safely serve as writable leader now.
	CommitReady bool
	// OffsetEpoch is the epoch that owns LogEndOffset when EpochHistory is unavailable.
	OffsetEpoch uint64
	// LogStartOffset is the first local offset available without snapshot bootstrap.
	LogStartOffset uint64
	// LogEndOffset is the durable log end offset visible on the target.
	LogEndOffset uint64
	// CheckpointHW is the target's durable committed high watermark.
	CheckpointHW uint64
	// EpochHistory optionally lets the evaluator prove the epoch at CutoverHW exactly.
	EpochHistory []channel.EpochPoint
	// TruncateTo reports a pending divergence correction before the target can be promoted.
	TruncateTo *uint64
	// SnapshotRequired reports that the target cannot prove the cutover prefix without snapshot bootstrap.
	SnapshotRequired bool
}

// FinalTargetProofRequest asks the evaluator to verify a post-drain target proof.
type FinalTargetProofRequest struct {
	// Meta is the current authoritative channel metadata.
	Meta channel.Meta
	// TargetNode is the desired target replica being proven.
	TargetNode channel.NodeID
	// CutoverLEO is the drained leader LEO stored in the migration task.
	CutoverLEO uint64
	// CutoverHW is the drained leader committed prefix that the target must prove.
	CutoverHW uint64
	// CutoverOffsetEpoch is the epoch that owns CutoverHW on the drained leader.
	CutoverOffsetEpoch uint64
	// Target is the target replica's durable probe report.
	Target ProbeReport
}

// FinalTargetProof is the accepted post-drain target proof used before cutover.
type FinalTargetProof struct {
	// Ready is true only when the target has proven the full cutover prefix.
	Ready bool
	// TargetNode is the proven target replica.
	TargetNode channel.NodeID
	// ChannelEpoch is the authoritative channel epoch used for the proof.
	ChannelEpoch uint64
	// LeaderEpoch is the authoritative leader epoch used for the proof.
	LeaderEpoch uint64
	// CutoverHW is the committed prefix proven on the target.
	CutoverHW uint64
	// CutoverLEO is the drained leader LEO paired with CutoverHW.
	CutoverLEO uint64
	// TargetLEO is the target durable log end offset at proof time.
	TargetLEO uint64
	// TargetCheckpointHW is the target durable checkpoint at proof time.
	TargetCheckpointHW uint64
	// OffsetEpoch is the epoch proven compatible at CutoverHW.
	OffsetEpoch uint64
}

// ProofEvaluator evaluates channel migration proof reports without side effects.
type ProofEvaluator struct{}
