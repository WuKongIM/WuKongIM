package message

import (
	"context"
	"errors"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/observability/sendtrace"
)

// recordSendPermissionTrace correlates the completed permission-call boundary
// with one SEND. In a shared batch each item observes the same enclosing span;
// these overlapping durations must not be added together. The existing sink
// owns sampling, retention bounds and sender redaction; no payload is retained.
func recordSendPermissionTrace(cmd SendCommand, start, end time.Time, reason Reason, err error) {
	if cmd.TraceID == "" || start.IsZero() || !sendtrace.Enabled() {
		return
	}
	result, code := sendtrace.ResultOK, ""
	switch {
	case errors.Is(err, context.Canceled):
		result, code = sendtrace.ResultCanceled, "context_canceled"
	case errors.Is(err, context.DeadlineExceeded):
		result, code = sendtrace.ResultTimeout, "deadline_exceeded"
	case err != nil:
		result, code = sendtrace.ResultError, "permission_failed"
	case reason != ReasonSuccess:
		result, code = sendtrace.ResultError, "permission_denied"
	}
	sendtrace.Record(sendtrace.Event{
		Stage: sendtrace.StageMessagePermission, At: end, Duration: sendtrace.Elapsed(start, end),
		TraceID: cmd.TraceID, NodeID: cmd.SenderNodeID, ChannelKey: cmd.ChannelKey,
		ClientMsgNo: cmd.ClientMsgNo, FromUID: cmd.FromUID,
		Result: result, ErrorCode: code, RequestCount: 1,
	})
}
