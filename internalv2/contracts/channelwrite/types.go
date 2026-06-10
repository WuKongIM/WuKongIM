package channelwrite

import (
	"context"
	"time"
)

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

// AuthorityTarget identifies the fenced channel authority for write admission.
type AuthorityTarget struct {
	// ChannelID is the canonical channel owned by the authority node.
	ChannelID ChannelID
	// ChannelKey is the stable routing and diagnostics key for ChannelID.
	ChannelKey string
	// LeaderNodeID is the node currently authoritative for the channel.
	LeaderNodeID uint64
	// Epoch fences channel metadata changes.
	Epoch uint64
	// LeaderEpoch fences authority leadership changes.
	LeaderEpoch uint64
	// RouteRevision is the routing-table revision used to resolve this target.
	RouteRevision uint64
}

// SendCommand is an entry-agnostic SEND request.
type SendCommand struct {
	// FromUID is the authenticated sender uid.
	FromUID string
	// SenderNodeID is the owner node id that accepted the sender gateway session.
	SenderNodeID uint64
	// SenderSessionID is the node-local gateway session id.
	SenderSessionID uint64
	// ClientSeq is the client sequence echoed in Sendack.
	ClientSeq uint64
	// ClientMsgNo is the client idempotency key echoed in Sendack.
	ClientMsgNo string
	// TraceID correlates diagnostics events for this SEND when sendtrace is enabled.
	TraceID string
	// ChannelKey is the diagnostics-safe channel identifier used by sendtrace.
	ChannelKey string
	// ChannelID is the client-visible channel id.
	ChannelID string
	// ChannelType is the protocol channel category.
	ChannelType uint8
	// Payload is the message body. Send-path implementations treat it as immutable.
	Payload []byte
	// NoPersist requests transient delivery; phase 1 returns ReasonUnsupported.
	NoPersist bool
	// SyncOnce marks a one-shot sync command; phase 1 passes it through only as a flag.
	SyncOnce bool
	// RedDot carries the client red-dot flag for future delivery side effects.
	RedDot bool
	// NormalizePersonChannel requests canonical person-channel ID normalization before append.
	NormalizePersonChannel bool
	// RequestScoped derives a one-shot command channel from MessageScopedUIDs.
	RequestScoped bool
	// MessageScopedUIDs are request-scoped one-shot delivery targets.
	MessageScopedUIDs []string
	// MessageID is optional and must be zero for gateway-origin sends.
	MessageID uint64
	// ProtocolVersion is the client protocol version.
	ProtocolVersion uint8
}

// Clone returns an independent copy of the send command.
func (c SendCommand) Clone() SendCommand {
	c.Payload = cloneBytes(c.Payload)
	c.MessageScopedUIDs = append([]string(nil), c.MessageScopedUIDs...)
	return c
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
	// Deadline bounds durable append for this item without replacing Context.
	Deadline time.Time
	// Command is the SEND command.
	Command SendCommand
}

// Clone returns an independent copy of the batch item.
func (i SendBatchItem) Clone() SendBatchItem {
	i.Command = i.Command.Clone()
	return i
}

// SendBatchItemResult aligns with one SendBatch item.
type SendBatchItemResult struct {
	// Result is the send result.
	Result SendResult
	// Err is a context or infrastructure error.
	Err error
}

// Submitter accepts channel write commands and returns item-aligned results.
type Submitter interface {
	// Send processes one send command.
	Send(context.Context, SendCommand) (SendResult, error)
	// SendBatch processes send commands and returns item-aligned results.
	SendBatch([]SendBatchItem) []SendBatchItemResult
}

// Decision is the result of send authorization.
type Decision struct {
	// Allowed reports whether the send may proceed.
	Allowed bool
	// Reason explains rejected decisions.
	Reason Reason
}

// IdempotencyQuery identifies one canonical sender/client message key.
type IdempotencyQuery struct {
	// FromUID is the authenticated sender UID.
	FromUID string
	// ClientMsgNo is the sender-provided idempotency key.
	ClientMsgNo string
	// ChannelID is the canonical channel ID after request normalization.
	ChannelID string
	// ChannelType is the canonical channel type.
	ChannelType uint8
}

