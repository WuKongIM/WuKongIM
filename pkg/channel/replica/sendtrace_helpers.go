package replica

import (
	"context"
	"errors"

	"github.com/WuKongIM/WuKongIM/pkg/observability/sendtrace"
)

const maxTraceErrorBytes = 256

func sendTraceResultFromError(err error) sendtrace.Result {
	if err == nil {
		return sendtrace.ResultOK
	}
	if errors.Is(err, context.Canceled) {
		return sendtrace.ResultCanceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return sendtrace.ResultTimeout
	}
	return sendtrace.ResultError
}

func sendTraceErrorCode(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.Canceled) {
		return "context_canceled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "deadline_exceeded"
	}
	return "unknown_error"
}

func shortTraceError(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	if len(msg) > maxTraceErrorBytes {
		return msg[:maxTraceErrorBytes]
	}
	return msg
}
