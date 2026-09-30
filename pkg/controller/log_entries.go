package controller

import "context"

// LogEntries returns one page from the local Controller Raft log.
func (r *Runtime) LogEntries(ctx context.Context, opts LogEntriesOptions) (LogEntries, error) {
	if err := ctxErr(ctx); err != nil {
		return LogEntries{}, err
	}
	service := r.raftService()
	if service == nil {
		return LogEntries{}, ErrNotStarted
	}
	return service.LogEntries(ctx, opts)
}
