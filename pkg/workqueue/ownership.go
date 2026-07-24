package workqueue

import (
	"time"

	goruntimeregistry "github.com/WuKongIM/WuKongIM/pkg/goroutine"
)

func normalizeOwnership(registry *goruntimeregistry.Registry, task goruntimeregistry.TaskID) (*goruntimeregistry.Registry, goruntimeregistry.TaskID) {
	if task == "" {
		task = goruntimeregistry.TaskAppDetachedWorkqueue
	}
	return registry, task
}

func poolPanicHandler(registry *goruntimeregistry.Registry, task goruntimeregistry.TaskID) func(any) {
	return func(recovered any) {
		registry.ReportPoolPanic(task, recovered)
	}
}

func poolGoroutines(running int, closed bool) int64 {
	if !closed {
		running++
	}
	return int64(running)
}

func unregisterPoolAfterWorkersExit(
	registry *goruntimeregistry.Registry,
	task goruntimeregistry.TaskID,
	running func() int,
	unregister func(),
) {
	if unregister == nil {
		return
	}
	goruntimeregistry.SafeGo(registry, task, func() {
		ticker := time.NewTicker(time.Millisecond)
		defer ticker.Stop()
		for running() > 0 {
			<-ticker.C
		}
		unregister()
	})
}
