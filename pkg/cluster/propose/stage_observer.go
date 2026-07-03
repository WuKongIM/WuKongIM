package propose

import (
	"context"
	"time"
)

// StageObserver receives low-cardinality proposal stage latencies.
type StageObserver interface {
	ObserveChannelAppendStage(stage string, result string, d time.Duration)
}

type stageObserverContextKey struct{}

// WithStageObserver attaches observer to proposal calls derived from one append path.
func WithStageObserver(ctx context.Context, observer StageObserver) context.Context {
	if observer == nil {
		return ctx
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, stageObserverContextKey{}, observer)
}

// StageObserverFromContext returns the proposal stage observer attached to ctx.
func StageObserverFromContext(ctx context.Context) StageObserver {
	if ctx == nil {
		return nil
	}
	observer, _ := ctx.Value(stageObserverContextKey{}).(StageObserver)
	return observer
}

// ObserveStage records one proposal stage when ctx carries a StageObserver.
func ObserveStage(ctx context.Context, stage string, err error, d time.Duration) {
	observer := StageObserverFromContext(ctx)
	if observer == nil {
		return
	}
	if d < 0 {
		d = 0
	}
	result := "ok"
	if err != nil {
		result = "err"
	}
	observer.ObserveChannelAppendStage(stage, result, d)
}
