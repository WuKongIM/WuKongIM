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
