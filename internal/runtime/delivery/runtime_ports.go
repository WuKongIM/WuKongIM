package delivery

import (
	"context"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/contracts/authority"
	channelappendcontract "github.com/WuKongIM/WuKongIM/internal/contracts/channelappend"
	"github.com/WuKongIM/WuKongIM/internal/contracts/onlinedelivery"
)

// PlanAdmitter is the Online Delivery interface used by channel append.
type PlanAdmitter interface {
	EnqueueRecipientDeliveryPlan(context.Context, onlinedelivery.RecipientDeliveryPlan) error
}

// OwnerPushHandler is the Online Delivery interface used by owner-push RPC.
type OwnerPushHandler interface {
	PushOwner(context.Context, onlinedelivery.OwnerPush) (onlinedelivery.OwnerPushResult, error)
}

// FeedbackHandler is the Online Delivery interface used by gateway feedback.
type FeedbackHandler interface {
	Recvack(context.Context, Recvack) error
	SessionClosed(context.Context, SessionClosed) error
}

// TargetPresenceResult is aligned with one exact recipient-authority target.
type TargetPresenceResult struct {
	// Routes contains the exact online endpoints resolved for the target.
	Routes []onlinedelivery.Route
	// Err is scoped only to the corresponding target.
	Err error
}

// PlanPresenceResolver resolves exact target groups without discarding their fences.
type PlanPresenceResolver interface {
	EndpointsByTargets(context.Context, []onlinedelivery.RecipientTargetBatch) []TargetPresenceResult
}

// RemoteOwnerPusher forwards a push to a non-local owner node.
type RemoteOwnerPusher interface {
	PushOwner(context.Context, onlinedelivery.OwnerPush) (onlinedelivery.OwnerPushResult, error)
}

// SessionWriteDisposition is the final classification of one exact local session write.
type SessionWriteDisposition uint8

const (
	// SessionWriteAccepted means the session accepted the packet.
	SessionWriteAccepted SessionWriteDisposition = iota + 1
	// SessionWriteRetryable means the exact route may be retried.
	SessionWriteRetryable
	// SessionWriteDropped means the exact route is terminally stale or invalid.
	SessionWriteDropped
)

// LocalSessionWrite contains one final exact-session write attempt.
type LocalSessionWrite struct {
	// Event is immutable shared message storage valid for this synchronous call.
	Event channelappendcontract.CommittedEnvelope
	// Route is the exact owner-local session fence to validate before writing.
	Route onlinedelivery.Route
}

// SessionWriteResult classifies one local session adapter result.
type SessionWriteResult struct {
	// Disposition controls exact-route retry and terminal ACK rollback.
	Disposition SessionWriteDisposition
	// Err carries bounded diagnostic context and never decides retry by itself.
	Err error
}

// LocalSessionWriter validates, builds, and writes one exact owner-local packet.
// It never receives pending-ACK tokens.
type LocalSessionWriter interface {
	WriteSession(context.Context, LocalSessionWrite) SessionWriteResult
}

// OfflineRecipientsEvent reports durable recipients with no online route.
type OfflineRecipientsEvent struct {
	// Event is the durable message whose successfully resolved target had offline recipients.
	Event channelappendcontract.CommittedEnvelope
	// UIDs is one call-owned, deduplicated offline recipient batch.
	UIDs []string
}

// OfflineRecipientsObserver receives one durable-only offline recipient batch.
type OfflineRecipientsObserver interface {
	ObserveOfflineRecipients(context.Context, OfflineRecipientsEvent)
}

// PlanAdmissionEvent describes one bounded plan admission result.
type PlanAdmissionEvent struct {
	// Result is a bounded observation result.
	Result ObservationResult
	// QueueDepth is the queue size observed after the admission attempt.
	QueueDepth int
	// QueueCapacity is the fixed plan queue capacity.
	QueueCapacity int
	// Duration is the caller-visible admission latency.
	Duration time.Duration
}

// PlanFailurePhase identifies the bounded stage that ended plan processing.
type PlanFailurePhase string

const (
	// PlanFailurePhaseContext reports cancellation before target processing.
	PlanFailurePhaseContext PlanFailurePhase = "context"
	// PlanFailurePhasePresence reports exact-target presence resolution failure.
	PlanFailurePhasePresence PlanFailurePhase = "presence"
	// PlanFailurePhaseOwnerPush reports local or remote owner execution failure.
	PlanFailurePhaseOwnerPush PlanFailurePhase = "owner_push"
	// PlanFailurePhasePanic reports an isolated unexpected plan panic.
	PlanFailurePhasePanic PlanFailurePhase = "panic"
)

