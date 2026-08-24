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
	// ProbeIndexes requests exact entry identities from the same consistent
	// frontier view. Entries are returned in this order.
	ProbeIndexes []uint64
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
	// Entries is position-aligned with LoadRequest.ProbeIndexes.
	Entries []EntryProbe
	Err     error
}

// EntryProbe is one exact recovery identity lookup.
type EntryProbe struct {
	Index    uint64
	Present  bool
	Identity ch.EntryIdentity
}

// LoadBatchResult aligns one result with every LoadBatch item.
type LoadBatchResult struct {
	Items []LoadResult
}

// MutationClass distinguishes the durability role of a synchronous mutation.
type MutationClass uint8

const (
	// MutationClassLeaderQuorum is the leader-local vote required by every
	// successful quorum round.
	MutationClassLeaderQuorum MutationClass = iota
	// MutationClassFollowerQuorum is one redundant synchronous follower vote.
	MutationClassFollowerQuorum
	// MutationClassTrailing is post-quorum follower convergence.
	MutationClassTrailing
)

func (c MutationClass) valid() bool {
	return c == MutationClassLeaderQuorum || c == MutationClassFollowerQuorum || c == MutationClassTrailing
}

// Mutation is one synchronous exact replica write. Manifest is mandatory;
// there is no caller-selectable durability or legacy append mode.
type Mutation struct {
	ChannelKey ch.ChannelKey
	ChannelID  ch.ChannelID
	Manifest   ch.ProposalManifest
	Records    []ch.Record
	Committed  uint64
	// Class controls commit selection only; all classes remain synchronous.
	Class MutationClass
	// ServerAllocatedMessageIDs carries the leader's all-record allocator proof.
	ServerAllocatedMessageIDs bool
}

// MutationResult is the closed per-item result of ReplicaStore.Sync.
type MutationResult struct {
	Outcome    ch.AppendOutcome
	LastOffset uint64
	// NeedFrom is the exact next offset when the follower has a gap.
	NeedFrom uint64
	Err      error
}

// RecoveryProposal is one complete immutable proposal in a replacement
// suffix. Its first record follows the preceding proposal without a gap.
type RecoveryProposal struct {
	Manifest ch.ProposalManifest
	Records  []ch.Record
}

// RecoveryReplacement atomically replaces the local suffix after KeepThrough.
// Expected fences the operation to the exact local frontier inspected by the
// recovery owner; Committed may advance but never regress.
type RecoveryReplacement struct {
	ChannelKey  ch.ChannelKey
	ChannelID   ch.ChannelID
	Expected    ReplicaState
	KeepThrough uint64
	Proposals   []RecoveryProposal
	Committed   uint64
}

// RecoveryReplacementResult is the closed durable outcome of one replacement.
type RecoveryReplacementResult struct {
	Outcome    ch.AppendOutcome
	LastOffset uint64
	Err        error
}

// FetchRange reads the largest non-empty complete-proposal prefix beginning at
// From and ending no later than Through from the exact proved donor frontier.
type FetchRange struct {
	ChannelKey ch.ChannelKey
	ChannelID  ch.ChannelID
	Expected   ReplicaState
	From       uint64
	Through    uint64
	Previous   ch.EntryIdentity
	MaxBytes   int
}

// FetchRangeResult is one position-aligned exact donor page.
type FetchRangeResult struct {
	State     ReplicaState
	Proposals []RecoveryProposal
	Err       error
}

// CommandLookup identifies one durable proposal by retry-stable command.
type CommandLookup struct {
	// ChannelKey and ChannelID select the exact local replica.
	ChannelKey ch.ChannelKey
	ChannelID  ch.ChannelID
	// CommandID is the immutable proposal identity to reconcile.
	CommandID ch.CommandID
	// MaxRecords and MaxBytes bound the result before payload allocation.
	MaxRecords int
	MaxBytes   int
}

// CommandLookupResult is one exact local command-index result.
type CommandLookupResult struct {
	// Manifest and Records contain the complete immutable proposal when Found.
	Manifest ch.ProposalManifest
	Records  []ch.Record
	// Found distinguishes an absent command from an empty proposal, which is invalid.
	Found bool
	Err   error
}

type commandStore interface {
	LookupCommands(context.Context, []CommandLookup) []CommandLookupResult
}

// ReplicaStore loads exact durable frontiers and synchronously applies exact
// immutable mutations. Returning from Sync with a durable outcome proves
// physical durability for that item.
type ReplicaStore interface {
	Load(context.Context, LoadBatch) (LoadBatchResult, error)
	Sync(context.Context, []Mutation) []MutationResult
	Replace(context.Context, []RecoveryReplacement) []RecoveryReplacementResult
	Fetch(context.Context, []FetchRange) []FetchRangeResult
}