// Message is the durable append payload used by the channel appender port.
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
	// TraceID correlates diagnostics events for this message append when sendtrace is enabled.
	TraceID string
	// ChannelKey is the diagnostics-safe channel identifier propagated with this message append.
	ChannelKey string
	// Payload is the durable message body.
	Payload []byte
	// ServerTimestampMS is the server append timestamp in Unix milliseconds.
	ServerTimestampMS int64
}

// Clone returns an independent copy of the durable append message.
func (m Message) Clone() Message {
	m.Payload = cloneBytes(m.Payload)
	return m
}

// AppendBatchRequest appends messages to one canonical channel.
type AppendBatchRequest struct {
	// ChannelID is the canonical append target.
	ChannelID ChannelID
	// ExpectedEpoch fences append against stale channel metadata.
	ExpectedEpoch uint64
	// ExpectedLeaderEpoch fences append against stale authority leadership.
	ExpectedLeaderEpoch uint64
	// Messages are the durable messages for the target channel.
	Messages []Message
	// TraceID is the first non-empty diagnostics trace identifier among request messages.
	TraceID string
	// ChannelKey is the first non-empty diagnostics-safe channel identifier among request messages.
	ChannelKey string
	// Attempt is the one-based append attempt associated with diagnostics metadata.
	Attempt int
	// CommitMode controls the durability requirement for this append.
	CommitMode CommitMode
	// OmitResultPayload lets appenders skip payloads in successful item results when callers only need id and sequence.
	OmitResultPayload bool
}

// Clone returns an independent copy of the append request.
func (r AppendBatchRequest) Clone() AppendBatchRequest {
	r.Messages = cloneMessages(r.Messages)
	return r
}

// AppendBatchResult returns item-aligned append outcomes.
type AppendBatchResult struct {
	// Items are ordered to match the append request messages.
	Items []AppendBatchItemResult
}

