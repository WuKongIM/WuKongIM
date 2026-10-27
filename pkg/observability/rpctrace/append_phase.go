package rpctrace

import "context"

// AppendPhaseObserver observes one shared append invocation. Implementations
// retain participating sampled owners, never an originating caller context.
type AppendPhaseObserver interface {
	BeginAppendPhase(Stage) AppendPhaseEnd
}

// AppendPhaseEnd is a synchronous, zero-safe completion handle. Implementations
// must consume resultAt before returning and must never retain it. Count -1
// with a nil reader denotes the common result supplied by End.
type AppendPhaseEnd func(common Result, count int, resultAt func(int) Result)

// End supplies one common outcome for every participating owner.
func (end AppendPhaseEnd) End(result Result) {
	if end != nil {
		end(result, -1, nil)
	}
}

// EndItems supplies results aligned with this invocation's storage items.
// Invalid cardinality or unavailable results are unfinished observations.
func (end AppendPhaseEnd) EndItems(count int, resultAt func(int) Result) {
	if end != nil {
		if count < 0 {
			end(ResultUnfinished, 0, nil)
			return
		}
		end(ResultOK, count, resultAt)
	}
}

type appendPhaseContextKey struct{}

// AppendPhaseObserverFrom returns observation ownership without retaining the
// source context's cancellation, deadline, or unrelated values.
func AppendPhaseObserverFrom(ctx context.Context) AppendPhaseObserver {
	if ctx == nil {
		return nil
	}
	observer, _ := ctx.Value(appendPhaseContextKey{}).(AppendPhaseObserver)
	return observer
}

// WithAppendPhaseObserver preserves the runtime context's cancellation and
// values. It never installs a single Trace. Nil observers keep the original ctx.
func WithAppendPhaseObserver(ctx context.Context, observer AppendPhaseObserver) context.Context {
	if observer == nil {
		return ctx
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, appendPhaseContextKey{}, observer)
}

// BeginAppendPhase starts one of the fixed shared-append phases only when an
// explicit observer is attached. A single Trace never enables this port.
func BeginAppendPhase(ctx context.Context, stage Stage) AppendPhaseEnd {
	if ctx == nil || !validAppendPhase(stage) {
		return nil
	}
	observer := AppendPhaseObserverFrom(ctx)
	if observer == nil {
		return nil
	}
	return observer.BeginAppendPhase(stage)
}

func validAppendPhase(stage Stage) bool {
	switch stage {
	case StageChannelMetaResolve, StageChannelMetaApply, StageChannelRuntimeAppend,
		StageChannelRuntimeReserve, StageChannelRuntimeSubmit, StageChannelRuntimeFutureWait,
		StageChannelLocalQueue, StageChannelLocalSync, StageChannelPeerWait,
		StageChannelQuorumDecision, StageChannelReactorComplete:
		return true
	default:
		return false
	}
}
