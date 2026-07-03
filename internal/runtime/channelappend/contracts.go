package channelappend

import (
	"context"

	contract "github.com/WuKongIM/WuKongIM/internal/contracts/channelappend"
)

// ChannelID identifies a message channel.
type ChannelID = contract.ChannelID

// AuthorityTarget identifies the fenced channel authority for write admission.
type AuthorityTarget = contract.AuthorityTarget

// SendCommand is an entry-agnostic SEND request.
type SendCommand = contract.SendCommand

// Reason is the entry-agnostic result code for SEND.
type Reason = contract.Reason

const (
	// ReasonSuccess means the send was durably accepted.
	ReasonSuccess = contract.ReasonSuccess
	// ReasonInvalidRequest means the command is malformed.
	ReasonInvalidRequest = contract.ReasonInvalidRequest
	// ReasonAuthFail means the sender is not authenticated.
	ReasonAuthFail = contract.ReasonAuthFail
	// ReasonChannelNotExist means the channel cannot accept this send.
	ReasonChannelNotExist = contract.ReasonChannelNotExist
	// ReasonNodeNotMatch means the client should retry through a fresher route.
	ReasonNodeNotMatch = contract.ReasonNodeNotMatch
	// ReasonSystemError means the send failed due to infrastructure pressure or error.
	ReasonSystemError = contract.ReasonSystemError
	// ReasonUnsupported means the phase-1 stack does not implement this send mode.
	ReasonUnsupported = contract.ReasonUnsupported
	// ReasonSubscriberNotExist means the sender is not a channel subscriber.
	ReasonSubscriberNotExist = contract.ReasonSubscriberNotExist
	// ReasonInBlacklist means the sender is blocked by a channel denylist.
	ReasonInBlacklist = contract.ReasonInBlacklist
	// ReasonNotAllowSend means the sender is not allowed to send to the channel.
	ReasonNotAllowSend = contract.ReasonNotAllowSend
	// ReasonNotInWhitelist means the sender is missing from a required allowlist.
	ReasonNotInWhitelist = contract.ReasonNotInWhitelist
	// ReasonBan means the channel is banned.
	ReasonBan = contract.ReasonBan
	// ReasonDisband means the channel has been disbanded.
	ReasonDisband = contract.ReasonDisband
	// ReasonSendBan means the sender is send-banned.
	ReasonSendBan = contract.ReasonSendBan
)

// SendResult is the client-facing SEND outcome.
type SendResult = contract.SendResult

// SendBatchItem carries one send command with its cancellation context.
type SendBatchItem = contract.SendBatchItem

// SendBatchItemResult aligns with one SendBatch item.
type SendBatchItemResult = contract.SendBatchItemResult

// Decision is the result of send authorization.
type Decision = contract.Decision

// CommitMode controls when durable append completes.
type CommitMode = contract.CommitMode

const (
	// CommitModeQuorum waits for quorum commit.
	CommitModeQuorum = contract.CommitModeQuorum
	// CommitModeLocal completes after local durable append.
	CommitModeLocal = contract.CommitModeLocal
)

// IdempotencyQuery identifies one canonical sender/client message key.
type IdempotencyQuery = contract.IdempotencyQuery

// Message is the durable append payload used by the channel appender port.
type Message = contract.Message

// AppendBatchRequest appends messages to one canonical channel.
type AppendBatchRequest = contract.AppendBatchRequest

// AppendBatchResult returns item-aligned append outcomes.
type AppendBatchResult = contract.AppendBatchResult

// AppendBatchItemResult is one append result inside a batch.
type AppendBatchItemResult = contract.AppendBatchItemResult

// CommittedEnvelope carries one committed message into post-commit effects.
type CommittedEnvelope = contract.CommittedEnvelope

// Recipient identifies one UID selected for committed-message effects.
type Recipient = contract.Recipient

// RecipientBatch carries one committed envelope and the recipients to process together.
type RecipientBatch = contract.RecipientBatch

// OfflineRecipientsEvent reports durable recipients with no online route.
type OfflineRecipientsEvent struct {
	// Event is the committed message whose recipients were classified offline.
	Event CommittedEnvelope
	// UIDs are recipient user identifiers without online routes, in first-seen order.
	UIDs []string
}

