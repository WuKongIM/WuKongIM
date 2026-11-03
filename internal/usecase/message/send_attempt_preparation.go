package message

import (
	"context"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/observability/sendtrace"
)

// recordPreparationAttempt borrows an existing stage clock for one original
// context. These shared batch/lane intervals are not exclusive item CPU time;
// the independent fixed summary capability owns all retention and fencing.
func recordPreparationAttempt(ctx context.Context, stage sendtrace.Stage, started time.Time, duration time.Duration, result string) {
	var boundary string
	switch stage {
	case sendtrace.StageMessagePermissionBatch:
		boundary = "shared_permission_batch"
	case sendtrace.StageMessagePreAppend:
		boundary = "shared_pre_append_until_submit"
	default:
		return
	}
	sendtrace.RecordContextAttemptPhase(ctx, sendtrace.AttemptPhase{
		Stage: stage, StartedAt: started, Duration: duration, Result: sendtrace.Result(result),
		Boundary: boundary, EndDerived: true,
	})
}
