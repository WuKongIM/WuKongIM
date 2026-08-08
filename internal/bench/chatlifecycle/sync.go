package chatlifecycle

import (
	"context"
	"errors"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/bench/target"
)

const (
	conversationSyncMaxConversations = 500
)

// SyncClassification identifies who owns a conversation-sync validation failure.
type SyncClassification string

const (
	// SyncClassificationHarnessInvalid means the response hit the harness completeness boundary.
	SyncClassificationHarnessInvalid SyncClassification = "harness_invalid"
	// SyncClassificationProductFailure means returned conversation evidence violated the product contract.
	SyncClassificationProductFailure SyncClassification = "product_failure"
)

// LoginSyncStage is the closed startup stage reported without endpoint data.
type LoginSyncStage string

const (
	LoginSyncStageFactory LoginSyncStage = "factory"
	LoginSyncStageConnect LoginSyncStage = "connect"
	LoginSyncStageSync    LoginSyncStage = "sync"
)

const (
	LoginSyncReasonTransport = "transport_failed"
	LoginSyncReasonCanceled  = "canceled"
)

// LoginSyncFailure contains only closed, low-cardinality startup diagnostics.
type LoginSyncFailure struct {
	Stage          LoginSyncStage
	Reason         string
	Classification SyncClassification
}

// LoginSyncFailureOf extracts closed startup diagnostics without exposing the
// wrapped transport cause.
func LoginSyncFailureOf(err error) (LoginSyncFailure, bool) {
	var failure interface {
		Stage() LoginSyncStage
		ReasonCode() string
		Classification() SyncClassification
	}
	if !errors.As(err, &failure) {
		return LoginSyncFailure{}, false
	}
	result := LoginSyncFailure{
		Stage: failure.Stage(), Reason: failure.ReasonCode(), Classification: failure.Classification(),
	}
	if !validLoginSyncFailure(result) {
		return LoginSyncFailure{}, false
	}
	return result, true
}

func validLoginSyncFailure(failure LoginSyncFailure) bool {
	if failure.Classification != SyncClassificationHarnessInvalid && failure.Classification != SyncClassificationProductFailure {
		return false
	}
	if failure.Reason == LoginSyncReasonTransport || failure.Reason == LoginSyncReasonCanceled {
		return failure.Classification == SyncClassificationHarnessInvalid &&
			(failure.Stage == LoginSyncStageFactory || failure.Stage == LoginSyncStageConnect || failure.Stage == LoginSyncStageSync)
	}
	if failure.Stage != LoginSyncStageSync {
		return false
	}
	switch failure.Reason {
	case "conversation_limit_reached":
		return failure.Classification == SyncClassificationHarnessInvalid
	case "conversation_identity_invalid", "duplicate_conversation", "last_message_invalid":
		return failure.Classification == SyncClassificationProductFailure
	default:
		return false
	}
}

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

// Stage identifies validation as part of the full conversation sync.
func (*ConversationSyncValidationError) Stage() LoginSyncStage { return LoginSyncStageSync }

// NewConversationSyncRequest constructs a fresh zero-coverage full-sync request for every login.
func NewConversationSyncRequest(uid string) target.ConversationSyncRequest {
	return target.ConversationSyncRequest{
		UID:               uid,
		CompletedCoverage: 0,
		MaxConversations:  conversationSyncMaxConversations,
	}
}

// ValidateConversationSync proves the complete bounded directory is internally valid.
func ValidateConversationSync(conversations []target.ConversationSyncConversation) error {
	if len(conversations) >= conversationSyncMaxConversations {
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

		if conversation.LastMessage != nil &&
			(conversation.LastMessage.MessageID == 0 || conversation.LastMessage.MessageSeq == 0) {
			return newConversationSyncValidationError(SyncClassificationProductFailure, "last_message_invalid")
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
	ConnectStarted          bool
	ConnectCompleted        bool
	SyncStarted             bool
	SyncCompleted           bool
}

// RunLoginSync performs CONNECT before a fresh full sync and admits traffic only after validation.
func RunLoginSync(ctx context.Context, uid string, connector LoginSyncConnector, syncer ConversationSyncer, now func() time.Time) (LoginSyncResult, error) {
	var result LoginSyncResult
	if err := ctx.Err(); err != nil {
		return result, newLoginSyncOperationError(LoginSyncStageConnect, LoginSyncReasonCanceled)
	}

	result.ConnectStarted = true
	connectStarted := now()
	connectErr := connector.Connect(ctx, uid)
	result.GatewayConnectLatency = now().Sub(connectStarted)
	if connectErr != nil {
		reason := LoginSyncReasonTransport
		if ctx.Err() != nil {
			reason = LoginSyncReasonCanceled
		}
		return result, newLoginSyncOperationError(LoginSyncStageConnect, reason)
	}
	result.ConnectCompleted = true
	if err := ctx.Err(); err != nil {
		return result, newLoginSyncOperationError(LoginSyncStageConnect, LoginSyncReasonCanceled)
	}

	result.SyncStarted = true
	syncStarted := now()
	conversations, syncErr := syncer.ConversationSync(ctx, NewConversationSyncRequest(uid))
	result.ConversationSyncLatency = now().Sub(syncStarted)
	if syncErr != nil {
		reason := LoginSyncReasonTransport
		if ctx.Err() != nil {
			reason = LoginSyncReasonCanceled
		}
		return result, newLoginSyncOperationError(LoginSyncStageSync, reason)
	}
	if err := ctx.Err(); err != nil {
		return result, newLoginSyncOperationError(LoginSyncStageSync, LoginSyncReasonCanceled)
	}
	if err := ValidateConversationSync(conversations); err != nil {
		return result, err
	}

	result.Conversations = conversations
	result.TrafficReady = true
	result.SyncCompleted = true
	return result, nil
}

type loginSyncOperationError struct {
	stage  LoginSyncStage
	reason string
}

func newLoginSyncOperationError(stage LoginSyncStage, reason string) error {
	return &loginSyncOperationError{stage: stage, reason: reason}
}

func (e *loginSyncOperationError) Error() string {
	if e == nil {
		return "login sync failed"
	}
	if e.reason == LoginSyncReasonCanceled {
		return "login sync canceled"
	}
	switch e.stage {
	case LoginSyncStageFactory:
		return "login sync session factory failed"
	case LoginSyncStageConnect:
		return "login sync gateway connect failed"
	case LoginSyncStageSync:
		return "login sync conversation request failed"
	default:
		return "login sync failed"
	}
}

func (e *loginSyncOperationError) Stage() LoginSyncStage {
	if e == nil {
		return ""
	}
	return e.stage
}

func (e *loginSyncOperationError) ReasonCode() string {
	if e == nil {
		return ""
	}
	return e.reason
}

func (e *loginSyncOperationError) Classification() SyncClassification {
	if e == nil {
		return ""
	}
	return SyncClassificationHarnessInvalid
}
