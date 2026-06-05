package message

import (
	"context"
	"errors"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/observability/sendtrace"
)

const (
	sendTraceErrorCodeRouteNotReady       = "route_not_ready"
	sendTraceErrorCodeStaleRoute          = "stale_route"
	sendTraceErrorCodeNotLeader           = "not_leader"
	sendTraceErrorCodeChannelNotFound     = "channel_not_found"
	sendTraceErrorCodeBackpressured       = "backpressured"
	sendTraceErrorCodeAppendResultMissing = "append_result_missing"
	sendTraceErrorCodeAppendFailed        = "append_failed"
	sendTraceErrorCodeCanceled            = "canceled"
	sendTraceErrorCodeTimeout             = "timeout"
	sendTraceErrorCodeOther               = "other"
)

func recordAppendDurableTrace(item preparedSend, messageSeq uint64, err error, duration time.Duration) {
	cmd := item.cmd
	if cmd.TraceID == "" || !sendtrace.Enabled() {
		return
	}
	result, errorCode := sendTraceResultForError(err)
	event := sendtrace.Event{
		Stage:       sendtrace.StageMessageSendDurable,
		TraceID:     cmd.TraceID,
		Duration:    duration,
		NodeID:      cmd.SenderNodeID,
		ChannelKey:  cmd.ChannelKey,
		ClientMsgNo: cmd.ClientMsgNo,
		MessageSeq:  messageSeq,
		FromUID:     cmd.FromUID,
		Result:      result,
		ErrorCode:   errorCode,
	}
	if err != nil {
		event.Error = err.Error()
	}
	sendtrace.Record(event)
}

func sendTraceResultForError(err error) (sendtrace.Result, string) {
	switch {
	case err == nil:
		return sendtrace.ResultOK, ""
	case errors.Is(err, context.Canceled):
		return sendtrace.ResultCanceled, sendTraceErrorCodeCanceled
	case errors.Is(err, context.DeadlineExceeded):
		return sendtrace.ResultTimeout, sendTraceErrorCodeTimeout
	default:
		return sendtrace.ResultError, sendTraceErrorCodeForAppendError(err)
	}
}

func sendTraceErrorCodeForAppendError(err error) string {
	switch {
	case errors.Is(err, ErrRouteNotReady):
		return sendTraceErrorCodeRouteNotReady
	case errors.Is(err, ErrStaleRoute):
		return sendTraceErrorCodeStaleRoute
	case errors.Is(err, ErrNotLeader):
		return sendTraceErrorCodeNotLeader
	case errors.Is(err, ErrChannelNotFound):
		return sendTraceErrorCodeChannelNotFound
	case errors.Is(err, ErrBackpressured):
		return sendTraceErrorCodeBackpressured
	case errors.Is(err, ErrAppendResultMissing):
		return sendTraceErrorCodeAppendResultMissing
	case errors.Is(err, ErrAppendFailed):
		return sendTraceErrorCodeAppendFailed
	default:
		return sendTraceErrorCodeOther
	}
}
