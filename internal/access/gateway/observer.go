package gateway

import (
	"time"

	"github.com/WuKongIM/WuKongIM/internal/usecase/message"
	"github.com/WuKongIM/WuKongIM/pkg/observability/sendtrace"
)

const (
	sendackSourceSingleResult                = "single_result"
	sendackSourceSingleError                 = "single_error"
	sendackSourceSingleMissingRequestContext = "single_missing_request_context"
	sendackSourceBatchPrecheck               = "batch_precheck"
	sendackSourceBatchMissingRequestContext  = "batch_missing_request_context"
	sendackSourceBatchResult                 = "batch_result"
	sendackSourceBatchResultError            = "batch_result_error"
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

const sendtraceErrorCodeWriteFailed = "write_failed"

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

type sendTraceFields struct {
	// traceID correlates sendtrace events for one SEND.
	traceID string
	// nodeID is the local gateway owner node that observed the event.
	nodeID uint64
	// channelKey is the diagnostics-safe channel lookup key.
	channelKey string
	// clientMsgNo is the client idempotency key copied into trace events.
	clientMsgNo string
	// fromUID is the authenticated sender copied into trace events.
	fromUID string
}

func sendTraceFieldsFromCommand(cmd message.SendCommand) sendTraceFields {
	return sendTraceFields{
		traceID:     cmd.TraceID,
		nodeID:      cmd.SenderNodeID,
		channelKey:  cmd.ChannelKey,
		clientMsgNo: cmd.ClientMsgNo,
		fromUID:     cmd.FromUID,
	}
}

func sendtraceElapsedSince(startedAt time.Time) time.Duration {
	return sendtrace.Elapsed(startedAt, time.Now())
}

func recordGatewayMessagesSend(cmd message.SendCommand, result message.SendResult, class string, duration time.Duration) {
	trace := sendTraceFieldsFromCommand(cmd)
	if trace.traceID == "" {
		return
	}
	traceResult, errorCode := sendtraceOutcomeForSendResult(result, class)
	sendtrace.Record(sendtrace.Event{
		Stage:       sendtrace.StageGatewayMessagesSend,
		TraceID:     trace.traceID,
		Duration:    duration,
		NodeID:      trace.nodeID,
		ChannelKey:  trace.channelKey,
		ClientMsgNo: trace.clientMsgNo,
		MessageSeq:  result.MessageSeq,
		FromUID:     trace.fromUID,
		Result:      traceResult,
		ErrorCode:   errorCode,
	})
}

func recordGatewayWriteSendack(trace sendTraceFields, result message.SendResult, err error, duration time.Duration) {
	if trace.traceID == "" {
		return
	}
	event := sendtrace.Event{
		Stage:       sendtrace.StageGatewayWriteSendack,
		TraceID:     trace.traceID,
		Duration:    duration,
		NodeID:      trace.nodeID,
		ChannelKey:  trace.channelKey,
		ClientMsgNo: trace.clientMsgNo,
		MessageSeq:  result.MessageSeq,
		FromUID:     trace.fromUID,
		Result:      sendtrace.ResultOK,
	}
	if err != nil {
		event.Result = sendtrace.ResultError
		event.ErrorCode = sendtraceErrorCodeWriteFailed
		event.Error = err.Error()
	}
	sendtrace.Record(event)
}

func sendtraceOutcomeForSendResult(result message.SendResult, class string) (sendtrace.Result, string) {
	switch class {
	case "", sendackErrorClassNone:
		if result.Reason == message.ReasonSuccess {
			return sendtrace.ResultOK, ""
		}
		return sendtrace.ResultError, sendtraceErrorCodeForReason(result.Reason)
	case sendackErrorClassCanceled:
		return sendtrace.ResultCanceled, class
	case sendackErrorClassTimeout:
		return sendtrace.ResultTimeout, class
	default:
		return sendtrace.ResultError, class
	}
}

func sendtraceErrorCodeForReason(reason message.Reason) string {
	switch reason {
	case message.ReasonSuccess:
		return ""
	case message.ReasonInvalidRequest:
		return "reason_invalid_request"
	case message.ReasonAuthFail:
		return "reason_auth_fail"
	case message.ReasonChannelNotExist:
		return "reason_channel_not_exist"
	case message.ReasonNodeNotMatch:
		return "reason_node_not_match"
	case message.ReasonSubscriberNotExist:
		return "reason_subscriber_not_exist"
	case message.ReasonInBlacklist:
		return "reason_in_blacklist"
	case message.ReasonNotAllowSend:
		return "reason_not_allow_send"
	case message.ReasonNotInWhitelist:
		return "reason_not_in_whitelist"
	case message.ReasonBan:
		return "reason_ban"
	case message.ReasonDisband:
		return "reason_disband"
	case message.ReasonSendBan:
		return "reason_send_ban"
	case message.ReasonSystemError:
		return "reason_system_error"
	case message.ReasonUnsupported:
		return "reason_unsupported"
	default:
		return "reason_unknown"
	}
}
