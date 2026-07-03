package channel

import (
	"context"
	"time"

	channelcompat "github.com/WuKongIM/WuKongIM/pkg/db/message/channelcompat"
)

type NodeID uint64
type ChannelKey = channelcompat.ChannelKey
type ChannelID = channelcompat.ChannelID

type Role uint8
type Status uint8
type MessageSeqFormat uint8

const (
	StatusCreating Status = iota + 1
	StatusActive
	StatusDeleting
	StatusDeleted
)

const (
	MessageSeqFormatLegacyU32 MessageSeqFormat = iota + 1
	MessageSeqFormatU64
)

type Features struct {
	MessageSeqFormat MessageSeqFormat
}

// CommitMode controls when an append request completes.
type CommitMode uint8

const (
	// CommitModeQuorum waits for the leader to reach quorum commit before completing.
	CommitModeQuorum CommitMode = iota + 1
	// CommitModeLocal completes after the leader durably appends the record batch.
	CommitModeLocal
)

type Message = channelcompat.Message

type Meta struct {
	Key   ChannelKey
	ID    ChannelID
	Epoch uint64
	// RouteGeneration is the authoritative version of the channel routing record.
	RouteGeneration uint64
	LeaderEpoch     uint64
	Leader          NodeID
	Replicas        []NodeID
	ISR             []NodeID
	MinISR          int
	LeaseUntil      time.Time
	Status          Status
	Features        Features
	// RetentionThroughSeq is the authoritative highest message sequence hidden by retention.
	RetentionThroughSeq uint64
	// WriteFence rejects new appends while a migration owns the channel cutover.
	WriteFence WriteFence
}

// WriteFenceReason explains why channel writes are currently fenced.
type WriteFenceReason uint8

const (
	// WriteFenceReasonMigration indicates writes are fenced by channel migration.
	WriteFenceReasonMigration WriteFenceReason = 1
)

// WriteFence is the channel runtime projection of an authoritative write fence.
type WriteFence struct {
	// Token identifies the task that owns the current write fence.
	Token string
	// Version is the monotonic fence generation applied from authoritative metadata.
	Version uint64
	// Reason explains why writes are currently fenced.
	Reason WriteFenceReason
	// Until is the fence lease deadline from authoritative metadata.
	Until time.Time
}

// Active reports whether the fence currently rejects new writes.
func (f WriteFence) Active(now time.Time) bool {
	return f.Token != "" && !now.After(f.Until)
}

// BlocksAppend reports whether local append admission must fail closed until authoritative metadata clears the fence.
func (f WriteFence) BlocksAppend() bool {
	return f.Token != ""
}

// FenceAndDrainRequest asks the current channel leader to prove a fenced cutover point.
type FenceAndDrainRequest struct {
	// ChannelKey identifies the channel runtime to drain.
	ChannelKey ChannelKey
	// TaskID identifies the migration task requesting the drain.
	TaskID string
	// WriteFenceToken identifies the authoritative migration write fence.
	WriteFenceToken string
	// WriteFenceVersion is the authoritative migration write fence generation.
	WriteFenceVersion uint64
	// ExpectedChannelEpoch fences the drain to the authoritative channel epoch.
	ExpectedChannelEpoch uint64
	// ExpectedLeaderEpoch fences the drain to the authoritative leader epoch.
	ExpectedLeaderEpoch uint64
	// ExpectedLeader fences the drain to the current local leader.
	ExpectedLeader NodeID
}

// DrainResult is a leader-side proof captured after append admission is fail-closed.
type DrainResult struct {
	// ChannelKey identifies the drained channel runtime.
	ChannelKey ChannelKey
	// LEO is the leader log end offset at the drain point.
	LEO uint64
	// HW is the committed high watermark at the drain point.
	HW uint64
	// CheckpointHW is the durable checkpoint high watermark at the drain point.
	CheckpointHW uint64
	// ChannelEpoch is the applied channel metadata epoch.
	ChannelEpoch uint64
	// LeaderEpoch is the applied leader epoch.
	LeaderEpoch uint64
	// WriteFenceVersion is the matching fence generation used for the drain proof.
	WriteFenceVersion uint64
	// RuntimeGeneration is the node-local runtime generation that produced the proof.
	RuntimeGeneration uint64
}

