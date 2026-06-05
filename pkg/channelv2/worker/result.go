package worker

import (
	"time"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/store"
	"github.com/WuKongIM/WuKongIM/pkg/channelv2/transport"
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
	RPCPull            *RPCPullResult
	RPCAck             *RPCAckResult
	RPCNotify          *RPCNotifyResult
	RPCPullHint        *RPCPullHintResult
	Value              any
}

// StoreLoadResult returns an opened channel store and its durable initial state.
type StoreLoadResult struct {
	// Store is the opened store handle owned by the reactor after the load result is accepted.
	Store store.ChannelStore
	// Initial is the durable runtime frontier loaded before metadata is applied.
	Initial store.InitialState
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

// StoreApplyResult returns the follower's durable log end offset.
type StoreApplyResult struct {
	// LEO is the follower log end offset after applying records.
	LEO uint64
}

// StoreCheckpointResult marks a completed checkpoint persistence task.
type StoreCheckpointResult struct{}

// StoreCloseResult marks a completed asynchronous store handle close.
type StoreCloseResult struct{}

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

// CompletionSink receives worker completions for routing back to reactors.
type CompletionSink interface {
	Complete(Result)
}
