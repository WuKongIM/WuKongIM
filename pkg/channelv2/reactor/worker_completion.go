package reactor

import "github.com/WuKongIM/WuKongIM/pkg/channelv2/worker"

func (r *Reactor) handleWorkerResult(event Event) {
	switch event.Worker.Kind {
	case worker.TaskStoreAppend:
		r.handleStoreAppendResult(event.Worker)
	case worker.TaskStoreLoad:
		r.handleStoreLoadResult(event.Worker)
	case worker.TaskStoreReadLog:
		r.handleStoreReadLogResult(event.Worker)
	case worker.TaskStoreLookupMessage:
		r.handleStoreLookupMessageResult(event.Worker)
	case worker.TaskStoreCheckpoint:
		r.handleStoreCheckpointResult(event.Worker)
	case worker.TaskStoreClose:
	case worker.TaskStoreRetention:
		r.handleStoreRetentionResult(event.Worker)
	case worker.TaskRPCPull:
		r.handleRPCPullResult(event.Worker)
	case worker.TaskStoreApply:
		r.handleStoreApplyResult(event.Worker)
	case worker.TaskRPCAck:
		r.handleRPCAckResult(event.Worker)
	case worker.TaskRPCPullHint:
		r.handleRPCPullHintResult(event.Worker)
	}
}