// MigrationControlClient sends migration control operations to channel leaders.
type MigrationControlClient interface {
	// FenceAndDrain asks the peer to validate the fenced leader state and return a drain proof.
	FenceAndDrain(ctx context.Context, nodeID NodeID, req FenceAndDrainRequest) (DrainResult, error)
}

type AppendRequest struct {
	ChannelID             ChannelID
	Message               Message
	SupportsMessageSeqU64 bool
	// CommitMode defaults to CommitModeQuorum when unset.
	CommitMode           CommitMode
	ExpectedChannelEpoch uint64
	ExpectedLeaderEpoch  uint64
	// TraceID is the diagnostics trace identifier propagated with this append request.
	TraceID string
	// Attempt records the diagnostics-only append attempt number; it is not persisted or encoded.
	Attempt int
}

type AppendResult struct {
	MessageID  uint64
	MessageSeq uint64
	Message    Message
}

// AppendBatchRequest appends multiple messages to one channel in strict request order.
type AppendBatchRequest struct {
	ChannelID             ChannelID
	Messages              []Message
	SupportsMessageSeqU64 bool
	// CommitMode defaults to CommitModeQuorum when unset.
	CommitMode           CommitMode
	ExpectedChannelEpoch uint64
	ExpectedLeaderEpoch  uint64
	// TraceID is the diagnostics trace identifier propagated with this append request.
	TraceID string
	// Attempt records the diagnostics-only append attempt number; it is not persisted or encoded.
	Attempt int
}

// AppendBatchResult returns per-message append results aligned with the request.
type AppendBatchResult struct {
	Items []AppendBatchItemResult
}

// AppendBatchItemResult is one append result inside a batch.
type AppendBatchItemResult struct {
	MessageID  uint64
	MessageSeq uint64
	Message    Message
	Err        error
}

type FetchRequest struct {
	ChannelID            ChannelID
	FromSeq              uint64
	Limit                int
	MaxBytes             int
	ExpectedChannelEpoch uint64
	ExpectedLeaderEpoch  uint64
}

type FetchResult struct {
	Messages     []Message
	NextSeq      uint64
	CommittedSeq uint64
	// MinAvailableSeq is the first message sequence available to clients.
	MinAvailableSeq uint64
	// RetentionThroughSeq is the authoritative highest retained-away sequence.
	RetentionThroughSeq uint64
}

type ChannelRuntimeStatus struct {
	Key          ChannelKey
	ID           ChannelID
	Status       Status
	Leader       NodeID
	LeaderEpoch  uint64
	HW           uint64
	CommittedSeq uint64
	// MinAvailableSeq is the first message sequence available to clients.
	MinAvailableSeq uint64
	// RetentionThroughSeq is the authoritative highest retained-away sequence.
	RetentionThroughSeq uint64
}

type Record = channelcompat.Record
type Checkpoint = channelcompat.Checkpoint
type EpochPoint = channelcompat.EpochPoint
type IdempotencyKey = channelcompat.IdempotencyKey
type IdempotencyEntry = channelcompat.IdempotencyEntry
type ApplyFetchStoreRequest = channelcompat.ApplyFetchStoreRequest

type ReplicaRole uint8

const (
	ReplicaRoleFollower ReplicaRole = iota + 1
	ReplicaRoleLeader
	ReplicaRoleFencedLeader
	ReplicaRoleTombstoned
)

type ReplicaState struct {
	ChannelKey     ChannelKey
	Role           ReplicaRole
	Epoch          uint64
	OffsetEpoch    uint64
	Leader         NodeID
	LogStartOffset uint64
	HW             uint64
	CheckpointHW   uint64
	CommitReady    bool
	LEO            uint64
	// RetentionThroughSeq is the local logical retention fence applied from metadata.
	RetentionThroughSeq uint64
	// MinAvailableSeq is the first message sequence that reads may return.
	MinAvailableSeq uint64
	// LocalRetentionThroughSeq is the highest authoritative retention boundary durably adopted locally.
	LocalRetentionThroughSeq uint64
	// PhysicalRetentionThroughSeq is the highest message sequence physically trimmed locally.
	PhysicalRetentionThroughSeq uint64
}

type RetentionState = channelcompat.RetentionState

// RetentionReset tells a follower to adopt the authoritative retention floor
// before continuing replication from MinAvailableSeq.
type RetentionReset struct {
	// RetentionThroughSeq is the authoritative highest sequence hidden by retention.
	RetentionThroughSeq uint64
	// RetainedThroughOffset is the highest log offset the follower must treat as unavailable.
	RetainedThroughOffset uint64
	// MinAvailableSeq is the first sequence the follower should fetch next.
	MinAvailableSeq uint64
}

