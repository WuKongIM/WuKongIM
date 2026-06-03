package delivery

import (
	"context"
	"errors"
	"time"
)

const (
	// DeliveryResultOK reports a completed delivery operation.
	DeliveryResultOK = "ok"
	// DeliveryResultRetryable reports a delivery operation that should be retried later.
	DeliveryResultRetryable = "retryable"
	// DeliveryResultError reports a non-retryable delivery operation failure.
	DeliveryResultError = "error"
	// DeliveryResultDropped reports delivery work that was discarded by local validation.
	DeliveryResultDropped = "dropped"
	// DeliveryResultOverflow reports a full bounded queue whose admission wait expired.
	DeliveryResultOverflow = "overflow"
	// DeliveryResultMaxAttempts reports retry work that reached its attempt cap.
	DeliveryResultMaxAttempts = "max_attempts"

	// DeliveryErrorClassNone reports that no error was present.
	DeliveryErrorClassNone = "none"
	// DeliveryErrorClassRetryable reports a retryable fanout or push error.
	DeliveryErrorClassRetryable = "retryable"
	// DeliveryErrorClassRouteNotReady reports a cluster route that is not ready yet.
	DeliveryErrorClassRouteNotReady = "route_not_ready"
	// DeliveryErrorClassQueueFull reports bounded delivery retry queue overflow.
	DeliveryErrorClassQueueFull = "queue_full"
	// DeliveryErrorClassInvalidCursor reports a subscriber scan cursor error.
	DeliveryErrorClassInvalidCursor = "invalid_cursor"
	// DeliveryErrorClassCanceled reports context cancellation.
	DeliveryErrorClassCanceled = "canceled"
	// DeliveryErrorClassDeadline reports context deadline expiry.
	DeliveryErrorClassDeadline = "deadline"
	// DeliveryErrorClassError reports an unclassified delivery error.
	DeliveryErrorClassError = "error"

	// DeliveryFanoutPathLocal reports an in-process fanout task execution.
	DeliveryFanoutPathLocal = "local"
	// DeliveryFanoutPathRemote reports a fanout task forwarded to another authority node.
	DeliveryFanoutPathRemote = "remote"

	// ManagerEventAdmission reports manager queue admission decisions.
	ManagerEventAdmission = "admission"
	// ManagerEventTerminal reports terminal outcomes for accepted manager work.
	ManagerEventTerminal = "terminal"
)

// Observer receives synchronous delivery fanout observations.
type Observer interface {
	ObserveFanoutTask(FanoutTaskEvent)
	ObserveFanoutResolve(FanoutResolveEvent)
	ObserveFanoutPush(FanoutPushEvent)
}

// ManagerObserver receives bounded manager admission and terminal observations.
type ManagerObserver interface {
	ObserveManagerAdmission(ManagerAdmissionEvent)
	ObserveManagerTerminal(ManagerTerminalEvent)
}

// ManagerAdmissionEvent describes one manager admission decision.
type ManagerAdmissionEvent struct {
	// Result is ok, overflow, or error.
	Result string
	// QueueDepth is the current async manager queue depth after the decision.
	QueueDepth int
}

// ManagerTerminalEvent describes the final outcome for accepted manager work.
type ManagerTerminalEvent struct {
	// Result is ok, retryable, error, dropped, overflow, or max_attempts.
	Result string
	// ErrorClass is the normalized delivery error class.
	ErrorClass string
	// QueueDepth is the current async manager queue depth after the event.
	QueueDepth int
}

// FanoutTaskEvent describes one fanout task routing attempt.
type FanoutTaskEvent struct {
	// TargetNodeID is the node selected to run the fanout task.
	TargetNodeID uint64
	// PartitionID is the delivery partition associated with the task.
	PartitionID uint32
	// Path is local or remote task routing.
	Path string
	// Result is the outcome label for the task routing attempt.
	Result string
	// ErrorClass is the normalized error class for failed attempts.
	ErrorClass string
	// Duration is the wall-clock time spent routing or executing the task.
	Duration time.Duration
}

// FanoutResolveEvent describes one UID-to-route resolution attempt.
type FanoutResolveEvent struct {
	// ChannelType is the source channel type from the committed message envelope.
	ChannelType uint8
	// Result is the outcome label for the route resolution attempt.
	Result string
	// ErrorClass is the normalized error class for failed attempts.
	ErrorClass string
	// Duration is the wall-clock time spent resolving presence routes.
	Duration time.Duration
	// Pages is the number of UID pages represented by the event.
	Pages int
	// UIDs is the number of requested recipient UIDs.
	UIDs int
	// Routes is the number of resolved routes before owner grouping.
	Routes int
}

// FanoutPushEvent describes one owner-node push attempt.
type FanoutPushEvent struct {
	// OwnerNodeID is the recipient owner node selected for the push.
	OwnerNodeID uint64
	// Result is the outcome label for the push attempt.
	Result string
	// ErrorClass is the normalized error class for failed attempts.
	ErrorClass string
	// Duration is the wall-clock time spent in the push port.
	Duration time.Duration
	// Routes is the number of routes included in the push command.
	Routes int
	// Accepted is the number of routes accepted by the owner node.
	Accepted int
	// Retryable is the number of routes that need retry scheduling.
	Retryable int
	// Dropped is the number of routes dropped by the owner node.
	Dropped int
}

// DeliveryErrorClass normalizes delivery errors into bounded labels.
func DeliveryErrorClass(err error) string {
	switch {
	case err == nil:
		return DeliveryErrorClassNone
	case errors.Is(err, ErrRetryQueueFull):
		return DeliveryErrorClassQueueFull
	case errors.Is(err, ErrRouteNotReady):
		return DeliveryErrorClassRouteNotReady
	case errors.Is(err, ErrRetryableFanoutTask), errors.Is(err, ErrRetryablePushRoutes):
		return DeliveryErrorClassRetryable
	case errors.Is(err, ErrInvalidSubscriberCursor):
		return DeliveryErrorClassInvalidCursor
	case errors.Is(err, context.Canceled):
		return DeliveryErrorClassCanceled
	case errors.Is(err, context.DeadlineExceeded):
		return DeliveryErrorClassDeadline
	default:
		return DeliveryErrorClassError
	}
}

// IsRetryableDeliveryError reports whether delivery should retry the failed work later.
func IsRetryableDeliveryError(err error) bool {
	if errors.Is(err, ErrRetryQueueFull) {
		return false
	}
	return errors.Is(err, ErrRouteNotReady) ||
		errors.Is(err, ErrRetryableFanoutTask) ||
		errors.Is(err, ErrRetryablePushRoutes)
}

func deliveryResultForError(err error) string {
	if err == nil {
		return DeliveryResultOK
	}
	if IsRetryableDeliveryError(err) {
		return DeliveryResultRetryable
	}
	return DeliveryResultError
}
