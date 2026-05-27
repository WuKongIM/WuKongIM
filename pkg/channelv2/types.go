package channelv2

import (
	"strconv"
	"time"
)

// NodeID identifies a node in the channel replica set.
type NodeID uint64

// ChannelKey is the stable runtime key used for reactor routing.
type ChannelKey string

// ChannelID is the client-visible channel identity.
type ChannelID struct {
	// ID is the channel identifier without the type suffix.
	ID string
	// Type is the channel category used by the protocol layer.
	Type uint8
}

// Status is the authoritative channel lifecycle state.
type Status uint8

const (
	StatusCreating Status = iota + 1
	StatusActive
	StatusDeleting
	StatusDeleted
)

// Role is the local replica role for a channel.
type Role uint8

const (
	RoleFollower Role = iota + 1
	RoleLeader
)

// CommitMode controls when append calls complete.
type CommitMode uint8

const (
	// CommitModeQuorum waits for the leader HW to cover the appended records.
	CommitModeQuorum CommitMode = iota + 1
	// CommitModeLocal completes after local durable append.
	CommitModeLocal
)

// Meta is the authoritative control-plane projection for a channel.
type Meta struct {
	// Key is the channel runtime key. If empty, service may derive it from ID.
	Key ChannelKey
	// ID is the client-visible channel identity.
	ID ChannelID
	// Epoch fences channel membership changes.
	Epoch uint64
	// LeaderEpoch fences leader changes within an epoch.
	LeaderEpoch uint64
	// Leader is the authoritative leader node.
	Leader NodeID
	// Replicas are nodes that should receive channel log data.
	Replicas []NodeID
	// ISR are replicas that participate in HW quorum calculation.
	ISR []NodeID
	// MinISR is the quorum size used for commit.
	MinISR int
	// LeaseUntil is reserved for later leader lease enforcement.
	LeaseUntil time.Time
	// Status controls local serving behavior.
	Status Status
}

// Message is the v0 client-visible message model.
type Message struct {
	MessageID   uint64
	MessageSeq  uint64
	ChannelID   string
	ChannelType uint8
	FromUID     string
	ClientMsgNo string
	Payload     []byte
}

// OpID identifies an asynchronous operation inside one channel generation.
type OpID uint64

// Fence is copied into async tasks and results to reject stale completions.
type Fence struct {
	ChannelKey  ChannelKey
	Generation  uint64
	Epoch       uint64
	LeaderEpoch uint64
	OpID        OpID
}

// Record is the durable log representation replicated between nodes.
type Record struct {
	// ID is the message id carried by this log entry.
	ID uint64
	// Index is the 1-based channel log offset and client message sequence.
	Index uint64
	// Epoch is the channel epoch that produced this entry.
	Epoch uint64
	// Payload is the encoded message body in v0 memory and store adapters.
	Payload []byte
	// SizeBytes is used by batching and read budgets.
	SizeBytes int
}

// Checkpoint records the durable committed frontier.
type Checkpoint struct {
	HW uint64
}

// AppendRequest appends one message to a channel.
type AppendRequest struct {
	ChannelID            ChannelID
	Message              Message
	CommitMode           CommitMode
	ExpectedChannelEpoch uint64
	ExpectedLeaderEpoch  uint64
}

// AppendResult is the committed result for one append.
type AppendResult struct {
	MessageID  uint64
	MessageSeq uint64
	Message    Message
}

// AppendBatchRequest appends messages to one channel in request order.
type AppendBatchRequest struct {
	ChannelID            ChannelID
	Messages             []Message
	CommitMode           CommitMode
	ExpectedChannelEpoch uint64
	ExpectedLeaderEpoch  uint64
}

// AppendBatchResult aligns per-message append results with the request order.
type AppendBatchResult struct {
	Items []AppendBatchItemResult
}

// AppendBatchItemResult is one result inside an append batch.
type AppendBatchItemResult struct {
	MessageID  uint64
	MessageSeq uint64
	Message    Message
	Err        error
}

// ChannelKeyForID derives the default reactor key for a channel id.
func ChannelKeyForID(id ChannelID) ChannelKey {
	return ChannelKey(strconv.Itoa(int(id.Type)) + ":" + id.ID)
}
