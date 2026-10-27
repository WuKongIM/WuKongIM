package cluster

import (
	"context"
	"errors"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/usecase/presence"
	"github.com/WuKongIM/WuKongIM/pkg/observability/rpctrace"
	"github.com/WuKongIM/WuKongIM/pkg/transport"
)

// recoveryProviderObservation stays on the physical round's stack. Only a
// failed result receives an immutable value snapshot; no caller trace is made.
type recoveryProviderObservation struct {
	started, stageStarted, ownerStarted time.Time
	stage                               presence.RecoveryProviderStage
	inStage, inOwner                    bool
	data                                presence.RecoveryProviderFailureEvidence
}

func newRecoveryProviderObservation(calls int) recoveryProviderObservation {
	o := recoveryProviderObservation{started: time.Now()}
	if calls >= 0 {
		o.data.Calls = uint64(calls)
	}
	return o
}

func (o *recoveryProviderObservation) begin(stage presence.RecoveryProviderStage) {
	o.stage, o.stageStarted, o.inStage = stage, time.Now(), true
}

func (o *recoveryProviderObservation) end() {
	o.data.Observed |= o.stage.Mask()
	o.data.Stages[o.stage] += time.Since(o.stageStarted)
	o.inStage = false
}

func (o *recoveryProviderObservation) owner(node uint64) {
	o.begin(presence.RecoveryProviderOwnerReadStage)
	o.ownerStarted, o.inOwner = o.stageStarted, true
	o.data.OwnerNodeID = node
	o.data.OwnerError = presence.RecoveryProviderOwnerNoError
	o.data.PageStart, o.data.PageNext = 0, 0
	o.data.Observed &^= presence.RecoveryProviderObservedPageNext | presence.RecoveryProviderObservedOwnerElapsed | presence.RecoveryProviderObservedOwnerError
	o.data.OwnerElapsed = 0
	o.data.OwnerConfiguredBudget = recoveryOwnerReadBudget
	o.page(0)
	o.data.Observed |= presence.RecoveryProviderObservedOwnerBudget
}

func (o *recoveryProviderObservation) page(start int) {
	o.data.OwnerRPCTrace = rpctrace.TerminalIdentity{}
	o.data.PageStart, o.data.PageNext = uint64(start), 0
	o.data.OwnerMode = presence.RecoveryProviderOwnerNone
	o.data.OwnerEffectiveBudget = 0
	o.data.Observed &^= presence.RecoveryProviderObservedPageNext | presence.RecoveryProviderObservedOwnerDispatch
}

func (o *recoveryProviderObservation) dispatch(ctx context.Context, mode presence.RecoveryProviderOwnerMode) {
	o.data.OwnerMode = mode
	if deadline, ok := ctx.Deadline(); ok {
		o.data.OwnerEffectiveBudget = max(0, time.Until(deadline))
	}
	o.data.Observed |= presence.RecoveryProviderObservedOwnerDispatch
}

func (o *recoveryProviderObservation) ownerError(err error) {
	o.data.Observed |= presence.RecoveryProviderObservedOwnerError
	switch {
	case err == nil:
		o.data.OwnerError = presence.RecoveryProviderOwnerNoError
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, transport.ErrTimeout):
		o.data.OwnerError = presence.RecoveryProviderOwnerTimeout
	case errors.Is(err, context.Canceled):
		o.data.OwnerError = presence.RecoveryProviderOwnerCanceled
	case errors.Is(err, transport.ErrBusy):
		o.data.OwnerError = presence.RecoveryProviderOwnerBusy
	case errors.Is(err, transport.ErrDialFailed):
		o.data.OwnerError = presence.RecoveryProviderOwnerDial
	case errors.Is(err, transport.ErrNodeNotFound):
		o.data.OwnerError = presence.RecoveryProviderOwnerMissing
	default:
		o.data.OwnerError = presence.RecoveryProviderOwnerOther
	}
}

func (o *recoveryProviderObservation) ownerEnd() {
	o.data.OwnerElapsed = time.Since(o.ownerStarted)
	o.data.Observed |= presence.RecoveryProviderObservedOwnerElapsed
	o.inOwner = false
	o.end()
}

func (o *recoveryProviderObservation) failure(reason presence.RecoveryProviderReason, err error) error {
	if err == nil {
		return nil
	}
	at := time.Now()
	data := o.data
	data.Reason, data.FailureElapsed = reason, at.Sub(o.started)
	// A previous group's transport kind must not classify a later limit/fence.
	if reason != presence.RecoveryProviderOwnerUnavailable && reason != presence.RecoveryProviderOwnerPage && reason != presence.RecoveryProviderOwnerResult {
		data.OwnerError = presence.RecoveryProviderOwnerNoError
		data.Observed &^= presence.RecoveryProviderObservedOwnerError
		data.OwnerRPCTrace = rpctrace.TerminalIdentity{}
	}
	if o.inStage {
		data.Observed |= o.stage.Mask()
		data.Stages[o.stage] += at.Sub(o.stageStarted)
	}
	if o.inOwner {
		data.OwnerElapsed = at.Sub(o.ownerStarted)
		data.Observed |= presence.RecoveryProviderObservedOwnerElapsed
	}
	return presence.WithRecoveryProviderFailure(err, data)
}
