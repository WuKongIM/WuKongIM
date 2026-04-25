package channel

import (
	"context"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
)

type NodeID uint64
type ChannelKey string

type ChannelID struct {
	ID   string
	Type uint8
}

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

type Message struct {
	MessageID  uint64
	MessageSeq uint64
	Framer     frame.Framer
	Setting    frame.Setting
	MsgKey     string
	Expire     uint32
	ClientSeq  uint64

	ClientMsgNo string
	StreamNo    string
	StreamID    uint64
	StreamFlag  frame.StreamFlag
	Timestamp   int32
	ChannelID   string
	ChannelType uint8
	Topic       string
	FromUID     string
	Payload     []byte
}

type Meta struct {
	Key         ChannelKey
	ID          ChannelID
	Epoch       uint64
	LeaderEpoch uint64
	Leader      NodeID
	Replicas    []NodeID
	ISR         []NodeID
	MinISR      int
	LeaseUntil  time.Time
	Status      Status
	Features    Features
}

type AppendRequest struct {
	ChannelID             ChannelID
	Message               Message
	SupportsMessageSeqU64 bool
	// CommitMode defaults to CommitModeQuorum when unset.
	CommitMode           CommitMode
	ExpectedChannelEpoch uint64
	ExpectedLeaderEpoch  uint64
}

type AppendResult struct {
	MessageID  uint64
	MessageSeq uint64
	Message    Message
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
}

type ChannelRuntimeStatus struct {
	Key          ChannelKey
	ID           ChannelID
	Status       Status
	Leader       NodeID
	LeaderEpoch  uint64
	HW           uint64
	CommittedSeq uint64
}

type Record struct {
	Payload   []byte
	SizeBytes int
}

type Checkpoint struct {
	Epoch          uint64
	LogStartOffset uint64
	HW             uint64
}

type EpochPoint struct {
	Epoch       uint64
	StartOffset uint64
}

type IdempotencyKey struct {
	ChannelID   ChannelID
	FromUID     string
	ClientMsgNo string
}

type IdempotencyEntry struct {
	MessageID  uint64
	MessageSeq uint64
	Offset     uint64
}

type ApplyFetchStoreRequest struct {
	PreviousCommittedHW uint64
	Records             []Record
	Checkpoint          *Checkpoint
}

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
}

type CommitResult struct {
	BaseOffset   uint64
	NextCommitHW uint64
	RecordCount  int
}

type Snapshot struct {
	ChannelKey ChannelKey
	Epoch      uint64
	EndOffset  uint64
	Payload    []byte
}

type ReplicaFetchRequest struct {
	ChannelKey  ChannelKey
	Epoch       uint64
	ReplicaID   NodeID
	FetchOffset uint64
	OffsetEpoch uint64
	MaxBytes    int
}

type ReplicaFetchResult struct {
	Epoch      uint64
	HW         uint64
	Records    []Record
	TruncateTo *uint64
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
	ChannelKey   ChannelKey
	Epoch        uint64
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