// PlanFailureSample carries at most one recipient and exact authority target
// for bounded failure logging. None of its identity fields are metric labels.
type PlanFailureSample struct {
	// Phase identifies the failed Online Delivery stage.
	Phase PlanFailurePhase
	// Err is the original error retained for structured logging.
	Err error
	// RecipientUID is one representative recipient from the failed target.
	RecipientUID string
	// Target is the representative exact authority fence.
	Target authority.Target
	// OwnerNodeID identifies the failed owner group when known.
	OwnerNodeID uint64
}

// ObservationResult is the closed low-cardinality result vocabulary shared by
// Online Delivery observers.
type ObservationResult string

const (
	// ObservationResultAccepted reports successful bounded admission.
	ObservationResultAccepted ObservationResult = "accepted"
	// ObservationResultInvalid reports rejected invalid input.
	ObservationResultInvalid ObservationResult = "invalid"
	// ObservationResultClosed reports rejection by a closed or closing runtime.
	ObservationResultClosed ObservationResult = "closed"
	// ObservationResultOK reports successful terminal processing.
	ObservationResultOK ObservationResult = "ok"
	// ObservationResultPanic reports an isolated panic.
	ObservationResultPanic ObservationResult = "panic"
	// ObservationResultTimeout reports deadline expiry.
	ObservationResultTimeout ObservationResult = "timeout"
	// ObservationResultCanceled reports explicit cancellation.
	ObservationResultCanceled ObservationResult = "canceled"
	// ObservationResultError reports a non-specialized terminal error.
	ObservationResultError ObservationResult = "error"
	// ObservationResultRetryExhausted reports bounded owner-push retry exhaustion.
	ObservationResultRetryExhausted ObservationResult = "retry_exhausted"
	// ObservationResultRetryable reports at least one exact route eligible for retry.
	ObservationResultRetryable ObservationResult = "retryable"
	// ObservationResultDropped reports at least one terminally dropped exact route.
	ObservationResultDropped ObservationResult = "dropped"
)

// PlanTerminalEvent describes terminal processing for accepted plan work.
type PlanTerminalEvent struct {
	// Result is the bounded terminal outcome.
	Result ObservationResult
	// Mode preserves the plan's explicit Durable or Transient semantics.
	Mode onlinedelivery.Mode
	// Recipients is the bounded number of recipient rows processed.
	Recipients int
	// Duration is total asynchronous processing time.
	Duration time.Duration
	// Failure contains one bounded diagnostic sample when Result is not OK.
	Failure PlanFailureSample
}

// RuntimePressureEvent describes bounded Online Delivery queue and worker pressure.
type RuntimePressureEvent struct {
	// QueueDepth is the current accepted-plan backlog.
	QueueDepth int
	// QueueCapacity is the fixed accepted-plan queue bound.
	QueueCapacity int
	// Inflight is the current number of executing plan workers.
	Inflight int
	// Workers is the fixed plan-worker capacity.
	Workers int
}

// OwnerPushFailureSample carries one failed exact route for bounded logging.
// Route identities are never used as metric labels.
type OwnerPushFailureSample struct {
	// Err is the adapter or context error when one is available.
	Err error
	// Route is one representative retryable or dropped exact route.
	Route onlinedelivery.Route
}

// OwnerPushEvent describes one owner-node push attempt.
type OwnerPushEvent struct {
	// OwnerNodeID identifies the exact owner group.
	OwnerNodeID uint64
	// Result is the bounded push outcome.
	Result ObservationResult
	// Routes is the number of exact routes submitted.
	Routes int
	// Accepted is the number of successful session writes.
	Accepted int
	// Retryable is the number of exact routes eligible for retry.
	Retryable int
	// Dropped is the number of terminally rejected exact routes.
	Dropped int
	// Duration is the owner-local execution latency.
	Duration time.Duration
	// Failure contains at most one exact-route diagnostic sample.
	Failure OwnerPushFailureSample
}

// RuntimeObserver receives low-cardinality Online Delivery observations.
type RuntimeObserver interface {
	ObservePlanAdmission(PlanAdmissionEvent)
	ObservePlanTerminal(PlanTerminalEvent)
	SetRuntimePressure(RuntimePressureEvent)
	ObserveOwnerPush(OwnerPushEvent)
}
