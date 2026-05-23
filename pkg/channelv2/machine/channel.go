package machine

import ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"

// TaskKind identifies side effects requested by the pure channel state machine.
type TaskKind uint8

const (
	TaskKindStoreAppend TaskKind = iota + 1
	TaskKindStoreReadCommitted
	TaskKindStoreReadLog
	TaskKindStoreApply
	TaskKindRPCPull
	TaskKindRPCAck
)

// Task is a pure description of work the reactor should run elsewhere.
type Task struct {
	Kind          TaskKind
	Fence         ch.Fence
	StoreAppend   *StoreAppendTask
	ReadCommitted *ReadCommittedTask
	ReadLog       *ReadLogTask
	StoreApply    *StoreApplyTask
	Ack           *AckTask
}

// StoreAppendTask asks a worker to durably append leader records.
type StoreAppendTask struct {
	Records []ch.Record
	Sync    bool
}

// ReadCommittedTask asks a worker to read committed messages.
type ReadCommittedTask struct {
	FromSeq  uint64
	MaxSeq   uint64
	Limit    int
	MaxBytes int
}

// ReadLogTask asks a worker to read raw records for replication.
type ReadLogTask struct {
	FromOffset uint64
	MaxOffset  uint64
	MaxBytes   int
}

// StoreApplyTask asks a worker to apply records on a follower.
type StoreApplyTask struct {
	Records  []ch.Record
	LeaderHW uint64
}

// AckTask asks transport to report follower progress to a leader.
type AckTask struct {
	Follower    ch.NodeID
	MatchOffset uint64
}

// ReplyKind identifies a synchronous caller reply produced by the machine.
type ReplyKind uint8

const (
	ReplyKindAppend ReplyKind = iota + 1
	ReplyKindFetch
)

// Reply completes a waiting caller future.
type Reply struct {
	Kind        ReplyKind
	OpID        ch.OpID
	Err         error
	Append      ch.AppendBatchItemResult
	AppendItems []ch.AppendBatchItemResult
	Fetch       ch.FetchResult
}

// SignalKind identifies non-blocking notifications requested by the machine.
type SignalKind uint8

const (
	SignalKindReplicate SignalKind = iota + 1
)

// Signal requests follow-up scheduling after a state transition.
type Signal struct {
	Kind SignalKind
}

// Decision is the output of one pure state-machine transition.
type Decision struct {
	Err     error
	Tasks   []Task
	Replies []Reply
	Signals []Signal
}

// ReplicaProgress tracks how far a replica has copied this channel log.
type ReplicaProgress struct {
	Match uint64
}

// AppendWaiter tracks one append request waiting for local or quorum commit.
type AppendWaiter struct {
	OpID       ch.OpID
	Target     uint64
	CommitMode ch.CommitMode
	Records    []ch.Record
}

// AppendOp is the currently durable in-flight append batch for one channel.
type AppendOp struct {
	OpID    ch.OpID
	Records []ch.Record
}

// ChannelState is the single-writer aggregate for one channel.
type ChannelState struct {
	Key          ch.ChannelKey
	LocalNode    ch.NodeID
	Generation   uint64
	ID           ch.ChannelID
	Epoch        uint64
	LeaderEpoch  uint64
	Role         ch.Role
	Status       ch.Status
	Leader       ch.NodeID
	Replicas     []ch.NodeID
	ISR          []ch.NodeID
	MinISR       int
	LEO          uint64
	HW           uint64
	CheckpointHW uint64
	CommitReady  bool
	Progress     map[ch.NodeID]ReplicaProgress

	PendingAppends map[ch.OpID]*AppendWaiter
	InflightAppend *AppendOp
}

// NewChannelState creates an empty local state owned by one reactor.
func NewChannelState(key ch.ChannelKey, local ch.NodeID, generation uint64) *ChannelState {
	return &ChannelState{
		Key:            key,
		LocalNode:      local,
		Generation:     generation,
		Progress:       make(map[ch.NodeID]ReplicaProgress),
		PendingAppends: make(map[ch.OpID]*AppendWaiter),
	}
}

func copyNodeIDs(in []ch.NodeID) []ch.NodeID {
	if len(in) == 0 {
		return nil
	}
	out := make([]ch.NodeID, len(in))
	copy(out, in)
	return out
}
