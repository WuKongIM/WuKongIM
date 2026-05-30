package multiraft

import (
	"context"
	"time"
)

// ProposalStageObserver receives low-cardinality proposal stage latencies.
type ProposalStageObserver interface {
	ObserveProposalStage(stage string, result string, d time.Duration)
}

type proposalStageContextKey struct{}

// WithProposalStageObserver attaches observer to proposal work derived from ctx.
func WithProposalStageObserver(ctx context.Context, observer ProposalStageObserver) context.Context {
	if observer == nil {
		return ctx
	}
	if ctx == nil {
		ctx = context.Background()
	}
	existing := proposalStageObserversFromContext(ctx)
	next := make([]ProposalStageObserver, 0, len(existing)+1)
	next = append(next, existing...)
	next = append(next, observer)
	return context.WithValue(ctx, proposalStageContextKey{}, next)
}

func withProposalStageObservers(ctx context.Context, observers []ProposalStageObserver) context.Context {
	if len(observers) == 0 {
		return ctx
	}
	if ctx == nil {
		ctx = context.Background()
	}
	next := append([]ProposalStageObserver(nil), observers...)
	return context.WithValue(ctx, proposalStageContextKey{}, next)
}

func proposalStageObserversFromContext(ctx context.Context) []ProposalStageObserver {
	if ctx == nil {
		return nil
	}
	observers, _ := ctx.Value(proposalStageContextKey{}).([]ProposalStageObserver)
	return observers
}

// ObserveProposalStage records one proposal stage when ctx carries observers.
func ObserveProposalStage(ctx context.Context, stage string, err error, d time.Duration) {
	observeProposalStage(proposalStageObserversFromContext(ctx), stage, err, d)
}

func observeProposalStage(observers []ProposalStageObserver, stage string, err error, d time.Duration) {
	if len(observers) == 0 {
		return
	}
	if d < 0 {
		d = 0
	}
	result := "ok"
	if err != nil {
		result = "err"
	}
	for _, observer := range observers {
		if observer != nil {
			observer.ObserveProposalStage(stage, result, d)
		}
	}
}
