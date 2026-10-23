package core

import (
	"context"
	"errors"

	"github.com/WuKongIM/WuKongIM/pkg/observability/rpctrace"
)

// RPCTraceResult preserves transport's typed outcomes without retaining error text.
func RPCTraceResult(err error) rpctrace.Result {
	switch {
	case err == nil:
		return rpctrace.ResultOK
	case errors.Is(err, ErrTimeout), errors.Is(err, context.DeadlineExceeded):
		return rpctrace.ResultTimeout
	case errors.Is(err, ErrCanceled), errors.Is(err, context.Canceled):
		return rpctrace.ResultCanceled
	case errors.Is(err, ErrBusy), errors.Is(err, ErrQueueFull):
		return rpctrace.ResultBusy
	case errors.Is(err, ErrStopped):
		return rpctrace.ResultStopped
	default:
		return rpctrace.ResultError
	}
}

// FinishRPCTrace transfers a detached sample once to the existing bounded drain.
func FinishRPCTrace(observer Observer, nodeID NodeID, trace *rpctrace.Trace, result rpctrace.Result) {
	sample := trace.Finish(result)
	if sample != nil && observer != nil {
		observer.ObserveTransport(Event{Name: "rpc_trace", NodeID: nodeID, ServiceID: sample.ServiceID, RPCTrace: sample})
	}
}
