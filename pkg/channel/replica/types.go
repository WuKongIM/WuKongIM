package replica

import (
	"context"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/channel"
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
	Status() channel.ReplicaState
}
