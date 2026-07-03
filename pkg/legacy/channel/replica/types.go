package replica

import (
	"context"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/legacy/channel"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
)

type SnapshotApplier interface {
	InstallSnapshot(ctx context.Context, snap channel.Snapshot) error
}

type LogStore interface {
	LEO() uint64
	Append(records []channel.Record) (base uint64, err error)
	Read(from uint64, maxBytes int) ([]channel.Record, error)
	Truncate(to uint64) error
	Sync() error
}

type CheckpointStore interface {
	Load() (channel.Checkpoint, error)
	Store(channel.Checkpoint) error
}

type ApplyFetchStore interface {
	StoreApplyFetch(req channel.ApplyFetchStoreRequest) (leo uint64, err error)
}

type EpochHistoryStore interface {
	Load() ([]channel.EpochPoint, error)
	Append(point channel.EpochPoint) error
	TruncateTo(leo uint64) error
}

type ReconcileProbeSource interface {
	ProbeQuorum(ctx context.Context, meta channel.Meta, local channel.ReplicaState) ([]channel.ReplicaReconcileProof, error)
}

// ExecutionObserver receives low-cardinality pooled execution metrics.
type ExecutionObserver interface {
	SetQueueDepth(int)
	ObserveEnqueue(result string)
	SetWorkerBusyRatio(float64)
	ObserveMailboxWait(time.Duration)
}

// ExecutionMode selects how replica loop and effect work is executed.
type ExecutionMode string

const (
	// ExecutionModeDedicated keeps the legacy per-replica execution workers.
	ExecutionModeDedicated ExecutionMode = "dedicated"
	// ExecutionModePooled uses a shared execution pool for replica loop work.
	ExecutionModePooled ExecutionMode = "pooled"
)

// ExecutionConfig configures replica loop and effect execution.
type ExecutionConfig struct {
	// Mode selects how replica loop and effect work is executed.
	Mode ExecutionMode
	// Pool is the shared worker pool used when Mode is pooled.
	Pool *ExecutionPool
	// MailboxSize bounds per-replica queued loop work in pooled mode.
	MailboxSize int
	// TurnBudget limits how many loop events one worker processes for a replica before yielding.
	TurnBudget int
}

type ReplicaConfig struct {
	LocalNode                   channel.NodeID
	LogStore                    LogStore
	CheckpointStore             CheckpointStore
	ApplyFetchStore             ApplyFetchStore
	EpochHistoryStore           EpochHistoryStore
	SnapshotApplier             SnapshotApplier
	ReconcileProbeSource        ReconcileProbeSource
	Now                         func() time.Time
	AppendGroupCommitMaxWait    time.Duration
	AppendGroupCommitMaxRecords int
	AppendGroupCommitMaxBytes   int
	// Execution configures replica loop and effect execution.
	Execution ExecutionConfig
	// Logger emits replica diagnostics for append wait and lifecycle paths.
	Logger        wklog.Logger
	OnStateChange func()
}

type Replica interface {
	ApplyMeta(meta channel.Meta) error
	BecomeLeader(meta channel.Meta) error
	BecomeFollower(meta channel.Meta) error
	Tombstone() error
	Close() error
	InstallSnapshot(ctx context.Context, snap channel.Snapshot) error
	Append(ctx context.Context, batch []channel.Record) (channel.CommitResult, error)
	Fetch(ctx context.Context, req channel.ReplicaFetchRequest) (channel.ReplicaFetchResult, error)
	ApplyFetch(ctx context.Context, req channel.ReplicaApplyFetchRequest) error
	ApplyProgressAck(ctx context.Context, req channel.ReplicaProgressAckRequest) error
	ApplyReconcileProof(ctx context.Context, proof channel.ReplicaReconcileProof) error
	ApplyRetentionBoundary(ctx context.Context, throughSeq uint64) error
	// FenceAndDrain captures a fenced leader drain proof while blocking further appends.
	FenceAndDrain(ctx context.Context, req channel.FenceAndDrainRequest) (channel.DrainResult, error)
	RetentionView() (channel.RetentionView, error)
	Status() channel.ReplicaState
}