// Clone returns an independent copy of the append result.
func (r AppendBatchResult) Clone() AppendBatchResult {
	r.Items = append([]AppendBatchItemResult(nil), r.Items...)
	for i := range r.Items {
		r.Items[i] = r.Items[i].Clone()
	}
	return r
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

// Clone returns an independent copy of the append item result.
func (r AppendBatchItemResult) Clone() AppendBatchItemResult {
	r.Message = r.Message.Clone()
	return r
}

// CommittedEnvelope carries one committed message into post-commit effects.
type CommittedEnvelope struct {
	// MessageID is the globally unique durable message identifier.
	MessageID uint64
	// MessageSeq is the committed channel sequence assigned by channelv2.
	MessageSeq uint64
	// ChannelID is the client-visible channel identifier.
	ChannelID string
	// ChannelType is the protocol channel category.
	ChannelType uint8
	// FromUID is the sender user id.
	FromUID string
	// SenderNodeID is the sender owner node id used to suppress same-connection echo.
	SenderNodeID uint64
	// SenderSessionID is the owner-local sender session id used to suppress same-connection echo.
	SenderSessionID uint64
	// ClientMsgNo is the client idempotency key.
	ClientMsgNo string
	// ServerTimestampMS is the server append timestamp used for conversation ordering.
	ServerTimestampMS int64
	// Payload is a copy of the committed payload.
	Payload []byte
	// RedDot carries the client red-dot flag for delivery side effects.
	RedDot bool
	// MessageScopedUIDs are request-scoped one-shot delivery targets.
	MessageScopedUIDs []string
}

// Clone returns an independent copy of the committed envelope.
func (e CommittedEnvelope) Clone() CommittedEnvelope {
	e.Payload = cloneBytes(e.Payload)
	e.MessageScopedUIDs = append([]string(nil), e.MessageScopedUIDs...)
	return e
}

// Recipient identifies one UID selected for committed-message effects.
type Recipient struct {
	// UID identifies the receiving user.
	UID string
	// JoinSeq is the first visible channel sequence for this recipient.
	JoinSeq uint64
}

// RecipientBatch carries one committed envelope and the recipients to process together.
type RecipientBatch struct {
	// Event is the committed message being processed.
	Event CommittedEnvelope
	// Recipients are the selected recipients for this effect batch.
	Recipients []Recipient
}

// Clone returns an independent copy of the recipient batch.
func (b RecipientBatch) Clone() RecipientBatch {
	b.Event = b.Event.Clone()
	b.Recipients = append([]Recipient(nil), b.Recipients...)
	return b
}

// SubscriberPageRequest describes one channel subscriber page scan.
type SubscriberPageRequest struct {
	// ChannelID identifies the channel whose subscribers should be scanned.
	ChannelID ChannelID
	// Cursor resumes after the previous page.
	Cursor string
	// Limit bounds the number of recipients returned in one page.
	Limit int
}

// SubscriberPage is one bounded subscriber scan page.
type SubscriberPage struct {
	// Recipients are the durable recipients found in this page.
	Recipients []Recipient
	// Cursor is the opaque continuation token for the next page.
	Cursor string
	// Done reports whether the scan is complete after this page.
	Done bool
}

// Clone returns an independent copy of the subscriber page.
func (p SubscriberPage) Clone() SubscriberPage {
	p.Recipients = append([]Recipient(nil), p.Recipients...)
	return p
}

// ConversationPatch is a recipient-scoped conversation activity update.
type ConversationPatch struct {
	// UID identifies the user that owns the conversation row.
	UID string
	// ChannelID identifies the conversation channel.
	ChannelID string
	// ChannelType identifies the channel namespace.
	ChannelType int64
	// ReadSeq is the minimum read floor derived from membership visibility.
	ReadSeq uint64
	// DeletedToSeq is the minimum delete floor derived from membership visibility.
	DeletedToSeq uint64
	// ActiveAt is the candidate active-list ordering timestamp.
	ActiveAt int64
	// UpdatedAt records when this active candidate was produced.
	UpdatedAt int64
	// SparseActive is the requested sparse-active mode.
	SparseActive bool
	// MessageSeq fences stale activity after user delete barriers.
	MessageSeq uint64
}

// Route describes one online recipient endpoint resolved by presence.
type Route struct {
	// UID is the recipient user ID for this endpoint.
	UID string
	// OwnerNodeID is the node that owns the recipient gateway session.
	OwnerNodeID uint64
	// OwnerBootID fences stale owner-node process incarnations.
	OwnerBootID uint64
	// OwnerSeq fences stale owner-session authority observations.
	OwnerSeq uint64
	// SessionID is the recipient owner-local gateway session identifier.
	SessionID uint64
	// DeviceID identifies the recipient client device.
	DeviceID string
	// DeviceFlag carries protocol device category metadata.
	DeviceFlag uint8
	// DeviceLevel carries protocol device priority metadata.
	DeviceLevel uint8
}

// PushCommand groups recipient routes owned by the same node for one envelope.
type PushCommand struct {
	// OwnerNodeID is the recipient owner node that should accept the push.
	OwnerNodeID uint64
	// Envelope is an independent copy of the message being pushed.
	Envelope CommittedEnvelope
	// Routes are the recipient endpoints owned by OwnerNodeID.
	Routes []Route
}

// Clone returns an independent copy of the push command.
func (c PushCommand) Clone() PushCommand {
	c.Envelope = c.Envelope.Clone()
	c.Routes = append([]Route(nil), c.Routes...)
	return c
}

// PushResult reports how an owner node classified pushed recipient routes.
type PushResult struct {
	// Accepted routes were accepted for delivery by the owner node.
	Accepted []Route
	// Retryable routes should be retried by a later runtime stage.
	Retryable []Route
	// Dropped routes should not be retried.
	Dropped []Route
}

// Clone returns an independent copy of the push result.
func (r PushResult) Clone() PushResult {
	r.Accepted = append([]Route(nil), r.Accepted...)
	r.Retryable = append([]Route(nil), r.Retryable...)
	r.Dropped = append([]Route(nil), r.Dropped...)
	return r
}

func cloneMessages(in []Message) []Message {
	out := append([]Message(nil), in...)
	for i := range out {
		out[i] = out[i].Clone()
	}
	return out
}

func cloneBytes(in []byte) []byte {
	return append([]byte(nil), in...)
}
