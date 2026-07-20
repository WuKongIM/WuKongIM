package delivery

import (
	"context"
	"errors"
	"time"
)

const (
	defaultFanoutPageSize = 512
	defaultPushBatchSize  = 512
)

// ErrInvalidSubscriberCursor reports a non-terminal page that cannot advance scanning.
var ErrInvalidSubscriberCursor = errors.New("internal/runtime/delivery: invalid subscriber cursor")

// ErrRetryablePushRoutes reports that at least one pushed route needs retry scheduling.
var ErrRetryablePushRoutes = errors.New("internal/runtime/delivery: retryable push routes")

// RetryablePushRoutesError carries owner routes that should be retried later.
type RetryablePushRoutesError struct {
	routes []Route
}

func newRetryablePushRoutesError(routes []Route) error {
	if len(routes) == 0 {
		return ErrRetryablePushRoutes
	}
	return &RetryablePushRoutesError{routes: cloneRoutes(routes)}
}

// Error returns the stable retryable push error text.
func (e *RetryablePushRoutesError) Error() string {
	return ErrRetryablePushRoutes.Error()
}

// Unwrap exposes ErrRetryablePushRoutes for errors.Is checks.
func (e *RetryablePushRoutesError) Unwrap() error {
	return ErrRetryablePushRoutes
}

// Routes returns an independent copy of retryable owner routes.
func (e *RetryablePushRoutesError) Routes() []Route {
	if e == nil {
		return nil
	}
	return cloneRoutes(e.routes)
}

// SubscriberPlanner pages channel subscribers for one delivery partition.
type SubscriberPlanner interface {
	NextPartitionPage(context.Context, FanoutTask, string, int) (UIDPage, error)
}

// UIDPage is one page of subscriber UIDs for a fanout task.
type UIDPage struct {
	// UIDs are recipient user IDs to resolve through presence; callers treat the slice as read-only.
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
	// PushBatchSize limits owner-node routes sent in one Push command; values <= 0 use the default.
	PushBatchSize int
	// Observer receives bounded fanout resolve and push observations.
	Observer Observer
}

// FanoutWorker synchronously resolves recipients and pushes them by owner node.
type FanoutWorker struct {
	subscribers   SubscriberPlanner
	presence      PresenceResolver
	push          Pusher
	pageSize      int
	pushBatchSize int
	observer      Observer
}

// NewFanoutWorker creates a synchronous fanout worker.
func NewFanoutWorker(opts FanoutWorkerOptions) *FanoutWorker {
	pageSize := opts.PageSize
	if pageSize <= 0 {
		pageSize = defaultFanoutPageSize
	}
	pushBatchSize := opts.PushBatchSize
	if pushBatchSize <= 0 {
		pushBatchSize = defaultPushBatchSize
	}
	return &FanoutWorker{
		subscribers:   opts.Subscribers,
		presence:      opts.Presence,
		push:          opts.Push,
		pageSize:      pageSize,
		pushBatchSize: pushBatchSize,
		observer:      opts.Observer,
	}
}

// RunTask resolves one fanout task and pushes online routes grouped by owner node.
func (w *FanoutWorker) RunTask(ctx context.Context, task FanoutTask) error {
	if w == nil || w.presence == nil || w.push == nil {
		return nil
	}
	if len(task.Envelope.MessageScopedUIDs) > 0 {
		return w.pushUIDs(ctx, task, task.Envelope.MessageScopedUIDs)
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
		if err := w.pushUIDs(ctx, task, page.UIDs); err != nil {
			return err
		}
		if page.Done {
			return nil
		}
		nextCursor := page.NextCursor
		if nextCursor == "" || nextCursor == cursor {
			return ErrInvalidSubscriberCursor
		}
		cursor = nextCursor
	}
}

func (w *FanoutWorker) pushUIDs(ctx context.Context, task FanoutTask, uids []string) error {
	if len(uids) == 0 {
		return nil
	}
	observing := w.observer != nil
	var resolveStarted time.Time
	if observing {
		resolveStarted = time.Now()
	}
	routesByUID, err := w.presence.EndpointsByUIDs(ctx, uids)
	if err != nil {
		if observing {
			w.observeFanoutResolve(FanoutResolveEvent{
				ChannelType: task.Envelope.ChannelType,
				Result:      deliveryResultForError(err),
				ErrorClass:  DeliveryErrorClass(err),
				Duration:    time.Since(resolveStarted),
				Pages:       1,
				UIDs:        len(uids),
			})
		}
		return err
	}
	if observing {
		resolvedRoutes := 0
		for _, routes := range routesByUID {
			resolvedRoutes += len(routes)
		}
		w.observeFanoutResolve(FanoutResolveEvent{
			ChannelType: task.Envelope.ChannelType,
			Result:      DeliveryResultOK,
			ErrorClass:  DeliveryErrorClassNone,
			Duration:    time.Since(resolveStarted),
			Pages:       1,
			UIDs:        len(uids),
			Routes:      resolvedRoutes,
		})
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
	var retryableRoutes []Route
	for ownerNodeID, routes := range grouped {
		if len(routes) == 0 {
			continue
		}
		for start := 0; start < len(routes); start += w.pushBatchSize {
			end := start + w.pushBatchSize
			if end > len(routes) {
				end = len(routes)
			}
			batch := routes[start:end]
			var pushStarted time.Time
			if observing {
				pushStarted = time.Now()
			}
			result, err := w.push.Push(ctx, PushCommand{
				OwnerNodeID: ownerNodeID,
				Envelope:    cloneEnvelope(task.Envelope),
				Routes:      cloneRoutes(batch),
			})
			if err != nil {
				if observing {
					w.observeFanoutPush(FanoutPushEvent{
						OwnerNodeID: ownerNodeID,
						Result:      deliveryResultForError(err),
						ErrorClass:  DeliveryErrorClass(err),
						Duration:    time.Since(pushStarted),
						Routes:      len(batch),
					})
				}
				return err
			}
			pushResult, errorClass := ClassifyPushObservation(len(result.Retryable), len(result.Dropped), nil)
			if observing {
				w.observeFanoutPush(FanoutPushEvent{
					OwnerNodeID: ownerNodeID,
					Result:      pushResult,
					ErrorClass:  errorClass,
					Duration:    time.Since(pushStarted),
					Routes:      len(batch),
					Accepted:    len(result.Accepted),
					Retryable:   len(result.Retryable),
					Dropped:     len(result.Dropped),
				})
			}
			retryableRoutes = append(retryableRoutes, result.Retryable...)
		}
	}
	if len(retryableRoutes) > 0 {
		return newRetryablePushRoutesError(retryableRoutes)
	}
	return nil
}

func (w *FanoutWorker) observeFanoutResolve(event FanoutResolveEvent) {
	if w == nil || w.observer == nil {
		return
	}
	w.observer.ObserveFanoutResolve(event)
}

func (w *FanoutWorker) observeFanoutPush(event FanoutPushEvent) {
	if w == nil || w.observer == nil {
		return
	}
	w.observer.ObserveFanoutPush(event)
}

func isSenderSameSession(env Envelope, route Route) bool {
	return env.SenderNodeID != 0 &&
		route.OwnerNodeID == env.SenderNodeID &&
		route.UID == env.FromUID &&
		env.SenderSessionID != 0 &&
		route.SessionID == env.SenderSessionID
}

func cloneRoutes(routes []Route) []Route {
	return append([]Route(nil), routes...)
}