// RetentionView exposes replica retention progress for retention planning.
type RetentionView struct {
	// ChannelKey identifies the channel this view belongs to.
	ChannelKey ChannelKey
	// Epoch is the channel epoch of this local replica view.
	Epoch uint64
	// LeaderEpoch is the current metadata leader epoch.
	LeaderEpoch uint64
	// Leader is the current metadata leader node.
	Leader NodeID
	// LeaseUntil is the current leader lease deadline.
	LeaseUntil time.Time
	// HW is the runtime committed high watermark.
	HW uint64
	// CheckpointHW is the durable committed high watermark.
	CheckpointHW uint64
	// LEO is the local log end offset, including any retained LEO floor.
	LEO uint64
	// CommitReady reports whether the replica can safely admit leader appends.
	CommitReady bool
	// RetentionThroughSeq is the authoritative logical retention boundary applied locally.
	RetentionThroughSeq uint64
	// MinAvailableSeq is the first sequence readable after retention and snapshots.
	MinAvailableSeq uint64
	// LocalRetentionThroughSeq is the highest retention boundary durably adopted locally.
	LocalRetentionThroughSeq uint64
	// PhysicalRetentionThroughSeq is the highest retention boundary physically trimmed locally.
	PhysicalRetentionThroughSeq uint64
	// MinISRMatchOffset is the minimum observed retention progress across current ISR members.
	MinISRMatchOffset uint64
}

// EffectiveMinAvailableSeq returns the first sequence readable after logical retention and physical trimming.
func EffectiveMinAvailableSeq(retentionThroughSeq, logStartOffset uint64) uint64 {
	nextSeq := func(seq uint64) uint64 {
		if seq == ^uint64(0) {
			return seq
		}
		return seq + 1
	}

	minSeq := uint64(1)
	if next := nextSeq(retentionThroughSeq); next > minSeq {
		minSeq = next
	}
	if next := nextSeq(logStartOffset); next > minSeq {
		minSeq = next
	}
	return minSeq
}

type CommitResult struct {
	BaseOffset   uint64
	NextCommitHW uint64
	RecordCount  int
}

type Snapshot = channelcompat.Snapshot

type ReplicaFetchRequest struct {
	ChannelKey  ChannelKey
	Epoch       uint64
	ReplicaID   NodeID
	FetchOffset uint64
	OffsetEpoch uint64
	MaxBytes    int
}

type ReplicaFetchResult struct {
	Epoch          uint64
	HW             uint64
	Records        []Record
	TruncateTo     *uint64
	RetentionReset *RetentionReset
}

type ReplicaApplyFetchRequest struct {
	ChannelKey ChannelKey
	Epoch      uint64
	Leader     NodeID
	TruncateTo *uint64
	Records    []Record
	LeaderHW   uint64
}

type ReplicaProgressAckRequest struct {
	ChannelKey  ChannelKey
	Epoch       uint64
	ReplicaID   NodeID
	MatchOffset uint64
}

type ReplicaFollowerCursorUpdate struct {
	ChannelKey  ChannelKey
	Epoch       uint64
	ReplicaID   NodeID
	MatchOffset uint64
	OffsetEpoch uint64
}

type ReplicaReconcileProof struct {
	ChannelKey ChannelKey
	Epoch      uint64
	// LeaderEpoch fences the proof to the leader meta generation that requested it.
	LeaderEpoch  uint64
	ReplicaID    NodeID
	OffsetEpoch  uint64
	LogEndOffset uint64
	CheckpointHW uint64
}

type commitModeContextKey struct{}

// WithCommitMode returns a context that carries the append commit mode.
func WithCommitMode(ctx context.Context, mode CommitMode) context.Context {
	return context.WithValue(ctx, commitModeContextKey{}, normalizeCommitMode(mode))
}

// CommitModeFromContext reads the append commit mode from ctx.
func CommitModeFromContext(ctx context.Context) CommitMode {
	mode, _ := ctx.Value(commitModeContextKey{}).(CommitMode)
	return normalizeCommitMode(mode)
}

func normalizeCommitMode(mode CommitMode) CommitMode {
	if mode == 0 {
		return CommitModeQuorum
	}
	return mode
}
