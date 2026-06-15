package workqueue

import "time"

const (
	observationCapacity  = "capacity"
	observationDepth     = "depth"
	observationAdmission = "admission"
	observationWait      = "wait"
	observationTask      = "task"
	observationWorker    = "worker"
	observationBatch     = "batch"

	resultOK       = "ok"
	resultFull     = "full"
	resultClosed   = "closed"
	resultCanceled = "canceled"
	resultTimeout  = "timeout"
	resultError    = "error"
	resultPanic    = "panic"
)

// BoundedPoolObservation describes one low-cardinality bounded-pool event.
type BoundedPoolObservation struct {
	// Name is the stable pool name supplied by BoundedPoolConfig.
	Name string
	// Kind identifies the observation category, such as admission, depth, or task.
	Kind string
	// Result is a low-cardinality outcome label.
	Result string
	// QueueDepth is the current accepted-but-not-yet-executing item count.
	QueueDepth int
	// QueueCapacity is the configured admission capacity.
	QueueCapacity int
	// Running is the number of currently executing worker tasks.
	Running int
	// Workers is the configured worker capacity.
	Workers int
	// Waiting is the current number of calls waiting inside the ants executor.
	Waiting int
	// Wait is the queue wait time before a task starts executing.
	Wait time.Duration
	// Duration is the handler execution duration.
	Duration time.Duration
	// Err is the concrete task error for adapters that need local classification.
	Err error
}

// BoundedPoolObserver receives bounded-pool events.
type BoundedPoolObserver interface {
	// ObserveBoundedPool records one low-level bounded pool observation.
	ObserveBoundedPool(BoundedPoolObservation)
}

// ShardedMailboxObservation describes one low-cardinality sharded-mailbox event.
type ShardedMailboxObservation struct {
	// Name is the stable mailbox name supplied by ShardedMailboxConfig.
	Name string
	// Shard is the shard index that produced the observation.
	Shard int
	// Kind identifies the observation category, such as admission, depth, or batch.
	Kind string
	// Result is a low-cardinality outcome label.
	Result string
	// QueueDepth is the current queued item count for the shard.
	QueueDepth int
	// QueueCapacity is the configured per-shard admission capacity.
	QueueCapacity int
	// Running is the number of currently executing shard drains.
	Running int
	// Workers is the configured worker capacity.
	Workers int
	// Waiting is the current number of calls waiting inside the ants executor.
	Waiting int
	// BatchSize is the number of items delivered to the mailbox handler.
	BatchSize int
	// Wait is the time from the first item enqueue to handler start.
	Wait time.Duration
	// Duration is the handler execution duration.
	Duration time.Duration
	// Err is the concrete handler error for adapters that need local classification.
	Err error
}

// ShardedMailboxObserver receives sharded-mailbox events.
type ShardedMailboxObserver interface {
	// ObserveShardedMailbox records one low-level sharded mailbox observation.
	ObserveShardedMailbox(ShardedMailboxObservation)
}
