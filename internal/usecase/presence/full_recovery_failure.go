package presence

import (
	"context"
	"errors"
	"time"

	authority "github.com/WuKongIM/WuKongIM/internal/runtime/presence"
)

// FullRecoveryFailureEvidence is one physical step's immutable failure receipt.
// EffectiveBudget is the remaining original context budget at step entry, not
// a refreshed deadline. Elapsed excludes later cleanup and follower waiting.
// The closed Stage and Kind values contain no request or owner identities.
type FullRecoveryFailureEvidence struct {
	Stage, Kind     string
	Elapsed         time.Duration
	EffectiveBudget time.Duration
}

type fullRecoveryFailureError struct {
	err      error
	evidence FullRecoveryFailureEvidence
}

func (e *fullRecoveryFailureError) Error() string { return e.err.Error() }
func (e *fullRecoveryFailureError) Unwrap() error { return e.err }

// FullRecoveryFailure exposes only the frozen scalar observation to consumers
// of the error capability; it does not retain the physical context or request.
func (e *fullRecoveryFailureError) FullRecoveryFailure() (stage, kind string, elapsed, effectiveBudget time.Duration) {
	v := e.evidence
	return v.Stage, v.Kind, v.Elapsed, v.EffectiveBudget
}

// FullRecoveryFailure extracts this module's failure receipt without accepting
// arbitrary error text or caller-supplied implementations as trusted log fields.
func FullRecoveryFailure(err error) (FullRecoveryFailureEvidence, bool) {
	var failure *fullRecoveryFailureError
	if !errors.As(err, &failure) || failure == nil {
		return FullRecoveryFailureEvidence{}, false
	}
	return failure.evidence, true
}

func fullRecoveryBudget(ctx context.Context) time.Duration {
	if deadline, ok := ctx.Deadline(); ok {
		return max(0, time.Until(deadline))
	}
	return 0
}

func observeFullRecoveryFailure(err error, stage string, started time.Time, budget time.Duration) error {
	if err == nil {
		return nil
	}
	// A joined parallel provider or nested reconciliation already froze its own
	// failed step. Outer waiting and cleanup cannot overwrite that observation.
	if _, observed := FullRecoveryFailure(err); observed {
		return err
	}
	kind := "other"
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		kind = "deadline"
	case errors.Is(err, context.Canceled):
		kind = "canceled"
	case errors.Is(err, ErrRecoveryResultBudget):
		kind = "result_budget"
	case errors.Is(err, authority.ErrStaleGeneration):
		kind = "stale_generation"
	case errors.Is(err, authority.ErrSlotIncomplete):
		kind = "slot_incomplete"
	case errors.Is(err, authority.ErrRouteNotReady):
		kind = "route_not_ready"
	case errors.Is(err, authority.ErrStaleRoute):
		kind = "stale_route"
	case errors.Is(err, authority.ErrNotLeader):
		kind = "not_leader"
	}
	return &fullRecoveryFailureError{err: err, evidence: FullRecoveryFailureEvidence{
		Stage: stage, Kind: kind, Elapsed: time.Since(started), EffectiveBudget: budget,
	}}
}
