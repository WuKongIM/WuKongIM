package replication

import (
	"context"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
)

// LoadRequest identifies one local replica whose exact durable frontier must
// be recovered before sequencing or authority installation.
type LoadRequest struct {
	ChannelKey ch.ChannelKey
	ChannelID  ch.ChannelID
}

// LoadBatch groups a bounded set of local replica frontier reads.
type LoadBatch struct {
	Items []LoadRequest
}

// ReplicaState is the exact durable frontier of one local replica. Manifest
// and TailIdentity are both zero only when LEO is zero.
type ReplicaState struct {
	LEO          uint64
	Committed    uint64
	Manifest     ch.ProposalManifest
	TailIdentity ch.EntryIdentity
}

// LoadResult is one position-aligned local replica read.
type LoadResult struct {
	State ReplicaState
	Err   error
}

// LoadBatchResult aligns one result with every LoadBatch item.
type LoadBatchResult struct {
	Items []LoadResult
}

// Mutation is one synchronous exact follower write. Manifest is mandatory;
// there is no caller-selectable durability or legacy append mode.
type Mutation struct {
	ChannelKey ch.ChannelKey
	ChannelID  ch.ChannelID
	Manifest   ch.ProposalManifest
	Records    []ch.Record
	Committed  uint64
}

// MutationResult is the closed per-item result of ReplicaStore.Sync.
type MutationResult struct {
	Outcome    ch.AppendOutcome
	LastOffset uint64
	// NeedFrom is the exact next offset when the follower has a gap.
	NeedFrom uint64
	Err      error
}

// ReplicaStore loads exact durable frontiers and synchronously applies exact
// immutable mutations. Returning from Sync with a durable outcome proves
// physical durability for that item.
type ReplicaStore interface {
	Load(context.Context, LoadBatch) (LoadBatchResult, error)
	Sync(context.Context, []Mutation) []MutationResult
}
