package message

import "context"

// Reason is the entry-agnostic result code for SEND.
type Reason uint8

const (
	// ReasonSuccess means the send was durably accepted.
	ReasonSuccess Reason = iota
	// ReasonInvalidRequest means the command is malformed.
	ReasonInvalidRequest
	// ReasonAuthFail means the sender is not authenticated.
	ReasonAuthFail
	// ReasonChannelNotExist means the channel cannot accept this send.
	ReasonChannelNotExist
	// ReasonNodeNotMatch means the client should retry through a fresher route.
	ReasonNodeNotMatch
	// ReasonSystemError means the send failed due to infrastructure pressure or error.
	ReasonSystemError
	// ReasonUnsupported means the phase-1 stack does not implement this send mode.
	ReasonUnsupported
)

// CommitMode controls when durable append completes.
type CommitMode uint8

const (
	// CommitModeQuorum waits for quorum commit.
	CommitModeQuorum CommitMode = iota + 1
	// CommitModeLocal completes after local durable append.
	CommitModeLocal
)

// ChannelID identifies a message channel.
type ChannelID struct {
	// ID is the client-visible channel id.
	ID string
	// Type is the protocol channel category.
	Type uint8
}

// SendCommand is an entry-agnostic SEND request.
type SendCommand struct {
	// FromUID is the authenticated sender uid.
	FromUID string
	// SenderSessionID is the node-local gateway session id.
	SenderSessionID uint64
	// ClientSeq is the client sequence echoed in Sendack.
	ClientSeq uint64
	// ClientMsgNo is the client idempotency key echoed in Sendack.
	ClientMsgNo string
	// ChannelID is the client-visible channel id.
	ChannelID string
	// ChannelType is the protocol channel category.
	ChannelType uint8
	// Payload is the message body. The usecase clones it before append.
	Payload []byte
	// NoPersist requests transient delivery; phase 1 returns ReasonUnsupported.
	NoPersist bool
	// SyncOnce marks a one-shot sync command; phase 1 passes it through only as a flag.
	SyncOnce bool
	// RedDot carries the client red-dot flag for future delivery side effects.
	RedDot bool
	// MessageID is optional and must be zero for gateway-origin sends.
	MessageID uint64
	// ProtocolVersion is the client protocol version.
	ProtocolVersion uint8
}

// SendResult is the client-facing SEND outcome.
type SendResult struct {
	// MessageID is the durable message id.
	MessageID uint64
	// MessageSeq is the committed channel sequence.
	MessageSeq uint64
	// Reason is the entry-agnostic result code.
	Reason Reason
}

// SendBatchItem carries one send command with its cancellation context.
type SendBatchItem struct {
	// Context is the per-send request context.
	Context context.Context
	// Command is the SEND command.
	Command SendCommand
}

// SendBatchItemResult aligns with one SendBatch item.
type SendBatchItemResult struct {
	// Result is the send result.
	Result SendResult
	// Err is a context or infrastructure error.
	Err error
}

// Message is the durable append payload used by the message appender port.
type Message struct {
	// MessageID is the durable message id.
	MessageID uint64
	// MessageSeq is the committed channel sequence.
	MessageSeq uint64
	// ChannelID is the client-visible channel id.
	ChannelID string
	// ChannelType is the protocol channel category.
	ChannelType uint8
	// FromUID is the sender user id.
	FromUID string
	// ClientMsgNo is the client idempotency key.
	ClientMsgNo string
	// Payload is the durable message body.
	Payload []byte
}

// AppendBatchRequest appends messages to one canonical channel.
type AppendBatchRequest struct {
	// ChannelID is the canonical append target.
	ChannelID ChannelID
	// Messages are the durable messages for the target channel.
	Messages []Message
	// CommitMode controls the durability requirement for this append.
	CommitMode CommitMode
}

// AppendBatchResult returns item-aligned append outcomes.
type AppendBatchResult struct {
	// Items are ordered to match the append request messages.
	Items []AppendBatchItemResult
}

// AppendBatchItemResult is one append result inside a batch.
type AppendBatchItemResult struct {
	// MessageID is the durable message id.
	MessageID uint64
	// MessageSeq is the committed channel sequence.
	MessageSeq uint64
	// Message is the appended message as accepted by the appender.
	Message Message
	// Err is the per-message append failure.
	Err error
}
