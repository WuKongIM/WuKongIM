package message

import "github.com/WuKongIM/WuKongIM/internal/contracts/channelappend"

// Reason is the entry-agnostic result code for SEND.
type Reason = channelappend.Reason

const (
	// DefaultPluginSendMaxHookDepth limits plugin-origin Send hook recursion.
	DefaultPluginSendMaxHookDepth = 1
)

// SendOrigin identifies where a SendCommand entered the message usecase.
type SendOrigin = channelappend.SendOrigin

const (
	// SendOriginClient marks sends that originated from a client or trusted host caller.
	SendOriginClient = channelappend.SendOriginClient
	// SendOriginPlugin marks sends that originated from a plugin host RPC.
	SendOriginPlugin = channelappend.SendOriginPlugin
)

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
	// ReasonSubscriberNotExist means the sender is not a channel subscriber.
	ReasonSubscriberNotExist = channelappend.ReasonSubscriberNotExist
	// ReasonInBlacklist means the sender is blocked by a channel denylist.
	ReasonInBlacklist = channelappend.ReasonInBlacklist
	// ReasonNotAllowSend means the sender is not allowed to send to the channel.
	ReasonNotAllowSend = channelappend.ReasonNotAllowSend
	// ReasonNotInWhitelist means the sender is missing from a required allowlist.
	ReasonNotInWhitelist = channelappend.ReasonNotInWhitelist
	// ReasonBan means the channel is banned.
	ReasonBan = channelappend.ReasonBan
	// ReasonDisband means the channel has been disbanded.
	ReasonDisband = channelappend.ReasonDisband
	// ReasonSendBan means the sender is send-banned.
	ReasonSendBan = channelappend.ReasonSendBan
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
