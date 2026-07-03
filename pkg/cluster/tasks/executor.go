package tasks

import (
	"context"

	"github.com/WuKongIM/WuKongIM/pkg/cluster/control"
)

// Executor reconciles active Controller tasks against local runtime state.
type Executor interface {
	Reconcile(context.Context, control.Snapshot) error
}

// CompositeExecutor runs several task executors in order.
type CompositeExecutor struct {
	executors []Executor
}

// NewCompositeExecutor creates an executor that delegates to each non-nil executor.
func NewCompositeExecutor(executors ...Executor) *CompositeExecutor {
	out := make([]Executor, 0, len(executors))
	for _, executor := range executors {
		if executor != nil {
			out = append(out, executor)
		}
	}
	return &CompositeExecutor{executors: out}
}

// Reconcile runs each child executor against the same snapshot.
func (e *CompositeExecutor) Reconcile(ctx context.Context, snapshot control.Snapshot) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if e == nil {
		return nil
	}
	for _, executor := range e.executors {
		if err := executor.Reconcile(ctx, snapshot); err != nil {
			return err
		}
	}
	return nil
}
