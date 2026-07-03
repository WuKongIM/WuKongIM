package controller

import "context"

// LogEntries returns one page from the local ControllerV2 Raft log.
func (r *Runtime) LogEntries(ctx context.Context, opts LogEntriesOptions) (LogEntries, error) {
	if err := ctxErr(ctx); err != nil {
		return LogEntries{}, err
	}
	if r == nil || r.raft == nil {
		return LogEntries{}, ErrNotStarted
	}
	return r.raft.LogEntries(ctx, opts)
}
