package store

import (
	"context"

	ch "github.com/WuKongIM/WuKongIM/pkg/channel"
	"github.com/WuKongIM/WuKongIM/pkg/quorumlog"
)

// AppendOutcome is the closed storage proof returned by every leader append.
type AppendOutcome = quorumlog.AppendOutcome

const (
	AppendOutcomeDurable              = quorumlog.AppendOutcomeDurable
	AppendOutcomeAlreadyDurable       = quorumlog.AppendOutcomeAlreadyDurable
	AppendOutcomeDefinitelyNotWritten = quorumlog.AppendOutcomeDefinitelyNotWritten
	AppendOutcomeConflict             = quorumlog.AppendOutcomeConflict
	AppendOutcomeUnknown              = quorumlog.AppendOutcomeUnknown
)

// Factory opens per-channel stores for channel reactors.
type Factory interface {
	ChannelStore(key ch.ChannelKey, id ch.ChannelID) (ChannelStore, error)
}

// LeaderAppendBatcher optionally appends leader batches for multiple channels in one store call.
type LeaderAppendBatcher interface {
	AppendLeaderBatch(ctx context.Context, items []AppendLeaderBatchItem) []AppendLeaderBatchResult
}

// FollowerApplyBatcher optionally applies follower batches for multiple channels in one store call.
type FollowerApplyBatcher interface {
	ApplyFollowerBatch(ctx context.Context, items []ApplyFollowerBatchItem) []ApplyFollowerBatchResult
}

// CheckpointBatcher optionally persists durable HW updates for multiple channels in one store call.
type CheckpointBatcher interface {
	StoreCheckpointBatch(ctx context.Context, items []StoreCheckpointBatchItem) []StoreCheckpointBatchResult
}

// ChannelCatalogLister pages the local message-store channel catalog.
type ChannelCatalogLister interface {
	ListChannelsPage(ctx context.Context, after ch.ChannelKey, limit int) ([]ChannelCatalogEntry, ch.ChannelKey, bool, error)
}

// ChannelStore is the narrow persistence contract used by the channel runtime.
type ChannelStore interface {
	Load(ctx context.Context) (InitialState, error)
	AppendLeader(ctx context.Context, req AppendLeaderRequest) (AppendLeaderResult, error)
	ApplyFollower(ctx context.Context, req ApplyFollowerRequest) (ApplyFollowerResult, error)
	ReadCommitted(ctx context.Context, req ReadCommittedRequest) (ReadCommittedResult, error)
	ReadLog(ctx context.Context, req ReadLogRequest) (ReadLogResult, error)
	LoadRetentionState(ctx context.Context) (RetentionState, error)
	AdoptRetentionBoundary(ctx context.Context, throughSeq uint64, cursorName string) (uint64, error)
	TrimMessagesThrough(ctx context.Context, throughSeq uint64, opts RetentionTrimOptions) (RetentionTrimResult, error)
	// StoreCheckpoint durably records checkpoint HW and must ignore regressive HW updates.
	StoreCheckpoint(ctx context.Context, checkpoint ch.Checkpoint) error
	// Close releases resources owned by this store handle without deleting durable channel state.
	Close() error
}

// ExactStateLoader reads the exact proposal identity at the durable local
// frontier. Implementations must take one consistent append/checkpoint view.
type ExactStateLoader interface {
	LoadExactState(ctx context.Context) (ExactState, error)
}

// ExactRecoveryStateLoader reads one exact frontier plus a bounded,
// position-aligned set of entry identities from the same consistent view.
type ExactRecoveryStateLoader interface {
	LoadExactRecoveryState(ctx context.Context, indexes []uint64) (ExactRecoveryState, error)
}

// RecoverySuffixReplacer atomically replaces an uncommitted divergent suffix.
// The capability is recovery-only and is intentionally not part of ordinary
// append or follower-apply admission.
type RecoverySuffixReplacer interface {
	ReplaceRecoverySuffix(ctx context.Context, req ReplaceRecoverySuffixRequest) (ReplaceRecoverySuffixResult, error)
}

// ExactRecoveryPageReader reads one exact frontier and the largest bounded
// complete-proposal prefix within the requested range from one consistent view.
type ExactRecoveryPageReader interface {
	ReadExactRecoveryPage(ctx context.Context, req ExactRecoveryPageRequest) (ExactRecoveryPage, error)
}

// ExactProposalLookup reads one immutable proposal by its retry-stable command
// identity. It is used only for recovery and exact retry reconciliation.
type ExactProposalLookup interface {
	LoadExactProposal(ctx context.Context, req ExactProposalRequest) (ExactProposal, bool, error)
}

