package message

import "github.com/WuKongIM/WuKongIM/internalv2/contracts/channelappend"

// Reason is the entry-agnostic result code for SEND.
type Reason = channelappend.Reason

const (
	// ReasonSuccess means the send was durably accepted.
	ReasonSuccess = channelappend.ReasonSuccess
	// ReasonInvalidRequest means the command is malformed.
	ReasonInvalidRequest = channelappend.ReasonInvalidRequest
	// ReasonAuthFail means the sender is not authenticated.
	ReasonAuthFail = channelappend.ReasonAuthFail
	// ReasonChannelNotExist means the channel cannot accept this send.
	ReasonChannelNotExist = channelappend.ReasonChannelNotExist
	// ReasonNodeNotMatch means the client should retry through a fresher route.
	ReasonNodeNotMatch = channelappend.ReasonNodeNotMatch
	// ReasonSystemError means the send failed due to infrastructure pressure or error.
	ReasonSystemError = channelappend.ReasonSystemError
	// ReasonUnsupported means the phase-1 stack does not implement this send mode.
	ReasonUnsupported = channelappend.ReasonUnsupported
)

// ChannelID identifies a message channel.
type ChannelID = channelappend.ChannelID

// SendCommand is an entry-agnostic SEND request.
type SendCommand = channelappend.SendCommand

// SendResult is the client-facing SEND outcome.
type SendResult = channelappend.SendResult

// SendBatchItem carries one send command with its cancellation context.
type SendBatchItem = channelappend.SendBatchItem

// SendBatchItemResult aligns with one SendBatch item.
type SendBatchItemResult = channelappend.SendBatchItemResult
