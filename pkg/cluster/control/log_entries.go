package control

import (
	"context"

	cv2 "github.com/WuKongIM/WuKongIM/pkg/controller"
)

// ControllerLogEntriesOptions controls a node-local Controller Raft log entry page.
type ControllerLogEntriesOptions = cv2.LogEntriesOptions

// ControllerLogEntry is a read-only summary of one Controller Raft log entry.
type ControllerLogEntry = cv2.LogEntry

// ControllerLogEntries is one node-local page of Controller Raft log entries.
type ControllerLogEntries = cv2.LogEntries

// ControllerRaftStatus is a node-local Controller Raft status snapshot.
type ControllerRaftStatus = cv2.RaftStatus

// ControllerRaftCompactionResult describes one node-local Controller Raft compaction attempt.
type ControllerRaftCompactionResult = cv2.LogCompactionResult

// ControllerLogEntries returns one page from the local Controller Raft log.
func (r *Runtime) ControllerLogEntries(ctx context.Context, opts ControllerLogEntriesOptions) (ControllerLogEntries, error) {
	if err := ctxErr(ctx); err != nil {
		return ControllerLogEntries{}, err
	}
	if r == nil || r.backend == nil {
		return ControllerLogEntries{}, cv2.ErrNotStarted
	}
	return r.backend.LogEntries(ctx, opts)
}

// ControllerRaftStatus returns the local Controller Raft status snapshot.
func (r *Runtime) ControllerRaftStatus(ctx context.Context) (ControllerRaftStatus, error) {
	if err := ctxErr(ctx); err != nil {
		return ControllerRaftStatus{}, err
	}
	if r == nil || r.backend == nil {
		return ControllerRaftStatus{}, cv2.ErrNotStarted
	}
	return r.backend.ControllerRaftStatus(ctx)
}

// CompactControllerRaftLog forces local Controller Raft log compaction.
func (r *Runtime) CompactControllerRaftLog(ctx context.Context) (ControllerRaftCompactionResult, error) {
	if err := ctxErr(ctx); err != nil {
		return ControllerRaftCompactionResult{}, err
	}
	if r == nil || r.backend == nil {
		return ControllerRaftCompactionResult{}, cv2.ErrNotStarted
	}
	return r.backend.CompactControllerRaftLog(ctx)
}
