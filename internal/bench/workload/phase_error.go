package workload

import (
	"context"
	"errors"
	"strings"
)

func shouldRecordPhaseOperationError(ctx context.Context, err error) bool {
	if err == nil {
		return false
	}
	if ctx == nil {
		return true
	}
	ctxErr := ctx.Err()
	if ctxErr == nil {
		return true
	}
	return !errors.Is(err, ctxErr)
}

// shouldContinueMeasuredMessageError keeps one message-level failure from
// invalidating a timed measurement whose declared error-rate limits own the verdict.
func shouldContinueMeasuredMessageError(ctx context.Context, phase string, err error) bool {
	if err == nil || phase != "run" && !strings.HasPrefix(phase, "run-window-") {
		return false
	}
	if ctx == nil || ctx.Err() == nil {
		return true
	}
	return !errors.Is(err, ctx.Err())
}
