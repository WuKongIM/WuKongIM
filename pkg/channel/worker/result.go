package worker

import (
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/channel/store"
	"github.com/WuKongIM/WuKongIM/pkg/channel/transport"
)

// Result is the common completion envelope for worker tasks.
type Result struct {
	Kind  TaskKind
	Fence ch.Fence
	Err   error
	// Duration is the worker execution time measured by the owning pool.
	Duration time.Duration

	StoreAppend        *StoreAppendResult
	StoreLoad          *StoreLoadResult
	StoreReadLog       *StoreReadLogResult
	StoreLookupMessage *StoreLookupMessageResult
	StoreApply         *StoreApplyResult
	StoreCheckpoint    *StoreCheckpointResult
	StoreClose         *StoreCloseResult
	StoreRetention     *StoreRetentionResult
	RPCPull            *RPCPullResult
	RPCAck             *RPCAckResult
	RPCNotify          *RPCNotifyResult
	RPCPullHint        *RPCPullHintResult
	MetaResolve        *MetaResolveResult
	Value              any
}

// StoreLoadResult returns an opened channel store and its durable initial state.
type StoreLoadResult struct {
	// Store is the opened store handle owned by the reactor after the load result is accepted.
	Store store.ChannelStore
	// Initial is the durable runtime frontier loaded before metadata is applied.
	Initial store.InitialState
	// Retention is the durable local retention progress loaded before metadata is applied.
	Retention store.RetentionState
}

// StoreAppendResult returns the durable offset range for a leader append.
type StoreAppendResult struct {
	// BaseOffset is the first offset assigned to the appended records.
	BaseOffset uint64
	// LastOffset is the last offset assigned to the appended records.
	LastOffset uint64
}

// StoreReadLogResult contains raw log records read for replication.
type StoreReadLogResult struct {
	// Records are the raw channel log records returned by storage.
	Records []ch.Record
}

// StoreLookupMessageResult contains one durable message lookup result.
type StoreLookupMessageResult struct {
	// Message is the durable row returned by storage when Found is true.
	Message ch.Message
	// Found reports whether storage has a row with the requested message id.
	Found bool
}

// StoreApplyResult returns the follower's durable log frontier and the checkpoint frontier covered by this apply.
type StoreApplyResult struct {
	// LEO is the follower log end offset after applying records.
	LEO uint64
	// CheckpointHW is the committed frontier covered by the atomic record apply.
	CheckpointHW uint64
}

// StoreCheckpointResult marks a completed checkpoint persistence task.
type StoreCheckpointResult struct {
	// Checkpoint is the committed frontier persisted by the task.
	Checkpoint ch.Checkpoint
}

// StoreCloseResult marks a completed asynchronous store handle close.
type StoreCloseResult struct{}

// StoreRetentionResult contains local retention progress after adoption and optional trim.
type StoreRetentionResult struct {
	// ThroughSeq is the requested inclusive retention boundary.
	ThroughSeq uint64
	// LocalRetentionThroughSeq is the local store-adopted boundary after the task.
	LocalRetentionThroughSeq uint64
	// PhysicalRetentionThroughSeq is the highest locally deleted sequence after the task.
	PhysicalRetentionThroughSeq uint64
	// RetainedMaxSeq preserves LEO when the physical tail has been fully removed.
	RetainedMaxSeq uint64
	// DeletedThroughSeq is the highest sequence deleted by this task.
	DeletedThroughSeq uint64
	// Deleted is the number of message rows removed by this task.
	Deleted int
	// More reports whether more rows may still be removable below ThroughSeq.
	More bool
	// TrimAllowed records whether reactor-local safety checks allowed physical deletion.
	TrimAllowed bool
	// TrimSkippedSafe reports that adoption succeeded but physical deletion was deliberately skipped.
	TrimSkippedSafe bool
	// BlockedReason explains why physical deletion was skipped.
	BlockedReason string
}

// RPCPullResult contains the response returned by a remote pull RPC.
type RPCPullResult struct {
	// Response is the leader pull response returned by transport.
	Response transport.PullResponse
}

// RPCAckResult marks a completed remote acknowledgement RPC.
type RPCAckResult struct{}

// RPCNotifyResult marks a completed legacy compatibility nudge RPC.
type RPCNotifyResult struct{}

// RPCPullHintResult marks a completed remote pull hint RPC.
type RPCPullHintResult struct{}

// MetaResolveResult contains authoritative channel metadata returned by the resolver.
type MetaResolveResult struct {
	// Meta is the authoritative metadata resolved for the requested channel identity.
	Meta ch.Meta
}

// CompletionSink receives worker completions for routing back to reactors.
type CompletionSink interface {
	Complete(Result)
}
