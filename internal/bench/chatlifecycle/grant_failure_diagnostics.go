package chatlifecycle

import (
	"context"
	"errors"
)

// CoordinatorGrantFailurePhase identifies a closed worker-control boundary.
type CoordinatorGrantFailurePhase string

const (
	CoordinatorGrantPhaseRequest  CoordinatorGrantFailurePhase = "request"
	CoordinatorGrantPhaseBody     CoordinatorGrantFailurePhase = "body"
	CoordinatorGrantPhaseResponse CoordinatorGrantFailurePhase = "response"
	CoordinatorGrantPhaseWorker   CoordinatorGrantFailurePhase = "worker"
)

// CoordinatorGrantFailureReason describes observation, never product attribution.
type CoordinatorGrantFailureReason string

const (
	CoordinatorGrantReasonDeadline   CoordinatorGrantFailureReason = "deadline"
	CoordinatorGrantReasonCanceled   CoordinatorGrantFailureReason = "canceled"
	CoordinatorGrantReasonHTTPStatus CoordinatorGrantFailureReason = "http_status"
	CoordinatorGrantReasonTransport  CoordinatorGrantFailureReason = "transport"
	CoordinatorGrantReasonBodyRead   CoordinatorGrantFailureReason = "body_read"
	CoordinatorGrantReasonBodyLimit  CoordinatorGrantFailureReason = "body_limit"
	CoordinatorGrantReasonBodyDecode CoordinatorGrantFailureReason = "body_decode"
	CoordinatorGrantReasonIdentity   CoordinatorGrantFailureReason = "identity"
	CoordinatorGrantReasonSequence   CoordinatorGrantFailureReason = "sequence"
	CoordinatorGrantReasonReleased   CoordinatorGrantFailureReason = "released"
	CoordinatorGrantReasonUnknown    CoordinatorGrantFailureReason = "unknown"
)

// CoordinatorGrantDeadlineSource retains only causally proven context ownership.
type CoordinatorGrantDeadlineSource string

const (
	CoordinatorGrantDeadlineRound   CoordinatorGrantDeadlineSource = "round"
	CoordinatorGrantDeadlineParent  CoordinatorGrantDeadlineSource = "parent"
	CoordinatorGrantDeadlineUnknown CoordinatorGrantDeadlineSource = "unknown"
)

// CoordinatorGrantFailure retains the terminal attempt of one failed worker.
// It contains no fence strings, endpoint, credentials, body, or arbitrary error.
type CoordinatorGrantFailure struct {
	// WorkerID must match its fixed vector position; Sequence is the exact grant.
	WorkerID uint64 `json:"worker_id"`
	Sequence uint64 `json:"sequence"`
	// Attempts is the original bounded mutation-attempt count, never a new retry.
	Attempts uint8                         `json:"attempts"`
	Phase    CoordinatorGrantFailurePhase  `json:"phase"`
	Reason   CoordinatorGrantFailureReason `json:"reason"`
	// HTTPStatus is zero when the failing port did not retain response headers.
	HTTPStatus     int                            `json:"http_status,omitempty"`
	DeadlineSource CoordinatorGrantDeadlineSource `json:"deadline_source,omitempty"`
}

// CoordinatorGrantFailures has one fixed position per worker and no history.
type CoordinatorGrantFailures [coordinatorWorkerCount]CoordinatorGrantFailure

// Bounded projects only valid failed-worker positions and closes all strings
// again before a CLI or another local consumer retains these observations.
func (failures CoordinatorGrantFailures) Bounded() []CoordinatorGrantFailure {
	var bounded []CoordinatorGrantFailure
	for workerID, failure := range failures {
		if failure.Reason == "" || failure.WorkerID != uint64(workerID) || failure.Sequence == 0 || failure.Attempts == 0 || failure.Attempts > 2 {
			continue
		}
		switch failure.Phase {
		case CoordinatorGrantPhaseRequest, CoordinatorGrantPhaseBody, CoordinatorGrantPhaseResponse, CoordinatorGrantPhaseWorker:
		default:
			failure.Phase = CoordinatorGrantPhaseWorker
		}
		switch failure.Reason {
		case CoordinatorGrantReasonDeadline, CoordinatorGrantReasonCanceled, CoordinatorGrantReasonHTTPStatus,
			CoordinatorGrantReasonTransport, CoordinatorGrantReasonBodyRead, CoordinatorGrantReasonBodyLimit,
			CoordinatorGrantReasonBodyDecode, CoordinatorGrantReasonIdentity, CoordinatorGrantReasonSequence,
			CoordinatorGrantReasonReleased, CoordinatorGrantReasonUnknown:
		default:
			failure.Reason = CoordinatorGrantReasonUnknown
		}
		if failure.HTTPStatus < 100 || failure.HTTPStatus > 599 {
			failure.HTTPStatus = 0
		}
		if failure.Reason != CoordinatorGrantReasonDeadline && failure.Reason != CoordinatorGrantReasonCanceled {
			failure.DeadlineSource = ""
		} else {
			switch failure.DeadlineSource {
			case CoordinatorGrantDeadlineRound, CoordinatorGrantDeadlineParent, CoordinatorGrantDeadlineUnknown:
			default:
				failure.DeadlineSource = CoordinatorGrantDeadlineUnknown
			}
		}
		bounded = append(bounded, failure)
	}
	return bounded
}