// ExactState is the local durability state required to recover a quorum-log
// sequencer. Manifest and TailIdentity are zero only for an empty log.
type ExactState struct {
	InitialState
	Manifest     ProposalManifest
	TailIdentity ch.EntryIdentity
}

// ExactEntryProbe is one position-aligned recovery identity lookup.
type ExactEntryProbe struct {
	Index    uint64
	Present  bool
	Identity ch.EntryIdentity
}

// ExactRecoveryState is one append/checkpoint-consistent recovery view.
type ExactRecoveryState struct {
	ExactState
	Entries []ExactEntryProbe
}

// RecoveryProposal is one complete exact proposal in a replacement suffix.
type RecoveryProposal struct {
	Manifest ProposalManifest
	Records  []ch.Record
}

// ReplaceRecoverySuffixRequest binds one replacement to the exact inspected
// frontier and a proposal-boundary prefix that must remain unchanged.
type ReplaceRecoverySuffixRequest struct {
	Expected    ExactState
	KeepThrough uint64
	Proposals   []RecoveryProposal
	Committed   uint64
}

// ReplaceRecoverySuffixResult reports the durable frontier after replacement.
type ReplaceRecoverySuffixResult struct {
	LastOffset uint64
	Outcome    AppendOutcome
}

// ExactRecoveryPageRequest bounds one inclusive recovery range.
type ExactRecoveryPageRequest struct {
	From     uint64
	Through  uint64
	MaxBytes int
}

// ExactRecoveryPage is one append/checkpoint-consistent donor page.
type ExactRecoveryPage struct {
	ExactState
	Records []ch.Record
	Entries []ExactEntryProbe
}

// ExactProposal is the complete semantic content sealed by one durable
// proposal manifest.
type ExactProposal struct {
	Manifest ProposalManifest
	Records  []ch.Record
}

// ExactProposalRequest bounds one command-index reconciliation read before
// any record-sized allocation.
type ExactProposalRequest struct {
	CommandID  ch.CommandID
	MaxRecords int
	MaxBytes   int
}

// MessageLookup is an optional point lookup surface for rare timeout recovery paths.
type MessageLookup interface {
	// LookupMessageByID returns a durable row without applying any committed-HW check.
	LookupMessageByID(ctx context.Context, messageID uint64) (ch.Message, bool, error)
}

// IdempotencyLookup is an optional committed-message lookup by sender/client key.
type IdempotencyLookup interface {
	// LookupIdempotency returns the durable row and raw payload hash for one sender/client key.
	LookupIdempotency(ctx context.Context, fromUID string, clientMsgNo string) (IdempotencyHit, bool, error)
}

// SenderSequenceLookup finds the latest sequence sent by one user through an
// explicit committed boundary.
type SenderSequenceLookup interface {
	GetLastSenderMessageSeq(ctx context.Context, fromUID string, throughSeq uint64) (uint64, bool, error)
}

// IdempotencyHit is the durable message selected by an idempotency key.
type IdempotencyHit struct {
	// Message is the durable committed row selected by the sender/client key.
	Message ch.Message
	// PayloadHash is the FNV-64a hash persisted with the idempotency index.
	PayloadHash uint64
}

// InitialState is the durable state loaded before a channel becomes ready.
type InitialState struct {
	LEO          uint64
	HW           uint64
	CheckpointHW uint64
}

// RetentionState records local retention progress for one channel store.
type RetentionState struct {
	// LocalRetentionThroughSeq is the adopted logical retention boundary.
	LocalRetentionThroughSeq uint64
	// PhysicalRetentionThroughSeq is the highest physically deleted sequence.
	PhysicalRetentionThroughSeq uint64
	// RetainedMaxSeq preserves LEO when all rows at the tail are trimmed.
	RetainedMaxSeq uint64
}

// ChannelCatalogEntry describes one locally known channel in the message store.
type ChannelCatalogEntry struct {
	// Key is the stable channel partition key.
	Key ch.ChannelKey
	// ID is the user-facing channel identity.
	ID ch.ChannelID
}

// RetentionTrimOptions bounds one physical retention trim.
type RetentionTrimOptions struct {
	// MaxMessages caps deleted messages when positive.
	MaxMessages int
	// MaxBytes caps deleted payload bytes when positive.
	MaxBytes int
}

// RetentionTrimResult describes one bounded physical retention trim.
type RetentionTrimResult struct {
	// DeletedThroughSeq is the highest sequence deleted by this trim.
	DeletedThroughSeq uint64
	// Deleted is the number of message rows deleted.
	Deleted int
	// More reports whether another trim may still find rows below the boundary.
	More bool
}

// AppendClass separates leader-critical, quorum-follower, and post-quorum
// writes without changing any path's synchronous durability contract.
type AppendClass uint8

