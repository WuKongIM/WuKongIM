package delivery

import "context"

const defaultFanoutPageSize = 512

// SubscriberPlanner pages channel subscribers for one delivery partition.
type SubscriberPlanner interface {
	NextPartitionPage(context.Context, FanoutTask, string, int) (UIDPage, error)
}

// UIDPage is one page of subscriber UIDs for a fanout task.
type UIDPage struct {
	// UIDs are recipient user IDs to resolve through presence.
	UIDs []string
	// NextCursor resumes subscriber scanning after this page.
	NextCursor string
	// Done reports whether the partition scan has reached the end.
	Done bool
}

// PresenceResolver resolves online recipient endpoints by UID.
type PresenceResolver interface {
	EndpointsByUIDs(context.Context, []string) (map[string][]Route, error)
}

// Pusher sends grouped recipient routes to their owner nodes.
type Pusher interface {
	Push(context.Context, PushCommand) (PushResult, error)
}

// FanoutWorkerOptions configures synchronous delivery fanout execution.
type FanoutWorkerOptions struct {
	// Subscribers pages channel subscribers when MessageScopedUIDs is empty.
	Subscribers SubscriberPlanner
	// Presence resolves online routes for selected UIDs.
	Presence PresenceResolver
	// Push sends grouped routes to recipient owner nodes.
	Push Pusher
	// PageSize controls subscriber page size; values <= 0 use the default.
	PageSize int
}

// FanoutWorker synchronously resolves recipients and pushes them by owner node.
type FanoutWorker struct {
	subscribers SubscriberPlanner
	presence    PresenceResolver
	push        Pusher
	pageSize    int
}

// NewFanoutWorker creates a synchronous fanout worker.
func NewFanoutWorker(opts FanoutWorkerOptions) *FanoutWorker {
	pageSize := opts.PageSize
	if pageSize <= 0 {
		pageSize = defaultFanoutPageSize
	}
	return &FanoutWorker{
		subscribers: opts.Subscribers,
		presence:    opts.Presence,
		push:        opts.Push,
		pageSize:    pageSize,
	}
}

// RunTask resolves one fanout task and pushes online routes grouped by owner node.
func (w *FanoutWorker) RunTask(ctx context.Context, task FanoutTask) error {
	if w == nil || w.presence == nil || w.push == nil {
		return nil
	}
	if len(task.Envelope.MessageScopedUIDs) > 0 {
		return w.pushUIDs(ctx, task, append([]string(nil), task.Envelope.MessageScopedUIDs...))
	}
	if w.subscribers == nil {
		return nil
	}

	cursor := task.Cursor
	for {
		page, err := w.subscribers.NextPartitionPage(ctx, task, cursor, w.pageSize)
		if err != nil {
			return err
		}
		if err := w.pushUIDs(ctx, task, append([]string(nil), page.UIDs...)); err != nil {
			return err
		}
		if page.Done {
			return nil
		}
		nextCursor := page.NextCursor
		if nextCursor == "" || nextCursor == cursor {
			return nil
		}
		cursor = nextCursor
	}
}

func (w *FanoutWorker) pushUIDs(ctx context.Context, task FanoutTask, uids []string) error {
	if len(uids) == 0 {
		return nil
	}
	routesByUID, err := w.presence.EndpointsByUIDs(ctx, uids)
	if err != nil {
		return err
	}
	grouped := make(map[uint64][]Route)
	for _, uid := range uids {
		for _, route := range routesByUID[uid] {
			if route.OwnerNodeID == 0 || isSenderSameSession(task.Envelope, route) {
				continue
			}
			grouped[route.OwnerNodeID] = append(grouped[route.OwnerNodeID], route)
		}
	}
	for ownerNodeID, routes := range grouped {
		if len(routes) == 0 {
			continue
		}
		_, err := w.push.Push(ctx, PushCommand{
			OwnerNodeID: ownerNodeID,
			Envelope:    cloneEnvelope(task.Envelope),
			Routes:      cloneRoutes(routes),
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func isSenderSameSession(env Envelope, route Route) bool {
	return route.UID == env.FromUID && env.SenderSessionID != 0 && route.SessionID == env.SenderSessionID
}

func cloneRoutes(routes []Route) []Route {
	return append([]Route(nil), routes...)
}