// workerGrantClientError keeps the existing private causal chain while exposing
// only the bounded boundary captured by the real HTTP Grant adapter.
type workerGrantClientError struct {
	phase  CoordinatorGrantFailurePhase
	reason CoordinatorGrantFailureReason
	status int
	cause  error
	// contextCause is frozen only when the HTTP error matches the caller's
	// actual context cause. It does not replace the existing unwrap chain.
	contextCause error
}

func (e *workerGrantClientError) Error() string {
	return "chat lifecycle worker grant delivery failed"
}

func (e *workerGrantClientError) Unwrap() error { return e.cause }

func workerGrantResponseError(response any, phase CoordinatorGrantFailurePhase, reason CoordinatorGrantFailureReason, status int, cause error) error {
	if _, grant := response.(*WorkerGrantResponse); !grant {
		return cause
	}
	return &workerGrantClientError{phase: phase, reason: reason, status: status, cause: cause}
}

func workerGrantContextReason(cause error) CoordinatorGrantFailureReason {
	if errors.Is(cause, context.DeadlineExceeded) {
		return CoordinatorGrantReasonDeadline
	}
	if errors.Is(cause, context.Canceled) {
		return CoordinatorGrantReasonCanceled
	}
	return CoordinatorGrantReasonUnknown
}

// workerGrantContextResponseError observes custom WithTimeoutCause transport
// errors without changing the error the original client exposed to its caller.
func workerGrantContextResponseError(
	response any, ctx context.Context, phase CoordinatorGrantFailurePhase,
	fallback CoordinatorGrantFailureReason, status int, observed, preserved error,
) error {
	if _, grant := response.(*WorkerGrantResponse); !grant {
		return preserved
	}
	failure := &workerGrantClientError{phase: phase, reason: fallback, status: status, cause: preserved}
	if ctx.Err() != nil && (errors.Is(observed, ctx.Err()) || errors.Is(observed, context.Cause(ctx))) {
		failure.reason = workerGrantContextReason(ctx.Err())
		failure.contextCause = context.Cause(ctx)
	}
	return failure
}

func workerGrantInvalidResponseReason(response any) CoordinatorGrantFailureReason {
	grant, ok := response.(*WorkerGrantResponse)
	if !ok {
		return CoordinatorGrantReasonUnknown
	}
	if !validWorkerFence(grant.WorkerFence) || grant.WorkerCount != coordinatorWorkerCount || grant.WorkerID >= grant.WorkerCount {
		return CoordinatorGrantReasonIdentity
	}
	if grant.Sequence == 0 {
		return CoordinatorGrantReasonSequence
	}
	return CoordinatorGrantReasonUnknown
}

// coordinatorGrantFailure captures only the failed terminal attempt. An opaque
// CoordinatorWorker port cannot claim an HTTP boundary or a causal deadline.
func coordinatorGrantFailure(
	parent, round context.Context,
	assignment WorkerAssignment,
	request WorkerGrantRequest,
	response WorkerGrantResponse,
	attempts uint8,
	err error,
) CoordinatorGrantFailure {
	failure := CoordinatorGrantFailure{
		WorkerID: assignment.WorkerID, Sequence: request.Sequence, Attempts: attempts,
		Phase: CoordinatorGrantPhaseWorker, Reason: CoordinatorGrantReasonUnknown,
	}
	if err != nil {
		var clientError *workerGrantClientError
		if errors.As(err, &clientError) {
			failure.Phase, failure.Reason = clientError.phase, clientError.reason
			if clientError.status >= 100 && clientError.status <= 599 {
				failure.HTTPStatus = clientError.status
			}
			if failure.Reason == CoordinatorGrantReasonDeadline || failure.Reason == CoordinatorGrantReasonCanceled {
				failure.DeadlineSource = CoordinatorGrantDeadlineUnknown
				if round.Err() != nil && clientError.contextCause != nil && errors.Is(clientError.contextCause, context.Cause(round)) {
					if errors.Is(context.Cause(round), errCoordinatorRoundDeadline) {
						failure.DeadlineSource = CoordinatorGrantDeadlineRound
					} else if parent.Err() != nil && errors.Is(clientError.contextCause, context.Cause(parent)) {
						failure.DeadlineSource = CoordinatorGrantDeadlineParent
					}
				}
			}
		}
		return failure
	}
	failure.Phase = CoordinatorGrantPhaseResponse
	expectedReleased, _ := request.Released.worker(assignment.WorkerID)
	switch {
	case !sameWorkerFence(response.WorkerFence, assignment.WorkerFence) || response.WorkerID != assignment.WorkerID || response.WorkerCount != coordinatorWorkerCount:
		failure.Reason = CoordinatorGrantReasonIdentity
	case response.Sequence != request.Sequence:
		failure.Reason = CoordinatorGrantReasonSequence
	case response.Released != expectedReleased:
		failure.Reason = CoordinatorGrantReasonReleased
	}
	return failure
}
