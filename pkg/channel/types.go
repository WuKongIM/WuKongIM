package channel

import (
	"context"
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

// WriteFenceReason identifies the control-plane operation that is blocking new writes.
type WriteFenceReason uint8

const (
	WriteFenceReasonUnknown WriteFenceReason = iota
	WriteFenceReasonLeaderTransfer
	WriteFenceReasonReplicaReplace
	WriteFenceReasonFailover
)

// WriteFence is durable control-plane metadata that prevents new leader appends.
type WriteFence struct {
	Token   string
	Version uint64
	Reason  WriteFenceReason
	Until   time.Time
}

// Set reports whether the authoritative metadata currently fences writes.
func (f WriteFence) Set() bool {
	return f.Token != "" && f.Version != 0
}

// AppendAdmissionRequest describes a leader append admission decision.
type AppendAdmissionRequest struct {
	// ChannelID is the client-visible channel identity.
	ChannelID ChannelID
	// ChannelKey is the stable runtime key used for reactor routing.
	ChannelKey ChannelKey
	// Epoch is the current channel membership epoch.
	Epoch uint64
	// LeaderEpoch is the current leader epoch within the channel epoch.
	LeaderEpoch uint64
	// Leader is the authoritative channel leader node.
	Leader NodeID
}

// AppendAdmissionGuard can fail closed before a leader append enters the reactor.
type AppendAdmissionGuard interface {
	AllowChannelAppend(context.Context, AppendAdmissionRequest) error
}

// AppendAdmissionGuardFunc adapts a function to AppendAdmissionGuard.
type AppendAdmissionGuardFunc func(context.Context, AppendAdmissionRequest) error

// AllowChannelAppend calls fn for the append admission decision.
func (fn AppendAdmissionGuardFunc) AllowChannelAppend(ctx context.Context, req AppendAdmissionRequest) error {
	if fn == nil {
		return ErrNotReady
	}
	return fn(ctx, req)
}

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
	// RetentionThroughSeq is the highest sequence hidden by authoritative compaction.
	RetentionThroughSeq uint64
	// WriteFence blocks new leader appends while control-plane migration is active.
	WriteFence WriteFence
	// Status controls local serving behavior.
	Status Status
}

// Message is the v0 client-visible message model.
type Message struct {
	MessageID   uint64
	MessageSeq  uint64
	ChannelID   string
	ChannelType uint8
	// Setting carries legacy message setting bits needed by compatible readers.
	Setting     uint8
	FromUID     string
	ClientMsgNo string
	// ServerTimestampMS is the server append timestamp in Unix milliseconds.
	ServerTimestampMS int64
	// TraceID correlates diagnostics events for this transient append message.
	TraceID string
	// ChannelKey is the diagnostics-safe channel identifier for this transient append message.
	ChannelKey string
	// SyncOnce marks one-shot command-sync messages in the durable channel log.
	SyncOnce bool
	Payload  []byte
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
	// Setting carries legacy message setting bits for durable compatibility.
	Setting uint8
	// FromUID is the sender user id preserved for conversation display.
	FromUID string
	// ClientMsgNo is the client idempotency key preserved for conversation display.
	ClientMsgNo string
	// ServerTimestampMS is the server append timestamp in Unix milliseconds.
	ServerTimestampMS int64
	// SyncOnce marks one-shot command-sync records in the durable channel log.
	SyncOnce bool
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
	ChannelID ChannelID
	Messages  []Message

	// TraceID correlates diagnostics events for this append batch.
	TraceID string
	// ChannelKey is the diagnostics-safe channel identifier for this append batch.
	ChannelKey string
	// Attempt is the one-based append attempt associated with diagnostics metadata.
	Attempt int

	CommitMode           CommitMode
	ExpectedChannelEpoch uint64
	ExpectedLeaderEpoch  uint64
	OmitResultPayload    bool
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
