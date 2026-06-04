package workload

import (
	"context"
	"errors"
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
