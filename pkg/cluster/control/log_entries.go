package control

import (
	"context"

	controller "github.com/WuKongIM/WuKongIM/pkg/controller"
)

// ControllerLogEntriesOptions controls a node-local Controller Raft log entry page.
type ControllerLogEntriesOptions = controller.LogEntriesOptions

// ControllerLogEntry is a read-only summary of one Controller Raft log entry.
type ControllerLogEntry = controller.LogEntry

// ControllerLogEntries is one node-local page of Controller Raft log entries.
type ControllerLogEntries = controller.LogEntries

// ControllerRaftStatus is a node-local Controller Raft status snapshot.
type ControllerRaftStatus = controller.RaftStatus

// ControllerRaftCompactionResult describes one node-local Controller Raft compaction attempt.
type ControllerRaftCompactionResult = controller.LogCompactionResult

// ControllerLogEntries returns one page from the local Controller Raft log.
func (r *Runtime) ControllerLogEntries(ctx context.Context, opts ControllerLogEntriesOptions) (ControllerLogEntries, error) {
	if err := ctxErr(ctx); err != nil {
		return ControllerLogEntries{}, err
	}
	if r == nil || r.backend == nil {
		return ControllerLogEntries{}, controller.ErrNotStarted
	}
	return r.backend.LogEntries(ctx, opts)
}

// ControllerRaftStatus returns the local Controller Raft status snapshot.
func (r *Runtime) ControllerRaftStatus(ctx context.Context) (ControllerRaftStatus, error) {
	if err := ctxErr(ctx); err != nil {
		return ControllerRaftStatus{}, err
	}
	if r == nil || r.backend == nil {
		return ControllerRaftStatus{}, controller.ErrNotStarted
	}
	return r.backend.ControllerRaftStatus(ctx)
}

// CompactControllerRaftLog forces local Controller Raft log compaction.
func (r *Runtime) CompactControllerRaftLog(ctx context.Context) (ControllerRaftCompactionResult, error) {
	if err := ctxErr(ctx); err != nil {
		return ControllerRaftCompactionResult{}, err
	}
	if r == nil || r.backend == nil {
		return ControllerRaftCompactionResult{}, controller.ErrNotStarted
	}
	return r.backend.CompactControllerRaftLog(ctx)
}