// OfflineRecipientsObserver receives offline recipient candidates in one batch after presence resolution.
type OfflineRecipientsObserver interface {
	// ObserveOfflineRecipients records durable recipients with no online route.
	ObserveOfflineRecipients(context.Context, OfflineRecipientsEvent)
}

// OfflineRecipientEvent reports one durable recipient with no online route.
type OfflineRecipientEvent struct {
	// Event is the committed message whose recipient was classified offline.
	Event CommittedEnvelope
	// UID is the recipient user identifier without an online route.
	UID string
}

// OfflineRecipientObserver receives offline recipient candidates after presence resolution.
type OfflineRecipientObserver interface {
	// ObserveOfflineRecipient records one durable recipient with no online route.
	ObserveOfflineRecipient(context.Context, OfflineRecipientEvent)
}

// SubscriberPageRequest describes one channel subscriber page scan.
type SubscriberPageRequest = contract.SubscriberPageRequest

// SubscriberPage is one bounded subscriber scan page.
type SubscriberPage = contract.SubscriberPage

// Route describes one online recipient endpoint resolved by presence.
type Route = contract.Route

// SubscriberMutationUpdate describes a committed subscriber-list change for one channel.
type SubscriberMutationUpdate struct {
	// ChannelID identifies the channel whose cached subscriber snapshot changed.
	ChannelID ChannelID
	// Large reports whether the channel should use paged subscriber fanout after the mutation.
	Large bool
	// SubscriberMutationVersion is the durable subscriber-list version after the mutation.
	SubscriberMutationVersion uint64
	// Reset reports that AddedUIDs replaces the cached snapshot instead of patching it.
	Reset bool
	// AddedUIDs are subscribers appended by this mutation.
	AddedUIDs []string
	// RemovedUIDs are subscribers removed by this mutation.
	RemovedUIDs []string
}

// PushCommand groups recipient routes owned by the same node for one envelope.
type PushCommand = contract.PushCommand

// PushResult reports how an owner node classified pushed recipient routes.
type PushResult = contract.PushResult

var (
	// ErrNotChannelAuthority reports that the local node is not the channel authority.
	ErrNotChannelAuthority = contract.ErrNotChannelAuthority
	// ErrBackpressured reports bounded runtime pressure.
	ErrBackpressured = contract.ErrBackpressured
	// ErrChannelBusy reports that channel-level write flow control is saturated.
	ErrChannelBusy = contract.ErrChannelBusy
	// ErrAppenderRequired reports that durable append is not configured.
	ErrAppenderRequired = contract.ErrAppenderRequired
	// ErrStaleRoute reports that append used stale channel metadata.
	ErrStaleRoute = contract.ErrStaleRoute
	// ErrRouteNotReady reports that cluster routing is not ready for foreground writes.
	ErrRouteNotReady = contract.ErrRouteNotReady
	// ErrNotLeader reports that the append target is no longer the leader.
	ErrNotLeader = contract.ErrNotLeader
	// ErrChannelNotFound reports that the target channel is not available.
	ErrChannelNotFound = contract.ErrChannelNotFound
	// ErrAppendFailed wraps unexpected append failures.
	ErrAppendFailed = contract.ErrAppendFailed
	// ErrAppendResultMissing reports a successful batch append response without a matching item result.
	ErrAppendResultMissing = contract.ErrAppendResultMissing
	// ErrRequestSubscribersRequireSyncOnce reports that request-scoped sends must be sync_once.
	ErrRequestSubscribersRequireSyncOnce = contract.ErrRequestSubscribersRequireSyncOnce
	// ErrRequestSubscribersConflictChannel reports that request-scoped sends cannot specify a channel.
	ErrRequestSubscribersConflictChannel = contract.ErrRequestSubscribersConflictChannel
	// ErrRequestSubscribersRequired reports that request-scoped sends need at least one usable subscriber.
	ErrRequestSubscribersRequired = contract.ErrRequestSubscribersRequired
	// ErrMessageIDAllocatorRequired reports that message id allocation is not configured.
	ErrMessageIDAllocatorRequired = contract.ErrMessageIDAllocatorRequired
)
