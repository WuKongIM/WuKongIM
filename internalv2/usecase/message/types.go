package message

import "github.com/WuKongIM/WuKongIM/internalv2/contracts/channelwrite"

// Reason is the entry-agnostic result code for SEND.
type Reason = channelwrite.Reason

const (
	// ReasonSuccess means the send was durably accepted.
	ReasonSuccess = channelwrite.ReasonSuccess
	// ReasonInvalidRequest means the command is malformed.
	ReasonInvalidRequest = channelwrite.ReasonInvalidRequest
	// ReasonAuthFail means the sender is not authenticated.
	ReasonAuthFail = channelwrite.ReasonAuthFail
	// ReasonChannelNotExist means the channel cannot accept this send.
	ReasonChannelNotExist = channelwrite.ReasonChannelNotExist
	// ReasonNodeNotMatch means the client should retry through a fresher route.
	ReasonNodeNotMatch = channelwrite.ReasonNodeNotMatch
	// ReasonSystemError means the send failed due to infrastructure pressure or error.
	ReasonSystemError = channelwrite.ReasonSystemError
	// ReasonUnsupported means the phase-1 stack does not implement this send mode.
	ReasonUnsupported = channelwrite.ReasonUnsupported
)

// CommitMode controls when durable append completes.
type CommitMode = channelwrite.CommitMode

const (
	// CommitModeQuorum waits for quorum commit.
	CommitModeQuorum = channelwrite.CommitModeQuorum
	// CommitModeLocal completes after local durable append.
	CommitModeLocal = channelwrite.CommitModeLocal
)

// ChannelID identifies a message channel.
type ChannelID = channelwrite.ChannelID

// SendCommand is an entry-agnostic SEND request.
type SendCommand = channelwrite.SendCommand

// SendResult is the client-facing SEND outcome.
type SendResult = channelwrite.SendResult

// SendBatchItem carries one send command with its cancellation context.
type SendBatchItem = channelwrite.SendBatchItem

// SendBatchItemResult aligns with one SendBatch item.
type SendBatchItemResult = channelwrite.SendBatchItemResult

// Message is the durable append payload used by the message appender port.
type Message = channelwrite.Message

// AppendBatchRequest appends messages to one canonical channel.
type AppendBatchRequest = channelwrite.AppendBatchRequest

// AppendBatchResult returns item-aligned append outcomes.
type AppendBatchResult = channelwrite.AppendBatchResult

// AppendBatchItemResult is one append result inside a batch.
type AppendBatchItemResult = channelwrite.AppendBatchItemResult
