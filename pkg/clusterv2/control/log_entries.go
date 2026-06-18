package control

import (
	"context"

	cv2 "github.com/WuKongIM/WuKongIM/pkg/controllerv2"
)

// ControllerLogEntriesOptions controls a node-local ControllerV2 Raft log entry page.
type ControllerLogEntriesOptions = cv2.LogEntriesOptions

// ControllerLogEntry is a read-only summary of one ControllerV2 Raft log entry.
type ControllerLogEntry = cv2.LogEntry

// ControllerLogEntries is one node-local page of ControllerV2 Raft log entries.
type ControllerLogEntries = cv2.LogEntries

// ControllerRaftStatus is a node-local ControllerV2 Raft status snapshot.
type ControllerRaftStatus = cv2.RaftStatus

// ControllerRaftCompactionResult describes one node-local ControllerV2 Raft compaction attempt.
type ControllerRaftCompactionResult = cv2.LogCompactionResult

// ControllerLogEntries returns one page from the local ControllerV2 Raft log.
func (r *Runtime) ControllerLogEntries(ctx context.Context, opts ControllerLogEntriesOptions) (ControllerLogEntries, error) {
	if err := ctxErr(ctx); err != nil {
		return ControllerLogEntries{}, err
	}
	if r == nil || r.backend == nil {
		return ControllerLogEntries{}, cv2.ErrNotStarted
	}
	return r.backend.LogEntries(ctx, opts)
}

// ControllerRaftStatus returns the local ControllerV2 Raft status snapshot.
func (r *Runtime) ControllerRaftStatus(ctx context.Context) (ControllerRaftStatus, error) {
	if err := ctxErr(ctx); err != nil {
		return ControllerRaftStatus{}, err
	}
	if r == nil || r.backend == nil {
		return ControllerRaftStatus{}, cv2.ErrNotStarted
	}
	return r.backend.ControllerRaftStatus(ctx)
}

// CompactControllerRaftLog forces local ControllerV2 Raft log compaction.
func (r *Runtime) CompactControllerRaftLog(ctx context.Context) (ControllerRaftCompactionResult, error) {
	if err := ctxErr(ctx); err != nil {
		return ControllerRaftCompactionResult{}, err
	}
	if r == nil || r.backend == nil {
		return ControllerRaftCompactionResult{}, cv2.ErrNotStarted
	}
	return r.backend.CompactControllerRaftLog(ctx)
}
