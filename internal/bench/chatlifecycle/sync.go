package chatlifecycle

import (
	"context"
	"errors"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/target"
)

const (
	conversationSyncMessageCount = 20
	conversationSyncLimit        = 500
)

// SyncClassification identifies who owns a conversation-sync validation failure.
type SyncClassification string

const (
	// SyncClassificationHarnessInvalid means the response hit the harness completeness boundary.
	SyncClassificationHarnessInvalid SyncClassification = "harness_invalid"
	// SyncClassificationProductFailure means returned conversation evidence violated the product contract.
	SyncClassificationProductFailure SyncClassification = "product_failure"
)

// ConversationSyncValidationError is a stable, low-cardinality sync failure.
type ConversationSyncValidationError struct {
	classification SyncClassification
	reasonCode     string
}

func (e *ConversationSyncValidationError) Error() string {
	if e == nil {
		return "conversation sync validation failed"
	}
	return "conversation sync validation failed: " + e.reasonCode
}

// Classification reports whether the run or the product owns this failure.
func (e *ConversationSyncValidationError) Classification() SyncClassification {
	if e == nil {
		return ""
	}
	return e.classification
}

// ReasonCode returns a stable reason without user, channel, or payload data.
func (e *ConversationSyncValidationError) ReasonCode() string {
	if e == nil {
		return ""
	}
	return e.reasonCode
}

// NewConversationSyncRequest constructs a fresh stateless full-sync request for every login.
func NewConversationSyncRequest(uid string) target.ConversationSyncRequest {
	return target.ConversationSyncRequest{
		UID:         uid,
		Version:     0,
		LastMsgSeqs: "",
		MsgCount:    conversationSyncMessageCount,
		OnlyUnread:  0,
		Limit:       conversationSyncLimit,
	}
}

// ValidateConversationSync proves the bounded response is complete and internally ordered.
func ValidateConversationSync(conversations []target.ConversationSyncConversation) error {
	if len(conversations) >= conversationSyncLimit {
		return newConversationSyncValidationError(SyncClassificationHarnessInvalid, "conversation_limit_reached")
	}

	seen := make(map[conversationIdentity]struct{}, len(conversations))
	for _, conversation := range conversations {
		identity := conversationIdentity{id: conversation.ChannelID, channelType: conversation.ChannelType}
		if identity.id == "" || identity.channelType == 0 {
			return newConversationSyncValidationError(SyncClassificationProductFailure, "conversation_identity_invalid")
		}
		if _, exists := seen[identity]; exists {
			return newConversationSyncValidationError(SyncClassificationProductFailure, "duplicate_conversation")
		}
		seen[identity] = struct{}{}

		var previousSequence uint64
		for index, recent := range conversation.Recents {
			if recent.ChannelID != conversation.ChannelID || recent.ChannelType != conversation.ChannelType {
				return newConversationSyncValidationError(SyncClassificationProductFailure, "recent_identity_mismatch")
			}
			if recent.MessageSeq == 0 || (index > 0 && previousSequence <= recent.MessageSeq) {
				return newConversationSyncValidationError(SyncClassificationProductFailure, "recent_sequence_invalid")
			}
			previousSequence = recent.MessageSeq
		}
	}
	return nil
}

type conversationIdentity struct {
	id          string
	channelType uint8
}

func newConversationSyncValidationError(classification SyncClassification, reasonCode string) error {
	return &ConversationSyncValidationError{classification: classification, reasonCode: reasonCode}
}

// LoginSyncConnector establishes the WKProto session for a login UID.
type LoginSyncConnector interface {
	Connect(context.Context, string) error
}

// ConversationSyncer performs the product HTTP conversation sync request.
type ConversationSyncer interface {
	ConversationSync(context.Context, target.ConversationSyncRequest) ([]target.ConversationSyncConversation, error)
}

// LoginSyncResult contains the two independently measured startup stages.
type LoginSyncResult struct {
	GatewayConnectLatency   time.Duration
	ConversationSyncLatency time.Duration
	TrafficReady            bool
	Conversations           []target.ConversationSyncConversation
}

// RunLoginSync performs CONNECT before a fresh full sync and admits traffic only after validation.
func RunLoginSync(ctx context.Context, uid string, connector LoginSyncConnector, syncer ConversationSyncer, now func() time.Time) (LoginSyncResult, error) {
	var result LoginSyncResult
	if err := ctx.Err(); err != nil {
		return result, newLoginSyncOperationError("login sync canceled", err)
	}

	connectStarted := now()
	connectErr := connector.Connect(ctx, uid)
	result.GatewayConnectLatency = now().Sub(connectStarted)
	if connectErr != nil {
		return result, newLoginSyncOperationError("login sync gateway connect failed", connectErr)
	}
	if err := ctx.Err(); err != nil {
		return result, newLoginSyncOperationError("login sync canceled", err)
	}

	syncStarted := now()
	conversations, syncErr := syncer.ConversationSync(ctx, NewConversationSyncRequest(uid))
	result.ConversationSyncLatency = now().Sub(syncStarted)
	if syncErr != nil {
		return result, newLoginSyncOperationError("login sync conversation request failed", syncErr)
	}
	if err := ctx.Err(); err != nil {
		return result, newLoginSyncOperationError("login sync canceled", err)
	}
	if err := ValidateConversationSync(conversations); err != nil {
		return result, err
	}

	result.Conversations = conversations
	result.TrafficReady = true
	return result, nil
}

type loginSyncOperationError struct {
	message string
	cause   error
}

func newLoginSyncOperationError(message string, cause error) error {
	if cause == nil {
		cause = errors.New(message)
	}
	return &loginSyncOperationError{message: message, cause: cause}
}

func (e *loginSyncOperationError) Error() string {
	if e == nil {
		return "login sync failed"
	}
	return e.message
}

func (e *loginSyncOperationError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}