const (
	// AppendClassLeaderQuorum is the default for leader-local durability.
	AppendClassLeaderQuorum AppendClass = iota
	// AppendClassFollowerQuorum is a synchronous follower vote. It yields
	// commit selection to leader-local durability because another follower is
	// independently eligible for the same quorum.
	AppendClassFollowerQuorum
	// AppendClassTrailing identifies post-quorum replica convergence.
	AppendClassTrailing
)

// Valid reports whether the append class belongs to the closed store contract.
func (c AppendClass) Valid() bool {
	return c == AppendClassLeaderQuorum || c == AppendClassFollowerQuorum || c == AppendClassTrailing
}

// AppendLeaderRequest persists a leader-owned continuous record batch.
type AppendLeaderRequest struct {
	Records []ch.Record
	// Class controls commit admission priority, never durability or validation.
	Class AppendClass
	// Committed is the monotonic committed frontier persisted atomically with
	// an exact append. It must not exceed Proposal.LastOffset.
	Committed uint64
	// ServerAllocatedMessageIDs proves globally unique allocator-issued IDs for
	// storage's fresh exact-append validation path.
	ServerAllocatedMessageIDs bool
	// ExactBaseOffset requires Records to begin at ExpectedBaseOffset+1 and
	// permits an exact idempotent replay of an already durable range.
	ExactBaseOffset bool
	// ExpectedBaseOffset is the durable frontier preceding an exact append.
	ExpectedBaseOffset uint64
	// Proposal is the immutable durable identity required by exact appends.
	Proposal ProposalManifest
}

// AppendLeaderBatchItem is one channel-scoped leader append inside a store-level batch.
type AppendLeaderBatchItem struct {
	ChannelKey ch.ChannelKey
	ChannelID  ch.ChannelID
	Request    AppendLeaderRequest
}

// AppendLeaderResult returns the durable offset range for a leader append.
type AppendLeaderResult struct {
	BaseOffset uint64
	LastOffset uint64
	// NeedFrom is the exact next offset when this replica has a gap.
	NeedFrom uint64
	// Outcome proves whether this request committed, already existed, was
	// rejected before commit, conflicted, or lost certainty after admission.
	Outcome AppendOutcome
}

// AppendLeaderBatchResult returns the result for one AppendLeaderBatchItem.
type AppendLeaderBatchResult struct {
	BaseOffset uint64
	LastOffset uint64
	// NeedFrom is the exact next offset when this replica has a gap.
	NeedFrom uint64
	Outcome  AppendOutcome
	Err      error
}

// ApplyFollowerRequest persists records received from the leader.
type ApplyFollowerRequest struct {
	Records  []ch.Record
	LeaderHW uint64
}

// ApplyFollowerBatchItem is one channel-scoped follower apply inside a store-level batch.
type ApplyFollowerBatchItem struct {
	ChannelKey ch.ChannelKey
	ChannelID  ch.ChannelID
	Request    ApplyFollowerRequest
}

// ApplyFollowerResult returns the follower's durable log frontier and the checkpoint frontier covered by this apply.
type ApplyFollowerResult struct {
	LEO          uint64
	CheckpointHW uint64
}

// ApplyFollowerBatchResult returns the result for one ApplyFollowerBatchItem.
type ApplyFollowerBatchResult struct {
	LEO          uint64
	CheckpointHW uint64
	Err          error
}

// StoreCheckpointBatchItem is one channel-scoped durable HW update inside a store-level batch.
type StoreCheckpointBatchItem struct {
	ChannelKey ch.ChannelKey
	ChannelID  ch.ChannelID
	Checkpoint ch.Checkpoint
}

// StoreCheckpointBatchResult returns the result for one StoreCheckpointBatchItem.
type StoreCheckpointBatchResult struct {
	Err error
}

// ReadCommittedRequest reads client-visible messages up to MaxSeq.
type ReadCommittedRequest struct {
	FromSeq uint64
	MaxSeq  uint64
	// MinSeq is the lowest visible message sequence for logical compaction.
	MinSeq   uint64
	Limit    int
	MaxBytes int
	// Reverse reads messages at or before FromSeq in descending sequence order.
	Reverse bool
}

// ReadCommittedResult contains committed messages from storage. Messages and
// their payloads are owned by the caller and remain valid after the store
// handle is closed; callers may transfer that ownership without cloning.
type ReadCommittedResult struct {
	Messages []ch.Message
	NextSeq  uint64
}

// ReadLogRequest reads raw log records for replication.
type ReadLogRequest struct {
	FromOffset uint64
	MaxOffset  uint64
	MaxBytes   int
}

// ReadLogResult contains raw log records for follower catch-up.
type ReadLogResult struct {
	Records []ch.Record
}
