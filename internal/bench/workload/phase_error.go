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

// shouldContinueTrafficOperationError keeps a typed warmup session failure or
// one measured message failure from bypassing the declared error-rate limits.
func shouldContinueTrafficOperationError(ctx context.Context, phase string, err error) bool {
	if err == nil {
		return false
	}
	measured := phase == "run" || strings.HasPrefix(phase, "run-window-")
	if phase == "warmup" {
		var sessionErr *SessionError
		if !errors.As(err, &sessionErr) {
			return false
		}
	} else if !measured {
		return false
	}
	if ctx == nil || ctx.Err() == nil {
		return true
	}
	return !errors.Is(err, ctx.Err())
}
