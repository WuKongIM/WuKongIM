package gateway

import "github.com/WuKongIM/WuKongIM/internalv2/usecase/message"

const (
	sendackSourceSingleResult                = "single_result"
	sendackSourceSingleError                 = "single_error"
	sendackSourceSingleMissingRequestContext = "single_missing_request_context"
	sendackSourceBatchPrecheck               = "batch_precheck"
	sendackSourceBatchMissingRequestContext  = "batch_missing_request_context"
	sendackSourceBatchResult                 = "batch_result"
	sendackSourceBatchResultError            = "batch_result_error"
	sendackSourceBatchFallbackResult         = "batch_fallback_result"
	sendackSourceBatchFallbackError          = "batch_fallback_error"
)

const (
	sendackErrorClassNone                  = "none"
	sendackErrorClassUnauthenticated       = "unauthenticated"
	sendackErrorClassMissingRequestContext = "missing_request_context"
	sendackErrorClassChannelNotFound       = "channel_not_found"
	sendackErrorClassNotLeader             = "not_leader"
	sendackErrorClassStaleRoute            = "stale_route"
	sendackErrorClassRouteNotReady         = "route_not_ready"
	sendackErrorClassInvalidRequest        = "invalid_request"
	sendackErrorClassCanceled              = "canceled"
	sendackErrorClassTimeout               = "timeout"
	sendackErrorClassOther                 = "other"
)

// SendackEvent reports a low-cardinality SEND acknowledgement outcome.
type SendackEvent struct {
	// Reason is the entry-agnostic send result written to the client.
	Reason message.Reason
	// Source classifies the gateway boundary that produced the reason.
	Source string
	// ErrorClass classifies the underlying error without exposing raw text.
	ErrorClass string
}

// SendackObserver receives successful Sendack writes for diagnostics.
type SendackObserver interface {
	SendackWritten(SendackEvent)
}
