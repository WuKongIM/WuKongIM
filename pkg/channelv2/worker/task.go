package worker

import (
	"context"

	ch "github.com/WuKongIM/WuKongIM/pkg/channelv2"
)

// TaskKind identifies one blocking work category.
type TaskKind uint8

const (
	TaskFunc TaskKind = iota + 1
	TaskStoreAppend
	TaskStoreApply
	TaskStoreReadCommitted
	TaskStoreReadLog
	TaskRPCPull
	TaskRPCAck
)

// Task describes blocking work submitted to a bounded pool.
type Task struct {
	Kind    TaskKind
	Fence   ch.Fence
	RunFunc func(context.Context) Result
}

// Run executes the task payload.
func (t Task) Run(ctx context.Context) Result {
	if t.RunFunc == nil {
		return Result{Fence: t.Fence, Err: ch.ErrInvalidConfig}
	}
	return t.RunFunc(ctx)
}
